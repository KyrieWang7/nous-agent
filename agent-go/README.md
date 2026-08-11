# Nous Agent Go

`agent-go` is the primary Nous Agent Harness. It owns the model loop,
middleware chain, tools, state, persistence, Subagent and Swarm orchestration,
and runtime event model. It does not import Python and does not depend on
LangGraph.

The compatibility routes under `/threads` and `/runs` are wire adapters for the
current frontend. Both native and compatibility APIs emit the frontend event
names `metadata`, `values`, `messages`, `custom`, `error`, and `end`; internal
events retain more precise content, reasoning, tool, task, usage, and audit
types.

The sibling `../gateway-go` is the primary control plane. The Python
implementations in `../agent` and `../gateway` are legacy compatibility targets
and are not required by this runtime.

## Repository development

From the repository root:

```bash
cp .env.example .env
make dev
```

This exposes the Go Harness at `http://127.0.0.1:7776`, the Go Gateway at
`http://127.0.0.1:7777`, and the local frontend at
`http://127.0.0.1:7775`. The Compose service uses `Dockerfile.dev` and Air, so
changes to Go, YAML, or JSON files under `agent-go/` trigger a rebuild and
restart.

Start only the Harness or follow its logs:

```bash
make agent-go
docker compose logs -f agent-go
```

No Python service starts in the default Compose project.

## Host run

Go 1.25 is required.

```bash
export DEEPSEEK_API_KEY=...
go run ./cmd/agentctl config validate --config config.example.yaml
go run ./cmd/agentd --config config.example.yaml
```

The address defaults to `http://127.0.0.1:7776`; health is available at
`/ok`. Environment variables override the address, database, Redis, skills, and
extensions paths documented in `config.example.yaml`.

## APIs

Native endpoints start at `/api/v1`:

```text
POST /api/v1/threads
GET  /api/v1/threads/{thread_id}/state
POST /api/v1/threads/{thread_id}/runs
GET  /api/v1/threads/{thread_id}/runs/{run_id}/events
POST /api/v1/threads/{thread_id}/runs/{run_id}/cancel
```

The frontend currently uses the compatible `/threads` and `/runs` routes. That
adapter preserves event semantics without making the Go runtime a graph engine
or a LangGraph checkpoint implementation.

## Configuration

See [config.example.yaml](./config.example.yaml). Main sections are:

- `models`: OpenAI-compatible or Anthropic endpoints and thinking options.
- `sandbox`: per-thread local, Docker, or remote/Kubernetes-backed isolation.
- `permissions` and `hooks`: fail-closed tool policy and command governance.
- `summarization`: long-context compaction and overflow recovery.
- `skills`, `subagents`, and `swarm`: bounded delegation and team workflows.
- `extensions.mcp_servers`: HTTP/SSE or stdio MCP tools.
- `tools`, `plugins`, and `acp_agents`: community web tools, command plugins,
  and ACP v1 subprocess agents.
- `memory`, `title`, and `guardrails`: optional stateful middleware.
- `runtime`: PostgreSQL, Redis, event retention, and SSE heartbeat.

MCP availability is not a startup dependency. An unavailable MCP server is
logged and skipped. Child identities and Swarm sender identities come from the
trusted runtime context rather than model tool arguments.

Set both `TUYOO_BASE_URL` and `TUYOO_API_KEY` to route every configured
OpenAI-compatible model through the Tuyoo gateway. They are an atomic pair:
setting only one fails startup so a gateway credential cannot be sent to a
model's original endpoint by mistake. Model names and capabilities still come
from `config.example.yaml`, which is also the Gateway's frontend catalog.

### Runtime reloads

`agentd` fingerprints the YAML configuration, Gateway extensions JSON, Skill
manifests, and plugin manifests before every new run. A change rebuilds one
complete runtime generation, so model routing, capabilities, prompts, tools,
MCP clients, Skills, plugins, and pricing switch together. Active runs retain
their original generation until completion.

`server.address`, `runtime.database_url`, and `runtime.redis_url` own
process-level listeners or clients and require a restart. Invalid reloads fail
the new run without partially installing the changed generation.

### Optional tools and agents

`web_search` and `image_search` use Tavily (`TAVILY_API_KEY`); `web_fetch` uses
Jina Reader and optionally `JINA_API_KEY`. They are registered only when listed
under `tools`.

Command plugins are immediate child directories containing `plugin.json`.
`requiredPermission` (or `required_permission`) is a minimum and never elevates
the run. Missing values default to `danger_full_access`; malformed values fail
closed. `acp_agents` registers `invoke_acp_agent` and speaks ACP v1 over
newline-delimited JSON-RPC. ACP permission requests are denied unless the agent
explicitly sets `auto_approve_permissions: true`.

### Remote sandbox API

Set `sandbox.provider: remote` and `sandbox.remote_url` to use an HTTP sandbox
service, including one backed by Kubernetes. The versioned contract is:

```text
POST   /v1/sandboxes/{thread_key}/acquire
DELETE /v1/sandboxes/{thread_key}
POST   /v1/sandboxes/{id}/exec
POST   /v1/sandboxes/{id}/fs/read
POST   /v1/sandboxes/{id}/fs/write
POST   /v1/sandboxes/{id}/fs/list
POST   /v1/sandboxes/{id}/fs/stat
```

Acquire returns `id` and `root`. File bodies use `path` and base64 data; exec
uses `line`, `work_dir`, `timeout_ms`, and `env`. Responses are size-bounded,
virtual paths are checked against the acquired root, and authentication headers
can be supplied through `sandbox.remote_headers`.

## Persistence

PostgreSQL stores threads, transcripts, runs, replay events, memory facts, and
Swarm mailboxes. Redis provides cross-instance run cancellation, event replay,
and asynchronous Subagent task state.

Both are optional for basic single-process development. PostgreSQL is required
for Swarm and production persistence.

```bash
export DATABASE_URL=postgresql://USER:PASSWORD@localhost:5432/DBNAME?sslmode=disable
go run ./cmd/agentctl migrate up
go run ./cmd/agentctl migrate version
```

From the repository root, `make migrate` performs the same migration through
the configured Go Compose service.

## Production image

Build the non-reloading image directly:

```bash
docker build -t nous-agent-go .
docker run --rm -p 7776:7776 \
  --env-file ../.env \
  -v "$(pwd)/config.example.yaml:/etc/nous-agent/config.yaml:ro" \
  -v nous-agent-go-data:/app/.nous-agent \
  nous-agent-go
```

The image contains `agentd` and `agentctl`, runs as a non-root user, and is
built with Docker sandbox support. Docker sandbox mode additionally requires an
appropriate Docker socket mount and `sandbox.provider: docker`.

## Verification

The default suite is offline and does not require an API key:

```bash
go test ./...
go test -race ./...
go vet ./...
go build ./...
go build -tags=docker -o /tmp/nous-agentd ./cmd/agentd
go run ./internal/tools/layercheck
go test -tags=docker ./pkg/sandbox/docker ./cmd/agentd
```

The `cmd/agentd` suite covers HTTP input through provider streaming, runtime
events, and SSE projection with a local stub provider.
