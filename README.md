# Humbert Agent

Humbert Agent 是一个面向个人使用的本地优先桌面 Agent 助手，使用 **Go + Eino** 运行 Agent，使用 **Wails 3 + Vue 3** 提供桌面界面。它把对话、工作区、工具、Skills、MCP 连接器和定时任务放在同一个应用中：用户可以与 Agent 讨论问题，也可以让它读取工作区文件、检索资料、操作网页或按计划执行任务。

项目名称来自作者喜欢的日本民谣组合「ハンバート ハンバート」。项目仍在持续开发中，欢迎通过 Issue 交流使用问题和改进建议。

> **项目状态**：当前版本为 0.1.0，适合开发、体验和个人场景。外部网页、工具和模型输出仍应按不可信输入对待；涉及文件修改、命令执行或网页交互时，请检查授权内容。

## 主要功能

| 领域 | 当前能力 |
| --- | --- |
| 对话与模型 | 多 Agent、多会话；OpenAI、OpenAI 兼容接口和 Ollama；模型能力配置、连接测试与首次使用引导；流式对话和工具调用展示 |
| 附件与图片 | 支持图片，以及文本、源码、PDF、DOCX、XLSX、PPTX 等可提取文本的附件；聊天模型有视觉能力时直接处理图片，否则由配置的视觉模型先生成观察文本，再交给聊天模型完成本轮对话 |
| 上下文与记忆 | 自动压缩长对话、保留最近原始消息、按需回查旧历史；会话派生记忆与用户确认后保存的跨会话个人记忆 |
| 工作区与工具 | 文件浏览和预览、文件读写与检索、补丁、Git 查询、网页搜索与抓取、浏览器操作；本地命令需显式开启并受权限与沙盒控制 |
| 扩展能力 | 按 Agent 配置内置工具、Skills 和 MCP 连接器；可将显式开放的其他 Agent 作为一次性子 Agent 调用 |
| 主动任务 | 一次性、间隔、每日、每周及手动任务；聊天中安排提醒；运行记录、重试、通知、审批与执行限额 |
| 数据管理 | 跨会话正文搜索、工作区文档搜索、会话归档、加密备份与恢复 |
| 语言 | 界面支持简体中文、英语、日语、韩语；可在「设置 → 语言」切换，Agent 的最终回答默认跟随所选语言 |

## 快速开始

### 开发环境

- go.mod 声明 **Go 1.27.1**。请使用能满足该版本要求的 Go 工具链。
- 安装 Node.js、npm 和 Wails 3 CLI（命令名为 wails3）；桌面构建还需要相应平台的 Wails 系统依赖。
- 模型连接需要可用的服务地址及凭据；使用本机 Ollama 时，需要另行运行 Ollama 服务。
- browser 工具需要本机 Google Chrome；可通过 HUMBERT_BROWSER_CHROME_PATH 指定其可执行文件。

在仓库根目录运行：

~~~bash
cd frontend
npm ci
cd ..
wails3 dev
~~~

wails3 dev 会启动桌面应用和前端开发服务。构建桌面程序可运行 wails3 build；平台打包任务定义在根目录的 Taskfile.yml 与 build/ 中。

### 首次使用

首次启动时，Humbert 会在用户主目录创建 ~/.humbert-agent/。如果还没有绑定可用模型的 Agent，界面会引导你：

1. 创建或选择 Provider，配置 API 地址和凭据。
2. 添加模型并设置其工具、视觉等能力。
3. 发送短消息测试文本连接，然后创建默认 Agent。
4. 为 Agent 选择工作区及允许使用的工具、Skills、MCP 连接器。

连接测试可能产生少量模型费用。文本测试通过只说明基础对话可用，工具调用和视觉能力仍取决于所选模型及其配置。已有可用 Agent 时，应用直接进入聊天。

## 日常使用

### 对话、附件与记忆

每个 Agent 可以有多个会话。消息保留用户输入、可见回答、工具调用与结果，便于查看和回溯。聊天支持引用选中的文字或整条消息；“记住”会先让用户编辑确认，再写入可管理的个人记忆。

