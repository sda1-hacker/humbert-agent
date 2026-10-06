# Humbert Agent 前端

Vue 3 + Vite 前端，桌面服务由 Wails 3 提供。项目总览与数据恢复说明见仓库根目录 README。

```bash
npm ci
npm run build
```

`npm run build` 会重建 `dist/.gitkeep`，保证 Go 的 `go:embed` 在首次构建前和构建后均能读取该目录。

前端不再维护自动化测试；修改后执行生产构建，并在桌面应用检查相关交互。Go 后端测试保留。

以下是已从当前工作区移除的 23 个旧测试文件的历史清单；无需再保留空文件或恢复测试脚本：

```text
src/utils/agentForm.test.js
src/utils/agentStateFlow.test.js
src/utils/appShellEvents.test.js
src/utils/composerAttachments.test.js
src/utils/defaultBuiltinTools.test.js
src/utils/featureRegistry.test.js
src/utils/i18n.test.js
src/utils/latestRequest.test.js
src/utils/markdown.test.js
src/utils/menuTooltip.test.js
src/utils/permissionModeFlow.test.js
src/utils/runtimeActivityFlow.test.js
src/utils/runtimeErrorFlow.test.js
src/utils/searchPolling.test.js
src/utils/sessionMessageFlow.test.js
src/utils/sessionSelectionFlow.test.js
src/utils/settingsStateFlow.test.js
src/utils/sharedStateFlow.test.js
src/utils/skillCommand.test.js
src/utils/taskStateFlow.test.js
src/utils/toolTrace.test.js
src/utils/workspacePanelSync.test.js
src/utils/workspaceStateFlow.test.js
```
