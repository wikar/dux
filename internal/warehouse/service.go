package warehouse

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

var ErrBusy = errors.New("warehouse ownership operation already running")
var ErrIdempotencyConflict = errors.New("idempotency key conflict")
var ErrDuplicateContent = errors.New("Parquet content was already imported")
var ErrImportsDisabled = errors.New("Parquet imports are disabled")
var ErrInvalidRequest = errors.New("invalid warehouse request")

func invalidRequest(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidRequest, fmt.Sprintf(format, args...))
}

type Job struct {
	ID             string          `json:"id"`
	Kind           string          `json:"kind"`
	Status         string          `json:"status"`
	RequestedAt    time.Time       `json:"requestedAt"`
	StartedAt      time.Time       `json:"startedAt"`
	FinishedAt     *time.Time      `json:"finishedAt,omitempty"`
	Error          string          `json:"error,omitempty"`
	Summary        json.RawMessage `json:"summary,omitempty"`
	Schema         string          `json:"schema,omitempty"`
	Table          string          `json:"table,omitempty"`
	IdempotencyKey string          `json:"idempotencyKey,omitempty"`
	Source         string          `json:"source,omitempty"`
	FileCount      int             `json:"fileCount,omitempty"`
}

type ImportRequest struct {
	Schema          string   `json:"schema"`
	Table           string   `json:"table"`
	Files           []string `json:"files"`
	CreateIfMissing bool     `json:"createIfMissing"`
}

type ServiceConfig struct {
	Owner              *Runtime
	Metadata           *sql.DB
	DataPath           string
	ImportPath         string
	MaintenanceTimeout time.Duration
	ImportTimeout      time.Duration
	MaxCompactions     int
	MaxImportFiles     int
	OrphanDelay        time.Duration
}

// Service serializes every operation owned by duxd. Direct pipelines still
// transact through DuckLake independently; this lock protects DUX maintenance
// and controlled imports from colliding with each other.
type Service struct {
	cfg      ServiceConfig
	ctx      context.Context
	cancel   context.CancelFunc
	wg       sync.WaitGroup
	mu       sync.Mutex
	activeMu sync.RWMutex
	active   string
	jobs     sync.Map
}

