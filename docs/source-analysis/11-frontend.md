# 11 桌面服务与前端实现

## Wails 边界和依赖

[services/enter.go](../../internal/services/enter.go) 的 `All` 显式装配 12 个 Service。每个 Service 接收窄 Dependencies，见 [dependencies.go](../../internal/services/dependencies.go)，再转换 DTO、添加调用超时和界面错误上下文。领域校验与执行归领域服务，Wails 层不另写一套调度或消息存储。

| Service | 实现职责 | 前端 API |
| --- | --- | --- |
| AgentService | Agent 列表/配置、模型角色、工具目录、沙箱设置/诊断、原生目录选择 | [agents.js](../../frontend/src/api/agents.js) |
| AppService | Core 状态、安排加密备份/恢复、待执行计划状态和取消 | [app.js](../../frontend/src/api/app.js) |
| ChatService | StartTurn、取消、审批决定、上下文状态/概览/压缩，桥接 Runtime 事件 | [chat.js](../../frontend/src/api/chat.js) |
| MCPService | Server CRUD、配置导入导出、连接测试/目录发现、工具风险和 Agent 选择 | [mcp.js](../../frontend/src/api/mcp.js) |
| ModelService | 供应商/模型 CRUD、多媒体配置、真实调用测试和诊断 | [models.js](../../frontend/src/api/models.js) |
| PermissionService | 模式、设置、持久/会话规则及清理 | [permissions.js](../../frontend/src/api/permissions.js) |
| PreferenceService | 用户名称/头像/语言、个人记忆及来源消息校验 | [preferences.js](../../frontend/src/api/preferences.js) |
| ProactiveService | 设置、状态、处理记录、近期通知、手动巡检及事件桥 | [proactive.js](../../frontend/src/api/proactive.js) |
| SessionService | 会话 CRUD、归档、消息分页/窗口、全文搜索、附件读取 | [sessions.js](../../frontend/src/api/sessions.js) |
| SkillService | 包目录/详情/资源、来源发现、安装/更新/重装、别名和 Agent 启停 | [skills.js](../../frontend/src/api/skills.js) |
| TaskService | Task/Run 列表、配置/归档/删除、立即运行/取消及事件桥 | [tasks.js](../../frontend/src/api/tasks.js) |
| WorkspaceService | 工作区概览/目录/预览和文档搜索 | [workspace.js](../../frontend/src/api/workspace.js) |

完整当前公开方法、参数与返回签名见 [服务接口索引](service-api.md)，无需依靠生成 bindings 或旧文档推测接口。

API 文件通过 `Call.ByName` 调用包全名和方法名，组件不直接依赖生成 bindings。这样集中桥接位置、便于测试 mock；代价是普通 JavaScript 构建不能静态检查 Go 方法名和 DTO 字段。接口索引还记录哪些方法当前有 API 调用，存在 Service 方法不代表已在菜单中开放。

[chatservice.go](../../internal/services/chatservice.go) 特别处理 StartTurn receipt：用户消息已保存但初始化失败时，仍向前端返回带 UserMessageID/StartError 的结果。若只抛一个 IPC 异常，前端会误以为消息完全没有发送，用户可能再次提交。

## 事件桥接和生命周期

Chat/Task/Proactive Service 在 startup 订阅 Core EventBus，将类型化 payload 转为 `humbert:runtime:event`、任务、主动助手和通知 Wails 事件；shutdown 解除订阅。事件是实时展示通道，历史加载仍通过 Session API。

[AppShell.vue](../../frontend/src/layouts/AppShell.vue) 集中启动 runtime/tasks/proactive/workspace Store 的事件监听。启动过程中某个 Store 失败时，对已经启动的 Store 做清理；卸载时解除所有监听和窗口 resize。避免组件反复挂载导致一个事件被处理多次。

后端 Notification 经 Proactive Store 更新序列号，AppShell 显示 Arco 提醒和任务跳转。在 document.hidden、浏览器 Notification 存在且 permission=granted 时，前端尝试 `new Notification`，失败保留应用内提醒。它依赖运行环境权限，不能承诺每个平台都已实现原生系统通知。

