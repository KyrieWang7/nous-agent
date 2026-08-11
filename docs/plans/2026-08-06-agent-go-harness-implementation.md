# agent-go Harness Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** 在 `agent-go/` 从零构建生产级 Go Agent Harness 库：自持 `for` 循环内核 + 四阶段中间件链 + LangGraph 兼容 HTTP/SSE 适配器，使现有 Next.js 前端可通过同一组事件语义切换到 Go 实现。

**Architecture:** 内核（`pkg/loop`）持有回合编排（压缩→裁剪→工具集→采样→落存→工具执行→停止判定），形态取自 `agentsdk-go` `runLoop`。横切关注点归四阶段中间件链（`BeforeAgent`/`BeforeModel`/`AfterModel`/`AfterAgent` + `BeforeTool`/`AfterTool`），代码风格与声明式锚点排序取自 `nous-agent`。15 个注入接口承载可替换实现。设计文档：`docs/plans/2026-08-06-agent-go-harness-design.md`（本计划的每个任务都对应其中一节，实现前先读对应节）。

**Tech Stack:** Go 1.25 · PostgreSQL(pgx/v5) · Redis(go-redis/v9) · golang-migrate · OpenTelemetry · testify · dockertest · miniredis · gopkg.in/yaml.v3

> 实施状态（2026-08-07）：M0-M4 已闭环。Go 服务采用自有 loop/runtime，
> 原生 API 与薄兼容 API 共用六类 SSE 投影，不追求 LangGraph 内部语义一致。
> 离线端到端测试覆盖 HTTP 请求、provider token stream、runtime event、SSE
> 投影、最终 state 持久化与 `end` 收口。压缩轮使用独立完整 transcript 契约；
> 流式 guardrail 通过 `message_replace` 补偿；Swarm 使用可信 thread 身份与逐成员
> receipt；异步 subagent 用量可在 run 完成后幂等回写 completion。

**测试纪律（全程适用）：**
- 每个任务先写失败测试，再写最小实现。
- 表驱动，`t.Parallel()` 默认开。
- 默认 `make check` **离线、无网络、无 API key**。任何需要真实模型的测试用 `faux` provider。
- 涉及并发的包必须 `make race` 干净。
- 不读源码文本做断言（禁止 regex 匹配源文件）。
- 不写变更检测型测试（不快照会变的数据，只断言不变量关系）。

---

## M0 — 内核骨架

目标：带工具调用的完整回合跑通，全离线可测，`-race` 干净。

### Task 1: 项目骨架

**Files:**
- Create: `agent-go/go.mod`
- Create: `agent-go/Makefile`
- Create: `agent-go/.golangci.yml`
- Create: `agent-go/.gitignore`
- Create: `agent-go/doc.go`

**Step 1: 初始化模块**

```bash
cd agent-go
go mod init github.com/KyrieWang7/nous-agent/agent-go
go mod edit -go=1.25
```

**Step 2: 写 Makefile**

```makefile
.PHONY: check test race lint fmt fmt-check tidy-check coverage layer-check build migrate

check: fmt-check tidy-check lint layer-check test

test:
	go test ./...

race:
	go test -race ./...

lint:
	golangci-lint run

fmt:
	gofmt -w .

fmt-check:
	@out=$$(gofmt -l .); if [ -n "$$out" ]; then echo "$$out"; exit 1; fi

tidy-check:
	go mod tidy -diff

coverage:
	go test -coverprofile=coverage.out ./... && go tool cover -func=coverage.out | tail -1

layer-check:
	go run ./internal/tools/layercheck

build:
	go build ./...
```

**Step 3: 写 `.golangci.yml`**

```yaml
version: "2"
linters:
  enable:
    - errcheck
    - govet
    - ineffassign
    - staticcheck
    - unused
    - errorlint
    - bodyclose
    - contextcheck
    - copyloopvar
    - nilerr
    - unconvert
```

**Step 4: 验证**

Run: `cd agent-go && go build ./... && make fmt-check`
Expected: 无输出、退出码 0

**Step 5: Commit**

```bash
git add agent-go/go.mod agent-go/Makefile agent-go/.golangci.yml agent-go/.gitignore agent-go/doc.go
git commit -m "chore(agent-go): scaffold module, Makefile, lint config"
```

---

### Task 2: pkg/message — Message 类型

**Files:**
- Create: `agent-go/pkg/message/message.go`
- Test: `agent-go/pkg/message/message_test.go`

**参考设计文档 §6.1。**

**Step 1: 写失败测试**

断言：`Clone` 深拷贝（改副本的 `ToolCalls` 不影响原件）；`IsToolTransactionStart` 对 `assistant`+非空 `ToolCalls` 为真。

**Step 2: 运行确认失败** — `go test ./pkg/message/ -run TestMessage -v`

**Step 3: 最小实现**

```go
package message

type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

type ToolCall struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

type ContentBlock struct {
	Type     string `json:"type"` // text | image | thinking
	Text     string `json:"text,omitempty"`
	MimeType string `json:"mime_type,omitempty"`
	Data     string `json:"data,omitempty"`
}

type Message struct {
	Role             Role           `json:"role"`
	Content          string         `json:"content,omitempty"`
	ContentBlocks    []ContentBlock `json:"content_blocks,omitempty"`
	ReasoningContent string         `json:"reasoning_content,omitempty"`
	ToolCalls        []ToolCall     `json:"tool_calls,omitempty"`
	ToolCallID       string         `json:"tool_call_id,omitempty"`
	Name             string         `json:"name,omitempty"`
	IsError          bool           `json:"is_error,omitempty"`
}

func Clone(m Message) Message { /* 深拷贝 slice 字段 */ }
```

