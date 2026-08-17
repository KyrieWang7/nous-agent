# Nous Agent Go

`agent-go` is the primary Nous Agent Runtime. It owns the explicit model loop,
capability registry, tools, state, persistence, Subagent and Swarm orchestration,
and runtime event model. It is independent from the archived Python runtime.
The target architecture is documented in
[`../docs/architecture-v2.md`](../docs/architecture-v2.md).

Models, tools, sandboxes, memory, skills, MCP servers and subagents are runtime
capabilities. The loop kernel owns Agent semantics; extension points are
provided by capability providers and runtime plugins. The Kernel owns a fixed
lifecycle dispatcher; it is not a general-purpose business extension chain.

The only public protocol is the versioned `/api/v1` Agent API. It emits the
frontend event names `metadata`, `values`, `messages`, `custom`, `error`, and
`end`; internal events retain more precise content, reasoning, tool, task,
usage, and audit types.

Requests are decoded strictly. Removed LangGraph fields and nested
`config.configurable` values return HTTP 400. Subagent tasks use only
`subagent_type` and structured terminal metadata. Auxiliary model work is
reported as `auxiliary_tokens`.

The sibling `../gateway-go` is the primary control plane. Historical Python
sources are not built, imported, queried, or used as a fallback by this runtime.

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
go run ./cmd/agentctl run --server http://127.0.0.1:7776 --prompt "Inspect this repository"
```

The address defaults to `http://127.0.0.1:7776`; health is available at
`/ok`. Environment variables override the address, database, Redis, skills, and
extensions paths documented in `config.example.yaml`.

## APIs

Endpoints start at `/api/v1`:

```text
POST /api/v1/threads
GET  /api/v1/threads/{thread_id}/state
POST /api/v1/threads/{thread_id}/runs
GET  /api/v1/threads/{thread_id}/runs/{run_id}/events
POST /api/v1/threads/{thread_id}/runs/{run_id}/cancel
GET  /api/v1/threads/{thread_id}/runs/{run_id}/questions/{question_id}
POST /api/v1/threads/{thread_id}/runs/{run_id}/questions/{question_id}/answer
```

Old unversioned routes are not registered. Historical data must be converted
offline into canonical threads, messages, runs, and events before deployment.

## Configuration

See [config.example.yaml](./config.example.yaml). Main sections are:

- `models`: OpenAI-compatible or Anthropic endpoints and thinking options.
- `sandbox`: per-thread local, Docker, or remote/Kubernetes-backed isolation.
- `permissions` and `hooks`: fail-closed tool policy and command governance.
- `plan`: deployment-owned Plan Mode guidance.
- `summarization`: long-context compaction and overflow recovery.
- `skills`, `subagents`, and `swarm`: bounded delegation and team workflows.
- `extensions.mcp_servers`: HTTP/SSE or stdio MCP tools.
- `tools`, `plugins`, and `acp_agents`: community web tools, command plugins,
  and ACP v1 subprocess agents.
- `memory`, `title`, and `guardrails`: optional governed runtime capabilities.
- `runtime`: PostgreSQL, Redis, event retention, and SSE heartbeat.

Repository-provided Skill capabilities live in `../skills/public`; custom
Skills use the sibling `../skills/custom` catalog or the configured runtime
volume. They are not owned by the historical Python source tree.

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
`requiredSandboxMode` is a minimum and never elevates the run without approval.
Plugin manifests are decoded strictly; removed permission fields are rejected.
Missing values default to `danger-full-access`; malformed values fail closed. Plugin commands
receive `NOUS_PLUGIN_NAME`, `NOUS_PLUGIN_ROOT`, and `NOUS_TOOL_NAME`.
`acp_agents` registers `invoke_acp_agent` and speaks ACP v1 over
newline-delimited JSON-RPC. ACP permission requests are denied unless the agent
explicitly sets `auto_approve_permissions: true`.

Plan-mode guidance is deployment-owned through `plan.guidance`. The model tool
catalog remains stable across mode changes; `write_todos` and `exit_plan_mode`
validate active plan state at execution. `exit_plan_mode` presents the complete
Markdown plan through the durable `interaction.questions` capability. Approval
persists `plan_mode_changed(active=false)` before the next model step;
keep-planning, dismissal, cancellation, or persistence failure leaves plan mode
active. User questions are collaboration state and never grant tool or sandbox
permissions.

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

`fs/list` receives a positive `limit` for bounded reads. Remote sandbox
implementations must honor it and may return at most `limit` entries to the
caller; the Go adapter requests one additional entry to detect truncation.

Acquire returns `id` and `root`. File bodies use `path` and base64 data; exec
uses `line`, `work_dir`, `timeout_ms`, and `env`. Responses are size-bounded,
virtual paths are checked against the acquired root, and authentication headers
can be supplied through `sandbox.remote_headers`.

## Persistence

PostgreSQL stores threads, transcripts, runs, replay events, durable user
questions, disposable projection snapshots, memory facts, and Swarm mailboxes. Redis provides
cross-instance run cancellation, event replay, and asynchronous Subagent task
state.

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
