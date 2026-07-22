// Package bootstrap owns the common DUX startup sequence.
package bootstrap

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/danielwikar/dux/internal/warehouse"
	"github.com/danielwikar/dux/parser"
	"github.com/danielwikar/dux/semantic"
)

// Runtime is the complete embedded storage runtime used by dux and duxd.
// duxd opens Owner as the single DuckLake maintenance writer and Query as an
// independent read-only connection. The CLI opens Query only.
type Runtime struct {
	Metadata    *semantic.MetadataDB
	Owner       *warehouse.Runtime
	Query       *warehouse.Runtime
	Schema      *semantic.Schema
	DBDir       string
	MetaPath    string
	CatalogPath string
	DataPath    string
	TOMLPath    string
	statusMu    sync.RWMutex
	lastRefresh time.Time
	refreshErr  string
	refreshWarn string
}

func (r *Runtime) DB() *sql.DB { return r.Query.DB() }

func (r *Runtime) Close() error {
	var first error
	if r.Query != nil {
		first = r.Query.Close()
	}
	if r.Owner != nil {
		if err := r.Owner.Close(); first == nil {
			first = err
		}
	}
	if r.Metadata != nil {
		if err := r.Metadata.Close(); first == nil {
			first = err
		}
	}
	return first
}

// RefreshSchema re-introspects DuckLake, then overlays DUX metadata and TOML.
func (r *Runtime) RefreshSchema() (schema *semantic.Schema, err error) {
	var warning string
	defer func() {
		r.statusMu.Lock()
		defer r.statusMu.Unlock()
		wasDegraded := r.refreshErr != "" || r.refreshWarn != ""
		if err != nil {
			r.refreshErr = err.Error()
			if !wasDegraded {
				log.Printf("warehouse schema status changed to degraded: %v", err)
			}
			return
		}
		r.lastRefresh, r.refreshErr, r.refreshWarn = time.Now().UTC(), "", warning
		isDegraded := warning != ""
		if wasDegraded != isDegraded {
			if isDegraded {
				log.Printf("warehouse schema status changed to degraded: %s", warning)
			} else {
				log.Printf("warehouse schema status recovered to healthy")
			}
		}
	}()
	schema, err = semantic.IntrospectDuckDB(r.Query.DB())
	if err != nil {
		return nil, fmt.Errorf("introspect DuckLake: %w", err)
	}
	if err := r.Metadata.LoadIntoSchema(schema); err != nil {
		return nil, fmt.Errorf("load metadata: %w", err)
	}
	if err := semantic.LoadDuxTOML(r.TOMLPath, schema); err != nil {
		return nil, fmt.Errorf("load dux.toml: %w", err)
	}
	if err := semantic.ValidateBidiPaths(schema); err != nil {
		return nil, fmt.Errorf("schema validation: %w", err)
	}
	warning = schemaReferenceWarning(schema)
	return schema, nil
}

func (r *Runtime) RefreshStatus() (time.Time, string, string) {
	r.statusMu.RLock()
	defer r.statusMu.RUnlock()
	return r.lastRefresh, r.refreshErr, r.refreshWarn
}

