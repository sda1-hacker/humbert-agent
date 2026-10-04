# Humbert Agent 非 RAG 代码评审与修改规划

评审日期：2026-10-04。基线提交：`3d01afd`。本文保留修改前的分析、复现和规划；后续代码修复及最新验证结果见 [修改说明](2026-10-04-implementation.md)。下文的缺陷和失败输出描述该基线，不代表修复后的状态。

## 1. 结论

**建议保留当前总体架构，先修复安全边界和运行生命周期的具体问题。没有依据要求重写项目或大规模重构。**

目前已有明确的组合根、领域服务、Wails 适配层、统一工具授权、会话事实存储、上下文压缩和任务复用机制。这些基础是合理的。主要不足集中在几处边界之间的衔接：受保护数据的派生副本、已解析快照的资源生命周期、终态事件与占用释放顺序，以及失败重试对工具副作用的处理。

本次通过临时测试复现了四个问题；另发现一个浏览器网络防护缺口和一组文档问题。既有测试通过，说明既有用例的行为稳定，但没有覆盖这些新增场景。

| 编号 | 优先级 | 问题 | 证据强度 | 建议 |
| --- | --- | --- | --- | --- |
| F1 | P1 | Standard 沙箱允许读取敏感搜索缓存和其他 Agent 的浏览器资料 | 人工路径探针复现 | 优先补全硬保护规则 |
| F2 | P1 | 失败任务在已有工具调用后仍自动重试 | 重试决策探针复现；未执行真实外部副作用 | 先限制安全重试范围 |
| F3 | P2 | Turn 完成事件先于会话占用释放 | 同步事件订阅探针复现 | 收敛终态处理顺序 |
| F4 | P2 | 修改 MCP 显示名称会关闭已有快照引用的连接 | 真实本机 MCP HTTP 服务复现 | 区分显示更新与连接更新 |
| F5 | P1，待补集成验证 | browser 的 DNS 校验没有绑定实际连接 IP，且只挂接一个页面 Target | 源码路径确认；未做真实攻击演练 | 增强浏览器网络边界 |
| F6 | P2 | 安全说明与实际命令权限不一致，架构链接目标缺失 | 文件和代码对照确认 | 修正文档并补链接检查 |

P1 表示应尽早处理的安全或副作用风险，不等于已经发生数据泄漏。P2 表示明确的正确性、可用性或文档契约问题。可选改进单独列在后文，不与缺陷混为一谈。

## 2. 范围、方法和验证结果

### 2.1 范围

- 排除 `internal/rag/**` 的实现分析和测试目标；不评价向量检索、切分、Embedding、重排等 RAG 设计。
- 评审桌面与数据 CLI 入口、应用装配、Runtime、ContextEngine、会话与附件、工具、权限/审批/沙箱、模型、Skills、MCP、协作、任务、主动执行、备份、搜索投影、前端状态与构建。
- `internal/searchindex` 在这里作为会话/文件的 SQLite 搜索投影检查；这不涉及被排除的 RAG 子系统。
- 按模块关系、重要调用链和故障路径进行检查，并对关键结论补充验证；没有逐行审阅约九万行全部实现，也没有完成每个平台的 GUI 和真实模型端到端审计。

统计仅用于理解维护规模，不作为代码质量评分：非 RAG `internal` 下有 211 个 Go 实现文件，约 58,111 行；111 个 Go 测试文件。`frontend/src` 下有 132 个 JS/Vue/TS 文件，约 32,136 行，包含测试和翻译资源。不统计生成 bindings、依赖目录或 RAG。

### 2.2 已执行检查

| 检查 | 结果 | 限制 |
| --- | --- | --- |
| 显式列出非 RAG 包执行 Go 测试 | 通过 | 首轮大量使用已有 Go 测试缓存 |
| Runtime、Tasks、Proactive、Transcript、Sessions、MCP、Usecases、ContextEngine、Permission、Tools、Skills、DataBackup、Models、WebSearch 关键包使用 `-count=1` 重跑 | 通过 | MCP Adapter 首次因执行环境禁止监听本机端口失败，授权在沙箱外重跑后通过 |
| 同一非 RAG 包范围执行 `go vet` | 通过 | 静态检查不能发现所有事件顺序或副作用问题 |
| `frontend` 的 `npm test` | 82 项通过 | 主要覆盖工具函数和状态流程，不等于桌面交互全覆盖 |
| `frontend` 的 `npm run build` | 通过 | 使用现有安装依赖，未执行一次新的 `npm ci` |
| 四个补充缺陷探针 | 四个预期安全/正确性断言均失败，复现 F1–F4 | 使用 Go overlay；没有向产品目录添加测试源码；没有使用真实会话、凭据或外部模型 |

构建/测试还出现了两类非致命提示：Sonic 在当前 Go 1.27 环境回退到 `encoding/json`；macOS 链接目标版本与部分对象文件版本不同。本次没有证据把它们认定为功能缺陷，发版时应纳入工具链和最低系统版本核对。

未执行真实供应商模型调用、真实浏览器 DNS rebinding/弹窗攻击、Windows/Linux 原生沙箱实测、桌面 GUI 操作和发版打包。后文不会把这些未执行项写成已验证。

### 2.3 复现材料

补充探针保存在 [probes](2026-10-04-probes/README.md)。它们是评审材料，扩展名为 `.go.txt`，不进入常规 Go 包。通过 overlay 注入目标包运行，源码不被覆盖。

