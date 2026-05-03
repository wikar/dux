// Command duxd is the DUX long-running query server.
//
// It embeds DuckDB in-process and exposes endpoints on :80:
//
//	POST /query   — accepts a DUX query string, returns a JSON result set
//	GET  /schema  — returns tables, columns, and relationships as JSON
//	GET  /export  — exports measures and relationships as TOML
//	POST /import  — imports a dux.toml body, updates the metadata DB
//	GET  /docs/*  — Scalar API reference UI
//	GET  /        — query builder UI
//
// Usage:
//
//	duxd [--db-dir <dir>] [--dux <metadata.duckdb>] [--toml <dux.toml>]
//	     [--import <file>] [--export <file>]
package main

import (
	"database/sql"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/static"
	"github.com/yokeTH/gofiber-scalar/scalar/v3"

	_ "github.com/duckdb/duckdb-go/v2"

	"github.com/danielwikar/dux/executor"
	"github.com/danielwikar/dux/parser"
	"github.com/danielwikar/dux/semantic"
	"github.com/danielwikar/dux/ui"
)

// openAPISpec is a minimal OpenAPI 3.1 document describing the Fiber API.
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
                      "from_table":  { "type": "string" },
                      "from_column": { "type": "string" },
                      "to_table":    { "type": "string" },
                      "to_column":   { "type": "string" }
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
                  "from_table":  { "type": "string", "example": "atp.matches" },
                  "from_column": { "type": "string", "example": "winner_id" },
                  "to_table":    { "type": "string", "example": "atp.players" },
                  "to_column":   { "type": "string", "example": "player_id" }
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
    }
  }
}`

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
  DELETE /measures/:table/:name  Delete a measure
  GET  /relationships  List all relationships
  POST /relationships  Add a relationship
  DELETE /relationships  Delete a relationship
  GET  /docs/*         Scalar interactive API reference
  GET  /               Query builder UI

Flags:
`

func main() {
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

	// Resolve metadata DB path.
	metaPath := *duxDB
	if metaPath == "" {
		metaPath = filepath.Join(*dbDir, "dux.duckdb")
	}

	// Open (or create) the metadata database.
	metaDB, err := semantic.OpenMetadataDB(metaPath)
	if err != nil {
		log.Fatalf("open metadata db: %v", err)
	}
	defer metaDB.Close()

	// Attach all data databases (*.duckdb, *.db) in db-dir to the metadata DB.
	// dux.duckdb itself is the primary connection and is never attached.
	if err := attachDataDBs(metaDB.DB(), *dbDir, metaPath); err != nil {
		log.Fatalf("attach data databases: %v", err)
	}

	// Introspect schema from all attached databases.
	schema, err := semantic.IntrospectDuckDB(metaDB.DB())
	if err != nil {
		log.Fatalf("introspect schema: %v", err)
	}

	// Load metadata (relationships + measures) from the metadata DB.
	if err := metaDB.LoadIntoSchema(schema); err != nil {
		log.Fatalf("load metadata: %v", err)
	}

	// Load dux.toml if present (supplements the metadata DB).
	if err := semantic.LoadDuxTOML(*tomlPath, schema); err != nil {
		log.Printf("warning: loading dux.toml: %v", err)
	}

	// --import: load TOML into metadata DB then continue startup.
	if *importPath != "" {
		importSchema := semantic.NewSchema()
		if err := semantic.LoadDuxTOML(*importPath, importSchema); err != nil {
			log.Fatalf("import: parse %q: %v", *importPath, err)
		}
		if err := metaDB.ReplaceAllFromSchema(importSchema); err != nil {
			log.Fatalf("import: write to metadata DB: %v", err)
		}
		// Reload schema with newly imported data.
		if err := metaDB.LoadIntoSchema(schema); err != nil {
			log.Fatalf("import: reload schema: %v", err)
		}
		log.Printf("imported %q into metadata DB", *importPath)
	}

	// --export: write TOML to file then exit.
	if *exportPath != "" {
		if err := semantic.WriteDuxTOML(*exportPath, schema); err != nil {
			log.Fatalf("export: %v", err)
		}
		log.Printf("exported schema to %q", *exportPath)
		os.Exit(0)
	}

	// Schema is shared between HTTP handlers; protect mutations with a mutex.
	var schemaMu sync.RWMutex

	// Fiber app on :80 — primary API with Scalar docs.
	app := fiber.New(fiber.Config{})

	app.Post("/query", fiberQueryHandler(metaDB.DB(), schema, &schemaMu))
	app.Get("/schema", fiberSchemaHandler(schema, &schemaMu))
	app.Get("/export", fiberExportHandler(schema, &schemaMu))
	app.Post("/import", fiberImportHandler(metaDB, schema, &schemaMu))
	app.Get("/measures", fiberListMeasuresHandler(schema, &schemaMu))
	app.Post("/measures", fiberAddMeasureHandler(metaDB, schema, &schemaMu))
	app.Delete("/measures/:table/:name", fiberDeleteMeasureHandler(metaDB, schema, &schemaMu))
	app.Get("/relationships", fiberListRelationshipsHandler(schema, &schemaMu))
	app.Post("/relationships", fiberAddRelationshipHandler(metaDB, schema, &schemaMu))
	app.Delete("/relationships", fiberDeleteRelationshipHandler(metaDB, schema, &schemaMu))

	app.Get("/docs/*", scalar.New(scalar.Config{
		Title:             "DUX Query API",
		FileContentString: openAPISpec,
		Path:              "/docs",
	}))

	// Serve the query builder UI embedded at build time.
	distFS, err := fs.Sub(ui.Dist, "dist")
	if err != nil {
		log.Fatalf("ui embed: %v", err)
	}
	app.Use("/", static.New("", static.Config{FS: distFS}))

	log.Printf("duxd listening on :8080  (metadata: %s)", metaPath)
	log.Fatal(app.Listen(":8080"))
}

