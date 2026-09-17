# Humbert Agent

Humbert Agent 是一个以 Go + Eino + Wails 3 + Vue 为核心的 Local-first Personal Agent 项目。

当前存储层使用文件持久化。Provider、Model、Agent Profile 使用领域级 JSON 文档；每个 Session 的配置和消息分别保存在 config.json 与 append-only JSONL 中。运行事件用于实时 UI，运行审计进入结构化日志。

## 数据目录

默认数据根目录：

```text
~/.humbert-agent/
├── config.yaml
├── config/
│   ├── providers.json
│   ├── models.json
│   └── preferences.json
├── secrets/
├── agents/
│   └── <agent-id>/
│       ├── config.json
│       ├── sessions/
│       │   └── <session-id>/
│       │       ├── config.json
│       │       ├── session.jsonl
│       │       └── memory.json
│       └── tasks/
│           └── <task-id>/
│               ├── config.json
│               └── runs/
│                   └── <run-id>.json
├── workspaces/
├── skills/
├── mcp/
├── cache/
├── tmp/
└── logs/
```

各类数据的职责：

- `config.yaml`：通过 Viper 加载的启动级配置；支持 `HUMBERT_*` 环境变量覆盖。
- `config/providers.json`：Provider 非敏感元数据；API Key 不写入该文件。
- `config/models.json`：Model Registry 与应用级多媒体模型路由的持久化数据。
- `config/preferences.json`：用户偏好预留入口。
- `secrets/`：本地 Credential 文件，Unix 下目录权限收紧为 `0700`、文件为 `0600`。
- `agents/<id>/config.json`：Agent Profile。
- `agents/<id>/sessions/<id>/config.json`：会话标题、工作目录等配置。
- `agents/<id>/sessions/<id>/session.jsonl`：消息、工具调用/结果与压缩检查点的事实来源。
- `agents/<id>/sessions/<id>/memory.json`：从会话历史派生的记忆，按需创建。
- `agents/<id>/tasks/<id>/config.json`：主动任务、结构化日程、重叠/错过策略与执行限制。
- `agents/<id>/tasks/<id>/runs/<id>.json`：每次运行的持久化状态、计数、审批投影与结果摘要。
- `logs/`：运行审计；实时 `turn.*` 事件不追加到消息 JSONL。
- `workspaces/`：Managed Agent Workspace。

Provider、Model、Agent 等低频配置使用“临时文件 + `fsync` + `rename`”原子替换；Session 使用 per-session/per-file 锁追加 JSONL。因此不同 Session 可以并行落盘，同一 Session 保持严格追加顺序。

> 当前并发保证针对单个 Humbert 进程内的多个 Agent / Session。若未来允许多个 Humbert Server 进程同时写同一个 `~/.humbert-agent`，需要再增加跨进程实例锁或 OS File Lock，不能仅依赖进程内 Mutex。

### 中断恢复与重试

- 会话配置缺失、Transcript 校验通过时，从 Header 重建配置，标题显示为“恢复的会话”；不覆盖已有的损坏配置。
- 缺失工具结果只在模型上下文中补充 `unknown` 标记，保留原始历史，不自动重放工具。
- 初始化失败返回已保存消息的收据；当前界面内相同内容重试会复用消息 ID。该机制不等于跨进程可靠任务队列。
- 回答后的压缩/记忆维护显示独立状态，用户停止或应用关闭均可取消。

### 日常会话体验

- 聊天界面按游标加载历史，每次默认展示最新 80 条；向前加载时保持当前阅读位置。
- 文字草稿按 Session 保存在本地，切换会话或重启界面不会互相覆盖；附件草稿按 Session 在当前进程内隔离。
- 用户消息支持复制和再次填入输入框，Assistant 回复支持整段复制。
- 默认“新会话”会在第一条用户输入落盘后自动生成短标题；用户手工命名的会话不会被覆盖。

### 多媒体输入

