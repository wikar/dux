// Command duxd is the DUX long-running query server.
//
// It embeds DuckDB in-process and exposes endpoints on :80:
//
//	POST /query   — accepts a DUX query string (or a JSON envelope with
//	                external filters), returns a JSON result set
//	GET  /version — server version and API capability flags
//	GET  /schema  — returns tables, columns, and relationships as JSON
//	GET  /export  — exports measures and relationships as TOML
//	POST /import  — imports a dux.toml body, updates the metadata DB
//	POST /refresh — re-introspects DuckLake and reloads metadata
//	GET  /docs    — Scalar API reference UI
//	GET  /        — DUX UI (builder, explorer, dashboards at /dash/;
//	                /api/dash/ backend, DUX_DASH=0 disables dashboards)
//
// Usage:
//
//	duxd [--db-dir <dir>] [--dux <dux.sqlite>] [--toml <dux.toml>]
//	     [--import <file>] [--export <file>]
package main

import (
	"bytes"
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	_ "github.com/duckdb/duckdb-go/v2"

	"github.com/danielwikar/dux/dash"
	"github.com/danielwikar/dux/executor"
	"github.com/danielwikar/dux/internal/bootstrap"
	"github.com/danielwikar/dux/internal/ducklake"
	"github.com/danielwikar/dux/parser"
	"github.com/danielwikar/dux/semantic"
	"github.com/danielwikar/dux/web"
)

// version is overridden at build time via -ldflags="-X main.version=..."
var version = "dev"

type optionalPathFlag struct {
	value string
	set   bool
}

func (f *optionalPathFlag) String() string { return f.value }
func (f *optionalPathFlag) Set(value string) error {
	f.value, f.set = value, true
	return nil
}

// Server flags (registered before bootstrap's flag.Parse).
// Setting the env var DUX_DASH=0 disables the dashboards module entirely.
var (
	listenAddr         = flag.String("listen", ":8080", "HTTP listen address")
	dashDir            = flag.String("dash-dir", "dashboards", "dashboard file store directory")
	importDir          optionalPathFlag
	importMaxFiles     = flag.Int("import-max-files", 100, "maximum Parquet files in one import manifest")
	importTimeout      = flag.Duration("import-timeout", 30*time.Minute, "maximum Parquet import runtime")
	schemaInterval     = flag.Duration("schema-refresh-interval", 30*time.Second, "DuckLake schema polling interval (0 disables)")
	compactInterval    = flag.Duration("maintenance-compact-interval", time.Hour, "scheduled DuckLake compaction interval (0 disables)")
	checkpointInterval = flag.Duration("maintenance-checkpoint-interval", 24*time.Hour, "scheduled DuckLake checkpoint interval (0 disables)")
	maintenanceTimeout = flag.Duration("maintenance-timeout", 30*time.Minute, "maximum maintenance runtime")
	maxCompactions     = flag.Int("maintenance-max-compactions", 10, "maximum files compacted per maintenance call")
)

func init() {
	flag.Var(&importDir, "import-dir", "controlled Parquet inbox (default: inbox/ alongside <db-dir>; empty disables imports)")
}

const maxRequestBodyBytes = 4 << 20

//go:embed openapi.json
var openAPISpec string

// docsHTML is a minimal Scalar API reference page served at GET /docs.
const docsHTML = `<!doctype html><title>DUX Query API</title><div id="app"></div><script src="https://cdn.jsdelivr.net/npm/@scalar/api-reference"></script><script>Scalar.createApiReference('#app', {url: '/openapi.json'})</script>`

const usage = `duxd — DUX long-running query server

Usage:
  duxd [flags]

Endpoints served on :80:
  POST /query          Accept a raw DUX query string, return a JSON result set
  GET  /schema         Return tables, columns, and relationships as JSON
  GET  /export         Export measures and relationships as dux.toml
  POST /import         Import a dux.toml body into the metadata database
  GET  /measures       List all measures
  POST /measures       Add a measure
  DELETE /measures/{table}/{name}  Delete a measure
  GET  /relationships  List all relationships
  POST /relationships  Add a relationship
  DELETE /relationships  Delete a relationship
  POST /datetable      Designate the model's date table
  DELETE /datetable    Clear the date-table designation
  POST /hidden         Mark a table, view, or column as hidden
  DELETE /hidden       Clear a hidden designation
  POST /refresh        Refresh schema from DuckLake
  GET  /docs           Scalar interactive API reference
  GET  /               DUX UI (builder, explorer, and dashboards at /dash/)
  *    /api/dash/      Dashboards API (documents, assets, theme, schema);
                       disable dashboards with DUX_DASH=0

Flags:
`

