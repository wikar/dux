// Command duxd is the DUX long-running query server.
//
// It embeds DuckDB in-process and exposes endpoints on :80:
//
//	POST /query   — accepts a DUX query string, returns a JSON result set
//	GET  /schema  — returns tables, columns, and relationships as JSON
//	GET  /docs/*  — Scalar API reference UI
//	GET  /        — query builder UI
//
// Usage:
//
//	duxd [--db <path>] [--schema <schema.dux.json>] [--measures <measures.dux>]
package main

import (
	"database/sql"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"os"

	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/static"
	"github.com/yokeTH/gofiber-scalar/scalar/v3"

	_ "github.com/duckdb/duckdb-go/v2"

	"github.com/danielwikar/dux/executor"
	"github.com/danielwikar/dux/semantic"
	"github.com/danielwikar/dux/ui"
)

// openAPISpec is a minimal OpenAPI 3.1 document describing the Fiber API.
const openAPISpec = `{
  "openapi": "3.1.0",
  "info": {
    "title": "DUX Query API",
    "version": "1.0.0",
    "description": "Execute DUX queries against an embedded DuckDB database."
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
              "example": "EVALUATE SUMMARIZECOLUMNS(matches[surface], \"Matches\", COUNT(matches[match_num]))"
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
    }
  }
}`

const usage = `duxd — DUX long-running query server

Usage:
  duxd [flags]

Endpoints served on :80:
  POST /query    Accept a raw DUX query string, return a JSON result set
  GET  /schema   Return tables, columns, and relationships as JSON
  GET  /docs/*   Scalar interactive API reference
  GET  /         Query builder UI

Flags:
`

func main() {
	dbPath := flag.String("db", "", "DuckDB database file path (required)")
	sidecar := flag.String("schema", "schema.dux.json", "path to sidecar relationship file")
	measuresPath := flag.String("measures", "measures.dux", "path to global measures file (optional)")

	flag.Usage = func() {
		fmt.Fprint(os.Stderr, usage)
		flag.PrintDefaults()
	}

	flag.Parse()

	if *dbPath == "" {
		flag.Usage()
		os.Exit(1)
	}

	db, err := sql.Open("duckdb", *dbPath)
	if err != nil {
		log.Fatalf("open database: %v", err)
	}
	defer db.Close()

	schema, err := semantic.IntrospectDuckDB(db)
	if err != nil {
		log.Fatalf("introspect schema: %v", err)
	}

	if err := semantic.MergeSidecarSchema(*sidecar, schema); err != nil {
		log.Printf("warning: loading sidecar schema: %v", err)
	}

	if err := semantic.LoadMeasuresFile(*measuresPath, schema); err != nil {
		log.Printf("warning: loading measures file: %v", err)
	}

	// Fiber app on :80 — primary API with Scalar docs.
	app := fiber.New(fiber.Config{})

	app.Post("/query", fiberQueryHandler(db, schema))
	app.Get("/schema", fiberSchemaHandler(schema))

	app.Get("/docs/*", scalar.New(scalar.Config{
		Title:             "DUX Query API",
		FileContentString: openAPISpec,
		Path:              "/docs",
	}))

	// Serve the query builder UI embedded at build time.
	// fs.Sub strips the leading "dist" from embed paths so "/" maps to index.html.
	distFS, err := fs.Sub(ui.Dist, "dist")
	if err != nil {
		log.Fatalf("ui embed: %v", err)
	}
	app.Use("/", static.New("", static.Config{FS: distFS}))

	log.Printf("duxd listening on :80")
	log.Fatal(app.Listen(":80"))
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
func fiberQueryHandler(db *sql.DB, schema *semantic.Schema) fiber.Handler {
	return func(c fiber.Ctx) error {
		body := c.Body()
		if len(body) == 0 {
			return c.Status(fiber.StatusBadRequest).SendString("empty query body")
		}

		cols, rowMaps, err := executor.Execute(db, schema, string(body))
		if err != nil {
			return c.Status(fiber.StatusBadRequest).SendString(err.Error())
		}

		return c.JSON(pivotResults(cols, rowMaps))
	}
}

// fiberSchemaHandler serves GET /schema on the Fiber app.
func fiberSchemaHandler(schema *semantic.Schema) fiber.Handler {
	return func(c fiber.Ctx) error {
		return c.JSON(schema)
	}
}
