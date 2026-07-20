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

# --fail-with-body prints the server's JSON on any error (409 carries
# currentEtag, 422 the validation message) and exits nonzero — see the
# skill's api.md for how to react to each status.
curl -sS --fail-with-body \
  -X PUT "$SERVER/api/dash/dashboards/$DASH_PATH" \
  -H "If-Match: $MATCH" -H 'Content-Type: application/json' \
  --data-binary @"$FILE"
echo
echo "published: $SERVER/dash/$DASH_PATH"