func NewService(cfg ServiceConfig) (*Service, error) {
	if cfg.Owner == nil || cfg.Owner.ReadOnly() {
		return nil, fmt.Errorf("read-write warehouse owner is required")
	}
	if cfg.Metadata == nil {
		return nil, fmt.Errorf("metadata database is required")
	}
	if cfg.MaintenanceTimeout <= 0 {
		cfg.MaintenanceTimeout = 30 * time.Minute
	}
	if cfg.ImportTimeout <= 0 {
		cfg.ImportTimeout = 30 * time.Minute
	}
	if cfg.MaxCompactions <= 0 {
		cfg.MaxCompactions = 10
	}
	if cfg.MaxImportFiles <= 0 {
		cfg.MaxImportFiles = 100
	}
	if cfg.OrphanDelay <= 0 {
		cfg.OrphanDelay = 24 * time.Hour
	}
	if cfg.ImportPath != "" {
		if err := os.MkdirAll(cfg.ImportPath, 0o755); err != nil {
			return nil, fmt.Errorf("create import directory: %w", err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	service := &Service{cfg: cfg, ctx: ctx, cancel: cancel}
	if err := service.recoverInterrupted(); err != nil {
		return nil, err
	}
	return service, nil
}

func (s *Service) ImportsEnabled() bool { return s.cfg.ImportPath != "" }

func (s *Service) Close() {
	if s == nil {
		return
	}
	s.cancel()
	s.mu.Lock()
	s.mu.Unlock()
	s.wg.Wait()
}

func (s *Service) recoverInterrupted() error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	message := "duxd restarted before the operation completed"
	if _, err := s.cfg.Metadata.Exec(`UPDATE dux_meta.dux_maintenance_runs SET status = 'failed', finished_at = ?, error = ? WHERE status = 'running'`, now, message); err != nil {
		return fmt.Errorf("recover maintenance jobs: %w", err)
	}
	rows, err := s.cfg.Metadata.Query(`SELECT id, schema_name, table_name FROM dux_meta.dux_imports WHERE status = 'running'`)
	if err != nil {
		return fmt.Errorf("list interrupted imports: %w", err)
	}
	type interruptedImport struct{ id, schema, table string }
	var interrupted []interruptedImport
	for rows.Next() {
		var item interruptedImport
		if err := rows.Scan(&item.id, &item.schema, &item.table); err != nil {
			rows.Close()
			return err
		}
		interrupted = append(interrupted, item)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, item := range interrupted {
		registered, err := s.importWasCommitted(item.id, item.schema, item.table)
		if err != nil {
			return fmt.Errorf("verify interrupted import %s: %w", item.id, err)
		}
		status, detail := "failed", message
		if registered {
			status, detail = "succeeded", ""
		} else {
			_, _ = s.cfg.Metadata.Exec(`DELETE FROM dux_meta.dux_import_files WHERE import_id = ?`, item.id)
		}
		if _, err := s.cfg.Metadata.Exec(`UPDATE dux_meta.dux_imports SET status = ?, finished_at = ?, error = ? WHERE id = ?`, status, now, nullable(detail), item.id); err != nil {
			return fmt.Errorf("recover import %s: %w", item.id, err)
		}
	}
	root := filepath.Join(s.cfg.DataPath, "_imports")
	cutoff := time.Now().Add(-s.cfg.OrphanDelay)
	_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() || !strings.HasSuffix(entry.Name(), ".partial") {
			return nil
		}
		info, err := entry.Info()
		if err == nil && info.ModTime().Before(cutoff) {
			_ = os.Remove(path)
		}
		return nil
	})
	return nil
}

func (s *Service) importWasCommitted(id, schema, table string) (bool, error) {
	rows, err := s.cfg.Metadata.Query(`SELECT target_path FROM dux_meta.dux_import_files WHERE import_id = ? ORDER BY target_path`, id)
	if err != nil {
		return false, err
	}
	var targets []string
	for rows.Next() {
		var target string
		if err := rows.Scan(&target); err != nil {
			rows.Close()
			return false, err
		}
		targets = append(targets, filepath.Join(s.cfg.DataPath, filepath.FromSlash(target)))
	}
	if err := rows.Close(); err != nil {
		return false, err
	}
	if len(targets) == 0 {
		return false, nil
	}
	stmt := fmt.Sprintf("SELECT data_file FROM ducklake_list_files(%s, %s, schema => %s)", sqlString(Alias), sqlString(table), sqlString(schema))
	fileRows, err := s.cfg.Owner.Conn().QueryContext(context.Background(), stmt)
	if err != nil {
		return false, err
	}
	defer fileRows.Close()
	registered := make(map[string]bool)
	for fileRows.Next() {
		var path string
		if err := fileRows.Scan(&path); err != nil {
			return false, err
		}
		absolute, _ := filepath.Abs(filepath.FromSlash(path))
		registered[normalPath(absolute)] = true
	}
	for _, target := range targets {
		absolute, _ := filepath.Abs(target)
		if !registered[normalPath(absolute)] {
			return false, nil
		}
	}
	return true, fileRows.Err()
}

func normalPath(path string) string {
	path = filepath.Clean(path)
	if filepath.Separator == '\\' {
		return strings.ToLower(path)
	}
	return path
}

func (s *Service) Job(id string) (Job, bool) {
	value, ok := s.jobs.Load(id)
	if !ok {
		return Job{}, false
	}
	return value.(Job), true
}

func (s *Service) Maintenance(id string) (Job, bool) {
	if job, ok := s.Job(id); ok && job.Kind != "import" {
		return job, true
	}
	var job Job
	var requested, started string
	var finished, summary, message sql.NullString
	job.ID = id
	err := s.cfg.Metadata.QueryRow(`SELECT operation, source, status, requested_at, started_at, finished_at, summary_json, error FROM dux_meta.dux_maintenance_runs WHERE id = ?`, id).Scan(&job.Kind, &job.Source, &job.Status, &requested, &started, &finished, &summary, &message)
	if err != nil {
		return Job{}, false
	}
	job.RequestedAt, _ = time.Parse(time.RFC3339Nano, requested)
	job.StartedAt, _ = time.Parse(time.RFC3339Nano, started)
	if finished.Valid {
		value, _ := time.Parse(time.RFC3339Nano, finished.String)
		job.FinishedAt = &value
	}
	job.Error = message.String
	if summary.Valid {
		job.Summary = json.RawMessage(summary.String)
	}
	return job, true
}

func (s *Service) Active() string {
	s.activeMu.RLock()
	defer s.activeMu.RUnlock()
	return s.active
}

func (s *Service) setActive(id string) {
	s.activeMu.Lock()
	s.active = id
	s.activeMu.Unlock()
}

func (s *Service) release() {
	s.setActive("")
	s.mu.Unlock()
}

func (s *Service) Import(id string) (Job, bool) {
	if job, ok := s.Job(id); ok && job.Kind == "import" {
		return job, true
	}
	job, err := s.loadImport(id)
	return job, err == nil
}

func (s *Service) MaintenanceRuns(limit int) ([]Job, error) {
	if limit <= 0 || limit > 100 {
		limit = 25
	}
	rows, err := s.cfg.Metadata.Query(`SELECT id, operation, source, status, requested_at, started_at, finished_at, summary_json, error FROM dux_meta.dux_maintenance_runs ORDER BY requested_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var jobs []Job
	for rows.Next() {
		var job Job
		var requested, started string
		var finished, summary, message sql.NullString
		if err := rows.Scan(&job.ID, &job.Kind, &job.Source, &job.Status, &requested, &started, &finished, &summary, &message); err != nil {
			return nil, err
		}
		job.RequestedAt, _ = time.Parse(time.RFC3339Nano, requested)
		job.StartedAt, _ = time.Parse(time.RFC3339Nano, started)
		if finished.Valid {
			value, _ := time.Parse(time.RFC3339Nano, finished.String)
			job.FinishedAt = &value
		}
		job.Error = message.String
		if summary.Valid {
			job.Summary = json.RawMessage(summary.String)
		}
		jobs = append(jobs, job)
	}
	return jobs, rows.Err()
}

func (s *Service) RecentImports(limit int) ([]Job, error) {
	if limit <= 0 || limit > 100 {
		limit = 5
	}
	rows, err := s.cfg.Metadata.Query(`SELECT id, status, requested_at, started_at, finished_at, summary_json, error, schema_name, table_name, idempotency_key, file_count FROM dux_meta.dux_imports ORDER BY requested_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var jobs []Job
	for rows.Next() {
		var job Job
		var requested, started string
		var finished, summary, message sql.NullString
		job.Kind = "import"
		if err := rows.Scan(&job.ID, &job.Status, &requested, &started, &finished, &summary, &message, &job.Schema, &job.Table, &job.IdempotencyKey, &job.FileCount); err != nil {
			return nil, err
		}
		job.RequestedAt, _ = time.Parse(time.RFC3339Nano, requested)
		job.StartedAt, _ = time.Parse(time.RFC3339Nano, started)
		if finished.Valid {
			value, _ := time.Parse(time.RFC3339Nano, finished.String)
			job.FinishedAt = &value
		}
		job.Error = message.String
		if summary.Valid {
			job.Summary = json.RawMessage(summary.String)
		}
		jobs = append(jobs, job)
	}
	return jobs, rows.Err()
}

func (s *Service) StartMaintenance(operation string) (Job, error) {
	return s.startMaintenance(operation, "api")
}

func (s *Service) StartScheduledMaintenance(operation string) (Job, error) {
	return s.startMaintenance(operation, "scheduled")
}

func (s *Service) startMaintenance(operation, trigger string) (Job, error) {
	if err := s.ctx.Err(); err != nil {
		return Job{}, fmt.Errorf("warehouse service is stopping: %w", err)
	}
	switch operation {
	case "compact", "checkpoint":
	default:
		return Job{}, invalidRequest("unsupported maintenance operation %q", operation)
	}
	if !s.mu.TryLock() {
		if trigger == "scheduled" {
			now := time.Now().UTC()
			job := Job{ID: newID(), Kind: operation, Source: trigger, Status: "skipped", RequestedAt: now, StartedAt: now, FinishedAt: &now, Error: "another warehouse ownership operation is active"}
			_, err := s.cfg.Metadata.Exec(`INSERT INTO dux_meta.dux_maintenance_runs (id, operation, source, status, requested_at, started_at, finished_at, error) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, job.ID, operation, trigger, job.Status, now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano), job.Error)
			log.Printf("warehouse maintenance %s skipped (%s): %s", operation, job.ID, job.Error)
			return job, err
		}
		return Job{}, fmt.Errorf("%w: active job %s", ErrBusy, s.Active())
	}
	now := time.Now().UTC()
	job := Job{ID: newID(), Kind: operation, Source: trigger, Status: "running", RequestedAt: now, StartedAt: now}
	s.setActive(job.ID)
	s.jobs.Store(job.ID, job)
	log.Printf("warehouse maintenance %s started (%s)", operation, job.ID)
	if _, err := s.cfg.Metadata.Exec(`INSERT INTO dux_meta.dux_maintenance_runs (id, operation, source, status, requested_at, started_at) VALUES (?, ?, ?, ?, ?, ?)`, job.ID, operation, trigger, job.Status, job.RequestedAt.Format(time.RFC3339Nano), job.StartedAt.Format(time.RFC3339Nano)); err != nil {
		s.jobs.Delete(job.ID)
		s.release()
		return Job{}, err
	}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		ctx, cancel := context.WithTimeout(s.ctx, s.cfg.MaintenanceTimeout)
		defer cancel()
		err := s.runMaintenance(ctx, operation)
		s.finishMaintenance(job, err)
		s.release()
	}()
	return job, nil
}

func (s *Service) LastScheduledMaintenance(operation string) (time.Time, bool) {
	var value string
	err := s.cfg.Metadata.QueryRow(`SELECT finished_at FROM dux_meta.dux_maintenance_runs WHERE operation = ? AND source = 'scheduled' AND status IN ('succeeded', 'failed', 'skipped') ORDER BY finished_at DESC LIMIT 1`, operation).Scan(&value)
	if err != nil {
		return time.Time{}, false
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	return parsed, err == nil
}

func (s *Service) runMaintenance(ctx context.Context, operation string) error {
	run := func(sql string) error {
		rows, err := s.cfg.Owner.Conn().QueryContext(ctx, sql)
		if err != nil {
			return err
		}
		return rows.Close()
	}
	if operation == "compact" {
		stmt := fmt.Sprintf(`CALL ducklake_merge_adjacent_files('warehouse', max_compacted_files => %d)`, s.cfg.MaxCompactions)
		if err := run(stmt); err != nil {
			return fmt.Errorf("compact: %w", err)
		}
	}
	if operation == "checkpoint" {
		if _, err := s.cfg.Owner.Conn().ExecContext(ctx, `CHECKPOINT`); err != nil {
			return fmt.Errorf("checkpoint: %w", err)
		}
	}
	return nil
}

func (s *Service) finishMaintenance(job Job, err error) {
	now := time.Now().UTC()
	job.FinishedAt = &now
	job.Status = "succeeded"
	if err != nil {
		job.Status, job.Error = "failed", boundedError(err)
	}
	if _, persistErr := s.cfg.Metadata.Exec(`UPDATE dux_meta.dux_maintenance_runs SET status = ?, finished_at = ?, error = ? WHERE id = ?`, job.Status, now.Format(time.RFC3339Nano), nullable(job.Error), job.ID); persistErr != nil {
		job.Status, job.Error = "failed", boundedError(fmt.Errorf("persist maintenance result: %w", persistErr))
	}
	if _, cleanupErr := s.cfg.Metadata.Exec(`DELETE FROM dux_meta.dux_maintenance_runs WHERE id NOT IN (SELECT id FROM dux_meta.dux_maintenance_runs ORDER BY requested_at DESC LIMIT 100)`); cleanupErr != nil {
		log.Printf("warning: trim warehouse maintenance history: %v", cleanupErr)
	}
	s.jobs.Store(job.ID, job)
	log.Printf("warehouse maintenance %s %s (%s)%s", job.Kind, job.Status, job.ID, logErrorSuffix(job.Error))
}

func (s *Service) StartImport(key string, request ImportRequest) (Job, error) {
	if err := s.ctx.Err(); err != nil {
		return Job{}, fmt.Errorf("warehouse service is stopping: %w", err)
	}
	if !s.ImportsEnabled() {
		return Job{}, ErrImportsDisabled
	}
	request.Schema = strings.TrimSpace(request.Schema)
	request.Table = strings.TrimSpace(request.Table)
	if request.Schema == "" {
		request.Schema = "main"
	}
	if request.Table == "" || len(request.Files) == 0 {
		return Job{}, invalidRequest("table and at least one file are required")
	}
	if len(request.Files) > s.cfg.MaxImportFiles {
		return Job{}, invalidRequest("import contains %d files; maximum is %d", len(request.Files), s.cfg.MaxImportFiles)
	}
	if !validIdentifier(request.Schema) || !validIdentifier(request.Table) {
		return Job{}, invalidRequest("schema and table must be simple DuckDB identifiers")
	}
	if key == "" {
		return Job{}, invalidRequest("Idempotency-Key header is required")
	}
	if len(key) > 256 {
		return Job{}, invalidRequest("Idempotency-Key must be at most 256 bytes")
	}
	seenFiles := make(map[string]bool, len(request.Files))
	for i, name := range request.Files {
		if name == "" || len(name) > 1024 {
			return Job{}, invalidRequest("import file paths must contain 1 to 1024 bytes")
		}
		normalized := filepath.ToSlash(filepath.Clean(name))
		duplicateKey := normalPath(normalized)
		if seenFiles[duplicateKey] {
			return Job{}, invalidRequest("import file %q is listed more than once", name)
		}
		seenFiles[duplicateKey] = true
		request.Files[i] = normalized
	}
	hashRequest := request
	hashRequest.Files = append([]string(nil), request.Files...)
	slices.Sort(hashRequest.Files)
	payload, _ := json.Marshal(hashRequest)
	digest := sha256.Sum256(payload)
	hash := hex.EncodeToString(digest[:])
	var existingID, existingHash string
	err := s.cfg.Metadata.QueryRow(`SELECT id, request_hash FROM dux_meta.dux_imports WHERE idempotency_key = ?`, key).Scan(&existingID, &existingHash)
	if err == nil {
		if existingHash != hash {
			return Job{}, fmt.Errorf("%w: key was already used for a different request", ErrIdempotencyConflict)
		}
		if job, ok := s.Job(existingID); ok {
			return job, nil
		}
		return s.loadImport(existingID)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return Job{}, err
	}
	for _, name := range request.Files {
		if _, err := safeImportPath(s.cfg.ImportPath, name); err != nil {
			return Job{}, invalidRequest("%v", err)
		}
	}
	if !s.mu.TryLock() {
		return Job{}, fmt.Errorf("%w: active job %s", ErrBusy, s.Active())
	}
	now := time.Now().UTC()
	job := Job{ID: newID(), Kind: "import", Status: "running", RequestedAt: now, StartedAt: now, Schema: request.Schema, Table: request.Table, IdempotencyKey: key, FileCount: len(request.Files)}
	s.setActive(job.ID)
	filesJSON, _ := json.Marshal(request.Files)
	_, err = s.cfg.Metadata.Exec(`INSERT INTO dux_meta.dux_imports (id, idempotency_key, request_hash, schema_name, table_name, status, create_if_missing, file_count, requested_at, started_at, files_json) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, job.ID, key, hash, request.Schema, request.Table, job.Status, request.CreateIfMissing, job.FileCount, job.RequestedAt.Format(time.RFC3339Nano), job.StartedAt.Format(time.RFC3339Nano), string(filesJSON))
	if err != nil {
		s.release()
		return Job{}, err
	}
	s.jobs.Store(job.ID, job)
	log.Printf("warehouse import %s.%s started (%s, %d files)", request.Schema, request.Table, job.ID, len(request.Files))
	deadline := job.RequestedAt.Add(s.cfg.ImportTimeout)
	stageContext, cancelStage := context.WithDeadline(s.ctx, deadline)
	copied, err := s.stageImport(stageContext, job, request)
	cancelStage()
	if err != nil {
		s.finishImport(job, nil, err)
		s.release()
		return Job{}, err
	}
	job.Summary = s.importSummary(copied)
	s.jobs.Store(job.ID, job)
	_, _ = s.cfg.Metadata.Exec(`UPDATE dux_meta.dux_imports SET summary_json = ? WHERE id = ?`, string(job.Summary), job.ID)
	log.Printf("warehouse import %s.%s validated and staged (%s)", request.Schema, request.Table, job.ID)
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		ctx, cancel := context.WithDeadline(s.ctx, deadline)
		defer cancel()
		summary, err := s.runImport(ctx, job, request, copied)
		s.finishImport(job, summary, err)
		s.release()
	}()
	return job, nil
}

type copiedFile struct {
	source, landing, target, hash string
	size, rows                    int64
	columns                       []fileColumn
}

type fileColumn struct {
	name, dataType string
}

type queryContext interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func (s *Service) stageImport(ctx context.Context, job Job, request ImportRequest) (copied []copiedFile, stageErr error) {
	root, err := filepath.Abs(s.cfg.ImportPath)
	if err != nil {
		return nil, err
	}
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, err
	}
	targetDir := filepath.Join(s.cfg.DataPath, "_imports", job.ID)
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		return nil, err
	}
	defer func() {
		if stageErr != nil {
			for _, file := range copied {
				_ = os.Remove(file.target)
			}
			_ = os.Remove(targetDir)
			_, _ = s.cfg.Metadata.Exec(`DELETE FROM dux_meta.dux_import_files WHERE import_id = ?`, job.ID)
		}
	}()
	seenHashes := make(map[string]bool, len(request.Files))
	for i, name := range request.Files {
		source, err := safeImportPath(resolvedRoot, name)
		if err != nil {
			return copied, err
		}
		target := filepath.Join(targetDir, fmt.Sprintf("%03d-%s", i, filepath.Base(source)))
		temporary := target + ".partial"
		file, err := copyAndHash(ctx, source, temporary)
		if err != nil {
			_ = os.Remove(temporary)
			return nil, err
		}
		if err := os.Rename(temporary, target); err != nil {
			_ = os.Remove(temporary)
			return copied, fmt.Errorf("publish warehouse copy: %w", err)
		}
		file.source, file.landing, file.target = name, source, target
		copied = append(copied, file)
		if seenHashes[file.hash] {
			return copied, invalidRequest("import contains duplicate Parquet content in %q", name)
		}
		seenHashes[file.hash] = true
		var priorID string
		err = s.cfg.Metadata.QueryRow(`SELECT import_id FROM dux_meta.dux_import_files WHERE schema_name = ? AND table_name = ? AND sha256 = ? LIMIT 1`, request.Schema, request.Table, file.hash).Scan(&priorID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return copied, err
		}
		if err == nil {
			return copied, fmt.Errorf("%w: file %q was registered by import %s for %s.%s", ErrDuplicateContent, name, priorID, request.Schema, request.Table)
		}
	}
	var exists int
	if err := s.cfg.Owner.Conn().QueryRowContext(ctx, `SELECT count(*) FROM information_schema.tables WHERE table_catalog = 'warehouse' AND table_schema = ? AND table_name = ?`, request.Schema, request.Table).Scan(&exists); err != nil {
		return copied, err
	}
	var expected []fileColumn
	if exists != 0 {
		expected, err = tableColumns(ctx, s.cfg.Owner.Conn(), request.Schema, request.Table)
		if err != nil {
			return copied, err
		}
	} else if !request.CreateIfMissing {
		return copied, invalidRequest("table %s.%s does not exist", request.Schema, request.Table)
	}
	for i := range copied {
		columns, err := parquetColumns(ctx, s.cfg.Owner.Conn(), copied[i].target)
		if err != nil {
			return copied, fmt.Errorf("%w: inspect %q: %w", ErrInvalidRequest, copied[i].source, err)
		}
		if exists == 0 && i == 0 {
			expected = columns
		}
		if err := equalColumns(expected, columns); err != nil {
			return copied, invalidRequest("schema mismatch in %q: %v", copied[i].source, err)
		}
		copied[i].columns = columns
		if err := s.cfg.Owner.Conn().QueryRowContext(ctx, `SELECT count(*) FROM read_parquet(`+sqlString(filepath.ToSlash(copied[i].target))+`)`).Scan(&copied[i].rows); err != nil {
			return copied, fmt.Errorf("count rows in %q: %w", copied[i].source, err)
		}
	}
	for _, file := range copied {
		targetReceipt, err := filepath.Rel(s.cfg.DataPath, file.target)
		if err != nil {
			return copied, fmt.Errorf("record imported file path: %w", err)
		}
		if _, err := s.cfg.Metadata.Exec(`INSERT INTO dux_meta.dux_import_files (import_id, schema_name, table_name, source_path, target_path, sha256, size_bytes, row_count) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, job.ID, request.Schema, request.Table, file.source, filepath.ToSlash(targetReceipt), file.hash, file.size, file.rows); err != nil {
			return copied, fmt.Errorf("record staged import file: %w", err)
		}
	}
	return copied, nil
}

func (s *Service) runImport(ctx context.Context, job Job, request ImportRequest, copied []copiedFile) (summary json.RawMessage, runErr error) {
	committed := false
	targetDir := filepath.Join(s.cfg.DataPath, "_imports", job.ID)
	defer func() {
		if runErr != nil && !committed {
			for _, file := range copied {
				_ = os.Remove(file.target)
			}
			_ = os.Remove(targetDir)
			_, _ = s.cfg.Metadata.Exec(`DELETE FROM dux_meta.dux_import_files WHERE import_id = ?`, job.ID)
		}
	}()
	tx, err := s.cfg.Owner.Conn().BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var exists int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM information_schema.tables WHERE table_catalog = 'warehouse' AND table_schema = ? AND table_name = ?`, request.Schema, request.Table).Scan(&exists); err != nil {
		return nil, err
	}
	var expected []fileColumn
	if exists != 0 {
		expected, err = tableColumns(ctx, tx, request.Schema, request.Table)
		if err != nil {
			return nil, err
		}
	} else if !request.CreateIfMissing {
		return nil, fmt.Errorf("table %s.%s no longer exists", request.Schema, request.Table)
	} else {
		expected = copied[0].columns
	}
	for _, file := range copied {
		if err := equalColumns(expected, file.columns); err != nil {
			return nil, fmt.Errorf("target schema changed while importing %q: %w", file.source, err)
		}
	}
	physical := quoteIdent(Alias) + "." + quoteIdent(request.Schema) + "." + quoteIdent(request.Table)
	if exists == 0 {
		if _, err := tx.ExecContext(ctx, `CREATE SCHEMA IF NOT EXISTS `+quoteIdent(Alias)+`.`+quoteIdent(request.Schema)); err != nil {
			return nil, err
		}
		if _, err := tx.ExecContext(ctx, `CREATE TABLE `+physical+` AS SELECT * FROM read_parquet(`+sqlString(filepath.ToSlash(copied[0].target))+`) LIMIT 0`); err != nil {
			return nil, fmt.Errorf("create table from Parquet schema: %w", err)
		}
	}
	for _, file := range copied {
		stmt := fmt.Sprintf("CALL ducklake_add_data_files(%s, %s, %s, schema => %s)", sqlString(Alias), sqlString(request.Table), sqlString(filepath.ToSlash(file.target)), sqlString(request.Schema))
		rows, err := tx.QueryContext(ctx, stmt)
		if err != nil {
			return nil, fmt.Errorf("register %q: %w", file.source, err)
		}
		if err := rows.Close(); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	committed = true
	for _, file := range copied {
		if err := os.Remove(file.landing); err != nil && !os.IsNotExist(err) {
			log.Printf("warning: remove imported landing file %q: %v", file.source, err)
		}
	}
	return s.importSummary(copied), nil
}

func (s *Service) importSummary(copied []copiedFile) json.RawMessage {
	type importedFile struct {
		Source    string `json:"source"`
		FileID    string `json:"fileId"`
		SHA256    string `json:"sha256"`
		SizeBytes int64  `json:"sizeBytes"`
		RowCount  int64  `json:"rowCount"`
	}
	result := struct {
		FileCount int            `json:"fileCount"`
		RowCount  int64          `json:"rowCount"`
		Files     []importedFile `json:"files"`
	}{FileCount: len(copied)}
	for _, file := range copied {
		fileID, _ := filepath.Rel(s.cfg.DataPath, file.target)
		result.RowCount += file.rows
		result.Files = append(result.Files, importedFile{file.source, filepath.ToSlash(fileID), file.hash, file.size, file.rows})
	}
	summary, _ := json.Marshal(result)
	return summary
}

func parquetColumns(ctx context.Context, query queryContext, path string) ([]fileColumn, error) {
	rows, err := query.QueryContext(ctx, `DESCRIBE SELECT * FROM read_parquet(`+sqlString(filepath.ToSlash(path))+`)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var columns []fileColumn
	for rows.Next() {
		var column fileColumn
		var nullable, key, defaultValue, extra sql.NullString
		if err := rows.Scan(&column.name, &column.dataType, &nullable, &key, &defaultValue, &extra); err != nil {
			return nil, err
		}
		columns = append(columns, column)
	}
	return columns, rows.Err()
}

func tableColumns(ctx context.Context, query queryContext, schema, table string) ([]fileColumn, error) {
	rows, err := query.QueryContext(ctx, `SELECT column_name, data_type FROM information_schema.columns WHERE table_catalog = 'warehouse' AND table_schema = ? AND table_name = ? ORDER BY ordinal_position`, schema, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var columns []fileColumn
	for rows.Next() {
		var column fileColumn
		if err := rows.Scan(&column.name, &column.dataType); err != nil {
			return nil, err
		}
		columns = append(columns, column)
	}
	return columns, rows.Err()
}

func equalColumns(expected, actual []fileColumn) error {
	if len(expected) != len(actual) {
		return fmt.Errorf("got %d columns, want %d", len(actual), len(expected))
	}
	for i := range expected {
		if expected[i].name != actual[i].name || !strings.EqualFold(expected[i].dataType, actual[i].dataType) {
			return fmt.Errorf("column %d is %s %s, want %s %s", i+1, actual[i].name, actual[i].dataType, expected[i].name, expected[i].dataType)
		}
	}
	return nil
}

func (s *Service) finishImport(job Job, summary json.RawMessage, err error) {
	now := time.Now().UTC()
	job.FinishedAt, job.Status = &now, "succeeded"
	if err != nil {
		job.Status, job.Error = "failed", boundedError(err)
	} else {
		job.Summary = summary
	}
	if _, persistErr := s.cfg.Metadata.Exec(`UPDATE dux_meta.dux_imports SET status = ?, finished_at = ?, summary_json = ?, error = ? WHERE id = ?`, job.Status, now.Format(time.RFC3339Nano), nullable(string(job.Summary)), nullable(job.Error), job.ID); persistErr != nil {
		job.Status, job.Error = "failed", boundedError(fmt.Errorf("persist import result: %w", persistErr))
	}
	s.jobs.Store(job.ID, job)
	log.Printf("warehouse import %s.%s %s (%s)%s", job.Schema, job.Table, job.Status, job.ID, logErrorSuffix(job.Error))
}

func (s *Service) loadImport(id string) (Job, error) {
	var job Job
	var requested, started string
	var finished, summary, message sql.NullString
	job.ID, job.Kind = id, "import"
	err := s.cfg.Metadata.QueryRow(`SELECT status, requested_at, started_at, finished_at, summary_json, error, schema_name, table_name, idempotency_key, file_count FROM dux_meta.dux_imports WHERE id = ?`, id).Scan(&job.Status, &requested, &started, &finished, &summary, &message, &job.Schema, &job.Table, &job.IdempotencyKey, &job.FileCount)
	if err != nil {
		return Job{}, err
	}
	job.RequestedAt, _ = time.Parse(time.RFC3339Nano, requested)
	job.StartedAt, _ = time.Parse(time.RFC3339Nano, started)
	if finished.Valid {
		value, _ := time.Parse(time.RFC3339Nano, finished.String)
		job.FinishedAt = &value
	}
	job.Error = message.String
	if summary.Valid {
		job.Summary = json.RawMessage(summary.String)
	}
	return job, nil
}

func safeImportPath(root, name string) (string, error) {
	if filepath.IsAbs(name) || strings.ToLower(filepath.Ext(name)) != ".parquet" {
		return "", fmt.Errorf("import file %q must be a relative .parquet path", name)
	}
	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolve import directory: %w", err)
	}
	candidate := filepath.Join(absoluteRoot, filepath.Clean(name))
	rel, err := filepath.Rel(absoluteRoot, candidate)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("import file %q escapes the import directory", name)
	}
	resolved, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		return "", fmt.Errorf("resolve import file %q: %w", name, err)
	}
	if normalPath(candidate) != normalPath(resolved) {
		return "", fmt.Errorf("import file %q uses a symlink or reparse point", name)
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.Mode().IsRegular() {
		return "", fmt.Errorf("import file %q is not a regular file", name)
	}
	return resolved, nil
}

func copyAndHash(ctx context.Context, source, target string) (copiedFile, error) {
	before, err := os.Stat(source)
	if err != nil {
		return copiedFile{}, err
	}
	in, err := os.Open(source)
	if err != nil {
		return copiedFile{}, err
	}
	defer in.Close()
	opened, err := in.Stat()
	if err != nil || !os.SameFile(before, opened) {
		return copiedFile{}, fmt.Errorf("source changed before it could be copied")
	}
	out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return copiedFile{}, err
	}
	hash := sha256.New()
	size, copyErr := io.Copy(io.MultiWriter(out, hash), contextReader{ctx: ctx, reader: in})
	if copyErr == nil {
		copyErr = out.Sync()
	}
	closeErr := out.Close()
	if copyErr != nil {
		return copiedFile{}, copyErr
	}
	if closeErr != nil {
		return copiedFile{}, closeErr
	}
	after, err := os.Stat(source)
	if err != nil || !os.SameFile(opened, after) || opened.Size() != after.Size() || !opened.ModTime().Equal(after.ModTime()) {
		return copiedFile{}, fmt.Errorf("source changed while it was being copied")
	}
	return copiedFile{target: target, hash: hex.EncodeToString(hash.Sum(nil)), size: size}, nil
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextReader) Read(buffer []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(buffer)
}

func quoteIdent(value string) string { return `"` + strings.ReplaceAll(value, `"`, `""`) + `"` }

func validIdentifier(value string) bool {
	if value == "" {
		return false
	}
	for i, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || r == '_' || (i > 0 && r >= '0' && r <= '9') {
			continue
		}
		return false
	}
	return true
}

func nullable(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func boundedError(err error) string {
	const limit = 2048
	message := err.Error()
	if len(message) > limit {
		return message[:limit]
	}
	return message
}

func logErrorSuffix(message string) string {
	if message == "" {
		return ""
	}
	return ": " + message
}

func newID() string {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(value[:])
}
