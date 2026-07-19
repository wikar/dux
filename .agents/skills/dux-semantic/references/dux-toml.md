# dux.toml format

One portable file carrying the entire semantic model. Round-trips through
`GET /export` / `POST /import` (import **replaces** the whole model). The
CLI and server also load it at startup via `--toml` (default `dux.toml`),
and `--import` / `--export` flags do one-shot conversions.

## Relationships

```toml
[[relationship]]
from_table  = "atp.matches"     # many side
from_column = "winner_id"
to_table    = "atp.players"     # one side
to_column   = "player_id"

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
table      = "atp.matches"
name       = "Total Matches"
expression = "COUNT(atp.matches[match_num])"

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
table = "atp.rounds"

# Hide a single column
[[hidden]]
table  = "atp.matches"
column = "winner_id"
```

Hidden objects stay queryable — presentation only.

## Date table

```toml
[[date_table]]
table  = "dates"
column = "date"
```

Only one table can be the date table. On it, time-intelligence functions
clear all filters before applying date ranges.

## Round-trip example

```sh
curl http://localhost:8080/export > dux.toml
# ... edit dux.toml ...
curl -X POST http://localhost:8080/import --data-binary @dux.toml
```
