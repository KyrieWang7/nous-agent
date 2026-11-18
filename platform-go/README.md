# Shared Go platform

Agent and Gateway consume this module through a local module replacement. Build
from the repository checkout so `platform-go/` sits beside both services.

| Package | Stack | Responsibility |
| --- | --- | --- |
| `httpx` | Gin | Routing, path-value adaptation, CORS, request logging, optional Redis rate limiting |
| `postgres` | GORM + PostgreSQL | Shared connection ownership, parameterized queries, explicit transactions |
| `config` | Viper | YAML source loading, explicit environment bindings, strict typed configuration |
| `logging` | Zap | JSON structured logging through the existing `slog` contracts |
| `redisclient` | go-redis | Optional Redis connection setup and health validation |

GORM and native PostgreSQL transaction adapters share one `pgxpool` per process.
GORM owns business queries and the runtime stores; transcript sequence locking
and Swarm transactions retain their native adapters on that same pool. No
`AutoMigrate` runs. Schema changes continue to use the existing SQL migrations.

The SQL boundary binds `$n` parameters through GORM. Repeated and out-of-order
parameters stay bound; literals, comments, PostgreSQL dollar quotes and JSON
operators remain SQL syntax. Query values are never interpolated into SQL.

Viper handles configuration sources and explicit environment overrides. A strict
YAML schema pass retains case-sensitive opaque maps (headers, model options),
duration fields, unknown-field rejection and explicit zero values. No global
Viper singleton is shared across runs or services.

`LOG_LEVEL` defaults to `info`. Both daemons install Zap before constructing
runtime components; existing library loggers keep the `slog` interface.

HTTP rate limiting is disabled by default. To enable it, configure `REDIS_URL`,
`HTTP_RATE_LIMIT_ENABLED=true`, `HTTP_RATE_LIMIT_REQUESTS=60` and
`HTTP_RATE_LIMIT_WINDOW=1m`. The fixed window counts direct peer IPs across
instances of the same service. Health endpoints and CORS preflights are excluded.
An exceeded limit returns 429 and `Retry-After`; an unavailable Redis returns 503.
Gin trusts no proxy headers by default.

```sh
go test -race ./...
go vet ./...
```

HTTP 限流配置与监听地址、数据库/Redis 连接属于进程级配置，修改后需重启服务。
