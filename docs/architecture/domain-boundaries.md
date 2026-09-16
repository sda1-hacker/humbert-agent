# Humbert Domain Boundaries

本文档描述当前开发版本的领域边界。数据可以在开发阶段重建，因此各 Store 只支持最新
schema，不维护旧版本迁移或双协议读取。

## Agent

Agent 是顶层 Aggregate，拥有：

- 名称、系统指令与模型角色；
- Skills、内置工具与 MCP Tool 选择；
- Sandbox/安全覆盖；
- Workspace 模式与路径；
- 该 Agent 下的全部 Sessions、附件与 Session Memory。

局部修改优先使用 `UpdateProfile`、`SetModel`、`SetModelRoles`、`SetSkills`、
`UpdateSecurity` 等窄命令，避免无关字段被旧快照覆盖。

删除 Agent 使用持久化状态机：`active -> deleting -> removed`。进入 `deleting` 后，Agent
立即从读取和列表中隐藏；应用重启会继续清理。Managed Workspace 属于 Humbert，会随
Agent 删除；Custom Workspace 是用户外部数据，只解除引用。

## Workspace

Workspace 配置直接属于 Agent：

`Agent -> Workspace`

Session 创建时解析当前 Agent Workspace，并把实际绝对路径冻结到 Session 的 `CWD`。
修改 Agent Workspace 只影响之后创建的 Session 和 Turn，不搬迁或删除原 Custom
Workspace 中的文件。

## Session

Session 只记录 `AgentID`，物理布局为：

```text
agents/<agent-id>/sessions/<session-id>/
├── config.json
├── session.jsonl
└── attachments/
```

`config.json` 是低频控制面；`session.jsonl` 是 Message、Thinking、ToolCall、ToolResult
与 Conversation Tree 的唯一事实来源。Store 只接受当前 schema。

一个 Session 的配置损坏时，Store 会隔离该 Session、保留原文件并记录诊断；其它健康
Session 仍可加载，应用启动不会被单个损坏会话阻塞。仅当 `config.json` 缺失且 transcript
header 合法时，Store 才会重建当前版本配置。

## Runtime

每个 Turn 冻结不可变快照：

`Session + Agent + Workspace + Model + Tools + Skills + MCP + Sandbox + Context`

同一 Session 同时最多有一个 Turn/压缩/删除操作。删除 Agent 时，Runtime 在同一并发
边界内拒绝新操作并检查已有 Session reservation；随后由 Agent 删除状态机级联清理。

## Chat Input 与附件

用户输入由文本和零个或多个附件组成。附件字节保存在 Session 的 `attachments/` sidecar，
JSONL 只保存稳定引用与元数据。Provider 调用前才把引用恢复为 Eino 多模态内容，因此
Base64 不进入 transcript、memory 或 compaction 记录。