func schemaReferenceWarning(schema *semantic.Schema) string {
	var issues []string
	for _, relationship := range schema.Relationships {
		from, _ := schema.FindTable(relationship.FromTable)
		to, _ := schema.FindTable(relationship.ToTable)
		if from == nil || to == nil {
			issues = append(issues, fmt.Sprintf("relationship %s[%s] -> %s[%s] references a missing table", relationship.FromTable, relationship.FromColumn, relationship.ToTable, relationship.ToColumn))
			continue
		}
		if _, ok := from.Columns[relationship.FromColumn]; !ok {
			issues = append(issues, fmt.Sprintf("relationship references missing column %s[%s]", relationship.FromTable, relationship.FromColumn))
		}
		if _, ok := to.Columns[relationship.ToColumn]; !ok {
			issues = append(issues, fmt.Sprintf("relationship references missing column %s[%s]", relationship.ToTable, relationship.ToColumn))
		}
	}
	for owner, measures := range schema.Measures {
		if table, _ := schema.FindTable(owner); table == nil {
			for name := range measures {
				issues = append(issues, fmt.Sprintf("measure %s[%s] belongs to a missing table", owner, name))
			}
			continue
		}
		for name, measure := range measures {
			walkMeasureColumns(measure.Expr, func(reference *parser.ColRef) {
				tableName := semantic.StripSingleQuotes(reference.Table)
				columnName := semantic.StripBrackets(reference.Column)
				if tableName == "" {
					if hasMeasureNamed(schema, columnName) {
						return
					}
					tableName = owner
				} else if hasTableMeasure(schema, tableName, columnName) {
					return
				}
				table, _ := schema.FindTable(tableName)
				if table == nil {
					issues = append(issues, fmt.Sprintf("measure %s[%s] references missing table %s", owner, name, tableName))
					return
				}
				for existing := range table.Columns {
					if strings.EqualFold(existing, columnName) {
						return
					}
				}
				issues = append(issues, fmt.Sprintf("measure %s[%s] references missing column %s[%s]", owner, name, tableName, columnName))
			})
		}
	}
	return strings.Join(issues, "; ")
}

func walkMeasureColumns(expression *parser.Expr, visit func(*parser.ColRef)) {
	if expression == nil {
		return
	}
	walkTermColumns(expression.Left, visit)
	for _, right := range expression.Right {
		walkTermColumns(right.Right, visit)
	}
}

func walkTermColumns(term *parser.Term, visit func(*parser.ColRef)) {
	if term == nil {
		return
	}
	if term.ColRef != nil {
		visit(term.ColRef)
	}
	if term.SubExpr != nil {
		walkMeasureColumns(term.SubExpr, visit)
	}
	if term.FuncCall != nil {
		for _, argument := range term.FuncCall.Args {
			walkMeasureColumns(argument, visit)
		}
	}
	if term.TableConstructor != nil {
		for _, row := range term.TableConstructor.Rows {
			for _, value := range row.Values {
				walkMeasureColumns(value, visit)
			}
		}
	}
}

func hasMeasureNamed(schema *semantic.Schema, name string) bool {
	for _, measures := range schema.Measures {
		for existing := range measures {
			if strings.EqualFold(existing, name) {
				return true
			}
		}
	}
	return false
}

func hasTableMeasure(schema *semantic.Schema, table, name string) bool {
	for owner, measures := range schema.Measures {
		if !strings.EqualFold(owner, table) {
			continue
		}
		for existing := range measures {
			if strings.EqualFold(existing, name) {
				return true
			}
		}
	}
	return false
}

