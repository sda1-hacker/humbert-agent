# 可靠性修复与代码清理

本次修改基于同目录的代码审查报告，优先修复会话、工具事务和运行生命周期，保持 Eino/Wails/Vue 与文件存储架构。

2026-09-16 后续又完成了 Agent 删除状态机、删除/Session 创建并发协调、Custom
Workspace 保留测试、损坏 Session 隔离，以及 UI/文档中的旧工作容器概念清理。当前领域
边界以 `docs/architecture/domain-boundaries.md` 为准。

## 已实现

### 1. 中断工具历史恢复

- 上下文校验同时检查调用和结果，拒绝缺失结果、孤立结果、重复结果、重复调用 ID 和跨消息边界的未完成事务。
- 构造模型上下文时，在缺失结果的位置补充明确的 `unknown` 工具结果。已记录的实际结果保持不变，原始 JSONL 不被重写。
- 恢复标记明确说明操作可能已经发生，需要核实效果，不自动重放工具。它是上下文恢复信息，不能视为真实执行成功或失败。
- 工具已经返回时，结果保存使用独立的 5 秒收尾 Context，避免同时发生的用户取消直接丢弃结果。

### 2. 缺失会话配置恢复

- 发现 config.json 缺失时，完整校验对应 Transcript，再用 Header 中的工作目录和创建时间重建配置，标题为“恢复的会话”。
- 创建与恢复使用同一把会话配置锁，避免并发读取抢先恢复正在创建的会话。
- 不覆盖已有、损坏或符号链接配置；原有消息文件保留。

选择了兼容现有目录的恢复策略，本轮没有切换到暂存目录发布协议。后续实现已将格式损坏
的 Session 隔离：原文件不被覆盖，健康 Session 继续加载，直接访问损坏项会返回
`ErrSessionUnavailable`。目前仍没有面向用户的修复/导出界面。

### 2.1 Agent 删除恢复

- 删除前原子写入 `.deleting.json`，Agent 随即从 Get/List 隐藏。
- Managed Workspace 和 Agent 内部数据分阶段清理；Custom Workspace 永不递归删除。
- 中途失败保留检查点，Bootstrap 会继续未完成删除且不会因此阻塞应用启动。
- Runtime 删除门闩与 Session reservation 协调 Turn、压缩、Session 创建和删除。

### 3. 发送失败与重试

- 用户消息已经保存但 Snapshot 初始化失败时，核心返回消息收据与错误；Wails 通过 startError 字段保留收据，避免普通 error 丢弃其他返回值。
- 前端展示失败并刷新已保存历史，相同内容再次发送时携带 retryUserMessageID。
- 后端只允许复用当前会话最后一条、内容完全相同且尚无后续回复的 UserMessage；拒绝旧轮次和不匹配引用。
- 启动成功后的历史刷新失败不再被当成发送失败，也不会因此恢复重复草稿。

重试收据当前在前端进程内保存，尚不是跨进程、跨网络不确定响应的完整幂等任务协议。界面重载或进程崩溃后，仍需用户核对历史。

### 4. 运行生命周期

- Start 初始化、审批处理、状态读取、手动压缩和删除纳入统一关闭计数与取消边界。
- Worker 在释放生命周期锁之前注册计数，修复 Close 与 Worker 启动的竞态。
- 多次 Close 共享同一个完成信号；前一次等待超时后，后一次不会误报已关闭完成。
- 回答后的上下文/记忆维护有独立 maintaining 状态，前端显示“回答已完成，正在整理记忆”。维护仍按会话串行，用户取消与应用关闭都能停止它。
- 删除会话走 Runtime 的同一占用机制，拒绝删除运行、初始化或手动压缩中的会话。

### 4.1 日常会话体验

- 新增 Active Branch 消息游标分页，前端默认读取最新 80 条并可向前加载；跨页编号稳定，分页边界不会拆开 Assistant ToolCall 与后续 Tool Result。
- 新增按 Session 持久化的文字草稿和按 Session 隔离的进程内附件草稿；文字草稿合并短时间内的连续写入，发送、删除和页面退出时立即刷新。
- 用户消息支持复制、再次填入输入框；Assistant 回复支持复制。
- 第一条用户输入会为默认标题生成短标题；命名失败不回滚已经成功落盘的消息。

