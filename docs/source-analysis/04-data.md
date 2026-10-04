# 04 Agent、Session 与 Transcript

源码：[agents/types.go](../../internal/agents/types.go)、[agents/service.go](../../internal/agents/service.go)、[agents/store.go](../../internal/agents/store.go)、[sessions/service.go](../../internal/sessions/service.go)、[sessions/store.go](../../internal/sessions/store.go)、[transcript/store.go](../../internal/transcript/store.go)、[codec.go](../../internal/transcript/codec.go)。

## Agent 配置

Agent 字段包括 ID/name/avatar/instruction、ModelID、Utility ModelRoles、SubagentEnabled、EnabledSkills、EnabledMCPTools、EnabledBuiltinTools、Sandbox、WorkspaceMode/Path 和时间。ModelID 是配置引用，不保存 SDK 实例；Skills/MCP 保存显式选择，不复制包正文或 Server 秘密。

Service.Create/Update 规范化文本、头像、模型可用性、Skill/MCP 选择、内置工具列表、工作区和 Sandbox 路径。AgentInfo 动态补模型显示名。专用的 SetModel/SetModelRoles/SetSkills/UpdateSecurity 等入口共同维护同一 Profile，避免设置页各写一份配置。Store 使用每 Agent 目录与原子 JSON，目录身份需要校验。

创建 Agent 失败会回滚已经创建的配置/托管工作区；自定义工作区不是应用拥有的数据，删除 Agent 时不移除它。删除使用 DeletionState/tombstone 记录可继续的进度，启动 RecoverDeletions 可恢复未完成操作。跨域删除入口先暂停该 Agent 的任务、阻止/收敛 Runtime，再处理 Session 元数据和权限；不能直接在页面调用 RemoveAll。

## Session 控制面

Session 包含 ID、AgentID、Title、Archived、CWD、CreatedAt/UpdatedAt。它的标题/归档与消息事实分开：

| 数据 | 存储 |
| --- | --- |
| Agent 配置 | `agents/<agent-id>/config.json` |
| 会话控制面 | `agents/session-metadata.sqlite` 的 sessions 表 |
| 消息和压缩事实 | `agents/<agent-id>/sessions/<session-id>/session.jsonl` |
| 附件原件 | 同一 Session 的 attachments 目录，文件名为生成 ID |
| 派生定位索引 | 同一 Session 的 session.locations.jsonl |
| 大结果 / 子运行审计 | context-artifacts / collaboration Store 对应 sidecar |

SQLite 开 WAL，保存 ID、所属 Agent、标题/归档、工作目录和时间。SessionStore 的启动 rebuildIndex 对照磁盘 Header 恢复缺失元数据、更新索引并记录损坏会话问题；一个坏会话被隔离，不让所有 Agent 无法启动。SQLite 不是消息正文的第二权威来源。

`PrepareUserMessage` 负责新输入和重试复用。RetryUserMessageID 必须与当前历史/提交内容匹配，包括附件，不允许用一个旧 ID替换成新参数后冒充重试。首次有效输入还可自动生成简短会话标题。

## JSONL v3 的消息结构

[transcript/types.go](../../internal/transcript/types.go) 定义 CurrentVersion=3。第一行是 SessionHeader，含 type/version/id/timestamp/cwd/parentSession；后续 Entry 包含稳定 ID、ParentID、类型、时间及对应 payload。

主要 EntryType 是 message、compaction；类型体系还容纳 model_change、thinking_level_change、branch_summary、custom/custom_message、label。声明格式支持一种 Entry，并不代表 UI 已提供创建它的完整操作。

消息 Role 为 user、assistant、toolResult；ContentBlock 可以是 text、image、file、thinking、toolCall。工具参数是 JSON RawMessage，ToolCall ID 与结果 ToolCallID 对应；thinking/文本签名与 Provider 特有信息经 Codec 转换。Assistant 还保存 API/provider/model、实际 responseModel/responseID、Usage、Cost 和 StopReason。

StopReason 包括 stop、length、toolUse、error、aborted、deferred。错误或取消的 partial 内容保持原事实状态，UI 不能因已有正文就把它标成完整成功。

附件块保存 AttachmentID、MIME、SizeBytes、必要提取文本/按需标记；二进制 Base64 不直接进入 JSONL。Codec 是内部 schema.Message 与稳定 wire format 的适配边界，不把供应商 SDK 的所有内存对象原样 JSON 序列化。

## 分支与追加一致性

Store 按 Transcript 文件取得引用计数的互斥锁；不同 Session 不相互串行化。读取构建 EntryByID 与当前 LeafID，沿 ParentID 回溯形成 ActiveBranch，检查循环、缺失父节点、无效 Header/Entry 等损坏。

appendEntry 在锁内验证消息、读取当前叶子、检查 ExpectedLeafID/压缩边界，再编码单行；单行上限 8MiB。使用 O_APPEND、完整写入、Sync、Close，成功后推进缓存与 sidecar；投影更新失败可失效/重建，不回滚已经成为事实的 JSONL。

Compaction 保存 Summary、FirstKeptEntryID、Token 前后数和 CompactionDetails（generation、来源首尾、数量等）。它不删除旧消息；下一轮上下文从 checkpoint 与保留区投影，历史仍可查询。

## 尾部修复和损坏处理

`repairTailLocked` 先看最后字节；已有换行则无需扫描尾修复。没有换行时逐行检查：只有最后一行不完整且前面存在合法数据时才能截断到最后合法偏移；合法 JSON 缺换行则补换行并 Sync。Header 损坏、中间无效 JSON、过大 Entry 返回 CorruptionError，不偷偷删掉中间历史。

“尾部半写可修复”与“任意损坏可以自动恢复”不同。Sessions.Issues 提供隔离原因，业务入口保留错误供用户诊断。

## 长会话读取

[cache.go](../../internal/transcript/cache.go) 的文档缓存最多 32 条/64MiB，检查文件身份、size/mtime；对外返回副本，防止消费者改写缓存。追加可以增量推进，不一致时失效。

[location_index.go](../../internal/transcript/location_index.go) 保存 Entry offset/length 和元数据，检查与 JSONL 的 size/mtime 对应。优先用内存位置缓存，再读 sidecar；无效则扫描重建。大历史的上下文窗口、分页和范围读取可以按字节定位，避免每次全量解码。

[history_index.go](../../internal/transcript/history_index.go) 提供反向访问 ActiveBranch 与指定 Entry 前后范围；Session MessagePage 返回 StartIndex、HasMore、NextBeforeID。sidecar 可丢失/重建，不能作为恢复消息正文的替代。

## 附件事务与水合

[attachments.go](../../internal/sessions/attachments.go) 输入 Base64 后校验名称、内容/MIME和大小：单附件 12MiB、单用户消息总计 24MiB、普通文本提取上限 512KiB，运行期水合总预算 32MiB。图片验证真实格式；PDF/Office 只校验容器并标 document_on_demand；普通文本按允许类型提取。

每个原件生成 UUID 文件名，展示名不直接参与物理路径。先保存附件，再追加用户消息；失败清理本次已创建文件。读取只允许当前 Session 的受控 ID，拒绝路径拼接/符号链接等危险对象。

`HydrateMessages` 在发给 Provider 前把内部附件引用恢复为图片数据或必要文本，遵守回放 mask 和预算；不会把会话私有磁盘路径直接发给模型。浏览器 `SaveToolImage` 也保存原始 PNG 到当前 Session。复制到工作区是独立文件动作，附件生命周期仍属于 Session。