func main() {
	runtime := bootstrap.Startup("duxd", version, usage, false, true)
	defer runtime.Close()
	metaDB, db, schema := runtime.Metadata, runtime.DB(), runtime.Schema

	// Schema is shared between HTTP handlers; protect mutations with a mutex.
	var schemaMu sync.RWMutex
	refreshSchema := func() {
		schemaMu.Lock()
		defer schemaMu.Unlock()
		fresh, err := runtime.RefreshSchema()
		if err != nil {
			log.Printf("warning: automatic schema refresh: %v", err)
			return
		}
		replaceSchema(schema, fresh)
		log.Printf("schema refreshed — %d tables, %d relationships", len(fresh.Tables), len(fresh.Relationships))
		if _, _, warning := runtime.RefreshStatus(); warning != "" {
			log.Printf("warning: semantic model degraded after schema refresh: %s", warning)
		}
	}
	resolvedImportDir := importDir.value
	if !importDir.set {
		// A sibling of the state directory, never a child: the catalog and data
		// files under --db-dir require a native local filesystem, while the
		// inbox is explicitly allowed to be a mount or host bridge. Nesting the
		// inbox inside --db-dir would force one filesystem choice on both.
		resolvedImportDir = filepath.Join(filepath.Dir(filepath.Clean(runtime.DBDir)), "inbox")
	}
	ducklakeService, err := ducklake.NewService(ducklake.ServiceConfig{
		Owner: runtime.Owner, Metadata: metaDB.DB(), DataPath: runtime.DataPath,
		ImportPath: resolvedImportDir, MaintenanceTimeout: *maintenanceTimeout, ImportTimeout: *importTimeout,
		MaxCompactions: *maxCompactions, MaxImportFiles: *importMaxFiles,
		OrphanDelay: runtime.Owner.FileDeleteDelay(),
	})
	if err != nil {
		log.Fatalf("DuckLake service: %v", err)
	}
	defer ducklakeService.Close()
	schedule := ducklakeSchedule{SchemaRefresh: *schemaInterval, Compact: *compactInterval, Checkpoint: *checkpointInterval, StartedAt: time.Now().UTC()}
	backgroundCtx, cancelBackground := context.WithCancel(context.Background())
	defer cancelBackground()
	go monitorDuckLakeSchema(backgroundCtx, runtime, *schemaInterval, refreshSchema)
	go scheduleDuckLakeMaintenance(backgroundCtx, ducklakeService, "compact", *compactInterval)
	go scheduleDuckLakeMaintenance(backgroundCtx, ducklakeService, "checkpoint", *checkpointInterval)

	mux := http.NewServeMux()

	mux.HandleFunc("POST /query", queryHandler(db, schema, &schemaMu))
	mux.HandleFunc("GET /schema", schemaHandler(schema, &schemaMu))
	mux.HandleFunc("GET /values", valuesHandler(db, schema, &schemaMu))
	mux.HandleFunc("GET /export", exportHandler(schema, &schemaMu))
	mux.HandleFunc("POST /import", importHandler(metaDB, schema, &schemaMu))
	mux.HandleFunc("GET /measures", listMeasuresHandler(schema, &schemaMu))
	mux.HandleFunc("POST /measures", addMeasureHandler(metaDB, schema, &schemaMu))
	mux.HandleFunc("DELETE /measures/{table}/{name}", deleteMeasureHandler(metaDB, schema, &schemaMu))
	mux.HandleFunc("GET /relationships", listRelationshipsHandler(schema, &schemaMu))
	mux.HandleFunc("POST /relationships", addRelationshipHandler(metaDB, schema, &schemaMu))
	mux.HandleFunc("DELETE /relationships", deleteRelationshipHandler(metaDB, schema, &schemaMu))
	mux.HandleFunc("POST /datetable", setDateTableHandler(metaDB, schema, &schemaMu))
	mux.HandleFunc("DELETE /datetable", deleteDateTableHandler(metaDB, schema, &schemaMu))
	mux.HandleFunc("POST /hidden", hiddenHandler(metaDB, schema, &schemaMu, true))
	mux.HandleFunc("DELETE /hidden", hiddenHandler(metaDB, schema, &schemaMu, false))
	mux.HandleFunc("POST /refresh", refreshHandler(runtime, schema, &schemaMu))
	mux.HandleFunc("GET /api/ducklake/status", ducklakeStatusHandler(runtime, ducklakeService, schedule))
	mux.HandleFunc("GET /api/ducklake/maintenance", maintenanceCollectionHandler(ducklakeService))
	mux.HandleFunc("POST /api/ducklake/maintenance", maintenanceCollectionHandler(ducklakeService))
	mux.HandleFunc("GET /api/ducklake/maintenance/{id}", maintenanceJobHandler(ducklakeService))
	if ducklakeService.ImportsEnabled() {
		mux.HandleFunc("POST /api/ducklake/imports", importCollectionHandler(ducklakeService))
	} else {
		mux.HandleFunc("POST /api/ducklake/imports", func(w http.ResponseWriter, _ *http.Request) {
			writeError(w, "Parquet imports are disabled", http.StatusNotFound)
		})
	}
	mux.HandleFunc("GET /api/ducklake/imports/{id}", importJobHandler(ducklakeService))

	dashEnabled := os.Getenv("DUX_DASH") != "0"
	if dashEnabled {
		dash, err := dash.NewServer(dash.Config{Root: *dashDir})
		if err != nil {
			log.Fatalf("dashboards module: %v", err)
		}
		mux.Handle("/api/dash/", dash)
		log.Printf("dashboards enabled at /dash/ and /api/dash/ (dir %q)", *dashDir)
	} else {
		// Keep the API surface a hard 404 — without this the SPA catch-all
		// would answer /api/dash/* with index.html.
		mux.HandleFunc("/api/dash/", func(w http.ResponseWriter, r *http.Request) {
			writeError(w, "dashboards disabled (DUX_DASH=0)", http.StatusNotFound)
		})
		log.Printf("dashboards disabled (DUX_DASH=0)")
	}

	mux.HandleFunc("GET /version", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{
			"version": version,
			"capabilities": map[string]bool{
				"externalFilters":     true,
				"measureFormats":      true,
				"dashboards":          dashEnabled,
				"ducklake":            true,
				"ducklakeMaintenance": true,
				"parquetImport":       ducklakeService.ImportsEnabled(),
			},
		})
	})

	mux.HandleFunc("GET /openapi.json", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, openAPISpec)
	})
	mux.HandleFunc("GET /docs", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, docsHTML)
	})

	// Serve the DUX UI SPA embedded at build time. Client-side routes
	// (/explorer, /dash/<dashboard path>) fall back to index.html; the app
	// feature-gates its Dash tab via /version capabilities.
	distFS, err := fs.Sub(web.App, "app/dist")
	if err != nil {
		log.Fatalf("ui embed: %v", err)
	}
	mux.Handle("/", spaFileServer("/", distFS))

	log.Printf("duxd %s listening on %s (metadata: %s, DuckLake: %s)", version, *listenAddr, runtime.MetaPath, runtime.CatalogPath)
	server := &http.Server{Addr: *listenAddr, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	shutdown, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()
	go func() {
		<-shutdown.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := server.Shutdown(ctx); err != nil {
			log.Printf("warning: HTTP shutdown: %v", err)
		}
	}()
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}

