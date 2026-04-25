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

	// Resolve metadata DB path.
	metaPath := *duxDB
	if metaPath == "" {
		metaPath = filepath.Join(*dbDir, "dux.duckdb")
	}

	// Open (or create) the metadata database.
	metaDB, err := semantic.OpenMetadataDB(metaPath)
	if err != nil {
		fatal("open metadata db: %v", err)
	}
	defer metaDB.Close()

	// Attach all data databases (*.duckdb, *.db) in db-dir to the metadata DB.
	if err := attachDataDBs(metaDB.DB(), *dbDir, metaPath); err != nil {
		fatal("attach data databases: %v", err)
	}

	// Introspect schema from all attached databases.
	schema, err := semantic.IntrospectDuckDB(metaDB.DB())
	if err != nil {
		fatal("introspect schema: %v", err)
	}

	// Load metadata (relationships + measures) from the metadata DB.
	if err := metaDB.LoadIntoSchema(schema); err != nil {
		fatal("load metadata: %v", err)
	}

	// Load dux.toml if present (supplements the metadata DB).
	if err := semantic.LoadDuxTOML(*tomlPath, schema); err != nil {
		fmt.Fprintf(os.Stderr, "warning: loading dux.toml: %v\n", err)
	}

	// --import: load TOML into metadata DB then exit.
	if *importPath != "" {
		importSchema := semantic.NewSchema()
		if err := semantic.LoadDuxTOML(*importPath, importSchema); err != nil {
			fatal("import: parse %q: %v", *importPath, err)
		}
		if err := metaDB.ReplaceAllFromSchema(importSchema); err != nil {
			fatal("import: write to metadata DB: %v", err)
		}
		log.Printf("imported %q into metadata DB", *importPath)
		os.Exit(0)
	}

	// --export: write the current schema + measures to a dux.toml file.
	if *exportPath != "" {
		if err := semantic.WriteDuxTOML(*exportPath, schema); err != nil {
			fatal("export: %v", err)
		}
		log.Printf("exported schema to %q", *exportPath)
		os.Exit(0)
	}

	if args := flag.Args(); len(args) > 0 {
		runFile(metaDB.DB(), schema, args[0])
	} else {
		runREPL(metaDB.DB(), schema)
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

// attachDataDBs attaches every *.duckdb and *.db file in dir (except the
// metadata DB itself) to db as a read-only named attachment.
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
			fmt.Fprintf(os.Stderr, "warning: attach %q as %q: %v\n", absPath, stem, err)
		}
	}
	return nil
}
