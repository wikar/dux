package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/danielwikar/dux/executor"
	"github.com/danielwikar/dux/internal/bootstrap"
	"github.com/danielwikar/dux/internal/ducklake"
	"github.com/danielwikar/dux/semantic"
)

func TestOpenAPISpecIsValidJSON(t *testing.T) {
	var document map[string]any
	if err := json.Unmarshal([]byte(openAPISpec), &document); err != nil {
		t.Fatal(err)
	}
	paths := document["paths"].(map[string]any)
	for _, path := range []string{"/query", "/values", "/api/ducklake/status", "/api/ducklake/imports", "/api/ducklake/imports/{id}", "/api/ducklake/maintenance", "/api/ducklake/maintenance/{id}"} {
		if _, ok := paths[path]; !ok {
			t.Fatalf("OpenAPI path %s missing", path)
		}
	}
}

func TestExplicitEmptyImportDirectoryIsDistinguishableFromDefault(t *testing.T) {
	var value optionalPathFlag
	if value.set {
		t.Fatal("unset path flag reported explicit value")
	}
	if err := value.Set(""); err != nil {
		t.Fatal(err)
	}
	if !value.set || value.value != "" {
		t.Fatalf("explicit empty path = %#v", value)
	}
}

// An empty result set must still marshal columns and rows as arrays: clients
// (the dashboard visuals among them) read rows as an array, and JSON null there
// breaks every one of them.
func TestEmptyQueryResultMarshalsAsArrays(t *testing.T) {
	data, err := json.Marshal(newQueryResponse(nil, nil))
	if err != nil {
		t.Fatal(err)
	}
	if got := string(data); got != `{"columns":[],"rows":[]}` {
		t.Errorf("empty result marshalled as %s", got)
	}
}

func TestBodyLimit(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/query", strings.NewReader(strings.Repeat("x", maxRequestBodyBytes+1)))
	_, err := readBody(httptest.NewRecorder(), req)
	if err == nil || bodyErrorStatus(err) != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413 body error, got %v", err)
	}
}

