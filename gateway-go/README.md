# Nous Gateway Go

`gateway-go` is the primary Nous Agent control plane and file service. It uses
the native Go Harness tables (`agent_thread`, `agent_message`, `memory_fact`,
and `agent_swarm_*`) and is HTTP-contract compatible with the current frontend.
It does not import either Harness implementation.

The sibling Python `gateway/` remains available only through the repository's
`legacy` Compose profile.

Implemented APIs include:

- model catalog and model details
- thread metadata CRUD
- uploads and artifact delivery
- PDF and Office-to-Markdown conversion
- Skills catalog, enable/disable, and hardened `.skill` extraction
- MCP configuration
- memory facts
- Swarm teams, members, messages, and SSE change events
- health and CORS

## Repository development

The default repository workflow starts this Gateway on port `7777`:

```bash
cp .env.example .env
make dev
```

Start only the Gateway or inspect its logs:

```bash
make gateway-go
docker compose logs -f gateway-go
```

Point a locally started frontend at it with
`GATEWAY_BASE_URL=http://127.0.0.1:7777`. The frontend itself runs on port
`7775`, while Harness traffic is proxied to `http://127.0.0.1:7776`.

## Host run

```bash
go run ./cmd/gateway
```

Configuration is supplied through environment variables:

| Variable | Default |
| --- | --- |
| `GATEWAY_ADDR` | `:7777` |
| `DATABASE_URL` | empty; persistence-backed APIs return empty data |
| `NOUS_HARNESS_CONFIG_PATH` | `/etc/nous-agent/config.yaml` |
| `NOUS_EXTENSIONS_CONFIG_PATH` | `/etc/nous-agent/extensions.json` |
| `NOUS_SKILLS_ROOT` | `/agent/skills` |
| `NOUS_WORKSPACE_ROOT` | `/data/workspaces` |
| `CORS_ORIGINS` | `*` |

The root Compose service mounts the same config, Skills, extensions, and data
volumes used by `agent-go`, while the two processes remain independently
deployable.

## Document conversion

Modern PDF and OOXML documents (`docx`, `pptx`, and `xlsx`) are converted with
Go libraries. Legacy binary Office documents (`doc`, `ppt`, and `xls`) are
normalized by the image's headless LibreOffice runtime and then passed through
the same Go Markdown converters. Conversion failures preserve the original
upload and do not fail unrelated files.

## Legacy comparison

The Python Gateway is isolated on host port `17777` and never starts by
default:

```bash
docker compose --profile legacy up -d --build gateway
```

## Verification

```bash
go test ./...
go test -race ./...
go vet ./...
go build ./...
```
