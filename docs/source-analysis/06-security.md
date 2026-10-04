# 06 权限、审批与沙箱

入口：[permission/engine.go](../../internal/permission/engine.go)、[identity.go](../../internal/permission/identity.go)、[tools/capability_identity.go](../../internal/tools/capability_identity.go)、[approval/manager.go](../../internal/approval/manager.go)、[sandbox/manager.go](../../internal/sandbox/manager.go)。

## 三层职责

工具 Descriptor/Permission 决定动作是否被允许或需要确认；PathGuard 决定具体路径可进行何种操作；Native Runner 约束第三方进程及其子进程。Allow 不会扩大路径范围，风险 read不等于“所有文件都可读”，命令参数过滤不代替 OS 隔离。

## CapabilityIdentity 和授权范围

Identity包含能力类型、Tool、Risk、SandboxFingerprint，并按来源增加实际 executable/调用参数指纹、Skill 内容身份、MCP Server ID/指纹/raw tool、模块 ID/revision等。长期授权绑定安全身份，修改连接指向、脚本内容、沙箱或命令参数后不能继续用不匹配的旧允许规则。

Permission Request 来自冻结 Scope，加上本次真实参数。BuildPresentation 生成有限、脱敏的可读字段，原始认证信息不直接用作审批卡片。DenyScope 与 EqualExact 等方法分别服务拒绝范围和恢复校验，不靠显示名称判断是否同一能力。

## Engine 裁决顺序

1. 校验 Context/Request；Permission系统关闭时普通工具早返回 Allow，schedule_task有特殊分支。
2. 生成展示信息，加载匹配的 Session/持久规则；显式 Deny 优先。
3. always模式每次 Ask；full模式普通操作 Allow，并由组合根影响 FullAccess策略。
4. 其余模式先考虑匹配 Session Allow，再 Agent Allow；install_skill/schedule_task不接受这些长期 Allow。
5. 风险模式区分工作区常规动作与高风险动作，后者 Ask；安装/任务安排走相应逐次确认分支。自定义默认风险动作使用配置。

因此“某工具永远弹窗”不是全模式通用的实现描述；需要结合 Enabled/Mode 和特殊分支。显式 full 是用户配置的实际语义。

GrantOnce 不保存规则；GrantSession存在 Engine 内存，进程退出丢失；GrantAgent写 permissions.json。install_skill/schedule_task 只允许 once scope；run_command长期规则必须有完整 InvocationFingerprint。显式 Deny 可收紧此前的允许，撤销/清除 API 操作同一规则来源。

## 审批不是前端重发工具

Guard Ask 将安全 InterruptInfo 和内部 state分开编码。state保存原始参数及信息，以 StatefulInterrupt进入 Eino checkpoint。前端只提交 ApprovalID + approve/reject/scope等决策，不把参数再发回来。

Runtime收到 interrupted后注册 ApprovalManager、记录 InterruptID/等待状态、保留 Session预约，启动可取消的超时等待。ResolveApproval先检查 activeRun匹配与 checkpoint真实存在，再创建授权，防止“授权保存成功但无法恢复”。若保存规则失败，Manager可恢复 Pending并由 timeoutworker重新取得责任。

Guard恢复时确认当前是目标 ResumeContext，拒绝缺少状态/数据；批准后重建当前CapabilityIdentity，与冻结 info精确比较，最终执行保存的 state.Arguments。拒绝返回明确未执行文本。其他 root-cause对应的工具重新中断而不丢掉等待状态。

[checkpoint_store.go](../../internal/approval/checkpoint_store.go) 是每个运行的加锁内存字节map，Get/Set复制数据；不落盘。所以应用重启不能直接恢复一次活动审批的 SDK栈；Tasks把不完整运行标 Interrupted，而不是假装继续同一次工具执行。

## 路径策略

Profile有 workspace_only/standard/full_access 策略，网络有 none/public/all，Native有 off/preferred/required。Manager将应用默认和Agent覆盖合成 EffectivePolicy。

非FullAccess下：工作区FULL；standard额外授予普通Home READ_ONLY；AdditionalWritePaths是READ_WRITE；应用secrets/config/agents/mcp/logs/cache和系统敏感路径有BLOCKED。BLOCKED命中即硬拒绝；其余重叠授权取最高访问级别，同级取更具体根，不是所有规则都按“最长前缀覆盖”。

| 操作 | 最低访问级别 |
| --- | --- |
| read/list/search | READ_ONLY |
| create/modify | READ_WRITE |
| delete/move_source/move_target | FULL |

[pathguard.go](../../internal/sandbox/pathguard.go) 规范化绝对路径、处理已有祖先/缺失新文件与符号链接，再进行裁决。CanonicalRoot拒绝本身为symlink的配置根。受控文件工具使用os.Root进一步约束路径解析；硬保护不能被Additional路径或MCP派生授权覆盖。FullAccess是既有显式例外。

## 子进程 Runner

[runner.go](../../internal/sandbox/runner.go) 校验绝对 executable、允许工作目录、ProcessIsolationPolicy；创建每次独立临时目录并覆盖TMPDIR/TMP/TEMP及工具缓存变量。ReadonlyWorkspace时先派生只读规则，再准备平台命令、环境和进程组/Job。输出 bounded collector有截断状态；超时/取消终止进程树，等待宽限后收敛。

[process_policy.go](../../internal/sandbox/process_policy.go) 明确禁止受限Profile在原生文件隔离缺失时静默退回宿主权限。NativeOff是用户显式关闭系统隔离；即使Off，NetworkNone仍拒绝，因为无法阻断任意子进程socket。FullAccess+Preferred可在部分平台退回宿主权限，结果报告NativeUsed。

## 平台实现与真实限制

| 平台 | 代码与机制 | 限制 |
| --- | --- | --- |
| macOS | [platform_darwin.go](../../internal/sandbox/platform_darwin.go)，生成Seatbelt策略并用sandbox-exec，配置进程组终止 | 系统运行根、可读/可写根和硬拒绝共同编译；公共网络模式不等于每个第三方进程socket都由浏览器公网dialer验证 |
| Linux | [platform_linux.go](../../internal/sandbox/platform_linux.go)，探测bwrap能否建立文件/网络namespace；ro-bind/bind、proc/dev、unshare、cap-drop | 安装bwrap不证明namespace可用，探针失败会关闭相应能力 |
| Windows | [platform_windows.go](../../internal/sandbox/platform_windows.go)，RestrictedToken+JobObject限制权限/进程树 | Filesystem=false、Network=false；当前受限模式下拒绝启动要求这些边界的本地命令/Skill脚本 |

HTTP网页工具有自己的公网拨号边界，MCP允许用户配置受控本机端点；原生进程网络策略又是第三种机制。不要把其中任意一个的保证推广到全部工具。
