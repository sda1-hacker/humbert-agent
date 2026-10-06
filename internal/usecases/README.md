# Usecases：跨领域业务协调

[架构目录](../../docs/项目源码详解.md)

| 用例 | 规则与所有权 |
| --- | --- |
| `AgentLifecycle` | 暂停任务派发，在 Runtime 删除门闩内删除 Agent/会话，再清理会话元数据和临时权限。 |
| `SkillMaintenance` | 查询 Agent 引用，仍被使用或引用查询失败时拒绝删除 Skill。 |
| `MCPConfiguration` | 校验配置、写入新凭据、提交 Server，再清理失去引用的旧凭据；失败保留原配置。 |
| `VisionInspector` | 选择 Agent 视觉模型或已配置的辅助模型，真实调用仍计入父运行预算。 |
| `WorkspaceQuery` | 从当前 Agent Profile 解析工作区，复用受控目录列表/预览，提供最多 5,000 个文件的概览统计。 |

这些用例不依赖 Wails，也不扫描其他模块的数据目录；持久化仍通过已有领域服务完成。普通单模块业务直接调用该模块即可，不需要为每个方法再造一层用例或接口。

`workspace_query.go` 接收 AgentService 和 WorkspaceManager，收拢原 `workspaceview` 的查询。每次请求重新读取 Profile 和磁盘状态，不另建文件缓存，也不从 Session 历史推导产物。目录列表与预览继续通过 WorkspaceManager，受控路径、截断和错误语义保留。将它直接放入 workspace 会引入 workspace → agents → workspace 循环依赖；跨领域协调放入现有 usecases 更符合依赖方向。
