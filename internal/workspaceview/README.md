# WorkspaceView：桌面工作区只读适配

[总目录](../../docs/architecture/README.md) · [Workspace](../workspace/README.md) · [Services](../services/README.md)

该包把 Agent 选择映射到工作区文件视图。`Service.resolve` 从 `agents.Service` 读取当前 Agent，再由 `workspace.Manager` 解析根目录；`Overview` 提供界面概览；`ListDirectory`/`PreviewFile` 复用 Workspace Manager 的受控浏览 API。它不扫描 Session 来推断文件产物，也不承担 Agent 运行时的写入权限。

```mermaid
flowchart LR
  UI[右侧文件树/预览] --> WS[Wails WorkspaceService]
  WS --> V[workspaceview.Service]
  V --> A[agents.Service]
  V --> W[workspace.Manager]
  W --> FS[文件系统]
```

阅读 `service.go` 的 `resolve` 和三个公开方法即可跟通链路。若当前 Agent 切换后展示旧文件，先检查前端 `stores/workspace.js` 的 AgentID，再检查 `resolve` 是否选中新的工作区。视图的读成功不代表 Agent 工具拥有同等写权限；工具仍走 Sandbox 和 Permission。
