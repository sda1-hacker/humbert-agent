# Humbert 架构与代码阅读指南

本文面向准备修改后端的开发者。以当前代码为准，先追踪一轮普通聊天，再按需要阅读上下文、工具、任务和数据模块。每个包的细节与图示见[架构手册总目录](README.md)。界面通过 Wails Service 调用 Go Core；`internal/services` 是桌面适配层，领域规则在 `internal/` 对应包中。

## 1. 先建立全局图

```text
Vue 组件 / Pinia Store
        │  Wails Call.ByName；Wails Events.On
        ▼
internal/services              桌面 DTO、参数转换、事件桥
        │
        ▼
internal/app                   Bootstrap 组装依赖与关闭顺序
        │
        ├── agents / models / sessions / transcript
        ├── runtime → resolver → contextengine / memory
        │             └── executor(Eino) → tools / skills / mcp
        ├── permission / approval / sandbox / workspace
        ├── tasks / proactive / notifications
        └── searchindex / databackup
                    │
                    ▼
       ~/.humbert-agent 下的文件、系统凭据库与可重建 SQLite 索引
```

有两条必须区分的数据通道：

- **事实写入**：Session 的 `session.jsonl` 保存用户、助手、工具和压缩检查点；Agent、Task、权限等配置各有自己的 Store。以这些持久化记录为准。
- **实时展示**：`internal/eventbus` 传递 Runtime 和 Task 事件，Wails 桥接给 Vue。流式 delta 可以随时丢弃；界面重载后应从 Session Store 恢复完整消息。

一次 Turn 会冻结 Agent、模型、工作区、工具、Skills、MCP、Sandbox 和 Context。运行过程中修改设置只影响之后的 Turn。同一 Session 的 Turn、压缩与删除由 Runtime 串行化；不同 Session 可以并发。

## 2. 启动与关闭

入口是 `cmd/desktop/main.go`：先处理上次安排的离线恢复/备份，再调用 `app.Bootstrap`，通过 `services.All(core)` 注册 Wails 服务，启动窗口；退出时调用 `core.Shutdown`。离线数据 CLI 的入口在 `cmd/data/`。

`internal/app/application.go` 的 `Bootstrap` 是依赖图的唯一组装处。建议按以下顺序读它：

1. `config.Load`、`logging.New`、凭据库与文件 Store；
2. Workspace、Sandbox、Permission/Approval；
3. Model、Agent、Session、Transcript，以及启动时的数据恢复；
4. Tool Registry、Skills、MCP、Memory、ContextEngine；
5. Runtime Resolver/Executor/Service；
6. Tasks、Proactive、Notifications、SearchIndex 和服务关闭顺序。

`internal/services/enter.go` 决定哪些方法暴露给桌面端。新增功能时先判断是新的领域规则，还是现有服务的一种 DTO 映射；避免把持久化逻辑放在 Wails Service 中。

## 3. 一次普通聊天的完整链路

```text
ComposerBar.vue
  → frontend/src/stores/runtime.js
  → frontend/src/api/chat.js
  → services.ChatService.StartTurn
  → runtime.Service.StartTurn
      ├─ reserveAgentSession：占用 Session
      ├─ sessions.Service.PrepareUserMessage：写入或安全复用用户消息
      ├─ runtime.Resolver.ResolveTurn：构造本轮快照
      │   ├─ 读取 Agent / Session / Model / Workspace
      │   ├─ 解析 Tool / Skill / MCP / Sandbox
      │   ├─ contextengine.Engine.Build
      │   │   └─ sessions.LoadContextTranscript → transcript.Store
      │   ├─ 必要时持久化 Compaction，再重建 Context
      │   └─ 图片附件恢复；聊天模型不支持视觉时走辅助视觉模型
      └─ 异步执行 runtime.Service.executeTurn
          → runtime.Executor.Execute
          → Eino ChatModelAgent / Tools
          → 完整 Assistant / Tool 消息写入 Session
          → Runtime EventBus → ChatService → Wails → runtime store
```

`StartTurn` 先占用 Session，再保存用户消息，然后解析快照。若快照失败，前端仍需要知道已经保存的 `UserMessageID`，以便重试时复用最后一条未回复的相同输入；这由 `ChatService.StartTurn` 的收据处理。不要把 Wails 返回错误等同于“用户消息一定没有落盘”。

`runtime.Service` 管 Turn 的开始、取消、审批暂停/恢复、结束和 Session 占用；`Resolver` 负责选择配置及 Context；`Executor` 负责调用 Eino 和消费流式事件。`Executor` 不重新决定权限策略。工具调用的实际边界仍由 Tool Registry、Permission 和 Sandbox 校验。