// Startup parses shared flags and opens the warehouse. owner must be true only
// for duxd, which owns creation and maintenance.
func Startup(binName, version, usage string, exitAfterImport, owner bool) *Runtime {
	showVersion := flag.Bool("version", false, "print version and exit")
	dbDir := flag.String("db-dir", "db", "DUX state directory")
	duxDB := flag.String("dux", "", "path to DUX SQLite metadata (default: <db-dir>/dux.sqlite)")
	catalog := flag.String("warehouse-catalog", "", "path to DuckLake SQLite catalog (default: <db-dir>/warehouse.sqlite)")
	data := flag.String("warehouse-data", "", "path to local DuckLake Parquet data (default: <db-dir>/warehouse)")
	toml := flag.String("toml", "dux.toml", "path to dux.toml configuration file")
	importPath := flag.String("import", "", "import this dux.toml into DUX metadata")
	exportPath := flag.String("export", "", "export measures and schema to this path then exit")
	retention := flag.Duration("time-travel-retention", 30*24*time.Hour, "DuckLake snapshot time-travel retention")
	deleteDelay := flag.Duration("file-delete-delay", 7*24*time.Hour, "delay before unreferenced Parquet files are deleted")

	flag.Usage = func() {
		fmt.Fprint(os.Stderr, usage)
		flag.PrintDefaults()
	}
	flag.Parse()
	if *showVersion {
		fmt.Println(binName, version)
		os.Exit(0)
	}

	resolve := func(value, fallback string) string {
		if value == "" {
			value = filepath.Join(*dbDir, fallback)
		}
		abs, err := filepath.Abs(value)
		if err != nil {
			log.Fatalf("resolve %q: %v", value, err)
		}
		return abs
	}
	r := &Runtime{
		DBDir:       resolve(*dbDir, "."),
		MetaPath:    resolve(*duxDB, "dux.sqlite"),
		CatalogPath: resolve(*catalog, "warehouse.sqlite"),
		DataPath:    resolve(*data, "warehouse"),
		TOMLPath:    *toml,
	}
	if err := os.MkdirAll(r.DBDir, 0o755); err != nil {
		log.Fatalf("create db-dir: %v", err)
	}

	var err error
	r.Metadata, err = semantic.OpenMetadataDB(r.MetaPath)
	if err != nil {
		log.Fatalf("open DUX metadata: %v", err)
	}
	cfg := warehouse.Config{
		CatalogPath:         r.CatalogPath,
		DataPath:            r.DataPath,
		TimeTravelRetention: *retention,
		FileDeleteDelay:     *deleteDelay,
	}
	if owner {
		r.Owner, err = warehouse.OpenOwner(context.Background(), cfg)
		if err != nil {
			r.Close()
			log.Fatalf("open DuckLake owner: %v", err)
		}
	}
	r.Query, err = warehouse.OpenReader(context.Background(), cfg)
	if err != nil {
		r.Close()
		if !owner && os.IsNotExist(err) {
			log.Fatalf("open DuckLake reader: %v; start duxd once to initialize the warehouse", err)
		}
		log.Fatalf("open DuckLake reader: %v", err)
	}
	r.Schema, err = r.RefreshSchema()
	if err != nil {
		r.Close()
		log.Fatalf("load schema: %v", err)
	}

	if *importPath != "" {
		if err := ImportTOML(r.Metadata, *importPath, r.Schema); err != nil {
			log.Fatalf("import: %v", err)
		}
		if exitAfterImport {
			r.Close()
			os.Exit(0)
		}
	}
	if *exportPath != "" {
		if err := semantic.WriteDuxTOML(*exportPath, r.Schema); err != nil {
			log.Fatalf("export: %v", err)
		}
		log.Printf("exported schema to %q", *exportPath)
		r.Close()
		os.Exit(0)
	}
	return r
}

// Bootstrap is the testable startup path without command-line parsing.
func Bootstrap(dbDir, metaPath, catalogPath, dataPath, tomlPath string, owner bool) (*Runtime, error) {
	r := &Runtime{DBDir: dbDir, MetaPath: metaPath, CatalogPath: catalogPath, DataPath: dataPath, TOMLPath: tomlPath}
	var err error
	r.Metadata, err = semantic.OpenMetadataDB(metaPath)
	if err != nil {
		return nil, err
	}
	cfg := warehouse.Config{CatalogPath: catalogPath, DataPath: dataPath, TimeTravelRetention: 30 * 24 * time.Hour, FileDeleteDelay: 7 * 24 * time.Hour}
	if owner {
		r.Owner, err = warehouse.OpenOwner(context.Background(), cfg)
		if err != nil {
			r.Close()
			return nil, err
		}
	}
	r.Query, err = warehouse.OpenReader(context.Background(), cfg)
	if err != nil {
		r.Close()
		return nil, err
	}
	r.Schema, err = r.RefreshSchema()
	if err != nil {
		r.Close()
		return nil, err
	}
	return r, nil
}

// ImportTOML persists a semantic model and overlays it on the live schema.
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
	log.Printf("imported %q into DUX metadata", path)
	return nil
}