**Step 4: 运行确认通过**

**Step 5: Commit** — `feat(agent-go): message types`

---

### Task 3: pkg/message — History 水位语义

**Files:**
- Create: `agent-go/pkg/message/history.go`
- Test: `agent-go/pkg/message/history_test.go`

**参考设计文档 §6.1、§3.3、§13.2。这是最容易写错的一个包 —— pace-grid 上坏过。**

**Step 1: 写失败测试（关键用例）**

```go
// 水位必须在 Replace 之后仍然正确：这是 pace-grid 的真实 bug
func TestHistory_SinceSurvivesReplace(t *testing.T) {
	h := message.NewHistory()
	h.Append(msg(RoleUser, "q1"))
	h.Append(msg(RoleAssistant, "a1"))

	wm := h.Watermark()            // 回合开始时取水位

	h.Append(msg(RoleUser, "q2"))
	// 模拟压缩：转录被整体替换，长度变短
	h.Replace([]message.Message{msg(RoleSystem, "## Summary"), msg(RoleUser, "q2")})
	h.Append(msg(RoleAssistant, "a2"))

	got := h.Since(wm)
	// 必须拿到本轮新增的 q2 与 a2，而不是空或越界 panic
	require.Len(t, got, 2)
	require.Equal(t, "q2", got[0].Content)
	require.Equal(t, "a2", got[1].Content)
}

func TestHistory_ReplaceLastAssistant(t *testing.T) { /* 护栏撤回 */ }
func TestHistory_ConcurrentAppendAll(t *testing.T)  { /* -race */ }
```

**Step 2: 运行确认失败**

**Step 3: 实现**

```go
type History struct {
	mu      sync.RWMutex
	msgs    []Message
	appends uint64 // 累计 append 次数，水位基准；Replace 不重置
}

func (h *History) Append(m Message) {
	h.mu.Lock(); defer h.mu.Unlock()
	h.msgs = append(h.msgs, m)
	h.appends++
}

// Since 从尾部回溯 (appends - wm) 条，并按当前长度封顶。
// 压缩缩短转录后，回溯量可能超过现有长度，此时返回全部。
func (h *History) Since(wm uint64) []Message {
	h.mu.RLock(); defer h.mu.RUnlock()
	n := int(h.appends - wm)
	if n <= 0 { return nil }
	if n > len(h.msgs) { n = len(h.msgs) }
	out := make([]Message, n)
	copy(out, h.msgs[len(h.msgs)-n:])
	return out
}

func (h *History) Replace(msgs []Message) {
	h.mu.Lock(); defer h.mu.Unlock()
	h.msgs = append([]Message(nil), msgs...)
	// 关键：appends 不动。水位是"累计 append 次数"，不是下标。
}

func (h *History) ReplaceLastAssistant(m Message) bool { /* 从尾部找第一条 assistant 替换 */ }
```

**Step 4: 运行确认通过** — 含 `go test -race ./pkg/message/`

**Step 5: Commit** — `feat(agent-go): history with watermark semantics`

---

### Task 4: pkg/message — Trimmer + token 估算

**Files:**
- Create: `agent-go/pkg/message/trimmer.go`
- Create: `agent-go/pkg/message/tokens.go`
- Test: `agent-go/pkg/message/trimmer_test.go`

**Step 1: 失败测试** — 断言：超限时从头部裁剪但**保留 system 首条**；裁剪不切断 tool 事务（assistant+tool_calls 与其 tool 消息同去同留）；未超限时返回原切片。

**Step 2-4: 实现并通过。** `EstimateTokens` 用字符数近似（中文 1.5 char/token、英文 4 char/token），接口留 `TokenCounter` 便于后续换 tiktoken。

**Step 5: Commit** — `feat(agent-go): message trimmer and token estimation`

---

### Task 5: pkg/model — 单一 Model 接口

**Files:**
- Create: `agent-go/pkg/model/model.go`
- Create: `agent-go/pkg/model/stream.go`
- Create: `agent-go/pkg/model/registry.go`
- Test: `agent-go/pkg/model/registry_test.go`

**参考设计文档 §11.1。**

**Step 1: 失败测试** — `Register` 同名二次注册返回 error；`Get` 未知名字返回 error（fail-fast，见 §16）。

**Step 3: 实现**

```go
type Request struct {
	Messages          []message.Message
	Tools             []tool.Definition
	System            string
	MaxTokens         int
	Temperature       *float64
	Thinking          bool
	ExtraBody         map[string]any // thinking 等供应商私有字段透传
	EnablePromptCache bool
}

type Response struct {
	Message    message.Message
	StopReason string // stop | tool_calls | length | content_filter | error
	Usage      Usage
	CallID     string // 用于三桶归因去重，见 §15
}

type Info struct {
	Name          string
	ContextLength int
	SupportsThinking, SupportsVision, SupportsStructured bool
}

type Model interface {
	Complete(ctx context.Context, req Request) (*Response, error)
	Stream(ctx context.Context, req Request) (StreamReader, error)
	Info() Info
}

type StreamEvent struct {
	Type  string // start | text_delta | thinking_delta | toolcall_delta | done | error
	Delta string
	Partial *Response
	Err   error
}

type StreamReader interface {
	Next() (StreamEvent, bool)
	Result() (*Response, error)
	Close() error
}
```

**Step 5: Commit** — `feat(agent-go): model interface and provider registry`

---

### Task 6: pkg/model/provider/faux — 测试用确定性 provider

**Files:**
- Create: `agent-go/pkg/model/provider/faux/faux.go`
- Test: `agent-go/pkg/model/provider/faux/faux_test.go`

