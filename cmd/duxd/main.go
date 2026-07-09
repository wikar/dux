// Command duxd is the DUX long-running query server.
//
// It embeds DuckDB in-process and exposes endpoints on :80:
//
//	POST /query   — accepts a DUX query string, returns a JSON result set
//	GET  /schema  — returns tables, columns, and relationships as JSON
//	GET  /export  — exports measures and relationships as TOML
//	POST /import  — imports a dux.toml body, updates the metadata DB
//	POST /refresh — re-introspects attached databases and reloads metadata
//	GET  /docs    — Scalar API reference UI
//	GET  /        — query builder UI
//
// Usage:
//
//	duxd [--db-dir <dir>] [--dux <metadata.duckdb>] [--toml <dux.toml>]
//	     [--import <file>] [--export <file>]
package main

import (
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/fsnotify/fsnotify"

	_ "github.com/duckdb/duckdb-go/v2"

	"github.com/danielwikar/dux/executor"
	"github.com/danielwikar/dux/internal/bootstrap"
	"github.com/danielwikar/dux/parser"
	"github.com/danielwikar/dux/semantic"
	"github.com/danielwikar/dux/ui"
)

// version is overridden at build time via -ldflags="-X main.version=..."
var version = "dev"

// openAPISpec is a minimal OpenAPI 3.1 document describing the HTTP API.
const openAPISpec = `{
  "openapi": "3.1.0",
  "info": {
    "title": "DUX Query API",
    "version": "1.0.0",
    "description": "Execute DUX queries against an embedded DuckDB database. Tables in attached databases are referenced with dot-qualified names (e.g. atp.matches)."
  },
  "paths": {
    "/query": {
      "post": {
        "summary": "Execute a DUX query",
        "description": "Send a raw DUX query string in the request body. Returns a JSON object with column names and row data.",
        "requestBody": {
          "required": true,
          "content": {
            "text/plain": {
              "schema": { "type": "string" },
              "example": "EVALUATE SUMMARIZECOLUMNS(atp.matches[surface], \"Matches\", COUNT(atp.matches[match_num]))"
            }
          }
        },
        "responses": {
          "200": {
            "description": "Query result set",
            "content": {
              "application/json": {
                "schema": {
                  "type": "object",
                  "properties": {
                    "columns": { "type": "array", "items": { "type": "string" } },
                    "rows":    { "type": "array", "items": { "type": "array" } }
                  }
                }
              }
            }
          },
          "400": { "description": "Parse or execution error" }
        }
      }
    },
    "/schema": {
      "get": {
        "summary": "Get schema",
        "description": "Returns tables, columns, relationships, and measures as JSON.",
        "responses": {
          "200": {
            "description": "Schema object",
            "content": {
              "application/json": {
                "schema": { "type": "object" }
              }
            }
          }
        }
      }
    },
    "/export": {
      "get": {
        "summary": "Export dux.toml",
        "description": "Download all measures and relationships as a dux.toml file.",
        "responses": {
          "200": {
            "description": "TOML file",
            "content": {
              "text/plain": {
                "schema": { "type": "string" }
              }
            }
          }
        }
      }
    },
    "/import": {
      "post": {
        "summary": "Import dux.toml",
        "description": "Upload a dux.toml body. Replaces all measures and relationships in the metadata database.",
        "requestBody": {
          "required": true,
          "content": {
            "text/plain": {
              "schema": { "type": "string" },
              "example": "[[relationship]]\nfrom_table = \"atp.matches\"\nfrom_column = \"winner_id\"\nto_table = \"atp.players\"\nto_column = \"player_id\"\n\n[[measure]]\ntable = \"atp.matches\"\nname = \"Total Matches\"\nexpression = \"COUNT(atp.matches[match_num])\""
            }
          }
        },
        "responses": {
          "200": { "description": "Imported successfully" },
          "400": { "description": "Invalid TOML" }
        }
      }
    },
    "/measures": {
      "get": {
        "summary": "List measures",
        "description": "Returns all measures as a flat JSON array.",
        "responses": {
          "200": {
            "description": "Array of measures",
            "content": {
              "application/json": {
                "schema": {
                  "type": "array",
                  "items": {
                    "type": "object",
                    "properties": {
                      "table":      { "type": "string" },
                      "name":       { "type": "string" },
                      "expression": { "type": "string" }
                    }
                  }
                }
              }
            }
          }
        }
      },
      "post": {
        "summary": "Add a measure",
        "description": "Create or update a named measure. The expression is validated through the DUX parser.",
        "requestBody": {
          "required": true,
          "content": {
            "application/json": {
              "schema": {
                "type": "object",
                "required": ["table", "name", "expression"],
                "properties": {
                  "table":      { "type": "string", "example": "atp.matches" },
                  "name":       { "type": "string", "example": "Total Matches" },
                  "expression": { "type": "string", "example": "COUNT(atp.matches[match_num])" }
                }
              }
            }
          }
        },
        "responses": {
          "201": { "description": "Created" },
          "400": { "description": "Validation error" }
        }
      }
    },
    "/measures/{table}/{name}": {
      "delete": {
        "summary": "Delete a measure",
        "parameters": [
          { "name": "table", "in": "path", "required": true, "schema": { "type": "string" } },
          { "name": "name",  "in": "path", "required": true, "schema": { "type": "string" } }
        ],
        "responses": {
          "204": { "description": "Deleted" },
          "400": { "description": "Missing parameters" }
        }
      }
    },
    "/relationships": {
      "get": {
        "summary": "List relationships",
        "description": "Returns all relationships as a JSON array.",
        "responses": {
          "200": {
            "description": "Array of relationships",
            "content": {
              "application/json": {
                "schema": {
                  "type": "array",
                  "items": {
                    "type": "object",
                    "properties": {
                      "from_table":     { "type": "string" },
                      "from_column":    { "type": "string" },
                      "to_table":       { "type": "string" },
                      "to_column":      { "type": "string" },
                      "bidirectional":  { "type": "boolean", "default": false }
                    }
                  }
                }
              }
            }
          }
        }
      },
      "post": {
        "summary": "Add a relationship",
        "description": "Declare a foreign-key relationship between two tables.",
        "requestBody": {
          "required": true,
          "content": {
            "application/json": {
              "schema": {
                "type": "object",
                "required": ["from_table", "from_column", "to_table", "to_column"],
                "properties": {
                  "from_table":    { "type": "string", "example": "atp.matches" },
                  "from_column":   { "type": "string", "example": "winner_id" },
                  "to_table":      { "type": "string", "example": "atp.players" },
                  "to_column":     { "type": "string", "example": "player_id" },
                  "bidirectional": { "type": "boolean", "default": false, "description": "When true, filter context propagates bidirectionally through this edge. Rejected at schema load if it creates an ambiguous filter graph." }
                }
              }
            }
          }
        },
        "responses": {
          "201": { "description": "Created" },
          "400": { "description": "Validation error" }
        }
      },
      "delete": {
        "summary": "Delete a relationship",
        "description": "Remove an existing relationship. The body must match an existing relationship exactly.",
        "requestBody": {
          "required": true,
          "content": {
            "application/json": {
              "schema": {
                "type": "object",
                "required": ["from_table", "from_column", "to_table", "to_column"],
                "properties": {
                  "from_table":  { "type": "string" },
                  "from_column": { "type": "string" },
                  "to_table":    { "type": "string" },
                  "to_column":   { "type": "string" }
                }
              }
            }
          }
        },
        "responses": {
          "204": { "description": "Deleted" },
          "400": { "description": "Validation error" }
        }
      }
    },
    "/datetable": {
      "post": {
        "summary": "Designate the date table",
        "description": "Marks a table as the model's date table with the given DATE/TIMESTAMP column. Only one date table is allowed — any previous designation is replaced. Time-intelligence functions clear all filters on the designated table.",
        "requestBody": {
          "required": true,
          "content": {
            "application/json": {
              "schema": {
                "type": "object",
                "required": ["table", "column"],
                "properties": {
                  "table":  { "type": "string", "example": "dates" },
                  "column": { "type": "string", "example": "date" }
                }
              }
            }
          }
        },
        "responses": {
          "201": { "description": "Designated" },
          "400": { "description": "Column is not a DATE or TIMESTAMP" },
          "404": { "description": "Unknown table or column" }
        }
      },
      "delete": {
        "summary": "Clear the date table designation",
        "responses": {
          "204": { "description": "Cleared" }
        }
      }
    },
    "/refresh": {
      "post": {
        "summary": "Refresh schema metadata",
        "description": "Re-introspect all attached databases and reload persisted metadata and TOML configuration. Use this after the underlying database schema has changed.",
        "responses": {
          "200": { "description": "Schema refreshed successfully" },
          "500": { "description": "Introspection or reload error" }
        }
      }
    }
  }
}`

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
  POST /refresh        Refresh schema from attached databases
  GET  /docs           Scalar interactive API reference
  GET  /               Query builder UI