上传图片时，如果当前聊天模型支持视觉，它直接接收图片并完成本轮对话；否则需要在模型设置中配置一个具备视觉能力的图片模型。图片模型产生受限的观察文本，聊天模型继续负责推理、工具调用和最终回答。普通 UTF-8 文本附件会直接提供给模型；PDF、DOCX、XLSX、PPTX 保留原件和附件编号，Agent 可用 `extract_document` 按需转换成 Markdown，并使用 `offset`、`limit` 分段读取。该工具也可读取 Sandbox 允许的工作区文档，需启用文件工具。单份文档上限 12 MiB，Markdown 提取结果上限 512 KiB；扫描版 PDF 当前没有 OCR。音频、视频尚未形成完整的输入处理链路。

长对话会在需要时追加压缩检查点，并继续保留完整原始记录。构造模型上下文时，使用最近的有效检查点和其后的原始消息；较旧内容可由历史查询工具按需读取。模型的 thinking 会保存用于当前会话展示和排障，但不写进压缩摘要或派生记忆；历史 thinking 不作为一般聊天上下文反复发送。工具调用及结果作为会话事实保留，进入模型上下文时会受到窗口与输出预算约束。

聊天中的「思考完成」等状态由界面根据运行事件显示；展开后看到的思考正文来自模型的原始 `reasoning_content`，并非界面生成。消息按游标分页加载，初次只读取最近 80 条；展开更早消息才继续读取。语言切换不会翻译或改写已经保存的用户消息、模型回答、思考、工具结果和文件内容。模型的原始思考可能使用与界面不同的语言。

### 工作区、搜索与浏览器

Agent 可使用应用管理的工作区，也可指向用户选择的目录。工作区文件只在聊天右侧展示，并跟随当前 Agent：左边是目录树，右边预览选中的文件。文件两栏及右侧面板宽度均可拖动调整。右侧面板占据独立的布局列，不会覆盖聊天；窄窗口打开右侧时自动收起左侧导航。右侧搜索按钮可检索工作区 PDF、DOCX、XLSX、PPTX 的 Markdown 正文；结果提供相对路径与行号。单次搜索最多遍历 5000 个文件、索引 1000 份文档；超过范围时可缩小工作区。左侧栏可搜索跨会话的用户和助手消息，并跳转到对应消息；归档会话不会删除其记录。可在“设置 → 归档会话”集中查看、解除归档或永久删除，也可在侧边栏展开归档会话。

开启 Agent 的 browser 工具后，首次打开网页会启动由 Humbert 管理的独立可见 Chrome 窗口。用户和 Agent 查看、操作的是同一网页。工具支持 open、snapshot、click、type、scroll、press、back、forward、refresh、show、screenshot 和 close；网页截图以原始 PNG 保存到当前会话的 `attachments/`，并直接显示在聊天中。需要将截图保存到工作区或其他 Sandbox 允许的目录时，Agent 再调用独立的 `copy_file` 工具，用附件 ID 作为来源、目标路径作为文件名；同一轮和后续轮次都使用这条流程。模型支持视觉时，截图还会生成可供 Agent 使用的画面观察文本。每个 Agent 有独立的 Chrome 配置目录，保存在 `~/.humbert-agent/cache/browser-profiles/`，重启后仍保留网站 Cookie；不会借用用户日常 Chrome 的登录信息，清空该目录会重置浏览器状态，备份不包含此目录。网站仍可能要求安全验证。工具识别到验证页后会停止自动交互，提示用户在可见窗口手动完成，随后 Agent 可调用 snapshot 继续；Humbert 不自动识别或点击验证码。要求本机安装 Chrome，也可用 `HUMBERT_BROWSER_CHROME_PATH` 指定路径。网页只允许经过公网地址检查的 HTTP/HTTPS 目标；网页内容视为不可信信息。聊天中的普通链接仍使用系统默认浏览器打开。

启用 `run_command` 后可以直接执行常用本地程序，无需维护程序名白名单。调用不经过 Shell，程序与其子进程在原生文件系统沙箱中只能以只读方式访问工作区；临时写入使用每次调用独立的 TMPDIR。直接删除命令和 `find -exec` 等明显破坏性参数会提前拒绝；解释器与子进程造成的写入也由操作系统沙箱阻断。原生文件隔离不可用时，本地命令拒绝执行。具体调用仍由权限设置决定是否询问，文件修改应使用受控文件工具。

