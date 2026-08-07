# Nous Gateway

Python control-plane service shared by the Nous Agent harness products. It is
deployed independently from both runtime implementations:

- `agent/`: Python/LangGraph-compatible harness
- `agent-go/`: native Go harness
- `gateway/`: models, MCP, skills, memory, uploads, artifacts, thread metadata,
  and swarm management APIs

The harnesses do not import this package. Gateway currently reuses explicitly
packaged Python domain adapters from `nous-agent` for the existing PostgreSQL,
skills, sandbox, and configuration contracts. This is a one-way package
dependency (`gateway -> agent`) behind the Gateway process boundary; selecting
either harness does not change the Gateway's public HTTP API.

The product topology is:

```text
frontend -> gateway (control-plane HTTP API)
frontend -> selected harness (run and SSE API)
gateway  -> nous-agent domain adapters (current implementation detail)
```

The Python and Go harnesses are separate runtime products. They share the
frontend-facing event contract, but neither runtime imports or embeds Gateway.

## Run

```bash
uv sync
uv run uvicorn nous_gateway.app:app --host 0.0.0.0 --port 7777
```

Configuration and data locations are selected with the existing environment
variables, including `DEER_FLOW_CONFIG_PATH`,
`DEER_FLOW_EXTENSIONS_CONFIG_PATH`, `DEER_FLOW_HOME`, and
`LANGGRAPH_PG_URI`.

Health is available at `GET /health`; OpenAPI documentation is at `GET /docs`.
