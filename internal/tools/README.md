# Tools：能力注册、授权包装与结果预算

[总目录](../../docs/architecture/README.md) · [内置工具](builtin/README.md) · [Permission](../permission/README.md)

## 一次 Tool 从配置到执行

`app.buildToolRegistry` 注册各 Builtin 的 Factory/Descriptor；Agent Profile 保存选中的名字。`Registry.Resolve` 用本轮 Scope 构造具体 Eino Tool，并包装权限判断与调用上下文。Scope 含 Agent/Session、工作区、Sandbox、审批、结果预算等，不能从模型传入的 JSON 自行推导授权对象。MCP 与 Skill Tool 最终并入 Runtime Snapshot。

```mermaid
flowchart LR
  F[Factory + Descriptor] --> R[Registry.Register]
  A[Agent Tool 选择] --> S[Registry.Resolve(scope)]
  R --> S
  S --> G[GuardInvokableTool]
  G --> P[Permission / Approval]
  P --> B[Builtin InvokableRun]
  B --> X[Sandbox / Workspace]
  B --> Q[ResultBudget / context-artifact]
```

`Descriptor` 定义名称、风险及能力身份；`capability_identity.go` 把参数与执行目标转换为可匹配的身份。`guarded_tool.go` 在真实调用前执行 Permission，Ask 时触发 Eino interrupt，并在大结果出现时交给 `contextartifact` 保存全文、返回可回查预览。`result_budget.go` 限制整个窗口内 Tool 输出量；`call_context.go` 传稳定 ToolCall ID 给审计和恢复逻辑。

| 文件 | 重点 |
| --- | --- |
| `registry.go` | 注册、去重、关闭、按 Agent 选择解析本轮工具。 |
| `types.go` | `Factory`、`Descriptor`、`Scope` 的契约。 |
| `guarded_tool.go`、`permission.go` | 授权、Eino interrupt/恢复与大结果保护。 |
| `capability_identity.go` | 规则匹配所需的工具目标。 |
| `result_budget.go` | 工具内容窗口预算。 |

新增工具先实现 Factory/Descriptor，再在 `app/tools.go` 注册；在实际 I/O 处再次校验 Sandbox/Workspace。仅设置 Descriptor 风险不能替代文件或网络边界。

## Scope 是工具的信任边界

模型生成的 JSON 只是一份调用参数，不能决定自己属于哪个 Session、能访问哪个工作区或是否跳过审批。`Scope` 由 Resolver 从已验证的 Agent/Session/Sandbox 快照构建；Factory 的 `Build` 把 Scope 固定到 Tool 实例。`GuardInvokableTool` 在每次调用前根据 Descriptor 与参数生成 CapabilityIdentity 并交给 Permission。Ask 中断保存原始调用状态，恢复时不接受前端重新提交的参数。

Tool 输出也有边界：`ResultBudget` 限制整轮保留的文字量；超出阈值时 `contextartifact.Store.Archive` 可保留原文并返回可回查 ID。Transcript 写入完整工具事务，ContextEngine 在模型输入中可能进一步缩短旧工具结果。调试时分别看 Tool 原始返回、JSONL ToolResult 和下一次模型输入，三者长度可能不同。
