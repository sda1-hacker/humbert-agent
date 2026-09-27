# 代码复核：剩余问题清单（2026-09-27）

> 修复状态：本清单中的 8 类问题已处理，代码入口与回归结果见[第五轮修复说明](code-review-fifth-fixes-2026-09-27.md)。下文保留发现问题时的证据。

本轮只检查和复现，未修改业务代码。检查对象是当前工作区，包含此前尚未提交的重构和修复，不以 Git HEAD 为准。此前已经修复的问题没有重复计入。

按修复方向合并为 8 项：2 项 P1、4 项 P2、2 项 P3。P1 建议交付前优先处理；P2 是有条件触发的功能缺陷；P3 是影响较小的恢复或界面问题。这不是“项目已不存在其他问题”的保证。

## 1. P1：Git 只读工具没有落实只读执行约束

代码位置：

- [Git 工具声明和执行](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/tools/builtin/git_tools.go:51)
- [git_diff 参数](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/tools/builtin/git_tools.go:93)
- [Runner 的只读约束入口](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/sandbox/runner.go:140)

`git_status`、`git_diff`、`git_log` 都声明为 `RiskRead`，共用的 `run()` 却没有设置 `ProcessSpec.ReadOnlyWorkspace`。常规工作区策略允许写入，Runner 不会仅凭工具名称自动将其收紧为只读。

`git_diff` 虽然传入了 `--no-ext-diff`，但没有关闭另一条外部程序入口 `textconv`。当 Git 配置和文件属性启用了 textconv 时，查看差异会启动配置中的转换程序；该程序能够产生工作区写入副作用。只读权限默认放行时，这与权限界面表达的能力不符。

复现：在临时 Git 仓库配置一个转换脚本，脚本只写入测试标记文件，再输出文件内容。调用真实 `NewGitDiffFactory` 构造的工具，观察到 `Risk=read` 的调用创建了标记文件。

验证边界：执行复现使用 `NativeOff`；`NativePreferred` 另行尝试时，当前宿主拒绝嵌套 Seatbelt，测试明确跳过。因此不能声称已经完成原生沙箱端到端复现。默认工作区具有写权限以及只读标志缺失，是静态代码证据。

建议：复用已有只读进程策略，关闭 textconv，并检查 Git 的其他外部程序入口。不能仅依赖 `GIT_OPTIONAL_LOCKS=0` 或命令参数名称来兑现只读承诺。

## 2. P1：同一数据目录缺少进程间独占保护

代码位置：

- [启动时使用固定数据目录并处理恢复](/Users/sda1_hacker/Desktop/humbert/humbert-agent/cmd/desktop/main.go:30)
- [Wails 应用配置](/Users/sda1_hacker/Desktop/humbert/humbert-agent/cmd/desktop/main.go:136)
- [任务启动恢复](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/tasks/manager.go:73)
- [将活动运行标记为中断](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/tasks/store.go:694)

应用使用固定的 `~/.humbert-agent`，没有数据目录进程锁，也未配置 Wails 的 `SingleInstance`。本地安装的 Wails 实现仅在显式提供该配置时初始化单实例管理。

当开发实例和已安装实例同时启动，或用户直接启动第二个进程时，两个进程会操作相同数据。第二个进程启动会调用 `ReconcileInterrupted()`，将第一个进程仍在执行的任务标记为 `interrupted`，而实际工具调用可能还在继续。各 Store 的 `sync.Mutex` 只能保护本进程；原子写文件也不能防止两个进程用各自快照相互覆盖。

复现：在临时数据目录保留一个 `running` TaskRun，再创建第二个 Task Store 并调用与启动流程相同的恢复方法。第一个 Store 重新读取时，运行状态已经变成 `interrupted`。没有启动两个真实桌面窗口，也没有访问用户真实数据。

建议：在备份、恢复和 `Bootstrap` 之前取得数据目录的进程间独占锁，并在应用退出时释放。单实例窗口行为可以补充，但只在创建 Wails 窗口时加锁已经晚于上述数据操作。

## 3. P2：点击刚过期的审批可能把会话留在等待状态

代码位置：

- [Resolve 提前设置 expired](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/approval/manager.go:158)
- [Expire 只处理 pending](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/approval/manager.go:270)
- [Runtime 对审批错误直接返回](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/runtime/service.go:580)
- [超时 worker 提前退出](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/runtime/service.go:812)

触发顺序：审批刚过期 → 用户点击操作先于超时 worker 获得处理机会 → `Resolve()` 标记 `expired` 并返回错误 → 超时 worker 的 `Expire()` 发现已经不是 `pending`，返回 `false` → worker 直接退出。

