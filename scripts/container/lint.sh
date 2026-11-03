#!/usr/bin/env bash
# Runs INSIDE the toolbox container.
set -euo pipefail
cd /repo/services/api
test -z "$(gofmt -l .)" || { echo "gofmt needed for:"; gofmt -l .; exit 1; }
go vet ./...
cd /repo/apps/web
if [ -f package-lock.json ]; then npm ci --no-audit --no-fund; else npm install --no-audit --no-fund; fi
npm run lint
npm run typecheck