Flags:
`

func main() {
	showVersion := flag.Bool("version", false, "print version and exit")
	dbDir := flag.String("db-dir", "db", "directory containing *.duckdb / *.db data files")
	duxDB := flag.String("dux", "", "path to dux metadata database (default: <db-dir>/dux.duckdb)")
	tomlPath := flag.String("toml", "dux.toml", "path to dux.toml configuration file")
	// One-shot import / export flags (run, then exit).
	importPath := flag.String("import", "", "import this dux.toml into the metadata DB then start normally")
	exportPath := flag.String("export", "", "export measures and schema to this path then exit")

	flag.Usage = func() {
		fmt.Fprint(os.Stderr, usage)
		flag.PrintDefaults()
	}
	flag.Parse()

	if *showVersion {
		fmt.Println("duxd", version)
		os.Exit(0)
	}

	// Resolve metadata DB path.
	metaPath := *duxDB
	if metaPath == "" {
		metaPath = filepath.Join(*dbDir, "dux.duckdb")
	}

	// Common startup: open metadata DB, attach data DBs, introspect, load metadata + TOML.
	metaDB, db, schema, err := bootstrap.Bootstrap(*dbDir, metaPath, *tomlPath)
	if err != nil {
		log.Fatalf("%v", err)
	}
	defer metaDB.Close()

	// --import: load TOML into metadata DB then continue startup.
	if *importPath != "" {
		if err := bootstrap.ImportTOML(metaDB, *importPath, schema); err != nil {
			log.Fatalf("import: %v", err)
		}
	}

	// --export: write TOML to file then exit.
	if *exportPath != "" {
		if err := bootstrap.ExportTOML(*exportPath, schema); err != nil {
			log.Fatalf("export: %v", err)
		}
		os.Exit(0)
	}

	// Schema is shared between HTTP handlers; protect mutations with a mutex.
	var schemaMu sync.RWMutex

	mux := http.NewServeMux()

	mux.HandleFunc("POST /query", queryHandler(db, schema, &schemaMu))
	mux.HandleFunc("GET /schema", schemaHandler(schema, &schemaMu))
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
	mux.HandleFunc("POST /refresh", refreshHandler(metaDB, db, schema, &schemaMu, *tomlPath))

	mux.HandleFunc("GET /openapi.json", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, openAPISpec)
	})
	mux.HandleFunc("GET /docs", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, docsHTML)
	})

	// Serve the query builder UI embedded at build time.
	distFS, err := fs.Sub(ui.Dist, "dist")
	if err != nil {
		log.Fatalf("ui embed: %v", err)
	}
	mux.Handle("/", http.FileServerFS(distFS))

	// Watch db-dir for new database files and auto-attach them.
	go watchDBDir(*dbDir, metaPath, metaDB, schema, &schemaMu)

	log.Printf("duxd %s listening on :8080  (metadata: %s)", version, metaPath)
	log.Fatal(http.ListenAndServe(":8080", mux))
}

// watchDBDir monitors dir for new *.duckdb and *.db files, attaching them
// to the metadata DB and merging their tables into the live schema.
func watchDBDir(dir, metaPath string, metaDB *semantic.MetadataDB, schema *semantic.Schema, mu *sync.RWMutex) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		log.Printf("warning: db-dir watcher: %v", err)
		return
	}
	defer watcher.Close()

	// Ensure the directory exists before watching.
	if err := os.MkdirAll(dir, 0o755); err != nil {
		log.Printf("warning: db-dir watcher: create %q: %v", dir, err)
		return
	}
	if err := watcher.Add(dir); err != nil {
		log.Printf("warning: db-dir watcher: watch %q: %v", dir, err)
		return
	}

	absMetaPath, _ := filepath.Abs(metaPath)
	log.Printf("watching %q for new databases", dir)

	for {
		select {
		case ev, ok := <-watcher.Events:
			if !ok {
				return
			}
			if !ev.Has(fsnotify.Create) {
				continue
			}
			name := filepath.Base(ev.Name)
			ext := strings.ToLower(filepath.Ext(name))
			if ext != ".duckdb" && ext != ".db" {
				continue
			}
			absPath, _ := filepath.Abs(ev.Name)
			if absPath == absMetaPath {
				continue
			}

			stem := strings.TrimSuffix(name, filepath.Ext(name))
			escapedPath := strings.ReplaceAll(absPath, "'", "''")
			quotedStem := `"` + strings.ReplaceAll(stem, `"`, `""`) + `"`
			q := fmt.Sprintf("ATTACH '%s' AS %s (READ_ONLY)", escapedPath, quotedStem)

			db := metaDB.DB()
			if _, err := db.Exec(q); err != nil {
				log.Printf("warning: auto-attach %q as %q: %v", absPath, stem, err)
				continue
			}
			log.Printf("auto-attached %q as %q (read-only)", name, stem)

			// Re-introspect and merge new tables into the live schema.
			fresh, err := semantic.IntrospectDuckDB(db)
			if err != nil {
				log.Printf("warning: re-introspect after attach %q: %v", stem, err)
				continue
			}
			mu.Lock()
			for k, t := range fresh.Tables {
				if _, exists := schema.Tables[k]; !exists {
					schema.Tables[k] = t
				}
			}
			schema.Relationships = append(schema.Relationships, fresh.Relationships...)
			mu.Unlock()
			log.Printf("schema refreshed — %d tables, %d relationships total", len(schema.Tables), len(schema.Relationships))

		case err, ok := <-watcher.Errors:
			if !ok {
				return
			}
			log.Printf("warning: db-dir watcher: %v", err)
		}
	}
}

