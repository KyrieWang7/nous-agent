# Gin、GORM/PostgreSQL、Viper、Zap、go-redis 统一封装

日期：2026-11-17。分支：`develop`。按确认的范围仅接入五项技术，数据库继续使用 PostgreSQL。

## 实现与边界

新增第三个 Go module `platform-go`，Agent 与 Gateway 通过同一个本地 module replacement
引用。基础设施只有一份实现，具体应用仍采用此前的七层 `internal` 结构。

| 技术 | 共享实现 | 接入位置 |
| --- | --- | --- |
| Gin 1.12.0 | `platform-go/httpx` | 两服务 router、middleware、server |
| GORM 1.31.2 / postgres driver 1.6.3 | `platform-go/postgres` | 两服务业务 Repository、Agent Runtime PostgreSQL Store |
| Viper 1.21.0 | `platform-go/config` | 两服务配置入口 |
| Zap 1.28.0 / zapslog 0.3.0 | `platform-go/logging` | daemon 启动和已有 slog 契约 |
| go-redis 9.22.0 | `platform-go/redisclient`、`httpx/ratelimit.go` | Agent Runtime Redis 客户端、可选 HTTP 限流 |

HTTP Handler 继续使用严格的请求解码和既有 HTTP/SSE 响应；Gin 适配 path values、GET/HEAD、
catch-all 路径、Flusher 与 CORS。请求日志记录路由模板，不记录请求正文或查询参数。

GORM 使用现有 PostgreSQL schema，不调用 `AutoMigrate`。连接工厂只建立一个原生 pool，
由 GORM database/sql 适配与转录/Swarm 的 PostgreSQL 专用事务共用。业务、审批、用户问题、
记忆、快照和 Runtime metadata 的查询进入 GORM；事件批次通过 GORM 显式事务提交。
转录序号锁、Swarm 并发锁等已有专用事务保留原生适配器，复用同一个 pool。

既有参数化 SQL 不改表义。共享 SQL expression 将 `$n` 参数交给 GORM driver 绑定，支持
重复/乱序参数，并保留引号、注释、PostgreSQL dollar quotes 和 JSON `?` 操作符；不插值参数。
事务回调失败时整批 rollback。单行查询保留 `sql.ErrNoRows`，GORM 构建错误也正常返回。

Viper 管理来源与显式环境覆盖；严格 YAML schema pass 保留大小写敏感的 headers/model
options、duration、显式零值与未知字段拒绝。扩展配置、API key 环境展开和既有配置优先级沿用。
Gateway 新增可选的 `NOUS_GATEWAY_CONFIG_PATH` YAML 来源。

Zap 在构造运行时组件前安装，通过官方 zapslog Handler 承接现有库的 slog 接口。Agent 内核
无需依赖 Zap。Kernel 的依赖检查禁止 Gin、GORM、Viper、Zap 和共享基础设施传递进入内核。

## 配置与构建

`LOG_LEVEL=info`。可选限流默认关闭；启用时要求可达的 `REDIS_URL`，配置为：

```dotenv
HTTP_RATE_LIMIT_ENABLED=true
HTTP_RATE_LIMIT_REQUESTS=60
HTTP_RATE_LIMIT_WINDOW=1m
```

固定窗口按服务和直接连接的 IP 在 Redis 中原子计数。超额返回 429 和 Retry-After；Redis
不可用返回 503；health 与 CORS preflight 不计数。默认不信任代理转发的 IP header。

两个服务 Docker build context 改为仓库根目录，Dockerfile 专用 ignore 仅允许当前服务与
`platform-go` 源码，并排除环境文件、开发工作目录和 Git。Compose 同步挂载公共 module。
未启动或更新现有 Compose 服务，未发布镜像，未迁移现有应用数据库。

```sh
docker build -f agent-go/Dockerfile -t nous-agent-go .
docker build -f gateway-go/Dockerfile -t nous-gateway .
```

## 验证

通过的检查：

- `platform-go`: `go test -race ./...`、`go vet ./...`、`go build ./...`。
- `agent-go`: 完整 `go test ./...`、`go vet ./...`、`go build ./...`，以及
  `go test -race ./internal/... ./pkg/config ./cmd/agentd`。
- `gateway-go`: `go test -race ./...`、`go vet ./...`、`go build ./...`。
- 三个 module 的 `go mod tidy -diff`。
- `make layer-check` 覆盖 Agent 与 Gateway；本次文件 gofmt 与 `git diff --check`。
- Docker PostgreSQL 17 集成测试：审批/用户问题的并发首个终态、快照往返与游标单调、
  Runtime metadata、记忆 scope，以及包含计划确认、子 Agent、事件与用量落存的完整
  Swarm HTTP 流程，全部通过。
- 两服务的 Docker `--target build` 编译阶段；Compose `config --quiet` 校验。
- `go test -tags=docker ./cmd/sandboxd -run '^$'` 验证独立 sandbox daemon 可编译。

共享包回归覆盖重复/乱序 SQL 参数、SQL 字面量与 JSON 操作符、空结果、绑定错误、
失败事务 rollback、Gin 路径/SSE/HEAD/CORS、Viper 严格解码与环境类型，以及两引擎共用
Redis 计数与窗口过期。

一次集成复测遇到 Docker Desktop 首次容器快照缺失 port mapping，空端口使 fixture
误尝试本机 5432。fixture 现先刷新并确认测试容器端口，再连接该端口；全组复测通过。

仍未执行真实模型或部署客户端验收。环境没有 `golangci-lint`，不声称执行完整
`make check`；以上检查均已单独执行。

## 参考

- [Gin 路由与中间件](https://gin-gonic.com/en/docs/)
- [GORM PostgreSQL 连接](https://gorm.io/docs/connecting_to_the_database.html)
- [Viper 配置优先级与环境绑定](https://github.com/spf13/viper)
- [Zap 官方 slog 适配](https://pkg.go.dev/go.uber.org/zap/exp/zapslog)

HTTP 限流配置与监听地址、数据库/Redis 连接属于进程级配置，修改后需重启服务。
