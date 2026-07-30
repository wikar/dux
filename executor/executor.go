// Package executor ties together the parser, semantic resolver, emitter, and
// go-duckdb to execute a DUX query end-to-end.
package executor

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	duckdb "github.com/duckdb/duckdb-go/v2"

	"github.com/danielwikar/dux/emitter"
	"github.com/danielwikar/dux/parser"
	"github.com/danielwikar/dux/semantic"
)

// QueryTimeout is the maximum duration allowed for a single DUX query,
// including VAR materialisation. The clock starts once the query holds a
// DuckDB connection, so time spent queueing for one never shortens the
// execution budget. Queries that exceed this are interrupted.
// Overridable at startup via the --query-timeout flag (see bootstrap).
var QueryTimeout = 60 * time.Second

// AdmissionTimeout bounds how long a query waits for a free DuckDB connection
// before the server sheds it. The connection pool is small (see
// internal/ducklake), so under load this queue is where requests pile up:
// shedding early keeps callers from holding a socket for a full QueryTimeout
// only to learn no work was ever attempted.
// Overridable at startup via the --admission-timeout flag (see bootstrap).
var AdmissionTimeout = 5 * time.Second

// ErrServerBusy reports that no DuckDB connection became available within
// AdmissionTimeout. It is returned before any SQL runs, so the query had no
// effect and the caller may retry it safely.
var ErrServerBusy = errors.New("server busy: no query connection available")

// Acquire borrows a DuckDB connection, bounding the wait by AdmissionTimeout.
// It reports ErrServerBusy when the pool stays full for that long, and the
// caller's own context error when the caller gave up first.
//
// The returned connection carries no deadline: start the execution budget
// after this returns so queue time never shortens it.
func Acquire(ctx context.Context, db *sql.DB) (*sql.Conn, error) {
	acquireCtx, cancel := context.WithTimeout(ctx, AdmissionTimeout)
	defer cancel()
	conn, err := db.Conn(acquireCtx)
	if err != nil {
		if ctx.Err() != nil {
			// The caller gave up (disconnect or their own deadline).
			return nil, fmt.Errorf("acquire connection: %w", err)
		}
		return nil, ErrServerBusy
	}
	return conn, nil
}

// QueryError is a query-pipeline failure with its pipeline stage and, when
// the underlying error carries one, a 1-based source position.
type QueryError struct {
	Stage   string // "parse", "resolve", "emit", or "execute query"
	Message string // error text, without stage prefix or position
	Line    int    // 0 when unknown
	Column  int
	err     error
}

func (e *QueryError) Error() string {
	if e.Line > 0 {
		return fmt.Sprintf("%s: %s (line %d, column %d)", e.Stage, e.Message, e.Line, e.Column)
	}
	return e.Stage + ": " + e.Message
}

func (e *QueryError) Unwrap() error { return e.err }

// queryErr wraps err as a QueryError, extracting the source position from
// parse and semantic errors when available.
func queryErr(stage string, err error) *QueryError {
	qe := &QueryError{Stage: stage, Message: err.Error(), err: err}
	if line, col, msg, ok := parser.ErrorDetails(err); ok {
		qe.Line, qe.Column, qe.Message = line, col, msg
	}
	var se *semantic.SemanticError
	if errors.As(err, &se) && se.Line > 0 {
		qe.Line, qe.Column = se.Line, se.Column
	}
	return qe
}

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
	return ExecuteFiltered(db, schema, input, nil)
}

// ExecuteFiltered is Execute with external filters applied to the query's
// outermost filter context after parsing (see ApplyExternalFilters).
func ExecuteFiltered(db *sql.DB, schema *semantic.Schema, input string, filters []ExternalFilter) ([]string, []map[string]any, error) {
	cols, rows, err := ExecuteFilteredContext(context.Background(), db, schema, input, filters)
	if err != nil {
		return nil, nil, err
	}
	rowMaps := make([]map[string]any, len(rows))
	for i, values := range rows {
		rowMaps[i] = make(map[string]any, len(cols))
		for j, col := range cols {
			rowMaps[i][col] = values[j]
		}
	}
	return cols, rowMaps, nil
}