func TestDuckLakeStatusDoesNotExposeAbsolutePaths(t *testing.T) {
	dir := t.TempDir()
	runtime, err := bootstrap.Bootstrap(dir, filepath.Join(dir, "dux.sqlite"), filepath.Join(dir, "ducklake.sqlite"), filepath.Join(dir, "ducklake"), filepath.Join(dir, "missing.toml"), true)
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	service, err := ducklake.NewService(ducklake.ServiceConfig{Owner: runtime.Owner, Metadata: runtime.Metadata.DB(), DataPath: runtime.DataPath, MaintenanceTimeout: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	recorder := httptest.NewRecorder()
	ducklakeStatusHandler(runtime, service, ducklakeSchedule{})(recorder, httptest.NewRequest(http.MethodGet, "/api/ducklake/status", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", recorder.Code, recorder.Body.String())
	}
	if strings.Contains(recorder.Body.String(), filepath.ToSlash(dir)) || strings.Contains(recorder.Body.String(), dir) {
		t.Fatalf("status exposed absolute path: %s", recorder.Body.String())
	}
	var status map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if status["health"] != "healthy" || status["ducklakeFormatVersion"] != "1.0" || status["importsEnabled"] != false {
		t.Fatalf("ducklake status = %#v", status)
	}
	if err := runtime.Metadata.SaveRelationship("missing_fact", "id", "missing_dimension", "id", false); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.RefreshSchema(); err != nil {
		t.Fatal(err)
	}
	recorder = httptest.NewRecorder()
	ducklakeStatusHandler(runtime, service, ducklakeSchedule{})(recorder, httptest.NewRequest(http.MethodGet, "/api/ducklake/status", nil))
	if err := json.Unmarshal(recorder.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if status["health"] != "degraded" || !strings.Contains(status["schemaWarning"].(string), "missing_fact") {
		t.Fatalf("degraded ducklake status = %#v", status)
	}
	recorder = httptest.NewRecorder()
	maintenanceCollectionHandler(service)(recorder, httptest.NewRequest(http.MethodPost, "/api/ducklake/maintenance", strings.NewReader(`{"operation":"checkpoint"}`)))
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("maintenance status = %d: %s", recorder.Code, recorder.Body.String())
	}
	var job ducklake.Job
	if err := json.Unmarshal(recorder.Body.Bytes(), &job); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if current, ok := service.Job(job.ID); ok && current.Status != "running" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if current, ok := service.Job(job.ID); !ok || current.Status != "succeeded" {
		t.Fatalf("maintenance did not succeed: %#v", current)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/ducklake/maintenance/{id}", maintenanceJobHandler(service))
	recorder = httptest.NewRecorder()
	mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/ducklake/maintenance/missing", nil))
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("missing job status = %d", recorder.Code)
	}
}

func TestDuckLakeHandlersRejectUnsafeAndOversizedRequests(t *testing.T) {
	dir := t.TempDir()
	runtime, err := bootstrap.Bootstrap(dir, filepath.Join(dir, "dux.sqlite"), filepath.Join(dir, "ducklake.sqlite"), filepath.Join(dir, "ducklake"), filepath.Join(dir, "missing.toml"), true)
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	service, err := ducklake.NewService(ducklake.ServiceConfig{Owner: runtime.Owner, Metadata: runtime.Metadata.DB(), DataPath: runtime.DataPath})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	maintenance := maintenanceCollectionHandler(service)
	for _, body := range []string{`{"operation":"vacuum"}`, `{"operation":"compact","sql":"DROP TABLE x"}`} {
		recorder := httptest.NewRecorder()
		maintenance(recorder, httptest.NewRequest(http.MethodPost, "/api/ducklake/maintenance", strings.NewReader(body)))
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("maintenance body %s status = %d: %s", body, recorder.Code, recorder.Body.String())
		}
	}
	recorder := httptest.NewRecorder()
	maintenance(recorder, httptest.NewRequest(http.MethodPost, "/api/ducklake/maintenance", strings.NewReader(strings.Repeat("x", maxRequestBodyBytes+1))))
	if recorder.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized maintenance status = %d", recorder.Code)
	}
	imports := importCollectionHandler(service)
	recorder = httptest.NewRecorder()
	imports(recorder, httptest.NewRequest(http.MethodPost, "/api/ducklake/imports", strings.NewReader(`{"table":"x","files":["x.parquet"]}`)))
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("disabled import status = %d: %s", recorder.Code, recorder.Body.String())
	}
	service.Close()
	inbox := filepath.Join(dir, "inbox")
	enabled, err := ducklake.NewService(ducklake.ServiceConfig{Owner: runtime.Owner, Metadata: runtime.Metadata.DB(), DataPath: runtime.DataPath, ImportPath: inbox})
	if err != nil {
		t.Fatal(err)
	}
	defer enabled.Close()
	imports = importCollectionHandler(enabled)
	for _, test := range []struct {
		body, key string
	}{
		{`{"table":"x","files":["../x.parquet"]}`, "unsafe"},
		{`{"table":"x","files":["x.parquet"],"sql":"DROP TABLE x"}`, "unknown"},
		{`{"table":"x","files":["x.parquet"]}`, ""},
	} {
		recorder = httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/api/ducklake/imports", strings.NewReader(test.body))
		request.Header.Set("Idempotency-Key", test.key)
		imports(recorder, request)
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("import body %s status = %d: %s", test.body, recorder.Code, recorder.Body.String())
		}
	}
	parquet := filepath.ToSlash(filepath.Join(inbox, "x.parquet"))
	if _, err := runtime.Owner.Conn().ExecContext(t.Context(), `COPY (SELECT 1::INTEGER id) TO '`+parquet+`' (FORMAT PARQUET)`); err != nil {
		t.Fatal(err)
	}
	fileBytes, err := os.ReadFile(filepath.FromSlash(parquet))
	if err != nil {
		t.Fatal(err)
	}
	recorder = httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/ducklake/imports", strings.NewReader(`{"table":"x","files":["x.parquet"],"createIfMissing":true}`))
	request.Header.Set("Idempotency-Key", "valid")
	imports(recorder, request)
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("valid import status = %d: %s", recorder.Code, recorder.Body.String())
	}
	var accepted ducklake.Job
	if err := json.Unmarshal(recorder.Body.Bytes(), &accepted); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if current, ok := enabled.Import(accepted.ID); ok && current.Status != "running" {
			accepted = current
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if accepted.Status != "succeeded" {
		t.Fatalf("accepted import = %#v", accepted)
	}
	if err := os.WriteFile(filepath.Join(inbox, "duplicate.parquet"), fileBytes, 0o644); err != nil {
		t.Fatal(err)
	}
	recorder = httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodPost, "/api/ducklake/imports", strings.NewReader(`{"table":"x","files":["duplicate.parquet"]}`))
	request.Header.Set("Idempotency-Key", "duplicate")
	imports(recorder, request)
	if recorder.Code != http.StatusConflict || !strings.Contains(recorder.Body.String(), accepted.ID) {
		t.Fatalf("duplicate import status = %d: %s", recorder.Code, recorder.Body.String())
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/ducklake/imports/{id}", importJobHandler(enabled))
	recorder = httptest.NewRecorder()
	mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/ducklake/imports/missing", nil))
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("missing import status = %d", recorder.Code)
	}
}