这两个分支都没有恢复或结束运行。最终审批已过期，但 Runtime 仍是 `waiting_approval`，`activeBySession` 仍占用会话，后续消息无法正常开始。手动取消可以清理，但超时机制没有按预期自动完成收尾。

复现调用真实的 `ResolveApproval()` 和 `awaitApproval()`，最终断言失败值为：`approval=expired`、`phase=waiting_approval`，会话仍被占用。

建议：让审批状态转换与 Runtime 的恢复/结束由一致的流程接管，确保每一种过期路径都有且只有一次收尾。

## 4. P2：主动助手的 500 条历史上限会删除未完成工作

代码位置：

- [记录上限](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/proactive/store.go:16)
- [加载时无条件裁剪](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/proactive/store.go:91)
- [写入时无条件裁剪](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/proactive/store.go:340)
- [自动运行收尾依赖记录](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/proactive/manager.go:576)

`Records` 同时承载历史、静默时段延期动作和正在执行的动作。超过 500 条时直接保留最后 500 条，没有区分 `deferred`、`executing` 与已完成记录。

两个已复现场景：

1. 静默时段接收 501 个不同事件，解除静默后只执行了 500 个提醒，较早的延期动作已经丢失。
2. 保留一个执行中的自动任务，再写入 500 条记录，原执行记录被淘汰；运行完成后，收尾流程找不到关联记录，内部任务仍保持 `active`。

建议：只裁剪终态历史；未完成记录必须保留到明确结束。同步检查查询上限，避免即使保留了未完成记录，后台扫描仍只读取最近 500 条而漏掉它们。

## 5. P2：删除会话后，旧列表响应仍可把它写回侧栏

代码位置：

- [无请求版本检查的列表写回](/Users/sda1_hacker/Desktop/humbert/humbert-agent/frontend/src/stores/sessions.js:398)
- [删除后清理缓存](/Users/sda1_hacker/Desktop/humbert/humbert-agent/frontend/src/stores/sessions.js:976)

`loadForAgent()` 的选择序列只保护切换后的主流程，无法阻止 `loadAgentSessions()` 先执行 `cacheAgentSessions()`。删除操作也没有使已经发出的会话列表请求失效。

复现：先发起列表读取，随后成功删除会话，最后让删除前的列表响应返回；刚移除的会话重新出现在 `items` 和侧栏缓存中。后端数据没有恢复，用户点击该条目会遇到会话不存在等错误。

建议：按 Agent 管理列表请求版本，并在创建、删除、归档、重命名以及 Agent 缓存清理时同步使相应旧快照失效。已有消息分页保护不能代替会话目录保护。

## 6. P2：部分设置模块仍存在旧快照覆盖或保存后刷新被吞掉

这些问题属于同一类用户可见的状态一致性缺陷，集中列出，避免每个症状单独计数。

| 模块 | 实现问题 | 已复现结果 | 代码 |
| --- | --- | --- | --- |
| 模型 | `load()` 无请求版本检查，保存多媒体配置也没有使旧读取失效 | 已保存的 `imageModelID=new` 被旧响应改回 `old` | [models.js](/Users/sda1_hacker/Desktop/humbert/humbert-agent/frontend/src/stores/models.js:77) |
| MCP | `load()` 总是复用 `pendingLoad`；设置页保存后仍调用同一个方法 | 保存后刷新拿到保存前快照，页面继续显示旧启用状态 | [mcp.js](/Users/sda1_hacker/Desktop/humbert/humbert-agent/frontend/src/stores/mcp.js:28)、[保存调用方](/Users/sda1_hacker/Desktop/humbert/humbert-agent/frontend/src/components/settings/MCPSettings.vue:310) |
| Skill | 设置页 `load()` 在已有请求时直接返回；启用/停用后需要的刷新被跳过 | 启用接口完成，旧列表仍把页面显示为未启用，随后还将旧引用同步给 Agent Store | [load](/Users/sda1_hacker/Desktop/humbert/humbert-agent/frontend/src/components/settings/SkillSettings.vue:378)、[保存后刷新](/Users/sda1_hacker/Desktop/humbert/humbert-agent/frontend/src/components/settings/SkillSettings.vue:518) |
| 主动助手设置 | `load()` 的旧设置响应可覆盖 `save()` 已写入的新设置 | 保存启用成功后页面变回未启用 | [proactive.js](/Users/sda1_hacker/Desktop/humbert/humbert-agent/frontend/src/stores/proactive.js:75) |
| 主动助手记录 | `refreshRecords()` 无条件替换记录数组 | 实时事件更新为成功后，旧列表又显示执行中 | [refreshRecords](/Users/sda1_hacker/Desktop/humbert/humbert-agent/frontend/src/stores/proactive.js:112) |