在上述基线上运行 `python3 docs/reviews/2026-10-04-probes/run_probes.py` 可检查 F1、F2、F3；加 `--mcp` 检查 F4，需允许监听本机临时端口。修改前基线返回非零退出码是预期现象：这些断言描述建议达到的行为。修复后的常规验证使用各包的正式回归测试，具体用例见修改说明。

## 3. 当前架构中值得保留的部分

### 3.1 分层和依赖注入

`cmd/desktop` 拥有进程入口和 Wails 窗口；`internal/app` 负责构造与关闭；`internal/services` 接收明确 Dependencies，负责桌面 DTO 与事件桥；跨域规则集中在 `internal/usecases`。领域服务没有把 Vue 页面状态当作业务事实。

这一划分已经足够支撑当前桌面 Agent。继续新增 Repository、Facade、Manager 等泛化层并不会自动改善它。新增抽象应围绕实际资源生命周期或明确的业务规则。

### 3.2 消息事实和 UI 状态分离

`session.jsonl` 保存消息、thinking、ToolCall、ToolResult 与压缩检查点；SQLite 保存会话标题、归档与列表信息；实时 delta 通过事件送到前端。前端 Store 和搜索索引都是投影。

Transcript 已有逐文件锁、缓存、位置 sidecar、尾部修复和分支一致性检查。长会话也有分页和按字节位置读取。没有理由把所有数据迁到单一数据库，或把 UI 事件重新当作消息事实。

### 3.3 统一执行和工具保护

普通聊天和 Agent 任务共用 Runtime；Builtin、MCP、模块工具通过 Guard 统一授权；Ask 使用 Eino interrupt/checkpoint，恢复执行原参数。模型、摘要和视觉调用都进入统一计账边界。

权限 Allow 与路径/进程限制分离，也是正确方向。F1 和 F5 是具体边界覆盖不完整，并不意味着这套分层本身应被替换。

### 3.4 扩展与生命周期

Skills 使用内容快照和按需披露；模块通过 `component.Provider` 贡献工具；MCP 使用适配层；子 Agent 沿用父运行的工作区与安全身份。App 清理按停止生产者、收敛 Runtime、释放资源分阶段执行，启动失败也复用清理清单。

这些设计应继续保留。F4 需要补足连接的持有和退休机制，不需要另建第二套 Runtime。

## 4. 具体问题与修改设计

### F1：敏感数据的缓存副本没有进入硬保护规则

**位置：** `internal/sandbox/manager.go:119`、`:176`；`internal/searchindex/service.go:31`；`internal/tools/builtin/sandbox_fs.go`。

Standard 模式为整个用户 Home 添加 READ_ONLY 规则，同时通过 BLOCKED 规则保护 Humbert 的 `secrets`、`config`、`agents`、`mcp`、`logs` 等目录。但是没有保护 `cache` 下的敏感内容。

搜索服务把会话正文汇总到 `cache/conversation-search.sqlite`，把不同工作区文档汇总到 `cache/document-search.sqlite`。浏览器把各 Agent 的站点资料放在 `cache/browser-profiles/<agent-id>`。数据虽然可重建或可以清理，其隐私敏感程度并不降低。

**复现：** 在临时人工 Home 中构造与生产相同的硬保护规则，再加入 Standard Home READ_ONLY。`agents/.../session.jsonl` 被拒绝，而两个搜索数据库、会话搜索 WAL 和另一个 Agent 的浏览器 History 路径均得到 READ_ONLY 裁决。

**实际影响：** 原始 Transcript 的保护没有覆盖正文的搜索副本；当前 Agent 的通用文件/命令能力可能访问其他会话、工作区或 Agent 的资料。浏览器管理器按 Agent 隔离 Profile，不能阻止其他文件能力直接读取其路径。探针没有解密或读取真实 Cookie，因此不把“Cookie 可被成功解密”作为已验证结论。

**建议修改：**

1. 优先把 `<appHome>/cache` 整体加入非 FullAccess 模式的 BLOCKED 规则，保护现有和未来缓存、SQLite 的 `-wal/-shm`、浏览器子目录。App 自身的 UI/索引/浏览器服务继续使用受信接口访问。
2. 如果某类缓存确实要开放给 Agent，再设计窄的领域工具返回必要信息；不要开放底层整份 SQLite 或浏览器目录。
3. 当前 `Config.Paths.CacheDir` 从应用数据根派生，并没有发现独立的 `cache_dir` 用户配置。规则应使用实际应用数据根，覆盖用户自定义 Home；未来若允许缓存根单独配置，再由组合根传入真实敏感路径。
4. 沿现有路径规则编译到 Seatbelt/Bubblewrap，不能只在某个 `read_file` 实现上做字符串判断。
5. 检查 Workspace/AdditionalWrite/MCP 路径授权不能重新授予该受保护根。FullAccess 保持用户显式选择的语义，不在本次中偷偷改变。

**验收：** 两个数据库及 WAL/SHM、其他 Agent 的浏览器目录、符号链接别名均被拒绝；普通 Home 文件仍可读；正常 UI 搜索、截图和浏览器资料复用不受影响。对默认和自定义应用数据根都验证。不要为验证而读取用户真实数据。

**成本与风险：** 小到中等。规则变更范围较小，但需要验证原生沙箱拒绝路径以及后台服务正常访问。无需数据迁移，也无需删除现有缓存。

### F2：自动重试没有区分已产生副作用的失败

**位置：** `internal/tasks/manager_events.go:157`、`:182`；`internal/tasks/manager_schedule.go:startRun`。

`maybeRetry` 仅依据 failed/timed_out、Task 是否 active、Attempt 是否达到 MaxAttempts 创建新 Run。它不检查工具是否执行过、工具性质、失败阶段或结果是否确定。重试 Run 随后重新启动整个提示词。

