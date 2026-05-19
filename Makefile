# Nous Agent — Development Commands
# Usage: make <target>

.PHONY: install dev stop build clean agent gateway frontend infra

# ============================================================================
# Quick Start
# ============================================================================

install: ## Install all dependencies (agent + frontend)
	@echo ">>> Installing agent dependencies..."
	cd agent && uv sync
	@echo ">>> Installing frontend dependencies..."
	cd frontend && pnpm install
	@echo ">>> Done. Run 'make dev' to start."

dev: ## Start all services (Agent + Gateway + Frontend)
	@echo ">>> Starting agent on :7776 ..."
	cd agent && uv run uvicorn src.server:app --host 0.0.0.0 --port 7776 --reload &
	@echo ">>> Starting gateway on :7777 ..."
	cd agent && uv run uvicorn src.gateway.app:app --host 0.0.0.0 --port 7777 --reload &
	@echo ">>> Starting frontend on :3000 ..."
	cd frontend && pnpm dev &
	@echo ">>> All services starting. Frontend: http://localhost:3000"

stop: ## Stop all services
	@echo ">>> Stopping services..."
	-@pkill -f "uvicorn src.server:app" 2>/dev/null || true
	-@pkill -f "uvicorn src.gateway.app:app" 2>/dev/null || true
	-@pkill -f "next dev" 2>/dev/null || true
	@echo ">>> Services stopped."

# ============================================================================
# Individual Services
# ============================================================================

agent: ## Start agent server only (port 7776)
	cd agent && uv run uvicorn src.server:app --host 0.0.0.0 --port 7776 --reload

gateway: ## Start gateway API only (port 7777)
	cd agent && uv run uvicorn src.gateway.app:app --host 0.0.0.0 --port 7777 --reload

frontend: ## Start frontend only (port 3000)
	cd frontend && pnpm dev

# ============================================================================
# Infrastructure
# ============================================================================

infra: ## Start PostgreSQL and MinIO via Docker Compose
	docker compose up -d

infra-down: ## Stop infrastructure
	docker compose down

# ============================================================================
# Build
# ============================================================================

build: ## Build frontend for production
	cd frontend && pnpm build

clean: ## Remove generated files
	rm -rf frontend/.next frontend/node_modules
	rm -rf agent/.venv agent/__pycache__

# ============================================================================
# Help
# ============================================================================

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-15s\033[0m %s\n", $$1, $$2}'
