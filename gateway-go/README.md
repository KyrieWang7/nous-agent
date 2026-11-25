# Nous Gateway Go

`gateway-go` is the primary Nous Agent control plane and file service. It uses
the native Go Harness tables (`agent_thread`, `agent_message`, `memory_fact`,
and `agent_swarm_*`) and is HTTP-contract compatible with the current frontend.
It does not import either Harness implementation.

Implemented APIs include:

- model catalog and model details
- model connection settings, credentials, and default model selection
- thread metadata CRUD
- uploads and artifact delivery
- PDF and Office-to-Markdown conversion
- Skills catalog, enable/disable, and hardened `.skill` extraction
- MCP configuration
- memory facts
- Swarm teams, members, messages, and SSE change events
- health and CORS

## Shared platform

The sibling `platform-go` module supplies Gin routing, GORM/PostgreSQL, Viper,
Zap logging and go-redis. An optional `NOUS_GATEWAY_CONFIG_PATH` YAML file can
supply configuration; the established environment variables override its values.
See `config.example.yaml`. Database schemas stay under explicit SQL migrations.

HTTP rate limiting defaults off. It can be enabled with `REDIS_URL`,
`HTTP_RATE_LIMIT_ENABLED=true`, `HTTP_RATE_LIMIT_REQUESTS=60` and
`HTTP_RATE_LIMIT_WINDOW=1m`. `LOG_LEVEL` defaults to `info`.

Build from the repository root with
`docker build -f gateway-go/Dockerfile -t nous-gateway .` so the common module
is available in the build context.

## Application layers

`internal/router` registers routes, `middleware` handles HTTP concerns, `handler`
decodes and projects HTTP/SSE, `service` owns use cases, `repository` implements
persistence, `model` defines application data, and `server` composes the boundary.

Catalog, filesystem and conversion adapters live under
`internal/repository/{catalog,files,convert}`. Swarm polling and member-status
reconciliation belong to `internal/service`; handlers only encode SSE events.

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
| `NOUS_SKILLS_ROOT` | `/opt/nous/skills` |
| `NOUS_WORKSPACE_ROOT` | `/data/workspaces` |
| `CORS_ORIGINS` | `*` |

The root Compose service mounts the same config, Skills, extensions, and data
volumes used by `agent-go`, while the two processes remain independently
deployable.

## Model configuration API

`GET /api/models/config` reads model connection settings. `PUT /api/models/config`
replaces the complete model list and selects its default. Adding a list entry
creates a model; changing it updates the model; removing it deletes the model.
At least one model must remain; the model names `default` and `config` are reserved by the runtime and API. Existing `/api/models` and `/api/models/{name}`
queries continue to return the frontend catalog and reflect saved settings.

```bash
curl -X PUT http://127.0.0.1:7777/api/models/config \
  -H 'Content-Type: application/json' \
  --data-binary @model-settings.json
```

Example `model-settings.json` (replace the model and key with your provider values):

```json
{
  "default_model": "my-model",
  "models": [
    {
      "name": "my-model",
      "display_name": "My model",
      "provider": "openai-compatible",
      "model": "YOUR_MODEL_ID",
      "base_url": "https://YOUR_PROVIDER/v1",
      "api_key": "YOUR_API_KEY",
      "context_length": 128000,
      "max_tokens": 8192
    }
  ]
}
```

Supported providers are `openai-compatible`, `anthropic`, and `deepseek`.
Model fields follow the Harness configuration, including thinking, vision,
timeouts, provider options, and pricing. An omitted `default_model` selects the
first model. An omitted `api_key` retains the credential of the existing model
with the same name; an explicit empty string clears it. GET and PUT responses
omit `api_key` and report `api_key_configured` instead. A redacted GET response
can be edited and sent back to PUT without losing existing keys.

Settings are atomically persisted to `models.json` beside
`NOUS_EXTENSIONS_CONFIG_PATH`, with file permissions `0600`, in the existing
shared configuration volume. Secrets are stored locally as plaintext; restrict
access to this volume and the configuration API through your deployment's
access controls. The repository YAML remains the initial configuration until
settings are saved. These settings configure the Nous Agent; Manager maintains
its own model configuration.

Agent loads a new runtime generation before the next run; active runs retain
their current generation. Saving validates configuration structure, not provider
connectivity or the validity of a credential. `TUYOO_BASE_URL` and `TUYOO_API_KEY`,
when explicitly configured, still take precedence for OpenAI-compatible models.

## Document conversion

Modern PDF and OOXML documents (`docx`, `pptx`, and `xlsx`) are converted with
Go libraries. Legacy binary Office documents (`doc`, `ppt`, and `xls`) are
normalized by the image's headless LibreOffice runtime and then passed through
the same Go Markdown converters. Conversion failures preserve the original
upload and do not fail unrelated files.

## Verification

```bash
go test ./...
go test -race ./...
go vet ./...
go build ./...
```
