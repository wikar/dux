package warehouse_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/danielwikar/dux/internal/warehouse"
	"github.com/danielwikar/dux/semantic"
)

func TestControlledImportCreatesAndRegistersTable(t *testing.T) {
	dir := t.TempDir()
	incoming := t.TempDir() // Deliberately outside the warehouse root, as a mounted landing directory would be.
	cfg := warehouse.Config{CatalogPath: filepath.Join(dir, "ducklake.sqlite"), DataPath: filepath.Join(dir, "data"), TimeTravelRetention: 7 * 24 * time.Hour, FileDeleteDelay: 24 * time.Hour}
	owner, err := warehouse.OpenOwner(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	parquet := filepath.ToSlash(filepath.Join(incoming, "sales.parquet"))
	if _, err := owner.Conn().ExecContext(t.Context(), `COPY (SELECT 1::BIGINT id, 12.5::DOUBLE amount) TO '`+parquet+`' (FORMAT PARQUET)`); err != nil {
		t.Fatal(err)
	}
	sourceBytes, err := os.ReadFile(filepath.FromSlash(parquet))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(incoming, "duplicate.parquet"), sourceBytes, 0o644); err != nil {
		t.Fatal(err)
	}
	meta, err := semantic.OpenMetadataDB(filepath.Join(dir, "dux.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer meta.Close()
	service, err := warehouse.NewService(warehouse.ServiceConfig{Owner: owner, Metadata: meta.DB(), DataPath: cfg.DataPath, ImportPath: incoming, ImportTimeout: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	job, err := service.StartImport("once", warehouse.ImportRequest{Table: "sales", Files: []string{"sales.parquet"}, CreateIfMissing: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(job.Summary), `"fileId":"_imports/`) {
		t.Fatalf("accepted import omitted staged receipt: %#v", job)
	}
	job = waitJob(t, service, job.ID)
	if job.Status != "succeeded" {
		t.Fatalf("import failed: %s", job.Error)
	}
	if !strings.Contains(string(job.Summary), `"rowCount":1`) || job.RequestedAt.IsZero() {
		t.Fatalf("import receipt = %#v", job)
	}
	var storedRows int64
	var targetPath, storedHash string
	if err := meta.DB().QueryRow(`SELECT row_count, target_path, sha256 FROM dux_meta.dux_import_files WHERE import_id = ?`, job.ID).Scan(&storedRows, &targetPath, &storedHash); err != nil || storedRows != 1 {
		t.Fatalf("stored receipt = rows %d path %q hash %q, %v", storedRows, targetPath, storedHash, err)
	}
	if want := fmt.Sprintf("%x", sha256.Sum256(sourceBytes)); storedHash != want {
		t.Fatalf("stored hash = %s, want %s", storedHash, want)
	}
	if warehouseBytes, err := os.ReadFile(filepath.Join(cfg.DataPath, filepath.FromSlash(targetPath))); err != nil || fmt.Sprintf("%x", sha256.Sum256(warehouseBytes)) != storedHash {
		t.Fatalf("warehouse copy changed: %v", err)
	}
	var registeredPath string
	if err := owner.Conn().QueryRowContext(t.Context(), `SELECT data_file FROM ducklake_list_files('warehouse', 'sales', schema => 'main')`).Scan(&registeredPath); err != nil || !strings.HasPrefix(strings.ToLower(filepath.Clean(filepath.FromSlash(registeredPath))), strings.ToLower(filepath.Clean(cfg.DataPath))) {
		t.Fatalf("registered path = %q, %v", registeredPath, err)
	}
	var count int
	if err := owner.Conn().QueryRowContext(context.Background(), `SELECT count(*) FROM warehouse.main.sales`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("count = %d", count)
	}
	if _, err := os.Stat(filepath.Join(incoming, "sales.parquet")); !os.IsNotExist(err) {
		t.Fatalf("successful import retained landing file: %v", err)
	}
	again, err := service.StartImport("once", warehouse.ImportRequest{Table: "sales", Files: []string{"sales.parquet"}, CreateIfMissing: true})
	if err != nil || again.ID != job.ID {
		t.Fatalf("idempotent result = %#v, %v", again, err)
	}
	if _, err := service.StartImport("once", warehouse.ImportRequest{Table: "other", Files: []string{"sales.parquet"}, CreateIfMissing: true}); !errors.Is(err, warehouse.ErrIdempotencyConflict) {
		t.Fatalf("conflicting idempotency key error = %v", err)
	}
	if _, err := service.StartImport("duplicate-content", warehouse.ImportRequest{Table: "sales", Files: []string{"duplicate.parquet"}}); !errors.Is(err, warehouse.ErrDuplicateContent) || !strings.Contains(err.Error(), job.ID) {
		t.Fatalf("duplicate content error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(incoming, "duplicate.parquet")); err != nil {
		t.Fatalf("duplicate failure removed source: %v", err)
	}
	if _, err := service.StartImport("duplicates", warehouse.ImportRequest{Table: "sales", Files: []string{"folder/../bad.parquet", "bad.parquet"}}); err == nil || !strings.Contains(err.Error(), "more than once") {
		t.Fatalf("normalized duplicate paths error = %v", err)
	}
	badParquet := filepath.ToSlash(filepath.Join(incoming, "bad.parquet"))
	if _, err := owner.Conn().ExecContext(t.Context(), `COPY (SELECT 2::BIGINT id, 'bad'::VARCHAR amount) TO '`+badParquet+`' (FORMAT PARQUET)`); err != nil {
		t.Fatal(err)
	}
	if _, err := service.StartImport("bad-schema", warehouse.ImportRequest{Table: "sales", Files: []string{"bad.parquet"}}); err == nil || !strings.Contains(err.Error(), "schema mismatch") {
		t.Fatalf("bad schema error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(incoming, "bad.parquet")); err != nil {
		t.Fatalf("failed import removed source: %v", err)
	}
	if _, err := service.StartImport("missing-target", warehouse.ImportRequest{Table: "missing", Files: []string{"bad.parquet"}}); err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("missing target error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(incoming, "malformed.parquet"), []byte("not parquet"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := service.StartImport("malformed", warehouse.ImportRequest{Table: "malformed", Files: []string{"malformed.parquet"}, CreateIfMissing: true}); err == nil {
		t.Fatal("malformed Parquet was accepted")
	}
	for name, query := range map[string]string{
		"multi-a.parquet": `SELECT 1::BIGINT id`,
		"multi-b.parquet": `SELECT 'one'::VARCHAR id`,
	} {
		path := filepath.ToSlash(filepath.Join(incoming, name))
		if _, err := owner.Conn().ExecContext(t.Context(), `COPY (`+query+`) TO '`+path+`' (FORMAT PARQUET)`); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := service.StartImport("multi-schema", warehouse.ImportRequest{Table: "multi", Files: []string{"multi-a.parquet", "multi-b.parquet"}, CreateIfMissing: true}); err == nil || !strings.Contains(err.Error(), "schema mismatch") {
		t.Fatalf("multi-schema error = %v", err)
	}
	var multiTables int
	if err := owner.Conn().QueryRowContext(t.Context(), `SELECT count(*) FROM information_schema.tables WHERE table_catalog = 'warehouse' AND table_name = 'multi'`).Scan(&multiTables); err != nil || multiTables != 0 {
		t.Fatalf("multi-schema table count = %d, %v", multiTables, err)
	}
	tooMany := make([]string, 101)
	if _, err := service.StartImport("many", warehouse.ImportRequest{Table: "sales", Files: tooMany}); err == nil {
		t.Fatal("oversized import manifest accepted")
	}
	service.Close()
	if _, err := meta.DB().Exec(`UPDATE dux_meta.dux_imports SET status = 'running', finished_at = NULL WHERE id = ?`, job.ID); err != nil {
		t.Fatal(err)
	}
	unrelatedLanding := []byte("new file with a reused name")
	if err := os.WriteFile(filepath.Join(incoming, "sales.parquet"), unrelatedLanding, 0o644); err != nil {
		t.Fatal(err)
	}
	recovered, err := warehouse.NewService(warehouse.ServiceConfig{Owner: owner, Metadata: meta.DB(), DataPath: cfg.DataPath, ImportPath: incoming, ImportTimeout: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	defer recovered.Close()
	if recoveredJob, ok := recovered.Import(job.ID); !ok || recoveredJob.Status != "succeeded" {
		t.Fatalf("committed import recovery = %#v, %v", recoveredJob, ok)
	}
	if got, err := os.ReadFile(filepath.Join(incoming, "sales.parquet")); err != nil || !slices.Equal(got, unrelatedLanding) {
		t.Fatalf("recovery removed or changed reused landing path: %q, %v", got, err)
	}
	recovered.Close()
	started := time.Now().UTC().Format(time.RFC3339Nano)
	interruptedSource := filepath.Join(incoming, "interrupted.parquet")
	if err := os.WriteFile(interruptedSource, sourceBytes, 0o644); err != nil {
		t.Fatal(err)
	}
	interruptedTarget := filepath.Join(cfg.DataPath, "_imports", "interrupted", "interrupted.parquet")
	if err := os.MkdirAll(filepath.Dir(interruptedTarget), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(interruptedTarget, sourceBytes, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := meta.DB().Exec(`INSERT INTO dux_meta.dux_imports (id, idempotency_key, request_hash, schema_name, table_name, status, create_if_missing, file_count, requested_at, started_at, files_json) VALUES ('interrupted', 'interrupted', 'hash', 'main', 'sales', 'running', false, 1, ?, ?, '["interrupted.parquet"]')`, started, started); err != nil {
		t.Fatal(err)
	}
	if _, err := meta.DB().Exec(`INSERT INTO dux_meta.dux_import_files (import_id, schema_name, table_name, source_path, target_path, sha256, size_bytes, row_count) VALUES ('interrupted', 'main', 'sales', 'interrupted.parquet', '_imports/interrupted/interrupted.parquet', 'interrupted-hash', 1, 0)`); err != nil {
		t.Fatal(err)
	}
	orphanDir := filepath.Join(cfg.DataPath, "_imports", "orphans")
	if err := os.MkdirAll(orphanDir, 0o755); err != nil {
		t.Fatal(err)
	}
	oldPartial, recentPartial := filepath.Join(orphanDir, "old.partial"), filepath.Join(orphanDir, "recent.partial")
	if err := os.WriteFile(oldPartial, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(recentPartial, []byte("recent"), 0o644); err != nil {
		t.Fatal(err)
	}
	oldTime := time.Now().Add(-25 * time.Hour)
	if err := os.Chtimes(oldPartial, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}
	recovered, err = warehouse.NewService(warehouse.ServiceConfig{Owner: owner, Metadata: meta.DB(), DataPath: cfg.DataPath, ImportPath: incoming, ImportTimeout: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	defer recovered.Close()
	if recoveredJob, ok := recovered.Import("interrupted"); !ok || recoveredJob.Status != "failed" {
		t.Fatalf("uncommitted import recovery = %#v, %v", recoveredJob, ok)
	}
	var receiptCount int
	if err := meta.DB().QueryRow(`SELECT count(*) FROM dux_meta.dux_import_files WHERE import_id = 'interrupted'`).Scan(&receiptCount); err != nil || receiptCount != 0 {
		t.Fatalf("uncommitted receipts = %d, %v", receiptCount, err)
	}
	if _, err := os.Stat(interruptedTarget); err != nil {
		t.Fatalf("bounded orphan removed prematurely: %v", err)
	}
	if _, err := os.Stat(interruptedSource); err != nil {
		t.Fatalf("interrupted import removed source: %v", err)
	}
	if _, err := os.Stat(oldPartial); !os.IsNotExist(err) {
		t.Fatalf("expired partial retained: %v", err)
	}
	if _, err := os.Stat(recentPartial); err != nil {
		t.Fatalf("recent partial removed: %v", err)
	}
}

func TestMaintenanceJob(t *testing.T) {
	dir := t.TempDir()
	cfg := warehouse.Config{CatalogPath: filepath.Join(dir, "ducklake.sqlite"), DataPath: filepath.Join(dir, "data"), TimeTravelRetention: 7 * 24 * time.Hour, FileDeleteDelay: 24 * time.Hour}
	owner, err := warehouse.OpenOwner(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	meta, err := semantic.OpenMetadataDB(filepath.Join(dir, "dux.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer meta.Close()
	service, err := warehouse.NewService(warehouse.ServiceConfig{Owner: owner, Metadata: meta.DB(), DataPath: cfg.DataPath, MaintenanceTimeout: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	job, err := service.StartMaintenance("checkpoint")
	if err != nil {
		t.Fatal(err)
	}
	job = waitJob(t, service, job.ID)
	if job.Status != "succeeded" {
		t.Fatalf("maintenance failed: %s", job.Error)
	}
	job, err = service.StartMaintenance("compact")
	if err != nil {
		t.Fatal(err)
	}
	job = waitJob(t, service, job.ID)
	if job.Status != "succeeded" {
		t.Fatalf("compaction failed: %s", job.Error)
	}
}

func TestImportsCanBeDisabled(t *testing.T) {
	dir := t.TempDir()
	cfg := warehouse.Config{CatalogPath: filepath.Join(dir, "ducklake.sqlite"), DataPath: filepath.Join(dir, "data"), TimeTravelRetention: 7 * 24 * time.Hour, FileDeleteDelay: 24 * time.Hour}
	owner, err := warehouse.OpenOwner(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	meta, err := semantic.OpenMetadataDB(filepath.Join(dir, "dux.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer meta.Close()
	service, err := warehouse.NewService(warehouse.ServiceConfig{Owner: owner, Metadata: meta.DB(), DataPath: cfg.DataPath})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	if service.ImportsEnabled() {
		t.Fatal("imports unexpectedly enabled")
	}
	if _, err := service.StartImport("disabled", warehouse.ImportRequest{Table: "sales", Files: []string{"sales.parquet"}}); !errors.Is(err, warehouse.ErrImportsDisabled) {
		t.Fatalf("disabled import error = %v", err)
	}
}

func TestImportReleasesOwnershipWorkerAfterCommit(t *testing.T) {
	dir := t.TempDir()
	incoming := filepath.Join(dir, "incoming")
	if err := os.MkdirAll(incoming, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := warehouse.Config{CatalogPath: filepath.Join(dir, "ducklake.sqlite"), DataPath: filepath.Join(dir, "data"), TimeTravelRetention: 7 * 24 * time.Hour, FileDeleteDelay: 24 * time.Hour}
	owner, err := warehouse.OpenOwner(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	parquet := filepath.ToSlash(filepath.Join(incoming, "batch.parquet"))
	if _, err := owner.Conn().ExecContext(t.Context(), `COPY (SELECT 1::BIGINT id) TO '`+parquet+`' (FORMAT PARQUET)`); err != nil {
		t.Fatal(err)
	}
	meta, err := semantic.OpenMetadataDB(filepath.Join(dir, "dux.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer meta.Close()
	service, err := warehouse.NewService(warehouse.ServiceConfig{
		Owner: owner, Metadata: meta.DB(), DataPath: cfg.DataPath, ImportPath: incoming,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	job, err := service.StartImport("busy", warehouse.ImportRequest{Table: "busy", Files: []string{"batch.parquet"}, CreateIfMissing: true})
	if err != nil {
		t.Fatal(err)
	}
	if done := waitJob(t, service, job.ID); done.Status != "succeeded" {
		t.Fatalf("import result = %#v", done)
	}
	maintenance, err := service.StartMaintenance("checkpoint")
	if err != nil {
		t.Fatalf("ownership worker remained busy after import: %v", err)
	}
	if done := waitJob(t, service, maintenance.ID); done.Status != "succeeded" {
		t.Fatalf("maintenance result = %#v", done)
	}
}

func TestRecoveryLeavesAmbiguousImportUntouched(t *testing.T) {
	dir := t.TempDir()
	cfg := warehouse.Config{CatalogPath: filepath.Join(dir, "ducklake.sqlite"), DataPath: filepath.Join(dir, "data"), TimeTravelRetention: 7 * 24 * time.Hour, FileDeleteDelay: 24 * time.Hour}
	owner, err := warehouse.OpenOwner(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	meta, err := semantic.OpenMetadataDB(filepath.Join(dir, "dux.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer meta.Close()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := meta.DB().Exec(`INSERT INTO dux_meta.dux_imports (id, idempotency_key, request_hash, schema_name, table_name, status, create_if_missing, file_count, requested_at, started_at, files_json) VALUES ('ambiguous', 'ambiguous', 'hash', 'main', 'sales', 'running', false, 1, ?, ?, '["sales.parquet"]')`, now, now); err != nil {
		t.Fatal(err)
	}
	if _, err := meta.DB().Exec(`INSERT INTO dux_meta.dux_import_files (import_id, schema_name, table_name, source_path, target_path, sha256, size_bytes, row_count) VALUES ('ambiguous', 'main', 'sales', 'sales.parquet', '_imports/ambiguous/sales.parquet', 'hash', 1, 1)`); err != nil {
		t.Fatal(err)
	}
	owner.Close()
	if _, err := warehouse.NewService(warehouse.ServiceConfig{Owner: owner, Metadata: meta.DB(), DataPath: cfg.DataPath, ImportPath: filepath.Join(dir, "incoming")}); err == nil || !strings.Contains(err.Error(), "verify interrupted import") {
		t.Fatalf("ambiguous recovery error = %v", err)
	}
	var status string
	var receipts int
	if err := meta.DB().QueryRow(`SELECT status FROM dux_meta.dux_imports WHERE id = 'ambiguous'`).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if err := meta.DB().QueryRow(`SELECT count(*) FROM dux_meta.dux_import_files WHERE import_id = 'ambiguous'`).Scan(&receipts); err != nil {
		t.Fatal(err)
	}
	if status != "running" || receipts != 1 {
		t.Fatalf("ambiguous recovery mutated status=%q receipts=%d", status, receipts)
	}
}

func TestMaintenanceHistoryIsBoundedAndSchedulesFromCompletion(t *testing.T) {
	dir := t.TempDir()
	cfg := warehouse.Config{CatalogPath: filepath.Join(dir, "ducklake.sqlite"), DataPath: filepath.Join(dir, "data"), TimeTravelRetention: 7 * 24 * time.Hour, FileDeleteDelay: 24 * time.Hour}
	owner, err := warehouse.OpenOwner(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	meta, err := semantic.OpenMetadataDB(filepath.Join(dir, "dux.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer meta.Close()
	base := time.Now().UTC().Add(-time.Hour)
	for i := 0; i < 101; i++ {
		stamp := base.Add(time.Duration(i) * time.Second).Format(time.RFC3339Nano)
		if _, err := meta.DB().Exec(`INSERT INTO dux_meta.dux_maintenance_runs (id, operation, source, status, requested_at, started_at, finished_at) VALUES (?, 'compact', 'scheduled', 'succeeded', ?, ?, ?)`, fmt.Sprintf("old-%03d", i), stamp, stamp, stamp); err != nil {
			t.Fatal(err)
		}
	}
	service, err := warehouse.NewService(warehouse.ServiceConfig{Owner: owner, Metadata: meta.DB(), DataPath: cfg.DataPath})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	want := base.Add(100 * time.Second)
	if got, ok := service.LastScheduledMaintenance("compact"); !ok || !got.Equal(want) {
		t.Fatalf("last scheduled completion = %v, %v; want %v", got, ok, want)
	}
	job, err := service.StartMaintenance("checkpoint")
	if err != nil {
		t.Fatal(err)
	}
	if done := waitJob(t, service, job.ID); done.Status != "succeeded" {
		t.Fatalf("maintenance = %#v", done)
	}
	var count int
	if err := meta.DB().QueryRow(`SELECT count(*) FROM dux_meta.dux_maintenance_runs`).Scan(&count); err != nil || count != 100 {
		t.Fatalf("maintenance history count = %d, %v", count, err)
	}
}

func TestCheckpointExpiresHistoryWithoutDeletingCurrentRows(t *testing.T) {
	dir := t.TempDir()
	cfg := warehouse.Config{CatalogPath: filepath.Join(dir, "ducklake.sqlite"), DataPath: filepath.Join(dir, "data"), TimeTravelRetention: time.Millisecond, FileDeleteDelay: time.Millisecond}
	owner, err := warehouse.OpenOwner(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	if _, err := owner.Conn().ExecContext(t.Context(), `CREATE TABLE facts(id INTEGER); INSERT INTO facts VALUES (1); INSERT INTO facts VALUES (2)`); err != nil {
		t.Fatal(err)
	}
	var before int
	if err := owner.Conn().QueryRowContext(t.Context(), `SELECT count(*) FROM warehouse.snapshots()`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	meta, err := semantic.OpenMetadataDB(filepath.Join(dir, "dux.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer meta.Close()
	service, err := warehouse.NewService(warehouse.ServiceConfig{Owner: owner, Metadata: meta.DB(), DataPath: cfg.DataPath})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	job, err := service.StartMaintenance("checkpoint")
	if err != nil {
		t.Fatal(err)
	}
	if done := waitJob(t, service, job.ID); done.Status != "succeeded" {
		t.Fatalf("checkpoint = %#v", done)
	}
	var after, rows int
	if err := owner.Conn().QueryRowContext(t.Context(), `SELECT count(*) FROM warehouse.snapshots()`).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if err := owner.Conn().QueryRowContext(t.Context(), `SELECT count(*) FROM facts`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if after >= before || rows != 2 {
		t.Fatalf("snapshots before=%d after=%d, current rows=%d", before, after, rows)
	}
}

func TestOperationTimeoutsAreRecorded(t *testing.T) {
	dir := t.TempDir()
	incoming := filepath.Join(dir, "incoming")
	if err := os.MkdirAll(incoming, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := warehouse.Config{CatalogPath: filepath.Join(dir, "ducklake.sqlite"), DataPath: filepath.Join(dir, "data"), TimeTravelRetention: 7 * 24 * time.Hour, FileDeleteDelay: 24 * time.Hour}
	owner, err := warehouse.OpenOwner(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	parquet := filepath.ToSlash(filepath.Join(incoming, "timeout.parquet"))
	if _, err := owner.Conn().ExecContext(t.Context(), `COPY (SELECT 1::INTEGER id) TO '`+parquet+`' (FORMAT PARQUET)`); err != nil {
		t.Fatal(err)
	}
	meta, err := semantic.OpenMetadataDB(filepath.Join(dir, "dux.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer meta.Close()
	service, err := warehouse.NewService(warehouse.ServiceConfig{
		Owner: owner, Metadata: meta.DB(), DataPath: cfg.DataPath, ImportPath: incoming,
		MaintenanceTimeout: time.Nanosecond, ImportTimeout: time.Nanosecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	maintenance, err := service.StartMaintenance("checkpoint")
	if err != nil {
		t.Fatal(err)
	}
	if maintenance = waitJob(t, service, maintenance.ID); maintenance.Status != "failed" || !strings.Contains(strings.ToLower(maintenance.Error), "context") {
		t.Fatalf("timed-out maintenance = %#v", maintenance)
	}
	if _, err := service.StartImport("timeout", warehouse.ImportRequest{Table: "timeout", Files: []string{"timeout.parquet"}, CreateIfMissing: true}); err == nil {
		t.Fatal("expired import context succeeded")
	}
	imports, err := service.RecentImports(1)
	if err != nil || len(imports) != 1 || imports[0].Status != "failed" {
		t.Fatalf("timed-out import history = %#v, %v", imports, err)
	}
	if _, err := os.Stat(filepath.FromSlash(parquet)); err != nil {
		t.Fatalf("timed-out import removed source: %v", err)
	}
}

func waitJob(t *testing.T, service *warehouse.Service, id string) warehouse.Job {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		job, ok := service.Job(id)
		if ok && job.Status != "running" {
			return job
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("job timed out")
	return warehouse.Job{}
}
