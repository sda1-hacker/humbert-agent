# 代码审查：已确认的问题

日期：2026-09-27。下文是修复前的审查记录，保留当时的复现与代码路径供追溯。六处问题现已修复，当前实现、正式回归测试与验证结果见 [修复记录](/Users/sda1_hacker/Desktop/humbert/humbert-agent/docs/architecture/code-review-fixes-2026-09-27.md)。历史行号和临时测试接口可能随修复失效。

使用 Go overlay 注入临时测试，以及 Node mock 的临时测试：没有向仓库加入默认会失败的测试，没有读取真实敏感数据，没有启动真实 Chrome 或调用云端模型。

## 结果概览

| 优先级 | 问题 | 验证 |
| --- | --- | --- |
| P1 | 递归搜索绕过子路径 BLOCKED 规则 | 临时禁止目录的直接读取被拒绝，但 grep 返回其中伪造的秘密文本 |
| P1 | browser 忽略 NetworkNone | 禁网 Scope 仍尝试启动 Chrome；用不存在的测试可执行路径阻止真实启动 |
| P1 | 同文件并发编辑丢失更新 | 两次 Edit 均成功，最终文件只保留其中一处修改 |
| P2 | TaskRun 旧快照覆盖终态 | 模拟启动回填与终态写入的合法交错，succeeded 被覆盖成 running |
| P2 | 摘要调用绕过任务模型次数上限 | MaxModelCalls=1，模拟模型实际调用 2 次，Runtime 只计 1 次 |
| P2 | 同会话旧消息刷新覆盖新消息 | 第二个请求先返回新回答，第一个请求后返回后界面恢复成旧消息 |

P1 表示涉及安全边界或数据修改正确性，建议发布前解决；P2 是应修复的功能正确性问题。

## 1. 递归 grep 没有逐个检查路径权限

位置：[filesystem.go:277](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/tools/builtin/filesystem.go:277)、[filesystem.go:295](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/tools/builtin/filesystem.go:295)、[walkRoot](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/tools/builtin/search_files.go:35)。

`GrepRaw` 只在打开搜索起点时调用 `openSandboxTarget`。随后 `walkRoot` 遍历后代，`readRootFileWithLimit` 直接通过已经取得的 os.Root 读取文件，没有对每个子路径再调用 PathGuard。os.Root 能限制逃出根目录，不能表达根目录内部的 BLOCKED 例外。

真实可达配置：Standard 默认允许读取用户 Home，同时在其中对 `.ssh`、`.aws`、应用配置和会话数据等设置禁止规则。对允许的父目录搜索时，这些禁止规则不会在递归过程中再次检查。相关策略见 [manager.go:104](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/sandbox/manager.go:104)。Glob 也共用遍历入口，会暴露禁止路径的文件名。

复现完全使用临时目录：建立 `blocked/secret.txt`，内容为 `AUDIT_FAKE_SECRET`；给该子目录配置 AccessBlocked。直接 read_file 拒绝，父目录 grep 却返回该行。

```text
direct read denied, recursive grep returned protected content:
[{Content:AUDIT_FAKE_SECRET Path:blocked/secret.txt Line:1}]
```

修复方向：共享遍历入口必须携带有效 Policy，在访问每个目录/文件前检查实际路径；禁止目录直接跳过整棵树。Grep 读取与 Glob 输出都遵循同一裁决，并保留受控句柄。回归测试应包含“父目录允许、后代禁止”，不能只测试 `../` 和外部 symlink。

## 2. browser 没有执行禁网策略

位置：[BrowserFactory.Build](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/tools/builtin/browser_tool.go:127)、[run](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/tools/builtin/browser_tool.go:138)、[Chrome 启动](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/tools/builtin/browser_tool.go:467)。

