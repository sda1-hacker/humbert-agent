# 05 工具实现

主入口：[app/tools.go](../../internal/app/tools.go)、[registry.go](../../internal/tools/registry.go)、[types.go](../../internal/tools/types.go)、[guarded_tool.go](../../internal/tools/guarded_tool.go)。

项目本地定义的输入/结果结构及准确 JSON 字段名见 [工具参数索引](tool-api.md)。Schema 来自依赖的工具在正文明确其来源，避免把 Go 字段名当成模型参数。

## 注册、选择与执行实例

Registry 保存 Factory 与 Descriptor，不保存某个 Agent 的运行状态。Factory.Descriptor 描述 name/risk/internal/origin，Build 接收本轮 Scope 并生成 Eino InvokableTool；工具 Schema 来自 Eino 工具或 InferTool 的 Go 输入结构。

Scope 的 AgentID/SessionID/RequestID、Workspace、Sandbox、选中工具/Skill/MCP身份和结果预算来自 Resolver。模型参数只能指定本次动作，不能指定“变成另一个 Agent”或“跳过权限”。Registry 对名称、重复注册和 Schema 一致性做检查，按选择构建工具，并统一 Guard。Internal 工具用于历史/资源可靠性，不与普通用户勾选完全等价。

Guard 先构建 CapabilityIdentity，交 PermissionEngine；Allow 执行，Deny 返回明确未执行文本，Ask 触发 StatefulInterrupt。审批恢复重新验证身份，并使用 checkpoint 保存的参数。真实文件/网络/进程实现仍有第二次边界检查。

## 文件工具：共享一个 Backend

[filesystem.go](../../internal/tools/builtin/filesystem.go) 将六项能力接到 Eino filesystem middleware：每个 Factory 只开放一个工具，Schema/解析/结果格式由 Eino 维护，FilesystemBackend 只负责安全 I/O。

| 工具 | 风险 | 实现细节 |
| --- | --- | --- |
| list_files | read | LsInfo 使用受控 os.Root 读取目录；数量超限拒绝并建议缩小；跳过 symlink 和不允许读取的子项 |
| read_file | read | 检查最大文件字节数、UTF-8；offset 从 1 起，limit 默认 2000并受 MaxReadLines 限制；不静默截断超长行 |
| write_file | write | 限制 UTF-8/写入大小；取得路径锁、检查目标普通文件、保留权限或新建0600，原子替换 |
| edit_file | write | 旧文本非空且与新文本不同；读取原文、匹配替换、检查版本后写回，拒绝危险文件类型 |
| glob_files | read | 使用 doublestar 模式匹配，遍历与返回数量受限；子路径仍经过允许规则 |
| grep_files | read | 正则匹配受限文本文件，受遍历/结果/文件大小限制，避免把所有工作区内容一次送给模型 |

Schema 请求类型由 Eino filesystem 提供，实际字段在 Backend 的 req 使用处可核对；不是项目自己另维护一份参数协议。

[sandbox_fs.go](../../internal/tools/builtin/sandbox_fs.go) 把相对路径解析到 Workspace，绝对路径交 PathGuard；按照允许根打开 os.Root，并校验子目标。`file_transactions.go` 对多目标按排序后的规范路径加锁，避免反向锁顺序死锁；等待锁可取消，引用归零移除锁记录。

`fileVersion.check` 比较文件身份、size、mode、mtime，必要时比较原文；外部编辑后拒绝覆盖。`atomic_fs.go` 写同目录临时文件，提交前校验版本再替换。这是应用内并发和受控路径保护，不是“外部任何进程永远无法改文件”的保证。

## 独立文件动作

源码：[file_ops.go](../../internal/tools/builtin/file_ops.go)、[apply_patch.go](../../internal/tools/builtin/apply_patch.go)。

| 工具 | 核心输入与行为 |
| --- | --- |
| copy_file | source 与 attachment_id 二选一，destination、overwrite；附件只能来自当前 Session，普通文件读权限与目标写权限分别检查 |
| move_file | source/destination/overwrite；源与目标均要求 FULL，删除来源不因为“允许写目标”而自动获得授权 |
| delete_file | 只删除一个普通文件，拒绝目录和 symlink，要求 FULL |
| apply_patch | changes 数组，每项 path/old_text/new_text/create；旧文本必须唯一匹配，新建要求不存在，先全部校验再写入 |

这些 Descriptor 都是 write，但 PathOperation 对删除/移动要求的 FULL 高于普通修改所需 READ_WRITE。Risk 是审批策略维度，不是精确路径权限。

apply_patch 对每个文件原子写入，**多文件提交没有整体事务回滚**：后一个文件遇到 I/O 错误时，前面已经写入的文件可能保留。全部预检减少常见失败，但不等于全有或全无。维护改进应优先增加明确的部分完成反馈，而不是盲目添加复杂回滚框架。

## 本地命令、Skill 脚本与 Git

[run_command.go](../../internal/tools/builtin/run_command.go) 输入 command/args、working_directory 和 timeout_seconds；解析实际 executable，不通过 Shell 拼接命令。命令、参数数量/长度、超时和输出受配置约束，环境由装配层的 SafeCommandEnvironment 传入，不直接继承全部 os.Environ。执行交 Sandbox.Runner。

