package bootstrap

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/danielwikar/dux/executor"
)

func TestBootstrapCreatesDuckLakeAndUsesPublicNames(t *testing.T) {
	dir := t.TempDir()
	r, err := Bootstrap(dir, filepath.Join(dir, "dux.sqlite"), filepath.Join(dir, "ducklake.sqlite"), filepath.Join(dir, "ducklake"), filepath.Join(dir, "missing.toml"), true)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if _, err := r.Owner.Conn().ExecContext(t.Context(), `
		CREATE TABLE sales(id BIGINT, amount DOUBLE);
		INSERT INTO sales VALUES (1, 10);
		CREATE VIEW current_sales AS SELECT * FROM sales;
		CREATE SCHEMA finance;
		CREATE TABLE finance.ledger(id BIGINT);
		INSERT INTO finance.ledger VALUES (7);
	`); err != nil {
		t.Fatal(err)
	}
	fresh, err := r.RefreshSchema()
	if err != nil {
		t.Fatal(err)
	}
	table, ok := fresh.Tables["sales"]
	if !ok {
		t.Fatalf("public table sales missing: %#v", fresh.Tables)
	}
	if table.SQLName != "ducklake.main.sales" {
		t.Fatalf("SQLName = %q", table.SQLName)
	}
	if view := fresh.Tables["current_sales"]; view == nil || !view.IsView || view.SQLName != "ducklake.main.current_sales" {
		t.Fatalf("view mapping = %#v", view)
	}
	if table := fresh.Tables["finance.ledger"]; table == nil || table.SQLName != "ducklake.finance.ledger" {
		t.Fatalf("schema mapping = %#v", table)
	}
	publicSchema, err := json.Marshal(fresh)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(publicSchema), "ducklake.main") || strings.Contains(string(publicSchema), "ducklake.finance") {
		t.Fatalf("public schema exposed physical names: %s", publicSchema)
	}
	columns, rows, err := executor.ExecuteContext(t.Context(), r.DB(), fresh, `EVALUATE SUMMARIZECOLUMNS(sales[id], "Total", SUM(sales[amount]))`)
	if err != nil {
		t.Fatal(err)
	}
	if len(columns) != 2 || len(rows) != 1 {
		t.Fatalf("query result = %#v %#v", columns, rows)
	}
	if _, rows, err := executor.ExecuteContext(t.Context(), r.DB(), fresh, `EVALUATE SUMMARIZECOLUMNS(current_sales[id])`); err != nil || len(rows) != 1 {
		t.Fatalf("view query result = %#v, %v", rows, err)
	}
	if _, rows, err := executor.ExecuteContext(t.Context(), r.DB(), fresh, `EVALUATE SUMMARIZECOLUMNS(finance.ledger[id])`); err != nil || len(rows) != 1 {
		t.Fatalf("non-main query result = %#v, %v", rows, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "dux.sqlite")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "ducklake.sqlite")); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(filepath.Join(dir, "ducklake")); err != nil || !info.IsDir() {
		t.Fatalf("ducklake directory: %v", err)
	}
	if matches, err := filepath.Glob(filepath.Join(dir, "*.duckdb")); err != nil || len(matches) != 0 {
		t.Fatalf("native DuckDB files = %v, %v", matches, err)
	}
}

func TestReaderBootstrapDoesNotCreateMissingDuckLake(t *testing.T) {
	dir := t.TempDir()
	catalog := filepath.Join(dir, "ducklake.sqlite")
	if _, err := Bootstrap(dir, filepath.Join(dir, "dux.sqlite"), catalog, filepath.Join(dir, "ducklake"), filepath.Join(dir, "missing.toml"), false); err == nil {
		t.Fatal("reader bootstrap created a missing ducklake")
	}
	if _, err := os.Stat(catalog); !os.IsNotExist(err) {
		t.Fatalf("catalog exists after reader failure: %v", err)
	}
}

func TestRefreshReportsBrokenSemanticObjectsAndRecovers(t *testing.T) {
	dir := t.TempDir()
	runtime, err := Bootstrap(dir, filepath.Join(dir, "dux.sqlite"), filepath.Join(dir, "ducklake.sqlite"), filepath.Join(dir, "ducklake"), filepath.Join(dir, "missing.toml"), true)
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	if _, err := runtime.Owner.Conn().ExecContext(t.Context(), `CREATE TABLE sales(id INTEGER, customer_id INTEGER, amount DOUBLE); CREATE TABLE customers(id INTEGER); INSERT INTO sales VALUES (1, 1, 10)`); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Metadata.SaveRelationship("sales", "customer_id", "customers", "id", false); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Metadata.SaveMeasure("sales", "Revenue", "SUM(sales[amount])", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.RefreshSchema(); err != nil {
		t.Fatal(err)
	}
	if _, _, warning := runtime.RefreshStatus(); warning != "" {
		t.Fatalf("initial warning = %q", warning)
	}
	if _, err := runtime.Owner.Conn().ExecContext(t.Context(), `DROP TABLE customers; ALTER TABLE sales DROP COLUMN amount`); err != nil {
		t.Fatal(err)
	}
	degraded, err := runtime.RefreshSchema()
	if err != nil {
		t.Fatal(err)
	}
	_, refreshError, warning := runtime.RefreshStatus()
	if refreshError != "" || !strings.Contains(warning, "relationship") || !strings.Contains(warning, "measure sales[Revenue]") {
		t.Fatalf("degraded status error=%q warning=%q", refreshError, warning)
	}
	if _, rows, err := executor.ExecuteContext(t.Context(), runtime.DB(), degraded, `EVALUATE SUMMARIZECOLUMNS(sales[id], "Rows", COUNTROWS(sales))`); err != nil || len(rows) != 1 {
		t.Fatalf("unaffected query rows=%#v, %v", rows, err)
	}
	if _, err := runtime.Owner.Conn().ExecContext(t.Context(), `ALTER TABLE sales ADD COLUMN amount DOUBLE; CREATE TABLE customers(id INTEGER)`); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.RefreshSchema(); err != nil {
		t.Fatal(err)
	}
	if _, refreshError, warning := runtime.RefreshStatus(); refreshError != "" || warning != "" {
		t.Fatalf("recovered status error=%q warning=%q", refreshError, warning)
	}
}

func TestRefreshFailureMarksDegradedAndRecovers(t *testing.T) {
	dir := t.TempDir()
	tomlPath := filepath.Join(dir, "dux.toml")
	runtime, err := Bootstrap(dir, filepath.Join(dir, "dux.sqlite"), filepath.Join(dir, "ducklake.sqlite"), filepath.Join(dir, "ducklake"), tomlPath, true)
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	if err := os.WriteFile(tomlPath, []byte("[[measure]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.RefreshSchema(); err == nil {
		t.Fatal("invalid semantic metadata refreshed successfully")
	}
	if _, refreshError, _ := runtime.RefreshStatus(); !strings.Contains(refreshError, "load dux.toml") {
		t.Fatalf("refresh error = %q", refreshError)
	}
	if err := os.Remove(tomlPath); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.RefreshSchema(); err != nil {
		t.Fatal(err)
	}
	if _, refreshError, _ := runtime.RefreshStatus(); refreshError != "" {
		t.Fatalf("refresh did not recover: %q", refreshError)
	}
}
