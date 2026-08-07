# Nous Gateway Go

Go control-plane implementation for the Nous Agent product. It is HTTP-contract
compatible with the sibling Python `gateway/`, while using the native Go
Harness tables (`agent_thread`, `agent_message`, `memory_fact`, and
`agent_swarm_*`). Neither Gateway imports either Harness implementation.

Implemented APIs:

- model catalog and details
- thread metadata CRUD
- uploads and artifact delivery
- PDF and Office-to-Markdown conversion (`pdf`, `docx`, `pptx`, `xlsx`, and legacy `doc`, `ppt`, `xls`)
- Skills catalog, enable/disable, and hardened `.skill` extraction
- MCP configuration
- memory facts
- Swarm team/member queries and SSE messages
- health and CORS

## Run

```bash
go run ./cmd/gateway
```

Configuration is supplied through environment variables:

| Variable | Default |
| --- | --- |
| `GATEWAY_ADDR` | `:7777` |
| `DATABASE_URL` | empty (read-only APIs return empty data) |
| `NOUS_HARNESS_CONFIG_PATH` | `/etc/nous-agent/config.yaml` |
| `NOUS_EXTENSIONS_CONFIG_PATH` | `/etc/nous-agent/extensions.json` |
| `NOUS_SKILLS_ROOT` | `/agent/skills` |
| `NOUS_WORKSPACE_ROOT` | `/data/workspaces` |
| `CORS_ORIGINS` | `*` |

Start the optional Compose implementation on host port 7780:

```bash
docker compose --profile go-gateway up -d gateway-go
```

Point the frontend at it with `GATEWAY_BASE_URL=http://127.0.0.1:7780`.

Modern PDF and OOXML documents are converted with Go libraries. Legacy binary
Office documents are normalized by the image's headless LibreOffice runtime,
then processed by the same Go Markdown converters. Conversion failures preserve
the original upload and are logged without failing unrelated files.
