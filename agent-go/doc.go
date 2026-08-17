// Package agentgo is the module root for the nous-agent Go harness.
//
// 架构分层（详见 docs/plans/2026-08-06-agent-go-harness-design.md）：
//
//   - pkg/loop      唯一编排者：自持 for 循环，持有回合编排
//   - pkg/runtime/lifecycle 内核生命周期分发器，承载显式处理器
//   - pkg/*         注入接口与其实现（model/tool/sandbox/skill/subagent/runtime/storage...）
//   - internal/transport/httpapi  HTTP/SSE transport and wire projection
//
// 依赖纪律由 make layer-check 断言，违反即 CI 失败。
package agentgo
