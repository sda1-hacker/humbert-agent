# Humbert Agent

Humbert Agent 是一个以 Go + Eino + Wails 3 + Vue 为核心的 Local-first Personal Agent 项目。
名字Humbert来自于作者喜欢的一个日本民谣组合ハンバート ハンバート。
作者也是刚接触Agent开发，功能正在逐渐完善,希望大家一起来学习，有什么好的建议多多issues

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
- `secrets/`：本地 Credential 文件。
- `agents/<id>/config.json`：Agent Profile。
- `agents/<id>/sessions/<id>/config.json`：会话标题、工作目录等配置。
- `agents/<id>/sessions/<id>/session.jsonl`：消息、工具调用/结果与压缩检查点的事实来源。
- `agents/<id>/sessions/<id>/memory.json`：从会话历史派生的记忆，按需创建。
- `agents/<id>/tasks/<id>/config.json`：主动任务、结构化日程、重叠/错过策略与执行限制。
- `agents/<id>/tasks/<id>/runs/<id>.json`：每次运行的持久化状态、计数、审批投影与结果摘要。
- `logs/`：运行审计；实时 `turn.*` 事件不追加到消息 JSONL。
- `workspaces/`：Managed Agent Workspace。

## 开发环境

项目 `go.mod` 当前要求：

```text
Go 1.27.1
```

桌面端使用 Wails 3，前端位于 `frontend/`，采用 Vue + JavaScript。

## 配置

第一次启动会自动创建 `~/.humbert-agent/config.yaml`。仓库中的 `config.example.yaml` 可作为配置参考。

配置覆盖示例：

```bash
export HUMBERT_LOGGING_LEVEL=debug
export HUMBERT_SECURITY_SHELL_ENABLED=false
```


## 生成 Wails Binding

在项目根目录执行：

```bash
wails3 generate bindings ./cmd/desktop/main.go -d ./frontend/bindings
wails3 dev
```

