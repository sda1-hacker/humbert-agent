# 12 可选优化与验证边界

## 当前判断

还有可以优化的地方，也新确认了部分构建模板没有同步当前入口的问题；目前没有足够依据继续进行大规模业务重构。上一轮已处理明确的执行生命周期、任务重试、MCP 连接持有与浏览器网络边界问题；这一轮重点建立源码说明和可回查索引，没有继续增加产品抽象层。

跨平台构建入口需要在相应平台发布前修正；其余性能和职责拆分建议按实际负载、故障或维护需求决定。下面区分实现事实、潜在成本和建议，避免为了行数好看把状态所有权拆散。

## 建议顺序

| 优先级 | 项目 | 源码事实 | 何时值得做 | 建议的最小改法 |
| --- | --- | --- | --- | --- |
| 发布前必修 | 构建模板入口 | Linux/Windows 本机与 server/cross Docker 的部分命令编译根目录，但主入口已在 cmd/desktop | 使用对应打包/部署任务前 | 编译目标统一到实际入口，同时调整 Windows syso 生成/清理位置；逐平台验证，不把移动模板直接宣布支持 |
| 较高，下一次相关功能修改时 | 多文件 apply_patch 的部分完成反馈 | 全部预检后逐文件提交，后续 I/O 失败会直接返回 error，前面可能已成功 | 模型/用户无法判断哪些文件实际写入，或遇到真实部分失败 | 在结果/错误中明确已完成文件和失败文件；先补可诊断性，再决定是否需要复杂回滚 |
| 中，先测量 | TaskRun 文件扫描 | 调度和 Session 引用查询反复列任务并读取各 runs 目录 | 运行记录增长后调度/删除/会话查询明显变慢 | 添加可重建的 queued/session 引用索引，保留文件事实和坏文件诊断 |
| 中，先测量 | 模型缓存锁范围 | ResolveSnapshot 未命中时在全局写锁内读取配置、取凭据、创建 SDK 对象 | 多模型并发冷启动或系统钥匙串慢，阻塞其他配置/解析 | 按模型合并初始化，锁外构造，提交前核对 revision；不把 SDK 实例直接暴露给多个写操作 |
| 中，修改接口时同步 | Go/前端方法契约 | API 使用字符串 Call.ByName，常规构建不检查 Go 方法存在及 DTO 字段 | Go 重命名和前端同步经常出错 | 将本次方法名核对纳入轻量检查；需要时再采用生成 binding/类型，避免同时维护两套手写 DTO |
| 低，按职责拆 | ComposerBar 与 Session Store | 当前约 1767/1106 行，承担多种交互/请求状态；ChatView 本身只有 82 行 | 经常修改附件/模型/分页时必须触碰无关状态 | 先提取输入附件生命周期、模型选择或分页请求的一项完整职责，保留单一状态 owner |
| 低，按改动频率拆 | Skill remote_installer | 下载、Git/archive、资源限制、暂存与清理集中实现 | 来源扩展频繁，资源所有权开始难以判断 | 按来源 materialize 与共同包验证边界拆文件；不新增平行安装框架 |
| 低，按真实差异改 | 能力 auto 推断和网页解析 | 模型能力按名称启发式；网页后端依赖外站响应形态 | 特定 Provider/网站出现已复现误判或解析失效 | 保存可脱敏响应样本，补针对性 fixture/覆盖；保留显式能力覆盖和诊断 |

这是实施优先级建议，不是漏洞严重度排名。根目录缺少 Go 文件的问题已用 go list . 复现；尚未对大量任务/多模型冷启动做负载测量，也没有在本轮复现 apply_patch 的磁盘故障。

## 每项如何修改和验证

### 构建入口

实际 main 位于 [cmd/desktop/main.go](../../cmd/desktop/main.go)。[Linux](../../build/linux/Taskfile.yml)、[Windows](../../build/windows/Taskfile.yml)、[公共 server 任务](../../build/Taskfile.yml)、[cross Docker](../../build/docker/Dockerfile.cross)、[server Docker](../../build/docker/Dockerfile.server) 尚有不指定包或指定 `.` 的构建命令；根目录 go list . 返回 no Go files，目标不一致是已确认事实。

最小修复是这些命令统一编译 `./cmd/desktop`，同时将 Windows 资源 syso 生成到该包并更新清理命令。还要检查 Docker 工作目录/复制范围、Wails server tag 下该入口的行为。验证应覆盖命令展开、生产 tag、本机平台编译和资源是否实际嵌入；macOS 桌面测试成功不能替代 Windows/Linux/Docker 发布验证。本轮没有修改发布脚本或执行未具备环境的跨平台打包。

### apply_patch

见 [apply_patch.go](../../internal/tools/builtin/apply_patch.go) 的 prepared 检查与后续提交循环。语法/旧文本不匹配会在写前拒绝；磁盘、取消或外部竞争可能发生在提交期间。当前不能向调用者承诺整体事务。

最小改造应返回明确的 partial 状态、completed_files、failed_file 和原因，并让工具活动展示同一事实。验证要注入“第一项成功、第二项写入失败”及提交间取消，检查磁盘内容和反馈一致；仅测试两文件正常替换不足以验证这个问题。若将来需要回滚，还要定义外部编辑后是否允许覆盖、回滚失败如何显示，不能用简单写回旧字节掩盖更复杂的竞态。

### TaskRun 查询

见 [store.go](../../internal/tasks/store.go) 的 RunBySession、ReferencesBySession、ListDispatchableRuns。已有 taskAgents/runTasks 定位缓存，但上述路径仍重新扫描配置/运行文件。

