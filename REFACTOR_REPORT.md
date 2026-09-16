# Humbert Agent Refactor Report

## 当前领域模型

Humbert 现在以 Agent 作为顶层 Aggregate，不再存在独立的工作容器领域或兼容 facade。

- Agent 拥有身份、模型角色、Skills、Tools、Sandbox 与 Workspace 配置。
- Session 只引用 `AgentID`，创建时冻结 Workspace 的实际 `CWD`。
- Runtime 从 `Session + Agent` 解析每轮不可变快照。
- Managed Workspace 随 Agent 生命周期管理；Custom Workspace 始终属于用户。

开发阶段只支持最新 Agent、Session 与 Transcript schema。旧数据应删除后重新构建，不在
生产代码中维护迁移分支。

## Agent 删除

删除流程已改为持久化状态机：

1. 原子写入 `.deleting.json`，冻结 Agent ID 与 Workspace 所有权；
2. Agent 立即从 `Get/List` 隐藏，拒绝新的 Session/Turn；
3. 删除 Managed Workspace（Custom Workspace 不处理）；
4. 删除 Agent 内部目录，包括 Sessions、附件与 Session Memory；
5. 如果中途失败，保留标记，应用重启时继续执行。

Runtime 使用 Agent 级删除门闩和 Session reservation，避免 Turn、压缩、Session 创建与
Agent 删除交错产生孤儿数据。

## Session 启动恢复

Session Store 重建索引时会隔离损坏的单个 Session，而不是让整个应用启动失败：

- 不覆盖损坏的 `config.json`；
- 健康 Session 正常进入列表；
- 诊断通过 `SessionIssue` 保留并写入统一日志；
- 直接访问损坏 Session 返回 `ErrSessionUnavailable`；
- 缺失配置仅在 transcript header 合法时按当前 schema 恢复。

## Chat 与附件

Assistant 消息使用结构化 Vue Markdown 渲染器，支持常用 Markdown、代码复制、安全链接、
表格和图片，且不通过 `v-html` 注入模型文本。

附件保存在每个 Session 的 `attachments/` sidecar。JSONL 只保存元数据和内部引用，调用
Provider 前才恢复为 Eino `UserInputMultiContent`，避免 Base64 污染历史、记忆和压缩记录。

## 验证命令

```bash
go test ./...
go vet ./...

cd frontend
npm run build
```

Wails bindings 是生成产物，不在手工重构范围内；服务接口需要刷新时由 Wails 工具重新生成。
