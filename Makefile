# Nous Agent development commands. The Go stack is the default product path.

.PHONY: install install-legacy dev dev-go backend stop logs compose-config \
	agent agent-go agent-local gateway gateway-go gateway-local frontend \
	dev-legacy legacy-backend legacy-agent legacy-gateway stop-legacy logs-legacy \
	migrate migrate-current migrate-down legacy-migrate legacy-migrate-current \
	legacy-migrate-down legacy-migrate-new build clean help

GATEWAY_BASE_URL ?= http://127.0.0.1:7777
HARNESS_BASE_URL ?= http://127.0.0.1:7776
LEGACY_GATEWAY_BASE_URL ?= http://127.0.0.1:17777
LEGACY_HARNESS_BASE_URL ?= http://127.0.0.1:17776

# -----------------------------------------------------------------------------
# Primary Go development path
# -----------------------------------------------------------------------------

install: ## Install Go and frontend dependencies
	cd agent-go && go mod download
	cd gateway-go && go mod download
	cd frontend && pnpm install

dev: backend ## Start Go backend containers and frontend locally on :7775
	cd frontend && GATEWAY_BASE_URL=$(GATEWAY_BASE_URL) HARNESS_BASE_URL=$(HARNESS_BASE_URL) npm run dev

dev-go: dev ## Alias for the default Go development path

backend: ## Start Go Harness (:7776) and Go Gateway (:7777)
	docker compose up -d --build agent-go gateway-go

stop: ## Stop the Go backend and local frontend
	docker compose stop agent-go gateway-go
	-@pkill -f "next dev --turbo --port 7775" 2>/dev/null || true

logs: ## Follow Go backend logs
	docker compose logs -f agent-go gateway-go

compose-config: ## Validate the Compose model without starting services
	docker compose config --quiet

# -----------------------------------------------------------------------------
# Individual Go services
# -----------------------------------------------------------------------------

agent: agent-go ## Start the primary Agent Harness

agent-go: ## Start the Go Agent with Air hot reload in Docker
	docker compose up -d --build agent-go

agent-local: ## Run the Go Agent directly on the host
	cd agent-go && go run ./cmd/agentd --config config.example.yaml

gateway: gateway-go ## Start the primary Gateway

gateway-go: ## Start the Go Gateway in Docker
	docker compose up -d --build gateway-go

gateway-local: ## Run the Go Gateway directly on the host
	cd gateway-go && go run ./cmd/gateway

frontend: ## Start the frontend locally on :7775 against the Go stack
	cd frontend && GATEWAY_BASE_URL=$(GATEWAY_BASE_URL) HARNESS_BASE_URL=$(HARNESS_BASE_URL) npm run dev

# -----------------------------------------------------------------------------
# Legacy Python compatibility path
# -----------------------------------------------------------------------------

install-legacy: ## Install Python Harness and Gateway dependencies
	cd agent && uv sync
	cd gateway && uv sync

dev-legacy: legacy-backend ## Start legacy Python backend and frontend on :7775
	cd frontend && GATEWAY_BASE_URL=$(LEGACY_GATEWAY_BASE_URL) HARNESS_BASE_URL=$(LEGACY_HARNESS_BASE_URL) npm run dev

legacy-backend: ## Start legacy Python Harness (:17776) and Gateway (:17777)
	docker compose --profile legacy up -d --build agent gateway

legacy-agent: ## Start only the legacy Python Harness
	docker compose --profile legacy up -d --build agent

legacy-gateway: ## Start only the legacy Python Gateway
	docker compose --profile legacy up -d --build gateway

stop-legacy: ## Stop legacy Python services
	docker compose --profile legacy stop agent gateway

logs-legacy: ## Follow legacy Python backend logs
	docker compose --profile legacy logs -f agent gateway

# -----------------------------------------------------------------------------
# Database migrations
# -----------------------------------------------------------------------------

migrate: ## Apply Go runtime migrations
	docker compose run --rm --build agent-go go run ./cmd/agentctl migrate up

migrate-current: ## Show the Go runtime migration version
	docker compose run --rm --build agent-go go run ./cmd/agentctl migrate version

migrate-down: ## Roll back one Go runtime migration
	docker compose run --rm --build agent-go go run ./cmd/agentctl migrate down --steps 1

legacy-migrate: ## Apply legacy Python migrations
	cd agent && uv run alembic upgrade head

legacy-migrate-current: ## Show the legacy Python migration revision
	cd agent && uv run alembic current

legacy-migrate-down: ## Roll back one legacy Python migration
	cd agent && uv run alembic downgrade -1

legacy-migrate-new: ## Create a legacy migration: make legacy-migrate-new m="add table"
	cd agent && uv run alembic revision -m "$(m)"

# -----------------------------------------------------------------------------
# Build and cleanup
# -----------------------------------------------------------------------------

build: ## Build both Go services and the frontend
	cd agent-go && go build ./...
	cd gateway-go && go build ./...
	cd frontend && pnpm build

clean: ## Remove generated development files
	rm -rf agent-go/tmp frontend/.next

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-24s\033[0m %s\n", $$1, $$2}'
