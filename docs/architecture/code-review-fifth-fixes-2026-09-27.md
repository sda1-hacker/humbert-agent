# 第五轮审查修复说明（2026-09-27）

本轮落实[剩余问题清单](code-review-fifth-2026-09-27.md)中的 8 类问题。保留原有 Eino / Wails / Pinia 架构，在现有边界上修正执行、恢复与异步状态规则，关键位置已补中文注释。

## 修复与代码入口

| 审查项 | 最终行为 | 实现入口 |
| --- | --- | --- |
| Git 只读能力 | 三个 Git 工具统一开启 `ReadOnlyWorkspace`；差异关闭 textconv / external diff；禁用 fsmonitor、pager 和子模块摘要的相关外部程序入口 | [git_tools.go](../../internal/tools/builtin/git_tools.go) |
| 数据目录多进程访问 | 桌面启动先取得操作系统独占锁，再恢复、备份、初始化；CLI 备份/恢复共用该锁 | [桌面入口](../../cmd/desktop/main.go)、[CLI](../../cmd/data/main.go)、[instancelock](../../internal/instancelock/lock.go) |
| 过期审批卡住会话 | 过期点击只返回错误；超时 worker 独占 Pending→Expired 转换及 Runtime 恢复/结束责任 | [审批 Manager](../../internal/approval/manager.go) |
| 主动记录丢失 | 500 条上限只约束完成历史；延期及执行中记录完整保留；后台读取全部待办；刚完成的长任务作为最新历史保留 | [Store](../../internal/proactive/store.go)、[Manager](../../internal/proactive/manager.go) |
| 删除会话后重现 | 会话目录按 Agent 校验请求版本；修改、归档、删除及移除 Agent 使旧读取和旧选择流程失效 | [sessions.js](../../frontend/src/stores/sessions.js) |
| 设置状态回退 | 模型、Skill、MCP、主动助手使用请求版本校验；保存后刷新不复用保存前请求；实时事件使旧列表失效 | [models.js](../../frontend/src/stores/models.js)、[mcp.js](../../frontend/src/stores/mcp.js)、[proactive.js](../../frontend/src/stores/proactive.js)、[Skill 设置](../../frontend/src/components/settings/SkillSettings.vue) |
| 自动任务归档失败 | 先幂等归档，成功后才提交主动记录终态；失败保留 executing，由后台或启动恢复重试 | [finalizeAutomationRun](../../internal/proactive/manager.go) |
| Skill 开关判断错误 | 修正 `unsupported` 状态比较，不支持的 Skill 正确禁用启用开关 | [SkillSettings.vue](../../frontend/src/components/settings/SkillSettings.vue) |

## 关键实现细节

### 进程锁

- macOS / Linux 使用 `flock`，Windows 使用 `LockFileEx`，不增加依赖。
- 锁文件位于数据目录旁，例如 `~/.humbert-agent.lock`。恢复替换数据目录时，不会替换锁文件。
- 锁文件不删除，避免两个进程持有不同 inode 的锁。释放文件句柄或进程异常退出时，系统释放锁；空锁文件留在磁盘不代表正在占用。
- 桌面退出先完成 Core 关闭，再释放锁。`os.Exit` 放在资源生命周期函数之外，避免跳过正常关闭的 defer。
- CLI 的 `backup` / `restore` 必须取得同一把锁；只验证独立归档的 `verify` 不锁应用数据。

### 主动助手保留和恢复

裁剪规则放在统一持久化入口，加载历史文档也采用同一规则。所有延期、执行及等待收尾的记录都会留下，只裁剪旧的 ignored / succeeded / failed 历史。

自动任务收尾采用“归档 → 保存主动记录终态”的顺序。归档失败时不消费待办；归档成功但记录写盘失败时，下次重复归档也不会执行任务。启动阶段遇到收尾的瞬时错误会记录日志、保留后台重试机会，不因为该错误阻止整个应用启动。

### 前端异步规则

- 复用 `latestRequest.js`，令牌按 Store 或组件实例隔离。
- 会话目录请求与聊天区选择分别验证版本；过期结果不会重新插入缓存，也不会重新选中已删除会话。不同 Agent 的正常读取仍可并行。
- MCP / Skill 的普通读取可共享当前 Promise；保存后的刷新显式使用 `force: true`。旧请求不能清除新请求的 loading。
- Skill 设置更新 Agent 的引用时通过 `applySkillSelection()` 提交，避免直接修改 Store 数组而绕过请求失效规则。Skill 包安装页和 Skill Store 的刷新路径一起修正。
- 主动助手的设置、状态、记录独立校验。事件更新优先于此前发出的快照，事件后的合并刷新补齐完整记录列表。
- 过期请求的错误不干扰当前页面；当前请求失败仍正常报告。

## 回归验证

项目内新增/扩展的测试：

- [进程锁测试](../../internal/instancelock/lock_test.go)：真实子进程竞争、替换数据目录、持锁进程崩溃后重新获取锁。
- [CLI 测试](../../cmd/data/main_test.go)：backup / restore 在访问口令、凭据和数据之前拒绝已占用目录。
- [Git 测试](../../internal/tools/builtin/git_readonly_test.go)：实际 Git 仓库配置 textconv / fsmonitor 钩子，验证查询正常返回且没有写入副作用；关闭原生隔离时拒绝执行。
- [审批 Manager 测试](../../internal/approval/manager_test.go)和 [Runtime 测试](../../internal/runtime/approval_timeout_test.go)：点击先于超时 worker 时仍能完成过期收尾；恢复失败也释放会话。
- [主动助手测试](../../internal/proactive/retention_test.go)：501 个延期事件跨重载不丢失；执行记录跨历史裁剪仍能收尾；归档失败可恢复。
- [前端状态测试](../../frontend/src/utils/settingsStateFlow.test.js)和 [Agent 测试](../../frontend/src/utils/agentStateFlow.test.js)：旧请求、乱序响应、旧错误、当前错误、保存失败和实时事件等场景。Skill 设置测试执行真实 Vue 组件脚本。

验证结果：

- `go test -race ./...` 通过。
- `go vet ./...` 通过。
- 前端 `npm test`：57 项通过。
- 前端 `npm run build` 与桌面 Go 编译通过。
- macOS 宿主环境的 Git 原生沙箱测试、工作区只读测试通过，未跳过。
- 进程锁 Windows / Linux 测试二进制交叉编译通过；这两个平台没有进行实际运行验证。

本轮没有调用真实模型，没有启动双桌面窗口或修改用户真实数据；多进程验证使用临时目录及真实子进程。桌面链接仍有此前已存在的 macOS deployment target 警告。

行为变化：若原生文件系统沙箱关闭或不可用，Git 只读工具现在会明确拒绝执行，以兑现其只读权限声明。