**复现：** 为一个 active Task 配置 `MaxAttempts=2`，创建 `ToolCalls=1` 的 failed Run。`maybeRetry` 创建了 Attempt 2 的 queued Run。这里验证的是缺少安全重试判断，不是用真实服务演示重复发消息或修改文件。

**触发条件：** 用户启用了多次尝试。默认只有一次尝试时，不会触发这个问题。典型风险是文件修改或远程 MCP 写操作已经完成，随后模型请求失败或整轮超时，新 Run 可能重复操作。`ParentRunID` 去重只能避免创建重复的重试记录，不能保证工具副作用幂等。

**建议分两步修改：**

1. 第一阶段采取保守规则：仅允许确认尚未执行真实工具的启动/模型失败自动重试；一旦已开始工具调用且没有可验证的安全性证明，停止自动重放，显示“存在已执行或结果未知的操作，需要检查后重试”。这会少重试一些只读场景，但能快速限制风险。
2. 工具拒绝或等待审批与真实执行要区分。现有 `EventToolStarted` 计数不等于已发生副作用，可作为保守拦截信号，不能作为最终效果证明。
3. 后续在 Descriptor/运行记录增加明确的执行语义：只读、幂等、可能产生非幂等副作用；以及 effect 为 none/committed/unknown 的状态。判断应来自受信工具实现，不能无条件相信远端 MCP 的描述或 annotation。
4. 非幂等调用在调用前记录 attempt，在返回后记录结果。发生进程退出或网络断开时保留 unknown；不把 unknown 当作“没执行”。外部系统支持幂等键时才使用稳定操作键安全恢复；换一个 RunID 不构成幂等保障。
5. `recoverRetries` 必须使用同一判断规则，防止重启后把原本被阻止的重试重新入队。
6. 如果未来保留“重新执行整个任务”的高级选项，应作为用户明确选择，与安全默认重试区分；UI 展示可能重复的操作和已有结果。

**验收：** 纯模型失败可重试；工具完成后模型失败不自动重放；超时且结果未知不重放；被明确拒绝的工具不被伪装成成功；重复重启不会创建额外 retry；真正幂等的工具可在有稳定键和证明时恢复。

**成本与风险：** 保守规则为中等工作量，完整效果记录为较大工作量。先提交保守修复，避免一次引入复杂“恰好一次执行”框架。外部副作用通常无法靠本地事务保证恰好一次。

### F3：Turn 完成事件发布时，Session 仍被旧 Turn 占用

**位置：** `internal/runtime/runs.go:176`、`:200`、`:203`；`internal/eventbus/bus.go`；`internal/tasks/manager_events.go`。

`completeTurn` 在维护完成后先发布 `EventTurnCompleted`，然后记录日志，最后调用 `cleanupRun`。后者才删除 `activeByRequest`、释放 `activeBySession`，并通知子运行生命周期观察者。

EventBus 同步调用订阅者。因此订阅者收到“完成”时，会话还处于 busy 状态。Tasks 的终态处理会释放自身 active 标记并启动下一次调度；Continuous 模式复用 Session 时，新的调度可能与 Runtime 的清理竞争。前端 IPC 通常晚于同步回调到达，不能据此说每次用户继续聊天都会失败。

**复现：** 构造一次人工 activeRun，在完成事件的同步回调里预约下一轮。回调得到 `当前 Session 已有正在执行的 Turn: request_id=request`。这个结果确定地证明事件和 reservation 状态不一致；自动任务实际触发频率还需要后续集成测试测量。

**建议修改：**

1. 明确终态事件契约：收到 completed/failed/cancelled 时，该 Turn 的必要持久化已结束，旧运行不能继续写会话，同一 Session 已可接受下一轮。
2. 收敛为一个内部终态收尾流程，统一成功、执行失败、取消、审批等待时取消等出口。按顺序完成必要写入、取消和回收相关资源、释放占用，再发布终态。具体函数名不是重点，避免各出口继续复制不同顺序。
3. 在同一个临界区内删除本次运行的索引和 reservation，保留现有指针/RequestID 校验，确保旧运行的清理不能删除新运行的占用。
4. 在宣布可继续前收敛仍能产生副作用的子工作。不能只提前 `delete(activeBySession)` 就放行下一轮，而让旧工具继续运行。
5. 保留 `Close` 的 Worker 等待和拒绝新工作机制。Session 可用与整个进程所有 goroutine 已退出是不同概念，需要清晰边界。
6. 不把 EventBus 改成任意异步派发来隐藏这个窗口。事件顺序属于发布者的生命周期契约；随机调度不能提供正确性。

**验收：** 终态回调可立即预约下一轮；旧清理不会影响新 reservation；Continuous 任务连续运行不因旧占用失败；终态后无旧消息追加；审批/取消/关闭仍能收敛。同一终态只发布一次。

**成本与风险：** 中等。改动集中在 Runtime，但会影响 Tasks、Proactive、UI 和子 Agent 对终态的解释。应先补状态机回归用例，再调整顺序。

### F4：MCP 的配置失效机制破坏了已冻结的运行快照

**位置：** `internal/mcp/manager.go:302`、`:321`；`internal/mcp/einoadapter/backend.go:105`、`:209`、`:230`、`:336`；`internal/mcp/fingerprint.go`。

`Manager.Update` 无条件调用 `invalidateRuntimeSession`，即使只修改显示名称。Adapter 的失效流程直接移除并关闭旧连接；之前解析出的 Eino MCP Tool 仍持有那个连接，所以本轮的后续工具调用失败。