// queryResponse is the JSON shape returned by POST /query.
type queryResponse struct {
	Columns []string `json:"columns"`
	Rows    [][]any  `json:"rows"`
}

// pivotResults converts an ordered column list and []map[string]any rows into
// the JSON query response, preserving the column order from the result set.
func pivotResults(cols []string, rowMaps []map[string]any) queryResponse {
	rows := make([][]any, 0, len(rowMaps))
	for _, rm := range rowMaps {
		rowVals := make([]any, len(cols))
		for i, col := range cols {
			rowVals[i] = rm[col]
		}
		rows = append(rows, rowVals)
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

// queryHandler handles POST /query.
func queryHandler(db *sql.DB, schema *semantic.Schema, mu *sync.RWMutex) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if len(body) == 0 {
			http.Error(w, "empty query body", http.StatusBadRequest)
			return
		}

		mu.RLock()
		cols, rowMaps, err := executor.Execute(db, schema, string(body))
		mu.RUnlock()
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		writeJSON(w, pivotResults(cols, rowMaps))
	}
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
			http.Error(w, err.Error(), http.StatusInternalServerError)
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
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if len(body) == 0 {
			http.Error(w, "empty body", http.StatusBadRequest)
			return
		}

		// Parse the uploaded TOML into a fresh schema overlay.
		importSchema := semantic.NewSchema()
		if err := semantic.LoadDuxTOMLBytes(body, importSchema); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		// Persist to the metadata DB.
		if err := metaDB.ReplaceAllFromSchema(importSchema); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		// Reload the live schema from DB (under write lock).
		mu.Lock()
		schema.Relationships = nil
		schema.Measures = make(map[string]map[string]*parser.MeasureDefinition)
		schema.DateTables = make(map[string]string)
		if err := metaDB.LoadIntoSchema(schema); err != nil {
			mu.Unlock()
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		mu.Unlock()

		_, _ = io.WriteString(w, "imported successfully")
	}
}

