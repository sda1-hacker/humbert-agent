# 08 Skills、MCP 与子 Agent 协作

这些模块都在扩展 Agent 的能力，但加载方式不同：Skill 是冻结的说明与资源包；MCP 提供外部协议工具；子 Agent 使用自己的模型和指令完成一个同步委派任务。它们最终接入 Runtime 的能力解析和工具权限链路。

## Skill 的数据和解析

实现入口：[types.go](../../internal/skills/types.go)、[parser.go](../../internal/skills/parser.go)、[discovery.go](../../internal/skills/discovery.go)、[manager.go](../../internal/skills/manager.go)。

一个 Skill Package 以 `SKILL.md` 为入口，可以带脚本、参考资料及其他文本资源。解析器拆分 YAML frontmatter 和 Markdown 正文，提取 name、description、license、compatibility、metadata、allowed-tools 及 context/agent/model 等字段。字段被解析不代表 Runtime 会实现它们所描述的全部执行语义；诊断和 runtime status 用于区分可以加载、待处理和不支持的包。

`inspectPackage` 不只检查入口文件，还遍历包内容、验证路径和大小边界，并计算内容身份。`Info` 面向列表和诊断；`Package` 包含实际内容；`SourceInfo` 记录安装来源、来源位置、安装/更新时间及安装时身份。名称规范化与兼容别名分开处理，别名不应成为第二份独立包。

`Manager` 管理安装根目录并保护维护操作。发现目录中的坏包时，通过 Issue/diagnostics 展示问题，避免一个坏包令所有技能消失。[diagnostics.go](../../internal/skills/diagnostics.go)、[aliases.go](../../internal/skills/aliases.go)、[sources.go](../../internal/skills/sources.go) 分别处理诊断、兼容名称和来源持久化。

## 安装、更新和回滚

[installer.go](../../internal/skills/installer.go) 的 `InstallFromDirectory` 按以下顺序执行：

1. 检查源目录并生成源包身份。
2. 在安装根目录创建私有临时目录，复制包内容。
3. 再次验证复制结果，比较名称和身份，发现源目录变化则拒绝提交。
4. rename 到正式目录，再验证正式包。
5. 写来源记录；来源保存失败时删除刚安装的包，并汇总回滚失败信息。

这样避免把半个包直接复制到可发现的正式位置。删除也先将目录移到隔离名称，再处理目录和来源记录；跨模块删除限制由 SkillMaintenance 用例负责，见第 10 章。

[update.go](../../internal/skills/update.go) 区分三个操作：`CheckUpdate` 准备候选并比较身份，不替换本地目录；`Update` 依据已记录来源更新正常安装包；`Reinstall` 支持从已知来源修复损坏安装。候选包名称变化会被拒绝，不能借更新悄悄把一个 Skill 换成另一个名称。

更新准备可能需要网络，提交前必须重新检查本地已安装状态。`captureInstalledState` 与替换函数比较预期身份，防止用户在下载期间修改本地内容后被覆盖。替换使用临时目录和旧目录备份，并在来源保存或验证失败时尝试恢复。`SourceDrifted` 表示当前本地身份与来源记录不同，不能与远端存在更新混为一谈。

远程来源见 [remote_source.go](../../internal/skills/remote_source.go)、[source_providers.go](../../internal/skills/source_providers.go)、[remote_installer.go](../../internal/skills/remote_installer.go)。解析器识别 skills.sh、GitHub/GitLab/Gitee 仓库地址及直接 HTTPS ZIP；来源提供者负责将地址转为稳定的仓库/子目录描述。Git 路径通过 fetch 和 archive 提取候选内容，随后进入相同的包验证流程；不是启动仓库内的安装脚本。下载、解包、文件数量、大小和路径边界分别受限制，包中的可执行资源也不会因为安装而自动执行。

## 一轮执行中的按需加载

[snapshot.go](../../internal/skills/snapshot.go) 解析 Agent 选中的技能，复制包内容并建立 revision。运行期间读取的是这个快照；用户在设置页更新 Skill 不会改变正在等待审批的调用。

Runtime 将快照交给 Eino Skill middleware，并以项目提供的描述和参数暴露 `skill` 工具。模型先获得技能名称和描述，根据需要请求正文或包内指定文件。资源内容来自冻结的包，不允许模型绕过包边界任意读取安装目录。

Skill 的指令是模型上下文，不是权限规则；`allowed-tools` 等元数据也不能替代 Guard、PathGuard 或系统沙箱。执行包内脚本仍需走项目的命令工具与审批链路。参见 [Runtime Resolver](../../internal/runtime/resolver.go) 和 [Skill middleware 构造](../../internal/skills/snapshot.go)。

## MCP 配置、目录和运行连接

实现：[types.go](../../internal/mcp/types.go)、[store.go](../../internal/mcp/store.go)、[manager.go](../../internal/mcp/manager.go)、[catalog.go](../../internal/mcp/catalog.go)。

Server 配置包含传输类型、命令/参数或 HTTP endpoint、环境/请求头凭据引用，以及安全和风险相关设置。Manager 是控制入口；Store 原子保存配置；Catalog 缓存发现的工具元数据。Agent 选择具体 Server/Tool，配置了 Server 并不等于所有 Agent 自动得到全部工具。

