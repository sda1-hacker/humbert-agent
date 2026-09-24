# Humbert Agent 开发规范

本文档是 Humbert Agent 后续代码生成、修改、重构和评审的工程约束。除非项目负责人明确修改本规范，否则新增代码与修改代码均应遵守。

## 1. 代码必须完整可用

- 每个函数必须实现完整功能。
- 禁止提交 `TODO`、伪代码、占位实现或 `panic("not implemented")`。
- 不允许省略需求要求的关键逻辑。
- 当前需求要求实现的功能，必须提供可编译、可运行的真实代码。

## 2. 统一日志

- 项目统一使用 `internal/logging`。
- 业务代码禁止随意使用 `fmt.Println`、`fmt.Printf`、`log.Println` 等作为日志输出。
- 使用统一日志级别：`Debug`、`Info`、`Warn`、`Error`。
- 日志应尽量携带可诊断上下文字段，例如：`request_id`、`session_id`、`agent_id`、`operation`、`duration_ms`。
- 禁止在日志中输出密码、Token、API Key、Cookie、完整 Credential、敏感 Tool 参数或其他 Secret。

## 3. 中文注释

核心类型、接口、结构体、公共函数及复杂逻辑必须提供有意义的中文注释。注释不仅说明“做什么”，还应在必要时解释：

- 为什么这样设计；
- 生命周期与资源所有权；
- 关键边界条件；
- 错误处理策略；
- 与其他模块的关系；
- 并发和安全约束。

避免重复代码本身含义的无意义注释。

## 4. 配置统一使用 Viper

- 应用启动配置统一由 `internal/config` 使用 Viper 加载。
- 业务代码禁止直接读取环境变量或自行解析 `config.yaml`。
- 配置支持：合理默认值、配置文件、环境变量覆盖。
- Provider、Model、Agent、Session 等动态领域状态不塞入 Viper；它们由各自 Store 管理。
- API Key、Token 等敏感信息与普通配置分离，统一进入 `secrets/` 对应 Credential Store，不得硬编码。

## 5. 错误处理

- 禁止忽略重要 `error`。
- 向上返回错误时增加业务上下文，例如：`fmt.Errorf("读取配置失败: %w", err)`。
- 需要上层判断的错误使用稳定 Sentinel Error 或自定义错误类型，并通过 `errors.Is` / `errors.As` 判断。
- 禁止通过比较错误字符串判断错误类型。
- 清理阶段若错误不会改变主操作结果，应明确说明为何属于 best-effort；否则必须返回或记录。

## 6. 工程结构

- 保持高内聚、低耦合。
- Package 只承担清晰职责。
- 禁止为了方便随意创建 `utils`、`common`、`misc` 等职责模糊目录。
- 不为了抽象而抽象；只有当抽象能够隔离明确的领域或基础设施边界时才引入接口。
- Domain 不直接依赖 Wails、Vue 或具体持久化实现。
- Wails/HTTP/CLI 等都属于 Adapter，通过 Application Service 进入核心逻辑。

## 7. Context 与并发

- 网络、文件 IO、数据库、外部进程、模型调用和长时间任务优先接收 `context.Context`。
- 禁止创建无法取消、无法退出、无法回收资源的 goroutine。
- 后台任务必须有明确 Owner、Context、退出路径和资源回收策略。
- 高频运行时状态避免全局写锁；Session Transcript 采用 per-file/per-session 串行化，不同 Session 应允许并行执行。
- 低频 Provider/Model/Agent 配置更新允许使用领域级互斥锁保护“读-改-原子写回”。

## 8. 安全要求

所有用户输入和外部输入默认不可信，必须考虑：

- 路径穿越和符号链接逃逸；
- 命令注入；
- 越权文件访问；
- SSRF 和不安全网络目标；
- 敏感信息泄漏；
- 非法配置或损坏持久化数据。

未经验证的输入不得直接传递给 Shell、文件系统、SQL 或外部进程。敏感数据不得进入普通日志和普通配置文件。

## 9. 测试要求

- 核心逻辑必须有测试。
- 修改 Bug 时增加对应回归测试。
- 并发、持久化和安全边界应优先增加测试。
- 完成代码后尽量真实执行：

```bash
gofmt -w <modified-go-files>
go test ./...
go vet ./...
```

- 不允许声称测试通过但实际上没有执行。
- 如果受 Go 版本、网络、系统依赖或 CI 环境限制无法执行，必须如实记录具体原因。

## 10. 修改原则

- 修改前先阅读并理解现有代码和依赖关系。
- 优先复用已有实现。
- 不擅自修改与当前需求无关的代码。
- 不进行无关的大规模重构。
- 修改现有文件时尽量保持原有代码风格、公开 API 和兼容性。
- 删除代码前必须确认没有调用方，并在变更说明中列出删除原因。

## 11. 输出与交付

每次实现任务至少说明：

- 修改了什么；
- 新增了什么；
- 为什么这样设计；
- 如何运行和测试；
- 实际执行了哪些验证；
- 哪些验证因环境限制未执行；
- 哪些旧文件已经可以删除或已经删除。

提供文件代码时必须是完整文件，不使用“省略”“其他代码不变”等方式跳过关键实现。

## 12. 当前 Local-first 持久化基线

SQLite 只用于可重建的搜索索引，不承担 Runtime 状态和会话事实存储。当前主要目录如下；某些文件在首次使用对应功能时才创建：

```text
~/.humbert-agent/
├── config.yaml
├── config/
│   ├── providers.json
│   ├── models.json
│   ├── preferences.json
│   ├── permissions.json
│   ├── personal-memory.json
│   └── proactive.json
├── secrets/
├── agents/
│   └── <agent-id>/
│       ├── config.json
│       ├── sessions/
│       │   └── <session-id>/
│       │       ├── config.json
│       │       ├── session.jsonl
│       │       ├── session.locations.jsonl
│       │       ├── memory.json
│       │       ├── attachments/
│       │       ├── context-artifacts/
│       │       └── subagents/
│       └── tasks/<task-id>/
│           ├── config.json
│           └── runs/<run-id>.json
├── workspaces/
├── skills/
├── mcp/servers.json
├── cache/
│   ├── conversation-search.sqlite
│   └── document-search.sqlite
└── logs/humbert.log
```

其中：

- `config.yaml`：Viper 启动级配置；
- `providers.json`：Provider 非敏感元数据；
- `models.json`：Model 配置；
- `preferences.json` 与 `personal-memory.json`：用户偏好及确认保存的个人记忆；
- `secrets/`：Credential 索引和迁移数据；桌面密钥使用系统凭据库；
- `agents/<id>/config.json`：Agent Profile；
- `sessions/<id>/config.json`：会话配置；
- `sessions/<id>/session.jsonl`：消息、工具事务和压缩检查点的事实来源；
- `sessions/<id>/memory.json`、`session.locations.jsonl`：可重建的记忆和字节位置索引；
- `tasks/<id>/runs/`：任务调度、状态与摘要；完整执行消息仍在对应 Session；
- `cache/*.sqlite`：可重建的跨会话与文档搜索索引；
- `workspaces/`：应用托管的工作目录；自定义工作区可在此目录之外。

配置 JSON 使用“同目录临时文件 + fsync + rename”原子替换；Session 使用 append-only JSONL 与 per-file 锁。可重建索引只能作为 Cache，不能成为第二份持久化事实来源。详细的代码入口和数据流见 [代码阅读导引](docs/architecture/code-reading-guide.md)。
