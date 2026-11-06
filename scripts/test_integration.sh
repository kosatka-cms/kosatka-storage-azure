#!/usr/bin/env bash
# The integration tests on Azure Blob Storage's emulator, Azurite (go test
# -tags integration): starts it from docker-compose.test.yml, runs the
# tests, stops it. Docker needed.
set -euo pipefail
cd "$(dirname "$0")/.."

compose="docker compose -f docker-compose.test.yml -p kosatka-storage-azure-test"
trap '$compose down -v >/dev/null 2>&1' EXIT
$compose up -d --wait

export KOSATKA_TEST_AZURE="http://127.0.0.1:31000/devstoreaccount1"
go test -tags integration -count=1 "$@" ./...