这与“本轮配置冻结，新配置影响下一轮”的项目规范冲突。已有 `ServerFingerprint` 明确排除了 Display Name，说明安全身份变化与显示变化本来就可区分，但 Update 没有利用这一区别控制连接失效。

**复现：** 启动一个真实的本机 MCP Streamable HTTP echo 服务，解析一个工具并成功调用；保持端点和传输配置不变，仅把 Name 从 Before 改为 After；再次使用同一快照调用，得到 `official mcp session is closed`。

**建议分两步修改：**

1. 先修复纯显示更新：比较连接/安全身份字段，Name 或普通展示状态变化不关闭 Runtime 连接。UI revision 可以更新；Schema/catalog 是否刷新按其实际依赖决定。
2. 对真实连接配置更新，引入明确的运行持有关系：解析快照取得 lease，旧连接变成 retired，不接收新的解析；当前使用者释放后才 Close。新 Turn 使用新配置连接。
3. Resolver、构建失败、部分工具解析失败、正常完成、取消和 App Close 都必须释放 lease，避免只解决中断却产生连接泄漏。
4. 配置编辑和权限撤销要分别定义。端点切换可以按冻结快照完成旧 Turn；用户明确删除/禁用能力或撤销授权时，如产品希望立即生效，应主动取消受影响运行，并给出明确原因。不要把安全撤销伪装成随机网络错误。
5. 不在旧 Tool 调用失败时偷偷解析新 Server 或自动重试写操作。这样会把旧快照的工具名称映射到不同外部身份，并与 F2 叠加。

**验收：** 修改 Name 不影响原工具；连接变更后新 Turn 使用新端点；按所选契约，旧 Turn 稳定完成或明确被取消；旧 lease 全部释放后连接关闭；部分 Resolve 失败和关闭竞争无泄漏。HTTP 与 stdio 都应覆盖生命周期。

**成本与风险：** 纯显示修复较小；完整 lease 管理为中等。应复用现有 Backend/generation 机制，不创建第二套连接管理层。

### F5：browser 的公网校验缺少与实际连接绑定的保证

**位置：** `internal/tools/builtin/browser_tool.go:369`、`:464`、`:576`、`:594`；对照 `internal/tools/builtin/webfetch_tool.go` 的 `publicWebDialer`。

当前 URL 校验调用 `resolvePublicIPs`，但丢弃返回 IP。随后 Chrome 自行建立连接；暂停请求通过 `Fetch.continueRequest` 放行。校验时获得的地址没有用于实际 socket 连接，因此校验与连接之间仍存在 DNS 结果变化的窗口。

此外，启动过程从 `/json/list` 选择第一个 page Target，只在该 Target 上启用 Fetch。没有发现对新页面、弹窗、worker 等 Target 的自动挂接。不能把当前页面的 `Fetch.enable("*")` 理解成 Chrome 进程全部网络已经受控。

**证据边界：** 这是代码确认的控制覆盖缺口，本次没有运行真实 DNS rebinding 或 worker 访问内网攻击。Chrome 自身缓存/协议限制可能影响具体攻击能否成立，需要受控集成验证。现有 `webfetch` 的 dialer 已将校验出的 IP 用于连接，browser 路径没有同样保证。

**建议修改：**

