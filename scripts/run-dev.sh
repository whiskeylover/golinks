#!/bin/sh
set -eu

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
REPO_DIR=$(CDPATH= cd -- "$SCRIPT_DIR/.." && pwd)

cd "$REPO_DIR"
exec go run ./cmd/golinks serve \
	-addr "${GOLINKS_ADDR:-:8080}" \
	-db "${GOLINKS_DB:-./data/golinks.db}"

