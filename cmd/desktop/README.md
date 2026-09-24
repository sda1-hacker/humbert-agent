# 桌面入口：启动前数据操作与 Wails 生命周期

[架构手册](../../docs/architecture/README.md) · [App](../../internal/app/README.md)

`main.go` 先在 Core 启动前执行待恢复/待备份计划，避免运行中的 Store 被替换。随后 `app.Bootstrap` 构建 Go Core，`runDesktop` 用 `services.All(core)` 注册 Wails 服务、嵌入前端资源并创建窗口；退出时在限时 Context 内调用 `core.Shutdown`。

```mermaid
flowchart LR
  M[main] --> D[ApplyPendingRestore / Backup]
  D --> B[app.Bootstrap]
  B --> W[runDesktop + Wails]
  W --> S[core.Shutdown]
```

`frontend/dist` 的资源由 `frontend` 包嵌入，`wails3 dev` 在开发时代理 Vite。修改桌面入口要核对备份/恢复发生在所有 Store 打开之前，且失败时应用不会继续在半恢复的数据上运行。普通业务逻辑应进入 `internal` 包，不放进 `main.go`。
