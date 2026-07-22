// Package bootstrap provides shared startup logic for the dux CLI and duxd
// server: opening the metadata database, attaching data files, introspecting
// schemas, and importing/exporting TOML configuration.
package bootstrap

import (
	"database/sql"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/danielwikar/dux/semantic"
)

// Startup parses the command-line flags shared by dux and duxd, runs the
// Bootstrap sequence, and handles the --version/--import/--export one-shot
// flags. exitAfterImport controls whether --import terminates the process
// (dux) or continues startup (duxd). The returned dbDir, metaPath, and
// tomlPath are the resolved flag values.
func Startup(binName, version, usage string, exitAfterImport bool) (metaDB *semantic.MetadataDB, db *sql.DB, schema *semantic.Schema, dbDir, metaPath, tomlPath string) {
	showVersion := flag.Bool("version", false, "print version and exit")
	dbDirFlag := flag.String("db-dir", "db", "directory containing *.duckdb / *.db data files")
	duxDB := flag.String("dux", "", "path to dux metadata database (default: <db-dir>/dux.duckdb)")
	tomlFlag := flag.String("toml", "dux.toml", "path to dux.toml configuration file")
	importPath := flag.String("import", "", "import this dux.toml into the metadata DB")
	exportPath := flag.String("export", "", "export measures and schema to this path then exit")

	flag.Usage = func() {
		fmt.Fprint(os.Stderr, usage)
		flag.PrintDefaults()
	}
	flag.Parse()

	if *showVersion {
		fmt.Println(binName, version)
		os.Exit(0)
	}

	// Resolve metadata DB path.
	metaPath = *duxDB
	if metaPath == "" {
		metaPath = filepath.Join(*dbDirFlag, "dux.duckdb")
	}

	metaDB, db, schema, err := Bootstrap(*dbDirFlag, metaPath, *tomlFlag)
	if err != nil {
		log.Fatalf("%v", err)
	}

	if *importPath != "" {
		if err := ImportTOML(metaDB, *importPath, schema); err != nil {
			log.Fatalf("import: %v", err)
		}
		if exitAfterImport {
			os.Exit(0)
		}
	}
	if *exportPath != "" {
		if err := semantic.WriteDuxTOML(*exportPath, schema); err != nil {
			log.Fatalf("export: %v", err)
		}
		log.Printf("exported schema to %q", *exportPath)
		os.Exit(0)
	}
	return metaDB, db, schema, *dbDirFlag, metaPath, *tomlFlag
}

// Bootstrap performs the common startup sequence shared by both the dux CLI
// and the duxd server:
//
//  1. Open (or create) the metadata database at metaPath.
//  2. Attach all *.duckdb / *.db files in dbDir (except the metadata DB).
//  3. Introspect the schema from all attached databases.
//  4. Load persisted metadata (relationships + measures) from the metadata DB.
//  5. Load dux.toml (if present) to supplement the metadata DB.
//
// The caller is responsible for closing the returned MetadataDB.
func Bootstrap(dbDir, metaPath, tomlPath string) (*semantic.MetadataDB, *sql.DB, *semantic.Schema, error) {
	metaDB, err := semantic.OpenMetadataDB(metaPath)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("open metadata db: %w", err)
	}

	db := metaDB.DB()

	if err := AttachDataDBs(db, dbDir, metaPath); err != nil {
		metaDB.Close()
		return nil, nil, nil, fmt.Errorf("attach data databases: %w", err)
	}

	schema, err := semantic.IntrospectDuckDB(db)
	if err != nil {
		metaDB.Close()
		return nil, nil, nil, fmt.Errorf("introspect schema: %w", err)
	}

	if err := metaDB.LoadIntoSchema(schema); err != nil {
		metaDB.Close()
		return nil, nil, nil, fmt.Errorf("load metadata: %w", err)
	}

	if err := semantic.LoadDuxTOML(tomlPath, schema); err != nil {
		log.Printf("warning: loading dux.toml: %v", err)
	}

	// Validate bidirectional relationships — ambiguous filter graphs are
	// rejected at startup rather than producing unpredictable SQL at runtime.
	if err := semantic.ValidateBidiPaths(schema); err != nil {
		metaDB.Close()
		return nil, nil, nil, fmt.Errorf("schema validation: %w", err)
	}

	return metaDB, db, schema, nil
}

