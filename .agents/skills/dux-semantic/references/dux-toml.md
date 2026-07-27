# dux.toml format

One portable file carrying the entire semantic model. Round-trips through
`GET /export` / `POST /import` (import **replaces** the whole model). The
CLI and server also load it at startup via `--toml` (default `dux.toml`),
and `--import` / `--export` flags do one-shot conversions.

## Relationships

```toml
[[relationship]]
from_table  = "Sales"       # many side
from_column = "ProductKey"
to_table    = "Product"     # one side
to_column   = "ProductKey"

# Bidirectional — filter context propagates both ways (bridge pattern)
[[relationship]]
from_table     = "Bridge"
from_column    = "DimBKey"
to_table       = "DimB"
to_column      = "DimBKey"
bidirectional  = true
```

## Measures

```toml
[[measure]]
table      = "Sales"
name       = "Total Revenue"
expression = "SUM(Sales[_NetSalesAmount])"

# Optional display format (structured enum, validated server-side)
[measure.format]
kind     = "compact"            # number | decimal | percent | currency | compact
# decimals = 1                  # 0-10; omit for the client default
# currency = "SEK"              # ISO 4217; required iff kind = "currency"
```

## Hidden objects

```toml
# Hide a whole table or view
[[hidden]]
table = "Venue"

# Hide a single column
[[hidden]]
table  = "Sales"
column = "OrderId"
```

Hidden objects stay queryable — presentation only.

## Date table

```toml
[[date_table]]
table  = "Date"
column = "Date"
```

Only one table can be the date table. On it, time-intelligence functions
clear all filters before applying date ranges.

## Round-trip example

```sh
curl http://localhost:8080/export > dux.toml
# ... edit dux.toml ...
curl -X POST http://localhost:8080/import --data-binary @dux.toml
```
