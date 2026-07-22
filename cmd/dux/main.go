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
	"context"
	"database/sql"
	"flag"
	"fmt"
	"log"
	"os"
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
	runtime := bootstrap.Startup("dux", version, usage, true, false)
	defer runtime.Close()

	if args := flag.Args(); len(args) > 0 {
		runFile(runtime.DB(), runtime.Schema, args[0])
	} else {
		runREPL(runtime.DB(), runtime.Schema)
	}
}

// runFile executes a .dux file and prints the results.
func runFile(db *sql.DB, schema *semantic.Schema, path string) {
	src, err := os.ReadFile(path)
	if err != nil {
		log.Fatalf("read file: %v", err)
	}
	cols, results, err := executor.ExecuteContext(context.Background(), db, schema, string(src))
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	printResults(cols, results)
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

			cols, results, err := executor.ExecuteContext(context.Background(), db, schema, input)
			if err != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", err)
				continue
			}
			printResults(cols, results)
		} else {
			lines = append(lines, line)
		}
	}
	if err := scanner.Err(); err != nil {
		log.Fatalf("read input: %v", err)
	}
}

// printResults writes result rows to stdout in a simple key: value format.
func printResults(cols []string, rows [][]any) {
	if len(rows) == 0 {
		fmt.Println("(no results)")
		return
	}
	for i, row := range rows {
		if i > 0 {
			fmt.Println()
		}
		for j, col := range cols {
			fmt.Printf("  %-20s %v\n", col+":", row[j])
		}
	}
}
