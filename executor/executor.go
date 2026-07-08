// Package executor ties together the parser, semantic resolver, emitter, and
// go-duckdb to execute a DUX query end-to-end.
package executor

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	duckdb "github.com/duckdb/duckdb-go/v2"

	"github.com/danielwikar/dux/emitter"
	"github.com/danielwikar/dux/parser"
	"github.com/danielwikar/dux/semantic"
)

// QueryTimeout is the maximum duration allowed for a single DUX query,
// including VAR materialisation. Queries that exceed this are cancelled.
const QueryTimeout = 60 * time.Second

// Execute parses the DUX input, resolves it against schema, emits DuckDB SQL,
// and runs it against db. It returns the ordered column names followed by all
// rows as maps keyed by column name. Column order matches the SELECT list of
// the emitted SQL.
//
// db must be opened with the "duckdb" driver (github.com/duckdb/duckdb-go/v2),
// which requires CGO.
//
// When the query contains VAR bindings each binding is materialised as a
// session-scoped temporary table on a dedicated connection before the final
// RETURN expression is evaluated. Temp tables are dropped on all code paths.
func Execute(db *sql.DB, schema *semantic.Schema, input string) ([]string, []map[string]any, error) {
	q, err := parser.Parse(input)
	if err != nil {
		return nil, nil, fmt.Errorf("parse: %w", err)
	}

	r := &semantic.Resolver{Schema: schema}
	if err := r.Resolve(q); err != nil {
		return nil, nil, fmt.Errorf("resolve: %w", err)
	}

	em := &emitter.Emitter{Schema: schema, Measures: r.EffectiveMeasures()}

	// Pin a single connection so that session temp tables created for VAR
	// bindings are visible across all statements.
	ctx, cancel := context.WithTimeout(context.Background(), QueryTimeout)
	defer cancel()
	conn, err := db.Conn(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("acquire connection: %w", err)
	}
	defer conn.Close()

	// Initialise scalar var map so the emitter can substitute values inline.
	em.ScalarVars = make(map[string]any)

	// Track created temp table names for cleanup.
	created := make([]string, 0, len(q.Evaluate.Vars))
	defer func() {
		for i := len(created) - 1; i >= 0; i-- {
			_, _ = conn.ExecContext(ctx, "DROP TABLE IF EXISTS "+created[i])
		}
	}()

	for _, v := range q.Evaluate.Vars {
		isTable, err := em.IsTableExpr(v.Expr)
		if err != nil {
			return nil, nil, fmt.Errorf("classify VAR %s: %w", v.Name, err)
		}

		if isTable {
			createSQL, err := em.EmitVarCreate(v.Name, v.Expr)
			if err != nil {
				return nil, nil, fmt.Errorf("emit VAR %s: %w", v.Name, err)
			}
			if _, err := conn.ExecContext(ctx, createSQL); err != nil {
				return nil, nil, fmt.Errorf("create temp table for VAR %s: %w", v.Name, err)
			}
			created = append(created, v.Name)
		} else {
			scalarSQL, err := em.EmitScalarQuery(v.Expr)
			if err != nil {
				return nil, nil, fmt.Errorf("emit scalar VAR %s: %w", v.Name, err)
			}
			row := conn.QueryRowContext(ctx, scalarSQL)
			var val any
			if err := row.Scan(&val); err != nil {
				return nil, nil, fmt.Errorf("execute scalar VAR %s: %w", v.Name, err)
			}
			em.ScalarVars[strings.ToLower(v.Name)] = val
		}
	}

	sqlStr, err := em.Emit(q)
	if err != nil {
		return nil, nil, fmt.Errorf("emit: %w", err)
	}

	rows, err := conn.QueryContext(ctx, sqlStr)
	if err != nil {
		return nil, nil, fmt.Errorf("execute query: %w", err)
	}
	defer rows.Close()
	return scanRows(rows)
}

// scanRows reads all rows from a *sql.Rows result set into []map[string]any,
// also returning the ordered column names as reported by the driver.
func scanRows(rows *sql.Rows) ([]string, []map[string]any, error) {
	cols, err := rows.Columns()
	if err != nil {
		return nil, nil, fmt.Errorf("get columns: %w", err)
	}

	var results []map[string]any
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, nil, fmt.Errorf("scan row: %w", err)
		}
		row := make(map[string]any, len(cols))
		for i, col := range cols {
			row[col] = normaliseValue(vals[i])
		}
		results = append(results, row)
	}
	return cols, results, rows.Err()
}

// normaliseValue converts DuckDB driver-specific types that do not JSON-marshal
// cleanly into standard Go primitives that the frontend can consume directly.
func normaliseValue(v any) any {
	if v == nil {
		return nil
	}
	switch t := v.(type) {
	case duckdb.Decimal:
		// Return as an exact decimal string (e.g. "1234.5600") to preserve
		// full precision. float64 would introduce rounding errors for DECIMAL/NUMERIC.
		return t.String()
	case []byte:
		// BLOB — return as hex string to avoid base64 surprises.
		return fmt.Sprintf("%x", t)
	default:
		return v
	}
}