web_search、web_fetch 会检查 `scope.SandboxPolicy().AllowsNetwork()`，browser 没有对应检查。它只验证 URL 是否为允许的公网地址，然后通过 `exec.Command` 启动 Chrome；公网 URL 合法与用户是否禁网是两件事。Registry/Guard 没有替它补上 NetworkNone 的强制校验。

复现：构造 NetworkNone Scope，调用 browser open，设置 Chrome 路径为临时目录中不存在的文件。实际得到“启动 Chrome 失败：文件不存在”，而不是网络策略拒绝，证明执行已经越过本应拒绝的位置。测试未进行真实浏览器或公网访问。

影响：用户禁用 Agent 网络访问后，只要 browser 工具可用且通过权限审批，代码仍允许启动并操作联网浏览器。已有按 Agent 复用的浏览器也没有在后续调用中重新检查本轮网络策略。

修复方向：在每次可能访问网络/页面的动作前执行策略检查；close 等清理动作可单独保留。明确策略变化后既有浏览器连接的处理，避免只在创建新浏览器时检查一次。

## 3. 同文件并发 Edit 会静默覆盖彼此的修改

位置：[读取原文](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/tools/builtin/filesystem.go:204)、[替换与提交](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/tools/builtin/filesystem.go:219)、[atomicWriteWorkspaceFile](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/tools/builtin/atomic_fs.go:56)。

Edit 的“读取 → 匹配 → 替换 → Rename”之间没有共享文件锁，也不校验提交时原文件是否还是读取的版本。临时文件加 Rename 只能保证单次替换完整，不能让读改写整个事务原子化。

Runtime 的串行限制只针对同一 Session；同一 Agent 的不同 Session、共享 Custom Workspace 的 Agent，以及手动聊天与后台任务，都可能同时操作同一文件。

复现：初始文件为 `alpha=0 / beta=0`，两个独立编辑分别修改 alpha 和 beta。临时测试在各自读完原文、写入前设置同步屏障，模拟两次合法操作的并发交错。两个调用均成功，结果为：

```text
both edits succeeded but one update disappeared: "alpha=0\nbeta=1\n"
```

修复方向：按规范化文件身份，协调所有相关修改入口的文件级事务；在提交前验证读取版本，对用户编辑器等外部修改明确报告冲突。`apply_patch`、copy/move 等也需要纳入同一个并发策略，不能只给某个 Factory 实例加锁。

## 4. TaskRun 启动回填可以覆盖已经完成的状态

位置：[startRun 回填](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/tasks/manager_schedule.go:385)、[Store.UpdateRun](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/tasks/store.go:389)、[终态事件保存](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/tasks/manager_events.go:80)。

`startRun` 在 Runtime.StartTurn 返回后，先 GetRun，再填入 RequestID/RuntimeRunID，最后整体 UpdateRun。与此同时，Runtime worker 可以发终态，由事件处理器更新相同 Run。Store 的锁只覆盖单次方法，不能覆盖调用者分开的 Get/Update；UpdateRun 不校验旧版本或禁止终态倒退。

复现的是实际代码允许的这个顺序：

```text
A: startRun 读到 starting 快照
B: 终态回调写 succeeded + FinishedAt + ResultMessageID
A: 用旧快照补 RequestID，将 starting 改为 running，整体保存
```

结果：`status=running result="" finished=<nil>`。这不是 Go 内存 data race，所以 `go test -race` 通过也不能排除。

影响：任务实际完成，但 UI/持久化显示运行中，完成时间与结果引用丢失。类似读改写还会影响计数和审批状态。

修复方向：为 TaskRun 增加锁内 Mutate/状态迁移接口，启动回填只修改身份字段；维护终态不可被旧状态覆盖的约束，必要时使用版本 compare-and-swap。

## 5. 执行上限没有覆盖所有模型调用

位置：[主模型计数中间件](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/runtime/limits.go:71)、[中间件追加位置](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/runtime/limits.go:117)、[摘要模型注入](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/runtime/resolver.go:321)、[Summarization 配置](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/contextengine/middleware.go:64)。