// spaFileServer serves a single-page app from fsys under prefix: real files
// are served as-is, anything else (client-side routes) gets index.html.
func spaFileServer(prefix string, fsys fs.FS) http.Handler {
	files := http.StripPrefix(prefix, http.FileServerFS(fsys))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := path.Clean(strings.TrimPrefix(r.URL.Path, prefix))
		if name != "" && name != "." && name != "/" {
			if f, err := fsys.Open(strings.TrimPrefix(name, "/")); err == nil {
				_ = f.Close()
				files.ServeHTTP(w, r)
				return
			}
		}
		http.ServeFileFS(w, r, fsys, "index.html")
	})
}

func mergeRelationships(schema *semantic.Schema, incoming []*semantic.Relationship) {
	for _, candidate := range incoming {
		found := false
		for _, existing := range schema.Relationships {
			if existing.FromTable == candidate.FromTable && existing.FromColumn == candidate.FromColumn &&
				existing.ToTable == candidate.ToTable && existing.ToColumn == candidate.ToColumn {
				found = true
				break
			}
		}
		if !found {
			schema.Relationships = append(schema.Relationships, candidate)
		}
	}
}

func replaceSchema(dst, src *semantic.Schema) {
	*dst = *src
	dst.ApplyHiddenFlags()
}

func clearSchemaMetadata(schema *semantic.Schema) {
	schema.Relationships = nil
	schema.Measures = make(map[string]map[string]*parser.MeasureDefinition)
	schema.MeasureFormats = make(map[string]map[string]*semantic.MeasureFormat)
	schema.DateTables = make(map[string]string)
	schema.ClearHidden()
}

// queryResponse is the JSON shape returned by POST /query.
type queryResponse struct {
	Columns []string `json:"columns"`
	Rows    [][]any  `json:"rows"`
}

