# 01 启动、配置与基础设施

源码入口：[desktop/main.go](../../cmd/desktop/main.go)、[data/main.go](../../cmd/data/main.go)、[application.go](../../internal/app/application.go)、[lifecycle.go](../../internal/app/lifecycle.go)、[config.go](../../internal/config/config.go)。

## 桌面进程的启动顺序

`main` 调用 `os.Exit(run())`，真正需要 defer 的资源放在 `run` 中。`run` 先解析用户目录并取得应用数据锁；锁必须早于恢复、备份和 Bootstrap，防止第二个实例改写仍在使用的数据。

随后顺序执行 `ApplyPendingRestore`、`ApplyPendingBackup`，再 `app.Bootstrap`。恢复失败阻止启动；待执行备份失败记录原因并允许应用继续，留待下次启动重试。恢复/备份阶段使用系统凭据库导入/导出秘密，不依赖尚未创建的 Application。

`runDesktop` 只装配 Wails 应用、服务、嵌入前端资源和主窗口。窗口初始 1100×750，最小 860×600；macOS 使用透明原生标题栏并在最后一个窗口关闭后终止。Wails 退出后 `run` 给 Core 一个 5 秒关闭 Context，再释放数据锁。`marshalFrontendError` 把错误变成只有 `message` 的 JSON，使用 `logging.SafeErrorText` 脱敏并限制长度。

离线 `cmd/data` 支持 backup/verify/restore：backup 和 restore 要求 `-offline`，且真实取得同一数据锁；verify 只读独立归档，无需锁应用数据。口令文件必须是普通文件、最多 1024 字节，非 Windows 要求无组/其他用户权限，不能放进被备份的数据根；口令至少 12 个 Unicode 字符。标志表达用户意图，锁负责证明当前可独占。

## Bootstrap 的依赖组装

`Bootstrap` 显式构造依赖，没有让领域服务反查全局 App。主要顺序为：

1. Config → Logger → 系统 Credentials → TranscriptStore。
2. WorkspaceManager → SandboxManager → PermissionStore/Engine → ApprovalManager → Preferences。
3. SkillManager → MCP Store/Manager → Model Store/Registry → Agent Store/Service。
4. Agent 删除恢复 → MCP Runtime Backend → Session Store/Service → WorkspaceView → SearchIndex。
5. ContextArtifactStore → ToolRegistry → ContextEngine → EventBus/Reporter → Runtime Resolver/Executor。
6. Collaboration → 协作工具 → Runtime Service → Tasks → schedule_task 工具 → Notifications → Proactive。
7. 外部注入模块安装/启动 → Task Scheduler 和 Proactive 启动 → 跨域 Usecases → ready。

这解释了“某些工具晚注册”的原因：`schedule_task` 需要 TaskManager，而 TaskManager 依赖 Runtime；`run_agent` 需要能够构建子 Agent 的 Resolver。注册发生在实际开始生产工作之前，不通过执行时查找全局依赖解决环。

`Application` 的 Getter 暴露已有服务供组合根/桌面适配层使用。`Status` 除内存 ready、版本、revision 和 uptime 外，还读取模型、Agent、Skill、MCP、Task 存储做健康检查，不能把 ready 与所有文件可读混为一谈。

## 资源所有权

`lifecycle` 保存 `phase/name/close` 清单。构造成功立即登记；启动失败与正常 Shutdown 共用清单。关闭阶段依次为：

| 阶段 | 作用 |
| --- | --- |
| stopWork | 停止模块、主动助手和调度器产生新工作 |
| finishRuns | Runtime 拒绝新操作、取消/等待当前运行 |
| closeResources | 释放模块、工具、搜索、数据库、MCP、Workspace、EventBus、Logger 等资源 |

同一阶段逆构造顺序关闭；`sync.Once` 防止重复关闭；`errors.Join` 聚合失败并继续关闭剩余资源。Logger 最后关闭。模块 `Start` 即使部分启动后失败也进入 Stop；未转交宿主的非法模块立即局部清理。

## 配置和路径

`config.Load/loadFromHome` 使用独立 Viper 实例：设置代码默认值，读取 `~/.humbert-agent/config.yaml`，再应用 `HUMBERT_` 环境变量；点号/连字符映射下划线。Unmarshal 后设置派生 Paths、normalize 和 validate。路径从用户 Home 派生，不把所有 Paths 当成可随意覆盖的 YAML 字段。

