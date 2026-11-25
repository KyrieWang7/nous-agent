# Research source ledger: Codex app-server desktop projects

Audience: user evaluating open-source desktop clients built on Codex app-server\
Date: 2026-08-29\
Scope: GitHub-hosted desktop projects that directly use `codex app-server` for core agent lifecycle. Browser-only UIs, usage widgets, CLI wrappers using only `codex exec`, and repackaged official clients are excluded from the main ranking.

## Direct answer

The strongest qualifying projects are T3 Code (20.8k stars), Codexia (902), Open Codex (84), EasyCodex (33), and CodexGui (15). T3 Code and Codexia are the only candidates with substantial community signal. `codex-desktop-linux` has about 3.7k stars but is a community distribution of the official app payload, not an independent app-server client implementation.

## Claim-to-source ledger

| Claim | Source | Access notes |
|---|---|---|
| app-server is the rich-client JSON-RPC interface; stdio JSONL is default | OpenAI, `codex-rs/app-server/README.md` | GitHub page accessed 2026-08-29 |
| T3 Code is an Electron desktop app and supports Codex | T3 Code README | GitHub page accessed 2026-08-29 |
| T3 Code contains `effect-codex-app-server`, a JSON-RPC client package | T3 Code `docs/internals/workspace-layout.md`; local shallow source inspection | GitHub page/source accessed 2026-08-29 |
| Codexia is Tauri v2/Rust and uses app-server JSON-RPC for session/turn lifecycle | Codexia README | GitHub page accessed 2026-08-29 |
| Open Codex starts and manages the app-server subprocess | Open Codex README | GitHub page accessed 2026-08-29 |
| EasyCodex relay starts app-server for agent sessions, with `codex exec` used for mobile CLI windows | EasyCodex README | GitHub page accessed 2026-08-29 |
| CodexGui defaults to local app-server and supports remote WebSocket | CodexGui README | GitHub page accessed 2026-08-29 |
| codex-desktop-linux reuses the official runtime/ASAR/bundled Codex | codex-desktop-linux README | GitHub page accessed 2026-08-29 |
| Star counts | GitHub repository HTML pages; exact values also extracted from repository page markup | Snapshot on 2026-08-29; counts are volatile |

## Confidence and limitations

High confidence in the first five projects' app-server use because each has first-party repository evidence. Moderate confidence in exclusion completeness: GitHub search is noisy and some new repositories may not be indexed. Star counts are point-in-time snapshots. Agent Hub was excluded from the strict OSS ranking due to no root LICENSE at the time checked. LiveAgent was excluded because direct app-server evidence was not found in its shallow source tree.

## Searches performed and stop rationale

Searched exact phrases including `codex app-server`, desktop, Electron, Tauri, Avalonia, GUI, and GitHub topics. Follow-up searches targeted candidate repositories and source-level runtime strings. Stopped after subsequent waves primarily surfaced browser WebUIs, quota widgets, official-client repackagers, and CLI/PTY integrations; these did not change the leading qualified set.
