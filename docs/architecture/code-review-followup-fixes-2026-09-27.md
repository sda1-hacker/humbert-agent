# 第二轮代码审查：五处问题的修复

日期：2026-09-27。本记录对应审批预算、主动助手延迟动作、任务页面、工作区预览和文档索引的五处问题。关键状态处理已补充中文注释。

## 修改与实现入口

| 模块 | 修改后的行为 | 实现 |
| --- | --- | --- |
| 审批与工具预算 | 使用 Eino 的 `GetInterruptState` 区分新调用与恢复调用。首次调用预留次数；恢复不重复扣次数，但仍检查 Token 上限。无需额外维护调用 ID 缓存 | [executor.go](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/runtime/executor.go:323) |
| 主动助手 | 新事件和延迟事件共用执行前规则检查，使用当前开关、动作、Agent、提示词及免打扰设置。失效动作记录为 ignored；延迟记录不参与冷却，实际执行后同批后续事件受冷却约束 | [manager.go](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/proactive/manager.go:410)、[store.go](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/proactive/store.go:268) |
| 任务页面 | 列表及每个任务的运行记录分别管理请求令牌，旧响应和旧错误不能覆盖新状态。删除、清空后使在途查询失效；后台刷新保留用户当前选择 | [tasks.js](/Users/sda1_hacker/Desktop/humbert/humbert-agent/frontend/src/stores/tasks.js:87) |
| 工作区 | 文件选择与内容刷新分离，刷新不再重新打开旧文件。目录、总览、预览各自校验请求令牌；切换 Agent 时全部失效，A → B → A 也不会接受第一次 A 的响应 | [workspace.js](/Users/sda1_hacker/Desktop/humbert/humbert-agent/frontend/src/stores/workspace.js:222) |
| 文档搜索 | 空文件、超大文件用空投影替换旧正文，并更新索引元数据。文件恢复有效内容后重新提取；处理单个文件时完成清理，不依赖全量扫描结束后的 prune | [document_scan.go](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/searchindex/document_scan.go:66) |

任务与工作区共用 [latestRequest.js](/Users/sda1_hacker/Desktop/humbert/humbert-agent/frontend/src/utils/latestRequest.js:1)。它只保存 Store 实例及资源对应的请求令牌，提供开始请求、判断是否仍有效、使请求失效三种操作。业务状态、错误展示和数据加载仍由各 Store 负责。

## 回归测试

原审查中的五个失败复现均已通过，并转化为项目内的正式测试，不依赖 `/tmp` 中的临时文件。

新增 6 个 Go 顶层测试（部分含表驱动子用例）和 14 个前端测试：

- [approval_budget_test.go](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/runtime/approval_budget_test.go:77)：真实 Eino Graph + Guard + Checkpoint；批准、拒绝、恢复时 Token 耗尽、恢复后的新调用超限、两个工具依次审批。
- [deferred_rules_test.go](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/proactive/deferred_rules_test.go:45)：禁用规则、改为忽略、切换动作、更换 Agent 和提示词、延迟动作的冷却、同批执行期间关闭主动助手。
- [document_scan_test.go](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/searchindex/document_scan_test.go:15)：通过真实文档解析器和 SQLite，验证“有效 DOCX → 清空或超限 → 恢复有效 DOCX”的完整索引生命周期。
- [taskStateFlow.test.js](/Users/sda1_hacker/Desktop/humbert/humbert-agent/frontend/src/utils/taskStateFlow.test.js:28)：IPC 乱序、旧错误、不同任务并行、选择变化、删除与清空、任务状态变更。
- [workspaceStateFlow.test.js](/Users/sda1_hacker/Desktop/humbert/humbert-agent/frontend/src/utils/workspaceStateFlow.test.js:25)：刷新期间切文件、保留文件元数据、重复预览、目录选择、A → B → A、并行目录加载、展开后收起、有效请求的错误传播。

## 验证与边界

已通过后端全量竞态测试、前端全部 38 项测试、前端生产构建和本机桌面生产编译：

```sh
GOCACHE=/tmp/humbert-go-cache go test -race ./...
npm --prefix frontend test
npm --prefix frontend run build
GOCACHE=/tmp/humbert-go-cache go build -tags production -trimpath -buildvcs=false -o /tmp/humbert-agent-followup-fixed ./cmd/desktop
git diff --check
```

主动规则在动作执行前检查，已经开始执行的动作不会因为随后修改规则而被追溯取消。前端令牌阻止旧响应写回状态，不取消已经发出的 Wails 请求。

回归测试不调用真实模型或发送系统通知，也未进行完整桌面交互验收。Go 链接器仍存在原有的 macOS 依赖编译版本与最低部署版本不一致警告。
