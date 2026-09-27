# 第三轮代码审查修复记录（2026-09-27）

本轮修复上次审查确认的四个问题，补充中文注释与回归测试。没有新增独立调度器、审批状态副本或前端请求管理框架，继续复用现有模块。

## 修改内容

| 模块 | 问题与修复 | 实现入口 |
| --- | --- | --- |
| 计划任务 | 一次性任务入队后被自动暂停，导致本次运行被分派器取消。现在只清空下一次触发时间，保留用户启用状态，允许本次执行及失败重试；用户主动暂停仍会取消排队的自动运行。 | [manager_schedule.go](../../internal/tasks/manager_schedule.go) |
| 审批提醒 | 审批终结后，Inbox 和静音时段延迟提醒仍可能发送。现在按 ApprovalID 清理两处记录，并通过审批模块的真实状态检查执行资格；聊天与任务来源的提醒使用相同路径。 | [manager.go](../../internal/proactive/manager.go)、[store.go](../../internal/proactive/store.go)、[应用依赖注入](../../internal/app/application.go) |
| 工作区监测 | 超过扫描上限后，快照缺项被误报为新增或删除。现在记录扫描完整性，达到上限就停止遍历，仅统计可确认的变化，并在摘要中提示未扫描部分未知。 | [workspace_monitor.go](../../internal/proactive/workspace_monitor.go) |
| Agent 前端状态 | 旧列表请求晚返回，会覆盖已经保存的模型或配置。现在复用 latestRequest，只让最新有效请求更新列表和 loading；保存成功后让在途旧列表失效，删除成功后立即清理本地条目。 | [agents.js](../../frontend/src/stores/agents.js)、[latestRequest.js](../../frontend/src/utils/latestRequest.js) |

一次性任务不需要新状态：`Status` 负责用户启用意图，`NextRunAt` 负责下一次计划触发。审批模块仍是审批生命周期的唯一事实来源；Proactive 仅保存提醒及其处理结果。前端保存失败时不使仍有效的列表请求失效，当前请求的真实错误继续抛给调用方。

## 回归覆盖与验证

新增 11 个 Go 顶层测试（包含表驱动子用例）与 6 个前端测试：

- [once_schedule_test.go](../../internal/tasks/once_schedule_test.go)：一次性执行、队列恢复、后续不重复触发、失败重试及恢复去重、容量等待和用户主动暂停。
- [approval_reminders_test.go](../../internal/proactive/approval_reminders_test.go)：聊天/任务来源、Inbox/延迟记录、完成/取消/过期、终结事件缺失、旧事件晚到、重启后审批不存在、正常提醒只执行一次，以及清理持久化失败时不修改内存。
- [workspace_monitor_test.go](../../internal/proactive/workspace_monitor_test.go)：真实 3000 文件边界、完整与不完整快照切换、准确计数、根目录不存在与取消扫描。
- [agentStateFlow.test.js](../../frontend/src/utils/agentStateFlow.test.js)：模型/配置保存与旧列表交错、新旧请求成功/失败乱序、创建/删除后刷新、loading 归属和真实错误传播。

已通过：

```sh
GOCACHE=/tmp/humbert-go-cache go test -race ./...
cd frontend
npm test
npm run build
cd ..
GOCACHE=/tmp/humbert-go-cache go build -tags production -trimpath -buildvcs=false -o /tmp/humbert-agent-third-fixed ./cmd/desktop
git diff --check
```

前端共 44 项测试通过；前端生产资源构建、嵌入步骤和桌面生产编译通过。验证没有使用真实模型服务，也没有完成完整桌面界面的端到端验收。

## 保留的边界

- 工作区监测仍保留 3000 文件上限；本轮保证不把未扫描到的文件误判为增删，不能保证检测到上限外的变化。
- 审批检查在动作执行前完成；已经通过检查并进入执行器的通知或 Agent 动作不会被追溯撤回。
- 本轮修复不会自动重放此前已被取消的一次性运行，以免重复执行外部操作。
- macOS 链接仍提示部分对象面向 macOS 13/14、当前链接目标为 macOS 11；构建成功不代表已验证旧系统兼容性。