// attachDataDBs attaches every *.duckdb and *.db file in dir (except the
// metadata DB itself) to db as a read-only named attachment.
// The attachment alias is the filename stem (e.g. "atp.duckdb" → alias "atp").
func attachDataDBs(db *sql.DB, dir, metaPath string) error {
	absMetaPath, _ := filepath.Abs(metaPath)

	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // db-dir not created yet — no-op
		}
		return fmt.Errorf("read db-dir %q: %w", dir, err)
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		ext := strings.ToLower(filepath.Ext(name))
		if ext != ".duckdb" && ext != ".db" {
			continue
		}

		absPath, _ := filepath.Abs(filepath.Join(dir, name))
		if absPath == absMetaPath {
			continue // skip the metadata DB itself
		}

		stem := strings.TrimSuffix(name, filepath.Ext(name))
		escapedPath := strings.ReplaceAll(absPath, "'", "''")
		quotedStem := `"` + strings.ReplaceAll(stem, `"`, `""`) + `"`
		q := fmt.Sprintf("ATTACH '%s' AS %s (READ_ONLY)", escapedPath, quotedStem)
		if _, err := db.Exec(q); err != nil {
			log.Printf("warning: attach %q as %q: %v", absPath, stem, err)
		} else {
			log.Printf("attached %q as %q (read-only)", name, stem)
		}
	}
	return nil
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

// fiberQueryHandler handles POST /query on the Fiber app.
func fiberQueryHandler(db *sql.DB, schema *semantic.Schema, mu *sync.RWMutex) fiber.Handler {
	return func(c fiber.Ctx) error {
		body := c.Body()
		if len(body) == 0 {
			return c.Status(fiber.StatusBadRequest).SendString("empty query body")
		}

		mu.RLock()
		cols, rowMaps, err := executor.Execute(db, schema, string(body))
		mu.RUnlock()
		if err != nil {
			return c.Status(fiber.StatusBadRequest).SendString(err.Error())
		}

		return c.JSON(pivotResults(cols, rowMaps))
	}
}

// fiberSchemaHandler serves GET /schema on the Fiber app.
func fiberSchemaHandler(schema *semantic.Schema, mu *sync.RWMutex) fiber.Handler {
	return func(c fiber.Ctx) error {
		mu.RLock()
		defer mu.RUnlock()
		return c.JSON(schema)
	}
}

