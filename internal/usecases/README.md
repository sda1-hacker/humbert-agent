# Usecases：跨领域业务协调

[架构目录](../../docs/architecture/README.md)

| 用例 | 规则与所有权 |
| --- | --- |
| `AgentLifecycle` | 暂停任务派发，在 Runtime 删除门闩内删除 Agent/会话，再清理会话元数据和临时权限。 |
| `SkillMaintenance` | 查询 Agent 引用，仍被使用或引用查询失败时拒绝删除 Skill。 |
| `MCPConfiguration` | 校验配置、写入新凭据、提交 Server，再清理失去引用的旧凭据；失败保留原配置。 |
| `VisionInspector` | 选择 Agent 视觉模型或已配置的辅助模型，真实调用仍计入父运行预算。 |

这些用例不依赖 Wails，也不扫描其他模块的数据目录；持久化仍通过已有领域服务完成。普通单模块业务直接调用该模块即可，不需要为每个方法再造一层用例或接口。
