#!/usr/bin/env bash
# Runs INSIDE the toolbox container (never on the host). Usage: verify.sh [go|web]   (no argument = both)
set -euo pipefail
cd /repo
step() { printf '\n==== %s\n' "$*"; }

run_go() {
  cd /repo/services/api
  [ -f go.sum ] || { step "go.sum missing: go mod tidy (commit the generated file)"; go mod tidy; }
  go mod download
  step "Go: migrations and restricted-role provisioning"
  MIGRATE_DATABASE_URL="$TEST_DATABASE_URL" DATABASE_URL="$TEST_APP_DATABASE_URL" go run ./cmd/api migrate up
  MIGRATE_DATABASE_URL="$TEST_DATABASE_URL" DATABASE_URL="$TEST_APP_DATABASE_URL" go run ./cmd/api db provision-runtime-role
  step "Go: gofmt, vet, full suite on real PostgreSQL with row-level security (-race, one package at a time, real Chromium)"
  test -z "$(gofmt -l .)" || { echo "gofmt needed for:"; gofmt -l .; exit 1; }
  go vet ./...
  go test -race -p 1 ./...
}

run_web() {
  cd /repo/apps/web
  step "Web: install, typecheck, lint, tests, build"
  if [ -f package-lock.json ]; then npm ci --no-audit --no-fund; else npm install --no-audit --no-fund; fi
  npm run typecheck
  npm run lint
  npm test
  npm run build
}

case "${1:-all}" in
  go) run_go ;;
  web) run_web ;;
  all) run_go; run_web ;;
  *) echo "usage: verify.sh [go|web]" >&2; exit 2 ;;
esac
printf '\nPassed.\n'
