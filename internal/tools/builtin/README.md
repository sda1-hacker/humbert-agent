# 内置工具适配

本包把现有领域能力适配成 Eino 工具。Factory 在 Bootstrap 注册，每轮通过 Scope 构造实例，再由 Registry 套上 Permission Guard。真实 I/O 继续执行路径、网络或进程校验。

| 文件 | 职责 |
| --- | --- |
| `filesystem.go` | 创建六个 Eino 文件工具，I/O 委托 `internal/fileops` |
| `run_command.go`、`run_skill_script.go` | 参数与 Skill 身份检查、暂存脚本和执行策略 |
| `process.go`、`process_output.go` | 公共超时、Runner 调用、结果字段和 head/tail 输出限制 |
| `git_tools.go` | 固定只读 Git 查询，复用限长输出 |
| `extract_document.go` | 会话附件或受控文档原件转换、缓存和分页 |
| `context_history.go`、`context_artifact.go` | 按需读取历史及完整归档结果 |
| `websearch_tool.go`、`webfetch_tool.go`、`htmlmarkdown.go` | 网页搜索与抓取；真实拨号复用 `internal/publicweb` |
| `install_skill.go`、`collaboration_tools.go`、`schedule_task.go` | Skill、协作与任务领域的工具入口 |

浏览器和桌面工具分别在相邻的 `tools/browser`、`tools/desktop` 包注册，不在本包复写实现。`app/tools.go` 是默认工具装配位置；`tools/catalog.go` 提供能力设置元数据。

命令仍使用程序与 argv，不隐式启动 Shell；环境数组为空时也不继承宿主环境。Skill 脚本必须匹配本轮冻结身份，先暂存到工作区，再使用只读派生策略。Git 也强制只读进程策略。Permission Allow 不扩大本轮 Sandbox。

文件读写、目录和搜索继续经 PathGuard + os.Root；遍历逐后代检查规则，原子写入使用跨会话锁与提交前冲突检测。工具结果由已有 Reduction/会话资源存储归档，不另存一份事实。审批恢复执行 checkpoint 保存的原参数，避免批准内容与执行内容不同。