向前端的事件由 `services.ChatService.ServiceStartup` 订阅 `runtime.TopicEvent` 并用 `humbert:runtime:event` 发出。前端 `frontend/src/stores/runtime.js` 订阅后更新实时状态，最终再读完整消息。调试“界面有 delta 但刷新后消失”时，检查完整消息的写入，而不只是事件桥。

### 审批分支

```text
工具触发 Ask → Eino interrupt + checkpoint
  → runtime.Service.registerInterruptedRun / awaitApproval
  → Approval Manager + Wails 审批卡
  → ChatService.ResolveApproval → runtime.Service.ResolveApproval
  → Executor.Resume（同一 RunID 和 checkpoint）
  → 工具继续执行或拒绝 → 正常终态写入
```

审批恢复使用 checkpoint 保存的原始工具参数；前端只提交决策，不重新提交工具参数。等待审批时 Session 仍被当前 Turn 占用。`runtime.Service.CancelTurn` 和应用关闭也要处理这条等待分支。

## 4. Context、Transcript 与 Memory

阅读顺序：`sessions/service.go` → `transcript/store.go`/`location_index.go` → `contextengine/engine.go`/`projection.go`/`planner.go` → `memory/manager.go`。

- `sessions.Service` 用 Session ID 安全定位 Agent 目录，处理用户输入、附件、完整消息、分页和压缩记录。
- `transcript.Store` 把 `session.jsonl` 当成唯一消息事实来源。小会话可命中容量受限的 Document 缓存；大会话使用 `session.locations.jsonl` 中的字节位置按需读。位置索引失效时可由原始 JSONL 重建。`LoadContextSession` 返回当前分支所需窗口；`LoadMessagePage` 读取历史页。
- `contextengine.Engine.Build` 根据模型上下文窗口算预算，读取当前分支，将最近有效压缩检查点与之后的消息投影成 Eino 消息，加入受控 Memory 和引用，再估算 Token。`projection.go` 处理 thinking、工具事务与长工具结果的可见形式；`planner.go` 决定何时压缩及切点。
- `Resolver.ResolveTurn` 在首次模型调用前按需持久化压缩检查点；执行过程中的中途保护由 MidRun middleware 完成。完整原始历史没有因压缩而删除。
- `memory.Manager` 生成可重建的会话事实；`personal-memory.json` 是用户确认后的跨会话个人记忆。历史 thinking 可用于界面展示，但不作为普通上下文和记忆反复注入。

`message index` 是消息页定位数据，`location index` 是 JSONL 字节位置，`memory index` 是派生记忆的校验/增量依据；它们服务于不同读取路径，均不是第二份消息真相。修改 JSONL wire 格式或 Context 投影时，要同时检查索引重建、旧会话兼容、工具调用与结果成对、压缩后回查历史。

附件原件在 Session 的 `attachments/`。文本和文档的提取内容按受限规则送入模型；`extract_document` 可按需把 PDF、DOCX、XLSX、PPTX 转为 Markdown 并分段读取。`context-artifacts/` 保存被缩短的大结果，供 Agent 用 `context_resource` 回查。

## 5. 各功能模块怎么实现

| 模块 | 主要代码 | 入口与实现要点 |
| --- | --- | --- |
| 配置、凭据、日志 | `internal/config`、`credential`、`logging` | 启动配置由 Viper 读取；模型密钥走系统凭据库；统一结构化日志用于诊断。 |
| Agent 与模型 | `internal/agents`、`models` | Agent Profile 引用模型、工具和工作区；Registry 构造提供商适配器；删除 Agent 有可恢复的状态流程。 |
| 会话与附件 | `internal/sessions`、`transcript` | 元数据与追加式 JSONL 分开；附件保留原件；消息分页和 Context 读取有独立入口。 |
| Runtime | `internal/runtime/service.go`、`resolver.go`、`executor.go` | Service 管生命周期和互斥，Resolver 冻结快照，Executor 调 Eino，事件经 EventBus 广播。 |
| Context 与记忆 | `internal/contextengine`、`memory` | 预算、投影、压缩、派生会话事实及用户确认的个人记忆。 |
| 内置工具 | `internal/tools/registry.go`、`tools/builtin`、`app/tools.go` | Registry 注册/解析能力；Builtin 负责文件、搜索、文档、浏览器、命令等实际动作。 |
| 扩展能力 | `internal/skills`、`mcp`、`einoadapter`、`collaboration` | Skills 以 `SKILL.md` 加载，MCP 连接外部工具；`run_agent` 在父 Turn 内同步运行隔离子 Agent。 |
| 权限与执行边界 | `internal/permission`、`approval`、`sandbox`、`workspace` | 权限按风险与规则判定，Ask 产生审批，Sandbox 约束命令/文件/网络；前端隐藏按钮不是授权。 |
| 工作区视图 | `internal/workspaceview`、`services/workspaceservice.go` | 右侧文件树与预览读取当前 Agent 工作区，和 Runtime 共用 Workspace Manager。 |
| 任务与通知 | `internal/tasks`、`notifications` | Task 配置和 Run 状态持久化；Scheduler 只在应用运行期间触发；Run 复用普通 Runtime/Session。 |
| 主动助手 | `internal/proactive` | 外部事件先进入持久化 Inbox，再由 Manager 决策并交给通知或 Task 执行器。 |
| 搜索与备份 | `internal/searchindex`、`databackup` | 两个 SQLite 文件是可重建搜索投影；备份/恢复处理持久化数据与凭据。 |
| 桌面 API | `internal/services`、`frontend/src/api` | DTO 和 Wails 方法名的适配，不另起一套业务状态。 |