// ─── Measures ────────────────────────────────────────────────────────────────

type measureRequest struct {
	Table      string `json:"table"`
	Name       string `json:"name"`
	Expression string `json:"expression"`
}

// listMeasuresHandler serves GET /measures.
// Returns all measures as a flat JSON array.
func listMeasuresHandler(schema *semantic.Schema, mu *sync.RWMutex) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		type item struct {
			Table      string `json:"table"`
			Name       string `json:"name"`
			Expression string `json:"expression"`
		}
		mu.RLock()
		var out []item
		for table, defs := range schema.Measures {
			for name, def := range defs {
				out = append(out, item{Table: table, Name: name, Expression: def.Expression})
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
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if req.Table == "" || req.Name == "" || req.Expression == "" {
			http.Error(w, "table, name, and expression are required", http.StatusBadRequest)
			return
		}

		// Parse the expression through the DUX parser to produce an AST node.
		defines, err := parser.ParseMeasures(
			fmt.Sprintf("DEFINE\n    MEASURE %s[%s] = %s", req.Table, req.Name, req.Expression),
		)
		if err != nil || len(defines) == 0 {
			http.Error(w, fmt.Sprintf("invalid expression: %v", err), http.StatusBadRequest)
			return
		}
		def := defines[0]
		def.Expression = req.Expression

		// Persist to the metadata DB.
		if err := metaDB.SaveMeasure(req.Table, req.Name, req.Expression); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		// Update the in-memory schema.
		mu.Lock()
		if schema.Measures[req.Table] == nil {
			schema.Measures[req.Table] = make(map[string]*parser.MeasureDefinition)
		}
		schema.Measures[req.Table][req.Name] = def
		mu.Unlock()

		w.WriteHeader(http.StatusCreated)
	}
}

