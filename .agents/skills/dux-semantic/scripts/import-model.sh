#!/usr/bin/env bash
# Import a dux.toml into a running duxd — REPLACES the whole semantic model
# (measures, relationships, hidden designations, date table).
#
# Usage: import-model.sh [-s http://host:port] dux.toml
# Requires: curl.
set -euo pipefail

SERVER="${DUXD_URL:-http://localhost:8080}"
while getopts "s:" opt; do
  case "$opt" in
    s) SERVER="$OPTARG" ;;
    *) echo "usage: $0 [-s server-url] dux.toml" >&2; exit 2 ;;
  esac
done
shift $((OPTIND - 1))

[ $# -ge 1 ] || { echo "usage: $0 [-s server-url] dux.toml" >&2; exit 2; }
[ -f "$1" ] || { echo "error: '$1' not found" >&2; exit 1; }

curl -sSf -X POST "$SERVER/import" --data-binary @"$1"
echo "model imported from $1 (full replace)"