## 前端入口与功能注册

[main.js](../../frontend/src/main.js) 初始化 Vue、Pinia、Arco、语言接口、全局错误处理和样式；[App.vue](../../frontend/src/App.vue) 接入语言对应的组件 locale 与 AppShell。[assets/main.css](../../frontend/src/assets/main.css) 定义界面共用样式和主题变量。

[scripts/localize-vue.mjs](../../frontend/scripts/localize-vue.mjs) 是 Vite 编译前插件：用 Vue SFC/Template AST 找到中文静态文本和 title/placeholder/aria-label 等静态属性，按源码 offset 从后向前替换为 `$t` 表达式；跳过 pre/code/script 和 v-pre。它不扫描运行时 DOM，也不翻译用户消息和模型结果。[ensure-embed.mjs](../../frontend/scripts/ensure-embed.mjs) 在 Vite 清空/重建 dist 后补回 `.gitkeep`，避免构建删除仓库跟踪的占位文件。

[features/registry.js](../../frontend/src/features/registry.js) 校验稳定 key、加载函数和功能元数据；[workspaces.js](../../frontend/src/features/workspaces.js) 注册任务、技能、连接器工作区；[settings.js](../../frontend/src/features/settings.js) 注册语言、个人资料、备份、归档会话、模型、供应商、多媒体、技能包、MCP、安全及操作确认等设置页。页面按需 import，事件转发也由注册元数据指定。

`ProactiveSettings.vue` 文件和后台接口存在，但当前 settings registry 没有将它加入普通设置菜单。不能仅凭组件文件就把该设置入口写成已开放功能。[i18n/index.js](../../frontend/src/i18n/index.js) 和 translations 系列负责界面语言、动态文本与默认回答语言相关展示；翻译文件有条目也不代表对应功能已连通。

## Store 的状态归属

| Store | 持有内容与关键边界 |
| --- | --- |
| agents | Agent 目录、选中 Agent 配置、局部更新与删除后的目录同步 |
| sessions | 会话列表、选中会话、消息页/定位窗口、按 Session 的草稿和搜索结果 |
| runtime | 每 Session 活动 request、文本/思考增量、工具活动、审批、终态错误和上下文概览 |
| models | 供应商/模型/多媒体设置及配置保存和诊断结果 |
| skills / mcp | 包/Server 目录、按 Agent 的能力选择及修改后的重新加载 |
| tasks | 任务/运行、选择、事件更新和删除返回的会话清理 |
| proactive | 设置/状态/记录/通知序列及相关事件 |
| workspace | 当前 Agent 目录树、展开路径、预览、revision 和刷新失效 |
| permissions / preferences | 审批模式/规则、用户资料/个人记忆 |
| layout / contextPanel | 面板宽度、当前工作区/设置、上下文面板展示状态 |

实现均在 [stores](../../frontend/src/stores)。界面缓存不负责写 Transcript；LocalStorage 中的草稿也不是已发送用户消息。选中 Agent、选中 Session、正在运行的 request 必须分别判断。

[utils/latestRequest.js](../../frontend/src/utils/latestRequest.js) 用 WeakMap 按 owner/key 保存 Symbol token。owner 先通过 Vue `toRaw` 取得原始对象，使 Pinia 开发工具为不同 action 创建的代理仍共享同一组请求；普通对象保持原身份。一次加载返回前检查是否仍是最新请求；切换资源主动 invalidate，使较慢旧响应不能覆盖新选择。它阻止旧响应提交状态，并不自动取消后端正在进行的工作。代理身份与资源隔离的回归测试见 [latestRequest.test.js](../../frontend/src/utils/latestRequest.test.js)。

## 流式消息：缓冲、对齐和收尾

[stores/runtime.js](../../frontend/src/stores/runtime.js) 按 Session 保存活动执行，按 request ID 判断事件是否还属于当前运行。StartTurn 返回与事件抵达的先后不固定：terminalRequests 防止早已结束的事件又被迟到 receipt 重新标成运行中。

