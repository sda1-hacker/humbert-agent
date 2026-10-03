# 一轮聊天的代码阅读路径

[架构目录](README.md)

1. `frontend/src/api/chat.js` 调用既有 `ChatService.StartTurn`，Wails DTO 转为 Runtime 输入。
2. `runtime/service.go:StartTurn` 占用 Session、保存或复用用户消息，再解析快照。占用和删除门闩在 `operations.go`。
3. `resolver.go:ResolveTurn` 读取模型、工作区与上下文；`capabilities.go` 共用 Builtin/Skills/MCP/扩展的能力装配，`extensions.go` 检查模块绑定、来源、版本和工具 Schema。
4. `eino_builder.go` 构造 Eino ChatModelAgent，固定 Skills → Reduction → Context Middleware 顺序。`executor.go` 创建 Runner、消费流式事件并持久化完整 Assistant/Tool 消息。
5. `runs.go` 处理正常完成、失败、取消或中断。审批等待与 checkpoint 恢复在 `approvals.go`；等待期间保留原 Snapshot 和 Session reservation。
6. `events.go` 投影瞬时事件，经 EventBus 与 ChatService 进入 `stores/runtime.js`；终态重新读取 Session 消息，旧请求不能清理新请求状态。

从 RequestID 追事件，从 SessionID 追消息事实，从 RunID 追 checkpoint。排查工具能力时比较实际工具列表、用于预算的 Schema 列表和 RuntimeManifest；排查重复消息时检查 StartTurn 收据和 Transcript 写入，不把 delta 当作持久化消息。

子 Agent 使用相同能力装配和 Eino 构造，读取自己的模型与选择，但不读取父 Session 历史，并继承父运行的 Workspace/Sandbox/授权范围。Task 使用相同 Runtime Service，另附执行限额。