| 路径 | 内容 |
| --- | --- |
| config.yaml | 应用、上下文、工具、安全设置 |
| config/providers.json、models.json | 模型配置，秘密仅保存引用 |
| config/preferences.json、personal-memory.json、permissions.json | 用户资料、明确保存的记忆、长期权限 |
| agents/ | Agent 配置、会话 JSONL/附件、Task 记录及会话元数据 SQLite |
| workspaces/ | 托管工作区 |
| skills/、mcp/servers.json | 扩展包和 MCP 控制面 |
| cache/、tmp/、logs/、secrets/ | 派生数据、临时数据、日志、凭据索引/受控资料 |

目录创建使用 0700，敏感 JSON 通常 0600，并拒绝危险的符号链接/文件类型。Tools 配置由 [tools.go](../../internal/config/tools.go) 单独装配各能力限制；权限和 Sandbox 的运行时更新由对应 configwrite/permission/sandbox 文件处理，不要求重启所有领域服务。

核心默认值可从 `setDefaults` 核对：ReAct 上限 200；自动压缩开启；上下文操作超时 120 秒；权限模式 risk；默认 read allow、write/exec ask；Sandbox standard/public/preferred；命令宽限期 1500ms。真正裁决还要进入 Permission 与 Sandbox，默认配置字段不是最终权限结果。

## 公共基础设施

| 包 / 源码 | 具体实现 |
| --- | --- |
| [atomicfile/json.go](../../internal/atomicfile/json.go) | JSON 读取及同目录临时文件写入、同步/关闭/替换；调用者锁保证领域读改写的整体一致性，原子文件替换不替代事务锁 |
| [eventbus/bus.go](../../internal/eventbus/bus.go) | 主题到 Handler 的映射；Publish 复制回调后释放锁，同步执行并捕获 panic；不创建隐藏 goroutine、不落盘、不保证回调顺序 |
| [logging/logger.go](../../internal/logging/logger.go) | slog 结构化日志、文件/控制台、级别/格式、敏感信息脱敏与安全错误文本 |
| [instancelock](../../internal/instancelock/lock.go) | 平台文件锁；Unix 与 Windows 实现分文件编译；保护进程级数据操作，而非替代单个 Session 的并发锁 |
| [commandenv/command.go](../../internal/commandenv/command.go) | 解析实际程序路径、规范化命令参数、构建环境身份，供命令能力/授权使用 |

## 模块接入

[component/module.go](../../internal/component/module.go) 定义 `Host`、`Installer`、`Module` 和 `Provider`。Host 只给公共 Logger/Credentials/Events/Sandbox/DataDir；业务依赖由组件构造器显式注入。Module 返回稳定 ID、Capabilities、Start/Stop/Close；后台模块有 Start 必须有 Stop。

Provider 的 Selection 来自其自己保存的 Agent 绑定；Describe 只描述本地 Schema，Resolve 构造实际工具。`runtime.extensionRegistry` 校验稳定 ID、模块前缀、重复选择、非空 revision、Schema 名称和工具来源，并通过同一个 Registry.Guard 包装。首次 snapshot 后注册封闭。`examples/modules/textstats.go` 展示这条接入链路；当前桌面入口没有传入 `WithModules`，因此示例模块不是默认启用的产品功能。

## 构建与平台模板

[Taskfile.yml](../../Taskfile.yml) 按 GOOS 分发到各平台任务；[build/config.yml](../../build/config.yml) 为 Wails dev 配置后端重建、前端后台服务和主运行任务。Vite 监听本机 9245 或指定 WAILS_VITE_PORT，生产资源由 frontend/assets.go 嵌入。

[darwin/Taskfile.yml](../../build/darwin/Taskfile.yml) 的本机构建已经显式指定 `./cmd/desktop`，随后可组装 `.app`、签名和 DMG。[windows/Taskfile.yml](../../build/windows/Taskfile.yml) 含资源 syso、NSIS/MSIX 和 WebView2 引导；[linux/Taskfile.yml](../../build/linux/Taskfile.yml) 含本机/Docker 编译、desktop/AppImage 等任务；公共 Taskfile 含 server/Docker 构建模板。

这些模板并非全部与当前入口同步：Linux/Windows 本机以及 server/cross Docker 的部分命令仍从仓库根编译；当前根目录没有 Go 文件，`go list .` 已实际返回 no Go files。Windows 资源文件生成位置也仍在根目录，迁移编译目标时需一并调整。详细建议见维护章，不能把文件存在当成这些发布链路已通过验证。

`build/android/main_android.go` 注册移动主入口，`build/ios/main_ios.go` 导出 UIKit 调用的 WailsIOSMain，其他脚本检查移动开发依赖。它们属于保留的平台封装模板；当前 Agent Runtime 的桌面沙箱、钥匙串、浏览器和窗口能力不能仅凭模板推定已适配移动端。本文未执行移动构建或平台部署。
