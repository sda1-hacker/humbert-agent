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
│   ├── preferences.json
│   └── proactive.json
├── secrets/
├── agents/
│   └── <agent-id>/
│       ├── config.json
│       ├── sessions/
│       │   └── <session-id>/
│       │       ├── config.json
│       │       ├── session.jsonl
│       │       ├── memory.json
│       │       ├── attachments/
│       │       └── subagents/
│       │           └── <subrun-id>.json
│       ├── tasks/
│       │   └── <task-id>/
│       │       ├── config.json
│       │       └── runs/
│       │           └── <run-id>.json
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
- `config/proactive.json`：主动助手设置、可靠待处理事件与处理记录。
- `secrets/`：本地 Credential 文件。
- `agents/<id>/config.json`：Agent Profile。
- `agents/<id>/sessions/<id>/config.json`：会话标题、工作目录等配置。
- `agents/<id>/sessions/<id>/session.jsonl`：消息、工具调用/结果与压缩检查点的事实来源。
- `agents/<id>/sessions/<id>/memory.json`：从会话历史派生的记忆，按需创建。
- `agents/<id>/tasks/<id>/config.json`：主动任务、结构化日程、重叠/错过策略与执行限制。
- `agents/<id>/tasks/<id>/runs/<id>.json`：每次运行的持久化状态、计数、审批投影与结果摘要。
- `agents/<id>/sessions/<id>/subagents/<id>.json`：当前会话内一次 Agent-as-Tool 调用的轻量审计；不保存独立聊天历史。
- `logs/`：运行审计；实时 `turn.*` 事件不追加到消息 JSONL。
- `workspaces/`：Managed Agent Workspace。

## 多 Agent 协作

Agent 可以按需启用 `list_agents` 与 `run_agent`。`run_agent` 使用 Eino AgentTool 在当前
Turn 内同步创建一次性的独立子 Runtime：子 Agent 只接收主 Agent 写出的自包含任务，不读取
父会话历史，也不创建普通 Session 或侧栏对话。它使用自己的 Profile、模型、指令、
Tool、Skill 和 MCP，不与父 Agent 的能力清单取交集。Workspace、Sandbox、Network 和
Permission 则继承父 Runtime 的安全边界，所有审批仍在父对话内完成。

Agent 默认不接受子 Agent 调用。只有在 Agent 设置中显式打开“允许作为子 Agent 调用”后，
它才会出现在 `list_agents` 结果中，`run_agent` 后端也会再次校验该开关。

子 Agent 完成后，最终文本作为标准 ToolResult 返回给主 Agent；主 Agent 必须继续判断、核验和
综合，再生成面向用户的回答。子 Agent 的高风险操作通过 Eino CompositeInterrupt 在当前父会话
审批。父 Session 的 ToolCall/ToolResult 是对话事实来源，`subagents/*.json` 只记录运行身份、
目标、状态和最终结果，便于排障，不形成第二套聊天历史。

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