// ExecuteContext is Execute with caller cancellation. Rows follow the returned
// column order, avoiding an intermediate map allocation per result row.
func ExecuteContext(ctx context.Context, db *sql.DB, schema *semantic.Schema, input string) ([]string, [][]any, error) {
	return ExecuteFilteredContext(ctx, db, schema, input, nil)
}

// ExecuteFilteredContext is ExecuteContext with external filters applied.
func ExecuteFilteredContext(ctx context.Context, db *sql.DB, schema *semantic.Schema, input string, filters []ExternalFilter) ([]string, [][]any, error) {
	q, err := parser.Parse(input)
	if err != nil {
		return nil, nil, queryErr("parse", err)
	}

	if err := ApplyExternalFilters(q, schema, filters); err != nil {
		return nil, nil, &QueryError{Stage: "filters", Message: err.Error(), err: err}
	}

	r := &semantic.Resolver{Schema: schema}
	if err := r.Resolve(q); err != nil {
		return nil, nil, queryErr("resolve", err)
	}

	em := &emitter.Emitter{Schema: schema, Measures: r.EffectiveMeasures(), Resolution: r.Result()}

	// Pin a single connection so that session temp tables created for VAR
	// bindings are visible across all statements.
	//
	// Admission and execution get separate budgets. A request that waited out
	// a long queue would otherwise start executing with whatever remained of
	// QueryTimeout and be interrupted mid-flight, spending a scarce connection
	// on work it could never finish.
	conn, err := Acquire(ctx, db)
	if err != nil {
		return nil, nil, err
	}
	defer conn.Close()

	// Execution budget starts here, now that the connection is held.
	ctx, cancel := context.WithTimeout(ctx, QueryTimeout)
	defer cancel()

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
			return nil, nil, queryErr("emit", fmt.Errorf("classify VAR %s: %w", v.Name, err))
		}

		if isTable {
			createSQL, err := em.EmitVarCreate(v.Name, v.Expr)
			if err != nil {
				return nil, nil, queryErr("emit", fmt.Errorf("VAR %s: %w", v.Name, err))
			}
			if _, err := conn.ExecContext(ctx, createSQL); err != nil {
				return nil, nil, queryErr("execute", fmt.Errorf("create temp table for VAR %s: %w", v.Name, err))
			}
			created = append(created, v.Name)
		} else {
			scalarSQL, err := em.EmitScalarQuery(v.Expr)
			if err != nil {
				return nil, nil, queryErr("emit", fmt.Errorf("scalar VAR %s: %w", v.Name, err))
			}
			row := conn.QueryRowContext(ctx, scalarSQL)
			var val any
			if err := row.Scan(&val); err != nil {
				return nil, nil, queryErr("execute", fmt.Errorf("scalar VAR %s: %w", v.Name, err))
			}
			em.ScalarVars[strings.ToLower(v.Name)] = val
		}
	}

	sqlStr, err := em.Emit(q)
	if err != nil {
		return nil, nil, queryErr("emit", err)
	}

	rows, err := conn.QueryContext(ctx, sqlStr)
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, nil, queryErr("execute query",
				fmt.Errorf("interrupted after exceeding the %s query timeout", QueryTimeout))
		}
		return nil, nil, queryErr("execute query", err)
	}
	defer rows.Close()
	return scanRows(rows)
}

// scanRows reads all rows from a *sql.Rows result set in driver column order.
func scanRows(rows *sql.Rows) ([]string, [][]any, error) {
	cols, err := rows.Columns()
	if err != nil {
		return nil, nil, fmt.Errorf("get columns: %w", err)
	}

	var results [][]any
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, nil, fmt.Errorf("scan row: %w", err)
		}
		for i := range vals {
			vals[i] = normaliseValue(vals[i])
		}
		results = append(results, vals)
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
