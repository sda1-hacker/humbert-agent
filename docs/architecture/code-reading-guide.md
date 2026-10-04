# 代码阅读导引

[总目录](README.md)

从一个实际操作沿调用链阅读，优先确认数据写入、Scope 与资源关闭，不必先通读所有 Manager。

| 场景 | 阅读顺序 | 核对点 |
| --- | --- | --- |
| 应用启动/退出 | `cmd/desktop/main.go` → `internal/app/application.go`、`lifecycle.go` | 启动前恢复、构造失败清理、Stop/Runtime 等待/Close |
| 一次聊天 | `services/chatservice.go` → `runtime/service.go` → `resolver.go` → `executor.go` → `sessions/service.go` | 用户消息先落盘；冻结 Scope；完整步骤写入；终态前释放占用 |
| 工具调用 | `app/tools.go` → Factory → `tools/registry.go`、`guarded_tool.go` → 实际 I/O | 模型参数、风险、审批、路径/网络复核、ToolCallID |
| 审批与取消 | `runtime/approvals.go`、`runs.go` → `approval/manager.go` | 同一 checkpoint/参数；暂停期间保留占用；终态唯一 |
| 长会话/压缩 | `contextengine/projection.go`、`middleware.go`、`compactor.go` → Transcript | 检查点与原始尾部；完整工具事务；ExpectedLeafID；硬预算 |
| MCP 配置变化 | `mcp/manager.go` → `einoadapter/backend.go` → Runtime 生命周期观察者 | Name 不失效；Retire 与 Invalidate 区别；RequestID 持有和释放 |
| 任务失败/恢复 | `tasks/manager_schedule.go`、`manager_events.go`、`store.go` | safeToRetry；旧队列校验；终态真实工具计数；不重放 unknown |
| 浏览器网络 | `tools/builtin/browser_tool.go` → `browser_proxy.go` → `publicWebDialer` | Chrome 全进程代理；无 DIRECT/bypass；实际 IP 拨号；关闭 CONNECT/WS |
| UI 消息不同步 | `frontend/src/api` → `stores/runtime.js`、`runtime/projections.js` → Session API | RequestID/SessionID；流事件为投影；终态回读事实 |
| 搜索/备份 | `searchindex` 或 `databackup` 包 README → 对应 Store/Controller | 索引可重建；缓存私密；恢复前验证与失败回退 |

排障用稳定 ID 串联：聊天 `RequestID → RunID → SessionID → EntryID/ToolCallID`，任务 `TaskID → TaskRunID → Runtime RequestID`，连接 `ServerID → Fingerprint → PolicyKey`。日志只保存必要身份/错误；不要打印提示词、凭据或文件正文。

回归测试优先看相关包 `*_test.go`。浏览器实际 Chrome 测试由 `HUMBERT_TEST_BROWSER=1` 启用，使用独立临时 Profile；缺少 Chrome 或权限时需注明未验证，不把编译当作交互成功。
