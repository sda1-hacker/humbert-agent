# Services：Wails 桌面入口

[架构目录](../../docs/项目源码详解.md) · [App](../app/README.md) · [Usecases](../usecases/README.md)

`enter.go:All` 是桌面依赖注入与注册点。12 个 Service 分别接收 `dependencies.go` 中声明的 Dependencies，方法使用自己的依赖，不再通过整个 `Application` 查找其他模块。

Service 保留现有名称、方法、请求和返回 DTO，负责超时、桌面对话框、DTO 转换和 Wails 事件桥。Agent/会话删除、Skill 引用检查、MCP 凭据提交与失败清理由应用用例复用。会话和文件搜索资源归 `searchindex.Service` 所有，Wails 入口只查询并返回 `updating` 状态。

`WorkspaceDependencies.WorkspaceView` 现在指向 `usecases.WorkspaceQuery`，不再依赖单独的 workspaceview 包。WorkspaceService 的 Overview/ListDirectory/PreviewFile/SearchDocuments 名称、超时和 DTO 字段保持原样；查询仍按 AgentID 读取当前配置，前端不能指定任意绝对根目录。

`AgentService.ListAgents` 在一次请求中只生成一份内置工具目录和 Sandbox 状态，再投影各 Agent 的专属配置与工作区路径。目录不跨请求缓存，下一次读取能看到新的全局策略；各 DTO 的可选择工具切片独立，修改某一项不会污染其他 Agent。单项查询和保存返回仍复用同一 DTO 投影。工具名称存在性校验不维护多余的去重集合，实际规范化与去重由 Agent 领域负责。

`SkillService.skillMutationTimeout` 统一远程单包安装、批量安装和更新的等待规则，按配置的下载超时增加必要的收尾时间，不在多个公开方法中重复计算。网络/Git/ZIP 的验证和清理由 Skill Manager 拥有，Wails 服务不增加另一套下载实现。

`ChatService.StartTurn` 返回初始化收据，后续状态经 EventBus 桥接为 `humbert:runtime:event`。已持久化的 `UserMessageID` 用于失败重试去重；流式 delta 不直接写入历史。

新增模块先实现自己的领域服务，再构造薄 Wails 入口，在桌面装配点调用 `All(core, additional...)`。既有 Service 不需要获取新模块的 Store。修改公开 DTO 后，可针对 `./cmd/desktop` 生成绑定，避免扫描不在当前应用依赖图中的开发模块。

`AgentService.RunSandboxDiagnostics` 保留公开名称和超时，调用 `sandbox.Manager.Diagnose`；路径/进程探针和汇总不再留在桌面服务。结果 DTO 使用领域类型别名，JSON 的 summary/checks/key/label/status/detail 保持原样。原生目录选择仍在桌面层，Agent/Sandbox 允许新建目录，Skill 只选择既有目录，统一复用 `selectDirectory`。

`SkillMetadataDTO` 定义列表/详情的共同元数据，并匿名嵌入 `SkillDTO`、`SkillDetailDTO`，JSON 仍是原有顶层字段。`projectSkillMetadata` 唯一转换公共字段，复制 Metadata、诊断和解释器信息；更新时间共用 UTC RFC3339Nano 格式化。列表额外包含有效性/引用，详情额外包含文件树，正文仍按需查询。

Skills State 只投影并排序一次 Agent 列表；`buildSkillUsageMap` 沿同一排序建立引用桶，按 Agent 去除重复 Skill 名称。UsedByAgents 保持最小身份信息，EnabledSkills 留空；各列表的可变集合仍独立。后端测试验证扁平 JSON 字段、null/空数组、UTC 时间、引用顺序与复制隔离，无需新增前端测试。