[fingerprint.go](../../internal/mcp/fingerprint.go) 将影响连接的配置生成指纹。显示名称等展示信息变化不再要求重连；命令、endpoint、凭据引用、安全策略等连接相关变化仍需使连接失效。工具展示名称的规范化与原始 MCP tool name 分开，调用时保留原始身份，见 [naming.go](../../internal/mcp/naming.go)。

[adapter.go](../../internal/mcp/einoadapter/adapter.go) 使用官方 MCP SDK 会话和 Eino adapter 构建工具；按 Agent 的选择过滤，再附上风险和 Server/Tool 身份。工具进入 Guard 后才能执行，不因来自 MCP 而绕开审批。返回结果可以包含结构化内容；对话中最终看到的文本仍受 Runtime 的输出和上下文处理影响。

## 连接的并发、失效和释放

核心在 [backend.go](../../internal/mcp/einoadapter/backend.go)。控制面的测试/发现连接与 Runtime 工具连接具有不同用途和缓存身份，不能把设置页“测试成功”直接当成某一轮已固定的能力。

Backend 为相同缓存键合并正在建立的连接，避免多轮同时创建相同进程/HTTP 会话。generation 与 fingerprint 用于检查连接建立期间配置是否已变化。失败连接设置指数退避：从 15 秒开始，最大 2 分钟；匹配同一代配置的请求在退避期内收到诊断，而不是反复启动失败的 Server。

运行取得 session entry 时，通过 request ID 持有引用。配置更新会将仍有运行使用的旧连接移到 retired 集合；新执行使用新连接，旧执行继续使用原来冻结的工具。Runtime 结束观察者调用 `ReleaseRun`，引用归零后关闭退役连接。应用整体 Close 会关闭缓存和退役集合中的会话，连接关闭本身有一次性保护。

这一生命周期非常关键：审批恢复使用原 Eino checkpoint 和旧工具对象，如果配置修改立刻杀死所有旧连接，就会令恢复过程失去原执行环境。冻结、持有、退役、释放四个步骤共同保证运行边界。

## stdio 与 HTTP 的安全边界

[stdio_environment.go](../../internal/mcp/einoadapter/stdio_environment.go) 从受控基础环境构建子进程环境，并解析显式配置的凭据引用；避免把应用全部环境变量无条件透传。进程由沙箱相关执行路径启动，并保留诊断与退出清理，见 [stdio_diagnostics.go](../../internal/mcp/einoadapter/stdio_diagnostics.go)。

[http_transport.go](../../internal/mcp/einoadapter/http_transport.go) 为 MCP HTTP 请求建立专用 transport。endpoint 主机解析和实际拨号使用验证后的地址；是否允许回环或私网由配置明确控制。请求头凭据由 origin 限制的 round tripper 注入，不能因重定向发送到不同来源。这里的地址策略与网页抓取的“仅公开网页”策略不同，MCP 可能明确配置本地服务，不能把两者合并成一条硬编码规则。

连接失败在 Manager/Backend 中形成状态和诊断，运行能力解析可以排除失效工具。它不意味着模型一定能自动完成降级任务，模型得到的可用工具集合才是本轮事实。

## 子 Agent 的实现

[collaboration/manager.go](../../internal/collaboration/manager.go) 提供 `ListAgents` 与 `RunAgent`；[runtime/resolver.go](../../internal/runtime/resolver.go) 的 BuildChildAgent 构造子 Agent；[collaboration/store.go](../../internal/collaboration/store.go) 保存审计记录。

`ListAgents` 排除当前 Agent 和未开启 SubagentEnabled 的配置，只返回可选对象及截断说明。`RunAgent` 要求目标 ID、自包含任务文本和真实 ToolCall 上下文，拒绝调用自身；任务文本最多 32,000 个字符。

子执行由 Eino `adk.NewAgentTool` 同步驱动。它使用目标 Agent 的指令、模型和能力选择，但权限身份继承父执行；共享父执行取消、限制和审批链路。子 Agent 没有自动继承父会话完整历史，也不自动建立一个独立可见 Session。其任务输入应包含完成工作所需的上下文。

子 Agent 禁用再次委派和若干跨会话/安装/计划能力，防止无限递归或在委派中建立额外执行层。具体过滤以 Runtime 的子 Agent 构造逻辑为准，而不是靠提示词要求模型自觉遵守。

审计 Run ID 由父 request ID 和 tool call ID 派生。恢复时校验父/子身份及 task 与原调用相同；成功记录直接返回原结果，失败记录返回原错误，从而避免同一个委派因审批恢复被再次完整执行。中断记录进入等待状态，父执行结束时清理活动审计并标记未完成记录。真正取消执行依赖 Runtime 上下文传播，审计状态本身不等于进程取消机制。

返回父 Agent 的是子任务结果和审计标识；主对话仍由父 Agent 继续组织最终答复。子 Agent 不是具有独立调度器和永久记忆的另一套后台任务系统。