// deleteMeasureHandler serves DELETE /measures/{table}/{name}.
func deleteMeasureHandler(metaDB *semantic.MetadataDB, schema *semantic.Schema, mu *sync.RWMutex) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		table := r.PathValue("table")
		name := r.PathValue("name")
		if table == "" || name == "" {
			http.Error(w, "table and name are required", http.StatusBadRequest)
			return
		}

		if err := metaDB.DeleteMeasure(table, name); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		mu.Lock()
		if defs, ok := schema.Measures[table]; ok {
			delete(defs, name)
			if len(defs) == 0 {
				delete(schema.Measures, table)
			}
		}
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

// setDateTableHandler serves POST /datetable — designates the model's date
// table and date column. Only one date table is allowed: any previous
// designation is replaced.
func setDateTableHandler(metaDB *semantic.MetadataDB, schema *semantic.Schema, mu *sync.RWMutex) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req dateTableRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if req.Table == "" || req.Column == "" {
			http.Error(w, "table and column are required", http.StatusBadRequest)
			return
		}

		mu.Lock()
		table, ok := schema.Tables[req.Table]
		if !ok {
			mu.Unlock()
			http.Error(w, fmt.Sprintf("unknown table %q", req.Table), http.StatusNotFound)
			return
		}
		col, ok := table.Columns[req.Column]
		if !ok {
			mu.Unlock()
			http.Error(w, fmt.Sprintf("unknown column %q in table %q", req.Column, req.Table), http.StatusNotFound)
			return
		}
		if !isDateColumnType(col.DataType) {
			mu.Unlock()
			http.Error(w, fmt.Sprintf("column %q has type %s — a DATE or TIMESTAMP column is required", req.Column, col.DataType), http.StatusBadRequest)
			return
		}
		previous := make([]string, 0, len(schema.DateTables))
		for tbl := range schema.DateTables {
			previous = append(previous, tbl)
		}
		schema.DateTables = make(map[string]string)
		schema.SetDateTable(req.Table, col.Name)
		mu.Unlock()

		for _, tbl := range previous {
			if err := metaDB.DeleteDateTable(tbl); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
		}
		if err := metaDB.SaveDateTable(strings.ToLower(req.Table), col.Name); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusCreated)
	}
}

