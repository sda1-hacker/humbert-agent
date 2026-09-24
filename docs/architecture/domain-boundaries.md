# Humbert Domain Boundaries

本文档描述当前开发版本的领域边界。会话控制面仅采用 SQLite；旧版 Session config 文件不导入。
其它 Store 目前只支持当前 schema，发布后改变其结构前必须增加逐版本迁移，不能直接拒绝已有用户数据。

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
agents/session-metadata.sqlite
agents/<agent-id>/sessions/<session-id>/
├── config.json  # 旧版遗留文件，当前版本忽略
├── session.jsonl
├── session.locations.jsonl
└── attachments/
```

`session-metadata.sqlite` 是标题、归属、归档及列表排序时间的控制面事实来源。
启动时不会读取旧 `config.json`。若数据库缺少记录，Store 只读 JSONL Header 恢复会话身份、CWD 和创建时间，标题设为“恢复的会话”，归档状态设为未归档。
`session.jsonl` 是 Message、Thinking、ToolCall、ToolResult 与 Conversation Tree 的唯一事实来源。
消息提交后更新时间索引；若进程恰在两次提交之间退出，启动扫描会用 JSONL mtime 修复排序时间。
因此元数据库属于用户数据，备份时不能按可重建缓存跳过。

较小的 Transcript 使用容量受限的进程内 Document LRU。超过完整文档缓存上限后，Store
用 `session.locations.jsonl` 记录 Entry 身份与字节位置；首次建立或索引失效时会严格扫描
完整 JSONL。正常追加增量更新位置、分支、压缩切点与消息分页索引。构建模型 Context 时只
解码最新压缩检查点对应的原始窗口，Memory 仍可校验完整分支的 Entry 身份；旧历史按需
通过位置读取。索引以 Transcript 的文件身份、大小和修改时间校验，崩溃后可重建。
`session.jsonl` 始终是唯一事实来源。

数据库无记录且 Transcript Header 损坏时，Store 会隔离该 Session、保留原文件并记录诊断；其它健康 Session 仍可加载。旧 `config.json` 是否存在、是否损坏均不影响恢复。数据库已有记录时，也不会用旧 JSON 覆盖较新的标题。

## Runtime

每个 Turn 冻结不可变快照：

`Session + Agent + Workspace + Model + Tools + Skills + MCP + Sandbox + Context`

同一 Session 同时最多有一个 Turn/压缩/删除操作。删除 Agent 时，Runtime 在同一并发
边界内拒绝新操作并检查已有 Session reservation；随后由 Agent 删除状态机级联清理。

## Task 与 TaskRun

Task 直接归属 Agent，不引入 Project：

`Agent -> Task -> TaskRun -> Session`

Task 是低频控制面，保存提示词、启用状态、结构化 Schedule、Misfire/Overlap Policy 和单次
执行限制。TaskRun 是持久化状态机：

`queued -> starting -> running -> waiting_approval -> running -> terminal`

终态包括 `succeeded`、`failed`、`cancelled`、`timed_out`、`interrupted` 与 `skipped`。
计划触发以 `TaskID + ScheduledFor` 做幂等去重；`queue_one` 最多保留一个候补。Scheduler
限制全局和单 Agent 并发，并在 Agent 删除边界内停止新建运行。Retry 记录父 Run ID；启动
扫描会幂等补建“失败终态已落盘、重试尚未创建”这一崩溃窗口中的候补 Run。

TaskRun 启动时创建普通 Session。Session JSONL 仍是用户消息、模型回复和工具调用/结果的
唯一执行事实来源；TaskRun JSON 不复制完整 transcript，只保存调度/重试状态、计数、可安全
展示的 Approval Presentation 与短结果摘要。应用重启时，遗留活动状态统一收敛为
`interrupted`，并清除 Approval 投影；不会恢复进程内 checkpoint，也不会自动重放可能已有
副作用的 Tool Call。原来尚未开始的 `queued` 运行可以继续分派。

普通聊天不启用 Task 执行限额。TaskRunner 在同一 Runtime 快照上附加最长时长、模型调用
次数和工具调用次数限制，因此仍复用既有 Context、Permission、Sandbox、Skills 和 MCP
链路，不存在第二套弱化的执行器。

主动助手先把外部事件写入 `config/proactive.json` 的持久化 Inbox，再唤醒进程内消费者。
事件只有在处理记录已经落盘后才从 Inbox 确认删除；重复 EventKey 会被去重。内部 Agent
运行以 `Origin + OriginRef` 作为幂等键，关闭“领域记录已保存但 TaskRun ID 尚未回写”的
崩溃窗口。重启时普通计划队列仍按调度策略恢复，未完成的内部自动运行则收敛为
`interrupted`，避免重放未知副作用。

## Agent-as-Tool 协作

协作链路是：

`Parent Session -> Parent ToolCall(run_agent) -> isolated Child Runtime -> Parent ToolResult`

`run_agent` 不是后台 Task，也不创建 Child Session。父 Agent 在当前 Turn 内等待 Eino
AgentTool 完成；子 Agent 只收到工具参数中的自包含 task，拥有独立的临时消息上下文。完成后
只有最终结果回到父 Agent，父 Agent继续推理并生成用户回答。父 Session 的标准 ToolCall 与
ToolResult 因而自然保留结果，后续 Context/Compaction/History 不需要额外注入协议。

子 Runtime 使用目标 Agent 的 Profile、Chat Model、Instruction 和自身选择的
Builtin Tool、Skill、MCP，不与父 Agent 的模型可见能力清单取交集。Workspace、
Sandbox、Network 与 Permission 沿用父 Runtime：子 Agent 可以使用自己的专业能力，
但不能借此扩大文件、网络或授权边界，审批仍定向当前父会话。`session_history`、`context_resource`、
`install_skill`、`list_agents`、`run_agent` 不向子 Runtime 暴露：子 Agent 不能读取父会话、
修改 Agent 配置或递归创建更多子 Agent。

Agent Profile 的 `subagent_enabled` 是显式能力边界，默认关闭。`list_agents` 只列出已开启的
Agent，`run_agent` 在执行前再次读取并校验目标 Profile，避免仅靠前端隐藏造成越权调用。

子工具需要审批时，Eino CompositeInterrupt 把中断沿 AgentTool 边界传回父 Runtime，审批卡
仍显示在当前对话；恢复时复用父 checkpoint 和稳定 ToolCall ID。每次调用在父 Session 目录的
`subagents/<subrun-id>.json` 保存轻量审计（目标 Agent、task、状态、结果/错误），但它不是消息
事实来源，也不会显示为侧栏会话。

## Chat Input 与附件

用户输入由文本和零个或多个附件组成。附件字节保存在 Session 的 `attachments/` sidecar，
JSONL 保存稳定引用、元数据，以及文本类文件的确定性 UTF-8 提取结果。Provider 调用前，
近期图片引用恢复为 Eino Base64 多模态内容，并保留到紧邻的一次用户追问；再早的图片在
Provider 请求中变为包含名称、MIME 和 Attachment ID 的文本占位，避免每轮重复读取、
Base64 膨胀及上传同一二进制。文本、源码和 JSON/YAML/XML 等文件转换为普通 text part，
从而不依赖 OpenAI Chat Completions/Ollama Adapter 尚未实现的原生 `file_url`。PDF、DOCX、
XLSX、PPTX 的原生文本由受限文档解析器提取；扫描版 PDF 在未配置 OCR 时明确拒绝。
其它二进制文件在写入 Session 前拒绝。Base64 不进入 transcript、memory 或 compaction
记录，压缩与 Memory 会保留附件名称、类型和文本提取结果。

模型自身的 Vision/Files/Audio 等 Capability 在“设置 → 模型”维护。应用级图片路由保存在
`config/models.json.multimedia.image_model_id`，只允许引用已启用且有效 Vision Capability
为 true 的模型。Agent Profile 不再保存 Vision Model；当前 Chat 模型缺少 Vision 时，Runtime
会先调用全局图片模型生成受长度和 Context 预算限制的“不可信视觉观察”，移除发给主模型的
图片二进制，再由 Chat 模型结合观察结果继续推理和调用工具。辅助图片模型和主 Chat 模型共享
同一次 Task 的模型调用次数上限。音频与视频
在完整解析链路落地前不提供虚假的应用级模型选择器。
