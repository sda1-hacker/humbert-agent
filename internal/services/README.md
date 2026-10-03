# Services：Wails 桌面入口

[架构目录](../../docs/architecture/README.md) · [App](../app/README.md) · [Usecases](../usecases/README.md)

`enter.go:All` 是桌面依赖注入与注册点。12 个 Service 分别接收 `dependencies.go` 中声明的 Dependencies，方法使用自己的依赖，不再通过整个 `Application` 查找其他模块。

Service 保留现有名称、方法、请求和返回 DTO，负责超时、桌面对话框、DTO 转换和 Wails 事件桥。Agent/会话删除、Skill 引用检查、MCP 凭据提交与失败清理由应用用例复用。会话和文件搜索资源归 `searchindex.Service` 所有，Wails 入口只查询并返回 `updating` 状态。

`ChatService.StartTurn` 返回初始化收据，后续状态经 EventBus 桥接为 `humbert:runtime:event`。已持久化的 `UserMessageID` 用于失败重试去重；流式 delta 不直接写入历史。

新增模块先实现自己的领域服务，再构造薄 Wails 入口，在桌面装配点调用 `All(core, additional...)`。既有 Service 不需要获取新模块的 Store。修改公开 DTO 后，可针对 `./cmd/desktop` 生成绑定，避免扫描不在当前应用依赖图中的开发模块。