**这是硬要求（设计文档 §18）。后续所有 loop/chain/subagent 测试都靠它，不碰真实 API。**

**Step 1: 失败测试** — 脚本化：`New([]Response{...})` 按顺序返回；耗尽后返回 `ErrScriptExhausted`；`Stream` 把 `Content` 切成 delta 逐个发出且 `Result()` 与 `Complete` 一致；记录收到的 `Request` 供断言。

**Step 3: 实现** — 支持 `WithDelay`、`WithError(n, err)`（第 n 次调用返回指定错误，用于测降级/重试）。

**Step 5: Commit** — `feat(agent-go): faux model provider for offline tests`

---

### Task 7: pkg/tool — Definition · Metadata · Registry

**Files:**
- Create: `agent-go/pkg/tool/tool.go`
- Create: `agent-go/pkg/tool/registry.go`
- Test: `agent-go/pkg/tool/registry_test.go`

**参考设计文档 §7.1。**

**Step 1: 失败测试** — 未知名字 `Get` 返回 error；`Destructive: true` 的工具 `Register` 直接拒绝（§7.3 硬约束：破坏性工具永不注册为可自动调用）；`Schemas(allow)` 只返回白名单内且非 `Deferred` 的。

**Step 3: 实现** — `Definition`/`Metadata` 见设计文档 §7.1；`Handler func(ctx, args json.RawMessage) (*Result, error)`；`Result{Content string, IsError bool, Artifacts []Artifact}`。

**Step 5: Commit** — `feat(agent-go): tool definition, metadata and registry`

---

### Task 8: pkg/tool — Executor 并发分段

**Files:**
- Create: `agent-go/pkg/tool/executor.go`
- Create: `agent-go/pkg/tool/segments.go`
- Test: `agent-go/pkg/tool/segments_test.go`
- Test: `agent-go/pkg/tool/executor_test.go`

**参考设计文档 §7.2；算法对照 `agentsdk-go` `pkg/api/tool_concurrency.go`。**

**Step 1: 失败测试**

```go
// 只读且并发安全才分到并发段；写操作独占；段间严格顺序
func TestPartitionSegments(t *testing.T) {
	// [read, read, write, read] -> [{concurrent:[read,read]}, {write}, {concurrent:[read]}]
}

// 并发执行后结果必须按原始 index 顺序回写
func TestExecutor_ConcurrentPreservesOrder(t *testing.T) {}

// 信号量限流：ToolConcurrency=2 时同时在跑的不超过 2
func TestExecutor_ConcurrencyLimit(t *testing.T) {}

// 一个调用返回 context.Canceled 时级联取消同段其余调用
func TestExecutor_CascadeCancel(t *testing.T) {}

// 工具 panic 转为 error 结果，不冒泡杀 run（§17.2）
func TestExecutor_RecoversPanic(t *testing.T) {}
```

**Step 3: 实现** — 并发段执行期**不写 History**，跑完按 index 顺序回写（避免多 goroutine 抢 History）。

**Step 4: 必须 `go test -race ./pkg/tool/` 干净**

**Step 5: Commit** — `feat(agent-go): tool executor with read-only concurrency segmentation`

---

### Task 9: pkg/middleware — State · Directive · 契约

**Files:**
- Create: `agent-go/pkg/middleware/state.go`
- Create: `agent-go/pkg/middleware/middleware.go`
- Test: `agent-go/pkg/middleware/state_test.go`

**参考设计文档 §4.1、§4.2。**

**Step 1: 失败测试** — `CloneForTool` 产出独立 `State`（改副本的 `ToolCall` 不影响原件），但 `History` 与 `Journal` 是同一指针（共享）；`Take()` 读取并复位 `Directive`。

**Step 3: 实现** — `State` 字段见设计文档 §4.2（**类型字段为主，树内代码不用 `Values`**）；`Directive` 三值；六个可选接口 + `Decision`。

**Step 5: Commit** — `feat(agent-go): middleware contract, typed state and directive`

---

### Task 10: pkg/middleware — Chain 执行语义

**Files:**
- Create: `agent-go/pkg/middleware/chain.go`
- Create: `agent-go/pkg/middleware/grade.go`
- Test: `agent-go/pkg/middleware/chain_test.go`

**参考设计文档 §4.3（逆序）、§17.1（错误分级）。**

**Step 1: 失败测试**

```go
// BeforeAgent/BeforeModel 正序；AfterModel/AfterAgent 逆注册顺序（§4.3）
func TestChain_AfterStagesRunInReverseOrder(t *testing.T) {}

// 治理类错误短路并返回；监听类错误只记日志不影响回合（§17.1）
func TestChain_ErrorGrading(t *testing.T) {
	// GradeGovernance -> Execute 返回 error
	// GradeListener   -> Execute 返回 nil，且后续中间件仍执行
}

// 每阶段独立超时，错误包装为 "middleware %s failed: %w"
func TestChain_PerStageTimeout(t *testing.T) {}

// 只实现了部分接口的中间件不被误调
func TestChain_OptionalInterfaces(t *testing.T) {}
```

**Step 3: 实现** — `Grade` 由中间件通过可选接口 `Graded interface{ Grade() Grade }` 声明，未声明默认 `GradeRepair`。

**Step 5: Commit** — `feat(agent-go): middleware chain with reverse after-stages and error grading`

---

### Task 11: pkg/middleware — 锚点排序 Build

**Files:**
- Create: `agent-go/pkg/middleware/ordering.go`
- Test: `agent-go/pkg/middleware/ordering_test.go`

**参考设计文档 §4.3；算法对照 `nous-agent` `src/agents/middleware_ordering.py`。**

