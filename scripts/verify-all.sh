#!/usr/bin/env bash
# Runs everything that could not be run in the build sandbox, in one go, against DISPOSABLE databases.
#
#   TEST_DATABASE_URL      owner connection to an EMPTY, disposable database (the tests TRUNCATE tables!)
#   TEST_APP_DATABASE_URL  the restricted login (certos_app) on the SAME database, so row-level security is exercised
#   TEST_CHROME_PATH       optional: path to Chrome/Chromium to include the real PDF rendering tests
#
# Example (compose Postgres, host port 5432; create the throw-away database first):
#   docker compose exec db createdb -U postgres certos_verify
#   TEST_DATABASE_URL=postgres://postgres:postgres@localhost:5432/certos_verify \
#   TEST_APP_DATABASE_URL=postgres://certos_app:CHANGE_ME@localhost:5432/certos_verify \
#   scripts/verify-all.sh
#
# The restricted role must exist: after the first run of the migrations, `api db provision-runtime-role`
# (with MIGRATE_DATABASE_URL pointing at the same database) creates certos_app; the script does this for you
# when APP_DB_PASSWORD is set.
set -euo pipefail
cd "$(dirname "$0")/.."

: "${TEST_DATABASE_URL:?set TEST_DATABASE_URL to a disposable database (owner connection)}"
: "${TEST_APP_DATABASE_URL:?set TEST_APP_DATABASE_URL to the restricted certos_app login on the same database}"
case "$TEST_DATABASE_URL" in
  *prod*|*production*) echo "Refusing: TEST_DATABASE_URL looks like a production database." >&2; exit 2 ;;
esac

step() { printf '\n==== %s\n' "$*"; }

step "Go: migrations and role provisioning"
(cd services/api
  MIGRATE_DATABASE_URL="$TEST_DATABASE_URL" DATABASE_URL="$TEST_APP_DATABASE_URL" go run ./cmd/api migrate up
  if [ -n "${APP_DB_PASSWORD:-}" ]; then
    MIGRATE_DATABASE_URL="$TEST_DATABASE_URL" DATABASE_URL="$TEST_APP_DATABASE_URL" APP_DB_PASSWORD="$APP_DB_PASSWORD" go run ./cmd/api db provision-runtime-role
  fi)

step "Go: vet and the whole test suite against real PostgreSQL (-race, one package at a time)"
(cd services/api
  test -z "$(gofmt -l .)" || { echo "gofmt needed:"; gofmt -l .; exit 1; }
  go vet ./...
  TEST_DATABASE_URL="$TEST_DATABASE_URL" TEST_APP_DATABASE_URL="$TEST_APP_DATABASE_URL" \
  TEST_CHROME_PATH="${TEST_CHROME_PATH:-}" go test -race -p 1 ./...)

step "Web: install, typecheck, lint, tests, build"
(cd apps/web
  npm ci
  npm run typecheck
  npm run lint
  npm test
  npm run build)

printf '\nAll verification steps passed.\nStill manual: a real email send, a phone QR scan, a restore rehearsal, a real XLSX import, a first deployment.\nSee docs/verification-checklist.md.\n'