定时与手动任务在左侧“任务”页面管理。

### Skills、MCP 与多 Agent

Skills 使用 SKILL.md 定义能力；MCP 连接器支持 stdio 和 Streamable HTTP。Agent Profile 决定其可用的内置工具、Skills 与 MCP 工具，工具执行还会经过权限、工作区和运行时限制。

启用 list_agents / run_agent 后，主 Agent 可以把自包含任务交给其他 Agent。被调用方必须在设置中显式开启“允许作为子 Agent 调用”。子 Agent 使用自己的模型、指令和能力配置，在当前父会话内同步运行；文件、网络和审批边界沿用父运行环境。其最终结果作为工具结果回到主 Agent，由主 Agent 继续整理答复。子运行只有轻量审计记录，不会额外创建侧栏会话。

### 提醒与主动任务

可在任务页面创建手动、一次性或周期任务，也可以在对话中让 Agent 使用 get_current_time 与 schedule_task 安排提醒。通过对话创建任务时，审批卡会展示任务内容、执行方式、时间和时区，确认后才会创建。任务运行可设置时长、模型调用次数、工具调用次数及 Token 限额；运行记录保存状态和结果摘要，执行过程仍记录在对应会话中。

**定时任务依赖 Humbert 进程运行。** 应用关闭期间不会按时执行；重新启动后，错过的运行按任务的错过策略处理。模型报告的 Token 用量用于限额判断，单次请求可能越过阈值；不同供应商的货币费用不统一计算。

## 架构与数据

~~~text
Vue 3 / Pinia / Wails 桌面界面
               │
        Wails Application Services
               │
Go Core：Agent · Session · Runtime/Eino · Context · Tools · Tasks
               │
本地文件、会话元数据 SQLite 与系统凭据库；搜索 SQLite 可重建
~~~

每次 Turn 都固定当时的 Agent、模型、工作区、工具、Skills、MCP、权限与上下文配置，避免运行中配置变化影响正在执行的调用。主要代码位置：

| 路径 | 职责 |
| --- | --- |
| cmd/desktop/、cmd/data/ | 桌面应用入口；离线备份、校验与恢复 CLI |
| internal/app/、internal/services/ | Core 组装与 Wails 服务边界 |
| internal/runtime/、internal/contextengine/ | Eino Turn 执行、模型路由、上下文构造与压缩 |
| internal/agents/、internal/sessions/、internal/transcript/ | Agent 配置、会话与追加式记录 |
| internal/tools/、internal/skills/、internal/mcp/ | 内置工具与扩展能力 |
| internal/tasks/、internal/proactive/ | 任务调度、运行与主动事件 |
| internal/searchindex/、internal/databackup/ | SQLite 搜索投影与加密备份 |
| frontend/ | Vue 3 桌面界面与 Wails bindings |

第一次阅读代码可从 [架构手册](docs/architecture/README.md) 进入各包的详细说明，并用 [代码阅读导引](docs/architecture/code-reading-guide.md) 跟踪一次聊天；设计约束见 [领域边界文档](docs/architecture/domain-boundaries.md)。

### 默认数据目录

~~~text
~/.humbert-agent/
├── config.yaml                     # 启动配置
├── config/
│   ├── providers.json              # Provider 非敏感元数据
│   ├── models.json                 # 模型与图片模型路由
│   ├── preferences.json            # 用户偏好
│   ├── permissions.json            # 权限决策
│   ├── personal-memory.json        # 用户确认的跨会话记忆
│   └── proactive.json              # 主动助手状态
├── secrets/                        # 系统凭据库索引及迁移数据
├── agents/
│   ├── session-metadata.sqlite     # 会话标题、归档与列表索引，须备份
│   └── <agent-id>/
│       ├── config.json             # Agent Profile
│       ├── sessions/<session-id>/
│       │   ├── config.json         # 旧版遗留文件，当前版本忽略
│       │   ├── session.jsonl       # 消息、工具事务、压缩检查点
│       │   ├── session.locations.jsonl # 可重建的字节位置索引
│       │   ├── memory.json         # 派生会话记忆
│       │   ├── attachments/        # 附件原件
│       │   ├── context-artifacts/  # 被移出窗口的大段内容
│       │   └── subagents/          # 子 Agent 运行摘要
│       └── tasks/<task-id>/
│           ├── config.json        # 计划及执行限制
│           └── runs/<run-id>.json  # 运行状态与摘要
├── workspaces/                     # 应用管理的工作区
├── skills/                         # 本地 Skills
├── mcp/servers.json                # MCP 连接器配置
├── cache/
│   ├── conversation-search.sqlite # 会话正文搜索索引
│   ├── document-search.sqlite     # 工作区文档搜索索引
│   └── browser-profiles/          # 各 Agent 独立的 Chrome Cookie 与站点状态
├── tmp/
└── logs/humbert.log
~~~

