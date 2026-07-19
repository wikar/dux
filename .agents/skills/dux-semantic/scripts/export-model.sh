#!/usr/bin/env bash
# Export the DUX semantic model (measures, relationships, hidden, date table)
# to a dux.toml file.
#
# Usage: export-model.sh [-s http://host:port] [output.toml]
# Requires: curl.
set -euo pipefail

SERVER="${DUXD_URL:-http://localhost:8080}"
while getopts "s:" opt; do
  case "$opt" in
    s) SERVER="$OPTARG" ;;
    *) echo "usage: $0 [-s server-url] [output.toml]" >&2; exit 2 ;;
  esac
done
shift $((OPTIND - 1))
OUT="${1:-dux.toml}"

curl -sSf "$SERVER/export" -o "$OUT"
echo "model exported to $OUT"