func TestDuplicateImportContentMapsToConflict(t *testing.T) {
	recorder := httptest.NewRecorder()
	writeDuckLakeOperationError(recorder, fmt.Errorf("%w: prior import abc", ducklake.ErrDuplicateContent))
	if recorder.Code != http.StatusConflict || !strings.Contains(recorder.Body.String(), "abc") {
		t.Fatalf("duplicate response = %d %s", recorder.Code, recorder.Body.String())
	}
}

func TestDuckLakeOperationErrorStatus(t *testing.T) {
	tests := []struct {
		err  error
		want int
	}{
		{fmt.Errorf("%w: bad input", ducklake.ErrInvalidRequest), http.StatusBadRequest},
		{context.DeadlineExceeded, http.StatusGatewayTimeout},
		{fmt.Errorf("%w: inspect file: %w", ducklake.ErrInvalidRequest, context.DeadlineExceeded), http.StatusGatewayTimeout},
		{errors.New("catalog unavailable"), http.StatusInternalServerError},
	}
	for _, test := range tests {
		recorder := httptest.NewRecorder()
		writeDuckLakeOperationError(recorder, test.err)
		if recorder.Code != test.want {
			t.Fatalf("error %v mapped to %d, want %d", test.err, recorder.Code, test.want)
		}
	}
}

func TestValuesHandlerUsesPhysicalTableName(t *testing.T) {
	db, err := sql.Open("duckdb", "")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`ATTACH ':memory:' AS ducklake; CREATE TABLE ducklake.main.Product(Category VARCHAR); INSERT INTO ducklake.main.Product VALUES ('Water'), ('Coffee')`); err != nil {
		t.Fatal(err)
	}
	schema := semantic.NewSchema()
	schema.Tables["Product"] = &semantic.Table{Name: "Product", SQLName: "ducklake.main.Product", Columns: map[string]*semantic.Column{"Category": {Name: "Category", DataType: "VARCHAR"}}}
	recorder := httptest.NewRecorder()
	valuesHandler(db, schema, &sync.RWMutex{})(recorder, httptest.NewRequest(http.MethodGet, "/values?table=Product&column=Category", nil))
	if recorder.Code != http.StatusOK || recorder.Body.String() != `["Coffee","Water"]`+"\n" {
		t.Fatalf("values response = %d %s", recorder.Code, recorder.Body.String())
	}
}