// AttachDataDBs attaches every *.duckdb and *.db file in dir (except the
// metadata DB itself) to db as a read-only named attachment.
// The attachment alias is the filename stem (e.g. "bev.duckdb" → alias "bev").
func AttachDataDBs(db *sql.DB, dir, metaPath string) error {
	files, err := dataDBFiles(dir, metaPath)
	if err != nil {
		return err
	}
	for _, file := range files {
		if stem, err := AttachDB(db, file.path, file.name); err != nil {
			log.Printf("warning: attach %q as %q: %v", file.path, stem, err)
		} else {
			log.Printf("attached %q as %q (read-only)", file.name, stem)
		}
	}
	return nil
}

// ReattachDataDBs atomically replaces every data attachment with the current
// top-level *.duckdb / *.db files in dir. This picks up files replaced under an
// existing name while preserving the old attachments if any new file fails to
// attach.
func ReattachDataDBs(db *sql.DB, dir, metaPath string) error {
	files, err := dataDBFiles(dir, metaPath)
	if err != nil {
		return err
	}

	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin attachment refresh: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	rows, err := tx.Query(`
		SELECT database_name
		FROM duckdb_databases()
		WHERE path IS NOT NULL
		  AND NOT internal
		  AND database_name <> current_database()
		ORDER BY database_name
	`)
	if err != nil {
		return fmt.Errorf("list attached databases: %w", err)
	}
	var aliases []string
	for rows.Next() {
		var alias string
		if err := rows.Scan(&alias); err != nil {
			_ = rows.Close()
			return fmt.Errorf("scan attached database: %w", err)
		}
		aliases = append(aliases, alias)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close attached database list: %w", err)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("list attached databases: %w", err)
	}

	for _, alias := range aliases {
		if _, err := tx.Exec("DETACH " + quoteIdent(alias)); err != nil {
			return fmt.Errorf("detach database %q: %w", alias, err)
		}
	}
	for _, file := range files {
		if _, err := attachDB(tx, file.path, file.name); err != nil {
			return fmt.Errorf("reattach database %q: %w", file.name, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit attachment refresh: %w", err)
	}
	return nil
}

// AttachDB attaches a single database file to db as a read-only named
// attachment aliased by the filename stem, which is returned.
func AttachDB(db *sql.DB, absPath, name string) (string, error) {
	return attachDB(db, absPath, name)
}

type execer interface {
	Exec(query string, args ...any) (sql.Result, error)
}

func attachDB(db execer, absPath, name string) (string, error) {
	stem := strings.TrimSuffix(name, filepath.Ext(name))
	escapedPath := strings.ReplaceAll(absPath, "'", "''")
	_, err := db.Exec(fmt.Sprintf("ATTACH '%s' AS %s (READ_ONLY)", escapedPath, quoteIdent(stem)))
	return stem, err
}

type dataDBFile struct {
	name string
	path string
}

func dataDBFiles(dir, metaPath string) ([]dataDBFile, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read db-dir %q: %w", dir, err)
	}

	var files []dataDBFile
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		ext := strings.ToLower(filepath.Ext(name))
		if ext != ".duckdb" && ext != ".db" {
			continue
		}
		absPath, err := filepath.Abs(filepath.Join(dir, name))
		if err != nil {
			return nil, fmt.Errorf("resolve data database %q: %w", name, err)
		}
		if !samePath(absPath, metaPath) {
			files = append(files, dataDBFile{name: name, path: absPath})
		}
	}
	return files, nil
}

func samePath(a, b string) bool {
	absA, errA := filepath.Abs(a)
	absB, errB := filepath.Abs(b)
	if errA != nil || errB != nil {
		return false
	}
	absA = filepath.Clean(absA)
	absB = filepath.Clean(absB)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(absA, absB)
	}
	return absA == absB
}

func quoteIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

// ImportTOML parses a dux.toml file, persists its contents to the metadata
// database, and reloads the schema with the newly imported data.
func ImportTOML(metaDB *semantic.MetadataDB, path string, schema *semantic.Schema) error {
	importSchema := semantic.NewSchema()
	if err := semantic.LoadDuxTOML(path, importSchema); err != nil {
		return fmt.Errorf("parse %q: %w", path, err)
	}
	if err := metaDB.ReplaceAllFromSchema(importSchema); err != nil {
		return fmt.Errorf("write to metadata DB: %w", err)
	}
	if err := metaDB.LoadIntoSchema(schema); err != nil {
		return fmt.Errorf("reload schema: %w", err)
	}
	log.Printf("imported %q into metadata DB", path)
	return nil
}
