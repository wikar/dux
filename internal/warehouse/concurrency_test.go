package warehouse_test

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/danielwikar/dux/internal/warehouse"
)

func TestSeveralDirectPipelinesWriteWhileReaderIsOpen(t *testing.T) {
	if schema := os.Getenv("DUX_PIPELINE_SCHEMA"); schema != "" {
		if err := runDirectPipelineBatch(os.Getenv("DUX_PIPELINE_CATALOG"), schema, os.Getenv("DUX_PIPELINE_ID")); err != nil {
			_, _ = fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		os.Exit(0)
	}
	dir := t.TempDir()
	cfg := warehouse.Config{CatalogPath: filepath.Join(dir, "ducklake.sqlite"), DataPath: filepath.Join(dir, "data"), TimeTravelRetention: 7 * 24 * time.Hour, FileDeleteDelay: 24 * time.Hour}
	owner, err := warehouse.OpenOwner(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	for i := 0; i < 4; i++ {
		if _, err := owner.Conn().ExecContext(t.Context(), fmt.Sprintf(`CREATE SCHEMA pipeline_%d; CREATE TABLE pipeline_%d.events(pipeline INTEGER, value INTEGER)`, i, i)); err != nil {
			t.Fatal(err)
		}
	}
	reader, err := warehouse.OpenReader(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()

	var wg sync.WaitGroup
	errs := make(chan error, 5)
	queryCtx, stopQueries := context.WithCancel(context.Background())
	queriesDone := make(chan struct{})
	go func() {
		defer close(queriesDone)
		for {
			select {
			case <-queryCtx.Done():
				return
			default:
			}
			var count int
			if err := reader.Conn().QueryRowContext(queryCtx, `SELECT count(*) FROM warehouse.pipeline_0.events`).Scan(&count); err != nil {
				if queryCtx.Err() == nil {
					errs <- fmt.Errorf("live reader: %w", err)
				}
				return
			}
			time.Sleep(500 * time.Millisecond)
		}
	}()
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			time.Sleep(time.Duration(i) * 2 * time.Second)
			command := exec.Command(os.Args[0], "-test.run=^TestSeveralDirectPipelinesWriteWhileReaderIsOpen$")
			command.Env = append(os.Environ(),
				"DUX_PIPELINE_CATALOG="+cfg.CatalogPath,
				fmt.Sprintf("DUX_PIPELINE_SCHEMA=pipeline_%d", i),
				fmt.Sprintf("DUX_PIPELINE_ID=%d", i),
			)
			if output, err := command.CombinedOutput(); err != nil {
				errs <- fmt.Errorf("pipeline %d: %w: %s", i, err, output)
			}
		}(i)
	}
	wg.Wait()
	stopQueries()
	<-queriesDone
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	for i := 0; i < 4; i++ {
		var count int
		if err := reader.Conn().QueryRowContext(t.Context(), fmt.Sprintf(`SELECT count(*) FROM warehouse.pipeline_%d.events`, i)).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 100 {
			t.Fatalf("pipeline_%d count = %d", i, count)
		}
	}
}

func runDirectPipelineBatch(catalog, schema, id string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	pipeline, err := openDirectPipeline(ctx, catalog)
	if err != nil {
		return err
	}
	defer pipeline.close()
	stmt := fmt.Sprintf(`INSERT INTO warehouse.%s.events SELECT %s, range FROM range(100)`, schema, id)
	var lastErr error
	for attempt := 0; attempt < 5; attempt++ {
		if _, lastErr = pipeline.conn.ExecContext(ctx, stmt); lastErr == nil {
			return nil
		}
		time.Sleep(time.Duration(attempt+1) * 400 * time.Millisecond)
	}
	return lastErr
}

type directPipeline struct {
	db   *sql.DB
	conn *sql.Conn
}

func openDirectPipeline(ctx context.Context, catalog string) (*directPipeline, error) {
	db, err := sql.Open("duckdb", "")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	conn, err := db.Conn(ctx)
	if err != nil {
		db.Close()
		return nil, err
	}
	for _, extension := range []string{"sqlite", "ducklake"} {
		if _, err := conn.ExecContext(ctx, "LOAD "+extension); err != nil {
			conn.Close()
			db.Close()
			return nil, err
		}
	}
	uri := "ducklake:sqlite:" + filepath.ToSlash(catalog)
	quoted := "'" + strings.ReplaceAll(uri, "'", "''") + "'"
	if _, err := conn.ExecContext(ctx, `ATTACH `+quoted+` AS warehouse`); err != nil {
		conn.Close()
		db.Close()
		return nil, err
	}
	if _, err := conn.ExecContext(ctx, `SET ducklake_max_retry_count = 3; SET ducklake_retry_wait_ms = 50`); err != nil {
		conn.Close()
		db.Close()
		return nil, err
	}
	return &directPipeline{db: db, conn: conn}, nil
}

func (p *directPipeline) close() {
	_ = p.conn.Close()
	_ = p.db.Close()
}

func TestReaderSeesOnlyCommittedBatchesAndMutations(t *testing.T) {
	dir := t.TempDir()
	cfg := warehouse.Config{CatalogPath: filepath.Join(dir, "ducklake.sqlite"), DataPath: filepath.Join(dir, "data"), TimeTravelRetention: 7 * 24 * time.Hour, FileDeleteDelay: 24 * time.Hour}
	writer, err := warehouse.OpenOwner(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	if _, err := writer.Conn().ExecContext(t.Context(), `CREATE TABLE events(id INTEGER, value INTEGER)`); err != nil {
		t.Fatal(err)
	}
	reader, err := warehouse.OpenReader(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	tx, err := writer.Conn().BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(t.Context(), `INSERT INTO events VALUES (1, 10), (2, 20)`); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := reader.Conn().QueryRowContext(t.Context(), `SELECT count(*) FROM warehouse.main.events`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("reader saw uncommitted rows: %d, %v", count, err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if err := reader.Conn().QueryRowContext(t.Context(), `SELECT count(*) FROM warehouse.main.events`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("reader saw rolled-back rows: %d, %v", count, err)
	}
	if _, err := writer.Conn().ExecContext(t.Context(), `BEGIN; INSERT INTO events VALUES (1, 10), (2, 20), (3, 30); UPDATE events SET value = 25 WHERE id = 2; DELETE FROM events WHERE id = 1; COMMIT`); err != nil {
		t.Fatal(err)
	}
	var total int
	if err := reader.Conn().QueryRowContext(t.Context(), `SELECT count(*), sum(value) FROM warehouse.main.events`).Scan(&count, &total); err != nil || count != 2 || total != 55 {
		t.Fatalf("committed mutations = count %d total %d, %v", count, total, err)
	}
}

func TestConflictingDDLDoesNotCorruptCatalog(t *testing.T) {
	if catalog := os.Getenv("DUX_DDL_CONFLICT_CATALOG"); catalog != "" {
		data := os.Getenv("DUX_DDL_CONFLICT_DATA")
		if err := runDDLConflict(catalog, data); err != nil {
			_, _ = fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		os.Exit(0)
	}
	dir := t.TempDir()
	cfg := warehouse.Config{CatalogPath: filepath.Join(dir, "ducklake.sqlite"), DataPath: filepath.Join(dir, "data"), TimeTravelRetention: 7 * 24 * time.Hour, FileDeleteDelay: 24 * time.Hour}
	owner, err := warehouse.OpenOwner(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Conn().ExecContext(t.Context(), `CREATE TABLE owned(id INTEGER)`); err != nil {
		t.Fatal(err)
	}
	if err := owner.Close(); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(os.Args[0], "-test.run=^TestConflictingDDLDoesNotCorruptCatalog$")
	command.Env = append(os.Environ(), "DUX_DDL_CONFLICT_CATALOG="+cfg.CatalogPath, "DUX_DDL_CONFLICT_DATA="+cfg.DataPath)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("conflict subprocess: %v\n%s", err, output)
	}
	reader, err := warehouse.OpenReader(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	rows, err := reader.Conn().QueryContext(t.Context(), `SELECT column_name FROM information_schema.columns WHERE table_catalog = 'warehouse' AND table_schema = 'main' AND table_name = 'owned' ORDER BY ordinal_position`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var columns []string
	for rows.Next() {
		var column string
		if err := rows.Scan(&column); err != nil {
			t.Fatal(err)
		}
		columns = append(columns, column)
	}
	if got := fmt.Sprint(columns); got != "[id]" && got != "[id from_first]" && got != "[id from_second]" {
		t.Fatalf("columns after conflict = %s", got)
	}
}

func runDDLConflict(catalog, data string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cfg := warehouse.Config{CatalogPath: catalog, DataPath: data, TimeTravelRetention: 7 * 24 * time.Hour, FileDeleteDelay: 24 * time.Hour}
	first, err := warehouse.OpenOwner(ctx, cfg)
	if err != nil {
		return err
	}
	second, err := warehouse.OpenOwner(ctx, cfg)
	if err != nil {
		return err
	}
	if _, err := second.Conn().ExecContext(ctx, `SET ducklake_max_retry_count = 1; SET ducklake_retry_wait_ms = 10`); err != nil {
		return err
	}
	firstTx, err := first.Conn().BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	secondTx, err := second.Conn().BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if _, err := firstTx.ExecContext(ctx, `ALTER TABLE owned ADD COLUMN from_first INTEGER`); err != nil {
		return err
	}
	secondResult := make(chan error, 1)
	go func() {
		_, err := secondTx.ExecContext(ctx, `ALTER TABLE owned ADD COLUMN from_second INTEGER`)
		secondResult <- err
	}()
	time.Sleep(50 * time.Millisecond)
	firstErr := firstTx.Commit()
	secondErr := <-secondResult
	if secondErr == nil {
		secondErr = secondTx.Commit()
	} else {
		_ = secondTx.Rollback()
	}
	if firstErr == nil && secondErr == nil {
		return fmt.Errorf("conflicting DDL transactions both committed")
	}
	return nil
}
