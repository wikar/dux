// Package warehouse owns DuckLake connection setup and warehouse-level
// operations. DuckDB instances are transient; durable state lives in the
// SQLite catalog and Parquet data path.
package warehouse

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "github.com/duckdb/duckdb-go/v2"
)

const Alias = "warehouse"

// Config defines the one DuckLake warehouse supported by a DUX process.
type Config struct {
	CatalogPath         string
	DataPath            string
	TimeTravelRetention time.Duration
	FileDeleteDelay     time.Duration
}

// Runtime is one transient DuckDB instance with DuckLake attached.
type Runtime struct {
	db            *sql.DB
	conn          *sql.Conn
	readOnly      bool
	cfg           Config
	settings      map[string]string
	formatVersion string
}

// OpenOwner creates the warehouse when absent and opens the one read-write
// connection used by duxd ownership operations.
func OpenOwner(ctx context.Context, cfg Config) (*Runtime, error) {
	if err := validateConfig(cfg); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(cfg.CatalogPath), 0o755); err != nil {
		return nil, fmt.Errorf("create warehouse catalog directory: %w", err)
	}
	if err := os.MkdirAll(cfg.DataPath, 0o755); err != nil {
		return nil, fmt.Errorf("create warehouse data directory: %w", err)
	}

	db, conn, err := openDuckDB(ctx)
	if err != nil {
		return nil, err
	}
	rt := &Runtime{db: db, conn: conn, cfg: cfg}
	if err := rt.attach(ctx, cfg, false); err != nil {
		rt.Close()
		return nil, err
	}
	if err := rt.verifyDataPath(ctx, cfg); err != nil {
		rt.Close()
		return nil, err
	}
	if err := rt.configure(ctx, cfg); err != nil {
		rt.Close()
		return nil, err
	}
	return rt, nil
}

// OpenReader opens an existing warehouse with a read-only DuckLake attachment.
func OpenReader(ctx context.Context, cfg Config) (*Runtime, error) {
	if err := validateConfig(cfg); err != nil {
		return nil, err
	}
	if _, err := os.Stat(cfg.CatalogPath); err != nil {
		return nil, fmt.Errorf("open warehouse catalog %q: %w", cfg.CatalogPath, err)
	}
	db, conn, err := openDuckDB(ctx)
	if err != nil {
		return nil, err
	}
	rt := &Runtime{db: db, conn: conn, readOnly: true, cfg: cfg}
	if err := rt.attach(ctx, cfg, true); err != nil {
		rt.Close()
		return nil, err
	}
	if err := rt.verifySettings(ctx, cfg); err != nil {
		rt.Close()
		return nil, err
	}
	return rt, nil
}

func validateConfig(cfg Config) error {
	if cfg.CatalogPath == "" {
		return fmt.Errorf("warehouse catalog path is required")
	}
	if cfg.DataPath == "" {
		return fmt.Errorf("warehouse data path is required")
	}
	if cfg.TimeTravelRetention <= 0 {
		return fmt.Errorf("time-travel retention must be positive")
	}
	if cfg.FileDeleteDelay <= 0 {
		return fmt.Errorf("file-delete delay must be positive")
	}
	return nil
}

func openDuckDB(ctx context.Context) (*sql.DB, *sql.Conn, error) {
	db, err := sql.Open("duckdb", "")
	if err != nil {
		return nil, nil, fmt.Errorf("open transient DuckDB: %w", err)
	}
	// Keep one pinned ownership connection while allowing the query executor to
	// borrow independent connections from the same transient DuckDB instance.
	db.SetMaxOpenConns(8)
	conn, err := db.Conn(ctx)
	if err != nil {
		db.Close()
		return nil, nil, fmt.Errorf("pin DuckDB connection: %w", err)
	}
	for _, extension := range []string{"sqlite", "ducklake"} {
		if _, err := conn.ExecContext(ctx, "LOAD "+extension); err == nil {
			continue
		}
		if _, err := conn.ExecContext(ctx, "INSTALL "+extension); err != nil {
			conn.Close()
			db.Close()
			return nil, nil, fmt.Errorf("install DuckDB extension %s: %w", extension, err)
		}
		if _, err := conn.ExecContext(ctx, "LOAD "+extension); err != nil {
			conn.Close()
			db.Close()
			return nil, nil, fmt.Errorf("load DuckDB extension %s: %w", extension, err)
		}
	}
	return db, conn, nil
}

func (r *Runtime) attach(ctx context.Context, cfg Config, readOnly bool) error {
	catalog, err := filepath.Abs(cfg.CatalogPath)
	if err != nil {
		return fmt.Errorf("resolve warehouse catalog: %w", err)
	}
	data, err := filepath.Abs(cfg.DataPath)
	if err != nil {
		return fmt.Errorf("resolve warehouse data path: %w", err)
	}
	uri := "ducklake:sqlite:" + filepath.ToSlash(catalog)
	options := ""
	if readOnly {
		options = " (READ_ONLY)"
	} else if _, err := os.Stat(catalog); os.IsNotExist(err) {
		options = fmt.Sprintf(" (DATA_PATH %s, DATA_INLINING_ROW_LIMIT 0)", sqlString(filepath.ToSlash(data)+"/"))
	}
	stmt := fmt.Sprintf("ATTACH %s AS %s%s", sqlString(uri), Alias, options)
	if _, err := r.conn.ExecContext(ctx, stmt); err != nil {
		return fmt.Errorf("attach DuckLake warehouse: %w", err)
	}
	if !readOnly {
		if _, err := r.conn.ExecContext(ctx, "USE "+Alias); err != nil {
			return fmt.Errorf("select DuckLake warehouse: %w", err)
		}
	}
	return nil
}

