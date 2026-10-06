# Vue 前端结构

[架构目录](../../docs/项目源码详解.md) · [Services](../../internal/services/README.md)

`layouts/AppShell.vue` 持有应用初始化、运行事件订阅和窗口布局。`features/workspaces.js` 声明任务、技能、连接器页面，侧栏与页面宿主共用定义；`features/settings.js` 声明设置分组、搜索词、组件及事件转发。组件按需加载，新增普通页面无需增加 AppShell 或 SettingsView 的模板分支。

`components/` 实现页面和展示；`stores/` 保存各领域的共享 UI 状态；`api/` 保留现有 Wails 调用协议。Store 的业务状态不复制到页面注册表。

`stores/runtime.js` 是活动会话运行态的唯一所有者。`runtime/projections.js` 只归一化 DTO 和计算输入签名，不订阅事件、不调用 IPC。模块能力摘要按后端 Manifest 展示，前端不重新推导启用工具。消息事实仍来自后端 Transcript。

`components/sidebar/AgentModal.vue` 负责弹窗交互；`utils/agentForm.js` 提供新建/编辑共用的独立表单和提交快照。可编辑嵌套对象与数组不直接引用 Store，取消编辑不会改变真实配置。能力目录迟到时只补齐目录、安全状态及尚未配置的默认工具，不重置用户已输入的名称和指令；显式空工具选择也不会被重新启用。

`components/chat/ComposerBar.vue` 负责输入、Skill 选择、模型切换和发送控制；`ComposerContextMenu.vue` 独立负责上下文用量、能力摘要及手动压缩，直接读取 Runtime Store，避免父组件传递大量状态与回调。它监听当前会话和 Agent 模型变化，统一发起上下文刷新。执行中的能力摘要优先使用该 Turn 冻结的 Manifest。

`utils/composerAttachments.js` 只负责文件预检和整批读取，不拥有会话状态。文字草稿由 Session Store 保存，附件草稿由 Composer 按 Session ID 保存。异步读取冻结目标会话，合并前重新校验数量和总大小；发送清空时增加版本，使迟到读取失效。启动失败把文字和附件恢复到原会话，同时保留等待期间新写的草稿。前端限制只是即时反馈，最终校验仍由后端执行。

`components/workspace/ContextPanel.vue` 在面板可见时每两秒同步已展开目录及选中文件的父目录，回到窗口时立即同步；窗口隐藏或面板卸载后停止定时检查。`stores/workspace.js` 根据最新目录结果清理已删除节点、子树缓存和预览，使迟到请求失效；截断目录的缺项不作为删除证据。后台刷新跳过总览扫描，仅在文件大小或修改时间变化时重读已有预览。读取失败会复查父目录：确认删除则清理选择，无法确认时仍报告原错误。

更新异步状态时继续使用请求编号，旧响应不能覆盖新选择、审批或运行。前端不维护自动化测试；生产资源由 `npm run build` 生成，交互、状态和四种语言在桌面应用中核对。
