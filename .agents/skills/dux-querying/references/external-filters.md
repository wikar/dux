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
| `op` | all | One of `in`, `between`, `=`, `!=`, `<`, `<=`, `>`, `>=`, `contains` |
| `values` | `in` | Array of scalars |
| `value` | `=` `!=` `<` `<=` `>` `>=` `contains` | Single scalar |
| `from`, `to` | `between` | Inclusive bounds |

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