func TestSchemaMonitorIgnoresDataCommitsAndDetectsDDL(t *testing.T) {
	if catalog := os.Getenv("DUX_SCHEMA_MONITOR_CHILD_CATALOG"); catalog != "" {
		runSchemaMonitorChild(t, catalog, os.Getenv("DUX_SCHEMA_MONITOR_CHILD_STATEMENT"))
		return
	}
	dir := t.TempDir()
	runtime, err := bootstrap.Bootstrap(dir, filepath.Join(dir, "dux.sqlite"), filepath.Join(dir, "ducklake.sqlite"), filepath.Join(dir, "ducklake"), filepath.Join(dir, "missing.toml"), true)
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	if _, err := runtime.Owner.Conn().ExecContext(t.Context(), `CREATE TABLE events(id INTEGER)`); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.RefreshSchema(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	defer func() {
		cancel()
		<-done
	}()
	refreshed := make(chan struct{}, 2)
	go func() {
		defer close(done)
		monitorDuckLakeSchema(ctx, runtime, 50*time.Millisecond, func() {
			_, _ = runtime.RefreshSchema()
			refreshed <- struct{}{}
		})
	}()
	time.Sleep(75 * time.Millisecond)
	if err := execDuckLakeChildWithRetry(t.Context(), runtime.CatalogPath, `INSERT INTO events VALUES (1)`); err != nil {
		t.Fatal(err)
	}
	select {
	case <-refreshed:
		t.Fatal("data-only commit triggered schema refresh")
	case <-time.After(100 * time.Millisecond):
	}
	if err := execDuckLakeChildWithRetry(t.Context(), runtime.CatalogPath, `CREATE TABLE new_table(id INTEGER)`); err != nil {
		t.Fatal(err)
	}
	select {
	case <-refreshed:
	case <-time.After(2 * time.Second):
		t.Fatal("DDL did not trigger schema refresh")
	}
}

func execDuckLakeChildWithRetry(ctx context.Context, catalog, statement string) error {
	deadline := time.Now().Add(20 * time.Second)
	for {
		command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestSchemaMonitorIgnoresDataCommitsAndDetectsDDL$")
		command.Env = append(os.Environ(), "DUX_SCHEMA_MONITOR_CHILD_CATALOG="+catalog, "DUX_SCHEMA_MONITOR_CHILD_STATEMENT="+statement)
		output, err := command.CombinedOutput()
		if err == nil {
			return nil
		}
		if !strings.Contains(strings.ToLower(string(output)), "database is locked") || time.Now().After(deadline) {
			return fmt.Errorf("pipeline child: %w: %s", err, output)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func runSchemaMonitorChild(t *testing.T, catalog, statement string) {
	db, err := sql.Open("duckdb", "")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	conn, err := db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	for _, extension := range []string{"sqlite", "ducklake"} {
		if _, err := conn.ExecContext(t.Context(), "LOAD "+extension); err != nil {
			t.Fatal(err)
		}
	}
	uri := "ducklake:sqlite:" + filepath.ToSlash(catalog)
	if _, err := conn.ExecContext(t.Context(), "ATTACH '"+strings.ReplaceAll(uri, "'", "''")+"' AS ducklake"); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(t.Context(), "USE ducklake"); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(t.Context(), statement); err != nil {
		t.Fatal(err)
	}
}

func TestMergeRelationshipsDeduplicates(t *testing.T) {
	r := &semantic.Relationship{FromTable: "a", FromColumn: "id", ToTable: "b", ToColumn: "a_id"}
	schema := semantic.NewSchema()
	schema.Relationships = []*semantic.Relationship{r}
	mergeRelationships(schema, []*semantic.Relationship{r})
	if len(schema.Relationships) != 1 {
		t.Fatalf("expected one relationship, got %d", len(schema.Relationships))
	}
}

func TestReplaceSchemaIncludesMeasureFormats(t *testing.T) {
	dst, src := semantic.NewSchema(), semantic.NewSchema()
	src.SetMeasureFormat("sales", "Revenue", &semantic.MeasureFormat{Kind: "currency", Currency: "SEK"})
	replaceSchema(dst, src)
	if got := dst.MeasureFormatFor("sales", "Revenue"); got == nil || got.Currency != "SEK" {
		t.Fatalf("measure format was not replaced: %#v", got)
	}
}

func TestClearSchemaMetadataRemovesMeasureFormats(t *testing.T) {
	schema := semantic.NewSchema()
	schema.SetMeasureFormat("sales", "Revenue", &semantic.MeasureFormat{Kind: "currency", Currency: "SEK"})
	clearSchemaMetadata(schema)
	if got := schema.MeasureFormatFor("sales", "Revenue"); got != nil {
		t.Fatalf("stale measure format survived metadata reset: %#v", got)
	}
}

func TestRejectedRelationshipUpdatePreservesStoredValue(t *testing.T) {
	metaDB, err := semantic.OpenMetadataDB(t.TempDir() + "/dux.sqlite")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = metaDB.Close() })

	rels := []*semantic.Relationship{
		{FromTable: "a", FromColumn: "id", ToTable: "b", ToColumn: "a_id"},
		{FromTable: "b", FromColumn: "id", ToTable: "c", ToColumn: "b_id"},
		{FromTable: "a", FromColumn: "id", ToTable: "c", ToColumn: "a_id"},
	}
	for _, r := range rels {
		if err := metaDB.SaveRelationship(r.FromTable, r.FromColumn, r.ToTable, r.ToColumn, false); err != nil {
			t.Fatal(err)
		}
	}
	schema := semantic.NewSchema()
	schema.Relationships = rels

	body := `{"from_table":"a","from_column":"id","to_table":"c","to_column":"a_id","bidirectional":true}`
	rec := httptest.NewRecorder()
	addRelationshipHandler(metaDB, schema, &sync.RWMutex{})(rec, httptest.NewRequest(http.MethodPost, "/relationships", strings.NewReader(body)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected rejected update, got %d: %s", rec.Code, rec.Body.String())
	}
	if schema.Relationships[2].Bidirectional {
		t.Fatal("in-memory relationship was not restored")
	}

	loaded := semantic.NewSchema()
	if err := metaDB.LoadIntoSchema(loaded); err != nil {
		t.Fatal(err)
	}
	if len(loaded.Relationships) != 3 || loaded.Relationships[2].Bidirectional {
		t.Fatalf("stored relationship was not preserved: %#v", loaded.Relationships)
	}
}

// A shed query must surface as 503 with Retry-After, not as a 400 alongside
// genuine query errors: clients retry the former and report the latter.
func TestQueryHandler_ServerBusyIs503(t *testing.T) {
	db, err := sql.Open("duckdb", "")
	if err != nil {
		t.Fatalf("open duckdb: %v", err)
	}
	defer db.Close()

	// Cap the pool at one connection and take it, so the handler must queue.
	db.SetMaxOpenConns(1)
	conn, err := db.Conn(context.Background())
	if err != nil {
		t.Fatalf("pin connection: %v", err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(context.Background(), `CREATE TABLE sales (id INTEGER)`); err != nil {
		t.Fatalf("seed table: %v", err)
	}

	defer func(d time.Duration) { executor.AdmissionTimeout = d }(executor.AdmissionTimeout)
	executor.AdmissionTimeout = 100 * time.Millisecond

	schema := &semantic.Schema{Tables: map[string]*semantic.Table{
		"sales": {Name: "sales", Columns: map[string]*semantic.Column{
			"id": {Name: "id", DataType: "INTEGER"},
		}},
	}}
	var mu sync.RWMutex

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/query", strings.NewReader(`EVALUATE sales`))
	queryHandler(db, schema, &mu)(recorder, request)

	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503; body %s", recorder.Code, recorder.Body.String())
	}
	if got := recorder.Header().Get("Retry-After"); got == "" {
		t.Error("missing Retry-After header on 503")
	}
	var body map[string]string
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if !strings.Contains(body["error"], "server busy") {
		t.Errorf("error = %q, want it to name the server-busy condition", body["error"])
	}
}

// Value pickers share the query pool, so they must shed the same way rather
// than blocking indefinitely on a full pool.
func TestValuesHandler_ServerBusyIs503(t *testing.T) {
	db, err := sql.Open("duckdb", "")
	if err != nil {
		t.Fatalf("open duckdb: %v", err)
	}
	defer db.Close()

	db.SetMaxOpenConns(1)
	conn, err := db.Conn(context.Background())
	if err != nil {
		t.Fatalf("pin connection: %v", err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(context.Background(), `CREATE TABLE sales (region VARCHAR)`); err != nil {
		t.Fatalf("seed table: %v", err)
	}

	defer func(d time.Duration) { executor.AdmissionTimeout = d }(executor.AdmissionTimeout)
	executor.AdmissionTimeout = 100 * time.Millisecond

	schema := &semantic.Schema{Tables: map[string]*semantic.Table{
		"sales": {Name: "sales", SQLName: "sales", Columns: map[string]*semantic.Column{
			"region": {Name: "region", DataType: "VARCHAR"},
		}},
	}}
	var mu sync.RWMutex

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/values?table=sales&column=region", nil)
	valuesHandler(db, schema, &mu)(recorder, request)

	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503; body %s", recorder.Code, recorder.Body.String())
	}
	if recorder.Header().Get("Retry-After") == "" {
		t.Error("missing Retry-After header on 503")
	}
}
