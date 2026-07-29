package ducklake

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testConfig(t *testing.T) Config {
	t.Helper()
	root := t.TempDir()
	return Config{
		CatalogPath:         filepath.Join(root, "ducklake.sqlite"),
		DataPath:            filepath.Join(root, "ducklake"),
		TimeTravelRetention: 30 * 24 * time.Hour,
		FileDeleteDelay:     7 * 24 * time.Hour,
	}
}

func TestResourceLimitsApplied(t *testing.T) {
	ctx := context.Background()
	cfg := testConfig(t)
	cfg.MemoryLimit = "512MiB"
	cfg.TempDirectory = filepath.Join(filepath.Dir(cfg.CatalogPath), "tmp")
	cfg.MaxTempDirectorySize = "1GiB"

	owner, err := OpenOwner(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()

	var limit, tmp, maxTmp string
	err = owner.Conn().QueryRowContext(ctx, `SELECT current_setting('memory_limit'),
		current_setting('temp_directory'), current_setting('max_temp_directory_size')`).Scan(&limit, &tmp, &maxTmp)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(limit, "512") {
		t.Errorf("memory_limit = %q, want the configured 512MiB cap", limit)
	}
	if !strings.Contains(filepath.ToSlash(tmp), "/tmp") {
		t.Errorf("temp_directory = %q, want the configured spill directory", tmp)
	}
	// A bounded spill directory, not the 90%-of-disk default.
	if strings.Contains(maxTmp, "%") || !strings.Contains(maxTmp, "1.0 GiB") {
		t.Errorf("max_temp_directory_size = %q, want the configured 1GiB cap", maxTmp)
	}
}

// TestDefaultLimitsLeaveDuckDBDefaults covers the shipped configuration:
// spilling enabled so heavy work degrades to disk, with neither the memory
// nor the spill bound narrowed from DuckDB's own defaults.
func TestDefaultLimitsLeaveDuckDBDefaults(t *testing.T) {
	ctx := context.Background()
	cfg := testConfig(t)
	cfg.TempDirectory = filepath.Join(filepath.Dir(cfg.CatalogPath), "tmp")

	owner, err := OpenOwner(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()

	var limit, tmp, maxTmp string
	err = owner.Conn().QueryRowContext(ctx, `SELECT current_setting('memory_limit'),
		current_setting('temp_directory'), current_setting('max_temp_directory_size')`).Scan(&limit, &tmp, &maxTmp)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(filepath.ToSlash(tmp), "/tmp") {
		t.Errorf("temp_directory = %q, want spilling enabled by default", tmp)
	}
	// DuckDB reports its unbounded spill default as a percentage of the disk.
	if !strings.Contains(maxTmp, "%") {
		t.Errorf("max_temp_directory_size = %q, want the unbounded DuckDB default", maxTmp)
	}
	// 80% of RAM lands in GiB on any supported host, never a small MiB cap.
	if !strings.Contains(limit, "GiB") {
		t.Errorf("memory_limit = %q, want the DuckDB default of 80%% of RAM", limit)
	}
}

func TestOwnerAndReaderShareSQLiteDuckLake(t *testing.T) {
	ctx := context.Background()
	cfg := testConfig(t)
	owner, err := OpenOwner(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()

	duckDB, duckLake, err := owner.Versions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(duckDB, "v1.5.4") {
		t.Fatalf("DuckDB version = %q, want pinned baseline v1.5.4", duckDB)
	}
	if duckLake == "" {
		t.Fatal("empty DuckLake extension version")
	}

	if _, err := owner.Conn().ExecContext(ctx, `CREATE TABLE sales (id INTEGER, amount DOUBLE)`); err != nil {
		t.Fatal(err)
	}
	pipelineDB, pipelineConn, err := openDuckDB(ctx, Config{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pipelineConn.ExecContext(ctx, `ATTACH `+sqlString("ducklake:sqlite:"+filepath.ToSlash(cfg.CatalogPath))+` AS ducklake`); err != nil {
		t.Fatal(err)
	}
	if _, err := pipelineConn.ExecContext(ctx, `INSERT INTO ducklake.main.sales VALUES (1, 42.5)`); err != nil {
		t.Fatal(err)
	}
	_ = pipelineConn.Close()
	_ = pipelineDB.Close()
	var fileCount int
	if err := owner.Conn().QueryRowContext(ctx, `SELECT count(*) FROM ducklake_list_files('ducklake', 'sales', schema => 'main')`).Scan(&fileCount); err != nil || fileCount == 0 {
		t.Fatalf("small insert was not written to Parquet: files=%d, %v", fileCount, err)
	}

	reader, err := OpenReader(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()

	var amount float64
	if err := reader.Conn().QueryRowContext(ctx, `SELECT amount FROM ducklake.main.sales WHERE id = 1`).Scan(&amount); err != nil {
		t.Fatal(err)
	}
	if amount != 42.5 {
		t.Fatalf("amount = %v", amount)
	}
	if _, err := reader.Conn().ExecContext(ctx, `INSERT INTO ducklake.main.sales VALUES (2, 1)`); err == nil {
		t.Fatal("read-only DuckLake attachment accepted a write")
	}

	_, schemaVersion, err := reader.SnapshotState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if schemaVersion < 1 {
		t.Fatalf("schema version = %d, want >= 1", schemaVersion)
	}
}

func TestDuckLakeDuration(t *testing.T) {
	for _, tc := range []struct {
		in   time.Duration
		want string
	}{
		{30 * 24 * time.Hour, "30d"},
		{7 * time.Hour, "7h"},
		{90 * time.Minute, "1h30m0s"},
	} {
		if got := duckLakeDuration(tc.in); got != tc.want {
			t.Fatalf("duckLakeDuration(%s) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestOwnerVerifiesPersistentSettingsAndDataPath(t *testing.T) {
	cfg := testConfig(t)
	rt, err := OpenOwner(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if got := rt.EffectiveSettings(); got["data_inlining_row_limit"] != "0" || got["expire_older_than"] != "30d" || got["delete_older_than"] != "7d" {
		t.Fatalf("effective settings = %#v", got)
	}
	if rt.FormatVersion() != "1.0" {
		t.Fatalf("format version = %q", rt.FormatVersion())
	}
	if _, err := rt.Conn().ExecContext(t.Context(), `CALL ducklake.set_option('data_inlining_row_limit', '10')`); err != nil {
		t.Fatal(err)
	}
	if err := rt.Close(); err != nil {
		t.Fatal(err)
	}

	restarted, err := OpenOwner(t.Context(), cfg)
	if err != nil {
		t.Fatalf("restart: %v", err)
	}
	if got := restarted.EffectiveSettings()["data_inlining_row_limit"]; got != "0" {
		t.Fatalf("repaired inlining = %q", got)
	}
	if err := restarted.Close(); err != nil {
		t.Fatal(err)
	}

	cfg.DataPath = filepath.Join(filepath.Dir(cfg.DataPath), "moved")
	if _, err := OpenOwner(t.Context(), cfg); err == nil || !strings.Contains(err.Error(), "--ducklake-data") {
		t.Fatalf("mismatched data path error = %v", err)
	}
}

func TestMaxConnectionsConfigurable(t *testing.T) {
	ctx := context.Background()

	t.Run("default when unset", func(t *testing.T) {
		owner, err := OpenOwner(ctx, testConfig(t))
		if err != nil {
			t.Fatal(err)
		}
		defer owner.Close()
		if got := owner.DB().Stats().MaxOpenConnections; got != DefaultMaxConnections {
			t.Errorf("MaxOpenConnections = %d, want the default %d", got, DefaultMaxConnections)
		}
	})

	t.Run("configured value reaches the pool", func(t *testing.T) {
		cfg := testConfig(t)
		cfg.MaxConnections = 3
		owner, err := OpenOwner(ctx, cfg)
		if err != nil {
			t.Fatal(err)
		}
		defer owner.Close()
		if got := owner.DB().Stats().MaxOpenConnections; got != 3 {
			t.Errorf("MaxOpenConnections = %d, want 3", got)
		}

		// One is pinned for ownership, so the rest must still serve queries.
		conns := make([]*sql.Conn, 0, 2)
		for i := 0; i < 2; i++ {
			c, err := owner.DB().Conn(ctx)
			if err != nil {
				t.Fatalf("borrow connection %d of 2: %v", i+1, err)
			}
			conns = append(conns, c)
		}
		for _, c := range conns {
			c.Close()
		}
	})

	t.Run("below the minimum is rejected", func(t *testing.T) {
		cfg := testConfig(t)
		cfg.MaxConnections = 1
		if _, err := OpenOwner(ctx, cfg); err == nil {
			t.Fatal("expected a pool of 1 to be rejected: the pinned owner would take it all")
		} else if !strings.Contains(err.Error(), "max connections") {
			t.Errorf("error = %v, want it to name the max-connections bound", err)
		}
	})
}

func TestThreadsConfigurable(t *testing.T) {
	ctx := context.Background()

	t.Run("configured value reaches DuckDB", func(t *testing.T) {
		cfg := testConfig(t)
		cfg.Threads = 3
		owner, err := OpenOwner(ctx, cfg)
		if err != nil {
			t.Fatal(err)
		}
		defer owner.Close()

		var got string
		if err := owner.Conn().QueryRowContext(ctx, `SELECT current_setting('threads')`).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != "3" {
			t.Errorf("threads = %q, want 3", got)
		}
	})

	t.Run("applies to borrowed connections too", func(t *testing.T) {
		// threads is instance-global in DuckDB, so setting it on the pinned
		// ownership connection must cover every connection the executor
		// borrows — otherwise the cap would silently apply to nothing.
		cfg := testConfig(t)
		cfg.Threads = 2
		owner, err := OpenOwner(ctx, cfg)
		if err != nil {
			t.Fatal(err)
		}
		defer owner.Close()

		conn, err := owner.DB().Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		var got string
		if err := conn.QueryRowContext(ctx, `SELECT current_setting('threads')`).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != "2" {
			t.Errorf("threads on a borrowed connection = %q, want 2", got)
		}
	})

	t.Run("unset leaves the DuckDB default", func(t *testing.T) {
		owner, err := OpenOwner(ctx, testConfig(t))
		if err != nil {
			t.Fatal(err)
		}
		defer owner.Close()
		var got string
		if err := owner.Conn().QueryRowContext(ctx, `SELECT current_setting('threads')`).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got == "" || got == "0" {
			t.Errorf("threads = %q, want DuckDB's detected default", got)
		}
	})
}