func (r *Runtime) configure(ctx context.Context, cfg Config) error {
	options := [][2]string{
		{"data_inlining_row_limit", "0"},
		{"expire_older_than", duckLakeDuration(cfg.TimeTravelRetention)},
		{"delete_older_than", duckLakeDuration(cfg.FileDeleteDelay)},
	}
	for _, option := range options {
		stmt := fmt.Sprintf("CALL %s.set_option(%s, %s)", Alias, sqlString(option[0]), sqlString(option[1]))
		if _, err := r.conn.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("set DuckLake option %s: %w", option[0], err)
		}
	}
	return r.verifySettings(ctx, cfg)
}

func (r *Runtime) verifySettings(ctx context.Context, cfg Config) error {
	rows, err := r.conn.QueryContext(ctx, `SELECT option_name, value FROM warehouse.options() WHERE scope = 'GLOBAL'`)
	if err != nil {
		return fmt.Errorf("read DuckLake options: %w", err)
	}
	defer rows.Close()
	settings := make(map[string]string)
	for rows.Next() {
		var name, value string
		if err := rows.Scan(&name, &value); err != nil {
			return fmt.Errorf("scan DuckLake option: %w", err)
		}
		settings[name] = value
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("read DuckLake options: %w", err)
	}
	for name, want := range map[string]string{
		"data_inlining_row_limit": "0",
		"expire_older_than":       duckLakeDuration(cfg.TimeTravelRetention),
		"delete_older_than":       duckLakeDuration(cfg.FileDeleteDelay),
	} {
		if got := settings[name]; got != want {
			return fmt.Errorf("DuckLake option %s = %q, want %q", name, got, want)
		}
	}
	if err := r.verifyDataPath(ctx, cfg); err != nil {
		return err
	}
	r.settings = map[string]string{
		"data_inlining_row_limit": settings["data_inlining_row_limit"],
		"expire_older_than":       settings["expire_older_than"],
		"delete_older_than":       settings["delete_older_than"],
	}
	r.formatVersion = settings["version"]
	return nil
}

func (r *Runtime) verifyDataPath(ctx context.Context, cfg Config) error {
	var dataPath string
	if err := r.conn.QueryRowContext(ctx, `SELECT data_path FROM warehouse.settings()`).Scan(&dataPath); err != nil {
		return fmt.Errorf("read DuckLake data path: %w", err)
	}
	storedPath, err := filepath.Abs(filepath.FromSlash(strings.TrimRight(dataPath, "/\\")))
	if err != nil {
		return fmt.Errorf("resolve stored DuckLake data path: %w", err)
	}
	wantPath, err := filepath.Abs(cfg.DataPath)
	if err != nil {
		return fmt.Errorf("resolve configured DuckLake data path: %w", err)
	}
	if normalPath(storedPath) != normalPath(wantPath) {
		return fmt.Errorf("DuckLake data path is %q, but --warehouse-data resolves to %q; move catalog and data together or restore the configured path", storedPath, wantPath)
	}
	return nil
}

func duckLakeDuration(d time.Duration) string {
	if d%(24*time.Hour) == 0 {
		return fmt.Sprintf("%dd", int(d/(24*time.Hour)))
	}
	if d%time.Hour == 0 {
		return fmt.Sprintf("%dh", int(d/time.Hour))
	}
	return d.String()
}

func sqlString(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

// DB returns the transient DuckDB database. It is intended for the query
// runtime; ownership operations should use Conn so current-catalog state is
// deterministic.
func (r *Runtime) DB() *sql.DB { return r.db }

// Conn returns the pinned connection used for warehouse ownership operations.
func (r *Runtime) Conn() *sql.Conn { return r.conn }

func (r *Runtime) ReadOnly() bool                 { return r.readOnly }
func (r *Runtime) FileDeleteDelay() time.Duration { return r.cfg.FileDeleteDelay }

func (r *Runtime) EffectiveSettings() map[string]string {
	settings := make(map[string]string, len(r.settings))
	for name, value := range r.settings {
		settings[name] = value
	}
	return settings
}

func (r *Runtime) FormatVersion() string { return r.formatVersion }

// Close releases the pinned connection and transient DuckDB instance.
func (r *Runtime) Close() error {
	if r == nil {
		return nil
	}
	if r.conn != nil {
		_ = r.conn.Close()
	}
	if r.db != nil {
		return r.db.Close()
	}
	return nil
}

// Versions returns the embedded DuckDB and loaded DuckLake extension versions.
func (r *Runtime) Versions(ctx context.Context) (duckDB, duckLake string, err error) {
	if err = r.conn.QueryRowContext(ctx, "SELECT version()").Scan(&duckDB); err != nil {
		return "", "", fmt.Errorf("read DuckDB version: %w", err)
	}
	if err = r.conn.QueryRowContext(ctx, "SELECT extension_version FROM warehouse.settings()").Scan(&duckLake); err != nil {
		return "", "", fmt.Errorf("read DuckLake version: %w", err)
	}
	return duckDB, duckLake, nil
}

// SnapshotState returns the latest committed DuckLake snapshot and schema version.
func (r *Runtime) SnapshotState(ctx context.Context) (snapshotID, schemaVersion int64, err error) {
	err = r.conn.QueryRowContext(ctx, `
		SELECT snapshot_id, schema_version
		FROM warehouse.snapshots()
		ORDER BY snapshot_id DESC
		LIMIT 1
	`).Scan(&snapshotID, &schemaVersion)
	if err != nil {
		return 0, 0, fmt.Errorf("read DuckLake snapshot state: %w", err)
	}
	return snapshotID, schemaVersion, nil
}
