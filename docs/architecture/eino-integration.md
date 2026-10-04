# Eino 集成边界

[总目录](README.md) · [Runtime](../../internal/runtime/README.md) · [ContextEngine](../../internal/contextengine/README.md)

宿主负责 Session reservation、配置快照、权限、事实存储和生命周期；Eino 负责 ChatModelAgent/Runner、工具调用协议、中断 checkpoint 与上下文中间件。`eino_builder.go` 收敛 Agent 创建，`executor.go` 消费运行流并持久化完整消息，而不是把每个 delta 写成事实。

内置文件工具使用 Eino filesystem Schema 和受限 Backend；MCP 使用 officialmcp 构造工具及 SDK Session。Guard 在统一入口处理权限，Ask 中断原调用；恢复使用 checkpoint 和 interruptID，前端不能替换参数。ToolCallID 使用 Eino compose 上下文，子运行和审计沿稳定调用身份关联。

上下文处理顺序为 Skill → Reduction → Summarization/硬预算。大工具结果先归档，较旧结果可以清理预览；完整事实保留。摘要待定内容在 Turn 后维护提交，维护不再次请求摘要模型。ToolCall 与 ToolResult 不被切开，缺失结果按未知解释，不能自动补成成功或重放。

主模型、摘要、视觉辅助与子 Agent 进入相同模型计账边界；任务的模型/工具次数在调用前原子预留，已报告 token 达到限额后拒绝后续调用。Provider reasoning 回放策略在模型上下文投影时处理，不改写保存的 thinking。

扩展模块通过 `component.Provider` 贡献原生 Eino 工具与内容版本；Describe 只做本地 Schema 预览，Resolve 捕获本轮配置。宿主统一处理名称冲突、预算、权限与 Reduction。框架升级时重点验证审批恢复、流式用量、ToolCall/Result 顺序和摘要事务，避免建立第二套近似协议。
