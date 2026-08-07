# Nous Agent Go

Production-oriented Go implementation of the Nous agent runtime. It owns its
loop, middleware, tools, persistence, and event model. It does not depend on
LangGraph. The HTTP service exposes both a native API and a thin compatibility
surface, with the same SSE event names on both:

`metadata`, `values`, `messages`, `custom`, `error`, `end`.

Internally the runtime keeps finer events such as `content_delta`,
`reasoning_delta`, `tool_start`, `subagent_start`, `usage`, and `run_end`.

This directory and `../agent` are interchangeable implementations of the same
Agent Harness product. The sibling `../gateway` is a separately deployed Python
control-plane service; this Go runtime does not import or embed it.

## Run

Go 1.25 is required.

```bash
cp config.example.yaml config.yaml
export OPENAI_API_KEY=...
go run ./cmd/agentctl config validate --config config.yaml
go run ./cmd/agentd --config config.yaml
```

The default address is `http://127.0.0.1:7776` (YAML `:7776`). Health is
available at `/ok`.

Native endpoints start at `/api/v1`:

```text
POST /api/v1/threads
GET  /api/v1/threads/{thread_id}/state
POST /api/v1/threads/{thread_id}/runs
GET  /api/v1/threads/{thread_id}/runs/{run_id}/events
POST /api/v1/threads/{thread_id}/runs/{run_id}/cancel
```

The compatibility routes under `/threads` and `/runs` exist only as a wire
adapter for the current frontend. The Go runtime is not a graph engine and does
not use LangGraph checkpoints.

## Docker

Build and run the Go Harness directly:

```bash
docker build -t nous-agent-go .
docker run --rm -p 7776:7776 \
  --env-file ../.env \
  -v "$(pwd)/config.yaml:/etc/nous-agent/config.yaml:ro" \
  -v nous-agent-go-data:/app/.nous-agent \
  nous-agent-go
```

The image contains both `agentd` and `agentctl`, runs as a non-root user, and
is built with Docker sandbox support. Using the Docker sandbox at runtime also
requires mounting an appropriate Docker socket and configuring
`sandbox.provider: docker`; local sandbox mode is the default.

From the repository root, the Compose `go-harness` profile exposes this service
at `http://localhost:7778` while the Python Harness keeps `7776`:

```bash
docker compose --profile go-harness up agent-go gateway
```

## Persistence

PostgreSQL stores threads, transcripts, runs, replay events, memory facts, and
swarm mailboxes. Redis provides cross-instance run cancellation, event replay,
and asynchronous subagent task state.

```bash
export DATABASE_URL=postgres://...
go run ./cmd/agentctl migrate up
```

Both are optional for local development. Without them, the service uses
in-memory stores. Do not use the in-memory mode for multi-instance deployment.

## Configuration

See [config.example.yaml](./config.example.yaml). Main sections are:

- `models`: OpenAI-compatible or Anthropic model endpoints.
- `sandbox`: per-thread local isolation, or Docker with `-tags=docker`.
- `permissions`: fail-closed tool policy.
- `hooks`: fail-open command hooks for tool governance and argument rewriting.
- `summarization`: long-conversation compaction and overflow retry.
- `skills` and `subagents`: lazy skills and bounded nested workers.
- `extensions.mcp_servers`: HTTP/SSE or stdio MCP tools.
- `memory`, `title`, and `swarm`: optional stateful capabilities.
- `runtime`: PostgreSQL, Redis, event retention, and SSE heartbeat.

MCP availability is not a startup dependency. An unreachable MCP server is
logged and skipped. Swarm requires PostgreSQL. Memory uses PostgreSQL when it is
configured and otherwise remains process-local. Swarm identities are derived
from the trusted run thread, and broadcast delivery uses per-member receipts.

## Verification

The default test suite is offline and does not require an API key:

```bash
go test ./...
go test -race ./...
go vet ./...
go build ./...
go build -tags=docker -o /tmp/nous-agentd ./cmd/agentd
go run ./internal/tools/layercheck
go test -tags=docker ./pkg/sandbox/docker
```

The `cmd/agentd` suite includes a complete HTTP to provider-stream to runtime
event to SSE test using a local stub provider.