非零 ExitCode 是结构化正常结果，模型可以看编译/测试错误继续处理；无法启动、路径拒绝、父取消是 Go error。输出包含 timed_out、termination_reason、duration_ms、output_truncated 和 native_sandbox，不能仅凭 exit_code=-1 猜测原因。

[run_skill_script.go](../../internal/tools/builtin/run_skill_script.go) 只运行已启用且身份匹配的 Skill 包脚本，校验脚本相对路径、文件内容身份和运行方式；进程策略派生只读视图，临时写入到每次调用的目录。不是把 SKILL.md 中任意命令自动执行。

[git_tools.go](../../internal/tools/builtin/git_tools.go) 提供 git_status、git_diff(path/staged/file)、git_log(path/limit，默认20最多100)。使用固定查询参数、受控 Git 环境和只读派生策略；输出有 ExitCode/Truncated/NativeSandbox。不提供任意 Git 写操作工具。

## 搜索与网页正文

[websearch_tool.go](../../internal/tools/builtin/websearch_tool.go) 将 query/count/allowed_domains/blocked_domains 交 `websearch.Service`；先检查本轮允许网络。返回标题、URL、摘要/时间和 backend diagnostics。

[webfetch_tool.go](../../internal/tools/builtin/webfetch_tool.go) 输入已知 URL/max_chars，拒绝非 http/https、用户名密码、无 Host、非法端口和超长 URL。手动处理重定向，每跳重新验证；限制响应字节数和跳数，2xx 后转换 HTML → Markdown 或保留 JSON/文本，按 Unicode 字符截断并返回 final_url/status/format/truncated。

publicWebDialer 校验所有 DNS结果，有私网/回环/链路本地等地址就拒绝混合解析；实际拨号使用已校验 IP，不重新按域名连接，不使用系统代理。URL 校验与 socket 校验分别负责格式和网络边界。

## 浏览器

[browser_tool.go](../../internal/tools/builtin/browser_tool.go) 的 browser 支持 open/snapshot/click/type/scroll/press/back/forward/refresh/show/screenshot/close。一个 Agent 对应独立持久 Profile；用户和工具使用同一可见 Chrome，测试可使用无头模式。Factory 管理 session、空闲回收和 Close。

启动 Chrome 指定应用 Profile、临时 CDP 端口；读取 DevToolsActivePort，连接 WebSocket，Page/Runtime 等域执行导航、页面脚本和键盘/鼠标操作。pending map 以 CDP 请求 ID对应应答，限制消息大小，Context 取消中断等待。页面 snapshot 提取文本与可操作元素，交互后再次读取页面状态；验证码/验证页返回需要人操作的结构化标记。

截图 PNG 保存到当前 Session 附件，可选调用视觉辅助生成观察。输出给模型的是附件 ID/必要观察，复制到用户工作区必须另调 copy_file；不会默认把所有截图作为工作区产物。

[browser_proxy.go](../../internal/tools/builtin/browser_proxy.go) 是本轮新增的 HTTP 出站边界：同一个 Chrome 的新页面也使用代理，HTTP/WS 转发交 ReverseProxy，HTTPS走 CONNECT，拨号复用 publicWebDialer。64 个请求槽、128个连接、头部/空闲超时和 hijack 连接跟踪由代理拥有；Close 回收隧道和 Transport。Chrome 移除 localhost bypass、禁用 QUIC，并请求 WebRTC 非代理 UDP 限制；WebRTC效果尚需版本/平台实测，不等于完整 OS 网络隔离。

## 会话资源、文档与安装

| 工具 / 源码 | 实现 |
| --- | --- |
| [session_history](../../internal/tools/builtin/context_history.go) | 内部 read 工具，从当前 ActiveBranch 查找/恢复消息，提供受限历史内容与稳定 EntryID，不开放任意其他会话 |
| [context_resource](../../internal/tools/builtin/context_artifact.go) | 内部 read 工具，按资源 ID、类型和 offset/limit 读取当前会话 Artifact/附件，返回分页信息 |
| [extract_document](../../internal/tools/builtin/extract_document.go) | 内部 read 工具，当前附件 ID 或允许文件路径取原件；调用 documenttext 转 Markdown，缓存解析结果并按需返回，路径能力仍受文件开关和 Sandbox约束 |
| [install_skill](../../internal/tools/builtin/install_skill.go) | 用户明确请求的远程安装，通过 Skills Manager下载/校验/提交，可为当前 Agent启用；安装并不让已冻结 Turn 动态增加旧快照中没有的工具 |

## 任务、协作与轻量系统工具

[schedule_task.go](../../internal/tools/builtin/schedule_task.go) 输入 name/prompt/execution/schedule_type/time_zone 及对应时间字段，创建归属当前 Agent 的 Task；notification只发文本，agent执行指令。风险策略下逐次确认，后台任务/子 Agent 对该能力有额外禁用，避免循环安排。

[collaboration_tools.go](../../internal/tools/builtin/collaboration_tools.go) 提供 list_agents 与 run_agent，均为 read Descriptor；后者是同步 Agent-as-Tool，其真正工具动作仍使用父 Permission/Sandbox，不能把 read 标签理解为子 Agent不能写文件。

[system_tools.go](../../internal/tools/builtin/system_tools.go) 的 get_current_time 支持 IANA时区；update_plan 返回完整结构化计划并规范化多个 in_progress，只保留第一个活动项。计划工具是执行过程结果，不是 Tasks 调度计划，也没有在这里建立独立的永久计划数据库。