次数限制挂在主 Agent 的 BeforeModelRewriteState，摘要则由前面的 ContextHandler 直接调用 Utility/Chat 实例。原生 Summarization 自己发起的 Generate 不会经过主 Agent 的次数计数。摘要响应也不走主 Executor 的普通回答用量路径。

模拟模型复现：设置 MaxModelCalls=1，输入达到摘要阈值，先生成摘要，再允许一次主模型调用。

```text
MaxModelCalls=1, actual calls=2, runtime counted=1
```

另外，[BuildChildAgent](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/runtime/resolver.go:161) 未将父任务共享的 limitState 放入子 Agent；子工具生命周期 Snapshot 也没有该状态。其固定 MaxIterations=12 不能替代父任务的模型/工具次数预算。这部分依据源码路径确认，没有冒充完整子 Agent 端到端复现。

修复方向：共享每次运行的预算状态，覆盖主模型、摘要、视觉辅助和子 Agent 的真实模型/工具调用，在调用前预留次数、调用后统一记账。Token 上限还需明确是调用前估算限制还是响应后停止，不能把不完整统计展示为全运行消耗。

## 6. 同会话的消息刷新缺少过期请求检查

位置：[sessions.refreshMessages](/Users/sda1_hacker/Desktop/humbert/humbert-agent/frontend/src/stores/sessions.js:638)。

该方法等待 listMessagePage 后只检查 `sessionID === selectedID`，没有请求序号。这个条件能防止 A 会话覆盖 B 会话，无法防止同一会话旧请求覆盖新请求。Runtime 的发送后刷新、终态刷新等会调用此方法。

Node 测试复现：发出旧、新两个查询；新查询先返回，messages 显示 `new-answer`；旧查询后返回，messages 被覆盖为 `old-message`。

```text
AssertionError: late stale IPC response replaced current messages
actual: 'old-message'
expected: 'new-answer'
```

影响：新回答在 UI 中消失、历史倒退，切换会话或再次刷新后又出现。后端 JSONL 未丢失。

修复方向：为消息加载增加递增请求序号或版本，只有最新请求提交到当前视图；与加载旧页、搜索定位窗口之间也要定义覆盖关系。已有 Runtime Overview 的竞态保护并不会自动保护 Session Store。

## 验证与覆盖缺口

现有测试复查：

```sh
GOCACHE=/tmp/humbert-go-cache go test ./internal/tools/... ./internal/runtime ./internal/tasks ./internal/contextengine ./internal/searchindex
npm --prefix frontend test
```

结果：上述 6 个 Go 包通过；前端 19 项测试通过。针对本文新增的 5 个 Go 临时用例、1 个 Node 临时用例均触发预期的失败断言，暴露缺失的回归场景。

本机临时复现材料（临时目录清理后失效）：

- `/tmp/humbert-audit-builtin_test.go`：路径权限、禁网、并发编辑。
- `/tmp/humbert-audit-runtime_test.go`：摘要调用预算。
- `/tmp/humbert-audit-tasks_test.go`：TaskRun 旧快照覆盖。
- `/tmp/humbert-audit-overlay.json`：将以上测试映射到原包，源码不落到仓库。
- `/tmp/humbert-audit-frontend.test.mjs`：真实 Pinia Store、模拟 IPC 返回顺序。

```sh
GOCACHE=/tmp/humbert-go-cache go test -overlay /tmp/humbert-audit-overlay.json -run '^TestAudit' -count=1 -timeout=30s ./internal/tools/builtin ./internal/runtime ./internal/tasks
node --experimental-test-module-mocks --test /tmp/humbert-audit-frontend.test.mjs
```

以上是修复前使用的临时复现命令，不适用于修改后的 Store 等接口。现在请运行修复记录中的正式测试。测试通过与实现精简都不等同于已经具备完整的端到端交付验证。