### 模型、工具与安全

`models.Store` 保存 Provider 和 Model 的非密钥配置，`models.Registry` 根据这些配置和凭据库构建可调用模型。Agent Profile 选择模型角色及可用能力，`Resolver` 在每轮运行时解析并验证它们。聊天模型缺少视觉能力时，图片先经应用配置的视觉模型生成受限观察，再交给聊天模型继续完成该 Turn。

内置工具在 `app/tools.go` 注册，`tools.Registry.Resolve` 按当前 Agent 的选择构建本轮工具集合。`tools/builtin` 中的具体实现处理输入校验和外部动作；文档解析、网页搜索、浏览器和命令各有自己的限制。Skills 由 `skills.Manager` 管理文件与快照，在 Eino middleware 中按本轮配置提供指令/能力；MCP 由 `mcp.Manager` 管理服务器配置并经 `mcp/einoadapter` 转成工具。子 Agent 的 `run_agent` 是父 Turn 内的工具调用，结果回到父会话，不创建后台 Task。

执行工具前，`permission.Engine.Evaluate` 结合风险和已有规则决定允许、拒绝或询问；询问由 `approval.Manager` 和 Eino checkpoint 连接到当前 Turn。`sandbox.Manager.Resolve` 计算工作区、命令及网络的有效边界。新增有副作用的工具要同时检查 Registry 暴露、Permission 请求和 Sandbox 约束，不能只靠 UI 控件限制调用。

### 任务、主动事件与搜索

`tasks.Store` 分别保存 Task 配置和每次 Run 状态。`tasks.Manager` 的调度循环扫描到期任务并按错过执行、重叠及并发策略入队；`startRun` 创建或复用 Session，再调用普通 `runtime.Service.StartTurn`。它订阅 Runtime 事件，更新 Run 的审批、用量、成功/失败等状态，完成后发送通知或安排重试。应用重启会收敛中断运行，避免重放可能已有副作用的工具调用。

`proactive.Store` 先把待处理事件写入 Inbox；`proactive.Manager` 订阅事件、恢复未处理记录并选择通知或 Agent 执行器。Agent 执行器复用 Task Manager，因此仍使用同一 Runtime、权限和会话链路。`searchindex` 读取持久化消息或工作区文档，更新两个 SQLite 搜索投影；结果可从事实来源重建。`databackup` 的离线 CLI 和启动前操作处理加密归档、校验、恢复与凭据导入。

### 前端怎么对应后端

`frontend/src/api/` 集中封装 `Call.ByName`；`stores/agents.js`、`sessions.js`、`runtime.js`、`tasks.js` 等保存界面状态和加载动作。`ComposerBar.vue` 发起输入，`ChatView.vue`/`MessageList.vue` 展示会话，`ApprovalCard.vue` 提交审批。`components/workspace/WorkspaceFileTree.vue` 与 `WorkspacePreview.vue` 展示文件。`utils/toolProtocol.js` 和 `toolTrace.js` 把持久化消息投影成可读的工具轨迹，`toolEffects.js` 从已完成结果提取文件变化；它们不修改后端会话事实。

`internal/tasks` 现在按职责阅读：`manager.go` 是依赖、启动、关闭和并发边界；`manager_task.go` 是配置修改、删除与会话引用清理；`manager_schedule.go` 是排程和启动；`manager_events.go` 把 Runtime 事件转换为 Run 状态、结果通知与重试。TaskRun 终态和崩溃恢复规则见 [领域边界](domain-boundaries.md)。

## 6. 数据落在哪里