// deleteDateTableHandler serves DELETE /datetable — clears the designation.
func deleteDateTableHandler(metaDB *semantic.MetadataDB, schema *semantic.Schema, mu *sync.RWMutex) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		previous := make([]string, 0, len(schema.DateTables))
		for tbl := range schema.DateTables {
			previous = append(previous, tbl)
		}
		schema.DateTables = make(map[string]string)
		mu.Unlock()

		for _, tbl := range previous {
			if err := metaDB.DeleteDateTable(tbl); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
		}
		w.WriteHeader(http.StatusNoContent)
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
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if req.FromTable == "" || req.FromColumn == "" || req.ToTable == "" || req.ToColumn == "" {
			http.Error(w, "from_table, from_column, to_table, and to_column are required", http.StatusBadRequest)
			return
		}

		if err := metaDB.SaveRelationship(req.FromTable, req.FromColumn, req.ToTable, req.ToColumn, req.Bidirectional); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		mu.Lock()
		// Find and update an existing entry (upsert) rather than always appending,
		// so that updating bidirectional on an existing relationship does not create
		// a duplicate entry in the in-memory schema.
		var existing *semantic.Relationship
		for _, rel := range schema.Relationships {
			if rel.FromTable == req.FromTable && rel.FromColumn == req.FromColumn &&
				rel.ToTable == req.ToTable && rel.ToColumn == req.ToColumn {
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
		if err := semantic.ValidateBidiPaths(schema); err != nil {
			// Ambiguous schema — roll back the change.
			if existing != nil {
				existing.Bidirectional = prevBidi
			} else {
				rels := schema.Relationships[:0]
				for _, rel := range schema.Relationships {
					if rel.FromTable == req.FromTable && rel.FromColumn == req.FromColumn &&
						rel.ToTable == req.ToTable && rel.ToColumn == req.ToColumn {
						continue
					}
					rels = append(rels, rel)
				}
				schema.Relationships = rels
			}
			mu.Unlock()
			_ = metaDB.DeleteRelationship(req.FromTable, req.FromColumn, req.ToTable, req.ToColumn)
			http.Error(w, err.Error(), http.StatusBadRequest)
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
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if req.FromTable == "" || req.FromColumn == "" || req.ToTable == "" || req.ToColumn == "" {
			http.Error(w, "from_table, from_column, to_table, and to_column are required", http.StatusBadRequest)
			return
		}

		if err := metaDB.DeleteRelationship(req.FromTable, req.FromColumn, req.ToTable, req.ToColumn); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		mu.Lock()
		rels := schema.Relationships[:0]
		for _, rel := range schema.Relationships {
			if rel.FromTable == req.FromTable && rel.FromColumn == req.FromColumn &&
				rel.ToTable == req.ToTable && rel.ToColumn == req.ToColumn {
				continue
			}
			rels = append(rels, rel)
		}
		schema.Relationships = rels
		mu.Unlock()

		w.WriteHeader(http.StatusNoContent)
	}
}

// refreshHandler serves POST /refresh — re-introspects all attached
// databases and reloads persisted metadata and TOML configuration.
func refreshHandler(metaDB *semantic.MetadataDB, db *sql.DB, schema *semantic.Schema, mu *sync.RWMutex, tomlPath string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Re-introspect the database to pick up schema changes.
		fresh, err := semantic.IntrospectDuckDB(db)
		if err != nil {
			http.Error(w, fmt.Sprintf("introspect: %v", err), http.StatusInternalServerError)
			return
		}

		// Reload persisted metadata (relationships + measures) from the metadata DB.
		if err := metaDB.LoadIntoSchema(fresh); err != nil {
			http.Error(w, fmt.Sprintf("load metadata: %v", err), http.StatusInternalServerError)
			return
		}

		// Re-apply TOML overlay if the file exists.
		if _, statErr := os.Stat(tomlPath); statErr == nil {
			if err := semantic.LoadDuxTOML(tomlPath, fresh); err != nil {
				http.Error(w, fmt.Sprintf("load toml: %v", err), http.StatusInternalServerError)
				return
			}
		}

		// Swap the live schema under write lock.
		mu.Lock()
		schema.Tables = fresh.Tables
		schema.Relationships = fresh.Relationships
		schema.Measures = fresh.Measures
		schema.DateTables = fresh.DateTables
		mu.Unlock()

		log.Printf("schema refreshed — %d tables, %d relationships", len(fresh.Tables), len(fresh.Relationships))
		_, _ = io.WriteString(w, "schema refreshed")
	}
}
