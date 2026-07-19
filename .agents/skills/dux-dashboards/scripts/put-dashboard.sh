#!/usr/bin/env bash
# Publish a dashboard JSON file to a duxd server (create-or-overwrite).
#
# Usage: put-dashboard.sh [-s http://host:port] [-e etag] <path> <file.json>
#   path      dashboard identity, e.g. sales/overview (no .json)
#   -e etag   safe update with If-Match: <etag> instead of the default
#             unconditional If-Match: * overwrite
# Requires: curl.
set -euo pipefail

SERVER="${DUXD_URL:-http://localhost:8080}"
MATCH="*"
while getopts "s:e:" opt; do
  case "$opt" in
    s) SERVER="$OPTARG" ;;
    e) MATCH="$OPTARG" ;;
    *) echo "usage: $0 [-s server-url] [-e etag] <path> <file.json>" >&2; exit 2 ;;
  esac
done
shift $((OPTIND - 1))

[ $# -ge 2 ] || { echo "usage: $0 [-s server-url] [-e etag] <path> <file.json>" >&2; exit 2; }
DASH_PATH="$1"
FILE="$2"
[ -f "$FILE" ] || { echo "error: '$FILE' not found" >&2; exit 1; }

HTTP_CODE=$(curl -sS -o /tmp/dux-dash-out.$$ -w '%{http_code}' \
  -X PUT "$SERVER/api/dash/dashboards/$DASH_PATH" \
  -H "If-Match: $MATCH" -H 'Content-Type: application/json' \
  --data-binary @"$FILE")

cat /tmp/dux-dash-out.$$
echo
rm -f /tmp/dux-dash-out.$$
case "$HTTP_CODE" in
  200|201) echo "published: $SERVER/dash/$DASH_PATH" ;;
  409) echo "conflict — someone saved in between; re-GET and retry (or use If-Match: *)" >&2; exit 1 ;;
  422) echo "document failed schema validation (see error above)" >&2; exit 1 ;;
  *) echo "publish failed (HTTP $HTTP_CODE)" >&2; exit 1 ;;
esac