**Step 1: 失败测试** — `AnchorAfter(X)` 插到 X 之后；`AnchorBefore(X)` 之前；同时声明二者 → error；两个扩展锚同一目标同方向 → error；成环 → error；锚点不存在 → error；无锚点 → 插到终端 `Clarification` 之前；支持扩展锚扩展（迭代插入）。

**Step 3: 实现** — `Build(base []Middleware, extras []Middleware) ([]Middleware, error)`。

**Step 5: Commit** — `feat(agent-go): declarative anchor-based middleware ordering`

---

### Task 12: pkg/prompt — 系统提示装配

**Files:**
- Create: `agent-go/pkg/prompt/builder.go`
- Test: `agent-go/pkg/prompt/builder_test.go`

**Step 1: 失败测试** — 分段装配（基础 + 工具说明 + skills 段 + subagent 段）；**同一份输入两次装配结果逐字节一致**（缓存边界要求，不可含时间戳/随机序）。

**Step 5: Commit** — `feat(agent-go): system prompt builder with stable output`

---

### Task 13: pkg/loop — 安全上限

**Files:**
- Create: `agent-go/pkg/loop/limits.go`
- Test: `agent-go/pkg/loop/limits_test.go`

**参考设计文档 §3.3（不可删）、§15。**

**Step 1: 失败测试** — 轮次超限 → `ErrMaxIterations`；墙钟超限 → `ErrDeadline`；累计成本超顶 → `ErrBudgetExhausted`；`MaxIterations: 0` 表示不限但墙钟仍生效。

**Step 5: Commit** — `feat(agent-go): unremovable kernel safety limits`

---

### Task 14: pkg/loop — Runner 内核

**Files:**
- Create: `agent-go/pkg/loop/loop.go`
- Create: `agent-go/pkg/loop/runner.go`
- Test: `agent-go/pkg/loop/loop_test.go`

**参考设计文档 §3.1 完整代码骨架、§3.2 失败语义表、§3.3 不变量。全部用 faux provider。**

**Step 1: 失败测试（覆盖 §3.2 每一行）**

```go
func TestLoop_NoToolCallsEndsTurn(t *testing.T)            // 步 12
func TestLoop_ToolCallsContinue(t *testing.T)              // 步 10
func TestLoop_MaxIterations(t *testing.T)                  // 步 2
func TestLoop_ContextCancel(t *testing.T)                  // 步 1
func TestLoop_CompactionBeforeSampling(t *testing.T)       // 步 3
func TestLoop_ToolSetRecomputedEachIteration(t *testing.T) // 步 5，不变量
func TestLoop_PublishAfterAfterModelStage(t *testing.T)    // 步 9b：切面改写后才发布
func TestLoop_DirectiveStopEndsTurn(t *testing.T)          // Clarification
func TestLoop_DirectiveContinueReiterates(t *testing.T)
func TestLoop_StopGateReinjectsBoundedByLimit(t *testing.T) // 步 11
func TestLoop_HistoryIsOnlyState(t *testing.T)              // 不变量
```

**Step 3: 实现** — 严格照 §3.1 骨架，阶段命名用 `StageBeforeAgent`/`StageBeforeModel`/`StageAfterModel`/`StageAfterAgent`。

**Step 4: `go test -race ./pkg/loop/` 干净**

**Step 5: Commit** — `feat(agent-go): for-loop kernel owning turn orchestration`

---

### Task 15: pkg/harness — 门面 + M0 端到端

**Files:**
- Create: `agent-go/pkg/harness/options.go`
- Create: `agent-go/pkg/harness/harness.go`
- Test: `agent-go/test/e2e_m0_test.go`

**Step 1: 失败测试** — `harness.New(Options{Model: faux, Tools: [echo], Middleware: nil})` → `Run` 一个"调 echo 再回答"的脚本 → 断言最终回答、工具被调用一次、History 含 4 条消息（user/assistant+toolcall/tool/assistant）。缺必填项（Model 为 nil）→ `New` 返回 error。

**Step 5: Commit** — `feat(agent-go): harness facade and M0 end-to-end test`

---

### Task 16: internal/tools/layercheck — 依赖纪律断言

**Files:**
- Create: `agent-go/internal/tools/layercheck/main.go`
- Test: `agent-go/internal/tools/layercheck/main_test.go`

**参考设计文档 §2「依赖纪律」全部 7 条。**

**Step 1: 失败测试** — 给一份伪造的 import 图，违反任一条时返回非零退出与明确信息。

**Step 3: 实现** — `go list -deps -json ./...` 解析后逐条断言。

**Step 5: Commit** — `chore(agent-go): CI layer discipline checker`

---

## M1 — 工具 · 沙箱 · 权限 · Hook · 压缩 · 路由

目标：5 级权限与 5 事件 hook 生效，bash/file 落沙箱，压缩触发正确，真实 provider 可用。

### Task 17: pkg/sandbox — Provider 接口 + local

**Files:** Create `agent-go/pkg/sandbox/{sandbox.go,local/local.go,pathmap.go}`；Test 同目录

**参考设计文档 §5；对照 `nous-agent` `agent/src/sandbox/`。**

**测试要点：** `Handle` 生命周期（`Acquire`/`Release`，`lazy_init` 首次工具调用才真正创建）；路径映射把宿主路径翻译为容器视角；**逃逸测试**：`../../etc/passwd` 被拒绝；`Release` 幂等。

**Commit** — `feat(agent-go): sandbox provider interface and local implementation`

### Task 18: pkg/sandbox/docker

**测试要点：** 用 dockertest；容器复用（同 thread 复用同容器）；超时杀进程；退出码与 stderr 透传。**打 `//go:build docker` tag，不进默认 `make check`。**

