// Command dux is the DUX query language CLI.
// It can run a .dux file directly or start an interactive REPL.
//
// Usage:
//
//	dux [--db <path>] [file.dux]
//
// If no file is given, a REPL is started. Type a query (multiple lines), then
// press Enter on a blank line to execute. Ctrl+C or Ctrl+D to exit.
package main

import (
	"bufio"
	"database/sql"
	"flag"
	"fmt"
	"os"
	"strings"

	// Register the DuckDB driver with database/sql.
	_ "github.com/duckdb/duckdb-go/v2"

	"github.com/danielwikar/dux/executor"
	"github.com/danielwikar/dux/semantic"
)

const usage = `dux — DUX query language CLI

Usage:
  dux [flags] [file.dux]

With no arguments a REPL is started. Type a query (multiple lines), then press
Enter on a blank line to execute. Ctrl+C or Ctrl+D to exit.

Flags:
`

func main() {
	dbPath := flag.String("db", "", "DuckDB database file path (required)")
	measuresPath := flag.String("measures", "measures.dux", "path to global measures file (optional)")
	tomlPath := flag.String("toml", "dux.toml", "path to dux.toml (optional)")
	importPath := flag.String("import", "", "import this dux.toml into the metadata DB then exit")
	exportPath := flag.String("export", "", "export measures and schema to this path then exit")

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
		fatal("open database: %v", err)
	}
	defer db.Close()

	schema, err := semantic.IntrospectDuckDB(db)
	if err != nil {
		fatal("introspect schema: %v", err)
	}

	if err := semantic.LoadMeasuresFile(*measuresPath, schema); err != nil {
		fmt.Fprintf(os.Stderr, "warning: measures file: %v\n", err)
	}

	if err := semantic.LoadDuxTOML(*tomlPath, schema); err != nil {
		fmt.Fprintf(os.Stderr, "warning: dux.toml: %v\n", err)
	}

	// --import: parse the given TOML and write it to a metadata DB alongside
	// the main DuckDB file, then exit.
	if *importPath != "" {
		importSchema := semantic.NewSchema()
		if err := semantic.LoadDuxTOML(*importPath, importSchema); err != nil {
			fatal("import: %v", err)
		}
		metaPath := *dbPath[:len(*dbPath)-len(".duckdb")] + ".dux.duckdb"
		metaDB, err := semantic.OpenMetadataDB(metaPath)
		if err != nil {
			fatal("open metadata db: %v", err)
		}
		defer metaDB.Close()
		if err := metaDB.ReplaceAllFromSchema(importSchema); err != nil {
			fatal("import: write metadata: %v", err)
		}
		fmt.Fprintf(os.Stderr, "imported %q into %q\n", *importPath, metaPath)
		os.Exit(0)
	}

	// --export: write the current schema + measures to a dux.toml file.
	if *exportPath != "" {
		if err := semantic.WriteDuxTOML(*exportPath, schema); err != nil {
			fatal("export: %v", err)
		}
		fmt.Fprintf(os.Stderr, "exported schema to %q\n", *exportPath)
		os.Exit(0)
	}

	if args := flag.Args(); len(args) > 0 {
		runFile(db, schema, args[0])
	} else {
		runREPL(db, schema)
	}
}

// runFile executes a .dux file and prints the results.
func runFile(db *sql.DB, schema *semantic.Schema, path string) {
	src, err := os.ReadFile(path)
	if err != nil {
		fatal("read file: %v", err)
	}
	_, results, err := executor.Execute(db, schema, string(src))
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	printResults(results)
}

// runREPL starts an interactive prompt. A blank line submits the buffered query.
func runREPL(db *sql.DB, schema *semantic.Schema) {
	fmt.Println("DUX REPL — enter a query, then press Enter on a blank line to run. Ctrl+C to exit.")
	scanner := bufio.NewScanner(os.Stdin)
	var lines []string

	for {
		if len(lines) == 0 {
			fmt.Print("> ")
		} else {
			fmt.Print("  ")
		}
		if !scanner.Scan() {
			break
		}
		line := scanner.Text()
		if line == "" && len(lines) > 0 {
			input := strings.Join(lines, "\n")
			lines = lines[:0]

			_, results, err := executor.Execute(db, schema, input)
			if err != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", err)
				continue
			}
			printResults(results)
		} else {
			lines = append(lines, line)
		}
	}
	if err := scanner.Err(); err != nil {
		fatal("read input: %v", err)
	}
}

// printResults writes result rows to stdout in a simple key: value format.
func printResults(rows []map[string]any) {
	if len(rows) == 0 {
		fmt.Println("(no results)")
		return
	}
	for i, row := range rows {
		if i > 0 {
			fmt.Println()
		}
		for k, v := range row {
			fmt.Printf("  %-20s %v\n", k+":", v)
		}
	}
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "dux: "+format+"\n", args...)
	os.Exit(1)
}
