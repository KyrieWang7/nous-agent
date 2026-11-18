# 原生插件控制与运行时切换

接续 `2026-11-17-session-inbox-and-nested-agents.md`。实现管理已安装的 `plugin.json` 命令插件，不包含在线市场、ZIP 上传或未实现的安装按钮。

## 操作契约

- 设置 → 插件展示实际安装包、声明工具、独立启用选择、当前加载状态，以及旧运行持有数。插件宿主开关与单个插件选择分开，关闭宿主会保留个体偏好。
- GET `/api/v1/runtime/plugins` 返回清单与配置 revision。POST 同路径提交 `{name?, enabled, revision}`，省略 name 表示宿主开关；POST `/api/v1/runtime/plugins/reload` 提交 `{revision}` 重装运行环境。
- 修改使用 revision 比较并持有跨进程文件锁。先完整装配候选 generation，再原子持久化设置、验证精确配置版本并发布。装配失败会释放候选资源，原配置与原 generation 保留；过期写入返回 409。前端不乐观修改状态，不自动重试 POST。
- 新运行立即使用新 generation。已有运行保留冻结的能力与工具目录，结束后才释放旧环境；页面显示真实引用数，不以定时器推导“已停止”。旧环境释放错误作为 close_error 返回并显示。
- 不通过网页接收命令、可执行路径或插件目录。操作只能指定从服务配置目录发现的已安装插件名称；API 清单不返回命令行和环境变量。
- 子 Agent 继续复用父运行持有的 generation，依赖注入、Apply 注册、失败回滚和 scope 清理由既有原生插件管理器承担。

## 设置存储与部署范围

配置文件单独保存，不改写插件包或 Gateway 的 MCP/Skill 配置：

1. 显式 `NOUS_PLUGIN_STATE_PATH`；
2. 未设置时，使用 `NOUS_EXTENSIONS_CONFIG_PATH` 同目录的 `plugins.json`；
3. 两者均未配置时，使用 `.nous/plugins.json`。

文件以 0600 权限临时写入、fsync 后原子 rename；`.lock` 负责串行化服务进程的控制写入。多个实例需要共享该配置文件及安装目录，才能观察相同的设置；执行引用数和关闭错误属于当前服务实例，不表示全服务集群已排空。

修复了 catalog fingerprint 对 `~/` 安装目录未展开的问题。Air 开发监听排除 `extensions`、运行时目录和测试源码，避免管理配置触发进程重启并取消运行。生产配置的模型、数据库连接和 HTTP 中间件等既有重启约束仍由配置校验维持。

## 验证

`TestPluginControlPersistsAndDrainsRealRegistrations` 使用真正的命令插件注册与 scope 释放，验证启停、旧运行租约、幂等释放、原注册移除、配置重建恢复、第二个运行时观察变更、过期 revision、候选装配失败回滚和候选注册清理，以及旧环境释放错误的可见性。

Playwright 通过实际设置入口调用后端，实际模型执行只读验收插件命令。先观察 tool_start，再禁用插件，验证新环境不再加载该工具、旧运行仍得到未出现在提示词中的随机标记，随后旧 generation 引用归零。并检查重载、409 后开关保持实际状态且不自动 POST，以及桌面和移动端入口。验证包仅用于本地验收，结束后删除。

原始脚本、截图、服务重启后设置恢复记录及最终结果位于 `/tmp/nous-plugin-control-20261117/`。

最终验收：thread `plugin-ui-938b038ba20d4b448ae4f08c784093e8`，run `93366b38-0e6b-4acc-968a-19add87d6145`。禁用时返回 loaded=false、retained_runs=1，实际命令完成后归零；运行成功。桌面和移动端均通过，无页面 JS 异常，验收插件包及其启用偏好已清理。