**Commit** — `feat(agent-go): docker sandbox provider`

### Task 19: pkg/tool/builtin — 文件与 bash 工具

**Files:** `ls.go` `read_file.go` `write_file.go` `str_replace.go` `bash.go` + 测试

**测试要点：** 全部经 `sandbox.Handle` 执行；`Metadata` 正确（`ls`/`read_file` 为 `IsReadOnly+IsConcurrencySafe`，写工具不是）；`str_replace` 目标串不唯一时报错；`bash` 超时与输出截断。

**Commit** — `feat(agent-go): builtin file and bash tools`

### Task 20: pkg/permission — 5 级 Policy

**参考设计文档 §8.1。**

**测试要点：** 五种模式的判定矩阵（表驱动）；`tool_overrides` 覆盖；**内部错误一律 deny（fail-closed）**；`prompt` 模式调用 `Prompter` 且超时视为拒绝。

**Commit** — `feat(agent-go): 5-level permission policy, fail-closed`

### Task 21: middleware/builtin — Permission

**测试要点：** `Deny` 时返回 `Decision{Deny:true}` 且工具**未被执行**，回灌的是错误 ToolMessage 让模型改换方案；`Grade()` 返回 `GradeGovernance`。

**Commit** — `feat(agent-go): permission middleware`

### Task 22: pkg/hooks — 5 事件 + Runner

**参考设计文档 §8.2。**

**测试要点：** 5 事件常量；外部子进程 hook（stdin 收 JSON、exit 0 放行、exit 非零 deny 且 stderr 转 reason）；进程内注册 hook 按名字解析，未知名字启动失败；`deny` 短路后续 hook；超时视为放行并记警告（不是 deny —— hook 是治理增强而非授权源）。

**Commit** — `feat(agent-go): hook runner with subprocess and in-process hooks`

### Task 23: middleware/builtin — Hook

**测试要点：** pre hook 通过 `Decision.Args` 改写工具入参后执行的是改写后的参数；post hook 的 feedback 追加到结果；`post_tool_use_failure` 在工具报错时触发而非 `post_tool_use`。

**Commit** — `feat(agent-go): hook middleware`

### Task 24: middleware/builtin — SandboxAudit · ToolErrorHandling · ToolOutputBudget

**参考设计文档 §7.4。**

**测试要点：** SandboxAudit 拦截危险 bash 模式（表驱动，含 `rm -rf /`、fork bomb、反向 shell）；ToolErrorHandling 把执行期 error/panic 转为 error ToolMessage 且回合继续；ToolOutputBudget 超 `externalize_min_chars` 时外部化并只留 head+tail+句柄，磁盘不可用时退化为截断，`read_file` 在豁免名单。

**Commit** — `feat(agent-go): sandbox audit, tool error handling, output budget middlewares`

### Task 25: pkg/compaction — 单路径摘要

**Files:** `agent-go/pkg/compaction/{compactor.go,cutpoint.go,strip.go}` + 测试

**参考设计文档 §6.2；算法对照 `agentsdk-go` `pkg/api/compact.go`。**

**测试要点（关键）：**
```go
// 切点落在 tool 事务中间时必须前移到事务起点
func TestCutPoint_NeverSplitsToolTransaction(t *testing.T) {}
// stripToolIO 剔除 tool 消息、清空 ToolCalls 与 reasoning
func TestStripToolIO(t *testing.T) {}
// 摘要模型不可用 -> 回退结构化占位摘要，不返回 error（§3.2 步 3）
func TestCompactor_FallsBackToPlaceholderSummary(t *testing.T) {}
// 摘要输入按条数与总长封顶（防压缩比不压缩更贵）
func TestCompactor_CapsSummaryInput(t *testing.T) {}
// 未达阈值不压缩；msgCount <= preserveCount 不压缩
func TestCompactor_ThresholdGate(t *testing.T) {}
```

**Commit** — `feat(agent-go): single-path LLM compaction with transaction-safe cut point`

### Task 26: pkg/model/provider/openai — OpenAI 兼容

**测试要点：** 用 `httptest.Server` 打桩，不碰真实 API。断言：`ExtraBody` 透传（deepseek thinking）；流式 SSE 解析（含 `[DONE]`、tool_call 增量拼接、`finish_reason` 映射）；上下文超限错误分类为 `ErrContextOverflow`；429 分类为 `ErrRateLimited`；`content_filter` 映射到 `StopReason`。

**Commit** — `feat(agent-go): OpenAI-compatible provider`

### Task 27: pkg/model/provider/anthropic

**测试要点：** 同上打桩。断言 system 单独字段、`thinking` 块、`stop_reason` 映射、prompt cache 标记。

**Commit** — `feat(agent-go): anthropic provider`

### Task 28: pkg/modelrouter — 分层 · 降级 · 熔断

**参考设计文档 §11.2。**

**测试要点：**
```go
func TestRouter_TierSelection(t *testing.T) {}
func TestRouter_FallbackChain(t *testing.T) {}
// 熔断按 供应商×模型 维度，半开探测恢复
func TestRouter_CircuitBreaker(t *testing.T) {}
// 429 退避重试，不立即降级
func TestRouter_RateLimitBackoffNotFallback(t *testing.T) {}
// 上下文超限 -> 强制压缩后重发，上限 2 次，不计入熔断失败
func TestRouter_ContextOverflowRecompactAndRetry(t *testing.T) {}
// 已流出内容的请求不重试（否则客户端看到两段回答）
func TestRouter_NoRetryAfterBytesStreamed(t *testing.T) {}
```