- 图片会校验真实文件格式并存入 Session 附件目录；调用 Provider 前才按需恢复为 Base64，二进制内容不会进入 transcript、Memory 或压缩摘要。
- 文本、源码、JSON/YAML/XML 等 UTF-8 文件会提取为普通文本内容，因此不依赖 Provider 原生 Files API。
- Agent 只保存 Chat、Utility 与 Memory 模型。全局图片回退模型在“设置 → 多媒体”中配置；Chat 模型缺少 Vision 时才会使用该模型。
- PDF、Office、音频和视频当前不支持。模型设置中的 Files/Audio 是 Provider 能力元数据，不代表 Humbert 已经实现对应附件入口。

### 主动任务

- “任务”工作台支持手动、单次、固定间隔、每天和每周计划；每天/每周计划使用 IANA 时区并由 Go 后端计算下一次运行时间。
- 错过计划可选择跳过或恢复后补跑一次；重叠可选择跳过或最多保留一个候补运行。全局最多并行两个任务，同一 Agent 同时只运行一个任务。
- 每次 TaskRun 创建独立 Session，完整消息与工具事实仍写入该 Session 的 JSONL；TaskRun JSON 只保存控制面状态、模型/工具调用计数、审批安全投影和结果摘要。
- 后台运行继续使用 Agent 的 Permission、Sandbox、Skills 与 MCP 配置。需要确认的工具会进入 `waiting_approval`，可在任务历史中批准或拒绝。
- 单次运行具有最长时间、模型调用次数、工具调用次数与重试次数限制。重试以父 Run ID 幂等创建，启动时会补建崩溃窗口中遗漏的重试。暂停会取消尚未开始的自动运行；手动运行仍可执行。
- 应用异常退出后，`starting/running/waiting_approval` 会在下次启动时转为 `interrupted`。旧 Eino checkpoint 和工具参数不会恢复或自动重放；原 Session 可用于审计。

当前分页只把选中窗口恢复成 Eino Message，限制了恢复、IPC 与前端渲染量。Transcript
首次访问或文件变化时仍会严格加载、校验完整 JSONL Tree；随后使用容量受限、可重建的
Document LRU，并在追加时增量推进 Leaf/Active Branch，避免同一进程内反复解析整份历史。
缓存同时维护 Message ID/序号到 Active Branch 位置的页级索引；历史分页只复制和解码当前
窗口，并继续保证 Assistant ToolCall、ToolResult 与最终 Assistant 回答不会被拆到两页。

本轮修复、测试和剩余工作见 [可靠性修复记录](docs/reviews/2026-09-15-reliability-fixes.md)。

## 开发环境

项目 `go.mod` 当前要求：

```text
Go 1.27.1+
```

桌面端使用 Wails 3，前端位于 `frontend/`，采用 Vue + JavaScript。

## 配置

第一次启动会自动创建 `~/.humbert-agent/config.yaml`。仓库中的 `config.example.yaml` 可作为配置参考。

配置覆盖示例：

```bash
export HUMBERT_LOGGING_LEVEL=debug
export HUMBERT_SECURITY_SHELL_ENABLED=false
```

业务代码不应直接读取环境变量；所有普通应用配置必须经 `internal/config` + Viper 进入 Application Composition Root。API Key、Token 等敏感信息由 `internal/credential` 管理。

## 生成 Wails Binding

在项目根目录执行：

```bash
wails3 generate bindings ./cmd/desktop/main.go -d ./frontend/bindings
wails3 dev


# 替换图标：
rm -fr ./bin/
rm -f build/darwin/icons.icns
rm -f build/windows/icon.ico
wails3 task common:generate:icons

从 build/darwin/Info.dev.plist 和 Info.plist 中删除
<key>CFBundleIconName</key>
<string>appicon</string>

rm -f build/darwin/Assets.car
```

## 前端依赖

```bash
cd frontend
npm install
cd ..
```

## 开发运行

```bash
wails3 dev
```

## Go 测试与静态检查

```bash
gofmt -w <modified-go-files>
go test ./...
go vet ./...
```

前端生产构建：

```bash
cd frontend
npm run build
```

## 工程规范

完整开发规范见 [`DEVELOPMENT.md`](./DEVELOPMENT.md)。后续代码生成、修改、重构与评审都应遵守该文件。