// newQueryResponse keeps the documented array shape for an empty result: a nil
// Go slice marshals to JSON null, and clients that legitimately treat rows as
// an array break on it.
func newQueryResponse(cols []string, rows [][]any) queryResponse {
	if cols == nil {
		cols = []string{}
	}
	if rows == nil {
		rows = [][]any{}
	}
	return queryResponse{Columns: cols, Rows: rows}
}

// writeJSON encodes v as JSON to w with the appropriate Content-Type.
func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("warning: encode response: %v", err)
	}
}

func writeError(w http.ResponseWriter, msg string, status int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// serveBusy reports load shedding as 503 and returns true when it handled err.
// Shared by every handler that borrows a DuckDB connection: no SQL ran, so the
// client can retry as-is, and Retry-After is the retry floor it honours.
func serveBusy(w http.ResponseWriter, err error) bool {
	if !errors.Is(err, executor.ErrServerBusy) {
		return false
	}
	w.Header().Set("Retry-After", "1")
	writeError(w, err.Error(), http.StatusServiceUnavailable)
	return true
}

func readBody(w http.ResponseWriter, r *http.Request) ([]byte, error) {
	return io.ReadAll(http.MaxBytesReader(w, r.Body, maxRequestBodyBytes))
}

func decodeJSONBody(w http.ResponseWriter, r *http.Request, v any) error {
	body, err := readBody(w, r)
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(v); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return errors.New("request body must contain one JSON object")
		}
		return err
	}
	return nil
}

func bodyErrorStatus(err error) int {
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		return http.StatusRequestEntityTooLarge
	}
	return http.StatusBadRequest
}

// queryRequest is the JSON body form of POST /query. The filters are applied
// to the query's outermost filter context (see executor.ApplyExternalFilters).
type queryRequest struct {
	Query   string                    `json:"query"`
	Filters []executor.ExternalFilter `json:"filters,omitempty"`
}

// queryHandler handles POST /query. The body is either a raw DUX query string
// or, with Content-Type application/json, a queryRequest envelope carrying
// external filters.
func queryHandler(db *sql.DB, schema *semantic.Schema, mu *sync.RWMutex) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, err := readBody(w, r)
		if err != nil {
			writeError(w, err.Error(), bodyErrorStatus(err))
			return
		}
		if len(body) == 0 {
			writeError(w, "empty query body", http.StatusBadRequest)
			return
		}

		query := string(body)
		var filters []executor.ExternalFilter
		if strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
			var req queryRequest
			decoder := json.NewDecoder(bytes.NewReader(body))
			decoder.UseNumber()
			if err := decoder.Decode(&req); err != nil {
				writeError(w, "invalid JSON body: "+err.Error(), http.StatusBadRequest)
				return
			}
			if strings.TrimSpace(req.Query) == "" {
				writeError(w, `"query" is required in the JSON body`, http.StatusBadRequest)
				return
			}
			query = req.Query
			filters = req.Filters
		}

		mu.RLock()
		cols, rows, err := executor.ExecuteFilteredContext(r.Context(), db, schema, query, filters)
		mu.RUnlock()
		if err != nil {
			if serveBusy(w, err) {
				return
			}
			// Structured pipeline errors carry a stage and source position;
			// serve them as JSON so the UI can mark the offending spot.
			var qe *executor.QueryError
			if errors.As(err, &qe) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"error":  qe.Message,
					"stage":  qe.Stage,
					"line":   qe.Line,
					"column": qe.Column,
				})
				return
			}
			writeError(w, err.Error(), http.StatusBadRequest)
			return
		}

		writeJSON(w, newQueryResponse(cols, rows))
	}
}