**Commit** — `feat(agent-go): model router with tiering, fallback and circuit breaker`

### Task 29: middleware/builtin — DanglingToolCall · SafetyFinishReason · LoopDetection

**测试要点：** DanglingToolCall 为缺失 ToolMessage 的 tool_call 补占位（防请求非法）；SafetyFinishReason 在 `content_filter`/`refusal` 时清空可能截断的 tool_calls；LoopDetection 检测 N 次相同 (工具名+参数) 后打断并注入提示。**顺序断言：SafetyFinishReason 注册在 LoopDetection 之后，因逆序执行故先观察原始输出（§4.4）。**

**Commit** — `feat(agent-go): dangling tool call, safety finish reason, loop detection middlewares`

---

## M2 — 运行时 · 持久化 · LangGraph 适配（关键里程碑）

目标：**现有 Next.js 前端指向 agent-go 正常跑**，SSE 契约测试通过。

### Task 30: pkg/runtime — Event 类型 + Journal 三桶

**参考设计文档 §12.4、§15。**

**测试要点：** 事件类型与三分类（`trace`/`usage`/`audit`）；Journal 三桶累加；**按 `CallID` 去重**（重复回调不重复计数）；`cost_micros` 整数微单位无浮点误差；subagent 用量回灌父 journal 且按 `subagent:<name>:<toolCallID>` 去重。

**Commit** — `feat(agent-go): run events and three-bucket usage journal`

### Task 31: pkg/runtime — EventBus 内存两通路

**测试要点：** `Publish` **非阻塞**（订阅者不消费时不阻塞发布者，满则丢最旧）；多订阅者同 run；环形缓冲上限 500；`audit` 类事件在缓冲压力下**不被丢弃**；`Unsubscribe` 后不再收到且不 panic。`-race` 干净。

**Commit** — `feat(agent-go): non-blocking in-memory event bus`

### Task 32: pkg/runtime — Redis Stream 第三通路

**测试要点（用 miniredis）：** entry ID **用 harness 序号**（`<seq>-1`）使 `Last-Event-ID` 成为真 seek；`XRANGE` 从游标之后开始而非从流头过滤；Redis 写失败时降级为仅内存且 `Publish` 仍返回成功（不阻塞循环）；TTL 设置正确。

**Commit** — `feat(agent-go): redis stream authoritative event channel`

### Task 33: pkg/runtime — RunRegistry

**参考设计文档 §12.3。**

**测试要点：** Redis 为权威（Hash 存句柄 + Pub/Sub 广播取消）；进程内只缓存本实例 `CancelFunc`；跨"实例"（两个 Registry 实例共享 miniredis）取消能生效；`OnDisconnect=cancel` 且无其他订阅者时取消，有其他订阅者时不取消；`OnDisconnect=continue` 断连不取消；TTL 续期。

**Commit** — `feat(agent-go): redis-authoritative run registry with disconnect policies`

### Task 34: pkg/runtime — RunEventStore

**测试要点：** 内存与 Postgres 两实现**共用一份契约测试**（table-driven over implementations）。断言游标语义一致：`Get(afterID, limit)` 必须真的从 `afterID` 之后取——忽略 `afterID` 的实现要能被这个测试抓住（设计文档 §5 明列的坑）。异步批量 flush，队列满丢 `trace` 保 `audit`，非可丢弃事件最多阻塞 2 秒后记 error。

**Commit** — `feat(agent-go): pluggable run event store with shared cursor contract tests`

### Task 35: migrations — 表结构

**Files:** `agent-go/migrations/001_init.up.sql` (+ `.down.sql`)

**参考设计文档 §13.1。**

**遵循迁移纪律：** 应用启动不自动执行 DDL；`NNN_description.up.sql` 命名；已合入的不得改。

**测试要点：** 全新库从 001 完整升级；重复执行幂等；`golang-migrate` 版本表 `dirty = false`。

**Commit** — `feat(agent-go): initial database schema`

### Task 36: pkg/storage — Store 接口 + Postgres

**参考设计文档 §13.2 —— 压缩持久化两条路，这是 pace-grid 坏过的地方。**

**测试要点（关键，用 dockertest）：**
```go
// 未压缩轮：只追加 Since(watermark)
func TestStore_AppendOnly(t *testing.T) {}

// 压缩轮：整体重写并标 superseded，且下一轮 LoadHistory 能读回本轮问答
func TestStore_CompactedTurnReplacesTranscript(t *testing.T) {
	// 这是 pace-grid 的真实 bug：压缩轮整轮问答不入库
}

// LoadHistory / ListVisibleMessages 只读 superseded = FALSE
func TestStore_LoadHistorySkipsSuperseded(t *testing.T) {}

// 被替换的行只标记不删除（审计记录），seq 跨压缩持续递增
func TestStore_SupersededRowsRetainedAndSeqMonotonic(t *testing.T) {}
```

**Commit** — `feat(agent-go): postgres store with superseded-based compaction persistence`

### Task 37: middleware/builtin — ThreadData · Uploads · TokenUsage

**测试要点：** ThreadData 装载 thread 元数据与工作目录；Uploads 注入文件引用；TokenUsage 写 Journal 且 `Grade()` 为 `GradeListener`（失败不判死回合）。

**Commit** — `feat(agent-go): thread data, uploads, token usage middlewares`

### Task 38: internal/langgraphapi — threads · state · history

**参考设计文档 §14；对照 `nous-agent` `agent/src/api/{threads,thread_state}.py`。**

**测试要点：** 端点形状与 Python 版一致；`/state` 的 `next` 与 `tasks` 为空数组（前端不依赖，§3.4）；`POST /state` 支持改标题；`/history` 分页。

