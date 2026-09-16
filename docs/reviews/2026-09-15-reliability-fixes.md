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

### 5. 清理与文档

删除了四个未接入文件，约 1,700 行：

- internal/tools/builtin/goal_tool.go：未注册，Goal 中断与当前审批协议也不一致。
- internal/tools/builtin/todo_tool.go：未注册，没有实际调用方。
- frontend/src/components/settings/AgentSettings.vue：旧界面无导入。
- frontend/src/components/settings/SkillAgentReferences.vue：无导入。

保留当前实际使用的 Agent 表单、Skills、MCP、权限审批和沙盒。同步 README/DEVELOPMENT 的实际数据目录，更新 Wails 绑定；绑定生成使用 string 时间字段，与现有前端数据约定一致。

## 回归测试

- contextengine：未完成事务、重复调用/结果、部分工具成功后中断、后续用户消息、恢复幂等性、原始历史不变。
- sessions：缺失 config 的重启恢复、保留 Transcript、恢复后改名再重启、损坏 Session 隔离且健康 Session 可用、连续重试不重复追加、拒绝重试已回复消息。
- agents：删除中断后重启续作、并发 Session 创建/Agent 删除无孤儿、Custom Workspace 保留。
- runtime：关闭取消并等待初始化、多次关闭等待、拒绝删除被占用会话、已完成工具结果在取消后仍保存、取消记忆维护并发出正确终态。
- sandbox：测试目录与生产路径一样先解析真实路径，修复 macOS `/var` 与 `/private/var` 别名导致的夹具错误，没有放宽沙盒规则。

验证命令：

```sh
go test ./...
go test -race ./internal/runtime ./internal/sessions ./internal/contextengine ./internal/sandbox
go vet ./...
cd frontend
npm run build
```

以上 Go 测试、静态检查和前端构建已执行通过。Wails bindings 是生成产物，本轮未手工修改
或重新生成。前端仍有约 1.11 MB 主 JS 的大包提示，npm 仍提示现有
minimum-release-age 配置不受支持。未运行真实 Provider/工具外部副作用或完整桌面 GUI 验收。

## 下一批工作

1. JSONL 增量读取/可重建索引与历史分页，解决长会话扫描成本。
2. 损坏会话修复/导出界面、持久化运行状态和更完整的重试协议。
3. 工作区产物预览。
4. 跨会话个人记忆的编辑、来源与遗忘。
5. 再按真实使用频率缩减搜索来源、Skills 管理和大型设置页；自主任务、浏览器控制、多 Agent 协作另行规划。