1. 让网络边界进入真正的出站连接层。可在本机为受控浏览器提供专用代理：代理解析目标、拒绝非公网 IP，并直接连接同一份通过校验的 IP；HTTP 和 CONNECT 都遵循同一规则。可以复用现有公网地址判断，不能直接用一次性文本抓取替代整个浏览器网络。
2. 浏览器代理不得配置 DIRECT 回退；仔细处理隐式 localhost/link-local bypass。Chromium 官方说明 HTTP 代理由代理端解析目标，并存在隐式本地地址绕过规则，因此代理配置本身也必须经过拒绝路径测试。[Chromium 代理说明](https://chromium.googlesource.com/chromium/src/+/HEAD/net/docs/proxy.md)
3. 将 CDP 控制提升到明确的 Target 管理，挂接新页面/相关 worker，必要时先暂停新 Target，安装控制后再继续。官方 `Target.setAutoAttach` 说明相关 Target 及递归挂接需要显式处理；`Fetch.continueRequest` 负责继续请求，不提供指定目标 IP 的参数。[Target 协议](https://github.com/ChromeDevTools/devtools-protocol/blob/master/pdl/domains/Target.pdl)、[Fetch 协议](https://github.com/ChromeDevTools/devtools-protocol/blob/master/pdl/domains/Fetch.pdl)
4. 代理方案还要列出 HTTP、HTTPS、WebSocket、Service Worker、QUIC/UDP 等实际范围。测试证明前不能宣称所有流量都受控；当前不支持的旁路协议应明确禁止。CDP 挂接帮助动作和生命周期控制，不能代替出站连接限制。
5. 暂停请求校验目前每个请求创建 goroutine。增加有界并发/队列，随 Browser Session 取消；队列满、DNS 超时、代理不可用、CDP 断开时明确拒绝，防止恶意页面耗尽校验资源。
6. 保持浏览器资料按 Agent 隔离，并配合 F1 保护 Profile。网络防护与磁盘资料保护是两个独立验收项。

**验收：** 重定向到内网、解析结果变化、多 IP 混合、IPv6、localhost 别名、弹窗、iframe/worker、WebSocket、代理关闭都不能旁路；普通 HTTPS、截图和用户手动站点操作正常；大量子请求不会产生无界校验工作。测试用本机人工服务与可注入 Resolver，不触碰真实内网服务。

**成本与风险：** 较大。建议先做受控验证和小原型，确认跨平台浏览器行为后再落地。若暂时无法保证完整公网限制，应在 browser 能力说明中明确当前范围；仅增加第二次 DNS 校验不能消除两次解析之间的竞态。

### F6：安全说明和架构链接需要与代码同步

**位置：** 根 `README.md:71`；`internal/tools/builtin/run_command.go:31`；`internal/sandbox/README.md`；多个模块 README。

根 README 描述 `run_command` 工作区只读、解释器写入被阻止。当前工具描述和 Sandbox 设计却允许在授权目录写入；只读派生模式用于 Git/Skill 等特定路径。用户不能同时按这两种语义理解安全边界。

多个模块和 DEVELOPMENT.md 链接到 `docs/architecture/README.md`、`domain-boundaries.md`、`code-reading-guide.md`、`eino-integration.md`，但这些目标不在当前工作树/版本记录中。扫描现有非 RAG Markdown 得到 46 处本地目标不存在的链接引用，其中包含同一目标的重复引用；这不表示缺少 46 篇独立文档。

**建议修改：**

1. 根 README 按当前代码写清 Standard/Strict/FullAccess 的文件与网络边界，以及 Permission、PathGuard、原生隔离各自作用。特别写明工作区内的修改能力。
2. 将架构入口恢复为代码地图、事实来源和核心生命周期说明，链接已有领域 README；不把每个 README 再复制一遍。
3. 清理不存在的文件名和过期职责说明。例如 ContextArtifact/Tools 文档应一致说明结果归档发生在哪一层。
4. 加入轻量的本地 Markdown 链接检查；允许外部 URL、锚点、图片和合法生成目标，按仓库实际情况配置。它应检查路径是否存在，不为简单文档修改另建复杂测试框架。

**验收：** 安全说明与实际工具行为对应；现有本地架构链接全部能打开；阅读路径可以从入口一路到事件、持久化与测试；文档明确哪些平台未实测。

**成本与风险：** 小到中等。属于值得修正的契约和可维护性问题，不要求改变已有工具能力来迎合过期文字。

## 5. 各模块的当前方式与调整判断

这一节提供本次规划需要的设计地图。它不替代完整设计说明书，但说明各块采用的方式、依赖和是否需要修改。未发现明确问题表示本次范围内没有足够证据要求调整，不是对模块作无缺陷保证。

### 5.1 入口、装配和桌面接口

| 模块 | 当前设计和关键内容 | 判断与修改边界 |
| --- | --- | --- |
| `cmd/desktop` | Wails 3 桌面入口；启动前处理待备份/恢复计划；创建 App 与窗口；关闭时调用受控清理 | 保留。发版补最低 OS 与工具链验证；不要把领域逻辑移到 main |
| `cmd/data` | 离线备份、验证、恢复命令；避免对运行中 Store 直接热替换 | 保留。恢复失败回退和凭据导入应继续作为核心测试 |
| `internal/app` | 组合根；显式构造依赖、注册工具和模块、装配事件记者；生命周期区分停止生产、等待运行、释放资源 | 保留；F1 的实际保护根、F4 的资源关闭需通过这里核对 |
| `internal/services` | Wails 的 DTO/错误与事件边界；调用领域服务/Usecase；不承担 JSONL 事实存储 | 保留；任何新增 retry blocked 状态与撤销错误需要同步 DTO、事件和前端 |
| `internal/usecases` | Agent 生命周期、Skill 维护、MCP 配置、视觉等跨域协调 | 保留；MCP 更新与撤销的语义可以在现有跨域路径协调，不再建总控层 |
| `internal/component` | Host 提供平台资源；Provider 分 Selection/Describe/Resolve，贡献原生 Eino 工具；Module 有 Start/Stop/Close | 接入契约已经清楚；动态资源持有如需扩展，围绕 lease 小幅增加，不扩大 Host 为服务定位器 |

主要技术基线来自锁定文件：Go 1.27.1、Eino 0.9.19、Wails 3 beta、MCP Go SDK、Vue 3、Pinia、Vite、Arco 和 Tailwind。它们是当前工作树采用的版本，不代表本次建议升级到的最新版本。没有发现必须先升级框架才能修复 F1–F4 的证据。

### 5.2 执行、上下文和模型

| 模块 | 当前设计和关键内容 | 判断与修改边界 |
| --- | --- | --- |
| `internal/runtime` | Session reservation；Resolver 冻结模型/工具/权限/工作区；Eino Executor；审批恢复、计账、事件与 Turn 后维护 | 优先 F3；同时让 F4 lease 跟随 Snapshot 生命周期释放。保持一个聊天/任务执行入口 |
| `internal/contextengine` | 从 Transcript 检查点与尾部投影窗口；ToolCall 修补、Reduction、Eino Summarization；软阈值摘要、硬阈值拒绝；ExpectedLeafID 防分支变化 | 保留；当前已经有摘要失败与预算超限处理。可选改进是按模型校准和行为评测，不能用新摘要框架替换已有机制 |
| `internal/models` | Provider/Model JSON 控制面；密钥外置；Registry 校验引用和能力并构造 Eino 实例；revision 支撑本轮快照 | 保留；能力由本地配置声明，连接测试不等于工具/视觉能力全验证。建议代表性真实模型场景抽查 |
| `internal/multimodal` | 图片回放策略和视觉桥；主模型能力不足时使用已配置辅助视觉模型；输出回到同一运行边界 | 保留；验证图片原件、大小限制、取消和辅助模型费用，避免引入独立消息事实 |
| `internal/contextartifact` | Session 范围的大工具结果归档；资源 ID/分页回查；由 Reduction 控制预览 | 保留；归档属于敏感会话数据，不能为便捷提供绕过 Scope 的全局读接口 |

当前上下文策略的重要取舍是：保留原始 JSONL，摘要只成为模型窗口的检查点。压缩失败或取消不会“修复”成成功，也不盲目重放缺失的工具结果。这些行为应继续保持。

### 5.3 事实、文件与配置

| 模块 | 当前设计和关键内容 | 判断与修改边界 |
| --- | --- | --- |
| `internal/agents` | Agent 配置、模型角色、能力选择和工作区引用；持久化由 Store 拥有 | 保留；删除与引用校验继续交给已有 Usecase，避免页面自行级联 |
| `internal/sessions` | 会话目录、元数据 SQLite、附件原件与消息服务；承担消息/附件协调 | 保留；F3 变更时检查追加事实已完成。附件路径不能仅靠前端隐藏保护 |
| `internal/transcript` | JSONL 事实、稳定 Entry/ToolCall ID、逐文件锁、缓存、位置/历史索引、尾部修复、分支一致性 | 保留。索引是投影，错误恢复不应删除唯一消息事实；无需全迁到 SQLite |
| `internal/workspace` | Managed/Custom 归属；规范化相对路径、受控根句柄；Session 冻结 CWD；仅 Managed 可由应用级联删除 | 保留；修改工作区设置不能重写旧会话 CWD，安全回归继续覆盖符号链接和归属 |
| `internal/workspaceview` | 面向文件树/预览的受限服务与查询投影 | 保留；展示范围不等于工具安全范围 |
| `internal/searchindex` | 两份 SQLite WAL 数据库、FTS5、流式索引与增量 revision；UI 查询旧提交快照，后台刷新 | 实现方式可保留；必须按 F1 保护派生私有正文。这里不评价 RAG |
| `internal/config`、`preferences` | Viper/YAML 管启动配置与数据布局；领域动态配置分散在各自 Store；显式保存偏好 | 保留；不要为一次修复集中搬回单个大 config。涉及根路径时用解析后的真实值 |
| `internal/credential` | 系统凭据库与受控索引；Secrets 与普通配置分离；支持导出/导入边界 | 保留；不把密钥写进快照 revision、探针、日志或普通 DTO |
| `internal/databackup` | 带清单归档、路径校验、加密、暂存切换与回退目录；启动前执行计划 | 保留；外部 Custom Workspace 不应被误说成已备份。新增持久化字段需核对备份边界 |

### 5.4 安全、工具和扩展

| 模块 | 当前设计和关键内容 | 判断与修改边界 |
| --- | --- | --- |
| `internal/permission` | Allow/Ask/Deny、风险和身份匹配；长期规则按工具/Server 身份约束 | 保留；它回答“能否执行”，并不替代路径或网络限制 |
| `internal/approval` | 审批记录、超时与错误；Eino checkpoint 保存中断恢复状态 | 保留；不建议把 checkpoint 直接改成可跨重启盲目重放。恢复必须面对副作用 unknown |
| `internal/sandbox` | Profile/PathRule/NetworkMode；PathGuard；进程策略；macOS Seatbelt/Linux Bubblewrap 等平台适配与失败拒绝路径 | 优先 F1；平台实际覆盖仍需发版验证。不能把“有配置字段”直接写成每平台能力完全相同 |
| `internal/tools` | Factory/Descriptor/Registry；Scope 冻结身份；Guard 授权；Eino Reduction 归档结果 | 保留；F2 第二阶段可增加受信执行语义，但不把所有 Guard 逻辑变成任务调度器 |
| `internal/tools/builtin` | 文件安全 Backend、原子写/事务、patch、命令、Git、网络、浏览器、Skill、任务与协作工具 | 多数无需结构改造；browser 处理 F5，run_command 文档处理 F6。复合目标继续由多工具组合 |
| `internal/skills` | 发现/解析/安装/更新、来源诊断；内容快照和按需披露；脚本沿已有沙箱 | 保留；外部 Skill 内容不成为新的授权来源；更新影响下一轮快照 |
| `internal/mcp`、`einoadapter` | Server 控制面、catalog、安全指纹、generation、HTTP/stdio 连接与 Eino adapter | 优先 F4。Schema 发现、传输连接与正在运行的工具引用需要不同失效语义 |
| `internal/collaboration` | `run_agent` 同步等待；目标自己的模型/指令/工具，继承父工作区和安全身份；轻量审计和父结束收敛 | 保留；没有需求时不改成后台多 Agent 调度，不开放递归调用扩大权限 |
| `internal/websearch`、`documenttext` | 供应商适配搜索；受限格式提取成文本/Markdown，为工具与索引提供输入 | 保留；继续把外部内容当数据，并维护大小、时间和格式边界 |

### 5.5 调度、基础设施和前端

| 模块 | 当前设计和关键内容 | 判断与修改边界 |
| --- | --- | --- |
| `internal/tasks` | 持久化 Task/Run；调度/限额/尝试次数；新建或连续 Session；运行进入统一 Runtime | 优先 F2，集成检查 F3；TaskRun 的状态不能掩盖已执行工具的 unknown 效果 |
| `internal/proactive` | 持久化 Inbox/Record；EventKey 去重、Quiet Hours、Heartbeat/工作区快照；Agent 动作通过 Tasks；后台收尾避免事件锁重入 | 保留。已有恢复与快照不完整语义，不应泛称“主动任务没有去重”；F2 状态变更需同步记录原因 |
| `internal/eventbus` | 本机同步发布/订阅和异常隔离；没有外部队列 | 保留；F3 修发布顺序，订阅回调避免锁重入和长阻塞 |
| `internal/logging`、`notifications` | 结构化、限长/脱敏诊断；本地通知执行器 | 保留；已有可观测性基础，优先补“为什么不重试/被撤销”的明确原因 |
| `internal/atomicfile`、`commandenv`、`instancelock`、`avatar` | 原子小配置写入、进程环境约束、单实例锁与头像资源辅助 | 本次未发现必须调整的具体缺陷；无需泛化成大基础框架 |
| `frontend/src/api` | `Call.ByName` 封装 Wails 方法；生成 bindings 为可选维护方式 | 可选增加 DTO/事件类型检查，先覆盖高风险边界 |
| `frontend/src/stores`、`runtime/projections` | Pinia 状态；RequestID/SessionID 校正流事件；终态回读 Session；工具轨迹从真实记录投影 | 保留；F2/F3/F4 涉及的 blocked/error/terminal 契约需同步验证 |
| `frontend/src/components`、`features`、`i18n` | Vue 页面和组合式逻辑；功能注册与四语言资源；聊天、设置和文件预览 | 有较大组件，但行数不能直接证明缺陷。按实际修改热点拆职责；新增状态补齐四语言 |

## 6. 值得安排，但不应阻塞缺陷修复的改进

### 6.1 前后端契约的静态检查

`frontend/tsconfig.json` 允许 JS，但 `checkJs=false`；API 调用使用字符串方法名。因此 Go DTO/事件字段变更可能直到运行时才暴露。现有 82 项测试有价值，但不能代替所有方法/字段契约检查。

建议从 `src/api` 和 `runtime/projections` 开始补 JSDoc 或小范围 TypeScript，使用共享的 DTO/事件定义检查调用参数和终态字段。可评估直接使用生成 bindings 或给现有 API 包装增加类型。保持一个调用入口，不同时维护两套桥接。无需为了本次评审把全部 Vue 页面改写为 TypeScript。

### 6.2 按真实修改热点拆分大文件

例如 `SkillDetailView.vue` 约 2201 行、`ComposerBar.vue` 约 1767 行、`ConversationSidebar.vue` 约 1483 行，后端也存在较大的 Service/Store。实际维护成本取决于职责数量和变更耦合，不是行数阈值。

下一次修改 Skill 编辑/安装时，将编辑状态、异步安装协调与纯展示分别放入已有 Store、composable 和子组件；聊天输入则分离附件/快捷命令协调与输入呈现。按功能提交，保留 public action 和事件契约。不要同时移动所有大文件并修安全问题，避免评审难以确认行为。

### 6.3 上下文策略的观测与评测

已有 ApproxEstimator 和真实 usage 校准；不足是粗估/校准对不同模型、语言、工具 Schema 和图片输入的适应仍需验证。建议按模型记录压缩前后 token、实际 usage 偏差、摘要失败率和上下文超限次数，继续沿现有脱敏日志，避免写原始内容。

建立少量代表性行为场景：中文长对话、多轮工具、当前用户要求跨摘要保留、Skill 主定义保留、图片和辅助模型、小窗口模型。先用结果决定是否按模型分桶校准；没有测量证据时不引入新 tokenizer、向量记忆或多阶段摘要框架。

### 6.4 固定可重复的工程检查

本次没有发现 `.github` 下的 CI 工作流，不等于所有外部 CI 都不存在。建议把当前可执行的检查固定下来：锁定 Go/Node 基线，干净依赖安装、Go 测试/vet、前端测试/build、本地文档链接、必要的原生沙箱拒绝用例。

真正需要扩大的测试是 F1–F5 的风险场景、生命周期/资源关闭以及 macOS/Linux/Windows 支持差异。无需为了提高数量给简单转发和样式镜像新增测试。真实模型评测、浏览器与桌面 GUI 可以作为带环境条件的集成检查，并如实标注未执行状态。

## 7. 建议的实施顺序与可拆分变更

以下是修改计划，不是已经实施的承诺。工作量采用相对规模，避免把未知的浏览器跨平台问题换算成虚假的精确天数。

| 顺序 | 单独变更的目标 | 主要修改位置 | 验收与交付 | 规模/依赖 |
| --- | --- | --- | --- | --- |
| 1 | F1：补全缓存硬保护 | `sandbox/manager.go`、规则/原生策略测试，必要时 `app/application.go` | 人工缓存、WAL/SHM、Profile、符号链接拒绝；UI 搜索/浏览器正常；更新 Sandbox 文档 | 小到中；可独立 |
| 2 | F2 第一阶段：保守安全重试 | `tasks/manager_events.go`、Run 类型/Store、Runtime 工具状态边界；TaskService 与前端任务状态 | 纯模型失败可重试；工具执行/未知后阻止；重启恢复同规则；UI 原因清晰 | 中；可独立，数据字段变化核对备份 |
| 3 | F3：统一终态收尾契约 | `runtime/runs.go`、执行失败/取消/审批出口、Tasks 连续调度回归 | 终态回调能立即继续；无旧写入/旧清理影响新运行；关闭与审批通过 | 中；为后续 lease 提供可靠释放点 |
| 4 | F4 第一阶段：Name 更新不关闭连接 | `mcp/manager.go`、指纹比较和 Manager/Adapter 测试 | 实际 echo 探针通过；显示更新仍反映到 UI；连接参数变化仍刷新 | 小；可提前并行设计，独立提交 |
| 5 | F4 第二阶段：连接 lease/retire | `mcp/einoadapter/backend.go`、Snapshot/Resolver 和收尾释放 | 老连接按既定契约退出；新轮采用新配置；失败/取消/stdio 无泄漏 | 中；建议在第 3 项之后 |
| 6 | F5：浏览器边界验证与原型 | `browser_tool.go`、出站代理/地址判断小模块、浏览器集成测试 | 受控 DNS/目标切换/旁路测试；确定支持协议与平台；验证拒绝模式 | 大；先验证方案，不能只做字符串补丁 |
| 7 | F6：文档和链接同步 | 根 README、现有模块 README、`docs/architecture`、轻量检查脚本 | 安全说明准确、导航全部可用、当前限制明确 | 小到中；伴随每项更新，最后集中清理 |
| 8 | 可选：类型契约、热点拆分、观测和 CI | 前端 API/projections、实际修改的组件、日志/检查脚本 | 有实际维护收益；不制造全库重写 diff | 逐步，按后续功能安排 |

第 1、2、4 项都可以作为范围很小的修复先完成。浏览器项风险重要，但实现不确定性更高，建议尽早启动验证，同时不要让它阻塞已经可复现的小修复。这里的“并行”指工作安排，不要求引入多 Agent 执行框架。

### 7.1 第一阶段应达到的状态

- 非 FullAccess 工具读不到其他会话/Agent 的敏感缓存副本。
- 任务失败之后，用户可以看到此前操作和阻止重试的原因，不发生无证明的自动重放。
- Runtime 的终态表示该 Session 可以继续；Tasks 的状态与 Runtime 不再竞争旧 reservation。
- MCP 仅改显示名称不会破坏正在进行的聊天。
- 所有变化都有直接覆盖该风险的回归用例；常规测试仍通过。

### 7.2 第二阶段应达到的状态

- MCP 连接更新、权限撤销和运行资源所有权都有明确契约。
- browser 的公网限制在出站连接层得到受控测试证明，覆盖所声明的协议和 Target。
- 文档把事实、投影、授权、原生隔离和平台限制讲清楚；前后端变化有基本契约检查。

### 7.3 数据和兼容性处理

F1/F3 和 Name 更新无需改变用户数据格式。任务效果字段或连接审计若需要持久化，应明确所属 Store、默认 unknown 语义和备份范围。当前 DEVELOPMENT.md 允许未发布试验 schema 同步调整，但本次计划没有理由删除用户会话或整个数据根。

历史 TaskRun 缺少效果证明时，默认按 unknown/不自动重放解释，不能把缺失字段当作“没有执行工具”。缓存保护规则不需要清空缓存；索引损坏的重建流程与安全保护变更也应分别处理。

## 8. 修复后的验证清单

1. 先运行附带探针，确认目标缺陷从失败变为通过；再把合适的用例整理成正式包内测试，探针可以继续作为评审记录。
2. 每项修复只运行关联测试与必要集成检查；完成一阶段后执行非 RAG 的整体测试/vet 和前端测试/build。范围显式列出，避免顺带测试被排除目录。
3. 生命周期变更检查取消、审批等待、超时、构造失败、配置变化与 App Close，不只检查成功。
4. 数据变更检查重启、重复事件、工具结果 unknown、索引重建和备份恢复。依然只用人工临时数据。
5. 涉及原生沙箱/浏览器时执行支持平台的拒绝路径。平台未能测试就保留未验证标记，不把 Go 编译当作安全隔离证明。
6. 桌面人工场景至少包括：普通对话后马上继续、Continuous 任务排队、工具后模型失败、MCP Name 更新、浏览器正常截图、搜索历史、审批取消与关闭应用。

本次常规 Go 检查使用以下显式范围；`go vet` 使用相同包列表。RAG 没有作为测试目标：

```bash
go test ./cmd/... ./examples/... ./frontend \
  ./internal/agents ./internal/app ./internal/approval ./internal/atomicfile \
  ./internal/avatar ./internal/collaboration ./internal/commandenv ./internal/config \
  ./internal/contextartifact ./internal/contextengine ./internal/credential \
  ./internal/databackup ./internal/documenttext ./internal/eventbus \
  ./internal/instancelock ./internal/logging ./internal/mcp/... ./internal/models \
  ./internal/multimodal ./internal/notifications ./internal/permission \
  ./internal/preferences ./internal/proactive ./internal/runtime ./internal/sandbox \
  ./internal/searchindex ./internal/services ./internal/sessions ./internal/skills \
  ./internal/tasks ./internal/tools/... ./internal/transcript ./internal/workspace \
  ./internal/workspaceview ./internal/usecases ./internal/websearch ./internal/component
```

评审环境另外使用 `env -u GOROOT` 以及 `/private/tmp/humbert-review-gocache`、`humbert-review-gotmp` 隔离测试缓存/临时目录；这属于本次环境设置，不是项目运行所必须的配置。

## 9. 目前不建议做的改造

- 不建议重写成微服务、引入外部事件队列或拆成多个数据库服务；当前桌面范围没有对应需求证据。
- 不建议统一搬迁 Transcript、配置与搜索索引。它们分别代表事实、控制面和派生投影。
- 不建议把所有失败自动恢复成重新跑提示词，或把内存 checkpoint 持久化后直接重放工具；优先处理效果 unknown。
- 不建议替换 Eino 上下文适配、自建多级摘要/记忆框架，或为了前端大文件一次全面重写。
- 不建议把子 Agent 同步调用改成自主后台调度，仅为了增加架构复杂度。

项目当前更需要补齐边界之间的行为契约和拒绝/故障路径。按上述顺序完成修改后，再整理完整设计手册，会比依据现状直接把这些问题写成“既定设计”更准确。
