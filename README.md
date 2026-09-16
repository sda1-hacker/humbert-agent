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
│       └── sessions/
│           └── <session-id>/
│               ├── config.json
│               ├── session.jsonl
│               └── memory.json
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
- `config/models.json`：Model Registry 持久化数据。
- `config/preferences.json`：用户偏好预留入口。
- `secrets/`：本地 Credential 文件，Unix 下目录权限收紧为 `0700`、文件为 `0600`。
- `agents/<id>/config.json`：Agent Profile。
- `agents/<id>/sessions/<id>/config.json`：会话标题、工作目录等配置。
- `agents/<id>/sessions/<id>/session.jsonl`：消息、工具调用/结果与压缩检查点的事实来源。
- `agents/<id>/sessions/<id>/memory.json`：从会话历史派生的记忆，按需创建。
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

当前分页只把选中窗口恢复成 Eino Message，限制了恢复、IPC 与前端渲染量；底层仍会加载并校验完整 JSONL Tree。长历史的增量索引/读取仍是后续性能工作，不能把界面分页等同于存储层随机读取。

本轮修复、测试和剩余工作见 [可靠性修复记录](docs/reviews/2026-09-15-reliability-fixes.md)。

## 开发环境

项目 `go.mod` 当前要求：

```text
Go 1.26.1+
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
