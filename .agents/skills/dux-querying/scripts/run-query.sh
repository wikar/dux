#!/usr/bin/env bash
# Run a DUX query against a duxd server.
#
# Usage:
#   run-query.sh [-s http://host:port] query.dux
#   echo 'EVALUATE atp.matches' | run-query.sh [-s ...]
#
# Requires: curl. Output is the raw JSON result ({"columns":[...],"rows":[...]})
# or the server's JSON error ({"error":"...","line":N,"column":N,"stage":"..."}).
set -euo pipefail

SERVER="${DUXD_URL:-http://localhost:8080}"
while getopts "s:" opt; do
  case "$opt" in
    s) SERVER="$OPTARG" ;;
    *) echo "usage: $0 [-s server-url] [query-file]" >&2; exit 2 ;;
  esac
done
shift $((OPTIND - 1))

if [ $# -ge 1 ]; then
  [ -f "$1" ] || { echo "error: query file '$1' not found" >&2; exit 1; }
  QUERY=$(cat "$1")
else
  QUERY=$(cat)
fi
[ -n "$QUERY" ] || { echo "error: empty query" >&2; exit 1; }

HTTP_CODE=$(curl -sS -o /tmp/dux-query-out.$$ -w '%{http_code}' \
  -X POST "$SERVER/query" \
  -H 'Content-Type: text/plain' \
  --data-binary "$QUERY")

cat /tmp/dux-query-out.$$
echo
rm -f /tmp/dux-query-out.$$
[ "$HTTP_CODE" = "200" ] || { echo "query failed (HTTP $HTTP_CODE)" >&2; exit 1; }