先用有代表性的 Task/Run 数量测量 wall time、读取次数和锁等待。若成为瓶颈，维护 session→run/task 与 queued 索引，启动重建、写入/删除同步失效，错误文件继续进入 Issue。对比原扫描和索引在创建、改会话、重试、删除、continuous 共享会话、重启以及损坏文件下结果一致。不要一开始就把所有 JSON 文件迁到新数据库。

### 模型初始化

见 [models/runtime.go](../../internal/models/runtime.go) 的 ResolveSnapshot。当前双重检查锁逻辑简单，且初始化不是实际流式推理；不能未经测量就宣称所有模型请求被全局串行化。

若冷启动阻塞成立，可用每 ID 的初始化状态合并同模型请求，在锁外创建对象，再比较配置 revision 决定是否提交。必须处理配置更新/删除与初始化交叉、初始化失败唤醒所有等待者、Context 取消及多模型同时构造。旧 Turn 保持原快照，不得为了清缓存立即破坏运行中的对象。

### 桥接契约与大文件

本次源码接口扫描核对了 117 个不同前端方法调用与 119 个公开业务方法，方法名全部有 Go 声明；它没有证明每个字段的 JSON tag 和前端取值都匹配。未来 DTO 改动宜同时更新 API 和状态流程测试。

组件/Store 拆分应减少一个职责的改动面，不只把函数移到新文件后仍依赖十几个回调。ComposerBar 可按附件队列与释放、模型选择、发送控制分别评估；Session Store 可按分页/搜索请求归属评估。每次只拆一个有明确输入输出的边界，并检查切换会话、迟到响应、删除及发送失败，避免重构损坏已经存在的竞态保护。

公开方法没有前端 API 调用，并不足以证明可以删除。例如 UpdateAgentProfile/SetAgentSkills 仍可能是预留窄命令；删除前必须核查所有 Go 调用方、导出接口、示例及用户功能，而不是只搜索某个 Vue 文件。已经共享的 FilesystemBackend、Resolver、文档提取和跨域 Usecase 值得继续保留，没必要再次复制。

## 不建议当前引入的内容

目前没有必要引入微服务、通用事件持久化平台、第二套任务调度器、全局 DI 容器，或为每个普通数据操作增加一层泛型 Repository。现有单进程桌面形态下，这些变更会扩大状态和错误边界，不能自动解决上表中的具体问题。

同样不应把 Transcript/Search/UI 三种表示强行合并：持久事实、可重建投影和瞬态展示具有不同要求。保留清晰的数据归属，比追求“所有模块都只用一种存储”更有维护价值。

## 验证地图

| 关键边界 | 源码测试入口 | 需要关注的断言 |
| --- | --- | --- |
| 终态资源释放 | [terminal_test.go](../../internal/runtime/terminal_test.go) | 终态收到后可预约同会话，不遗留资源 |
| 限额/子执行统计 | [model_accounting_test.go](../../internal/runtime/model_accounting_test.go) | 模型/子 Agent 共用预算，使用统计对应执行 |
| 安全重试与任务状态 | [manager_test.go](../../internal/tasks/manager_test.go)、[run_updates_test.go](../../internal/tasks/run_updates_test.go) | 已调用工具不重试，旧队列复验、共享会话引用与状态更新 |
| MCP 旧快照持有 | [metadata_update_test.go](../../internal/mcp/einoadapter/metadata_update_test.go) | 更新后旧工具仍可用、释放后关闭、并发关闭只一次 |
| 缓存硬保护 | [cache_protection_test.go](../../internal/sandbox/cache_protection_test.go) | 搜索/浏览器私有副本不能绕过 BLOCKED |
| 浏览器代理 | [browser_proxy_test.go](../../internal/tools/builtin/browser_proxy_test.go)、[browser_tool_test.go](../../internal/tools/builtin/browser_tool_test.go) | 私网拒绝、HTTP/CONNECT/关闭及 Chrome 实际行为 |
| 上下文和 Transcript | [contextengine](../../internal/contextengine)、[transcript](../../internal/transcript) 测试清单见索引 | 工具事务边界、摘要提交叶子、尾修复与分页 |
| 搜索 | [controller_test.go](../../internal/searchindex/controller_test.go)、[document_scan_test.go](../../internal/searchindex/document_scan_test.go) | 后台刷新/关闭、文档限制和截断不误删 |
| 备份/凭据 | [encrypted_test.go](../../internal/databackup/encrypted_test.go)、[schedule_encrypted_test.go](../../internal/databackup/schedule_encrypted_test.go) | 错口令/坏归档拒绝、计划口令、凭据/目录失败补偿 |
| 前端状态 | [frontend/src/utils](../../frontend/src/utils) 下 test.js | 迟到请求/事件、切换资源、审批与发送失败，不能只测渲染成功 |

上一轮修改已执行非 RAG Go 测试、vet、针对性 race、真实 Chrome 相关验证、前端测试及构建。那些结果对应上一轮代码状态；本轮只新增/修正文档，因此没有重复向真实供应商发请求，也没有把文档链接检查当成功能测试。

本轮验证内容包括：所有范围内实现文件都有章节映射、Go 服务方法与前端调用名称对应、DTO/声明提取、源码内容摘要、Markdown 本地链接及 diff 空白检查。索引基于当前工作区，包含未提交代码；以后源码变化需核对章节和摘要。

真实模型流式协议、外部 MCP Server、第三方网页、不同 Chrome/WebView 版本和三平台 NativeSandbox，仍需要对应环境的集成验证。本文是一份源码实现解释与维护建议，没有宣称完成对每一行代码的安全审计或完整平台验收。