// fiberExportHandler serves GET /export — returns the current schema as dux.toml.
func fiberExportHandler(schema *semantic.Schema, mu *sync.RWMutex) fiber.Handler {
	return func(c fiber.Ctx) error {
		mu.RLock()
		data, err := semantic.ExportDuxTOML(schema)
		mu.RUnlock()
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).SendString(err.Error())
		}
		c.Set("Content-Type", "text/plain; charset=utf-8")
		c.Set("Content-Disposition", `attachment; filename="dux.toml"`)
		return c.Send(data)
	}
}

// fiberImportHandler serves POST /import — accepts a dux.toml body, persists
// it to the metadata DB, and reloads the in-memory schema.
func fiberImportHandler(metaDB *semantic.MetadataDB, schema *semantic.Schema, mu *sync.RWMutex) fiber.Handler {
	return func(c fiber.Ctx) error {
		body := c.Body()
		if len(body) == 0 {
			return c.Status(fiber.StatusBadRequest).SendString("empty body")
		}

		// Parse the uploaded TOML into a fresh schema overlay.
		importSchema := semantic.NewSchema()
		if err := semantic.LoadDuxTOMLBytes(body, importSchema); err != nil {
			return c.Status(fiber.StatusBadRequest).SendString(err.Error())
		}

		// Persist to the metadata DB.
		if err := metaDB.ReplaceAllFromSchema(importSchema); err != nil {
			return c.Status(fiber.StatusInternalServerError).SendString(err.Error())
		}

		// Reload the live schema from DB (under write lock).
		mu.Lock()
		schema.Relationships = nil
		schema.Measures = make(map[string]map[string]*parser.MeasureDefinition)
		if err := metaDB.LoadIntoSchema(schema); err != nil {
			mu.Unlock()
			return c.Status(fiber.StatusInternalServerError).SendString(err.Error())
		}
		mu.Unlock()

		return c.SendString("imported successfully")
	}
}

// ─── Measures ────────────────────────────────────────────────────────────────

type measureRequest struct {
	Table      string `json:"table"`
	Name       string `json:"name"`
	Expression string `json:"expression"`
}

// fiberListMeasuresHandler serves GET /measures.
// Returns all measures as a flat JSON array.
func fiberListMeasuresHandler(schema *semantic.Schema, mu *sync.RWMutex) fiber.Handler {
	return func(c fiber.Ctx) error {
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
		return c.JSON(out)
	}
}

// fiberAddMeasureHandler serves POST /measures.
// Body: {"table":"...","name":"...","expression":"..."}
func fiberAddMeasureHandler(metaDB *semantic.MetadataDB, schema *semantic.Schema, mu *sync.RWMutex) fiber.Handler {
	return func(c fiber.Ctx) error {
		var req measureRequest
		if err := c.Bind().JSON(&req); err != nil {
			return c.Status(fiber.StatusBadRequest).SendString(err.Error())
		}
		if req.Table == "" || req.Name == "" || req.Expression == "" {
			return c.Status(fiber.StatusBadRequest).SendString("table, name, and expression are required")
		}

		// Parse the expression through the DUX parser to produce an AST node.
		defines, err := parser.ParseMeasures(
			fmt.Sprintf("DEFINE\n    MEASURE %s[%s] = %s", req.Table, req.Name, req.Expression),
		)
		if err != nil || len(defines) == 0 {
			return c.Status(fiber.StatusBadRequest).SendString(fmt.Sprintf("invalid expression: %v", err))
		}
		def := defines[0]
		def.Expression = req.Expression

		// Persist to the metadata DB.
		if err := metaDB.SaveMeasure(req.Table, req.Name, req.Expression); err != nil {
			return c.Status(fiber.StatusInternalServerError).SendString(err.Error())
		}

		// Update the in-memory schema.
		mu.Lock()
		if schema.Measures[req.Table] == nil {
			schema.Measures[req.Table] = make(map[string]*parser.MeasureDefinition)
		}
		schema.Measures[req.Table][req.Name] = def
		mu.Unlock()

		return c.SendStatus(fiber.StatusCreated)
	}
}

