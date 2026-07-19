#!/usr/bin/env bash
# Download a dashboard's JSON file verbatim (backup / edit / restore loop).
#
# Usage: get-dashboard.sh [-s http://host:port] <path> [output.json]
# Requires: curl.
set -euo pipefail

SERVER="${DUXD_URL:-http://localhost:8080}"
while getopts "s:" opt; do
  case "$opt" in
    s) SERVER="$OPTARG" ;;
    *) echo "usage: $0 [-s server-url] <path> [output.json]" >&2; exit 2 ;;
  esac
done
shift $((OPTIND - 1))

[ $# -ge 1 ] || { echo "usage: $0 [-s server-url] <path> [output.json]" >&2; exit 2; }
DASH_PATH="$1"
OUT="${2:-$(basename "$DASH_PATH").json}"

curl -sSf "$SERVER/api/dash/dashboards/$DASH_PATH?raw=1" -o "$OUT"
echo "saved to $OUT"