**Commit** — `feat(agent-go): langgraph-compatible threads and state endpoints`

### Task 39: internal/langgraphapi — runs/stream SSE

**测试要点：** 四种 `stream_mode`（`values`/`messages`/`custom`/`updates`）chunk 形状；run 跑在 detached goroutine，**客户端断连不杀 run**（`OnDisconnect=continue`）；心跳注释帧；`POST /runs/stream` 无状态模式。

**Commit** — `feat(agent-go): langgraph-compatible SSE run streaming`

### Task 40: internal/langgraphapi — 重连三约束

**参考设计文档 §12.5 —— 三条各写一个测试。**

```go
// 先订阅再回放：两步之间发布的事件不得丢失
func TestReconnect_SubscribeBeforeReplay(t *testing.T) {}
// 回放分页到取空：1500 条 content_delta 必须全部送达且含 run_end
func TestReconnect_ReplayPaginatesToExhaustion(t *testing.T) {}
// 重叠按 ID 去重：同一段 delta 不得追加两次
func TestReconnect_DedupesOverlapById(t *testing.T) {}
```

**Commit** — `feat(agent-go): SSE reconnect with subscribe-before-replay, full pagination, dedupe`

### Task 41: cmd/agentd + cmd/agentctl

**测试要点：** `agentd` 启动时校验配置引用的每个名字已注册，**未知名字启动失败**（§16）；`/ok` 健康检查；优雅关闭（等在跑的 run 到 `continue` 策略允许的边界）。`agentctl migrate up/down/version`；`agentctl skill validate`。

**Commit** — `feat(agent-go): agentd server and agentctl CLI`

### Task 42: pkg/config — YAML 加载与校验

**参考设计文档 §16。**

**测试要点：** 段名与 Python 版同构；`provider:` 名字未注册 → 加载失败并列出可用名字；环境变量展开（`$DEEPSEEK_API_KEY`）；解析顺序 默认→文件→环境变量→请求级；**`pkg/loop` 不 import `pkg/config`**（由 layercheck 断言）。

**Commit** — `feat(agent-go): config loading with fail-fast name validation`

### Task 43: 契约测试 — 语义 SSE

**Files:** `agent-go/internal/langgraphapi/server_test.go`、`agent-go/cmd/agentd/main_test.go`

> **2026-08-07 范围调整：已由语义契约测试替代。** Go runtime 不复制
> LangGraph 内部语义，服务端允许两套实现并存；兼容边界收窄为
> `metadata / values / messages / custom / error / end` 六类 SSE 事件。
> `internal/langgraphapi/server_test.go` 覆盖成功、失败、原生路由与兼容路由，
> `cmd/agentd/main_test.go` 覆盖真实装配后的 HTTP/SSE 链路。原计划的 Python
> 逐字节 golden 因会错误锁定 payload 内部实现而不再作为完成条件。

**完成条件：** 成功流包含 `metadata / values / messages / custom / end`；失败流
包含 `metadata / error / end`；原生与兼容路由使用相同事件类型；内部 runtime
事件保持细粒度类型，不因 wire 投影被压平成 `content_delta`。

**Commit** — `test(agent-go): verify semantic SSE contract`

---

## M3 — Skills · Subagent · MCP · Memory

### Task 44: pkg/skill — L1 加载与 frontmatter

**参考设计文档 §9.1。**

**测试要点：** YAML frontmatter 解析（`name`/`description`/`allowed-tools`/`priority`/`mutex_key`）；**缺必填字段直接让加载失败**（不静默跳过）；`description` 长度上限校验。

**Commit** — `feat(agent-go): skill L1 metadata loading`

### Task 45: pkg/skill — L2 惰性 + mtime 热重载

**测试要点：** 首次激活才读正文；`Invalidate` 后重新读文件；mtime 变化触发重载；并发激活只读一次（`-race`）。

**Commit** — `feat(agent-go): skill lazy body loading with mtime hot reload`

### Task 46: pkg/skill — Matcher + allowed-tools 收窄

**参考设计文档 §9.2、§7.3。**

**测试要点：** 按 `Priority` 排序、`MutexKey` 互斥去重；`ForceSkills` 绕过匹配；**只能披露已注册且已通过权限校验的 `Deferred` 工具，不能借 skill 获得新权限**。

**Commit** — `feat(agent-go): skill matcher and allowed-tools narrowing`

### Task 47: middleware/builtin — SkillActivation · DeferredToolFilter

**测试要点（关键）：**
```go
// 同一 thread 只注入一次：转录已有标记则跳过
func TestSkillActivation_InjectsOncePerThread(t *testing.T) {}
// 跳过注入时仍须应用 allowed-tools 收窄与延迟披露（否则限制下一轮悄悄失效）
func TestSkillActivation_StillNarrowsToolsWhenSkippingInjection(t *testing.T) {}
// 注入位置在压缩之后，不被当轮摘要掉
func TestSkillActivation_InjectedAfterCompaction(t *testing.T) {}
```

**Commit** — `feat(agent-go): skill activation and deferred tool filter middlewares`

### Task 48: pkg/subagent — Manager + 嵌套 Runner

**参考设计文档 §10.1。**

**测试要点：** 嵌套 `loop.Runner` 有独立 History；最小中间件链；**继承父沙箱句柄**；`RestrictTools` 只能收窄不能放宽（放宽尝试报错）；`MaxTurns` 生效。

**Commit** — `feat(agent-go): subagent manager with nested runner`

### Task 49: pkg/subagent — task 工具 + 5 内置

**测试要点：** 单个 `task` 工具而非每 worker 一个（避免每请求都带全部 schema）；5 种内置定义齐全；派发结果作为 tool result 回灌父转录。

