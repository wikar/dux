package executor_test

import (
	"database/sql"
	"testing"

	_ "github.com/duckdb/duckdb-go/v2"

	"github.com/danielwikar/dux/semantic"
)

// setupSchemaViewDB creates an in-memory DuckDB with a view in the default
// schema, a table in a non-default schema, and an attached database with a
// table in a non-default schema (three-part name).
func setupSchemaViewDB(t *testing.T) (*sql.DB, *semantic.Schema) {
	t.Helper()

	db, err := sql.Open("duckdb", "")
	if err != nil {
		t.Fatalf("open duckdb: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	ddl := []string{
		`CREATE TABLE orders (id INTEGER, product VARCHAR, amount DOUBLE)`,
		`INSERT INTO orders VALUES (1, 'Widget', 50.0), (2, 'Gadget', 150.0), (3, 'Gadget', 250.0)`,
		`CREATE VIEW big_orders AS SELECT * FROM orders WHERE amount > 100`,
		`CREATE SCHEMA sales`,
		`CREATE TABLE sales.Customer (customer_id INTEGER, name VARCHAR)`,
		`INSERT INTO sales.Customer VALUES (1, 'Alice'), (2, 'Bob')`,
		`ATTACH ':memory:' AS analytics`,
		`CREATE SCHEMA analytics.dim`,
		`CREATE TABLE analytics.dim.Region (region_id INTEGER, region VARCHAR)`,
		`INSERT INTO analytics.dim.Region VALUES (1, 'North'), (2, 'South'), (3, 'East')`,
	}
	for _, s := range ddl {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("setup: %v — %s", err, s)
		}
	}

	schema, err := semantic.IntrospectDuckDB(db)
	if err != nil {
		t.Fatalf("introspect: %v", err)
	}
	return db, schema
}

func TestIntrospectViewsAndSchemas(t *testing.T) {
	_, schema := setupSchemaViewDB(t)

	// A view in the default schema is keyed like a table and flagged IsView.
	view, ok := schema.Tables["big_orders"]
	if !ok {
		t.Fatalf("view big_orders not introspected; keys: %v", tableKeys(schema))
	}
	if !view.IsView {
		t.Errorf("big_orders should have IsView = true")
	}
	if base := schema.Tables["orders"]; base == nil || base.IsView {
		t.Errorf("orders should be introspected as a base table")
	}

	// A table in a non-default schema of the primary db is keyed schema.table.
	if _, ok := schema.Tables["sales.Customer"]; !ok {
		t.Fatalf("sales.Customer not introspected; keys: %v", tableKeys(schema))
	}

	// A table in a non-default schema of an attached db is keyed db.schema.table.
	if _, ok := schema.Tables["analytics.dim.Region"]; !ok {
		t.Fatalf("analytics.dim.Region not introspected; keys: %v", tableKeys(schema))
	}
}

func TestQueryViewAndSchemaQualifiedTables(t *testing.T) {
	db, schema := setupSchemaViewDB(t)

	// Query a view exactly like a table.
	_, rows := run(t, db, schema, `EVALUATE SUMMARIZECOLUMNS(big_orders[product], "N", COUNT(big_orders[id]))`)
	if len(rows) != 1 || rows[0]["product"] != "Gadget" {
		t.Errorf("view query rows = %v", rows)
	}

	// Two-part schema-qualified table in the primary db.
	_, rows = run(t, db, schema, `EVALUATE SUMMARIZECOLUMNS(sales.Customer[name])`)
	if len(rows) != 2 {
		t.Errorf("sales.Customer rows = %v", rows)
	}

	// Three-part db.schema.table name.
	_, rows = run(t, db, schema, `EVALUATE SUMMARIZECOLUMNS(analytics.dim.Region[region], "N", COUNT(analytics.dim.Region[region_id]))`)
	if len(rows) != 3 {
		t.Errorf("analytics.dim.Region rows = %v", rows)
	}
}

func tableKeys(s *semantic.Schema) []string {
	keys := make([]string, 0, len(s.Tables))
	for k := range s.Tables {
		keys = append(keys, k)
	}
	return keys
}