分页 API 当前会先在 Wire Entry 上确定窗口，只把本页恢复成 Eino Message。完整尾行不再
重复扫描，Compaction Prepare 复用同一次 Transcript 读取。Transcript 首次访问或文件变化
时严格校验完整 JSONL，随后使用容量受限、可重建的 Document LRU；正常追加只增量推进
Leaf/Active Branch，并同步维护 Message ID/序号到 Active Branch 位置的索引。分页现在只
复制、恢复当前窗口，不再克隆或筛选完整 Document。

### 4.2 前端首屏加载

- Settings、Skills 和 Connectors 改为异步一级视图，不再随聊天首屏同步加载。
- Arco Vue 从整包注册和整包样式改为只注册模板实际使用的组件及其样式；子组件仍由对应插件统一注册。
- 生产构建的主 JS 从约 1.11 MB（gzip 326 KB）降至约 301 KB（gzip 87.5 KB），主 CSS 从约 505 KB 降至约 255 KB；低频页面形成独立 chunk，Vite 不再报告超过 500 KB 的 chunk。

### 4.3 附件与多媒体模型路由

- 图片附件校验真实 MIME 后保存到 Session sidecar，请求时按上下文重放策略水合为 Base64；Base64 不进入 transcript、Memory 或压缩记录。
- UTF-8 文本、源码及 JSON/YAML/XML 等附件提取为普通 text part；PDF/Office、音频和视频在可靠解析链路落地前明确拒绝。
- Agent Profile 不再保存 Vision Model。用户在“设置 → 模型”声明 Capability，并在“设置 → 多媒体”选择全局图片回退模型；保存、更新和删除都会校验引用完整性。
- 前端业务 API 统一使用 Wails `Call.ByName`，源码和生产构建不再依赖提交或手工维护生成 bindings。

### 5. 清理与文档

删除了四个未接入文件，约 1,700 行：

- internal/tools/builtin/goal_tool.go：未注册，Goal 中断与当前审批协议也不一致。
- internal/tools/builtin/todo_tool.go：未注册，没有实际调用方。
- frontend/src/components/settings/AgentSettings.vue：旧界面无导入。
- frontend/src/components/settings/SkillAgentReferences.vue：无导入。

保留当前实际使用的 Agent 表单、Skills、MCP、权限审批和沙盒。同步 README/DEVELOPMENT 的实际数据目录；Wails bindings 继续视为生成物，不在业务修改中手工维护。

## 回归测试

- contextengine：未完成事务、重复调用/结果、部分工具成功后中断、后续用户消息、恢复幂等性、原始历史不变。
- sessions：缺失 config 的重启恢复、保留 Transcript、恢复后改名再重启、损坏 Session 隔离且健康 Session 可用、连续重试不重复追加、拒绝重试已回复消息、分页游标与 Tool 事务边界、首条输入自动命名。
- agents：删除中断后重启续作、并发 Session 创建/Agent 删除无孤儿、Custom Workspace 保留。
- runtime：关闭取消并等待初始化、多次关闭等待、拒绝删除被占用会话、已完成工具结果在取消后仍保存、取消记忆维护并发出正确终态。
- models/runtime：多媒体配置在模型增删改后保持、拒绝无 Vision/已禁用模型、保护正在使用的图片模型，以及 Chat Vision/全局图片回退路由选择。
- sandbox：测试目录与生产路径一样先解析真实路径，修复 macOS `/var` 与 `/private/var` 别名导致的夹具错误，没有放宽沙盒规则。
- tasks：Task/TaskRun 原子持久化、损坏记录隔离、计划时区与历史槽位跳过、并发计划幂等、暂停/归档队列收敛、重启中断恢复，以及模型/工具执行上限。

验证命令：

```sh
go test ./...
go test -race ./internal/runtime ./internal/sessions ./internal/contextengine ./internal/sandbox
go vet ./...
cd frontend
npm run build
```

以上 Go 测试、静态检查和前端构建已执行通过。Wails bindings 只通过
`cmd/desktop/main.go` 入口生成，不作为手工维护的源码。已经删除 npm 不支持且实际不会生效的
`minimum-release-age` 项；未运行真实 Provider/工具外部副作用或完整桌面 GUI 验收。

## 下一批工作

1. 用真实 Provider、MCP 与桌面休眠/唤醒场景验收主动任务，并根据实际使用补系统级通知。
2. 工作区产物预览。
3. 多 Agent 与后台委派。
4. 跨会话长期记忆的编辑、来源、置信度与遗忘策略（最后实施）。
