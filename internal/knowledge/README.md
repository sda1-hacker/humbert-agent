# 知识库业务模块

[使用说明](../../README.md#知识库-rag) · [桌面集成详解](../../docs/项目源码详解.md#knowledge) · [RAG 底座](../rag/README.md)

`Manager.Module()` 通过现有 `CapabilityProvider` 向 Runtime 注册知识库工具；Wails 的 KnowledgeService 独立装配。模块拥有自己的设置、任务与连接，不向 Runtime 增加 RAG 业务分支。

| 文件 | 职责 |
| --- | --- |
| `manager.go` | 模块身份、构造与共享状态所有权 |
| `settings.go` | 设置、知识库和 Agent 绑定 |
| `lifecycle.go`、`engine.go` | 操作占用、冻结连接、延迟初始化和关闭 |
| `jobs.go` | 上传、排队、并发上限、重试、取消与导入状态 |
| `documents.go` | 文档分页、预览、删除和原件下载 |
| `capability.go` | Eino 工具、按轮绑定和引用投影 |

任务继续调用 `rag.Service`；版本预留、发布与失败状态由 RAG 层负责。停止新工作、等待在途运行和关闭连接仍沿应用的三阶段生命周期执行。

连接初始化先在锁内冻结配置，再在锁外连接/迁移，最后核对版本并安装连接。并发请求等待同一个连接尝试；单个等待者可以取消。发起连接的请求取消会结束该次共享尝试，等待者收到失败后可重试。普通连接不能代替显式 Initialize 的迁移。初始化期间 State/绑定读取可以继续，SaveSettings 明确拒绝修改；绑定版本变化会使正在建立的连接失效。

同步检索、文档操作、异步导入和连接建立均登记关闭等待。Stop 取消并等待这些操作，Close 再释放连接；连接池关闭在 Manager 锁外执行。

## 数据与业务流程

`knowledge/state.json` 保存非敏感参数、凭据引用、知识库/Agent 绑定和任务摘要；秘密进入系统凭据库。`sources/` 保存原件，`images/` 保存提取图片和识别缓存，`parse-cache/` 保存转换结果。文档正式状态、版本和分块由 PostgreSQL 管理，任务摘要不能覆盖数据库事实。启动无需连接数据库；显式 Initialize 负责建库、扩展与迁移，普通操作只检查已有 schema。

上传路径来自桌面原生文件选择器。先保存原件，再进入后台队列；最多同时导入两篇、单次选择和待处理任务最多 16 个。内容 hash 与格式决定文档身份，已发布文档重复上传会跳过，修改切分参数后可明确重新索引。重启时未完成任务标记中断，不自动重放模型调用。取消、重试和删除仍由 RAG 的 attempt/revision 规则保护旧发布版本。

文档每页 50 条，预览读取已发布分块；下载读取托管原件。删除文档不等于立即回收共享原件/图片磁盘空间。应用本地备份包含知识库目录，PostgreSQL 需要另行备份。

## Agent 能力与前端

`knowledge_search` 提供 list/search/read，每轮最多调用六次。真实运行捕获绑定与连接，查询前后核对授权版本；来源 ID 绑定知识库、文档、分块及发布版本，不能作为任意数据库读取参数。该工具复用 Runtime 的 Permission Guard，不在 Manager 内启动第二个回答 Agent。

来源快照随 ToolResult 持久化，实时 `knowledge.sources` 只用于界面投影；状态变化发布 `knowledge.changed`。前端通过 `api/knowledge.js` 调用 KnowledgeService，知识库页面本地状态配合请求序号处理切换竞态，没有额外的 Knowledge Pinia Store。聊天来源由 `useKnowledgeCitations` 合并当前事件与历史工具结果，点击 `humbert-source://` 链接后重新校验并读取已发布证据。引用失效时显示错误并可保留历史快照；原件下载在知识库页面完成。