// fiberDeleteMeasureHandler serves DELETE /measures/:table/:name.
func fiberDeleteMeasureHandler(metaDB *semantic.MetadataDB, schema *semantic.Schema, mu *sync.RWMutex) fiber.Handler {
	return func(c fiber.Ctx) error {
		table, _ := url.PathUnescape(c.Params("table"))
		name, _ := url.PathUnescape(c.Params("name"))
		if table == "" || name == "" {
			return c.Status(fiber.StatusBadRequest).SendString("table and name are required")
		}

		if err := metaDB.DeleteMeasure(table, name); err != nil {
			return c.Status(fiber.StatusInternalServerError).SendString(err.Error())
		}

		mu.Lock()
		if defs, ok := schema.Measures[table]; ok {
			delete(defs, name)
			if len(defs) == 0 {
				delete(schema.Measures, table)
			}
		}
		mu.Unlock()

		return c.SendStatus(fiber.StatusNoContent)
	}
}

// ─── Relationships ────────────────────────────────────────────────────────────

type relationshipRequest struct {
	FromTable  string `json:"from_table"`
	FromColumn string `json:"from_column"`
	ToTable    string `json:"to_table"`
	ToColumn   string `json:"to_column"`
}

// fiberListRelationshipsHandler serves GET /relationships.
// Returns all relationships as a JSON array.
func fiberListRelationshipsHandler(schema *semantic.Schema, mu *sync.RWMutex) fiber.Handler {
	return func(c fiber.Ctx) error {
		mu.RLock()
		rels := schema.Relationships
		mu.RUnlock()
		if rels == nil {
			rels = []*semantic.Relationship{}
		}
		return c.JSON(rels)
	}
}

// fiberAddRelationshipHandler serves POST /relationships.
// Body: {"from_table":"...","from_column":"...","to_table":"...","to_column":"..."}
func fiberAddRelationshipHandler(metaDB *semantic.MetadataDB, schema *semantic.Schema, mu *sync.RWMutex) fiber.Handler {
	return func(c fiber.Ctx) error {
		var req relationshipRequest
		if err := c.Bind().JSON(&req); err != nil {
			return c.Status(fiber.StatusBadRequest).SendString(err.Error())
		}
		if req.FromTable == "" || req.FromColumn == "" || req.ToTable == "" || req.ToColumn == "" {
			return c.Status(fiber.StatusBadRequest).SendString("from_table, from_column, to_table, and to_column are required")
		}

		if err := metaDB.SaveRelationship(req.FromTable, req.FromColumn, req.ToTable, req.ToColumn); err != nil {
			return c.Status(fiber.StatusInternalServerError).SendString(err.Error())
		}

		mu.Lock()
		schema.Relationships = append(schema.Relationships, &semantic.Relationship{
			FromTable:  req.FromTable,
			FromColumn: req.FromColumn,
			ToTable:    req.ToTable,
			ToColumn:   req.ToColumn,
		})
		mu.Unlock()

		return c.SendStatus(fiber.StatusCreated)
	}
}

// fiberDeleteRelationshipHandler serves DELETE /relationships.
// Body: {"from_table":"...","from_column":"...","to_table":"...","to_column":"..."}
func fiberDeleteRelationshipHandler(metaDB *semantic.MetadataDB, schema *semantic.Schema, mu *sync.RWMutex) fiber.Handler {
	return func(c fiber.Ctx) error {
		var req relationshipRequest
		if err := c.Bind().JSON(&req); err != nil {
			return c.Status(fiber.StatusBadRequest).SendString(err.Error())
		}
		if req.FromTable == "" || req.FromColumn == "" || req.ToTable == "" || req.ToColumn == "" {
			return c.Status(fiber.StatusBadRequest).SendString("from_table, from_column, to_table, and to_column are required")
		}

		if err := metaDB.DeleteRelationship(req.FromTable, req.FromColumn, req.ToTable, req.ToColumn); err != nil {
			return c.Status(fiber.StatusInternalServerError).SendString(err.Error())
		}

		mu.Lock()
		rels := schema.Relationships[:0]
		for _, r := range schema.Relationships {
			if r.FromTable == req.FromTable && r.FromColumn == req.FromColumn &&
				r.ToTable == req.ToTable && r.ToColumn == req.ToColumn {
				continue
			}
			rels = append(rels, r)
		}
		schema.Relationships = rels
		mu.Unlock()

		return c.SendStatus(fiber.StatusNoContent)
	}
}
