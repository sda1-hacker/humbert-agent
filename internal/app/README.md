# App：依赖装配与生命周期

[架构目录](../../docs/architecture/README.md) · [Services](../services/README.md)

`application.go` 创建已有领域服务与应用用例，成功后返回 `Application`。`tools.go` 注册内置工厂；浏览器视觉模型选择已放到 `usecases.VisionInspector`。`runtime_event_reporter.go` 将运行事件发布到 EventBus。

`modules.go` 提供可选的静态模块装配：`Bootstrap(ctx, WithModules(installer))`。模块只获得通用平台依赖，继续拥有自己的数据、连接和 Agent 绑定；能力交给 Runtime，桌面服务由桌面装配点独立注册。最小示例见 [textstats.go](../../examples/modules/textstats.go)。

`lifecycle.go` 保存每个已构造资源的清理方法。启动失败与正常退出共用清单，资源只关闭一次，一个关闭失败不会跳过后续清理：

1. 停止模块后台工作、Proactive 和 Tasks，阻止继续派发。
2. 取消并等待 Runtime 的初始化、执行和审批等待者。
3. 逆构造顺序关闭模块连接、事件总线、工具工厂、搜索索引、会话数据库、MCP、工作区和日志。

新模块有后台工作时同时提供 `Start/Stop`，即使 `Start` 部分失败也会调用 `Stop`。构造器返回错误时，尚未交给宿主的资源由构造器清理。清理回调需要遵守传入 Context。

Application getter 只供装配点使用；桌面 Service 构造函数接收明确的 Dependencies。跨模块业务规则放在 `usecases`，这里不增加模块业务分支。
