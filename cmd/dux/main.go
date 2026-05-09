// Command dux is the DUX query language CLI.
// It can run a .dux file directly or start an interactive REPL.
//
// Usage:
//
//	dux [flags] [file.dux]
//
// If no file is given, a REPL is started. Type a query (multiple lines), then
// press Enter on a blank line to execute. Ctrl+C or Ctrl+D to exit.
package main

import (
	"bufio"
	"database/sql"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	// Register the DuckDB driver with database/sql.
	_ "github.com/duckdb/duckdb-go/v2"

	"github.com/danielwikar/dux/executor"
	"github.com/danielwikar/dux/internal/bootstrap"
	"github.com/danielwikar/dux/semantic"
)

// version is overridden at build time via -ldflags="-X main.version=..."
var version = "dev"

const usage = `dux — DUX query language CLI

Usage:
  dux [flags] [file.dux]

With no arguments a REPL is started. Type a query (multiple lines), then press
Enter on a blank line to execute. Ctrl+C or Ctrl+D to exit.

Flags:
`

func main() {
	showVersion := flag.Bool("version", false, "print version and exit")
	dbDir := flag.String("db-dir", "db", "directory containing *.duckdb / *.db data files")
	duxDB := flag.String("dux", "", "path to dux metadata database (default: <db-dir>/dux.duckdb)")
	tomlPath := flag.String("toml", "dux.toml", "path to dux.toml configuration file")
	importPath := flag.String("import", "", "import this dux.toml into the metadata DB then exit")
	exportPath := flag.String("export", "", "export measures and schema to this path then exit")

	flag.Usage = func() {
		fmt.Fprint(os.Stderr, usage)
		flag.PrintDefaults()
	}

	flag.Parse()

	if *showVersion {
		fmt.Println("dux", version)
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

	// --import: load TOML into metadata DB then exit.
	if *importPath != "" {
		if err := bootstrap.ImportTOML(metaDB, *importPath, schema); err != nil {
			log.Fatalf("import: %v", err)
		}
		os.Exit(0)
	}

	// --export: write the current schema + measures to a dux.toml file.
	if *exportPath != "" {
		if err := bootstrap.ExportTOML(*exportPath, schema); err != nil {
			log.Fatalf("export: %v", err)
		}
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
		log.Fatalf("read file: %v", err)
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
		log.Fatalf("read input: %v", err)
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
