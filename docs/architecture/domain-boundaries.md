# 数据与领域边界

[架构目录](README.md)

| 所有者 | 权威数据或职责 |
| --- | --- |
| Agents | Agent Profile、模型与能力选择、工作区绑定。 |
| Models / Credentials | 模型供应商配置与秘密存储；DTO 不回显秘密。 |
| Sessions | SQLite 会话元数据与附件引用；通过 Transcript 操作消息。 |
| Transcript | JSONL 消息树、完整模型消息和工具事务。 |
| ContextEngine / ContextArtifact | 上下文预算、派生摘要与可回查的大结果。 |
| Runtime | Session 占用、冻结 Snapshot、Turn 生命周期和 Eino checkpoint。 |
| Permission / Approval / Sandbox | 授权身份、人类确认以及文件/进程/网络边界。 |
| Skills / MCP / 扩展模块 | 各自配置、Agent 绑定、内容版本和连接。 |
| Tasks / Proactive / Notifications | 调度、任务状态、主动策略和通知。 |
| SearchIndex | 可重建 SQLite 搜索投影及其后台刷新。 |
| EventBus / Wails / Pinia | 瞬时事件与前端实时状态，不是历史事实。 |

跨模块规则由 `usecases` 调用这些公开服务完成，不越过边界扫描或改写其他模块目录。Application 负责构造和资源所有权，Services 负责桌面传输。

每轮模型、工具、工作区和安全配置在 Snapshot 中冻结。修改配置只影响后续 Turn；审批恢复沿用原 checkpoint 和能力身份。新增模块长期 Allow 绑定模块版本及沙箱身份，Deny 保留稳定模块/工具范围。

TaskRun 保存调度、计数、审批投影及短摘要，不复制完整 Transcript。重启将遗留活动状态收敛为 interrupted，不恢复进程内 checkpoint，也不自动重放可能已有副作用的工具调用。

扩展优先使用独立包、显式构造与静态装配。无需服务定位器、反射容器、另一套事件持久化或新 Agent 执行循环。当前不提供动态热卸载，也不把所有业务都强行改为一个泛化接口。
