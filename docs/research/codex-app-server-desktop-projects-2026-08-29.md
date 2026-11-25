# 基于 Codex app-server 的开源桌面项目

检索日期：2026-08-29（Asia/Shanghai）

## 结论

真正同时满足“桌面应用、直接以 `codex app-server` 作为 Codex runtime、开放源码并有明确开源许可证”的高 Star 项目并不多。优先级非常清楚：

| 排名 | 项目 | GitHub Star 快照 | 桌面技术栈 / 平台 | app-server 证据 | 适合场景 |
|---|---|---:|---|---|---|
| 1 | [pingdotgg/t3code](https://github.com/pingdotgg/t3code) | 20.8k | Electron；Windows、macOS、Linux | 仓库包含独立的 `effect-codex-app-server` JSON-RPC client，Codex provider 启动 `codex app-server` | 最成熟的多-agent桌面控制台；首选 |
| 2 | [milisp/codexia](https://github.com/milisp/codexia) | 902 | Tauri v2 + Rust + React；Windows、macOS、Linux | README 明确写明 Codex `app-server` JSON-RPC 负责 session/turn 生命周期 | 更轻量、偏本地 IDE/worktree/调度；Codex 专注度更高 |
| 3 | [solrizon-ai/open-codex](https://github.com/solrizon-ai/open-codex) | 84 | Electron；当前以 macOS 为主 | 应用自行启动并管理 `codex app-server` 子进程 | 想研究“开源复刻 Codex App”可看，但项目很新 |
| 4 | [Ryan-Laws/easycodex](https://github.com/Ryan-Laws/easycodex) | 33 | Electron desktop relay + Android；桌面端覆盖三平台 | relay 为 agent session 启动 `codex app-server`，部分移动 CLI 窗口仍走 `codex exec` | 手机远控、本地 LAN/Tailscale 工作流 |
| 5 | [wieslawsoltes/CodexGui](https://github.com/wieslawsoltes/CodexGui) | 15 | .NET 10 + Avalonia 12 | 默认启动本地 `codex app-server`，也支持远程 WebSocket | 研究强类型协议客户端、原生跨平台 UI |

Star 是检索时快照，会持续变化。

## 最值得看的两个

### T3 Code

这是当前明显的头部项目。它不是 Codex 专用客户端，而是 Claude Code、Codex、Cursor、Grok Build、OpenCode 的统一控制面。桌面端是 Electron shell，后台负责 provider、权限、终端、文件系统和编排；Codex 集成拥有独立的 app-server client package。优点是社区和工程体量远超其他候选，缺点是架构较大，如果目标只是学习最小 app-server 客户端，会显得重。

### Codexia

如果更看重“轻量桌面端 + Codex 工作流”，Codexia 更贴题。它用 Tauri/Rust 承载桌面后端，用 JSON-RPC 对接 app-server，并将文件树、编辑器、Git worktree、任务调度和远程控制放在一个工作台里。Star 规模低于 T3 Code，但仍显著高于其余独立实现。

## 高 Star 但不应混入主榜的项目

- [ilysenko/codex-desktop-linux](https://github.com/ilysenko/codex-desktop-linux)（约 3.7k Star）：现在主要验证、重打包 OpenAI 官方 Linux 桌面 payload，并直接复用官方 Electron runtime、ASAR 和 bundled Codex。它当然最终使用官方 app-server，但不是一个独立开源实现的桌面 client，因此应归类为“社区发行/打包工程”。
- [davej/pocodex](https://github.com/davej/pocodex)（约 71 Star）：复用已安装的官方 Codex webview 和 bundled app-server，主要输出浏览器/PWA 远控体验，不属于独立桌面客户端。
- [jk-gan/agent-hub](https://github.com/jk-gan/agent-hub)（约 14 Star）：技术上是很纯粹的 Rust + GPUI app-server 客户端，但检索时仓库根目录没有明确 LICENSE，因此不纳入严格“开源许可证”排名。
- [Stack-Cairn/LiveAgent](https://github.com/Stack-Cairn/LiveAgent)（约 1.8k Star）：虽然支持 Codex，但本次没有找到它直接以 Codex app-server 为 Codex provider runtime 的可靠实现证据，因此排除。

## 选型建议

- 要社区规模、成品度、远程与多 provider：选 T3 Code。
- 要轻量跨平台、Tauri/Rust、工作区与 worktree：选 Codexia。
- 要研究 Codex App 的 Electron 复刻：看 Open Codex。
- 要手机远控桌面 Codex：看 EasyCodex。
- 要研究强类型协议层或 Avalonia：看 CodexGui。

## 判定标准与限制

纳入主榜必须同时满足：有真正的桌面 shell 或发行包；仓库文档或源码明确直接启动/连接 `codex app-server`；该通道负责 thread/turn/approval 等核心 agent 生命周期，而不只是读取用量；仓库具有明确开源许可证。官方 app-server 文档将其定义为富客户端使用的双向 JSON-RPC 接口，默认传输为 stdio JSONL；WebSocket 仍标为实验性/不受支持。

本次通过 GitHub 仓库 README、架构文档、协议 client 源码和实时仓库页面交叉核验。搜索已达到明显的收益递减点：新增结果大多是 WebUI、用量监控、官方 UI 重打包，或仅通过 CLI/PTY 支持 Codex 的多-agent工具。

## 主要来源

- [OpenAI Codex app-server README](https://github.com/openai/codex/blob/main/codex-rs/app-server/README.md)
- [T3 Code README](https://github.com/pingdotgg/t3code) 与 [workspace layout](https://github.com/pingdotgg/t3code/blob/main/docs/internals/workspace-layout.md)
- [Codexia README](https://github.com/milisp/codexia)
- [Open Codex README](https://github.com/solrizon-ai/open-codex)
- [EasyCodex README](https://github.com/Ryan-Laws/easycodex)
- [CodexGui README](https://github.com/wieslawsoltes/CodexGui)
- [codex-desktop-linux README](https://github.com/ilysenko/codex-desktop-linux)