部分文件会在首次使用对应功能时才创建。session.jsonl 是会话消息与工具事务的事实来源；长会话使用内存缓存或 session.locations.jsonl 的字节位置按需读取，不要求每次构造上下文时把整份 JSONL 加载进内存。会话控制面以 agents/session-metadata.sqlite 为事实来源，该数据库使用 WAL，必须包含在离线备份中。旧版会话的 config.json 被忽略；数据库记录缺失时，只能从 JSONL Header 恢复会话身份，标题会成为“恢复的会话”，归档状态会重置。memory.json、位置索引及 cache/*.sqlite 是可重建的派生数据，两个缓存 SQLite 只用于搜索。browser-profiles 则保存可清除的站点状态，不进入备份。自定义工作区位于用户指定路径，不一定在上述数据目录内。

## 配置与安全

首次启动自动生成 ~/.humbert-agent/config.yaml；可参考仓库中的 [配置示例](config.example.yaml)。启动配置由 Viper 读取，支持 HUMBERT_* 环境变量覆盖，例如：

~~~bash
export HUMBERT_LOGGING_LEVEL=debug
export HUMBERT_SECURITY_SHELL_ENABLED=false
~~~

Provider 和模型等动态配置在应用设置中管理。桌面应用的模型密钥存于系统凭据库，普通 Provider JSON 不保存 API Key。文件写入、命令执行和浏览器操作受 Agent 能力选择、权限策略与运行环境共同约束；默认命令工具需要显式启用。与远程模型或 MCP 服务交互时，发送给对方的对话和工具数据仍受该服务自身的数据处理方式影响。

## 备份与恢复

在“设置 → 数据与备份”中设置至少 12 个字符的口令并安排加密备份。完全退出后再次启动，Humbert 会在加载数据服务前生成 .age 归档；如失败，原因会显示在设置页，并在下次启动重试或由用户取消。备份可包含模型密钥、会话、附件、任务、Skills 与托管工作区，请妥善保存归档及口令。

应用完全退出后，也可使用离线 CLI：

~~~bash
go run ./cmd/data backup  -output /path/to/humbert-backup.age -passphrase-file /path/to/passphrase -offline
go run ./cmd/data verify  -archive /path/to/humbert-backup.age -passphrase-file /path/to/passphrase
go run ./cmd/data restore -archive /path/to/humbert-backup.age -passphrase-file /path/to/passphrase -offline
~~~

口令文件须位于数据目录之外，在 Unix 系统上权限为 0600 或更严格。恢复会先校验归档，并把原数据目录保留为 ~/.humbert-agent.before-restore-* 供回退；旧版未加密 ZIP 也可校验和恢复。应用内恢复安排在下次启动执行。

## 开发与验证

~~~bash
cd frontend
npm ci
npm test
npm run build
cd ..
go test ./...
go vet ./...
~~~

frontend/dist/.gitkeep 让未构建前端的干净检出也能通过 Go 的嵌入资源检查。前端通过 Wails Runtime 的 `Call.ByName` 调用服务，正常构建不依赖生成的 JS bindings。修改 Wails 服务签名时，应同步检查 `frontend/src/api/` 的调用参数；如需类型 bindings，可显式执行：

~~~bash
wails3 generate bindings ./cmd/desktop/main.go -d ./frontend/bindings
~~~

工程约束见 [DEVELOPMENT.md](DEVELOPMENT.md)；目前的核心边界见 [领域边界文档](docs/architecture/domain-boundaries.md)。
