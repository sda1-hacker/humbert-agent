# Humbert Agent 前端

Vue 3 + Pinia 提供桌面界面，Vite 构建资源，Wails 3 连接 Go 服务。项目运行环境与完整启动方式见 [项目 README](../README.md)，实现说明见 [项目源码详解](../docs/项目源码详解.md#frontend)。

## 安装与构建

在本目录执行：

```bash
npm ci
npm run build
```

从仓库根目录运行 `wails3 dev` 启动桌面与前端开发服务。`npm run build` 调用 `scripts/ensure-embed.mjs` 保留 `dist/.gitkeep`，支持 Go 资产嵌入。桌面最终构建使用 `wails3 build`；生成资源不手工修改。

## 数据流与扩展位置

- `src/api/` 按领域封装 `Call.ByName`；当前主调用不依赖生成 bindings，服务参数仍须与 Go DTO 核对。
- `src/stores/` 管理共享 UI 状态。Session 历史来自后端，Runtime Store 只维护本次运行和异步展示快照。
- `src/runtime/` 分离 DTO 投影、事件状态转换、流式缓冲、历史同步与 Context 查询，避免页面各自监听同一套运行事件。
- `src/features/` 定义知识库、任务、技能、连接器和设置页面；页面业务仍由各自 API、Store 或 composable 管理。
- `src/components/sidebar/` 中父组件负责导航/操作，Agent 分组与会话条目负责展示；搜索由 `useConversationSearch` 管理。
- `src/components/knowledge/`、`src/api/knowledge.js` 与 `useKnowledgeCitations` 提供知识库、检索预览和聊天来源，不复制后端授权或数据库规则。

目录与组件职责见 [src/README.md](src/README.md)。界面静态文案使用 `src/i18n/` 的四种语言；文件、用户内容、模型 thinking 和工具结果保持原文。

## 验证

生产构建只确认资源能生成。可在仓库根目录执行 `./scripts/verify.sh --checks ../humbert-agent-checks`，加载独立保存的 Store/事件/搜索竞态及组件渲染回归；该目录不随源码自动下载。真实 Wails 中还需检查发送、审批恢复、侧栏菜单、知识库来源预览和窄窗口布局。具体步骤见 [持续验证说明](../docs/持续验证说明.md)。