默认根目录为 `~/.humbert-agent/`；实际路径由 `config.Load` 解析。下面列出追踪代码最常用的文件：

```text
config.yaml                                  启动配置
config/providers.json、models.json           Provider 与模型非密钥信息
config/permissions.json、proactive.json      权限、主动助手配置与 Inbox
config/personal-memory.json                 用户确认的跨会话记忆
agents/<agent-id>/config.json               Agent Profile
agents/<agent-id>/sessions/<session-id>/
  config.json                                Session 控制面
  session.jsonl                              消息、工具事务、压缩检查点
  session.locations.jsonl                    可重建的字节位置索引
  memory.json                                派生会话记忆
  attachments/、context-artifacts/、subagents/
agents/<agent-id>/tasks/<task-id>/
  config.json、runs/<run-id>.json             Task 与 TaskRun 状态
workspaces/                                 应用管理的工作区
cache/conversation-search.sqlite            会话正文搜索投影
cache/document-search.sqlite                工作区文档搜索投影
logs/humbert.log                            结构化日志
```

这些 SQLite 文件只用于搜索，不承担消息或任务的唯一存储。运行时不要直接编辑活跃会话的 JSONL；先通过 `sessions`/`transcript` 的 API 修改。持久化格式升级要考虑旧数据迁移和崩溃后的重试。详细所有权及恢复约束见 [领域边界](domain-boundaries.md)。

## 7. 从哪里开始学习

建议每一步都先读入口函数，再运行对应测试，不必顺着大文件从第一行读到最后一行。

1. **跑起来并找到入口**：读 `README.md` 的快速开始、`cmd/desktop/main.go` 和 `app.Bootstrap`；搜索 `services.All` 看桌面暴露面。
2. **走通一轮无工具聊天**：从 `ComposerBar.vue` 的发送动作，跟到 `ChatService.StartTurn`、`runtime.Service.StartTurn`、`Resolver.ResolveTurn`、`Executor.Execute`，最后看 `sessions.AppendAssistantMessage` 与前端事件处理。
3. **看长会话为何仍可用**：读 `transcript.Store.LoadContextSession`、`contextengine.Engine.Build`、`projectActiveBranch`、`Resolver.compactUntilSafe`；比较 JSONL 和 location index 的职责。
4. **跟一个工具和审批**：从 `app.buildToolRegistry` 和 `tools.Registry.Resolve` 找到具体 Builtin，再跟 `permission.Engine.Evaluate`、`approval.Manager`、`Executor.Resume`。
5. **跟一个任务**：从 `services.TaskService` 到 `tasks.Manager.Create`、`enqueueDue`、`dispatchLocked`、`startRun`、`handleRuntimePayload`；观察 Run JSON 与对应 Session JSONL 分别存什么。
6. **最后看跨领域功能**：Skills/MCP、主动事件、子 Agent、搜索与备份。它们复用上述 Runtime 和 Store 边界。

最小本地验证：

```bash
cd frontend && npm ci && npm test && npm run build
cd ..
go test ./internal/runtime ./internal/contextengine ./internal/sessions ./internal/transcript ./internal/tasks
go test ./...
go vet ./...
wails3 dev
```

需要真实模型时，在 UI 完成 Provider、Model 和 Agent 设置，再发一条普通消息；仓库测试不要求真实模型凭据。`wails3 dev` 需要 Wails CLI 和平台依赖，不能用 `go test` 代替桌面交互测试。

### 如何定位一次请求

- 在前端搜索 `StartTurn`、`humbert:runtime:event` 和 `refreshMessages`，分别对应发起、实时展示、终态重读。
- 在后端按 `RequestID`、`RunID`、`SessionID` 看 `runtime.Event` 与结构化日志；`SessionID` 定位持久化目录。TaskRun 还要看 `TaskID` 和其 `RequestID`。
- 断点建议依次放在 `ChatService.StartTurn`、`Service.StartTurn`、`Resolver.ResolveTurn`、`Executor.consumeEvents`、`Service.completeTurn`。若工具调用失败，再进入该 Builtin 与 `permission.Engine.Evaluate`。
- 如果界面状态与落盘内容不同，先用 `sessions.Messages` 或消息分页确认 JSONL 的终态，再查 EventBus/Wails/Pinia；如果 Context 不对，检查 `LoadContextSession` 返回的当前分支和 `projectActiveBranch`，不要直接以文件末尾几行猜测模型输入。

开发规则见根目录的 `DEVELOPMENT.md`。新增领域行为优先写在领域包，Wails Service 保持参数转换和错误映射；复杂并发与恢复分支请写中文注释解释为什么这样处理。