// valuesHandler serves GET /values?table=...&column=...&q=... — distinct
// values of a column for filter pickers, optionally narrowed by a
// case-insensitive substring match, capped at 50.
func valuesHandler(db *sql.DB, schema *semantic.Schema, mu *sync.RWMutex) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tableKey := r.URL.Query().Get("table")
		colName := r.URL.Query().Get("column")
		q := r.URL.Query().Get("q")
		if tableKey == "" || colName == "" {
			writeError(w, "table and column are required", http.StatusBadRequest)
			return
		}

		mu.RLock()
		defer mu.RUnlock()
		table, ok := schema.Tables[tableKey]
		var col *semantic.Column
		if ok {
			col = table.Columns[colName]
		}
		if !ok {
			writeError(w, fmt.Sprintf("unknown table %q", tableKey), http.StatusNotFound)
			return
		}
		if col == nil {
			writeError(w, fmt.Sprintf("unknown column %q in table %q", colName, tableKey), http.StatusNotFound)
			return
		}

		// No SQL ORDER BY: DuckDB's string sort validates UTF-8 and errors on
		// data files with mis-encoded text (hash DISTINCT and scans do not).
		// The result is sorted in Go instead.
		colSQL := quoteIdent(col.Name)
		physicalName := table.SQLName
		if physicalName == "" {
			physicalName = tableKey
		}
		query := fmt.Sprintf(
			"SELECT CAST(v AS VARCHAR) FROM (SELECT DISTINCT %s AS v FROM %s WHERE %s IS NOT NULL",
			colSQL, quoteTableKey(physicalName), colSQL)
		var args []any
		if q != "" {
			query += fmt.Sprintf(` AND CAST(%s AS VARCHAR) ILIKE ? ESCAPE '\'`, colSQL)
			args = append(args, "%"+escapeLike(q)+"%")
		}
		query += ") LIMIT 50"

		// Value pickers compete for the same small pool as /query, so they get
		// the same admission control and execution budget.
		conn, err := executor.Acquire(r.Context(), db)
		if err != nil {
			if serveBusy(w, err) {
				return
			}
			writeError(w, err.Error(), http.StatusInternalServerError)
			return
		}
		defer conn.Close()
		ctx, cancel := context.WithTimeout(r.Context(), executor.QueryTimeout)
		defer cancel()

		rows, err := conn.QueryContext(ctx, query, args...)
		if err != nil {
			writeError(w, err.Error(), http.StatusInternalServerError)
			return
		}
		defer rows.Close()

		values := []string{}
		for rows.Next() {
			var s string
			if err := rows.Scan(&s); err != nil {
				writeError(w, err.Error(), http.StatusInternalServerError)
				return
			}
			values = append(values, s)
		}
		if err := rows.Err(); err != nil {
			writeError(w, err.Error(), http.StatusInternalServerError)
			return
		}

		// Numeric-aware sort: numbers by value, everything else lexically.
		sort.Slice(values, func(i, j int) bool {
			a, errA := strconv.ParseFloat(values[i], 64)
			b, errB := strconv.ParseFloat(values[j], 64)
			if errA == nil && errB == nil {
				return a < b
			}
			return values[i] < values[j]
		})
		writeJSON(w, values)
	}
}

// quoteIdent wraps a single identifier in double quotes, escaping embedded ones.
func quoteIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

// quoteTableKey quotes each dot-separated segment of a schema table key
// (e.g. "ducklake.main.Sales" → "ducklake"."main"."Sales").
func quoteTableKey(key string) string {
	parts := strings.Split(key, ".")
	for i, p := range parts {
		parts[i] = quoteIdent(p)
	}
	return strings.Join(parts, ".")
}

// escapeLike escapes LIKE wildcards in a user-supplied search term.
func escapeLike(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `%`, `\%`)
	return strings.ReplaceAll(s, `_`, `\_`)
}

// schemaHandler serves GET /schema.
func schemaHandler(schema *semantic.Schema, mu *sync.RWMutex) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		mu.RLock()
		defer mu.RUnlock()
		writeJSON(w, schema)
	}
}

