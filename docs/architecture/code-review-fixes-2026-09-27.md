# 六处代码问题的修复记录

日期：2026-09-27。对应上一轮代码审查中的六处问题。本轮在当前工作区直接修复，关键并发和权限逻辑已补充中文注释；没有清空用户数据或修改数据格式。

## 实现与代码入口

| 问题 | 最终修改 | 主要代码 |
| --- | --- | --- |
| 递归搜索绕过受保护子目录 | `sandboxTarget` 保存本轮策略，遍历每个后代前重新裁决；禁止目录整棵跳过，grep、glob、目录列表遵循同一规则 | [canReadChild](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/tools/builtin/sandbox_fs.go:75)、[遍历](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/tools/builtin/search_files.go:33) |
| 浏览器忽略禁网 | 在启动、复用浏览器和操作页面前检查当前 Scope；禁网调用同时回收旧连接，close 仍可用于清理 | [浏览器入口](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/tools/builtin/browser_tool.go:138) |
| 并发编辑丢失更新 | 文件锁按规范化绝对路径跨 Backend、Factory、会话共享；读改写全程持锁，写入、补丁、复制、移动、删除共同使用；多文件按固定顺序取锁，等待可取消，锁记录用完释放 | [文件事务](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/tools/builtin/file_transactions.go:39)、[提交前版本检查](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/tools/builtin/file_transactions.go:100) |
| TaskRun 旧快照覆盖终态 | 删除整对象 UpdateRun 接口，所有调用改为 Store 锁内 MutateRun；启动只回填身份字段；终态禁止修改状态、用量和结果；失败收敛读取最新记录，运行占用释放幂等 | [MutateRun](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/tasks/store.go:393)、[启动与取消](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/tasks/manager_schedule.go:127)、[运行事件](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/tasks/manager_events.go:1) |
| 摘要、子 Agent 绕过预算 | 移除只统计主 Agent 步骤的中间件，改在真实 Generate/Stream 调用前原子预留次数；主模型、摘要、视觉桥接、浏览器截图分析和子 Agent 共用预算；工具上下文传递父运行身份；流式 Usage 按累计差值统计 | [模型调用与记账](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/runtime/model_accounting.go:24)、[预算状态](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/runtime/limits.go:1)、[子 Agent 接线](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/runtime/resolver.go:145) |
| 前端旧刷新覆盖新消息 | Session Store 增加视图请求版本，刷新、搜索定位、分页与重置共同校验；新窗口落地使旧窗口分页失效，旧请求不能清除新请求的加载状态 | [消息加载](/Users/sda1_hacker/Desktop/humbert/humbert-agent/frontend/src/stores/sessions.js:631) |

这些修改保留 Eino 的文件工具 schema、Agent/Runner、Summarization 和流式协议；应用仅负责权限、事务、预算及 UI 状态这些自身约束。

## 正式回归测试

新增 13 个 Go 顶层测试和 5 个前端测试，并更新原有的模型调用限制测试。临时审查测试已经转化为正式测试中的行为断言，后续不再依赖 `/tmp` overlay。

- [security_regression_test.go](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/tools/builtin/security_regression_test.go)：父目录允许、子目录禁止，直接读取与 grep/glob/list 一致；禁网下各浏览器动作拒绝、旧槽位清理、close 可用。
- [file_transactions_test.go](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/tools/builtin/file_transactions_test.go)：16 个独立 Backend 并发编辑不丢更新；六类文件修改共用可取消的锁；已有文件被改或新文件被抢先创建时拒绝覆盖，并清理临时文件。
- [run_updates_test.go](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/tasks/run_updates_test.go)：完成后回填身份保留终态结果；终态字段不能通过指针修改；并发事件计数完整；重复释放不误扣其它运行；取消队列不等待调度周期。
- [model_accounting_test.go](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/runtime/model_accounting_test.go)：真实摘要中间件消耗任务预算；子模型及子工具继承预算；流被两个消费者读取时 Usage 不重复；并发模型不超额预留；工具内辅助视觉调用也计数、计 Token。
- [sessionMessageFlow.test.js](/Users/sda1_hacker/Desktop/humbert/humbert-agent/frontend/src/utils/sessionMessageFlow.test.js)：同会话乱序返回、搜索与刷新交错、相同游标分页、重置失效、刷新期间发出的分页。

模型使用模拟实现，文件使用临时目录，浏览器测试不启动真实 Chrome 或联网。这些测试不等同于真实 Provider 与桌面交互的端到端验收。

## 验证结果

以下命令已通过：

```sh
GOCACHE=/tmp/humbert-go-cache go test -race ./...
npm --prefix frontend test
npm --prefix frontend run build
GOCACHE=/tmp/humbert-go-cache go build -tags production -trimpath -buildvcs=false -o /tmp/humbert-agent-review-fixed ./cmd/desktop
git diff --check
```

前端合计 24 项测试全部通过。生产模式桌面程序已在本机编译，未启动应用，也未签名或打安装包。Go 链接器仍报告现有的 macOS 最低版本与依赖编译版本不一致警告；这次成功编译不能证明对旧版 macOS 的兼容性。

## 明确的边界

- 文件锁协调当前应用进程里的文件工具，不会阻止外部编辑器、命令行或其它进程写文件。提交前会比较身份、大小、时间、模式及读改写原文，但普通文件系统没有针对外部进程的原子“比较并替换”；版本检查与 Rename 之间仍有很短的外部竞争窗口。多文件补丁也不是跨文件系统事务，中途 I/O 失败可能已有部分文件提交。
- Token 上限按 Provider 已报告的用量累计，达到阈值后禁止后续调用；当前响应或已经在途的并发响应可能跨过阈值。Provider 未返回 Usage 时无法提供精确消费统计。模型和工具次数则在调用前原子预留。
- 浏览器按每次调用的冻结 Scope 检查。收到禁网 Scope 调用会清理旧浏览器；仅修改设置、尚无新工具调用时，不会主动扫描并关闭所有正在运行的旧 Scope。
