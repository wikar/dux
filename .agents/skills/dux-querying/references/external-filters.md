# External filters — the POST /query JSON envelope

Inject filters into a query from outside the query text (slicer semantics —
the DUX stays untouched, filters compose with AND). Send JSON instead of
plain text:

```
POST /query
Content-Type: application/json
```

```json
{
  "query": "EVALUATE SUMMARIZECOLUMNS(atp.matches[surface], \"Matches\", COUNT(atp.matches[match_num]))",
  "filters": [
    { "table": "atp.matches", "column": "tourney_level", "op": "in", "values": ["G", "M"] },
    { "table": "atp.matches", "column": "draw_size", "op": ">=", "value": 32 }
  ]
}
```

## Filter shape

| Field | Used by | Notes |
|-------|---------|-------|
| `table` | all | Dot-qualified table name |
| `column` | all | Column name |
| `op` | all | One of `in`, `between`, `=`, `!=`, `<`, `<=`, `>`, `>=`, `contains`, `in_tuples` |
| `values` | `in` | Array of scalars |
| `value` | `=` `!=` `<` `<=` `>` `>=` `contains` | Single scalar |
| `from`, `to` | `between` | Inclusive bounds |
| `columns`, `tuples` | `in_tuples` | Multi-column set membership (OR-of-tuples) |

### `in_tuples` — multi-column set membership

Selecting several multi-dimensional points at once (e.g. a multi-select of
clustered/stacked chart marks) needs an OR of column tuples, which a flat AND
of per-column filters cannot express. `in_tuples` carries the columns and the
value rows; `table`/`column`/`values` are omitted.

```json
{
  "op": "in_tuples",
  "columns": [
    { "table": "sales", "column": "region" },
    { "table": "sales", "column": "product" }
  ],
  "tuples": [ ["North", "Widget"], ["South", "Gadget"] ]
}
```

Compiles to a multi-column `TREATAS` emitted as OR-of-ANDs
(`(region='North' AND product='Widget') OR (region='South' AND product='Gadget')`).
**Constraint:** all `columns` must belong to **one table** so the predicate
routes to a single cluster; a cross-table request is rejected (callers should
fall back to per-column `in` filters, an approximate cross-product).

Rules:

- Give numeric columns **numbers**, not strings — values are typed as sent.
- Multiple filters combine with AND. One filter per column is the norm.
- Filters propagate through the model's relationships exactly like
  `TREATAS` arguments: they reach every measure whose table is connected
  (one side → many side; both directions across bidirectional edges).
- A filter that reaches no measure table is an error, same as in-query
  filters.
- `contains` is a case-insensitive substring match.

The response shape is identical to the plain-text form:
`{"columns": [...], "rows": [[...], ...]}`.