// exportHandler serves GET /export — returns the current schema as dux.toml.
func exportHandler(schema *semantic.Schema, mu *sync.RWMutex) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		mu.RLock()
		data, err := semantic.ExportDuxTOML(schema)
		mu.RUnlock()
		if err != nil {
			writeError(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="dux.toml"`)
		_, _ = w.Write(data)
	}
}

// importHandler serves POST /import — accepts a dux.toml body, persists
// it to the metadata DB, and reloads the in-memory schema.
func importHandler(metaDB *semantic.MetadataDB, schema *semantic.Schema, mu *sync.RWMutex) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, err := readBody(w, r)
		if err != nil {
			writeError(w, err.Error(), bodyErrorStatus(err))
			return
		}
		if len(body) == 0 {
			writeError(w, "empty body", http.StatusBadRequest)
			return
		}

		// Parse the uploaded TOML into a fresh schema overlay.
		importSchema := semantic.NewSchema()
		if err := semantic.LoadDuxTOMLBytes(body, importSchema); err != nil {
			writeError(w, err.Error(), http.StatusBadRequest)
			return
		}

		// Persist to the metadata DB.
		if err := metaDB.ReplaceAllFromSchema(importSchema); err != nil {
			writeError(w, err.Error(), http.StatusInternalServerError)
			return
		}

		// Reload the live schema from DB (under write lock).
		mu.Lock()
		clearSchemaMetadata(schema)
		if err := metaDB.LoadIntoSchema(schema); err != nil {
			mu.Unlock()
			writeError(w, err.Error(), http.StatusInternalServerError)
			return
		}
		mu.Unlock()

		_, _ = io.WriteString(w, "imported successfully")
	}
}

// ─── Measures ────────────────────────────────────────────────────────────────

type measureRequest struct {
	Table      string                  `json:"table"`
	Name       string                  `json:"name"`
	Expression string                  `json:"expression"`
	Format     *semantic.MeasureFormat `json:"format,omitempty"`
}

// listMeasuresHandler serves GET /measures.
// Returns all measures as a flat JSON array.
func listMeasuresHandler(schema *semantic.Schema, mu *sync.RWMutex) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		type item struct {
			Table      string                  `json:"table"`
			Name       string                  `json:"name"`
			Expression string                  `json:"expression"`
			Format     *semantic.MeasureFormat `json:"format,omitempty"`
		}
		mu.RLock()
		var out []item
		for table, defs := range schema.Measures {
			for name, def := range defs {
				out = append(out, item{
					Table:      table,
					Name:       name,
					Expression: def.Expression,
					Format:     schema.MeasureFormatFor(table, name),
				})
			}
		}
		mu.RUnlock()
		if out == nil {
			out = []item{}
		}
		writeJSON(w, out)
	}
}

// addMeasureHandler serves POST /measures.
// Body: {"table":"...","name":"...","expression":"..."}
func addMeasureHandler(metaDB *semantic.MetadataDB, schema *semantic.Schema, mu *sync.RWMutex) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req measureRequest
		if err := decodeJSONBody(w, r, &req); err != nil {
			writeError(w, err.Error(), bodyErrorStatus(err))
			return
		}
		if req.Table == "" || req.Name == "" || req.Expression == "" {
			writeError(w, "table, name, and expression are required", http.StatusBadRequest)
			return
		}
		if req.Format != nil {
			if err := req.Format.Validate(); err != nil {
				writeError(w, fmt.Sprintf("invalid format: %v", err), http.StatusBadRequest)
				return
			}
		}

		// Parse and store in the in-memory schema. A nil format clears any
		// previously stored format — the request replaces the measure whole.
		mu.Lock()
		err := schema.AddMeasureFromExpr(req.Table, req.Name, req.Expression)
		if err == nil {
			schema.SetMeasureFormat(req.Table, req.Name, req.Format)
		}
		mu.Unlock()
		if err != nil {
			writeError(w, fmt.Sprintf("invalid expression: %v", err), http.StatusBadRequest)
			return
		}

		// Persist to the metadata DB.
		if err := metaDB.SaveMeasure(req.Table, req.Name, req.Expression, req.Format); err != nil {
			writeError(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusCreated)
	}
}

// deleteMeasureHandler serves DELETE /measures/{table}/{name}.
func deleteMeasureHandler(metaDB *semantic.MetadataDB, schema *semantic.Schema, mu *sync.RWMutex) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		table := r.PathValue("table")
		name := r.PathValue("name")
		if table == "" || name == "" {
			writeError(w, "table and name are required", http.StatusBadRequest)
			return
		}

		if err := metaDB.DeleteMeasure(table, name); err != nil {
			writeError(w, err.Error(), http.StatusInternalServerError)
			return
		}

		mu.Lock()
		if defs, ok := schema.Measures[table]; ok {
			delete(defs, name)
			if len(defs) == 0 {
				delete(schema.Measures, table)
			}
		}
		schema.DeleteMeasureFormat(table, name)
		mu.Unlock()

		w.WriteHeader(http.StatusNoContent)
	}
}

// ─── Date table ───────────────────────────────────────────────────────────────

type dateTableRequest struct {
	Table  string `json:"table"`
	Column string `json:"column"`
}

// isDateColumnType reports whether a column data type can hold calendar dates.
func isDateColumnType(dataType string) bool {
	dt := strings.ToUpper(dataType)
	return dt == "DATE" || strings.HasPrefix(dt, "TIMESTAMP")
}

// clearDateTables removes every in-memory date-table designation.
// The caller must hold the schema write lock.
func clearDateTables(schema *semantic.Schema) {
	schema.DateTables = make(map[string]string)
}

// setDateTableHandler serves POST /datetable — designates the model's date
// table and date column. Only one date table is allowed: any previous
// designation is replaced.
func setDateTableHandler(metaDB *semantic.MetadataDB, schema *semantic.Schema, mu *sync.RWMutex) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req dateTableRequest
		if err := decodeJSONBody(w, r, &req); err != nil {
			writeError(w, err.Error(), bodyErrorStatus(err))
			return
		}
		if req.Table == "" || req.Column == "" {
			writeError(w, "table and column are required", http.StatusBadRequest)
			return
		}

		mu.Lock()
		table, ok := schema.Tables[req.Table]
		if !ok {
			mu.Unlock()
			writeError(w, fmt.Sprintf("unknown table %q", req.Table), http.StatusNotFound)
			return
		}
		col, ok := table.Columns[req.Column]
		if !ok {
			mu.Unlock()
			writeError(w, fmt.Sprintf("unknown column %q in table %q", req.Column, req.Table), http.StatusNotFound)
			return
		}
		if !isDateColumnType(col.DataType) {
			mu.Unlock()
			writeError(w, fmt.Sprintf("column %q has type %s — a DATE or TIMESTAMP column is required", req.Column, col.DataType), http.StatusBadRequest)
			return
		}
		if err := metaDB.ReplaceDateTable(strings.ToLower(req.Table), col.Name); err != nil {
			mu.Unlock()
			writeError(w, err.Error(), http.StatusInternalServerError)
			return
		}
		clearDateTables(schema)
		schema.SetDateTable(req.Table, col.Name)
		mu.Unlock()

		w.WriteHeader(http.StatusCreated)
	}
}

// deleteDateTableHandler serves DELETE /datetable — clears the designation.
func deleteDateTableHandler(metaDB *semantic.MetadataDB, schema *semantic.Schema, mu *sync.RWMutex) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		if err := metaDB.ClearDateTables(); err != nil {
			mu.Unlock()
			writeError(w, err.Error(), http.StatusInternalServerError)
			return
		}
		clearDateTables(schema)
		mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	}
}

// ─── Hidden ───────────────────────────────────────────────────────────────────

type hiddenRequest struct {
	Table  string `json:"table"`
	Column string `json:"column,omitempty"`
}

// resolveHiddenTarget validates a hidden request against the schema and
// returns the canonical column name ("" for a table-level designation).
// The caller must hold the schema lock.
func resolveHiddenTarget(schema *semantic.Schema, req hiddenRequest) (string, int, error) {
	table, ok := schema.Tables[req.Table]
	if !ok {
		return "", http.StatusNotFound, fmt.Errorf("unknown table %q", req.Table)
	}
	if req.Column == "" {
		return "", 0, nil
	}
	col, ok := table.Columns[req.Column]
	if !ok {
		return "", http.StatusNotFound, fmt.Errorf("unknown column %q in table %q", req.Column, req.Table)
	}
	return col.Name, 0, nil
}

// hiddenHandler serves POST /hidden (hide=true) and DELETE /hidden
// (hide=false) — sets or clears a hidden designation for a table, view, or
// single column. Body: {"table":"...","column":"..."} (column optional).
func hiddenHandler(metaDB *semantic.MetadataDB, schema *semantic.Schema, mu *sync.RWMutex, hide bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req hiddenRequest
		if err := decodeJSONBody(w, r, &req); err != nil {
			writeError(w, err.Error(), bodyErrorStatus(err))
			return
		}
		if req.Table == "" {
			writeError(w, "table is required", http.StatusBadRequest)
			return
		}

		mu.Lock()
		colName, status, err := resolveHiddenTarget(schema, req)
		if err != nil {
			mu.Unlock()
			writeError(w, err.Error(), status)
			return
		}
		table, column := strings.ToLower(req.Table), strings.ToLower(colName)
		if hide {
			err = metaDB.SaveHidden(table, column)
		} else {
			err = metaDB.DeleteHidden(table, column)
		}
		if err != nil {
			mu.Unlock()
			writeError(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if colName == "" {
			schema.SetTableHidden(req.Table, hide)
		} else {
			schema.SetColumnHidden(req.Table, colName, hide)
		}
		mu.Unlock()
		if hide {
			w.WriteHeader(http.StatusCreated)
		} else {
			w.WriteHeader(http.StatusNoContent)
		}
	}
}

// ─── Relationships ────────────────────────────────────────────────────────────

type relationshipRequest struct {
	FromTable     string `json:"from_table"`
	FromColumn    string `json:"from_column"`
	ToTable       string `json:"to_table"`
	ToColumn      string `json:"to_column"`
	Bidirectional bool   `json:"bidirectional"`
}

// matches reports whether rel joins the same table/column pair as the request.
func (req relationshipRequest) matches(rel *semantic.Relationship) bool {
	return rel.FromTable == req.FromTable && rel.FromColumn == req.FromColumn &&
		rel.ToTable == req.ToTable && rel.ToColumn == req.ToColumn
}

// removeRelationship drops the matching relationship from the in-memory
// schema. The caller must hold the schema write lock.
func removeRelationship(schema *semantic.Schema, req relationshipRequest) {
	rels := schema.Relationships[:0]
	for _, rel := range schema.Relationships {
		if !req.matches(rel) {
			rels = append(rels, rel)
		}
	}
	schema.Relationships = rels
}

// listRelationshipsHandler serves GET /relationships.
// Returns all relationships as a JSON array.
func listRelationshipsHandler(schema *semantic.Schema, mu *sync.RWMutex) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		mu.RLock()
		rels := schema.Relationships
		mu.RUnlock()
		if rels == nil {
			rels = []*semantic.Relationship{}
		}
		writeJSON(w, rels)
	}
}

// addRelationshipHandler serves POST /relationships.
// Body: {"from_table":"...","from_column":"...","to_table":"...","to_column":"..."}
func addRelationshipHandler(metaDB *semantic.MetadataDB, schema *semantic.Schema, mu *sync.RWMutex) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req relationshipRequest
		if err := decodeJSONBody(w, r, &req); err != nil {
			writeError(w, err.Error(), bodyErrorStatus(err))
			return
		}
		if req.FromTable == "" || req.FromColumn == "" || req.ToTable == "" || req.ToColumn == "" {
			writeError(w, "from_table, from_column, to_table, and to_column are required", http.StatusBadRequest)
			return
		}

		mu.Lock()
		// Find and update an existing entry (upsert) rather than always appending,
		// so that updating bidirectional on an existing relationship does not create
		// a duplicate entry in the in-memory schema.
		var existing *semantic.Relationship
		for _, rel := range schema.Relationships {
			if req.matches(rel) {
				existing = rel
				break
			}
		}
		prevBidi := false
		if existing != nil {
			prevBidi = existing.Bidirectional
			existing.Bidirectional = req.Bidirectional
		} else {
			schema.Relationships = append(schema.Relationships, &semantic.Relationship{
				FromTable:     req.FromTable,
				FromColumn:    req.FromColumn,
				ToTable:       req.ToTable,
				ToColumn:      req.ToColumn,
				Bidirectional: req.Bidirectional,
			})
		}
		if err := semantic.ValidateFilterPaths(schema); err != nil {
			if existing != nil {
				existing.Bidirectional = prevBidi
			} else {
				removeRelationship(schema, req)
			}
			mu.Unlock()
			writeError(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := metaDB.SaveRelationship(req.FromTable, req.FromColumn, req.ToTable, req.ToColumn, req.Bidirectional); err != nil {
			if existing != nil {
				existing.Bidirectional = prevBidi
			} else {
				removeRelationship(schema, req)
			}
			mu.Unlock()
			writeError(w, err.Error(), http.StatusInternalServerError)
			return
		}
		mu.Unlock()

		w.WriteHeader(http.StatusCreated)
	}
}

// deleteRelationshipHandler serves DELETE /relationships.
// Body: {"from_table":"...","from_column":"...","to_table":"...","to_column":"..."}
func deleteRelationshipHandler(metaDB *semantic.MetadataDB, schema *semantic.Schema, mu *sync.RWMutex) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req relationshipRequest
		if err := decodeJSONBody(w, r, &req); err != nil {
			writeError(w, err.Error(), bodyErrorStatus(err))
			return
		}
		if req.FromTable == "" || req.FromColumn == "" || req.ToTable == "" || req.ToColumn == "" {
			writeError(w, "from_table, from_column, to_table, and to_column are required", http.StatusBadRequest)
			return
		}

		if err := metaDB.DeleteRelationship(req.FromTable, req.FromColumn, req.ToTable, req.ToColumn); err != nil {
			writeError(w, err.Error(), http.StatusInternalServerError)
			return
		}

		mu.Lock()
		removeRelationship(schema, req)
		mu.Unlock()

		w.WriteHeader(http.StatusNoContent)
	}
}

// refreshHandler serves POST /refresh — reconciles data attachments with the
// database directory, then re-introspects them and reloads metadata and TOML.
func refreshHandler(runtime *bootstrap.Runtime, schema *semantic.Schema, mu *sync.RWMutex) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()

		fresh, err := runtime.RefreshSchema()
		if err != nil {
			writeError(w, fmt.Sprintf("refresh: %v", err), http.StatusInternalServerError)
			return
		}

		// Swap the live schema while queries remain blocked.
		replaceSchema(schema, fresh)

		log.Printf("schema refreshed — %d tables, %d relationships", len(fresh.Tables), len(fresh.Relationships))
		_, _ = io.WriteString(w, "schema refreshed")
	}
}
