# 本地启动与真实模型烟测

日期：2026-11-17；分支：develop。

- Frontend： http://localhost:7775 ，本地 Next.js dev。
- Go Agent： http://localhost:7776/ok ，Docker + Air，200。
- Go Gateway： http://localhost:7777/health ，Docker，200。
- Remote Sandbox daemon：Compose 内部服务，healthy。

本轮按当前代码重新构建并启动三个容器。旧 Agent 未挂载 platform-go 导致编译失败；
新 Compose 挂载已生效。复用既有本地 sandbox controller token 补充到被忽略的 .env，
没有写入报告或版本控制。继续使用本地 PostgreSQL/Redis 和已有模型配置，未执行数据库迁移。

## 真实验收

1. 浏览器首页、模型目录代理和模式切换正常；4 个模型可见。
2. 浏览器 Flash 对话经真实配置模型返回“本地服务运行正常。”，约 2.28 秒；
   SSE 200，未见页面脚本错误或失败接口。刷新后历史回复正常恢复。
3. API Flash 请求返回 LOCAL_AGENT_OK，终态 success。
4. Ultra/fork：检查并批准仅一次只读 task 的测试计划后，explore 子任务返回 SUBAGENT_OK；
   子执行约 1.2 秒，stop_reason=completed，父任务完成待办并 success 收尾。
5. 原始 model_input_committed 证明 fork 包含上一轮已完成的 LOCAL_AGENT_OK 对话，
   排除当前父任务的输入；task_started、task_completed 与 subagent_result 正常。

对话验收：http://127.0.0.1:7775/workspace/chats/9d147cda-0f3f-47ac-994d-c7907cff2d44

子任务验收：http://127.0.0.1:7775/workspace/chats/smoke-6fbee743-91a2-4aa8-a96f-8b4b261533ef

临时脚本、SSE/原始事件、浏览器截图位于 /tmp/nous-agent-smoke-20261117/。
本轮模型测试未调用文件、搜索、系统命令工具；未把后台任务派发新增为产品工具。
服务保持运行，改动未提交、推送或部署到远端。
