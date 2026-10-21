# 知识库业务模块

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
