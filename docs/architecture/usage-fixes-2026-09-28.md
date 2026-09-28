# 桌面使用问题修复（2026-09-28）

本次针对真实反馈处理程序路径、桌面 PATH、审批体验和长任务失败。关键逻辑添加了中文注释。

## 1. 完整程序路径与桌面 PATH

原先 `run_command` 的 Capability Identity 层拒绝路径，执行层也有另一套名称限制；两处还依赖桌面进程继承的 PATH。Finder 启动的应用可能没有 `/opt/homebrew/bin`，于是完整路径被拒绝，改成 `go` 又找不到程序。

现在由 [`internal/commandenv/command.go`](../../internal/commandenv/command.go) 统一解析：

- 支持程序名称、完整路径、相对工作目录的程序路径，返回解析符号链接后的绝对可执行路径。
- 保留有效的绝对 PATH 条目，补充 Homebrew、Go 和常见用户工具目录；不执行 shell 启动脚本，不继承整份环境变量。
- [`capability_identity.go`](../../internal/tools/capability_identity.go) 和 [`run_command.go`](../../internal/tools/builtin/run_command.go) 使用同一解析器；子进程环境使用相同 PATH。Git 工具与 Skill 解释器也复用解析器。
- 程序和 argv 仍分别传递，不把模型提供的字符串直接当作 shell 命令。

目标项目可以不是 Agent 的工作目录。`working_directory` 支持沙箱已授权的绝对目录；标准模式默认可读用户目录。修改外部项目需要将其设为工作区、配置写入目录，或选择完全操作。单次批准命令不会自动扩大目录权限。

## 2. 三种审批模式

入口位于聊天输入区及“设置 → 操作确认”，两个入口共享已保存的模式；保存失败时保留原显示。模式写入 `security.permissions.mode`，重启后保留。

| 模式 | 执行方式 |
| --- | --- |
| `risk` 风险审批（默认） | 内置只读查询、工作区内常规编辑和有原生文件隔离的可识别命令自动执行；删除、未知脚本及其他越界操作等询问。 |
| `full` 完全操作 | 无需逐次确认，新一轮任务采用 `full_access` 文件权限，可处理工作区外项目。 |
| `always` 请求审批 | 每次工具调用都询问，历史 Allow 不能跳过确认。 |

三种模式都保留显式拒绝规则。模式是应用级配置，影响所有 Agent；目录范围在新一轮任务解析时冻结，正在运行的任务不会中途扩大目录权限。网络策略及操作系统本身的权限保持有效。

后续实际使用又发现了只读查询误审批：2026-09-28 10:09 的请求 `06a6d3b1-7ea8-461f-ab20-8c1780fc0be9` 调用 `glob_files`（`pattern=**/*.go`，`path=/Users/sda1_hacker/Desktop/humbert/humbert-agent`），因为 Agent 使用独立工作目录，原风险分类先检查“路径必须在工作区内”，将这个只读请求误判为 Ask。现在内置只读工具先于该路径分类自动执行；实际文件访问仍由 Sandbox + PathGuard 决定。新增真实 Registry/Guard/Eino 文件工具回归，覆盖四个查询工具访问外部已授权目录及拒绝受保护目录。

[`permission/risk.go`](../../internal/permission/risk.go) 负责常规操作分类。例如 `go vet/test/build/list/version`、普通 `rg/ls/find` 可在工作区内自动执行；`find -delete/-exec`、自定义 vet 工具、shell 脚本等需要确认。未知操作默认询问。分类不是任意代码的安全证明，测试代码仍由原生沙箱限制文件副作用。

删除了 `run_command` 自己的删除黑名单和无条件只读设置，避免“用户已批准，执行器仍然不允许正常修改”。受限模式下它遵守真实目录权限；Git 查询与 Skill 脚本继续使用只读执行策略。

后端入口：[`permission/engine.go`](../../internal/permission/engine.go)、[`services/permissionservice.go`](../../internal/services/permissionservice.go)、[`sandbox/manager.go`](../../internal/sandbox/manager.go)。前端入口：[`ApprovalModeSelect.vue`](../../frontend/src/components/chat/ApprovalModeSelect.vue)、[`PermissionSettings.vue`](../../frontend/src/components/settings/PermissionSettings.vue)、[`stores/permissions.js`](../../frontend/src/stores/permissions.js)。

## 3. 截图中失败的实际原因

按请求编号 `1b0a4b3f-6a27-4676-be7a-39238f597e7d` 检查本机 `~/.humbert-agent/logs/humbert.log`，对应错误是：

```text
Eino Agent 执行失败: [NodeRunError] run node[ChatModel] pre processor fail: exceeds max iterations
node path: [node_1, ChatModel]
```

当前依赖 Eino v0.9.19 的 `adk/chatmodel.go` 在未设置 MaxIterations 时使用 20。主 Agent 没有显式设置，长审查任务因而触发上限；子 Agent 原先另有硬编码 12 次限制。

现在主、子 Agent 使用 `runtime.max_iterations`，默认 200，可配置 1–1000。达到上限时明确说明原因、保留已有结果，并提示用户继续或调整配置；不再统称为模型生成失败。没有增加无限自动重试。

源码：[`runtime/resolver.go`](../../internal/runtime/resolver.go)、[`runtime/executor.go`](../../internal/runtime/executor.go)、[`runtime/service.go`](../../internal/runtime/service.go)、[`config.example.yaml`](../../config.example.yaml)。

## 4. 验证范围

- 真实 Eino Runner 连续执行 25 次工具调用后正常结束，显式设置 3 次时准确返回迭代上限错误；模型使用本地测试替身，不产生远程 API 费用。
- macOS 宿主原生沙箱测试：精简 PATH 下找到 Homebrew Go，对工作目录外已授权的临时项目执行 `go vet ./...` 成功；工作区内命令写入成功，越界写入被阻止。
- 三模式优先级、历史授权、显式拒绝、符号链接越界、模式持久化、冻结目录权限及前端保存失败/旧响应回滚的回归覆盖。
- 全项目 `go test -race ./...`、`go vet ./...`，前端测试与生产构建，以及桌面 production 构建。受限测试环境禁止本地监听和嵌套 Seatbelt，相关验证在允许这些操作的宿主环境补跑。

测试没有调用真实模型完成整段桌面聊天；更新后需要重新构建并重启应用，旧进程不会自动采用新代码。