Node 复现控制 API Promise 的返回顺序并执行实际 Store；Skill 场景执行实际 Vue 单文件组件中的脚本，替换其导入依赖和生命周期，没有另写一份业务实现。

这些复现说明前端状态回退，不意味着后端保存被自动撤销。但用户看到旧值后再次编辑保存，可能据此提交错误配置。

建议：统一“读取去重”和“保存后强制刷新”的区别。保存必须使保存前的读取失效；实时事件和列表读取也需要明确的优先顺序。可复用现有请求版本工具，无须再引入新的状态管理框架。

补充代码核对：`stores/skills.js` 的 `remove()` 也有类似跳过刷新路径，但当前删除界面直接调用 API，因此未把该未接入界面的路径单独当作已发生的界面缺陷。

## 7. P3：自动任务归档失败后没有重试

代码位置：

- [先保存终态，再归档，归档失败只记日志](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/proactive/manager.go:612)
- [后台只扫描 executing 记录](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/proactive/manager.go:581)
- [启动恢复同样跳过终态记录](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/proactive/manager.go:629)

任务运行结束后，主动记录先保存为成功/失败，再尝试归档内部 Task。若归档遇到瞬时文件访问错误，仅记日志，方法仍返回成功。之后后台和重启恢复都跳过这条终态记录，归档不会重试。

复现：在临时目录中，短暂隐藏 Task 配置使第一次归档失败，随后恢复文件并依次运行后台扫描和启动恢复；Record 为 `succeeded`，内部 Task 仍为 `active`。

影响是内部任务收尾不完整并长期遗留。该任务是手动触发类型，本复现没有显示它会因此重复执行，不应夸大为重复调用模型或工具。

建议：保留可重试的归档待办状态，或让扫描器补偿“运行已终结、内部任务未归档”的组合，不重放实际任务。

## 8. P3：不支持的 Skill 没有正确禁用启用开关

代码位置：

- [前端状态字符串拼写](/Users/sda1_hacker/Desktop/humbert/humbert-agent/frontend/src/components/settings/SkillSettings.vue:864)
- [后端标准状态值](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/skills/types.go:32)
- [后端启用校验](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/skills/manager.go:242)

前端禁用条件使用 `skill.runtimeStatus === 'unsupporte'`，少了最后的 `d`。后端和同一页面的其他位置使用的值是 `unsupported`。

因此一个规范有效、但运行要求不被支持的 Skill，其启用开关仍可点击；后端最终拒绝，用户得到错误提示。这是确定的界面条件错误，未绕过后端校验。

建议：修正字符串，后续将状态判断集中到已有 Skill UI 工具函数，减少各组件重复手写状态值。

## 验证范围与复现文件

现有测试：

- `go test ./internal/approval ./internal/runtime ./internal/proactive ./internal/tasks ./internal/tools/builtin` 全部通过。
- 前端 `npm test`：44 项全部通过。

额外复现文件全部放在 `/tmp/humbert-fifth-review`，Go 使用 overlay 加载，未加入项目测试目录。新增断言以“正确行为”为目标，因此在当前实现上失败属于发现缺陷的预期结果。

- `approval_timeout_test.go`：审批过期竞态。
- `retention_test.go`：延期动作丢失、执行记录丢失、第二个 Store 干扰活动运行、归档失败不再重试。
- `git_read_test.go`：实际 Git 工具的 textconv 写入副作用。
- `frontend.test.mjs`：会话、模型、MCP、Skill 设置、主动助手设置及记录的 6 个场景。

复现命令：

```sh
cd /Users/sda1_hacker/Desktop/humbert/humbert-agent
GOCACHE=/tmp/humbert-go-cache go test -overlay /tmp/humbert-fifth-review/overlay.json ./internal/runtime ./internal/proactive ./internal/tools/builtin -run '^TestReview' -count=1 -timeout=60s
node --experimental-test-module-mocks --test /tmp/humbert-fifth-review/frontend.test.mjs
```

未调用真实模型，没有修改真实应用数据，未进行双桌面进程端到端测试。原生 Seatbelt 复现因宿主不允许嵌套沙箱而跳过。没有把这些验证限制本身算作代码缺陷。
