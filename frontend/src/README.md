# Vue 前端结构

[架构目录](../../docs/architecture/README.md) · [Services](../../internal/services/README.md)

`layouts/AppShell.vue` 持有应用初始化、运行事件订阅和窗口布局。`features/workspaces.js` 声明任务、技能、连接器页面，侧栏与页面宿主共用定义；`features/settings.js` 声明设置分组、搜索词、组件及事件转发。组件按需加载，新增普通页面无需增加 AppShell 或 SettingsView 的模板分支。

`components/` 实现页面和展示；`stores/` 保存各领域的共享 UI 状态；`api/` 保留现有 Wails 调用协议。Store 的业务状态不复制到页面注册表。

`stores/runtime.js` 是活动会话运行态的唯一所有者。`runtime/projections.js` 只归一化 DTO 和计算输入签名，不订阅事件、不调用 IPC。模块能力摘要按后端 Manifest 展示，前端不重新推导启用工具。消息事实仍来自后端 Transcript。

更新异步状态时继续使用请求编号，旧响应不能覆盖新选择、审批或运行。测试入口为 `npm test`；生产资源由 `npm run build` 生成。