**Commit** — `feat(agent-go): task tool and five builtin subagents`

### Task 50: pkg/subagent — 异步 + 用量回灌

**测试要点：** `DispatchAsync` 返回 id，`TaskStatus` 跨"实例"可查（共享 miniredis）；**用量回灌父 Journal 的 subagent 桶且去重**——不回灌则永远为 0 且可绕过预算；`subagent_start`/`subagent_result` 事件发到父 run 流。

**Commit** — `feat(agent-go): async subagent dispatch with usage attribution`

### Task 51: middleware/builtin — SubagentLimit

**测试要点：** 超过 `max_concurrent` 的并行 `task` 调用被截断并回灌说明；`Grade()` 为 `GradeListener`。

**Commit** — `feat(agent-go): subagent limit middleware`

### Task 52: pkg/mcp — 客户端与工具注册

**测试要点：** stdio 与 SSE 两种传输（打桩 server）；工具 schema 转 `tool.Definition`；server 不可用时**降级为不注册这些工具而非启动失败**（外部依赖不该阻塞启动）；重连。

**Commit** — `feat(agent-go): MCP client and tool registration`

### Task 53: pkg/memory + middleware

**测试要点：** 事实抽取（faux 模型）；`max_facts` 与置信度阈值；注入 token 上限；更新走 `AfterAgent` 且 `Grade()` 为 `GradeListener`。

**Commit** — `feat(agent-go): memory extraction and injection`

### Task 54: middleware/builtin — Todo · ViewImage · DynamicContext · Clarification

**测试要点：** Todo 仅 plan 模式生效；ViewImage 仅 `SupportsVision` 模型生效；DynamicContext 跨压缩救援（提醒不被摘要掉）；**Clarification 拦截 `ask_clarification` 返回 `Decision{EndTurn:true}`，问题作为 ToolMessage 留在历史，不使用任何挂起机制**（§3.4）；Clarification 必须是链的终端。

**Commit** — `feat(agent-go): todo, view image, dynamic context, clarification middlewares`

---

## M4 — Swarm · 护栏 · 遥测 · 进程外插件

### Task 55: pkg/swarm — Team · Mailbox · Spawner + 迁移

**参考设计文档 §10.2。Go 与 Python 保持工具/事件语义一致，但使用独立表和
运行时，不在同一个 team 中混跑。**

**测试要点：** `agent_swarm_messages` 广播只保存一条 `to_agent='*'`，逐成员
receipt 独立消费；发送者与后加入成员不可消费该广播；`TeamManager` CRUD；
`Spawner` 复用 `subagent.Manager`；`max_team_size` 生效；Go 迁移不得修改
Python legacy 的 `swarm_*` 表。

**Commit** — `feat(agent-go): swarm team, mailbox and spawner`

### Task 56: middleware/builtin — InboxPoller

**测试要点：** `BeforeModel` 轮询注入；无消息时不注入（不产生空轮）；轮询间隔可配。

**Commit** — `feat(agent-go): swarm inbox poller middleware`

### Task 57: pkg/guardrail + middlewares

**参考设计文档 §8.3。**

**测试要点：** `fail_closed` 默认——provider 内部错误按最严格处理；输入侧命中时安全话术作为 assistant 轮写入转录并以 `content_delta` 下发（**不是 `error` 事件**），run 记 `completed`；输出侧命中时同时改写 `ModelOutput` 与 `History.ReplaceLastAssistant`，已流式则补 `message_replace`；判定作为结构化 `risk_level` 字段随 run 与 `run_end` 下发，**字段缺失 fail-closed**。

**Commit** — `feat(agent-go): guardrail provider and input/output middlewares`

### Task 58: pkg/telemetry — OTel

**测试要点：** span 层级 run → 采样 → 工具调用 → subagent 派发；三桶归因作为 attribute；audit 事件不可丢。用 in-memory exporter 断言。

**Commit** — `feat(agent-go): opentelemetry tracing with usage attribution`

### Task 59: middleware/builtin — Title

**测试要点：** 仅首轮触发；`Grade()` 为 `GradeListener`——**生成失败不得判死一个已成功的回合**（§17.1）；`max_words`/`max_chars` 截断。

**Commit** — `feat(agent-go): title generation middleware`

### Task 60: 收尾 — 文档与默认链装配

**Files:** `agent-go/README.md`、`agent-go/config.yaml`、`pkg/middleware/builtin/default.go`

**测试要点：** `builtin.Default(cfg)` 返回 24 个中间件的完整链；**顺序快照测试**；"配置声明启用的每个中间件都出现在最终链里"断言（对治 nous-agent 的 `RuntimeFeatures`/`extra_middleware` 死缝）。

**Commit** — `feat(agent-go): default middleware chain and documentation`

---

## 执行顺序约束

- Task 1→16 必须按序（后者依赖前者的类型）。
- Task 17-29 可在 M0 完成后并行（sandbox / permission / hooks / compaction / providers 四条线互不依赖）。
- Task 30-43 中，35→36 必须先于 38-40；43 依赖 39-40。
- Task 44-54 可在 M2 完成后并行三条线（skill / subagent / mcp+memory）。
- Task 55-59 全部可并行。
- Task 60 最后。

## 每个任务的完成定义

1. 测试先失败、后通过。
2. `make check` 全绿（含 `fmt-check`、`lint`、`layer-check`）。
3. 涉及并发的包 `make race` 干净。
4. 提交信息符合 `<type>(agent-go): <message>`。
5. 不留 TODO、注释掉的代码、`_v2`/`_new`/`_old` 命名。
