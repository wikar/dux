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
//	POST /refresh — re-introspects attached databases and reloads metadata
//	GET  /docs    — Scalar API reference UI
//	GET  /        — DUX UI (builder, explorer, dashboards at /dash/;
//	                /api/dash/ backend, DUX_DASH=0 disables dashboards)
//
// Usage:
//
//	duxd [--db-dir <dir>] [--dux <metadata.duckdb>] [--toml <dux.toml>]
//	     [--import <file>] [--export <file>]
package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/fsnotify/fsnotify"

	_ "github.com/duckdb/duckdb-go/v2"

	"github.com/danielwikar/dux/dash"
	"github.com/danielwikar/dux/executor"
	"github.com/danielwikar/dux/internal/bootstrap"
	"github.com/danielwikar/dux/parser"
	"github.com/danielwikar/dux/semantic"
	"github.com/danielwikar/dux/web"
)

// version is overridden at build time via -ldflags="-X main.version=..."
var version = "dev"

// Server flags (registered before bootstrap's flag.Parse).
// Setting the env var DUX_DASH=0 disables the dashboards module entirely.
var (
	listenAddr   = flag.String("listen", ":8080", "HTTP listen address")
	dashDir      = flag.String("dash-dir", "dashboards", "dashboard file store directory")
	dashAssetMax = flag.Int64("dash-asset-max", 10<<20, "max dashboard asset upload size in bytes")
)

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
            },
            "application/json": {
              "schema": {
                "type": "object",
                "required": ["query"],
                "properties": {
                  "query": { "type": "string", "description": "The DUX query text" },
                  "filters": {
                    "type": "array",
                    "description": "External filters applied to the query's outermost filter context (dashboard slicers). Ops: in, between, =, !=, <, <=, >, >=, contains.",
                    "items": {
                      "type": "object",
                      "required": ["table", "column", "op"],
                      "properties": {
                        "table":  { "type": "string" },
                        "column": { "type": "string" },
                        "op":     { "type": "string", "enum": ["in", "between", "=", "!=", "<", "<=", ">", ">=", "contains"] },
                        "values": { "type": "array", "description": "op=in" },
                        "value":  { "description": "scalar ops and contains" },
                        "from":   { "description": "op=between" },
                        "to":     { "description": "op=between" }
                      }
                    }
                  }
                }
              },
              "example": { "query": "EVALUATE SUMMARIZECOLUMNS(atp.matches[surface], \"Matches\", COUNT(atp.matches[match_num]))", "filters": [ { "table": "atp.matches", "column": "surface", "op": "in", "values": ["Clay", "Grass"] } ] }
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
    "/version": {
      "get": {
        "summary": "Server version and API capabilities",
        "description": "Returns the duxd version and capability flags (externalFilters, measureFormats) for client handshakes.",
        "responses": {
          "200": {
            "description": "Version object",
            "content": {
              "application/json": {
                "schema": { "type": "object" }
              }
            }
          }
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
    "/hidden": {
      "post": {
        "summary": "Mark a table, view, or column as hidden",
        "description": "Marks a table (or view) as hidden when column is omitted, or a single column when it is present. Hidden objects stay queryable; the flag only affects UI presentation.",
        "requestBody": {
          "required": true,
          "content": {
            "application/json": {
              "schema": {
                "type": "object",
                "required": ["table"],
                "properties": {
                  "table":  { "type": "string", "example": "atp.matches" },
                  "column": { "type": "string", "example": "winner_id", "description": "Omit to hide the whole table or view." }
                }
              }
            }
          }
        },
        "responses": {
          "201": { "description": "Hidden" },
          "400": { "description": "Missing table" },
          "404": { "description": "Unknown table or column" }
        }
      },
      "delete": {
        "summary": "Clear a hidden designation",
        "description": "Un-hides a table (or view) when column is omitted, or a single column when it is present.",
        "requestBody": {
          "required": true,
          "content": {
            "application/json": {
              "schema": {
                "type": "object",
                "required": ["table"],
                "properties": {
                  "table":  { "type": "string" },
                  "column": { "type": "string" }
                }
              }
            }
          }
        },
        "responses": {
          "204": { "description": "Cleared" },
          "400": { "description": "Missing table" },
          "404": { "description": "Unknown table or column" }
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
    },
    "/api/dash/dashboards": {
      "get": {
        "summary": "List dashboards",
        "description": "All dashboard files under the store, recursively. Files that fail JSON parsing or schema validation are listed with valid=false and their error. Identities are lower-case slash-separated paths without the .json extension.",
        "responses": {
          "200": {
            "description": "Array of dashboard entries",
            "content": {
              "application/json": {
                "schema": {
                  "type": "array",
                  "items": {
                    "type": "object",
                    "properties": {
                      "path":     { "type": "string" },
                      "name":     { "type": "string" },
                      "modified": { "type": "string", "format": "date-time" },
                      "etag":     { "type": "string" },
                      "valid":    { "type": "boolean" },
                      "error":    { "type": "string" }
                    }
                  }
                }
              }
            }
          }
        }
      }
    },
    "/api/dash/dashboards/{path}": {
      "get": {
        "summary": "Get a dashboard",
        "description": "Returns the listing entry plus the parsed document (or raw text for unparseable files). The ETag response header carries the version for If-Match on save.",
        "parameters": [ { "name": "path", "in": "path", "required": true, "schema": { "type": "string" }, "description": "Slash-separated identity without .json" } ],
        "responses": {
          "200": { "description": "Dashboard envelope {path, name, modified, etag, valid, error?, document?, raw?}" },
          "404": { "description": "Not found" }
        }
      },
      "put": {
        "summary": "Create or update a dashboard",
        "description": "Body is the dashboard document (validated against /api/dash/schema.json). Create: no If-Match header. Update: If-Match with the last seen etag; 428 without it, 409 with a stale one (response carries currentEtag and modified for the conflict dialog).",
        "parameters": [
          { "name": "path", "in": "path", "required": true, "schema": { "type": "string" } },
          { "name": "If-Match", "in": "header", "required": false, "schema": { "type": "string" } }
        ],
        "requestBody": { "required": true, "content": { "application/json": { "schema": { "type": "object" } } } },
        "responses": {
          "200": { "description": "Updated — {etag, created:false}" },
          "201": { "description": "Created — {etag, created:true}" },
          "409": { "description": "Stale If-Match — {error, currentEtag, modified}" },
          "412": { "description": "If-Match on a dashboard that no longer exists" },
          "422": { "description": "Document fails schema validation" },
          "428": { "description": "Update without If-Match" }
        }
      },
      "delete": {
        "summary": "Delete a dashboard",
        "parameters": [
          { "name": "path", "in": "path", "required": true, "schema": { "type": "string" } },
          { "name": "If-Match", "in": "header", "required": true, "schema": { "type": "string" } }
        ],
        "responses": {
          "204": { "description": "Deleted" },
          "404": { "description": "Not found" },
          "409": { "description": "Stale If-Match" },
          "428": { "description": "Missing If-Match" }
        }
      }
    },
    "/api/dash/assets/{path}": {
      "post": {
        "summary": "Upload an image asset",
        "description": "Raw image bytes; the path's extension (.png/.jpg/.jpeg/.webp/.svg) decides the type. Size capped by --dash-asset-max (default 10 MiB).",
        "responses": {
          "201": { "description": "Stored — {path, type}" },
          "413": { "description": "Too large" },
          "415": { "description": "Unsupported extension" }
        }
      },
      "get": {
        "summary": "Fetch an asset",
        "description": "SVG is served with a no-execute Content-Security-Policy.",
        "responses": {
          "200": { "description": "Image bytes" },
          "404": { "description": "Not found" }
        }
      }
    },
    "/api/dash/theme": {
      "get": {
        "summary": "Global theme tokens",
        "description": "Contents of dashboards/theme.json ({} when absent). ETag header when the file exists.",
        "responses": { "200": { "description": "Theme JSON object" } }
      },
      "put": {
        "summary": "Replace the global theme",
        "description": "Same If-Match semantics as dashboards.",
        "responses": {
          "200": { "description": "Saved — {etag}" },
          "409": { "description": "Stale If-Match" },
          "428": { "description": "Update without If-Match" }
        }
      }
    },
    "/api/dash/schema.json": {
      "get": {
        "summary": "Dashboard document JSON Schema",
        "description": "The JSON Schema (draft 2020-12) that PUT dashboards are validated against.",
        "responses": { "200": { "description": "JSON Schema document" } }
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
  POST /datetable      Designate the model's date table
  DELETE /datetable    Clear the date-table designation
  POST /hidden         Mark a table, view, or column as hidden
  DELETE /hidden       Clear a hidden designation
  POST /refresh        Refresh schema from attached databases
  GET  /docs           Scalar interactive API reference
  GET  /               DUX UI (builder, explorer, and dashboards at /dash/)
  *    /api/dash/      Dashboards API (documents, assets, theme, schema);
                       disable dashboards with DUX_DASH=0

Flags:
`

func main() {
	metaDB, db, schema, dbDir, metaPath, tomlPath := bootstrap.Startup("duxd", version, usage, false)
	defer metaDB.Close()

	// Schema is shared between HTTP handlers; protect mutations with a mutex.
	var schemaMu sync.RWMutex

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
	mux.HandleFunc("POST /refresh", refreshHandler(metaDB, db, schema, &schemaMu, tomlPath))

	dashEnabled := os.Getenv("DUX_DASH") != "0"
	if dashEnabled {
		dash, err := dash.NewServer(dash.Config{
			Root:          *dashDir,
			MaxAssetBytes: *dashAssetMax,
		})
		if err != nil {
			log.Fatalf("dashboards module: %v", err)
		}
		mux.Handle("/api/dash/", dash)
		log.Printf("dashboards enabled at /dash/ and /api/dash/ (dir %q)", *dashDir)
	} else {
		// Keep the API surface a hard 404 — without this the SPA catch-all
		// would answer /api/dash/* with index.html.
		mux.HandleFunc("/api/dash/", func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "dashboards disabled (DUX_DASH=0)", http.StatusNotFound)
		})
		log.Printf("dashboards disabled (DUX_DASH=0)")
	}

	mux.HandleFunc("GET /version", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{
			"version": version,
			"capabilities": map[string]bool{
				"externalFilters": true,
				"measureFormats":  true,
				"dashboards":      dashEnabled,
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

	// Watch db-dir for new database files and auto-attach them.
	go watchDBDir(dbDir, metaPath, metaDB, schema, &schemaMu)

	log.Printf("duxd %s listening on %s  (metadata: %s)", version, *listenAddr, metaPath)
	log.Fatal(http.ListenAndServe(*listenAddr, mux))
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

			db := metaDB.DB()
			stem, err := bootstrap.AttachDB(db, absPath, name)
			if err != nil {
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
			schema.ApplyHiddenFlags()
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
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if len(body) == 0 {
			http.Error(w, "empty query body", http.StatusBadRequest)
			return
		}

		query := string(body)
		var filters []executor.ExternalFilter
		if strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
			var req queryRequest
			if err := json.Unmarshal(body, &req); err != nil {
				http.Error(w, "invalid JSON body: "+err.Error(), http.StatusBadRequest)
				return
			}
			if strings.TrimSpace(req.Query) == "" {
				http.Error(w, `"query" is required in the JSON body`, http.StatusBadRequest)
				return
			}
			query = req.Query
			filters = req.Filters
		}

		mu.RLock()
		cols, rowMaps, err := executor.ExecuteFiltered(db, schema, query, filters)
		mu.RUnlock()
		if err != nil {
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
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		writeJSON(w, pivotResults(cols, rowMaps))
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
			http.Error(w, "table and column are required", http.StatusBadRequest)
			return
		}

		mu.RLock()
		table, ok := schema.Tables[tableKey]
		var col *semantic.Column
		if ok {
			col = table.Columns[colName]
		}
		mu.RUnlock()
		if !ok {
			http.Error(w, fmt.Sprintf("unknown table %q", tableKey), http.StatusNotFound)
			return
		}
		if col == nil {
			http.Error(w, fmt.Sprintf("unknown column %q in table %q", colName, tableKey), http.StatusNotFound)
			return
		}

		// No SQL ORDER BY: DuckDB's string sort validates UTF-8 and errors on
		// data files with mis-encoded text (hash DISTINCT and scans do not).
		// The result is sorted in Go instead.
		colSQL := quoteIdent(col.Name)
		query := fmt.Sprintf(
			"SELECT CAST(v AS VARCHAR) FROM (SELECT DISTINCT %s AS v FROM %s WHERE %s IS NOT NULL",
			colSQL, quoteTableKey(tableKey), colSQL)
		var args []any
		if q != "" {
			query += fmt.Sprintf(` AND CAST(%s AS VARCHAR) ILIKE ? ESCAPE '\'`, colSQL)
			args = append(args, "%"+escapeLike(q)+"%")
		}
		query += ") LIMIT 50"

		rows, err := db.Query(query, args...)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		defer rows.Close()

		values := []string{}
		for rows.Next() {
			var s string
			if err := rows.Scan(&s); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			values = append(values, s)
		}
		if err := rows.Err(); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
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
// (e.g. "bev.Sales" → "bev"."Sales").
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
		schema.ClearHidden()
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
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if req.Table == "" || req.Name == "" || req.Expression == "" {
			http.Error(w, "table, name, and expression are required", http.StatusBadRequest)
			return
		}
		if req.Format != nil {
			if err := req.Format.Validate(); err != nil {
				http.Error(w, fmt.Sprintf("invalid format: %v", err), http.StatusBadRequest)
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
			http.Error(w, fmt.Sprintf("invalid expression: %v", err), http.StatusBadRequest)
			return
		}

		// Persist to the metadata DB.
		if err := metaDB.SaveMeasure(req.Table, req.Name, req.Expression, req.Format); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
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

// clearDateTables removes every date-table designation from the in-memory
// schema and returns the cleared table keys. The caller must hold the schema
// write lock.
func clearDateTables(schema *semantic.Schema) []string {
	previous := make([]string, 0, len(schema.DateTables))
	for tbl := range schema.DateTables {
		previous = append(previous, tbl)
	}
	schema.DateTables = make(map[string]string)
	return previous
}

// dropDateTables deletes the given date-table designations from the metadata DB.
func dropDateTables(metaDB *semantic.MetadataDB, tables []string) error {
	for _, tbl := range tables {
		if err := metaDB.DeleteDateTable(tbl); err != nil {
			return err
		}
	}
	return nil
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
		previous := clearDateTables(schema)
		schema.SetDateTable(req.Table, col.Name)
		mu.Unlock()

		if err := dropDateTables(metaDB, previous); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
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
		previous := clearDateTables(schema)
		mu.Unlock()

		if err := dropDateTables(metaDB, previous); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
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
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if req.Table == "" {
			http.Error(w, "table is required", http.StatusBadRequest)
			return
		}

		mu.Lock()
		colName, status, err := resolveHiddenTarget(schema, req)
		if err != nil {
			mu.Unlock()
			http.Error(w, err.Error(), status)
			return
		}
		if colName == "" {
			schema.SetTableHidden(req.Table, hide)
		} else {
			schema.SetColumnHidden(req.Table, colName, hide)
		}
		mu.Unlock()

		table, column := strings.ToLower(req.Table), strings.ToLower(colName)
		if hide {
			err = metaDB.SaveHidden(table, column)
		} else {
			err = metaDB.DeleteHidden(table, column)
		}
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
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
		if err := semantic.ValidateBidiPaths(schema); err != nil {
			// Ambiguous schema — roll back the change.
			if existing != nil {
				existing.Bidirectional = prevBidi
			} else {
				removeRelationship(schema, req)
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
		removeRelationship(schema, req)
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
		schema.HiddenTables = fresh.HiddenTables
		schema.HiddenColumns = fresh.HiddenColumns
		schema.ApplyHiddenFlags()
		mu.Unlock()

		log.Printf("schema refreshed — %d tables, %d relationships", len(fresh.Tables), len(fresh.Relationships))
		_, _ = io.WriteString(w, "schema refreshed")
	}
}
