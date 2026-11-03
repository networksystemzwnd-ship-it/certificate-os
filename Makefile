# Optional shortcuts. Everything runs in Docker; nothing here needs Go, Node or psql on the host.
# Requires COMPOSE_FILE=compose.yaml:compose.tools.yaml (set in .env, see .env.example).
.PHONY: dev down test lint fmt verify migrate seed restore-check
dev:
	cp -n .env.example .env || true
	docker compose up --build
down:
	docker compose down
migrate:
	docker compose exec api /api migrate up
seed:
	docker compose exec api /api seed
test:
	docker compose run --rm test-go && docker compose run --rm test-web
lint:
	docker compose run --rm lint
fmt:
	docker compose run --rm fmt
verify:
	docker compose run --rm verify
# Usage: make restore-check DB=certos
restore-check:
	DB=$(DB) docker compose run --rm restore-check
