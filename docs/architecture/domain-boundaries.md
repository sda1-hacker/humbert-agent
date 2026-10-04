# 领域事实、权限与生命周期

[总目录](README.md)

## 数据事实

| 内容 | 所属模块与权威位置 | 派生内容 |
| --- | --- | --- |
| 会话消息、thinking、ToolCall/ToolResult、压缩检查点 | Transcript 的 `agents/<id>/sessions/<id>/session.jsonl` | 位置索引、前端消息列表、模型上下文 |
| 会话标题、归档、排序与归属 | Sessions 的 `agents/session-metadata.sqlite` | 侧边栏列表 |
| 上传文件、截图原件 | Session 的 `attachments/` | 图像预览、提取文本；JSONL 只保存引用 |
| 大工具结果原件 | Session 的 `context-artifacts/` | 模型可见预览与分页资源 |
| Task 与 Run 控制状态 | Tasks 的配置/Run JSON | 主动事件、UI 状态；完整对话仍在 Session |
| Provider、Model、Agent、MCP、偏好 | 各自的 Store | 本轮不可变 Snapshot 与 revision |
| 密钥 | Credential / 系统凭据库 | 受控索引；普通配置不保存秘密 |
| 跨会话、工作区文档搜索 | SearchIndex 的 `cache/*.sqlite` | 全部为可重建正文投影，不能替代原件 |

缓存可重建不等于可公开。非 FullAccess 的通用文件/命令能力硬拒绝应用会话、控制面、日志、凭据和整个 `cache/`，包括 SQLite WAL/SHM 与浏览器 Profile。受信 UI 和后台服务仍通过自己的接口访问。

## 授权边界

Scope 的 Request/Session/Agent/Workspace 来自 Runtime，不由模型参数选择。Permission 负责 Allow/Ask/Deny；Sandbox PathGuard 和原生进程隔离负责真正的路径/网络边界，二者不能互相替代。子 Agent 有自己的能力选择，但继承父运行的安全身份。stdio MCP 是用户配置的 Connector，其权限语义见 MCP 文档，不能把它声称为普通工作区沙箱进程。

Managed Workspace 属于 App；Custom Workspace 属于用户。删除 Agent 可以清理 Managed 目录，不能级联物理删除 Custom 目录。旧 Session 的 CWD 不随 Agent 设置被静默改写。

## 执行资源

一个 Session 同时只有一个 Turn/压缩/删除占用。审批中断保留 activeRun 和 checkpoint，恢复同一参数与工具快照。所有终态经过 `finishRun`：先结束必要维护、取消上下文、通知生命周期观察者、释放 reservation，再发布终态；同步订阅者可马上继续。

MCP 显示更新不改变连接身份。连接配置更新让旧连接 retired，新轮使用新配置；主/子运行按父 RequestID 持有旧连接，初始化失败或最终收尾释放。显式断开、停用和删除立即关闭相关连接，包括 retired 连接；旧工具不会自动换到新身份。

任务失败/超时仅在没有工具调用记录时允许自动整体重试。等待审批、调用失败或效果未知都采取保守阻止规则；重启恢复和旧队列启动使用同一判断。手动再次执行属于用户检查后选择，不提供外部副作用的恰好一次保证。

Application 停止生产新工作，收敛 Runtime，再释放连接/Store。构造失败也释放已接管资源。进程重启不会盲目恢复未知工具副作用。