文本和 reasoning delta 先进入 Session 缓冲，再通过 requestAnimationFrame 或定时 fallback 合并更新 Pinia，减少每个 token 都触发组件渲染。工具开始/完成、审批和 manifest 更新通过独立活动结构处理，不靠分析自然语言猜测。

`finalise` 先 flush 增量、记录终态和错误并清除匹配审批，再请求持久历史与上下文用量。最后只清理仍属于该 request 的临时状态；若新一轮已经启动，旧轮收尾不能清掉它。历史刷新失败与实际模型执行失败分别处理，保留可见错误信息。

[runtime/projections.js](../../frontend/src/runtime/projections.js) 对 manifest、budget、活动状态和工具字段做展示边界归一化。前端展示后端冻结的 exposedToolNames/skills/MCP/sandbox，不重新推导“这个 Agent 应该具有什么权限”。

## 会话分页、定位和草稿

[stores/sessions.js](../../frontend/src/stores/sessions.js) 管理列表和分页请求、Entry ID 去重及选中资源的竞态判断。历史页使用 beforeEntryID，全文搜索跳转使用 MessageWindow；窗口模式与普通向前分页区分，以免把不连续片段误认为完整历史。

草稿按 Session 存入 `humbert.session-drafts.v1`，延迟合并写 LocalStorage，并在相应生命周期 flush。已发送消息来自服务 receipt/重新读取，不能把发送按钮点击前的草稿直接当持久成功。删除/归档操作同步列表、选中状态和草稿，后台任务关联会话由返回结果同步清理。

## 聊天组件与工具投影

[ChatView.vue](../../frontend/src/components/chat/ChatView.vue) 仅组合 EmptyState、MessageList 和 ComposerBar，当前 82 行。复杂交互主要位于 [MessageList.vue](../../frontend/src/components/chat/MessageList.vue)、[ComposerBar.vue](../../frontend/src/components/chat/ComposerBar.vue)，不能因为聊天功能复杂就误判 ChatView 是巨大单体。

MessageList 管理滚动、历史加载与定位；MessageItem/UserMessageContent 展示用户内容和附件；AssistantTurn/LiveAssistantTurn 区分历史与实时助手；ActivityTimeline 展示思考、工具、审批和结果顺序。[utils/toolTrace.js](../../frontend/src/utils/toolTrace.js) 从历史消息/tool call/result 重建展示，`toolProtocol` 与 `toolEffects` 为结构化结果、文件/任务等可跳转对象提供转换。

ApprovalCard 根据后端 Request 展示参数、风险和原因，将 allow/deny 及授权范围交回 ChatService。前端不会把可编辑任意参数重新提交为另一条工具调用，恢复源仍是后端 checkpoint。

ComposerBar 处理输入、附件、模型选择、发送/取消、上下文用量和压缩、工具/安全相关交互。SkillComposerInput/SkillReference 和 [skillCommand.js](../../frontend/src/utils/skillCommand.js) 处理 `/skill` 引用、名称/别名和输入编辑；最终是否加载技能由 Runtime 当前快照解析，不能只靠前端标签。

## Markdown 与附件

[utils/markdown.js](../../frontend/src/utils/markdown.js) 是项目自有的小型 parser，产出块和 inline token，支持标题、段落、围栏代码、引用、列表、表格、分隔线及常见行内格式；不是完整 CommonMark 实现。[MarkdownRenderer.vue](../../frontend/src/components/chat/MarkdownRenderer.vue) 与 [MarkdownInline.vue](../../frontend/src/components/chat/MarkdownInline.vue) 用 Vue 模板渲染，不把模型原文作为任意 HTML 注入。

URL allowlist 接受 HTTP/HTTPS、mailto 和限定类型的 base64 图片。内嵌 data 图片可以显示；远程图片显示“打开远程图片”按钮，用户点击后才打开。普通 HTTP 链接通过 Wails Browser.OpenURL；HTML 标签按文本处理，javascript/file 等不符合 allowlist 的链接不会成为可执行链接。

