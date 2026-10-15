.PHONY: dev down test lint migrate seed e2e fmt

dev:
	cp -n .env.example .env || true
	docker compose up --build

down:
	docker compose down

migrate:
	cd services/api && go run ./cmd/api migrate up

seed:
	@echo "No seed data yet (arrives with COS-007)."

fmt:
	cd services/api && gofmt -w .
	cd apps/web && npm run format

lint:
	cd services/api && test -z "$$(gofmt -l .)" && go vet ./...
	cd apps/web && npm run lint && npm run typecheck

test:
	cd services/api && go test ./...
	cd apps/web && npm test

e2e:
	@echo "E2E suite arrives with later milestones."