[MessageAttachments.vue](../../frontend/src/components/chat/MessageAttachments.vue) 通过 Session API 按 ID 读取附件。图片保存为 data URL，共享同一附件的加载 Promise；下载将 base64 转成 Blob，建立短暂 object URL，点击下载后 revoke。附件不是把任意本地路径直接交给 WebView 读取。

## 工作区文件同步与失效处理

[ContextPanel.vue](../../frontend/src/components/workspace/ContextPanel.vue) 只在聊天右侧面板挂载时运行同步循环。每次后台刷新完成后等待两秒再发起下一次，避免慢请求持续叠加；窗口隐藏时停止定时检查，收到 `focus` 或恢复可见时立即尝试刷新。卸载会移除监听和计时器，在途任务结束后不会重建循环。选中 Agent 与 workspace Store 的 Agent 尚未一致时跳过同步。聊天终态和主动助手事件触发的短延迟刷新仍保留。

[stores/workspace.js](../../frontend/src/stores/workspace.js) 的 `refresh({ background: true })` 重读已展开目录和当前选择的父目录，不遍历整个工作区统计总览。目录返回后，`reconcileDirectory` 核对每个缓存、展开及加载中的路径是否仍有对应直接子项；确认缺失或类型变化时，清掉失效子树及目录请求 token。选中项失效时同时清掉选中路径、预览和 loading，并使旧预览响应失效。目录列表被截断时，未列出的项目可能仍存在，不能据此当成删除。用户在请求期间换了选择时，旧结果不会清空新选择。

刷新先完成目录核对，再决定是否读取预览，避免重复读取已经删除的文件。后台只在没有预览、文件大小或修改时间变化时读取；手动/事件刷新仍重读预览。如果用户在同步前点击已删除的旧节点，预览失败会复查父目录：目录确认删除后清理选择，权限不足等无法确认删除的情况仍保留原错误。目录读取失败也沿父目录核对，因此删除整个展开目录时能够清掉子树。折叠后重新展开强制读取最新内容，不沿用旧缓存。持续的同一后台错误只提示一次，成功恢复后再次失败仍会提示。

这是一套可见面板的按需同步，没有额外文件监听服务。通常在下一次两秒间隔检查及目录读取完成后反映外部变化；相同大小和修改时间的内容替换不会仅凭后台元数据检查识别。回归测试见 [workspaceStateFlow.test.js](../../frontend/src/utils/workspaceStateFlow.test.js) 和 [workspacePanelSync.test.js](../../frontend/src/utils/workspacePanelSync.test.js)，覆盖真实临时文件删除、失效子树、迟到响应、截断目录、内容变更及同步生命周期。

## 其他界面与构建

sidebar 管理会话列表、Agent 配置和重命名；workspace 组件提供文件树、预览及 ContextPanel；tasks 展示计划和 Run；skills/mcp 工作区按 Agent 开关能力。settings 负责应用目录维护及配置；ui 组件提供对话框、图片预览和通用交互。各文件定位见源码索引，不把通用对话框另解释成独立业务领域。

[frontend/assets.go](../../frontend/assets.go) 嵌入构建结果；[vite.config.js](../../frontend/vite.config.js)、[package.json](../../frontend/package.json) 配置 Vite/Vue 构建、Node 测试和嵌入目录准备。当前测试包括纯工具函数和 mock Wails/Store 的状态流程；Vite build 能验证模块编译，不能替代真实桌面 IPC、原生对话框和多平台 WebView 验证。

[examples](../../examples) 中 tools、AgenticMessage、subAgent、rod 和 modules 展示不同 SDK/模块用法。`examples/2-AgenticMessage` 使用的适配方式不代表生产 Runtime 当前也使用该适配；rod 示例也不等于正式 browser 工具实现。示例与桌面应用共用依赖，执行入口和装配仍是分开的。
