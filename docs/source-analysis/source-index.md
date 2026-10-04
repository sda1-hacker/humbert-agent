# 源码文件与声明索引

本索引从当前工作区实现文件直接生成，不读取既有项目文档。排除 internal/rag、生成 bindings、依赖、构建产物和二进制资源；测试另列。内容摘要是 SHA-256 前 12 位，用于识别本次说明对应的文件版本，不是安全签名。

文件表列出全部范围内实现文件，声明栏选取前 8 个类型/函数；后面的展开区列出扫描到的全部顶层 Go 函数和类型及行号。函数扫描用于定位，具体行为以章节中阅读函数体的解释和源文件为准。

共 **355 个实现/配置文件**，其中 **226 个 Go 文件**；另有 **135 个非 RAG 测试文件**。

## 01 章节文件

实现说明：[01-bootstrap.md](01-bootstrap.md)。

| 文件 | 行数 | 内容摘要 | 主要声明（节选） |
| --- | ---: | --- | --- |
| [Taskfile.yml](../../Taskfile.yml) | 65 | `c0c528360071` | 组件/配置/样式 |
| [build/Taskfile.yml](../../build/Taskfile.yml) | 390 | `c67982f859f1` | 组件/配置/样式 |
| [build/android/main_android.go](../../build/android/main_android.go) | 11 | `8009f2995afd` | `init` |
| [build/android/scripts/deps/install_deps.go](../../build/android/scripts/deps/install_deps.go) | 266 | `b43404869b4b` | `main`、`checkCommand`、`offerCreateAVD`、`findAVDManager`、`promptUser` |
| [build/config.yml](../../build/config.yml) | 79 | `232ce102eb86` | 组件/配置/样式 |
| [build/darwin/Taskfile.yml](../../build/darwin/Taskfile.yml) | 220 | `1846bc5ecbba` | 组件/配置/样式 |
| [build/docker/Dockerfile.cross](../../build/docker/Dockerfile.cross) | 220 | `59bc0c437979` | 组件/配置/样式 |
| [build/docker/Dockerfile.server](../../build/docker/Dockerfile.server) | 67 | `afc6ccef990f` | 组件/配置/样式 |
| [build/ios/app_options_default.go](../../build/ios/app_options_default.go) | 10 | `f94c44d8b3ce` | `modifyOptionsForIOS` |
| [build/ios/app_options_ios.go](../../build/ios/app_options_ios.go) | 11 | `ce12de872777` | `modifyOptionsForIOS` |
| [build/ios/main_ios.go](../../build/ios/main_ios.go) | 23 | `5340fe49ccbe` | `WailsIOSMain` |
| [build/ios/scripts/deps/install_deps.go](../../build/ios/scripts/deps/install_deps.go) | 319 | `c8619ee58f0f` | `Dependency`、`main`、`checkCommand`、`promptUser` |
| [build/linux/Taskfile.yml](../../build/linux/Taskfile.yml) | 220 | `ce84cca63f4b` | 组件/配置/样式 |
| [build/windows/Taskfile.yml](../../build/windows/Taskfile.yml) | 195 | `b6ff59dcde8c` | 组件/配置/样式 |
| [cmd/data/main.go](../../cmd/data/main.go) | 178 | `5d9ae3ece86e` | `main`、`acquireDataLock`、`usage`、`readPassphrase`、`fail` |
| [cmd/desktop/main.go](../../cmd/desktop/main.go) | 252 | `80d11b96812d` | `main`、`run`、`runDesktop`、`marshalFrontendError` |
| [go.mod](../../go.mod) | 109 | `07bc47fb0293` | 组件/配置/样式 |
| [internal/app/application.go](../../internal/app/application.go) | 583 | `4096438b0b48` | `Status`、`Application`、`Bootstrap`、`Application.Search`、`Application.Lifecycle`、`Application.Maintenance`、`Application.Config`、`Application.Logger` |
| [internal/app/lifecycle.go](../../internal/app/lifecycle.go) | 53 | `1b7ee17fce29` | `cleanupEntry`、`lifecycle`、`lifecycle.add`、`lifecycle.close` |
| [internal/app/modules.go](../../internal/app/modules.go) | 103 | `dda0aeef7741` | `BootstrapOption`、`bootstrapOptions`、`mountedModule`、`moduleManager`、`WithModules`、`moduleManager.install`、`moduleManager.start`、`moduleManager.stop` |
| [internal/app/runtime_event_reporter.go](../../internal/app/runtime_event_reporter.go) | 41 | `ff0089cfefcd` | `runtimeEventPublisher`、`runtimeEventReporter`、`newRuntimeEventReporter`、`runtimeEventReporter.Report` |
| [internal/app/tools.go](../../internal/app/tools.go) | 184 | `5c77a7ee6266` | `toolDependencies`、`registerCollaborationTools`、`buildToolRegistry` |
| [internal/atomicfile/json.go](../../internal/atomicfile/json.go) | 258 | `1a29b3c855ad` | `WriteJSON`、`ReadJSON`、`ensureRealDirectory`、`validateRealDirectory`、`writeFull`、`replaceFile` |
| [internal/commandenv/command.go](../../internal/commandenv/command.go) | 101 | `f4c76bde8840` | `Path`、`Validate`、`Resolve`、`Name` |
| [internal/component/module.go](../../internal/component/module.go) | 72 | `fc9020968ed1` | `Host`、`Module`、`Installer`、`Provider`、`Request`、`Tool`、`Contribution` |
| [internal/config/config.go](../../internal/config/config.go) | 719 | `4e467293f1d8` | `Config`、`AppConfig`、`LoggingConfig`、`RuntimeConfig`、`ContextConfig`、`SkillConfig`、`MCPConfig`、`SecurityConfig` |
| [internal/config/configwrite.go](../../internal/config/configwrite.go) | 152 | `b066bbec850f` | `saveConfigMutation`、`validateWritableConfigFile`、`replaceConfigFile` |
| [internal/config/permission.go](../../internal/config/permission.go) | 70 | `7e94a27d1bb3` | `NormalizePermissionConfig`、`ValidatePermissionConfig`、`SavePermissionConfig` |
| [internal/config/sandbox.go](../../internal/config/sandbox.go) | 79 | `e266a3dc1a6c` | `NormalizeSandboxConfig`、`ValidateSandboxConfig`、`SaveSandboxConfig`、`SaveSandboxAndShellConfig` |
| [internal/config/tools.go](../../internal/config/tools.go) | 748 | `7f8afd13f569` | `ToolConfig`、`FileToolConfig`、`WebSearchToolConfig`、`WebFetchToolConfig`、`CommandToolConfig`、`LoadToolConfig`、`ToolConfig.Validate`、`FileToolConfig.Validate` |
| [internal/eventbus/bus.go](../../internal/eventbus/bus.go) | 235 | `7de217c73196` | `Handler`、`Bus`、`New`、`Bus.Subscribe`、`Bus.Publish`、`callHandler`、`Bus.Close` |
| [internal/instancelock/lock.go](../../internal/instancelock/lock.go) | 62 | `8e9bbc1387d2` | `Lock`、`Acquire`、`Lock.Close` |
| [internal/instancelock/lock_unix.go](../../internal/instancelock/lock_unix.go) | 18 | `27979de75b90` | `tryLock` |
| [internal/instancelock/lock_windows.go](../../internal/instancelock/lock_windows.go) | 17 | `0f556feb8e6b` | `tryLock` |
| [internal/logging/logger.go](../../internal/logging/logger.go) | 391 | `f1caa6c34e70` | `Logger`、`NewBootstrap`、`New`、`parseLevel`、`Logger.Slog`、`Logger.Level`、`Logger.Debug`、`Logger.Info` |

<details>
<summary>本章 Go 顶层函数与类型定位（自动扫描）</summary>

### build/android/main_android.go

[build/android/main_android.go](../../build/android/main_android.go)

函数/方法：`init`（7）。

### build/android/scripts/deps/install_deps.go

[build/android/scripts/deps/install_deps.go](../../build/android/scripts/deps/install_deps.go)

函数/方法：`main`（14）；`checkCommand`（150）；`offerCreateAVD`（161）；`findAVDManager`（224）；`promptUser`（253）。

### build/ios/app_options_default.go

[build/ios/app_options_default.go](../../build/ios/app_options_default.go)

函数/方法：`modifyOptionsForIOS`（8）。

### build/ios/app_options_ios.go

[build/ios/app_options_ios.go](../../build/ios/app_options_ios.go)

函数/方法：`modifyOptionsForIOS`（8）。

### build/ios/main_ios.go

[build/ios/main_ios.go](../../build/ios/main_ios.go)

函数/方法：`WailsIOSMain`（21）。

### build/ios/scripts/deps/install_deps.go

[build/ios/scripts/deps/install_deps.go](../../build/ios/scripts/deps/install_deps.go)

类型：`Dependency`（20）。

函数/方法：`main`（30）；`checkCommand`（291）；`promptUser`（302）。

### cmd/data/main.go

[cmd/data/main.go](../../cmd/data/main.go)

函数/方法：`main`（18）；`acquireDataLock`（121）；`usage`（129）；`readPassphrase`（134）；`fail`（175）。

### cmd/desktop/main.go

[cmd/desktop/main.go](../../cmd/desktop/main.go)

函数/方法：`main`（31）；`run`（36）；`runDesktop`（144）；`marshalFrontendError`（226）。

### internal/app/application.go

[internal/app/application.go](../../internal/app/application.go)

类型：`Status`（49）；`Application`（75）。

函数/方法：`Bootstrap`（127）；`Application.Search`（412）；`Application.Lifecycle`（415）；`Application.Maintenance`（418）；`Application.Config`（421）；`Application.Logger`（426）；`Application.Credentials`（431）；`Application.Events`（436）；`Application.Workspaces`（441）；`Application.WorkspaceView`（448）；`Application.Sandbox`（453）；`Application.Permissions`（459）；`Application.Preferences`（464）；`Application.Approvals`（470）；`Application.Tools`（475）；`Application.Skills`（481）；`Application.MCP`（486）；`Application.Models`（491）；`Application.Agents`（496）；`Application.Sessions`（501）；`Application.Runtime`（506）；`Application.Tasks`（511）；`Application.Collaboration`（516）；`Application.Proactive`（521）；`Application.Notifications`（526）；`Application.Status`（531）；`Application.Shutdown`（573）；`Application.MCPConfiguration`（583）。

### internal/app/lifecycle.go

[internal/app/lifecycle.go](../../internal/app/lifecycle.go)

类型：`cleanupEntry`（18）；`lifecycle`（24）。

函数/方法：`lifecycle.add`（31）；`lifecycle.close`（36）。

### internal/app/modules.go

[internal/app/modules.go](../../internal/app/modules.go)

类型：`BootstrapOption`（14）；`bootstrapOptions`（15）；`mountedModule`（24）；`moduleManager`（28）。

函数/方法：`WithModules`（20）；`moduleManager.install`（30）；`moduleManager.start`（65）；`moduleManager.stop`（79）；`moduleManager.close`（92）。

### internal/app/runtime_event_reporter.go

[internal/app/runtime_event_reporter.go](../../internal/app/runtime_event_reporter.go)

类型：`runtimeEventPublisher`（12）；`runtimeEventReporter`（19）。

函数/方法：`newRuntimeEventReporter`（24）；`runtimeEventReporter.Report`（33）。

### internal/app/tools.go

[internal/app/tools.go](../../internal/app/tools.go)

类型：`toolDependencies`（43）。

函数/方法：`registerCollaborationTools`（24）；`buildToolRegistry`（56）。

### internal/atomicfile/json.go

[internal/atomicfile/json.go](../../internal/atomicfile/json.go)

函数/方法：`WriteJSON`（27）；`ReadJSON`（112）；`ensureRealDirectory`（172）；`validateRealDirectory`（194）；`writeFull`（208）；`replaceFile`（222）。

### internal/commandenv/command.go

[internal/commandenv/command.go](../../internal/commandenv/command.go)

函数/方法：`Path`（16）；`Validate`（47）；`Resolve`（59）；`Name`（91）。

### internal/component/module.go

[internal/component/module.go](../../internal/component/module.go)

类型：`Host`（17）；`Module`（28）；`Installer`（37）；`Provider`（43）；`Request`（53）；`Tool`（61）；`Contribution`（69）。

### internal/config/config.go

[internal/config/config.go](../../internal/config/config.go)

类型：`Config`（119）；`AppConfig`（131）；`LoggingConfig`（136）；`RuntimeConfig`（142）；`ContextConfig`（158）；`SkillConfig`（189）；`MCPConfig`（219）；`SecurityConfig`（242）；`SandboxConfig`（258）；`PermissionConfig`（273）；`Paths`（307）。

函数/方法：`Load`（348）；`loadFromHome`（359）；`resolvePaths`（407）；`ensureLayout`（452）；`ensurePrivateDirectory`（483）；`ensureDefaultConfig`（510）；`ensureDefaultTextFile`（518）；`setDefaults`（565）；`normalize`（606）；`validate`（616）。

### internal/config/configwrite.go

[internal/config/configwrite.go](../../internal/config/configwrite.go)

函数/方法：`saveConfigMutation`（21）；`validateWritableConfigFile`（111）；`replaceConfigFile`（134）。

### internal/config/permission.go

[internal/config/permission.go](../../internal/config/permission.go)

函数/方法：`NormalizePermissionConfig`（19）；`ValidatePermissionConfig`（32）；`SavePermissionConfig`（57）。

### internal/config/sandbox.go

[internal/config/sandbox.go](../../internal/config/sandbox.go)

函数/方法：`NormalizeSandboxConfig`（13）；`ValidateSandboxConfig`（21）；`SaveSandboxConfig`（46）；`SaveSandboxAndShellConfig`（62）。

### internal/config/tools.go

[internal/config/tools.go](../../internal/config/tools.go)

类型：`ToolConfig`（73）；`FileToolConfig`（81）；`WebSearchToolConfig`（105）；`WebFetchToolConfig`（123）；`CommandToolConfig`（148）。

函数/方法：`LoadToolConfig`（171）；`ToolConfig.Validate`（301）；`FileToolConfig.Validate`（322）；`WebSearchToolConfig.Validate`（383）；`WebFetchToolConfig.Validate`（443）；`CommandToolConfig.Validate`（500）；`SafeCommandEnvironment`（557）；`setToolDefaults`（636）。

### internal/eventbus/bus.go

[internal/eventbus/bus.go](../../internal/eventbus/bus.go)

类型：`Handler`（22）；`Bus`（37）。

函数/方法：`New`（48）；`Bus.Subscribe`（59）；`Bus.Publish`（128）；`callHandler`（198）；`Bus.Close`（221）。

### internal/instancelock/lock.go

[internal/instancelock/lock.go](../../internal/instancelock/lock.go)

类型：`Lock`（15）。

函数/方法：`Acquire`（23）；`Lock.Close`（59）。

### internal/instancelock/lock_unix.go

[internal/instancelock/lock_unix.go](../../internal/instancelock/lock_unix.go)

函数/方法：`tryLock`（12）。

### internal/instancelock/lock_windows.go

[internal/instancelock/lock_windows.go](../../internal/instancelock/lock_windows.go)

函数/方法：`tryLock`（10）。

### internal/logging/logger.go

[internal/logging/logger.go](../../internal/logging/logger.go)

类型：`Logger`（39）。

函数/方法：`NewBootstrap`（52）；`New`（72）；`parseLevel`（162）；`Logger.Slog`（190）；`Logger.Level`（195）；`Logger.Debug`（200）；`Logger.Info`（213）；`Logger.Warn`（226）；`Logger.Error`（239）；`Duration`（252）；`SafeErrorText`（260）；`RedactText`（278）；`sanitizeAttrs`（311）；`sanitizeAttr`（347）；`Logger.Close`（366）。

</details>

## 02 章节文件

实现说明：[02-runtime.md](02-runtime.md)。

| 文件 | 行数 | 内容摘要 | 主要声明（节选） |
| --- | ---: | --- | --- |
| [internal/runtime/approvals.go](../../internal/runtime/approvals.go) | 313 | `db0405f3ff08` | `Service.ResolveApproval`、`Service.resolveApproval`、`Service.registerInterruptedRun`、`Service.awaitApproval`、`signalApprovalDoneLocked`、`Service.finalizeWaitingCancellation` |
| [internal/runtime/capabilities.go](../../internal/runtime/capabilities.go) | 148 | `6bf3053d49cc` | `SkillSource`、`MCPSource`、`capabilitySet`、`capabilityAssembler`、`capabilityAssembler.validate`、`capabilityAssembler.resolve`、`capabilityAssembler.resolveSkills`、`capabilityAssembler.resolveMCP` |
| [internal/runtime/eino_builder.go](../../internal/runtime/eino_builder.go) | 57 | `e4cf74d0e689` | `buildChatModelAgent`、`Resolver.buildAgentHandlers` |
| [internal/runtime/events.go](../../internal/runtime/events.go) | 229 | `ae0033905ae2` | `Service.deltaEmitter`、`Service.finishRun`、`toolCallCount`、`runtimeUserVisibleError`、`Service.activeRunStatus`、`cloneRuntimeManifest`、`Service.publishEvent` |
| [internal/runtime/executor.go](../../internal/runtime/executor.go) | 660 | `f23b9c2ed86d` | `DeltaEmitter`、`Executor`、`NewExecutor`、`Executor.Execute`、`Executor.Resume`、`validateExecutionInput`、`buildRunner`、`Executor.consumeEvents` |
| [internal/runtime/extensions.go](../../internal/runtime/extensions.go) | 188 | `7fdd6b5f79e5` | `CapabilitySummary`、`extensionRegistry`、`registeredProvider`、`extensionRegistry.register`、`extensionRegistry.snapshot`、`Resolver.RegisterCapabilityProvider`、`extensionRegistry.resolve`、`cloneCapabilityScope` |
| [internal/runtime/limits.go](../../internal/runtime/limits.go) | 96 | `f343dea2a6de` | `executionLimitState`、`executionLimitState.beforeModelCall`、`executionLimitState.beforeToolCall`、`reserveCall`、`executionLimitState.checkTokens`、`executionLimitState.addTokens`、`prepareExecutionLimitState`、`configureExecutionLimits` |
| [internal/runtime/model_accounting.go](../../internal/runtime/model_accounting.go) | 120 | `e955f8c29d72` | `executionSnapshotContextKey`、`trackedChatModel`、`modelUsageAccumulator`、`limitStateFromContext`、`TrackAuxiliaryModel`、`trackModel`、`trackAuxiliaryModel`、`trackedChatModel.WithTools` |
| [internal/runtime/model_roles.go](../../internal/runtime/model_roles.go) | 213 | `fa299949e06a` | `turnInputRequirements`、`resolvedModelRoles`、`ModelCapabilityError`、`ModelCapabilityError.Error`、`ModelCapabilityError.Unwrap`、`Resolver.resolveModelRoles`、`Resolver.currentTurnRequirements`、`requirementsFromMessage` |
| [internal/runtime/operations.go](../../internal/runtime/operations.go) | 169 | `5b8fb3a0e2bf` | `Service.DeleteSession`、`Service.DeleteAgent`、`Service.beginOperation`、`Service.reserveSession`、`Service.ensureSessionUnreserved`、`Service.reserveAgentSession`、`Service.releaseReservation` |
| [internal/runtime/prompt.go](../../internal/runtime/prompt.go) | 173 | `fa0590fec428` | `buildRuntimeInstruction`、`appendMCPUnavailableInstruction`、`hasTool`、`escapePromptText` |
| [internal/runtime/provider_error.go](../../internal/runtime/provider_error.go) | 76 | `08a1b2db50c9` | `classifyProviderError`、`isProviderContentBlockedText` |
| [internal/runtime/reasoning_policy.go](../../internal/runtime/reasoning_policy.go) | 15 | `d5ea10888792` | `reasoningReplayPolicyForProvider` |
| [internal/runtime/resolver.go](../../internal/runtime/resolver.go) | 785 | `fc9a0ab8af29` | `modelSnapshotResolver`、`resolvedContextBase`、`Resolver`、`Resolver.BuildChildAgent`、`NewResolver`、`Resolver.ResolveTurn`、`Resolver.buildContextSnapshot`、`Resolver.alignModelToContext` |
| [internal/runtime/runs.go](../../internal/runtime/runs.go) | 231 | `808ac5d6e352` | `Service.CancelTurn`、`Service.executeTurn`、`Service.resumeTurn`、`Service.handleExecutionOutcome`、`Service.completeTurn`、`Service.cleanupRun`、`Service.notifyRunFinished`、`Service.Close` |
| [internal/runtime/service.go](../../internal/runtime/service.go) | 376 | `f6ee2c71feab` | `activeRun`、`RunLifecycleObserver`、`Service`、`Service.AddRunLifecycleObserver`、`NewService`、`Service.StartTurn`、`Service.ContextStatus`、`Service.ContextOverview` |
| [internal/runtime/types.go](../../internal/runtime/types.go) | 367 | `5c78492ba0d7` | `EventType`、`Event`、`EventReporter`、`SessionWriter`、`RuntimeManifest`、`RuntimeModelRolesManifest`、`RuntimeWorkspaceManifest`、`RuntimeSandboxManifest` |

<details>
<summary>本章 Go 顶层函数与类型定位（自动扫描）</summary>

### internal/runtime/approvals.go

[internal/runtime/approvals.go](../../internal/runtime/approvals.go)

函数/方法：`Service.ResolveApproval`（19）；`Service.resolveApproval`（40）；`Service.registerInterruptedRun`（114）；`Service.awaitApproval`（179）；`signalApprovalDoneLocked`（286）；`Service.finalizeWaitingCancellation`（295）。

### internal/runtime/capabilities.go

[internal/runtime/capabilities.go](../../internal/runtime/capabilities.go)

类型：`SkillSource`（18）；`MCPSource`（24）；`capabilitySet`（31）；`capabilityAssembler`（44）。

函数/方法：`capabilityAssembler.validate`（51）；`capabilityAssembler.resolve`（59）；`capabilityAssembler.resolveSkills`（127）；`capabilityAssembler.resolveMCP`（137）。

### internal/runtime/eino_builder.go

[internal/runtime/eino_builder.go](../../internal/runtime/eino_builder.go)

函数/方法：`buildChatModelAgent`（18）；`Resolver.buildAgentHandlers`（37）。

### internal/runtime/events.go

[internal/runtime/events.go](../../internal/runtime/events.go)

函数/方法：`Service.deltaEmitter`（18）；`Service.finishRun`（40）；`toolCallCount`（77）；`runtimeUserVisibleError`（88）；`Service.activeRunStatus`（154）；`cloneRuntimeManifest`（188）；`Service.publishEvent`（209）。

### internal/runtime/executor.go

[internal/runtime/executor.go](../../internal/runtime/executor.go)

类型：`DeltaEmitter`（26）；`Executor`（31）。

函数/方法：`NewExecutor`（34）；`Executor.Execute`（43）；`Executor.Resume`（64）；`validateExecutionInput`（94）；`buildRunner`（116）；`Executor.consumeEvents`（129）；`extractApprovalInterrupt`（225）；`formatRecoverableToolError`（258）；`isRecoverableToolErrorResult`（265）；`buildToolLifecycleMiddleware`（274）；`reportToolLifecycleEvent`（360）；`materializeAssistantOutput`（379）；`emitAssistantDeltas`（431）；`materializeMessageOutput`（444）；`concatMessagesBestEffort`（490）；`assistantBlockedByFinishReason`（521）；`partialAssistantForPersistence`（536）；`persistAssistantMessage`（549）；`persistCompletedTool`（576）；`assistantText`（610）；`assistantReasoning`（628）；`extraString`（646）。

### internal/runtime/extensions.go

[internal/runtime/extensions.go](../../internal/runtime/extensions.go)

类型：`CapabilitySummary`（20）；`extensionRegistry`（28）；`registeredProvider`（34）。

函数/方法：`extensionRegistry.register`（39）；`extensionRegistry.snapshot`（65）；`Resolver.RegisterCapabilityProvider`（82）；`extensionRegistry.resolve`（89）；`cloneCapabilityScope`（173）。

### internal/runtime/limits.go

[internal/runtime/limits.go](../../internal/runtime/limits.go)

类型：`executionLimitState`（11）。

函数/方法：`executionLimitState.beforeModelCall`（20）；`executionLimitState.beforeToolCall`（30）；`reserveCall`（41）；`executionLimitState.checkTokens`（53）；`executionLimitState.addTokens`（62）；`prepareExecutionLimitState`（68）；`configureExecutionLimits`（78）。

### internal/runtime/model_accounting.go

[internal/runtime/model_accounting.go](../../internal/runtime/model_accounting.go)

类型：`executionSnapshotContextKey`（12）；`trackedChatModel`（33）；`modelUsageAccumulator`（100）。

函数/方法：`limitStateFromContext`（14）；`TrackAuxiliaryModel`（24）；`trackModel`（38）；`trackAuxiliaryModel`（48）；`trackedChatModel.WithTools`（56）；`trackedChatModel.before`（64）；`trackedChatModel.Generate`（75）；`trackedChatModel.Stream`（85）；`modelUsageAccumulator.record`（102）。

### internal/runtime/model_roles.go

[internal/runtime/model_roles.go](../../internal/runtime/model_roles.go)

类型：`turnInputRequirements`（23）；`resolvedModelRoles`（28）；`ModelCapabilityError`（37）。

函数/方法：`ModelCapabilityError.Error`（44）；`ModelCapabilityError.Unwrap`（66）；`Resolver.resolveModelRoles`（68）；`Resolver.currentTurnRequirements`（135）；`requirementsFromMessage`（146）；`requirementsFromMessageWithImages`（150）；`stringMessagePartExtra`（170）；`requirementsFromMessages`（181）；`capabilityError`（192）；`validateToolCapability`（199）；`compactionModel`（208）。

### internal/runtime/operations.go

[internal/runtime/operations.go](../../internal/runtime/operations.go)

函数/方法：`Service.DeleteSession`（16）；`Service.DeleteAgent`（45）；`Service.beginOperation`（99）；`Service.reserveSession`（121）；`Service.ensureSessionUnreserved`（125）；`Service.reserveAgentSession`（137）；`Service.releaseReservation`（159）。

### internal/runtime/prompt.go

[internal/runtime/prompt.go](../../internal/runtime/prompt.go)

函数/方法：`buildRuntimeInstruction`（26）；`appendMCPUnavailableInstruction`（123）；`hasTool`（156）；`escapePromptText`（166）。

### internal/runtime/provider_error.go

[internal/runtime/provider_error.go](../../internal/runtime/provider_error.go)

函数/方法：`classifyProviderError`（28）；`isProviderContentBlockedText`（48）。

### internal/runtime/reasoning_policy.go

[internal/runtime/reasoning_policy.go](../../internal/runtime/reasoning_policy.go)

函数/方法：`reasoningReplayPolicyForProvider`（10）。

### internal/runtime/resolver.go

[internal/runtime/resolver.go](../../internal/runtime/resolver.go)

类型：`modelSnapshotResolver`（31）；`resolvedContextBase`（136）；`Resolver`（163）。

函数/方法：`Resolver.BuildChildAgent`（41）；`NewResolver`（178）；`Resolver.ResolveTurn`（216）；`Resolver.buildContextSnapshot`（345）；`Resolver.alignModelToContext`（361）；`Resolver.ContextOverview`（381）；`Resolver.ContextStatus`（399）；`Resolver.ManualCompact`（408）；`Resolver.MaintainAfterTurn`（441）；`Resolver.observeInitialPromptUsage`（456）；`Resolver.resolveContextBase`（480）；`runtimeManifestFromBase`（567）；`uniqueSortedStrings`（620）；`Resolver.withPersonalMemory`（640）；`Resolver.withResponseLanguage`（665）；`cloneOptionalStrings`（680）；`mcpSelectionMap`（687）；`mergeRuntimeDescriptors`（697）；`mergeRuntimeTools`（714）；`toolResultMaxCharsForContext`（723）；`Resolver.OperationTimeout`（743）；`Resolver.Validate`（752）。

### internal/runtime/runs.go

[internal/runtime/runs.go](../../internal/runtime/runs.go)

函数/方法：`Service.CancelTurn`（20）；`Service.executeTurn`（71）；`Service.resumeTurn`（104）；`Service.handleExecutionOutcome`（121）；`Service.completeTurn`（141）；`Service.cleanupRun`（175）；`Service.notifyRunFinished`（197）；`Service.Close`（207）。

### internal/runtime/service.go

[internal/runtime/service.go](../../internal/runtime/service.go)

类型：`activeRun`（25）；`RunLifecycleObserver`（46）；`Service`（64）。

函数/方法：`Service.AddRunLifecycleObserver`（92）；`NewService`（102）；`Service.StartTurn`（133）；`Service.ContextStatus`（284）；`Service.ContextOverview`（307）；`Service.ManualCompact`（332）。

### internal/runtime/types.go

[internal/runtime/types.go](../../internal/runtime/types.go)

类型：`EventType`（37）；`Event`（58）；`EventReporter`（104）；`SessionWriter`（111）；`RuntimeManifest`（123）；`RuntimeModelRolesManifest`（154）；`RuntimeWorkspaceManifest`（163）；`RuntimeSandboxManifest`（169）；`RunPhase`（181）；`ActiveRunStatus`（191）；`ContextOverview`（210）；`Snapshot`（218）；`StartTurnInput`（293）；`ExecutionLimits`（302）；`ResolveTurnOptions`（311）；`StartTurnResult`（316）；`ManualCompactionResult`（332）；`ExecutionResult`（341）；`InterruptedExecution`（352）；`ResolveApprovalInput`（358）；`ResolveApprovalResult`（365）。

</details>

## 03 章节文件

实现说明：[03-context.md](03-context.md)。

| 文件 | 行数 | 内容摘要 | 主要声明（节选） |
| --- | ---: | --- | --- |
| [internal/avatar/avatar.go](../../internal/avatar/avatar.go) | 55 | `0ce9fab6df86` | `NormalizeDataURL` |
| [internal/contextartifact/store.go](../../internal/contextartifact/store.go) | 121 | `15c04afb35bf` | `SessionDirectoryResolver`、`Artifact`、`Store`、`NewStore`、`Store.Archive`、`Store.Read`、`Store.path`、`Store.ReadContextArtifact` |
| [internal/contextengine/budget.go](../../internal/contextengine/budget.go) | 116 | `0afdd6cbbd99` | `CalculateBudget`、`ResolveBudgetForFixedContext`、`maxInt`、`minInt` |
| [internal/contextengine/compactor.go](../../internal/contextengine/compactor.go) | 113 | `80526d00a952` | `Engine.Compact`、`Engine.commitSummary`、`sameMessageIdentity` |
| [internal/contextengine/engine.go](../../internal/contextengine/engine.go) | 324 | `a4b61a7c6535` | `SessionRepository`、`Engine`、`usageBreakdown`、`NewEngine`、`Engine.Build`、`Engine.buildFromDocument`、`assemblyFromProjection`、`usageFromBudget` |
| [internal/contextengine/estimator.go](../../internal/contextengine/estimator.go) | 260 | `a1bfdecd5721` | `Estimator`、`ApproxEstimator`、`UsageCalibrator`、`NewApproxEstimator`、`ApproxEstimator.calibrationFactor`、`ApproxEstimator.ObservePromptUsage`、`ApproxEstimator.EstimateText`、`ApproxEstimator.EstimateMessage` |
| [internal/contextengine/middleware.go](../../internal/contextengine/middleware.go) | 399 | `c2f8e7e16ab1` | `MidRunCompactor`、`pendingSummary`、`ContextMiddlewareConfig`、`NewMidRunCompactor`、`MidRunCompactor.Count`、`MidRunCompactor.BeforeModelRewriteState`、`MidRunCompactor.operationContext`、`MidRunCompactor.Summarize` |
| [internal/contextengine/projection.go](../../internal/contextengine/projection.go) | 308 | `ee9b61bb799d` | `projectionResult`、`projectActiveBranch`、`compactionCheckpointText`、`latestUserMessageIndex`、`reasoningPolicyForIndex`、`compactionGeneration`、`latestCompaction`、`findEntryIndex` |
| [internal/contextengine/serializer.go](../../internal/contextengine/serializer.go) | 81 | `0d4495891091` | `truncateText`、`summarizeToolArguments`、`sanitizeCompactionArgument`、`compactionArgumentShouldOmit` |
| [internal/contextengine/summary_input.go](../../internal/contextengine/summary_input.go) | 63 | `46927d43d0b0` | `serializeMessagesForCheckpoint` |
| [internal/contextengine/types.go](../../internal/contextengine/types.go) | 262 | `1259c228d0e1` | `ReasoningReplayPolicy`、`CompactionReason`、`Budget`、`Usage`、`WindowState`、`Assembly`、`Snapshot`、`BuildRequest` |
| [internal/documenttext/extract.go](../../internal/documenttext/extract.go) | 194 | `305471fc7ba7` | `boundedWriter`、`init`、`MIMEForName`、`Validate`、`Extract`、`parseInWorker`、`boundedWriter.Write`、`boundedWriter.String` |
| [internal/multimodal/replay.go](../../internal/multimodal/replay.go) | 93 | `b32957dcd2df` | `ImageReplayMask`、`HistoricalImagePlaceholder`、`extraString`、`FileReplayMask`、`HistoricalFilePlaceholder` |
| [internal/multimodal/vision_bridge.go](../../internal/multimodal/vision_bridge.go) | 237 | `690a347b575f` | `BridgeImagesForTextModel`、`buildVisionRequestParts`、`latestUserText`、`assistantVisibleText`、`cloneMessages`、`appendTextToUserMessage`、`visionImagePlaceholder`、`formatVisionObservation` |
| [internal/preferences/memory.go](../../internal/preferences/memory.go) | 159 | `58c8fe0066de` | `PersonalMemory`、`memoryDocument`、`Store.memoryPath`、`Store.ListMemories`、`Store.AddMemory`、`Store.AddMemoryWithSource`、`Store.UpdateMemory`、`Store.DeleteMemory` |
| [internal/preferences/store.go](../../internal/preferences/store.go) | 148 | `5108d32d3090` | `UserProfile`、`document`、`Store`、`SupportedLanguage`、`NewStore`、`Store.Get`、`Store.Update`、`Store.SetLanguage` |

<details>
<summary>本章 Go 顶层函数与类型定位（自动扫描）</summary>

### internal/avatar/avatar.go

[internal/avatar/avatar.go](../../internal/avatar/avatar.go)

函数/方法：`NormalizeDataURL`（23）。

### internal/contextartifact/store.go

[internal/contextartifact/store.go](../../internal/contextartifact/store.go)

类型：`SessionDirectoryResolver`（22）；`Artifact`（28）；`Store`（38）。

函数/方法：`NewStore`（43）；`Store.Archive`（50）；`Store.Read`（78）；`Store.path`（96）；`Store.ReadContextArtifact`（115）。

### internal/contextengine/budget.go

[internal/contextengine/budget.go](../../internal/contextengine/budget.go)

函数/方法：`CalculateBudget`（14）；`ResolveBudgetForFixedContext`（71）；`maxInt`（104）；`minInt`（111）。

### internal/contextengine/compactor.go

[internal/contextengine/compactor.go](../../internal/contextengine/compactor.go)

函数/方法：`Engine.Compact`（13）；`Engine.commitSummary`（42）；`sameMessageIdentity`（97）。

### internal/contextengine/engine.go

[internal/contextengine/engine.go](../../internal/contextengine/engine.go)

类型：`SessionRepository`（23）；`Engine`（38）；`usageBreakdown`（188）。

函数/方法：`NewEngine`（49）；`Engine.Build`（73）；`Engine.buildFromDocument`（111）；`assemblyFromProjection`（157）；`usageFromBudget`（195）；`Engine.Config`（234）；`Engine.EstimateTools`（242）；`Engine.EstimateMessages`（248）；`Engine.ObservePromptUsage`（258）；`Engine.ObserveSessionPromptUsage`（268）；`Engine.BudgetForModel`（280）；`Engine.NewMidRunHandler`（288）。

### internal/contextengine/estimator.go

[internal/contextengine/estimator.go](../../internal/contextengine/estimator.go)

类型：`Estimator`（23）；`ApproxEstimator`（38）；`UsageCalibrator`（45）。

函数/方法：`NewApproxEstimator`（50）；`ApproxEstimator.calibrationFactor`（54）；`ApproxEstimator.ObservePromptUsage`（66）；`ApproxEstimator.EstimateText`（96）；`ApproxEstimator.EstimateMessage`（129）；`ApproxEstimator.estimateMessageWithAttachments`（133）；`extraStringValue`（197）；`ApproxEstimator.EstimateMessages`（206）；`ApproxEstimator.EstimateMessageCosts`（216）；`ApproxEstimator.EstimateTools`（231）。

### internal/contextengine/middleware.go

[internal/contextengine/middleware.go](../../internal/contextengine/middleware.go)

类型：`MidRunCompactor`（22）；`pendingSummary`（34）；`ContextMiddlewareConfig`（40）。

函数/方法：`NewMidRunCompactor`（55）；`MidRunCompactor.Count`（82）；`MidRunCompactor.BeforeModelRewriteState`（100）；`MidRunCompactor.operationContext`（148）；`MidRunCompactor.Summarize`（157）；`MidRunCompactor.FirstInputTokens`（165）；`MidRunCompactor.captureRequest`（171）；`MidRunCompactor.summaryBoundary`（189）；`MidRunCompactor.modelInput`（235）；`MidRunCompactor.finalize`（252）；`preserveSkillDefinitions`（300）；`MidRunCompactor.Commit`（337）；`isCompactionCheckpointMessage`（350）；`runtimeToolTransactionStart`（354）；`messageVisibleText`（377）。

### internal/contextengine/projection.go

[internal/contextengine/projection.go](../../internal/contextengine/projection.go)

类型：`projectionResult`（23）。

函数/方法：`projectActiveBranch`（40）；`compactionCheckpointText`（161）；`latestUserMessageIndex`（175）；`reasoningPolicyForIndex`（185）；`compactionGeneration`（195）；`latestCompaction`（208）；`findEntryIndex`（219）；`applyReasoningReplayPolicy`（233）；`validateProjectedToolTransactions`（262）；`cloneExtra`（302）。

### internal/contextengine/serializer.go

[internal/contextengine/serializer.go](../../internal/contextengine/serializer.go)

函数/方法：`truncateText`（23）；`summarizeToolArguments`（34）；`sanitizeCompactionArgument`（47）；`compactionArgumentShouldOmit`（70）。

### internal/contextengine/summary_input.go

[internal/contextengine/summary_input.go](../../internal/contextengine/summary_input.go)

函数/方法：`serializeMessagesForCheckpoint`（10）。

### internal/contextengine/types.go

[internal/contextengine/types.go](../../internal/contextengine/types.go)

类型：`ReasoningReplayPolicy`（15）；`CompactionReason`（27）；`Budget`（41）；`Usage`（77）；`WindowState`（139）；`Assembly`（151）；`Snapshot`（175）；`BuildRequest`（192）；`CompactRequest`（207）；`CompactResult`（236）。

### internal/documenttext/extract.go

[internal/documenttext/extract.go](../../internal/documenttext/extract.go)

类型：`boundedWriter`（149）。

函数/方法：`init`（28）；`MIMEForName`（54）；`Validate`（57）；`Extract`（83）；`parseInWorker`（123）；`boundedWriter.Write`（155）；`boundedWriter.String`（170）；`preflightOffice`（172）。

### internal/multimodal/replay.go

[internal/multimodal/replay.go](../../internal/multimodal/replay.go)

函数/方法：`ImageReplayMask`（15）；`HistoricalImagePlaceholder`（31）；`extraString`（48）；`FileReplayMask`（59）；`HistoricalFilePlaceholder`（73）。

### internal/multimodal/vision_bridge.go

[internal/multimodal/vision_bridge.go](../../internal/multimodal/vision_bridge.go)

函数/方法：`BridgeImagesForTextModel`（32）；`buildVisionRequestParts`（104）；`latestUserText`（136）；`assistantVisibleText`（156）；`cloneMessages`（172）；`appendTextToUserMessage`（188）；`visionImagePlaceholder`（210）；`formatVisionObservation`（222）；`truncateRunes`（231）。

### internal/preferences/memory.go

[internal/preferences/memory.go](../../internal/preferences/memory.go)

类型：`PersonalMemory`（21）；`memoryDocument`（31）。

函数/方法：`Store.memoryPath`（36）；`Store.ListMemories`（41）；`Store.AddMemory`（51）；`Store.AddMemoryWithSource`（57）；`Store.UpdateMemory`（84）；`Store.DeleteMemory`（109）；`Store.readMemories`（126）；`normalizeMemoryText`（153）。

### internal/preferences/store.go

[internal/preferences/store.go](../../internal/preferences/store.go)

类型：`UserProfile`（17）；`document`（34）；`Store`（39）。

函数/方法：`SupportedLanguage`（25）；`NewStore`（44）；`Store.Get`（59）；`Store.Update`（69）；`Store.SetLanguage`（101）；`Store.readLocked`（118）。

</details>

## 04 章节文件

实现说明：[04-data.md](04-data.md)。

| 文件 | 行数 | 内容摘要 | 主要声明（节选） |
| --- | ---: | --- | --- |
| [internal/agents/errors.go](../../internal/agents/errors.go) | 11 | `b356ff5ccedc` | 组件/配置/样式 |
| [internal/agents/service.go](../../internal/agents/service.go) | 1259 | `c82ecd2db4a8` | `ServiceOption`、`SkillSelectionValidator`、`MCPSelectionValidator`、`Service`、`normalizedInput`、`WithWorkspaceManager`、`WithSkillCatalog`、`WithMCPCatalog` |
| [internal/agents/store.go](../../internal/agents/store.go) | 584 | `c21430f57268` | `agentDocument`、`agentDeletionDocument`、`Store`、`NewStore`、`Store.Create`、`Store.Mutate`、`Store.updateLocked`、`Store.Get` |
| [internal/agents/types.go](../../internal/agents/types.go) | 148 | `b2c924387a66` | `ModelRoles`、`Agent`、`AgentInfo`、`DeletionState`、`CreateInput`、`UpdateInput` |
| [internal/sessions/attachments.go](../../internal/sessions/attachments.go) | 626 | `dbf35b145182` | `Service.appendUserInput`、`Service.renameDefaultSessionFromFirstInput`、`Service.userInputMatchesStoredMessage`、`Service.HydrateMessages`、`Service.hydrateUserAttachments`、`Service.hydrateUserAttachmentsWithBudget`、`Service.hydrateCommon`、`Service.readAttachmentBytes` |
| [internal/sessions/catalog.go](../../internal/sessions/catalog.go) | 186 | `a3473c8d7c2a` | `sessionCatalog`、`openSessionCatalog`、`sessionCatalog.close`、`sessionCatalog.get`、`sessionCatalog.list`、`sessionCatalog.insert`、`sessionCatalog.rename`、`sessionCatalog.setArchived` |
| [internal/sessions/errors.go](../../internal/sessions/errors.go) | 18 | `80c8a7225cff` | 组件/配置/样式 |
| [internal/sessions/service.go](../../internal/sessions/service.go) | 423 | `797d32125f8a` | `Service`、`NewService`、`Service.List`、`Service.ListIDs`、`Service.PurgeAgentMetadata`、`Service.Get`、`Service.Create`、`Service.Rename` |
| [internal/sessions/store.go](../../internal/sessions/store.go) | 596 | `41a5635c7451` | `Store`、`sessionLockEntry`、`sessionLockRegistry`、`NewStore`、`Store.Close`、`Store.ListSessions`、`Store.ListSessionIDs`、`Store.GetSession` |
| [internal/sessions/types.go](../../internal/sessions/types.go) | 134 | `83f79b195b15` | `Session`、`SessionIssue`、`Message`、`MessagePage`、`CreateSessionInput`、`AssistantPersistence`、`ToolResultPersistence`、`UserInput` |
| [internal/transcript/cache.go](../../internal/transcript/cache.go) | 283 | `768404ce552a` | `documentCache`、`documentCacheItem`、`newDocumentCache`、`documentCache.readOnly`、`documentCache.putOwned`、`documentCache.advance`、`documentCache.messagePage`、`documentCache.invalidate` |
| [internal/transcript/codec.go](../../internal/transcript/codec.go) | 630 | `f94017beab11` | `EncodeOptions`、`DecodedMessage`、`EncodeMessage`、`DecodeMessage`、`encodeUserMessageContent`、`decodeUserMessage`、`extraInt64`、`encodeTextOnlyMessageContent` |
| [internal/transcript/errors.go](../../internal/transcript/errors.go) | 63 | `1118d53a28dd` | `CorruptionError`、`CorruptionError.Error`、`CorruptionError.Unwrap` |
| [internal/transcript/history_index.go](../../internal/transcript/history_index.go) | 136 | `3c7572912cdd` | `Store.VisitActiveBranchReverse`、`Store.ReadActiveBranchRange` |
| [internal/transcript/location_index.go](../../internal/transcript/location_index.go) | 550 | `ae3f2ee7fbe9` | `locationHeader`、`locationRecord`、`locationIndex`、`locationCache`、`newLocationCache`、`locationCache.get`、`locationCache.put`、`locationCache.invalidate` |
| [internal/transcript/store.go](../../internal/transcript/store.go) | 1467 | `f8b2cd052704` | `Store`、`lockEntry`、`lockRegistry`、`NewStore`、`Store.AgentsRoot`、`Store.CreateSession`、`Store.AppendMessage`、`Store.AppendCompaction` |
| [internal/transcript/tool_result.go](../../internal/transcript/tool_result.go) | 25 | `50628e04ec87` | `ToolResultSucceeded`、`ToolResultRejected` |
| [internal/transcript/types.go](../../internal/transcript/types.go) | 394 | `2f29b9d81d62` | `EntryType`、`MessageRole`、`ContentType`、`StopReason`、`SessionHeader`、`ContentBlock`、`UsageCost`、`Usage` |

<details>
<summary>本章 Go 顶层函数与类型定位（自动扫描）</summary>

### internal/agents/errors.go

[internal/agents/errors.go](../../internal/agents/errors.go)

### internal/agents/service.go

[internal/agents/service.go](../../internal/agents/service.go)

类型：`ServiceOption`（32）；`SkillSelectionValidator`（40）；`MCPSelectionValidator`（48）；`Service`（95）；`normalizedInput`（777）。

函数/方法：`WithWorkspaceManager`（56）；`WithSkillCatalog`（71）；`WithMCPCatalog`（78）；`NewService`（114）；`Service.List`（142）；`Service.Get`（154）；`Service.WithActiveAgent`（190）；`Service.Create`（224）；`Service.Update`（437）；`Service.mutateProfile`（624）；`Service.UpdateProfile`（632）；`Service.SetModel`（648）；`Service.SetModelRoles`（655）；`Service.SetSkills`（662）；`Service.UpdateSecurity`（669）；`Service.Delete`（691）；`Service.RecoverDeletions`（705）；`Service.resumeDeletion`（718）；`Service.enrichModelDisplayNames`（744）；`Service.normalizeInput`（791）；`Service.normalizeEnabledSkills`（933）；`Service.normalizeEnabledMCPTools`（951）；`cloneMCPSelections`（968）；`normalizeBuiltinToolSelection`（981）；`normalizeSandboxPolicy`（1011）；`normalizeSandboxPaths`（1034）；`cloneStringsPreserveNil`（1062）；`Service.SetMCPToolsForAgent`（1070）；`Service.CountAgentsUsingMCPServer`（1080）；`Service.EnableSkillForAgent`（1102）；`Service.DisableSkillForAgent`（1124）；`Service.normalizeAndValidateModelRoles`（1145）；`Service.ensureModelUsable`（1162）；`Service.rollbackCreatedAgent`（1219）；`Service.AgentsUsingSkill`（1237）。

### internal/agents/store.go

[internal/agents/store.go](../../internal/agents/store.go)

类型：`agentDocument`（26）；`agentDeletionDocument`（32）；`Store`（51）。

函数/方法：`NewStore`（60）；`Store.Create`（100）；`Store.Mutate`（143）；`Store.updateLocked`（169）；`Store.Get`（209）；`Store.List`（234）；`Store.BeginDelete`（295）；`Store.ListDeleting`（338）；`Store.DeleteMarked`（374）；`Store.CountAgentsByModel`（407）；`Store.readAgentLocked`（429）；`Store.readDeletionLocked`（461）；`deletionMarkerExists`（481）；`Store.configPath`（495）；`ensureRealDirectory`（518）；`validateRealDirectory`（543）；`validateStoreContext`（561）；`validateAgentID`（571）。

### internal/agents/types.go

[internal/agents/types.go](../../internal/agents/types.go)

类型：`ModelRoles`（13）；`Agent`（23）；`AgentInfo`（68）；`DeletionState`（78）；`CreateInput`（89）；`UpdateInput`（119）。

### internal/sessions/attachments.go

[internal/sessions/attachments.go](../../internal/sessions/attachments.go)

函数/方法：`Service.appendUserInput`（33）；`Service.renameDefaultSessionFromFirstInput`（165）；`Service.userInputMatchesStoredMessage`（205）；`Service.HydrateMessages`（296）；`Service.hydrateUserAttachments`（319）；`Service.hydrateUserAttachmentsWithBudget`（324）；`Service.hydrateCommon`（384）；`Service.readAttachmentBytes`（413）；`normalizeAttachmentMIME`（462）；`validateImageAttachment`（473）；`extractTextAttachment`（485）；`textAttachmentTypeSupported`（509）；`formatExtractedFileForModel`（533）；`ensureAttachmentDirectory`（537）；`validateAttachmentDirectory`（557）；`writeAttachmentFile`（568）；`Service.ReadAttachment`（586）；`Service.SaveToolImage`（592）；`transcriptEncodeOptions`（618）；`stringExtra`（620）。

### internal/sessions/catalog.go

[internal/sessions/catalog.go](../../internal/sessions/catalog.go)

类型：`sessionCatalog`（18）。

函数/方法：`openSessionCatalog`（20）；`sessionCatalog.close`（72）；`sessionCatalog.get`（74）；`sessionCatalog.list`（92）；`sessionCatalog.insert`（114）；`sessionCatalog.rename`（120）；`sessionCatalog.setArchived`（125）；`existingSessionResult`（130）；`sessionCatalog.touch`（144）；`sessionCatalog.delete`（149）；`sessionCatalog.deleteAgent`（154）；`sessionCatalog.prune`（159）。

### internal/sessions/errors.go

[internal/sessions/errors.go](../../internal/sessions/errors.go)

### internal/sessions/service.go

[internal/sessions/service.go](../../internal/sessions/service.go)

类型：`Service`（36）。

函数/方法：`NewService`（47）；`Service.List`（62）；`Service.ListIDs`（77）；`Service.PurgeAgentMetadata`（86）；`Service.Get`（91）；`Service.Create`（110）；`Service.Rename`（162）；`Service.SetArchived`（178）；`Service.Delete`（189）；`Service.AppendUserMessage`（212）；`Service.AppendUserInput`（217）；`Service.PrepareUserMessage`（223）；`Service.AppendAssistantMessage`（252）；`Service.AppendToolResult`（277）；`Service.Messages`（297）；`Service.MessagePage`（311）；`Service.LoadTranscript`（333）；`Service.LoadContextTranscript`（342）；`Service.VisitActiveBranchReverse`（346）；`Service.ReadActiveBranchRange`（350）；`Service.AppendCompaction`（355）；`Service.SessionDirectory`（379）；`Service.append`（383）；`normalizeTitle`（405）；`cloneRawJSON`（416）。

### internal/sessions/store.go

[internal/sessions/store.go](../../internal/sessions/store.go)

类型：`Store`（28）；`sessionLockEntry`（40）；`sessionLockRegistry`（46）。

函数/方法：`NewStore`（53）；`Store.Close`（81）；`Store.ListSessions`（84）；`Store.ListSessionIDs`（91）；`Store.GetSession`（104）；`Store.Issues`（130）；`Store.CreateSession`（147）；`Store.RenameSession`（189）；`Store.SetArchived`（202）；`Store.DeleteSession`（213）；`Store.PurgeAgentMetadata`（235）；`Store.AppendMessage`（254）；`Store.ListMessages`（302）；`Store.ListMessagePage`（314）；`Store.LoadTranscript`（362）；`Store.LoadContextTranscript`（376）；`Store.VisitActiveBranchReverse`（388）；`Store.ReadActiveBranchRange`（396）；`Store.AppendCompaction`（409）；`Store.SessionDirectory`（432）；`Store.rebuildIndex`（451）；`Store.recoverSessionFromHeader`（530）；`Store.clearIssue`（553）；`Store.lookupIssue`（559）；`translateTranscriptError`（566）；`string.func`（576）。

### internal/sessions/types.go

[internal/sessions/types.go](../../internal/sessions/types.go)

类型：`Session`（16）；`SessionIssue`（33）；`Message`（46）；`MessagePage`（68）；`CreateSessionInput`（79）；`AssistantPersistence`（89）；`ToolResultPersistence`（108）；`UserInput`（115）；`AttachmentInput`（121）；`Attachment`（128）。

### internal/transcript/cache.go

[internal/transcript/cache.go](../../internal/transcript/cache.go)

类型：`documentCache`（18）；`documentCacheItem`（29）。

函数/方法：`newDocumentCache`（41）；`documentCache.readOnly`（52）；`documentCache.putOwned`（72）；`documentCache.advance`（98）；`documentCache.messagePage`（135）；`documentCache.invalidate`（165）；`documentCache.evictLocked`（176）；`documentCache.removeLocked`（186）；`sameTranscriptFile`（192）；`indexMessages`（199）；`cloneDocument`（212）；`cloneEntries`（238）；`cloneEntry`（249）；`cloneStringPointer`（277）。

### internal/transcript/codec.go

[internal/transcript/codec.go](../../internal/transcript/codec.go)

类型：`EncodeOptions`（19）；`DecodedMessage`（44）。

函数/方法：`EncodeMessage`（55）；`DecodeMessage`（153）；`encodeUserMessageContent`（276）；`decodeUserMessage`（333）；`extraInt64`（375）；`encodeTextOnlyMessageContent`（398）；`encodeAssistantContent`（408）；`encodeUsage`（485）；`decodeResponseMeta`（505）；`normalizeStopReason`（529）；`stopReasonFromRaw`（542）；`einoFinishReason`（559）；`wireTextContent`（576）；`normalizeToolArguments`（590）；`extraString`（606）；`cloneRawJSON`（623）。

### internal/transcript/errors.go

[internal/transcript/errors.go](../../internal/transcript/errors.go)

类型：`CorruptionError`（37）。

函数/方法：`CorruptionError.Error`（46）；`CorruptionError.Unwrap`（61）。

### internal/transcript/history_index.go

[internal/transcript/history_index.go](../../internal/transcript/history_index.go)

函数/方法：`Store.VisitActiveBranchReverse`（12）；`Store.ReadActiveBranchRange`（66）。

### internal/transcript/location_index.go

[internal/transcript/location_index.go](../../internal/transcript/location_index.go)

类型：`locationHeader`（31）；`locationRecord`（38）；`locationIndex`（44）；`locationCache`（56）。

函数/方法：`newLocationCache`（64）；`locationCache.get`（67）；`locationCache.put`（79）；`locationCache.invalidate`（92）；`locationCache.remove`（97）；`locationCache.removeOrder`（103）；`sidecarPath`（111）；`metadataEntry`（112）；`locationIndex.rebuildBranch`（120）；`locationIndex.appendRecord`（173）；`locationIndex.branchMetadata`（211）；`locationIndex.matches`（214）；`readLocationIndex`（226）；`scanLocationIndex`（276）；`writeLocationIndex`（338）；`Store.locationIndexLocked`（377）；`readLocatedEntry`（415）；`Store.largeMetadataLocked`（435）；`Store.largeContextSessionLocked`（447）；`Store.largeMessagePageLocked`（489）；`Store.advanceLocationLocked`（513）。

### internal/transcript/store.go

[internal/transcript/store.go](../../internal/transcript/store.go)

类型：`Store`（43）；`lockEntry`（52）；`lockRegistry`（58）。

函数/方法：`NewStore`（65）；`Store.AgentsRoot`（103）；`Store.CreateSession`（118）；`Store.AppendMessage`（202）；`Store.AppendCompaction`（229）；`Store.LoadSession`（256）；`Store.LoadContextSession`（287）；`Store.LoadMessagePage`（329）；`Store.documentForReadLocked`（365）；`Store.ListSessionRefs`（396）；`Store.SessionDirectory`（462）；`Store.LoadHeader`（468）；`Store.SessionModifiedAt`（500）；`Store.DeleteSession`（522）；`Store.RepairSession`（561）；`Store.appendEntry`（589）；`messageEntryPageFromIndex`（707）；`validateCompactionAgainstDocument`（763）；`Store.sessionPath`（787）；`Store.agentSessionsDirectory`（799）；`Store.sessionDirectory`（810）；`Store.ensureSessionDirectory`（825）；`Store.validateExistingSessionPath`（852）；`ensureRealDirectory`（891）；`validateRealDirectory`（898）；`validateIdentifier`（912）；`pathWithinRoot`（926）；`validateAgentMessage`（934）；`validateContentBlock`（987）；`validStopReason`（1046）；`validateEntryPayload`（1060）；`validateContext`（1116）；`encodeJSONLine`（1126）；`repairTailLocked`（1137）；`loadLocked`（1239）；`contextWindowIndex`（1333）；`validateHeader`（1353）；`buildActiveBranch`（1369）；`decodeStrictJSON`（1403）；`formatTime`（1421）；`parseTime`（1425）；`writeFull`（1433）；`string.func`（1447）。

### internal/transcript/tool_result.go

[internal/transcript/tool_result.go](../../internal/transcript/tool_result.go)

函数/方法：`ToolResultSucceeded`（7）；`ToolResultRejected`（21）。

### internal/transcript/types.go

[internal/transcript/types.go](../../internal/transcript/types.go)

类型：`EntryType`（21）；`MessageRole`（38）；`ContentType`（47）；`StopReason`（58）；`SessionHeader`（74）；`ContentBlock`（97）；`UsageCost`（138）；`Usage`（154）；`AgentMessage`（181）；`CompactionDetails`（216）；`AppendCompactionInput`（245）；`Entry`（267）；`CreateSessionInput`（311）；`SessionRef`（325）；`RepairResult`（332）；`MessageEntryPage`（342）；`Document`（358）；`ReadStats`（381）；`ContextWindowIndex`（389）。

</details>

## 05 章节文件

实现说明：[05-tools.md](05-tools.md)。

| 文件 | 行数 | 内容摘要 | 主要声明（节选） |
| --- | ---: | --- | --- |
| [internal/tools/builtin/apply_patch.go](../../internal/tools/builtin/apply_patch.go) | 166 | `c2bf937e7d3e` | `PatchChange`、`ApplyPatchInput`、`ApplyPatchOutput`、`ApplyPatchFactory`、`preparedPatch`、`NewApplyPatchFactory`、`ApplyPatchFactory.Descriptor`、`ApplyPatchFactory.Build` |
| [internal/tools/builtin/atomic_fs.go](../../internal/tools/builtin/atomic_fs.go) | 290 | `d70d5d4ba868` | `ensureRootParentDirectory`、`atomicWriteWorkspaceFile`、`siblingTemporaryPath`、`writeAllWithContext`、`syncRootParent` |
| [internal/tools/builtin/browser_proxy.go](../../internal/tools/builtin/browser_proxy.go) | 168 | `ccd98c2872a0` | `browserProxy`、`browserProxyListener`、`browserProxyConn`、`startBrowserProxy`、`browserProxy.ServeHTTP`、`browserProxy.connect`、`browserProxyListener.Accept`、`browserProxyConn.Close` |
| [internal/tools/builtin/browser_tool.go](../../internal/tools/builtin/browser_tool.go) | 818 | `2ea13cb8a4e6` | `BrowserInput`、`BrowserOutput`、`BrowserElement`、`BrowserFactory`、`BrowserAttachmentWriter`、`BrowserVisionInspector`、`browserSession`、`cdpMessage` |
| [internal/tools/builtin/collaboration_tools.go](../../internal/tools/builtin/collaboration_tools.go) | 71 | `b9272772658b` | `collaborationToolFactory`、`ListAgentsInput`、`ListAgentsOutput`、`RunAgentInput`、`NewListAgentsFactory`、`NewRunAgentFactory`、`newCollaborationToolFactory`、`collaborationToolFactory.Descriptor` |
| [internal/tools/builtin/context_artifact.go](../../internal/tools/builtin/context_artifact.go) | 198 | `3d5a8ec6e41b` | `ContextArtifactReader`、`ContextResourceFactory`、`ContextResourceInput`、`ContextResourceOutput`、`NewContextResourceFactory`、`ContextResourceFactory.Descriptor`、`ContextResourceFactory.Build`、`ContextResourceFactory.readArtifact` |
| [internal/tools/builtin/context_history.go](../../internal/tools/builtin/context_history.go) | 293 | `6eddeabc5995` | `HistoryRepository`、`indexedHistoryRepository`、`SessionHistoryFactory`、`SessionHistoryInput`、`SessionHistoryMatch`、`SessionHistoryEntry`、`SessionHistoryOutput`、`NewSessionHistoryFactory` |
| [internal/tools/builtin/extract_document.go](../../internal/tools/builtin/extract_document.go) | 240 | `8759504217a0` | `DocumentAttachmentReader`、`ExtractDocumentFactory`、`ExtractDocumentInput`、`ExtractDocumentOutput`、`NewExtractDocumentFactory`、`ExtractDocumentFactory.Descriptor`、`ExtractDocumentFactory.Build`、`ExtractDocumentFactory.run` |
| [internal/tools/builtin/file_ops.go](../../internal/tools/builtin/file_ops.go) | 259 | `ff3b3bd96541` | `CopyFileInput`、`MoveFileInput`、`FileOperationOutput`、`CopyFileFactory`、`MoveFileFactory`、`DeleteFileInput`、`DeleteFileFactory`、`NewCopyFileFactory` |
| [internal/tools/builtin/file_transactions.go](../../internal/tools/builtin/file_transactions.go) | 122 | `bde030641cb2` | `fileLock`、`fileVersion`、`fileLockKey`、`lockFileTargets`、`fileVersion.check` |
| [internal/tools/builtin/filesystem.go](../../internal/tools/builtin/filesystem.go) | 406 | `890e119dc151` | `FilesystemBackend`、`filesystemFactory`、`NewFilesystemFactories`、`filesystemFactory.Descriptor`、`filesystemFactory.Build`、`FilesystemBackend.LsInfo`、`fileInfo`、`FilesystemBackend.Read` |
| [internal/tools/builtin/git_tools.go](../../internal/tools/builtin/git_tools.go) | 175 | `5bee411c290d` | `gitReadFactory`、`GitStatusInput`、`GitDiffInput`、`GitLogInput`、`GitReadOutput`、`NewGitStatusFactory`、`NewGitDiffFactory`、`NewGitLogFactory` |
| [internal/tools/builtin/htmlmarkdown.go](../../internal/tools/builtin/htmlmarkdown.go) | 48 | `ad821cd09e0b` | `htmlToMarkdown` |
| [internal/tools/builtin/install_skill.go](../../internal/tools/builtin/install_skill.go) | 160 | `0126eb36aaa6` | `InstallSkillInput`、`InstallSkillOutput`、`AgentSkillEnableFunc`、`SkillRemoteInstaller`、`InstallSkillFactory`、`NewInstallSkillFactory`、`InstallSkillFactory.Descriptor`、`InstallSkillFactory.Build` |
| [internal/tools/builtin/limits.go](../../internal/tools/builtin/limits.go) | 56 | `c44b5bc30b6e` | `FileLimits`、`FileLimits.Validate` |
| [internal/tools/builtin/run_command.go](../../internal/tools/builtin/run_command.go) | 653 | `f7e43498387f` | `CommandLimits`、`RunCommandInput`、`RunCommandOutput`、`RunCommandFactory`、`boundedCommandOutput`、`CommandLimits.Validate`、`NewRunCommandFactory`、`RunCommandFactory.Descriptor` |
| [internal/tools/builtin/run_skill_script.go](../../internal/tools/builtin/run_skill_script.go) | 302 | `7bf293a11d1f` | `RunSkillScriptInput`、`RunSkillScriptOutput`、`RunSkillScriptFactory`、`NewRunSkillScriptFactory`、`RunSkillScriptFactory.Descriptor`、`RunSkillScriptFactory.Build`、`RunSkillScriptFactory.run` |
| [internal/tools/builtin/sandbox_fs.go](../../internal/tools/builtin/sandbox_fs.go) | 88 | `9ca4b2f629ba` | `sandboxTarget`、`openSandboxTarget`、`sandboxTarget.canReadChild`、`sandboxTarget.Close` |
| [internal/tools/builtin/schedule_task.go](../../internal/tools/builtin/schedule_task.go) | 146 | `e907a225fe3c` | `ScheduleTaskInput`、`ScheduleTaskOutput`、`TaskScheduler`、`ScheduleTaskFactory`、`NewScheduleTaskFactory`、`ScheduleTaskFactory.Descriptor`、`ScheduleTaskFactory.Build`、`ScheduleTaskFactory.run` |
| [internal/tools/builtin/search_files.go](../../internal/tools/builtin/search_files.go) | 98 | `3aaed88f8034` | `defaultSearchPath`、`displayRootChild`、`walkRoot` |
| [internal/tools/builtin/system_tools.go](../../internal/tools/builtin/system_tools.go) | 111 | `e5fa8a9e18ac` | `CurrentTimeInput`、`CurrentTimeOutput`、`CurrentTimeFactory`、`PlanItem`、`UpdatePlanInput`、`UpdatePlanOutput`、`UpdatePlanFactory`、`NewCurrentTimeFactory` |
| [internal/tools/builtin/webfetch_tool.go](../../internal/tools/builtin/webfetch_tool.go) | 318 | `e5e1eb5dacfa` | `WebFetchInput`、`WebFetchOutput`、`WebFetchFactory`、`publicWebDialer`、`NewWebFetchFactory`、`WebFetchFactory.Descriptor`、`WebFetchFactory.Build`、`WebFetchFactory.run` |
| [internal/tools/builtin/websearch_tool.go](../../internal/tools/builtin/websearch_tool.go) | 54 | `f988117bbb65` | `WebSearchInput`、`WebSearchResult`、`WebSearchAttempt`、`WebSearchDiagnostics`、`WebSearchOutput`、`WebSearchFactory`、`NewWebSearchFactory`、`WebSearchFactory.Descriptor` |
| [internal/tools/capability_identity.go](../../internal/tools/capability_identity.go) | 134 | `49997def0b19` | `buildCapabilityIdentity` |
| [internal/tools/errors.go](../../internal/tools/errors.go) | 42 | `9fdf56ceeac6` | 组件/配置/样式 |
| [internal/tools/guarded_tool.go](../../internal/tools/guarded_tool.go) | 251 | `f960e9f55381` | `guardedInvokableTool`、`GuardInvokableTool`、`guardedInvokableTool.Info`、`guardedInvokableTool.InvokableRun`、`guardedInvokableTool.resumeInvocation`、`guardedInvokableTool.invokeRealTool`、`permissionDeniedResult`、`approvalRejectedResult` |
| [internal/tools/permission.go](../../internal/tools/permission.go) | 9 | `799df161d7e8` | `Authorizer` |
| [internal/tools/reduction.go](../../internal/tools/reduction.go) | 102 | `d3eb6ec0b6d7` | `Registry.Reduction`、`textToolResult`、`stringToolResult`、`isArchivedResult`、`Registry.archiveResult` |
| [internal/tools/registry.go](../../internal/tools/registry.go) | 268 | `240edde50751` | `Registry`、`Registry.Close`、`NewRegistry`、`Registry.Revision`、`Registry.Register`、`Registry.Unregister`、`Registry.List`、`Registry.Resolve` |
| [internal/tools/types.go](../../internal/tools/types.go) | 250 | `2bdcdb91e834` | `RiskLevel`、`Descriptor`、`ModuleOrigin`、`MCPOrigin`、`Scope`、`Factory`、`ResultArchiver`、`ResolvedTools` |

<details>
<summary>本章 Go 顶层函数与类型定位（自动扫描）</summary>

### internal/tools/builtin/apply_patch.go

[internal/tools/builtin/apply_patch.go](../../internal/tools/builtin/apply_patch.go)

类型：`PatchChange`（20）；`ApplyPatchInput`（26）；`ApplyPatchOutput`（29）；`ApplyPatchFactory`（34）；`preparedPatch`（51）。

函数/方法：`NewApplyPatchFactory`（36）；`ApplyPatchFactory.Descriptor`（42）；`ApplyPatchFactory.Build`（45）；`ApplyPatchFactory.run`（59）。

### internal/tools/builtin/atomic_fs.go

[internal/tools/builtin/atomic_fs.go](../../internal/tools/builtin/atomic_fs.go)

函数/方法：`ensureRootParentDirectory`（15）；`atomicWriteWorkspaceFile`（38）；`siblingTemporaryPath`（199）；`writeAllWithContext`（227）；`syncRootParent`（262）。

### internal/tools/builtin/browser_proxy.go

[internal/tools/builtin/browser_proxy.go](../../internal/tools/builtin/browser_proxy.go)

类型：`browserProxy`（16）；`browserProxyListener`（113）；`browserProxyConn`（138）。

函数/方法：`startBrowserProxy`（29）；`browserProxy.ServeHTTP`（54）；`browserProxy.connect`（73）；`browserProxyListener.Accept`（118）；`browserProxyConn.Close`（143）；`browserProxy.Close`（151）。

### internal/tools/builtin/browser_tool.go

[internal/tools/builtin/browser_tool.go](../../internal/tools/builtin/browser_tool.go)

类型：`BrowserInput`（36）；`BrowserOutput`（45）；`BrowserElement`（59）；`BrowserFactory`（67）；`BrowserAttachmentWriter`（79）；`BrowserVisionInspector`（84）；`browserSession`（381）；`cdpMessage`（396）。

函数/方法：`NewBrowserFactory`（86）；`BrowserFactory.SetAttachmentWriter`（94）；`BrowserFactory.reap`（98）；`BrowserFactory.Descriptor`（124）；`BrowserFactory.Build`（127）；`BrowserFactory.run`（138）；`BrowserFactory.persistScreenshot`（330）；`BrowserFactory.closeSession`（342）；`BrowserFactory.Close`（352）；`validateBrowserURL`（369）；`chromePath`（406）；`startBrowser`（420）；`startBrowserWithMode`（424）；`persistentBrowserProfile`（433）；`ensureBrowserProfileDirectory`（448）；`startBrowserWithProfile`（465）；`browserSession.readLoop`（593）；`browserSession.call`（617）；`browserSession.evaluate`（648）；`browserSession.waitReady`（670）；`browserSession.snapshot`（690）；`browserSession.snapshotWithVerification`（703）；`browserNeedsHumanVerification`（712）；`markBrowserVerification`（728）；`browserSession.pageIdentity`（736）；`browserSession.captureScreenshot`（748）；`browserSession.navigateHistory`（769）；`browserSession.close`（794）；`browserSession.closeGracefully`（811）。

### internal/tools/builtin/collaboration_tools.go

[internal/tools/builtin/collaboration_tools.go](../../internal/tools/builtin/collaboration_tools.go)

类型：`collaborationToolFactory`（14）；`ListAgentsInput`（19）；`ListAgentsOutput`（20）；`RunAgentInput`（23）。

函数/方法：`NewListAgentsFactory`（28）；`NewRunAgentFactory`（32）；`newCollaborationToolFactory`（36）；`collaborationToolFactory.Descriptor`（43）；`collaborationToolFactory.Build`（47）。

### internal/tools/builtin/context_artifact.go

[internal/tools/builtin/context_artifact.go](../../internal/tools/builtin/context_artifact.go)

类型：`ContextArtifactReader`（24）；`ContextResourceFactory`（30）；`ContextResourceInput`（49）；`ContextResourceOutput`（56）。

函数/方法：`NewContextResourceFactory`（35）；`ContextResourceFactory.Descriptor`（45）；`ContextResourceFactory.Build`（71）；`ContextResourceFactory.readArtifact`（108）；`ContextResourceFactory.readAttachment`（134）；`contextResourceRange`（189）。

### internal/tools/builtin/context_history.go

[internal/tools/builtin/context_history.go](../../internal/tools/builtin/context_history.go)

类型：`HistoryRepository`（25）；`indexedHistoryRepository`（29）；`SessionHistoryFactory`（39）；`SessionHistoryInput`（56）；`SessionHistoryMatch`（69）；`SessionHistoryEntry`（76）；`SessionHistoryOutput`（88）。

函数/方法：`NewSessionHistoryFactory`（43）；`SessionHistoryFactory.Descriptor`（50）；`SessionHistoryFactory.Build`（94）；`SessionHistoryFactory.search`（121）；`SessionHistoryFactory.read`（166）；`historyEntryText`（250）；`historySnippet`（286）。

### internal/tools/builtin/extract_document.go

[internal/tools/builtin/extract_document.go](../../internal/tools/builtin/extract_document.go)

类型：`DocumentAttachmentReader`（24）；`ExtractDocumentFactory`（29）；`ExtractDocumentInput`（50）；`ExtractDocumentOutput`（57）。

函数/方法：`NewExtractDocumentFactory`（38）；`ExtractDocumentFactory.Descriptor`（45）；`ExtractDocumentFactory.Build`（70）；`ExtractDocumentFactory.run`（81）；`ExtractDocumentFactory.markdown`（160）；`findDocumentAttachment`（207）。

### internal/tools/builtin/file_ops.go

[internal/tools/builtin/file_ops.go](../../internal/tools/builtin/file_ops.go)

类型：`CopyFileInput`（26）；`MoveFileInput`（33）；`FileOperationOutput`（38）；`CopyFileFactory`（46）；`MoveFileFactory`（69）；`DeleteFileInput`（211）；`DeleteFileFactory`（214）。

函数/方法：`NewCopyFileFactory`（51）；`CopyFileFactory.Descriptor`（60）；`CopyFileFactory.Build`（63）；`NewMoveFileFactory`（71）；`MoveFileFactory.Descriptor`（77）；`MoveFileFactory.Build`（80）；`copyFile`（89）；`NewDeleteFileFactory`（216）；`DeleteFileFactory.Descriptor`（217）；`DeleteFileFactory.Build`（220）；`sameSandboxPath`（252）。

### internal/tools/builtin/file_transactions.go

[internal/tools/builtin/file_transactions.go](../../internal/tools/builtin/file_transactions.go)

类型：`fileLock`（19）；`fileVersion`（95）。

函数/方法：`fileLockKey`（30）；`lockFileTargets`（39）；`fileVersion.check`（100）。

### internal/tools/builtin/filesystem.go

[internal/tools/builtin/filesystem.go](../../internal/tools/builtin/filesystem.go)

类型：`FilesystemBackend`（28）；`filesystemFactory`（51）。

函数/方法：`NewFilesystemFactories`（37）；`filesystemFactory.Descriptor`（57）；`filesystemFactory.Build`（64）；`FilesystemBackend.LsInfo`（90）；`fileInfo`（126）；`FilesystemBackend.Read`（130）；`FilesystemBackend.Write`（162）；`FilesystemBackend.Edit`（195）；`FilesystemBackend.GlobInfo`（241）；`FilesystemBackend.GrepRaw`（273）；`matchesFileType`（361）；`readRootFileWithLimit`（376）。

### internal/tools/builtin/git_tools.go

[internal/tools/builtin/git_tools.go](../../internal/tools/builtin/git_tools.go)

类型：`gitReadFactory`（25）；`GitStatusInput`（55）；`GitDiffInput`（58）；`GitLogInput`（63）；`GitReadOutput`（67）。

函数/方法：`NewGitStatusFactory`（33）；`NewGitDiffFactory`（36）；`NewGitLogFactory`（39）；`newGitReadFactory`（42）；`gitReadFactory.Descriptor`（51）；`gitReadFactory.Build`（74）；`gitReadFactory.run`（127）；`filepathAbs`（159）；`gitReadEnvironment`（160）。

### internal/tools/builtin/htmlmarkdown.go

[internal/tools/builtin/htmlmarkdown.go](../../internal/tools/builtin/htmlmarkdown.go)

函数/方法：`htmlToMarkdown`（42）。

### internal/tools/builtin/install_skill.go

[internal/tools/builtin/install_skill.go](../../internal/tools/builtin/install_skill.go)

类型：`InstallSkillInput`（23）；`InstallSkillOutput`（36）；`AgentSkillEnableFunc`（54）；`SkillRemoteInstaller`（60）；`InstallSkillFactory`（70）。

函数/方法：`NewInstallSkillFactory`（76）；`InstallSkillFactory.Descriptor`（87）；`InstallSkillFactory.Build`（92）；`InstallSkillFactory.run`（115）。

### internal/tools/builtin/limits.go

[internal/tools/builtin/limits.go](../../internal/tools/builtin/limits.go)

类型：`FileLimits`（22）。

函数/方法：`FileLimits.Validate`（36）。

### internal/tools/builtin/run_command.go

[internal/tools/builtin/run_command.go](../../internal/tools/builtin/run_command.go)

类型：`CommandLimits`（35）；`RunCommandInput`（84）；`RunCommandOutput`（103）；`RunCommandFactory`（129）；`boundedCommandOutput`（490）。

函数/方法：`CommandLimits.Validate`（48）；`NewRunCommandFactory`（143）；`RunCommandFactory.Descriptor`（180）；`RunCommandFactory.Build`（189）；`RunCommandFactory.run`（228）；`cloneStringSlice`（382）；`normalizeCommandName`（399）；`validateCommandName`（400）；`validateCommandArguments`（402）；`resolveCommandWorkingDirectory`（446）；`newBoundedCommandOutput`（506）；`boundedCommandOutput.Write`（526）；`boundedCommandOutput.Result`（593）。

### internal/tools/builtin/run_skill_script.go

[internal/tools/builtin/run_skill_script.go](../../internal/tools/builtin/run_skill_script.go)

类型：`RunSkillScriptInput`（38）；`RunSkillScriptOutput`（51）；`RunSkillScriptFactory`（69）。

函数/方法：`NewRunSkillScriptFactory`（77）；`RunSkillScriptFactory.Descriptor`（105）；`RunSkillScriptFactory.Build`（109）；`RunSkillScriptFactory.run`（137）。

### internal/tools/builtin/sandbox_fs.go

[internal/tools/builtin/sandbox_fs.go](../../internal/tools/builtin/sandbox_fs.go)

类型：`sandboxTarget`（15）。

函数/方法：`openSandboxTarget`（27）；`sandboxTarget.canReadChild`（75）；`sandboxTarget.Close`（83）。

### internal/tools/builtin/schedule_task.go

[internal/tools/builtin/schedule_task.go](../../internal/tools/builtin/schedule_task.go)

类型：`ScheduleTaskInput`（21）；`ScheduleTaskOutput`（33）；`TaskScheduler`（40）；`ScheduleTaskFactory`（45）。

函数/方法：`NewScheduleTaskFactory`（47）；`ScheduleTaskFactory.Descriptor`（54）；`ScheduleTaskFactory.Build`（58）；`ScheduleTaskFactory.run`（73）；`scheduleFromToolInput`（113）。

### internal/tools/builtin/search_files.go

[internal/tools/builtin/search_files.go](../../internal/tools/builtin/search_files.go)

函数/方法：`defaultSearchPath`（13）；`displayRootChild`（20）；`walkRoot`（33）。

### internal/tools/builtin/system_tools.go

[internal/tools/builtin/system_tools.go](../../internal/tools/builtin/system_tools.go)

类型：`CurrentTimeInput`（21）；`CurrentTimeOutput`（24）；`CurrentTimeFactory`（29）；`PlanItem`（50）；`UpdatePlanInput`（54）；`UpdatePlanOutput`（57）；`UpdatePlanFactory`（63）。

函数/方法：`NewCurrentTimeFactory`（31）；`CurrentTimeFactory.Descriptor`（32）；`CurrentTimeFactory.Build`（35）；`NewUpdatePlanFactory`（65）；`UpdatePlanFactory.Descriptor`（66）；`UpdatePlanFactory.Build`（69）。

### internal/tools/builtin/webfetch_tool.go

[internal/tools/builtin/webfetch_tool.go](../../internal/tools/builtin/webfetch_tool.go)

类型：`WebFetchInput`（27）；`WebFetchOutput`（32）；`WebFetchFactory`（43）；`publicWebDialer`（255）。

函数/方法：`NewWebFetchFactory`（51）；`WebFetchFactory.Descriptor`（78）；`WebFetchFactory.Build`（82）；`WebFetchFactory.run`（98）；`WebFetchFactory.doRequest`（174）；`parsePublicFetchURL`（190）；`isRedirectStatus`（214）；`readLimitedWebFetchBody`（223）；`truncateUTF8ByRunes`（234）；`publicWebDialer.DialContext`（257）；`resolvePublicIPs`（278）；`isForbiddenWebFetchIP`（302）；`looksLikeHTML`（315）。

### internal/tools/builtin/websearch_tool.go

[internal/tools/builtin/websearch_tool.go](../../internal/tools/builtin/websearch_tool.go)

类型：`WebSearchInput`（19）；`WebSearchResult`（20）；`WebSearchAttempt`（21）；`WebSearchDiagnostics`（22）；`WebSearchOutput`（23）；`WebSearchFactory`（26）。

函数/方法：`NewWebSearchFactory`（28）；`WebSearchFactory.Descriptor`（36）；`WebSearchFactory.Build`（41）。

### internal/tools/capability_identity.go

[internal/tools/capability_identity.go](../../internal/tools/capability_identity.go)

函数/方法：`buildCapabilityIdentity`（16）。

### internal/tools/errors.go

[internal/tools/errors.go](../../internal/tools/errors.go)

### internal/tools/guarded_tool.go

[internal/tools/guarded_tool.go](../../internal/tools/guarded_tool.go)

类型：`guardedInvokableTool`（83）。

函数/方法：`GuardInvokableTool`（21）；`guardedInvokableTool.Info`（94）；`guardedInvokableTool.InvokableRun`（99）；`guardedInvokableTool.resumeInvocation`（187）；`guardedInvokableTool.invokeRealTool`（233）；`permissionDeniedResult`（245）；`approvalRejectedResult`（249）。

### internal/tools/permission.go

[internal/tools/permission.go](../../internal/tools/permission.go)

类型：`Authorizer`（9）。

### internal/tools/reduction.go

[internal/tools/reduction.go](../../internal/tools/reduction.go)

函数/方法：`Registry.Reduction`（17）；`textToolResult`（70）；`stringToolResult`（76）；`isArchivedResult`（79）；`Registry.archiveResult`（86）。

### internal/tools/registry.go

[internal/tools/registry.go](../../internal/tools/registry.go)

类型：`Registry`（37）。

函数/方法：`Registry.Close`（46）；`NewRegistry`（70）；`Registry.Revision`（84）；`Registry.Register`（95）；`Registry.Unregister`（125）；`Registry.List`（144）；`Registry.Resolve`（178）；`Registry.Guard`（266）。

### internal/tools/types.go

[internal/tools/types.go](../../internal/tools/types.go)

类型：`RiskLevel`（21）；`Descriptor`（43）；`ModuleOrigin`（60）；`MCPOrigin`（69）；`Scope`（144）；`Factory`（223）；`ResultArchiver`（232）；`ResolvedTools`（237）。

函数/方法：`Descriptor.Validate`（77）；`Scope.SandboxPolicy`（186）；`Scope.Validate`（194）。

</details>

## 06 章节文件

实现说明：[06-security.md](06-security.md)。

| 文件 | 行数 | 内容摘要 | 主要声明（节选） |
| --- | ---: | --- | --- |
| [internal/approval/checkpoint_store.go](../../internal/approval/checkpoint_store.go) | 109 | `0958ef3c0bae` | `CheckpointStore`、`NewCheckpointStore`、`CheckpointStore.Get`、`CheckpointStore.Has`、`CheckpointStore.Set`、`CheckpointStore.Delete` |
| [internal/approval/codec.go](../../internal/approval/codec.go) | 99 | `c729aac38a6d` | `init`、`registerCheckpointGobType`、`EncodeInterrupt`、`DecodeInterruptInfo`、`DecodeInterruptState`、`EncodeResumeData`、`DecodeResumeData` |
| [internal/approval/errors.go](../../internal/approval/errors.go) | 14 | `3b10182638a0` | 组件/配置/样式 |
| [internal/approval/manager.go](../../internal/approval/manager.go) | 319 | `5c4a2afbeee0` | `Manager`、`NewManager`、`Manager.UpdateTimeout`、`Manager.Register`、`Manager.Resolve`、`Manager.Complete`、`Manager.Cancel`、`Manager.Forget` |
| [internal/approval/types.go](../../internal/approval/types.go) | 160 | `da5e65d197b6` | `Status`、`Decision`、`InterruptInfo`、`InterruptState`、`ResumeData`、`Request`、`Resolution`、`Decision.Validate` |
| [internal/permission/engine.go](../../internal/permission/engine.go) | 493 | `6461c087b4bc` | `Authorizer`、`Engine`、`NewEngine`、`Engine.Evaluate`、`Engine.Grant`、`Engine.DenyAgent`、`Engine.createRule`、`Engine.ListPersistentRules` |
| [internal/permission/errors.go](../../internal/permission/errors.go) | 14 | `77d9de56ed45` | 组件/配置/样式 |
| [internal/permission/identity.go](../../internal/permission/identity.go) | 200 | `4fdedfd0f2b5` | `CapabilityKind`、`CapabilityIdentity`、`CapabilityIdentity.Empty`、`CapabilityIdentity.Normalize`、`CapabilityIdentity.Validate`、`CapabilityIdentity.EqualExact`、`CapabilityIdentity.DenyScope`、`CapabilityIdentity.MatchesDeny` |
| [internal/permission/presenter.go](../../internal/permission/presenter.go) | 401 | `248722ae6613` | `BuildPresentation`、`buildSafeArgumentPreview`、`safePreviewValue`、`sensitiveArgumentKey`、`safeField` |
| [internal/permission/risk.go](../../internal/permission/risk.go) | 151 | `7e730216a132` | `routineOperation`、`pathsInside`、`insideWorkspace`、`routineCommand` |
| [internal/permission/store.go](../../internal/permission/store.go) | 380 | `ea2dcfba98de` | `policyDocument`、`legacyPolicyDocumentV1`、`legacyRuleV1`、`legacyConditionV1`、`Store`、`NewStore`、`Store.List`、`Store.Upsert` |
| [internal/permission/types.go](../../internal/permission/types.go) | 180 | `895385ba7f95` | `RiskLevel`、`Action`、`GrantScope`、`Request`、`PresentationField`、`Presentation`、`Decision`、`Rule` |
| [internal/sandbox/access.go](../../internal/sandbox/access.go) | 356 | `3fe6d14ea53e` | `AccessLevel`、`PathOperation`、`RuleSource`、`PathRule`、`PathDecision`、`NativeFilesystemView`、`AccessLevel.String`、`AccessLevel.Validate` |
| [internal/sandbox/fingerprint.go](../../internal/sandbox/fingerprint.go) | 95 | `e100d57838c8` | `fingerprintPathRule`、`fingerprintPayload`、`EffectivePolicy.Fingerprint`、`normalizeFingerprintPath` |
| [internal/sandbox/manager.go](../../internal/sandbox/manager.go) | 322 | `af85cfb1186d` | `Manager`、`NewManager`、`Manager.Capability`、`Manager.Config`、`Manager.UpdateConfig`、`Manager.Runner`、`Manager.SetFullAccessProvider`、`Manager.Resolve` |
| [internal/sandbox/pathguard.go](../../internal/sandbox/pathguard.go) | 188 | `36492f84f766` | `EffectivePolicy.CheckPath`、`CanonicalRoot`、`canonicalizePath`、`pathWithin`、`pathMoreSpecific`、`filesystemRoot`、`normalizePlatformPath` |
| [internal/sandbox/platform_darwin.go](../../internal/sandbox/platform_darwin.go) | 245 | `0e2d3393f93a` | `probeNativeCapability`、`prepareNativeCommand`、`buildSeatbeltProfile`、`seatbeltExecutableRuntimeRoots`、`seatbeltAncestorMetadataRoots`、`configurePlatformProcess`、`attachPlatformProcess` |
| [internal/sandbox/platform_linux.go](../../internal/sandbox/platform_linux.go) | 266 | `9dfc4fdea305` | `probeNativeCapability`、`probeBubblewrap`、`prepareNativeCommand`、`linuxSystemRuntimeRoots`、`linuxExecutableRuntimeRoots`、`linuxResolvedSystemTargets`、`configurePlatformProcess`、`attachPlatformProcess` |
| [internal/sandbox/platform_windows.go](../../internal/sandbox/platform_windows.go) | 179 | `816fee933882` | `jobObjectBasicLimitInformation`、`ioCounters`、`jobObjectExtendedLimitInformation`、`probeNativeCapability`、`prepareNativeCommand`、`configurePlatformProcess`、`attachPlatformProcess` |
| [internal/sandbox/process_policy.go](../../internal/sandbox/process_policy.go) | 46 | `165b73b36def` | `validateProcessIsolationPolicy` |
| [internal/sandbox/runner.go](../../internal/sandbox/runner.go) | 279 | `7369270d9d07` | `ProcessSpec`、`ProcessResult`、`Runner`、`NewRunner`、`prepareRuntimeTemp`、`withRuntimeTempEnvironment`、`Runner.Run`、`Runner.readOnlyCommandPolicy` |
| [internal/sandbox/types.go](../../internal/sandbox/types.go) | 210 | `bc263a67a011` | `Profile`、`NetworkMode`、`NativeMode`、`AgentPolicy`、`Config`、`Capability`、`EffectivePolicy`、`NormalizeProfile` |

<details>
<summary>本章 Go 顶层函数与类型定位（自动扫描）</summary>

### internal/approval/checkpoint_store.go

[internal/approval/checkpoint_store.go](../../internal/approval/checkpoint_store.go)

类型：`CheckpointStore`（19）。

函数/方法：`NewCheckpointStore`（25）；`CheckpointStore.Get`（30）；`CheckpointStore.Has`（53）；`CheckpointStore.Set`（71）；`CheckpointStore.Delete`（94）。

### internal/approval/codec.go

[internal/approval/codec.go](../../internal/approval/codec.go)

函数/方法：`init`（10）；`registerCheckpointGobType`（22）；`EncodeInterrupt`（39）；`DecodeInterruptInfo`（56）；`DecodeInterruptState`（72）；`EncodeResumeData`（84）；`DecodeResumeData`（93）。

### internal/approval/errors.go

[internal/approval/errors.go](../../internal/approval/errors.go)

### internal/approval/manager.go

[internal/approval/manager.go](../../internal/approval/manager.go)

类型：`Manager`（22）。

函数/方法：`NewManager`（32）；`Manager.UpdateTimeout`（54）；`Manager.Register`（65）；`Manager.Resolve`（136）；`Manager.Complete`（225）；`Manager.Cancel`（240）；`Manager.Forget`（256）；`Manager.Expire`（270）；`Manager.Get`（290）；`Manager.ListPending`（298）；`Manager.restorePending`（311）。

### internal/approval/types.go

[internal/approval/types.go](../../internal/approval/types.go)

类型：`Status`（17）；`Decision`（33）；`InterruptInfo`（59）；`InterruptState`（109）；`ResumeData`（116）；`Request`（124）；`Resolution`（156）。

函数/方法：`Decision.Validate`（45）；`InterruptInfo.Validate`（74）；`InterruptInfo.PermissionRequest`（93）；`Request.InterruptID`（146）；`Request.CheckpointID`（149）；`Request.Identity`（152）。

### internal/permission/engine.go

[internal/permission/engine.go](../../internal/permission/engine.go)

类型：`Authorizer`（21）；`Engine`（29）。

函数/方法：`NewEngine`（39）；`Engine.Evaluate`（58）；`Engine.Grant`（163）；`Engine.DenyAgent`（197）；`Engine.createRule`（202）；`Engine.ListPersistentRules`（283）；`Engine.DeletePersistentRule`（288）；`Engine.ClearPersistentAllows`（301）；`Engine.ListSessionRules`（319）；`Engine.DeleteSessionRule`（331）；`Engine.ClearSessionRules`（362）；`Engine.UpdateConfig`（372）；`Engine.Config`（384）；`Engine.matchingSessionRules`（391）；`matchingRules`（398）；`newestRuleWithAction`（408）；`ruleMatches`（430）；`sameRuleTarget`（447）；`capabilityLogicalTarget`（454）；`Engine.defaultActionWithConfig`（470）。

### internal/permission/errors.go

[internal/permission/errors.go](../../internal/permission/errors.go)

### internal/permission/identity.go

[internal/permission/identity.go](../../internal/permission/identity.go)

类型：`CapabilityKind`（14）；`CapabilityIdentity`（31）。

函数/方法：`CapabilityIdentity.Empty`（52）；`CapabilityIdentity.Normalize`（63）；`CapabilityIdentity.Validate`（83）；`CapabilityIdentity.EqualExact`（131）；`CapabilityIdentity.DenyScope`（141）；`CapabilityIdentity.MatchesDeny`（160）；`normalizeIdentityPath`（190）。

### internal/permission/presenter.go

[internal/permission/presenter.go](../../internal/permission/presenter.go)

函数/方法：`BuildPresentation`（23）；`buildSafeArgumentPreview`（289）；`safePreviewValue`（323）；`sensitiveArgumentKey`（381）；`safeField`（395）。

### internal/permission/risk.go

[internal/permission/risk.go](../../internal/permission/risk.go)

函数/方法：`routineOperation`（14）；`pathsInside`（45）；`insideWorkspace`（70）；`routineCommand`（97）。

### internal/permission/store.go

[internal/permission/store.go](../../internal/permission/store.go)

类型：`policyDocument`（21）；`legacyPolicyDocumentV1`（29）；`legacyRuleV1`（34）；`legacyConditionV1`（45）；`Store`（57）。

函数/方法：`NewStore`（63）；`Store.List`（98）；`Store.Upsert`（127）；`Store.Delete`（167）；`Store.DeleteByAction`（211）；`samePersistentTarget`（248）；`Store.loadLocked`（255）；`decodeStrictJSON`（296）；`validatePolicyDocument`（311）；`migratePolicyV1`（326）；`inferLegacyCapabilityKind`（368）。

### internal/permission/types.go

[internal/permission/types.go](../../internal/permission/types.go)

类型：`RiskLevel`（11）；`Action`（20）；`GrantScope`（29）；`Request`（42）；`PresentationField`（92）；`Presentation`（98）；`Decision`（105）；`Rule`（118）；`ApprovalGrant`（177）。

函数/方法：`Request.Validate`（62）；`Rule.Validate`（130）。

### internal/sandbox/access.go

[internal/sandbox/access.go](../../internal/sandbox/access.go)

类型：`AccessLevel`（23）；`PathOperation`（58）；`RuleSource`（109）；`PathRule`（124）；`PathDecision`（147）；`NativeFilesystemView`（160）。

函数/方法：`AccessLevel.String`（32）；`AccessLevel.Validate`（47）；`PathOperation.Validate`（71）；`PathOperation.RequiredAccess`（81）；`PathOperation.allowsMissingTarget`（95）；`AccessLevel.Allows`（104）；`PathRule.Validate`（130）；`EffectivePolicy.WithPathAccess`（170）；`EffectivePolicy.NativeFilesystem`（200）；`pathRuleMatches`（246）；`appendGrantRule`（255）；`appendPathRule`（272）；`sortedPathRules`（293）；`appendUniquePath`（309）；`pathEqual`（318）；`sortedPaths`（326）；`coveredByAnyRoot`（332）；`collapseCoveredRoots`（341）。

### internal/sandbox/fingerprint.go

[internal/sandbox/fingerprint.go](../../internal/sandbox/fingerprint.go)

类型：`fingerprintPathRule`（16）；`fingerprintPayload`（22）。

函数/方法：`EffectivePolicy.Fingerprint`（44）；`normalizeFingerprintPath`（89）。

### internal/sandbox/manager.go

[internal/sandbox/manager.go](../../internal/sandbox/manager.go)

类型：`Manager`（16）。

函数/方法：`NewManager`（26）；`Manager.Capability`（50）；`Manager.Config`（52）；`Manager.UpdateConfig`（60）；`Manager.Runner`（70）；`Manager.SetFullAccessProvider`（74）；`Manager.Resolve`（80）；`rejectProtectedPath`（156）；`defaultProtectedRules`（169）；`protectedRulesFor`（176）；`appendProtectedPathRuleCandidates`（267）；`Manager.PrepareExternalCommand`（285）。

### internal/sandbox/pathguard.go

[internal/sandbox/pathguard.go](../../internal/sandbox/pathguard.go)

函数/方法：`EffectivePolicy.CheckPath`（16）；`CanonicalRoot`（93）；`canonicalizePath`（120）；`pathWithin`（148）；`pathMoreSpecific`（158）；`filesystemRoot`（167）；`normalizePlatformPath`（178）。

### internal/sandbox/platform_darwin.go

[internal/sandbox/platform_darwin.go](../../internal/sandbox/platform_darwin.go)

函数/方法：`probeNativeCapability`（15）；`prepareNativeCommand`（23）；`buildSeatbeltProfile`（59）；`seatbeltExecutableRuntimeRoots`（185）；`seatbeltAncestorMetadataRoots`（215）；`configurePlatformProcess`（238）；`attachPlatformProcess`（242）。

### internal/sandbox/platform_linux.go

[internal/sandbox/platform_linux.go](../../internal/sandbox/platform_linux.go)

函数/方法：`probeNativeCapability`（18）；`probeBubblewrap`（51）；`prepareNativeCommand`（92）；`linuxSystemRuntimeRoots`（182）；`linuxExecutableRuntimeRoots`（204）；`linuxResolvedSystemTargets`（229）；`configurePlatformProcess`（247）；`attachPlatformProcess`（252）；`visibleUnderRoots`（257）。

### internal/sandbox/platform_windows.go

[internal/sandbox/platform_windows.go](../../internal/sandbox/platform_windows.go)

类型：`jobObjectBasicLimitInformation`（39）；`ioCounters`（51）；`jobObjectExtendedLimitInformation`（60）。

函数/方法：`probeNativeCapability`（69）；`prepareNativeCommand`（82）；`configurePlatformProcess`（89）；`attachPlatformProcess`（123）。

### internal/sandbox/process_policy.go

[internal/sandbox/process_policy.go](../../internal/sandbox/process_policy.go)

函数/方法：`validateProcessIsolationPolicy`（16）。

### internal/sandbox/runner.go

[internal/sandbox/runner.go](../../internal/sandbox/runner.go)

类型：`ProcessSpec`（16）；`ProcessResult`（30）；`Runner`（44）。

函数/方法：`NewRunner`（46）；`prepareRuntimeTemp`（48）；`withRuntimeTempEnvironment`（60）；`Runner.Run`（97）；`Runner.readOnlyCommandPolicy`（242）。

### internal/sandbox/types.go

[internal/sandbox/types.go](../../internal/sandbox/types.go)

类型：`Profile`（12）；`NetworkMode`（21）；`NativeMode`（30）；`AgentPolicy`（41）；`Config`（51）；`Capability`（60）；`EffectivePolicy`（74）。

函数/方法：`NormalizeProfile`（90）；`NormalizeNetworkMode`（98）；`NormalizeNativeMode`（106）；`AgentPolicy.Validate`（114）；`Config.Validate`（140）；`EffectivePolicy.Validate`（150）；`CurrentPlatform`（191）；`EffectivePolicy.AllowsNetwork`（194）；`WorkspaceOnlyPolicy`（198）。

</details>

## 07 章节文件

实现说明：[07-models.md](07-models.md)。

| 文件 | 行数 | 内容摘要 | 主要声明（节选） |
| --- | ---: | --- | --- |
| [internal/credential/store.go](../../internal/credential/store.go) | 471 | `23d3ed703fe3` | `Store`、`New`、`Store.Put`、`Store.Get`、`Store.Delete`、`Store.pathFor`、`validateCredentialTarget`、`replaceFile` |
| [internal/credential/system.go](../../internal/credential/system.go) | 304 | `90e821fafba8` | `systemKeyring`、`osKeyring`、`osKeyring.Set`、`osKeyring.Get`、`osKeyring.Delete`、`NewSystem`、`newSystemWithBackend`、`Store.MigrateLegacy` |
| [internal/credential/vault.go](../../internal/credential/vault.go) | 22 | `be1c4c79d607` | `BackupVault`、`BackupVault.Set`、`BackupVault.Get`、`BackupVault.Delete` |
| [internal/models/capabilities.go](../../internal/models/capabilities.go) | 162 | `848c0a814810` | `CapabilityMode`、`CapabilityConfig`、`Capabilities`、`EffectiveCapabilities`、`normalizeCapabilityConfig`、`normalizeCapabilityMode`、`resolveCapability`、`inferCapabilities` |
| [internal/models/errors.go](../../internal/models/errors.go) | 40 | `4248fcb97e59` | `InvalidCapabilityModeError`、`InvalidCapabilityModeError.Error` |
| [internal/models/factory.go](../../internal/models/factory.go) | 133 | `aa3279a7031e` | `Factory`、`NewFactory`、`Factory.Create`、`newStreamingHTTPClient`、`Factory.requiredCredential` |
| [internal/models/reasoning_replay.go](../../internal/models/reasoning_replay.go) | 57 | `c6b3d1b888cb` | `reasoningOmittingModel`、`reasoningOmittingModel.Generate`、`reasoningOmittingModel.Stream`、`reasoningOmittingModel.WithTools`、`withoutReasoning` |
| [internal/models/registry.go](../../internal/models/registry.go) | 498 | `0c429da68ac7` | `Registry`、`ModelReferenceChecker`、`RegistryOption`、`credentialBackup`、`WithModelReferenceChecker`、`NewRegistry`、`Registry.Revision`、`Registry.ListProviders` |
| [internal/models/runtime.go](../../internal/models/runtime.go) | 129 | `aad40709a414` | `RuntimeSnapshot`、`Registry.ResolveSnapshot`、`runtimeSnapshotFromResolved`、`providerRuntimeAPI` |
| [internal/models/store.go](../../internal/models/store.go) | 582 | `d2a8d3a79b11` | `providersDocument`、`modelsDocument`、`Store`、`NewStore`、`Store.ListProviders`、`Store.GetProvider`、`Store.CreateProvider`、`Store.UpdateProvider` |
| [internal/models/types.go](../../internal/models/types.go) | 206 | `30e178aabee8` | `ProviderType`、`Provider`、`Model`、`MultimediaConfig`、`ModelInfo`、`ResolvedModel`、`CreateProviderInput`、`UpdateProviderInput` |
| [internal/models/validation.go](../../internal/models/validation.go) | 427 | `583a0c78602f` | `normalizeCreateProviderInput`、`normalizeUpdateProviderInput`、`normalizeCreateModelInput`、`normalizeUpdateModelInput`、`normalizeProviderBaseURL`、`normalizeRemoteURL`、`isLoopbackHost`、`normalizeRequiredText` |

<details>
<summary>本章 Go 顶层函数与类型定位（自动扫描）</summary>

### internal/credential/store.go

[internal/credential/store.go](../../internal/credential/store.go)

类型：`Store`（33）。

函数/方法：`New`（42）；`Store.Put`（111）；`Store.Get`（229）；`Store.Delete`（298）；`Store.pathFor`（331）；`validateCredentialTarget`（380）；`replaceFile`（397）。

### internal/credential/system.go

[internal/credential/system.go](../../internal/credential/system.go)

类型：`systemKeyring`（19）；`osKeyring`（25）。

函数/方法：`osKeyring.Set`（27）；`osKeyring.Get`（28）；`osKeyring.Delete`（29）；`NewSystem`（33）；`newSystemWithBackend`（35）；`Store.MigrateLegacy`（47）；`Store.putSystem`（64）；`Store.getSystem`（100）；`Store.deleteSystem`（146）；`removeLegacy`（172）；`Store.readIndex`（182）；`Store.addIndex`（198）；`Store.ExportAll`（214）；`Store.ImportAll`（255）。

### internal/credential/vault.go

[internal/credential/vault.go](../../internal/credential/vault.go)

类型：`BackupVault`（10）。

函数/方法：`BackupVault.Set`（14）；`BackupVault.Get`（15）；`BackupVault.Delete`（16）。

### internal/models/capabilities.go

[internal/models/capabilities.go](../../internal/models/capabilities.go)

类型：`CapabilityMode`（10）；`CapabilityConfig`（20）；`Capabilities`（30）。

函数/方法：`EffectiveCapabilities`（41）；`normalizeCapabilityConfig`（53）；`normalizeCapabilityMode`（82）；`resolveCapability`（95）；`inferCapabilities`（112）；`containsAny`（155）。

### internal/models/errors.go

[internal/models/errors.go](../../internal/models/errors.go)

类型：`InvalidCapabilityModeError`（34）。

函数/方法：`InvalidCapabilityModeError.Error`（38）。

### internal/models/factory.go

[internal/models/factory.go](../../internal/models/factory.go)

类型：`Factory`（35）。

函数/方法：`NewFactory`（39）；`Factory.Create`（45）；`newStreamingHTTPClient`（103）；`Factory.requiredCredential`（121）。

### internal/models/reasoning_replay.go

[internal/models/reasoning_replay.go](../../internal/models/reasoning_replay.go)

类型：`reasoningOmittingModel`（13）。

函数/方法：`reasoningOmittingModel.Generate`（19）；`reasoningOmittingModel.Stream`（23）；`reasoningOmittingModel.WithTools`（27）；`withoutReasoning`（36）。

### internal/models/registry.go

[internal/models/registry.go](../../internal/models/registry.go)

类型：`Registry`（38）；`ModelReferenceChecker`（62）；`RegistryOption`（67）；`credentialBackup`（464）。

函数/方法：`WithModelReferenceChecker`（70）；`NewRegistry`（82）；`Registry.Revision`（97）；`Registry.ListProviders`（102）；`Registry.ListModels`（109）；`Registry.MultimediaConfig`（116）；`Registry.SetMultimediaConfig`（126）；`Registry.CreateProvider`（142）；`Registry.UpdateProvider`（178）；`Registry.DeleteProvider`（243）；`Registry.CreateModel`（276）；`Registry.UpdateModel`（309）；`Registry.DeleteModel`（354）；`Registry.TestModel`（390）；`Registry.configurationChangedLocked`（425）；`Registry.validateImageModelLocked`（430）；`Registry.backupCredential`（470）；`Registry.rollbackCredential`（484）；`Registry.restoreCredential`（491）。

### internal/models/runtime.go

[internal/models/runtime.go](../../internal/models/runtime.go)

类型：`RuntimeSnapshot`（15）。

函数/方法：`Registry.ResolveSnapshot`（44）；`runtimeSnapshotFromResolved`（96）；`providerRuntimeAPI`（120）。

### internal/models/store.go

[internal/models/store.go](../../internal/models/store.go)

类型：`providersDocument`（17）；`modelsDocument`（22）；`Store`（45）。

函数/方法：`NewStore`（56）；`Store.ListProviders`（97）；`Store.GetProvider`（116）；`Store.CreateProvider`（123）；`Store.UpdateProvider`（143）；`Store.DeleteProvider`（172）；`Store.CountModelsByProvider`（198）；`Store.ListModels`（217）；`Store.GetModel`（263）；`Store.MultimediaConfig`（270）；`Store.SetMultimediaConfig`（282）；`Store.ResolveModel`（298）；`Store.CreateModel`（315）；`Store.UpdateModel`（346）；`Store.DeleteModel`（386）；`Store.ensureDocuments`（411）；`Store.listProvidersLocked`（439）；`Store.listModelsRawLocked`（456）；`Store.readModelsDocumentLocked`（464）；`applyModelDefaults`（494）；`Store.getProviderLocked`（504）；`Store.getModelLocked`（518）；`Store.writeProvidersLocked`（532）；`Store.writeModelsLocked`（548）；`Store.writeModelsDocumentLocked`（557）；`providerExists`（575）。

### internal/models/types.go

[internal/models/types.go](../../internal/models/types.go)

类型：`ProviderType`（15）；`Provider`（29）；`Model`（55）；`MultimediaConfig`（94）；`ModelInfo`（101）；`ResolvedModel`（113）；`CreateProviderInput`（123）；`UpdateProviderInput`（147）；`CreateModelInput`（160）；`UpdateModelInput`（179）；`TestResult`（200）。

### internal/models/validation.go

[internal/models/validation.go](../../internal/models/validation.go)

函数/方法：`normalizeCreateProviderInput`（37）；`normalizeUpdateProviderInput`（70）；`normalizeCreateModelInput`（102）；`normalizeUpdateModelInput`（212）；`normalizeProviderBaseURL`（228）；`normalizeRemoteURL`（282）；`isLoopbackHost`（376）；`normalizeRequiredText`（394）。

</details>

## 08 章节文件

实现说明：[08-extensions.md](08-extensions.md)。

| 文件 | 行数 | 内容摘要 | 主要声明（节选） |
| --- | ---: | --- | --- |
| [internal/collaboration/manager.go](../../internal/collaboration/manager.go) | 221 | `61fea918dda8` | `AgentCatalog`、`Manager`、`NewManager`、`Manager.ListAgents`、`Manager.RunAgent`、`Manager.fail`、`Manager.track`、`Manager.untrack` |
| [internal/collaboration/store.go](../../internal/collaboration/store.go) | 75 | `a783a553412d` | `Store`、`NewStore`、`Store.Load`、`Store.Save`、`Store.runPath` |
| [internal/collaboration/types.go](../../internal/collaboration/types.go) | 90 | `fa78ffc07f2e` | `Status`、`Run`、`AgentSummary`、`BuildAgentInput`、`BuiltAgent`、`AgentBuilder`、`SessionDirectoryResolver`、`RunInput` |
| [internal/mcp/catalog.go](../../internal/mcp/catalog.go) | 55 | `22daf03394ba` | `ToolCatalogItem`、`ConnectionState`、`RuntimeStatus`、`RuntimeControlBackend` |
| [internal/mcp/einoadapter/adapter.go](../../internal/mcp/einoadapter/adapter.go) | 292 | `b1a16fcdb115` | `Adapter`、`observedInvokableTool`、`New`、`Adapter.BuildTools`、`observedInvokableTool.Info`、`observedInvokableTool.InvokableRun`、`Adapter.DiscoverTools`、`projectInputSchema` |
| [internal/mcp/einoadapter/backend.go](../../internal/mcp/einoadapter/backend.go) | 1017 | `5d271cc9dd94` | `Backend`、`sessionEntry`、`runtimeStatusEntry`、`runtimeRetryState`、`connectionAttempt`、`NewBackend`、`Backend.Resolve`、`Backend.DiscoverTools` |
| [internal/mcp/einoadapter/http_transport.go](../../internal/mcp/einoadapter/http_transport.go) | 320 | `2fd9e52482d2` | `ipResolver`、`credentialHeaderRoundTripper`、`secureMCPDialer`、`credentialHeaderRoundTripper.RoundTrip`、`secureMCPDialer.DialContext`、`secureMCPDialer.lookup`、`newStreamableHTTPClient`、`newStreamableHTTPClientWithAccess` |
| [internal/mcp/einoadapter/stdio_diagnostics.go](../../internal/mcp/einoadapter/stdio_diagnostics.go) | 428 | `ddf293ee661f` | `stdioStderrCapture`、`validateStdioLaunch`、`resolveStdioExecutable`、`validateKnownStdioArgs`、`expandKnownStdioSandboxPolicy`、`prepareStdioStderrCapture`、`stdioStderrCapture.Read`、`stdioStderrCapture.Cleanup` |
| [internal/mcp/einoadapter/stdio_environment.go](../../internal/mcp/einoadapter/stdio_environment.go) | 240 | `a79b15d7f967` | `resolveStdioEnvironment`、`isolatedStdioEnvironmentOverrides`、`baseStdioEnvironmentNames`、`safeInheritedStdioEnvironmentValue`、`canonicalEnvName`、`applySandboxedStdioLauncherDefaults`、`ensureResolvedStdioCommandOnPath` |
| [internal/mcp/errors.go](../../internal/mcp/errors.go) | 13 | `d59d4933be24` | 组件/配置/样式 |
| [internal/mcp/fingerprint.go](../../internal/mcp/fingerprint.go) | 29 | `a2436d4aa233` | `ServerFingerprint` |
| [internal/mcp/manager.go](../../internal/mcp/manager.go) | 867 | `65b2f1fed2bf` | `Manager`、`catalogCacheEntry`、`projectedMCPTool`、`NewManager`、`Manager.SetRuntimeBackend`、`Manager.DiscoverTools`、`Manager.DiscoverToolsFresh`、`Manager.TestConnection` |
| [internal/mcp/naming.go](../../internal/mcp/naming.go) | 97 | `652633490c75` | `NameExposedTool`、`toSnakeSlug`、`isSimpleSnakeName`、`shortNameHash` |
| [internal/mcp/store.go](../../internal/mcp/store.go) | 230 | `30edb3d56fe9` | `serversDocument`、`Store`、`NewStore`、`Store.List`、`Store.Get`、`Store.Create`、`Store.Update`、`Store.Delete` |
| [internal/mcp/types.go](../../internal/mcp/types.go) | 505 | `7fbc7b0d9706` | `Transport`、`StdioConfig`、`StdioEnvCredential`、`HTTPConfig`、`HTTPHeaderCredential`、`CredentialReader`、`Server`、`CreateServerInput` |
| [internal/skills/aliases.go](../../internal/skills/aliases.go) | 227 | `cb6f095b1ce7` | `skillOverride`、`skillOverridesDocument`、`Manager.Aliases`、`Manager.SetAlias`、`Manager.overridesPath`、`Manager.readOverridesLocked`、`Manager.writeOverridesLocked`、`Manager.removeAliasLocked` |
| [internal/skills/browser.go](../../internal/skills/browser.go) | 99 | `bcff09e67d55` | `Manager.ReadTextFile` |
| [internal/skills/diagnostics.go](../../internal/skills/diagnostics.go) | 261 | `7e0acbf3aa5d` | `evaluateSpecCompatibility`、`validateStandardSkillName`、`evaluateRuntimeCompatibility`、`inspectScriptRuntimes`、`scriptRuntimeCandidates`、`scriptShebangCommand`、`runtimeFromShebang` |
| [internal/skills/discovery.go](../../internal/skills/discovery.go) | 447 | `2789624bb912` | `discoveredPackage`、`Manager.DiscoverFromDirectory`、`Manager.DiscoverFromURL`、`Manager.InstallDiscoveredFromDirectory`、`Manager.InstallDiscoveredFromURL`、`Manager.discoverPackages`、`collectSkillDirectories`、`Manager.projectDiscoveryCandidates` |
| [internal/skills/errors.go](../../internal/skills/errors.go) | 37 | `e30792c10d0f` | 组件/配置/样式 |
| [internal/skills/installer.go](../../internal/skills/installer.go) | 542 | `3915707e0ab5` | `validatedInstallRequest`、`Manager.InstallFromDirectory`、`Manager.installValidatedPackage`、`Manager.Remove`、`copyPackageTree`、`copyRegularFile`、`Manager.installValidatedPackages`、`Manager.StagePackage` |
| [internal/skills/manager.go](../../internal/skills/manager.go) | 295 | `c47735fb38ab` | `Manager`、`NewManager`、`Manager.RegisterRemoteSourceResolver`、`Manager.RemoteSourceResolvers`、`Manager.RootDir`、`Manager.List`、`Manager.Get`、`Manager.NormalizeAndValidateSelection` |
| [internal/skills/parser.go](../../internal/skills/parser.go) | 552 | `401cce86f9ec` | `frontMatter`、`inspectPackage`、`parseDefinition`、`normalizeSkillName`、`normalizeDescription`、`normalizeMetadata`、`normalizeAllowedTools`、`cloneStringMap` |
| [internal/skills/remote_installer.go](../../internal/skills/remote_installer.go) | 1167 | `12ec44317d4b` | `gitSkillRepositorySpec`、`publicSkillDialer`、`Manager.InstallFromURL`、`Manager.prepareRemoteSkillPackage`、`Manager.prepareRemoteSkillArchive`、`Manager.publicSkillHTTPClient`、`Manager.publicSkillHTTPClientWithHTTP2`、`resolveSystemGitExecutable` |
| [internal/skills/remote_source.go](../../internal/skills/remote_source.go) | 336 | `0bf003598c96` | `RemoteSkillSource`、`RemoteSkillSourceResolver`、`registeredRemoteSkillSourceResolver`、`RemoteSkillSourceRegistry`、`RemoteSkillSource.Clone`、`NewRemoteSkillSourceRegistry`、`NewDefaultRemoteSkillSourceRegistry`、`RemoteSkillSourceRegistry.Register` |
| [internal/skills/snapshot.go](../../internal/skills/snapshot.go) | 485 | `dfedb296ae01` | `snapshotBackend`、`skillToolArguments`、`Manager.ResolveRuntimeSnapshot`、`snapshotBackend.List`、`snapshotBackend.Get`、`RuntimeSnapshot.buildContent`、`RuntimeSnapshot.readAsset`、`normalizeAssetPath` |
| [internal/skills/source_providers.go](../../internal/skills/source_providers.go) | 465 | `c93018ef0b91` | `skillsShSourceResolver`、`githubSourceResolver`、`gitLabSourceResolver`、`giteeSourceResolver`、`directArchiveSourceResolver`、`NewSkillsShSourceResolver`、`NewGitHubSourceResolver`、`NewGitLabSourceResolver` |
| [internal/skills/sources.go](../../internal/skills/sources.go) | 310 | `979d80d4f004` | `SourceInfo`、`skillSourcesDocument`、`SourceInfo.Clone`、`Manager.Source`、`Manager.Sources`、`Manager.sourcesPath`、`Manager.readSourcesLocked`、`Manager.writeSourcesLocked` |
| [internal/skills/types.go](../../internal/skills/types.go) | 265 | `ab6f2da6eede` | `RuntimeStatus`、`SpecStatus`、`DiagnosticLevel`、`Diagnostic`、`ScriptRuntime`、`DiscoveryCandidate`、`DiscoveryResult`、`FileInfo` |
| [internal/skills/update.go](../../internal/skills/update.go) | 586 | `a5176e6817c2` | `UpdateCheck`、`UpdateResult`、`installedState`、`installedState.identity`、`Manager.CheckUpdate`、`Manager.Update`、`Manager.Reinstall`、`Manager.ReinstallFromURL` |

<details>
<summary>本章 Go 顶层函数与类型定位（自动扫描）</summary>

### internal/collaboration/manager.go

[internal/collaboration/manager.go](../../internal/collaboration/manager.go)

类型：`AgentCatalog`（24）；`Manager`（29）。

函数/方法：`NewManager`（37）；`Manager.ListAgents`（44）；`Manager.RunAgent`（63）；`Manager.fail`（167）；`Manager.track`（184）；`Manager.untrack`（190）；`Manager.ParentRunFinished`（197）；`stableRunID`（218）。

### internal/collaboration/store.go

[internal/collaboration/store.go](../../internal/collaboration/store.go)

类型：`Store`（17）。

函数/方法：`NewStore`（22）；`Store.Load`（29）；`Store.Save`（49）；`Store.runPath`（63）。

### internal/collaboration/types.go

[internal/collaboration/types.go](../../internal/collaboration/types.go)

类型：`Status`（17）；`Run`（29）；`AgentSummary`（51）；`BuildAgentInput`（57）；`BuiltAgent`（62）；`AgentBuilder`（70）；`SessionDirectoryResolver`（74）；`RunInput`（78）；`RunOutput`（84）。

### internal/mcp/catalog.go

[internal/mcp/catalog.go](../../internal/mcp/catalog.go)

类型：`ToolCatalogItem`（14）；`ConnectionState`（25）；`RuntimeStatus`（38）；`RuntimeControlBackend`（49）。

### internal/mcp/einoadapter/adapter.go

[internal/mcp/einoadapter/adapter.go](../../internal/mcp/einoadapter/adapter.go)

类型：`Adapter`（23）；`observedInvokableTool`（185）。

函数/方法：`New`（28）；`Adapter.BuildTools`（37）；`observedInvokableTool.Info`（190）；`observedInvokableTool.InvokableRun`（194）；`Adapter.DiscoverTools`（204）；`projectInputSchema`（276）。

### internal/mcp/einoadapter/backend.go

[internal/mcp/einoadapter/backend.go](../../internal/mcp/einoadapter/backend.go)

类型：`Backend`（27）；`sessionEntry`（45）；`runtimeStatusEntry`（58）；`runtimeRetryState`（63）；`connectionAttempt`（71）。

函数/方法：`NewBackend`（79）；`Backend.Resolve`（112）；`Backend.DiscoverTools`（179）；`Backend.retainRunSession`（197）；`Backend.ReleaseRun`（220）；`Backend.RuntimeStatus`（237）；`Backend.Invalidate`（259）；`Backend.Retire`（265）；`Backend.resetServer`（269）；`Backend.invalidateSession`（290）；`Backend.removeSessions`（294）；`Backend.latestLiveSessionLocked`（397）；`closeSessionEntry`（413）；`Backend.Close`（428）；`Backend.session`（461）；`Backend.runtimeSession`（465）；`runtimeSessionTarget`（485）；`Backend.sessionWithPolicy`（496）；`Backend.sessionAfterAttempt`（650）；`Backend.markConnectingLocked`（685）；`Backend.finishConnectionAttempt`（709）；`Backend.recordRuntimeRetryFailure`（728）；`sandboxPolicyKey`（763）；`Backend.recordConnectError`（772）；`Backend.findExpectedSessionLocked`（805）；`Backend.markOperationSuccess`（815）；`Backend.recordOperationError`（838）；`Backend.connectStdio`（864）；`Backend.connectHTTP`（976）。

### internal/mcp/einoadapter/http_transport.go

[internal/mcp/einoadapter/http_transport.go](../../internal/mcp/einoadapter/http_transport.go)

类型：`ipResolver`（31）；`credentialHeaderRoundTripper`（38）；`secureMCPDialer`（71）。

函数/方法：`credentialHeaderRoundTripper.RoundTrip`（44）；`secureMCPDialer.DialContext`（79）；`secureMCPDialer.lookup`（123）；`newStreamableHTTPClient`（144）；`newStreamableHTTPClientWithAccess`（151）；`validateMCPDialIP`（198）；`mustParsePrefixes`（233）；`explicitLoopbackHost`（241）；`canonicalHost`（250）；`normalizedOrigin`（254）；`resolveCredentialHeaders`（276）。

### internal/mcp/einoadapter/stdio_diagnostics.go

[internal/mcp/einoadapter/stdio_diagnostics.go](../../internal/mcp/einoadapter/stdio_diagnostics.go)

类型：`stdioStderrCapture`（184）。

函数/方法：`validateStdioLaunch`（24）；`resolveStdioExecutable`（42）；`validateKnownStdioArgs`（83）；`expandKnownStdioSandboxPolicy`（141）；`prepareStdioStderrCapture`（196）；`stdioStderrCapture.Read`（231）；`stdioStderrCapture.Cleanup`（254）；`expandStdioLauncherSandboxPolicy`（268）；`isKnownStdioPackageManager`（288）；`stdioLauncherRuntimeRoots`（299）；`enrichStdioConnectError`（378）。

### internal/mcp/einoadapter/stdio_environment.go

[internal/mcp/einoadapter/stdio_environment.go](../../internal/mcp/einoadapter/stdio_environment.go)

函数/方法：`resolveStdioEnvironment`（25）；`isolatedStdioEnvironmentOverrides`（49）；`baseStdioEnvironmentNames`（81）；`safeInheritedStdioEnvironmentValue`（103）；`canonicalEnvName`（128）；`applySandboxedStdioLauncherDefaults`（145）；`ensureResolvedStdioCommandOnPath`（195）。

### internal/mcp/errors.go

[internal/mcp/errors.go](../../internal/mcp/errors.go)

### internal/mcp/fingerprint.go

[internal/mcp/fingerprint.go](../../internal/mcp/fingerprint.go)

函数/方法：`ServerFingerprint`（14）。

### internal/mcp/manager.go

[internal/mcp/manager.go](../../internal/mcp/manager.go)

类型：`Manager`（24）；`catalogCacheEntry`（36）；`projectedMCPTool`（737）。

函数/方法：`NewManager`（42）；`Manager.SetRuntimeBackend`（58）；`Manager.DiscoverTools`（69）；`Manager.DiscoverToolsFresh`（74）；`Manager.TestConnection`（122）；`Manager.Close`（131）；`Manager.SetReferenceChecker`（142）；`Manager.Revision`（148）；`Manager.List`（154）；`Manager.Get`（158）；`Manager.RuntimeStatus`（163）；`Manager.SetEnabled`（194）；`Manager.Disconnect`（219）；`Manager.SetToolRisk`（234）；`Manager.Create`（275）；`Manager.Update`（302）；`Manager.ParentRunFinished`（340）；`Manager.Delete`（349）；`Manager.NormalizeAndValidateSelection`（380）；`Manager.ResolveRuntimeSnapshot`（448）；`Manager.ResolveRuntimeSnapshotAvailable`（461）；`runtimeServerFailure`（554）；`Manager.ResolveRuntimeSnapshotBestEffort`（577）；`Manager.resolveRuntimeSnapshot`（581）；`Manager.resolveRuntimeProjection`（642）；`Manager.cachedCatalogForProjection`（719）；`projectedMCPTool.Info`（743）；`finalizeRuntimeSnapshot`（753）；`Manager.bumpRevision`（771）；`cloneStdio`（777）；`cloneHTTP`（796）；`Manager.invalidateCatalog`（814）；`Manager.invalidateRuntimeSession`（820）；`cloneCatalog`（829）；`cloneJSONMap`（843）；`cloneJSONValue`（854）。

### internal/mcp/naming.go

[internal/mcp/naming.go](../../internal/mcp/naming.go)

函数/方法：`NameExposedTool`（20）；`toSnakeSlug`（57）；`isSimpleSnakeName`（81）；`shortNameHash`（94）。

### internal/mcp/store.go

[internal/mcp/store.go](../../internal/mcp/store.go)

类型：`serversDocument`（17）；`Store`（26）。

函数/方法：`NewStore`（31）；`Store.List`（61）；`Store.Get`（80）；`Store.Create`（102）；`Store.Update`（124）；`Store.Delete`（154）；`Store.listLocked`（183）；`Store.writeLocked`（211）；`validateContext`（222）。

### internal/mcp/types.go

[internal/mcp/types.go](../../internal/mcp/types.go)

类型：`Transport`（31）；`StdioConfig`（42）；`StdioEnvCredential`（51）；`HTTPConfig`（59）；`HTTPHeaderCredential`（67）；`CredentialReader`（74）；`Server`（81）；`CreateServerInput`（111）；`UpdateServerInput`（122）；`ToolSelection`（133）；`RuntimeServerSnapshot`（142）；`RuntimeToolSnapshot`（151）；`RuntimeServerFailure`（163）；`RuntimeSnapshot`（172）；`ResolveRequest`（190）；`RuntimeBackend`（198）；`ServerReferenceChecker`（205）。

函数/方法：`Server.UnmarshalJSON`（100）；`RuntimeSnapshot.Enabled`（183）；`validateServer`（209）；`validateServerKey`（236）；`validateTransportConfig`（256）；`validStdioEnvName`（362）；`isLoopbackHost`（378）；`isReservedMCPHeader`（387）；`normalizeRawToolName`（407）；`cloneServer`（423）；`cloneSelections`（445）；`ToolRisk`（458）；`ValidateStdioConfig`（472）；`ValidateHTTPConfig`（477）；`ValidateHTTPHeaderName`（484）；`ValidateServerForRuntime`（497）；`NormalizeRawToolName`（503）。

### internal/skills/aliases.go

[internal/skills/aliases.go](../../internal/skills/aliases.go)

类型：`skillOverride`（29）；`skillOverridesDocument`（33）。

函数/方法：`Manager.Aliases`（43）；`Manager.SetAlias`（78）；`Manager.overridesPath`（137）；`Manager.readOverridesLocked`（141）；`Manager.writeOverridesLocked`（183）；`Manager.removeAliasLocked`（198）；`normalizeSkillAlias`（210）。

### internal/skills/browser.go

[internal/skills/browser.go](../../internal/skills/browser.go)

函数/方法：`Manager.ReadTextFile`（21）。

### internal/skills/diagnostics.go

[internal/skills/diagnostics.go](../../internal/skills/diagnostics.go)

函数/方法：`evaluateSpecCompatibility`（17）；`validateStandardSkillName`（31）；`evaluateRuntimeCompatibility`（53）；`inspectScriptRuntimes`（140）；`scriptRuntimeCandidates`（184）；`scriptShebangCommand`（212）；`runtimeFromShebang`（241）。

### internal/skills/discovery.go

[internal/skills/discovery.go](../../internal/skills/discovery.go)

类型：`discoveredPackage`（16）。

函数/方法：`Manager.DiscoverFromDirectory`（25）；`Manager.DiscoverFromURL`（50）；`Manager.InstallDiscoveredFromDirectory`（76）；`Manager.InstallDiscoveredFromURL`（111）；`Manager.discoverPackages`（157）；`collectSkillDirectories`（268）；`Manager.projectDiscoveryCandidates`（319）；`invalidDiscoveryPackage`（340）；`truncateDiscoveryError`（354）；`selectDiscoveredPackages`（365）；`canonicalDiscoveryDirectory`（393）；`sourceRelativeSkillPath`（413）；`discoveryPathMatches`（430）；`safeDisplayRemoteURL`（438）。

### internal/skills/errors.go

[internal/skills/errors.go](../../internal/skills/errors.go)

### internal/skills/installer.go

[internal/skills/installer.go](../../internal/skills/installer.go)

类型：`validatedInstallRequest`（342）。

函数/方法：`Manager.InstallFromDirectory`（23）；`Manager.installValidatedPackage`（69）；`Manager.Remove`（164）；`copyPackageTree`（236）；`copyRegularFile`（282）；`Manager.installValidatedPackages`（352）；`Manager.StagePackage`（482）。

### internal/skills/manager.go

[internal/skills/manager.go](../../internal/skills/manager.go)

类型：`Manager`（28）。

函数/方法：`NewManager`（41）；`Manager.RegisterRemoteSourceResolver`（94）；`Manager.RemoteSourceResolvers`（105）；`Manager.RootDir`（113）；`Manager.List`（124）；`Manager.Get`（188）；`Manager.NormalizeAndValidateSelection`（209）；`Manager.getLocked`（253）；`normalizeInstalledDirectoryName`（273）；`Manager.validate`（290）。

### internal/skills/parser.go

[internal/skills/parser.go](../../internal/skills/parser.go)

类型：`frontMatter`（27）。

函数/方法：`inspectPackage`（55）；`parseDefinition`（259）；`normalizeSkillName`（303）；`normalizeDescription`（329）；`normalizeMetadata`（333）；`normalizeAllowedTools`（366）；`cloneStringMap`（395）；`scanPackageFiles`（406）；`shouldIgnorePackagePath`（528）；`packageIdentity`（543）。

### internal/skills/remote_installer.go

[internal/skills/remote_installer.go](../../internal/skills/remote_installer.go)

类型：`gitSkillRepositorySpec`（239）；`publicSkillDialer`（1124）。

函数/方法：`Manager.InstallFromURL`（39）；`Manager.prepareRemoteSkillPackage`（87）；`Manager.prepareRemoteSkillArchive`（135）；`Manager.publicSkillHTTPClient`（211）；`Manager.publicSkillHTTPClientWithHTTP2`（215）；`resolveSystemGitExecutable`（256）；`probeGitExecutable`（326）；`skillGitEnvironment`（351）；`gitExecutableCandidates`（371）；`isGitSkillRepositorySource`（403）；`gitSkillRepositorySpecForSource`（408）；`Manager.materializeGitSkillRepository`（478）；`gitSkillCandidateDirectories`（587）；`selectGitSkillArchivePaths`（614）；`minimalGitSkillArchivePaths`（646）；`sanitizeRemoteWrapperSegment`（684）；`Manager.downloadRemoteArchive`（705）；`Manager.extractRemoteArchive`（769）；`validateArchiveEntry`（840）；`extractArchiveRegularFile`（871）；`findRemoteSkillPackage`（924）；`selectRemoteSkillByName`（1013）；`readRemoteSkillDefinitionName`（1076）；`stripArchiveWrapper`（1101）；`limitStrings`（1110）；`publicSkillDialer.DialContext`（1126）；`isPublicSkillIP`（1156）。

### internal/skills/remote_source.go

[internal/skills/remote_source.go](../../internal/skills/remote_source.go)

类型：`RemoteSkillSource`（24）；`RemoteSkillSourceResolver`（60）；`registeredRemoteSkillSourceResolver`（70）；`RemoteSkillSourceRegistry`（80）。

函数/方法：`RemoteSkillSource.Clone`（39）；`NewRemoteSkillSourceRegistry`（87）；`NewDefaultRemoteSkillSourceRegistry`（95）；`RemoteSkillSourceRegistry.Register`（112）；`RemoteSkillSourceRegistry.Names`（147）；`RemoteSkillSourceRegistry.Resolve`（162）；`normalizeResolvedRemoteSkillSource`（240）；`cloneURL`（263）；`splitURLPath`（271）；`normalizeRepositoryPathSegment`（283）；`normalizeRemoteSkillPath`（300）；`parsePublicSkillURL`（318）。

### internal/skills/snapshot.go

[internal/skills/snapshot.go](../../internal/skills/snapshot.go)

类型：`snapshotBackend`（130）；`skillToolArguments`（178）。

函数/方法：`Manager.ResolveRuntimeSnapshot`（27）；`snapshotBackend.List`（135）；`snapshotBackend.Get`（156）；`RuntimeSnapshot.buildContent`（187）；`RuntimeSnapshot.readAsset`（254）；`normalizeAssetPath`（320）；`NormalizeScriptPath`（344）；`validateAssetPathComponents`（358）；`buildSkillInstruction`（392）；`buildSkillToolDescription`（400）；`snapshotRevision`（414）；`clonePackage`（426）；`normalizeSelection`（434）；`Manager.RuntimeIdentities`（455）。

### internal/skills/source_providers.go

[internal/skills/source_providers.go](../../internal/skills/source_providers.go)

类型：`skillsShSourceResolver`（53）；`githubSourceResolver`（119）；`gitLabSourceResolver`（212）；`giteeSourceResolver`（343）；`directArchiveSourceResolver`（431）。

函数/方法：`NewSkillsShSourceResolver`（16）；`NewGitHubSourceResolver`（21）；`NewGitLabSourceResolver`（29）；`NewGiteeSourceResolver`（44）；`NewDirectArchiveSourceResolver`（49）；`skillsShSourceResolver.Name`（55）；`skillsShSourceResolver.Priority`（57）；`skillsShSourceResolver.Match`（59）；`skillsShSourceResolver.Resolve`（67）；`githubSourceResolver.Name`（121）；`githubSourceResolver.Priority`（123）；`githubSourceResolver.Match`（125）；`githubSourceResolver.Resolve`（137）；`gitLabSourceResolver.Name`（216）；`gitLabSourceResolver.Priority`（227）；`gitLabSourceResolver.Match`（229）；`gitLabSourceResolver.Resolve`（250）；`giteeSourceResolver.Name`（345）；`giteeSourceResolver.Priority`（347）；`giteeSourceResolver.Match`（349）；`giteeSourceResolver.Resolve`（363）；`directArchiveSourceResolver.Name`（433）；`directArchiveSourceResolver.Priority`（435）；`directArchiveSourceResolver.Match`（437）；`directArchiveSourceResolver.Resolve`（439）；`looksLikeArchivePath`（457）。

### internal/skills/sources.go

[internal/skills/sources.go](../../internal/skills/sources.go)

类型：`SourceInfo`（34）；`skillSourcesDocument`（68）。

函数/方法：`SourceInfo.Clone`（57）；`Manager.Source`（79）；`Manager.Sources`（112）；`Manager.sourcesPath`（137）；`Manager.readSourcesLocked`（141）；`Manager.writeSourcesLocked`（176）；`normalizeStoredSourceInfo`（187）；`localSourceInfo`（266）；`remoteSourceInfo`（281）；`Manager.removeSourceLocked`（300）。

### internal/skills/types.go

[internal/skills/types.go](../../internal/skills/types.go)

类型：`RuntimeStatus`（27）；`SpecStatus`（39）；`DiagnosticLevel`（47）；`Diagnostic`（59）；`ScriptRuntime`（69）；`DiscoveryCandidate`（85）；`DiscoveryResult`（94）；`FileInfo`（109）；`Info`（123）；`Package`（184）；`RuntimeSnapshot`（197）。

函数/方法：`RuntimeSnapshot.Enabled`（216）；`RuntimeSnapshot.Middleware`（224）；`RuntimeSnapshot.ToolDefinition`（230）；`RuntimeSnapshot.PackageNames`（235）；`RuntimeSnapshot.PackageIdentities`（242）；`RuntimeSnapshot.ScriptRuntimeCommands`（252）。

### internal/skills/update.go

[internal/skills/update.go](../../internal/skills/update.go)

类型：`UpdateCheck`（19）；`UpdateResult`（36）；`installedState`（51）。

函数/方法：`installedState.identity`（57）；`Manager.CheckUpdate`（69）；`Manager.Update`（104）；`Manager.Reinstall`（132）；`Manager.ReinstallFromURL`（181）；`Manager.ReinstallFromDirectory`（223）；`Manager.prepareUpdateContext`（265）；`Manager.operationContextForSource`（295）；`Manager.captureInstalledState`（311）；`Manager.installedStateLocked`（324）；`installedStateMatches`（362）；`Manager.preparePackageFromSource`（372）；`Manager.replaceValidatedPackage`（414）；`cloneSkillSourcesDocument`（577）。

</details>

## 09 章节文件

实现说明：[09-background.md](09-background.md)。

| 文件 | 行数 | 内容摘要 | 主要声明（节选） |
| --- | ---: | --- | --- |
| [internal/notifications/service.go](../../internal/notifications/service.go) | 143 | `b2cce22a07de` | `Level`、`Notification`、`Provider`、`Sender`、`Service`、`EventProvider`、`New`、`Service.AddProvider` |
| [internal/proactive/decision.go](../../internal/proactive/decision.go) | 69 | `02c44fad41be` | `DecisionEngine`、`RuleDecisionEngine`、`RuleDecisionEngine.Decide`、`InQuietHours` |
| [internal/proactive/executors.go](../../internal/proactive/executors.go) | 126 | `9a44c5ef09f1` | `ExecutionResult`、`Executor`、`NotificationExecutor`、`AgentExecutor`、`NewNotificationExecutor`、`NotificationExecutor.Action`、`NotificationExecutor.Execute`、`NewAgentExecutor` |
| [internal/proactive/manager.go](../../internal/proactive/manager.go) | 734 | `62dfe2955c17` | `ApprovalReader`、`AutomationRunner`、`AutomationTasks`、`WorkspaceScanner`、`Manager`、`NewManager`、`Manager.registerExecutor`、`Manager.Start` |
| [internal/proactive/store.go](../../internal/proactive/store.go) | 454 | `00e3753ee8dc` | `storeDocument`、`Store`、`NewStore`、`normalizeDocument`、`Store.EnqueueEvent`、`Store.PendingEvents`、`Store.PendingEventCount`、`Store.RemovePendingEvent` |
| [internal/proactive/types.go](../../internal/proactive/types.go) | 171 | `1dc9c66a3215` | `EventKind`、`Action`、`QuietHours`、`EventRule`、`Settings`、`Event`、`Decision`、`RecordStatus` |
| [internal/proactive/workspace_monitor.go](../../internal/proactive/workspace_monitor.go) | 151 | `c044c178321b` | `WorkspaceMonitor`、`NewWorkspaceMonitor`、`WorkspaceMonitor.Scan`、`scanWorkspace`、`workspaceChangeSummary` |
| [internal/tasks/manager.go](../../internal/tasks/manager.go) | 247 | `563b8c475924` | `Event`、`AgentCatalog`、`SessionRepository`、`TurnRuntime`、`Manager`、`NewManager`、`Manager.Start`、`Manager.Close` |
| [internal/tasks/manager_events.go](../../internal/tasks/manager_events.go) | 255 | `eab51acf1186` | `Manager.handleRuntimePayload`、`Manager.resultPreview`、`Manager.failRun`、`Manager.maybeRetry`、`safeToRetry`、`Manager.recoverRetries`、`Manager.releaseActive`、`Manager.taskRunOccupancy` |
| [internal/tasks/manager_schedule.go](../../internal/tasks/manager_schedule.go) | 507 | `3c1f0aab61da` | `Manager.RunNow`、`Manager.RunAutomation`、`Manager.createAutomationRun`、`Manager.AutomationByOrigin`、`Manager.CancelRun`、`Manager.schedulerLoop`、`Manager.runCycle`、`Manager.enqueueDue` |
| [internal/tasks/manager_task.go](../../internal/tasks/manager_task.go) | 457 | `c5c56618d2c3` | `Manager.Create`、`Manager.Update`、`Manager.SetStatus`、`Manager.Archive`、`Manager.Delete`、`Manager.DeleteRun`、`Manager.ClearRuns`、`Manager.RunBySession` |
| [internal/tasks/schedule.go](../../internal/tasks/schedule.go) | 261 | `be433872891b` | `normalizeTaskInput`、`normalizeLimits`、`normalizeSchedule`、`nextOccurrence`、`sameWallClock`、`advanceOccurrence`、`parseTimeOfDay`、`containsWeekday` |
| [internal/tasks/store.go](../../internal/tasks/store.go) | 857 | `042ae1b80728` | `taskDocument`、`runDocument`、`Store`、`runRef`、`NewStore`、`Store.Issues`、`Store.ListTasks`、`Store.listTasksLocked` |
| [internal/tasks/types.go](../../internal/tasks/types.go) | 259 | `2b3e94a25c32` | `ExecutionType`、`ConversationMode`、`TaskStatus`、`ScheduleType`、`MisfirePolicy`、`OverlapPolicy`、`Schedule`、`Limits` |

<details>
<summary>本章 Go 顶层函数与类型定位（自动扫描）</summary>

### internal/notifications/service.go

[internal/notifications/service.go](../../internal/notifications/service.go)

类型：`Level`（17）；`Notification`（29）；`Provider`（46）；`Sender`（51）；`Service`（55）；`EventProvider`（130）。

函数/方法：`New`（61）；`Service.AddProvider`（71）；`Service.Send`（80）；`Service.Recent`（118）；`NewEventProvider`（134）；`EventProvider.Send`（138）。

### internal/proactive/decision.go

[internal/proactive/decision.go](../../internal/proactive/decision.go)

类型：`DecisionEngine`（11）；`RuleDecisionEngine`（15）。

函数/方法：`RuleDecisionEngine.Decide`（17）；`InQuietHours`（42）。

### internal/proactive/executors.go

[internal/proactive/executors.go](../../internal/proactive/executors.go)

类型：`ExecutionResult`（13）；`Executor`（18）；`NotificationExecutor`（23）；`AgentExecutor`（57）。

函数/方法：`NewNotificationExecutor`（27）；`NotificationExecutor.Action`（31）；`NotificationExecutor.Execute`（33）；`NewAgentExecutor`（61）；`AgentExecutor.Action`（65）；`AgentExecutor.Execute`（67）；`buildAgentPrompt`（99）。

### internal/proactive/manager.go

[internal/proactive/manager.go](../../internal/proactive/manager.go)

类型：`ApprovalReader`（24）；`AutomationRunner`（29）；`AutomationTasks`（35）；`WorkspaceScanner`（43）；`Manager`（47）。

函数/方法：`NewManager`（72）；`Manager.registerExecutor`（104）；`Manager.Start`（110）；`Manager.Close`（149）；`Manager.Settings`（176）；`Manager.UpdateSettings`（178）；`Manager.Records`（194）；`Manager.Status`（196）；`Manager.RunHeartbeat`（208）；`Manager.loop`（222）；`Manager.enqueue`（253）；`Manager.signalWake`（269）；`Manager.drainPendingEvents`（276）；`Manager.beginHeartbeat`（295）；`Manager.endHeartbeat`（305）；`Manager.handleTaskPayload`（311）；`Manager.handleRuntimePayload`（331）；`proactiveEventFromTask`（372）；`Manager.processEvent`（445）；`Manager.executeRecord`（460）；`Manager.heartbeat`（536）；`Manager.finalizeCompletedAutomations`（595）；`Manager.finalizeAutomationRun`（615）；`Manager.reconcileRecords`（644）；`isApprovalReminder`（704）；`Manager.reminderStillPending`（708）；`Manager.publishRecord`（719）；`Manager.publishStatus`（724）；`Manager.publish`（729）。

### internal/proactive/store.go

[internal/proactive/store.go](../../internal/proactive/store.go)

类型：`storeDocument`（22）；`Store`（34）。

函数/方法：`NewStore`（41）；`normalizeDocument`（79）；`Store.EnqueueEvent`（106）；`Store.PendingEvents`（134）；`Store.PendingEventCount`（140）；`Store.RemovePendingEvent`（146）；`Store.DismissApproval`（163）；`NormalizeSettings`（199）；`Store.Settings`（249）；`Store.UpdateSettings`（255）；`Store.Records`（270）；`Store.FindByEventKey`（282）；`Store.FindByAutomationRunID`（293）；`Store.LastHandledKind`（304）；`Store.PutRecord`（322）；`retainRecords`（340）；`Store.DeferredRecords`（360）；`Store.WorkspaceSnapshot`（372）；`Store.PutWorkspaceSnapshot`（383）；`Store.SetHeartbeat`（396）；`Store.LastHeartbeat`（405）；`Store.persistLocked`（416）；`cloneSettings`（427）；`cloneRules`（432）；`cloneFileMap`（440）；`parseClock`（448）。

### internal/proactive/types.go

[internal/proactive/types.go](../../internal/proactive/types.go)

类型：`EventKind`（7）；`Action`（20）；`QuietHours`（28）；`EventRule`（35）；`Settings`（51）；`Event`（85）；`Decision`（104）；`RecordStatus`（112）；`Record`（122）；`WorkspaceFileStamp`（138）；`WorkspaceSnapshot`（144）；`Status`（155）；`PublicEvent`（164）。

函数/方法：`DefaultSettings`（61）。

### internal/proactive/workspace_monitor.go

[internal/proactive/workspace_monitor.go](../../internal/proactive/workspace_monitor.go)

类型：`WorkspaceMonitor`（20）。

函数/方法：`NewWorkspaceMonitor`（25）；`WorkspaceMonitor.Scan`（29）；`scanWorkspace`（53）；`workspaceChangeSummary`（124）。

### internal/tasks/manager.go

[internal/tasks/manager.go](../../internal/tasks/manager.go)

类型：`Event`（21）；`AgentCatalog`（31）；`SessionRepository`（36）；`TurnRuntime`（44）；`Manager`（50）。

函数/方法：`NewManager`（78）；`Manager.Start`（93）；`Manager.Close`（125）；`Manager.List`（169）；`Manager.Runs`（184）；`Manager.Run`（188）；`Manager.ActiveRunForSession`（194）；`Manager.SetNotificationService`（209）；`string.func`（217）；`Manager.agentSuspended`（236）；`Manager.publish`（242）。

### internal/tasks/manager_events.go

[internal/tasks/manager_events.go](../../internal/tasks/manager_events.go)

函数/方法：`Manager.handleRuntimePayload`（14）；`Manager.resultPreview`（110）；`Manager.failRun`（132）；`Manager.maybeRetry`（160）；`safeToRetry`（183）；`Manager.recoverRetries`（190）；`Manager.releaseActive`（210）；`Manager.taskRunOccupancy`（230）。

### internal/tasks/manager_schedule.go

[internal/tasks/manager_schedule.go](../../internal/tasks/manager_schedule.go)

函数/方法：`Manager.RunNow`（16）；`Manager.RunAutomation`（46）；`Manager.createAutomationRun`（95）；`Manager.AutomationByOrigin`（119）；`Manager.CancelRun`（127）；`Manager.schedulerLoop`（167）；`Manager.runCycle`（182）；`Manager.enqueueDue`（195）；`Manager.dispatch`（248）；`Manager.dispatchLocked`（254）；`Manager.startNotificationRun`（317）；`Manager.startRun`（357）；`Manager.resolveRunSession`（443）；`continuousTaskSessionTitle`（489）；`taskSessionTitle`（495）。

### internal/tasks/manager_task.go

[internal/tasks/manager_task.go](../../internal/tasks/manager_task.go)

函数/方法：`Manager.Create`（14）；`Manager.Update`（43）；`Manager.SetStatus`（81）；`Manager.Archive`（120）；`Manager.Delete`（137）；`Manager.DeleteRun`（186）；`Manager.ClearRuns`（208）；`Manager.RunBySession`（223）；`Manager.AutomationBySession`（229）；`Manager.DeleteConversation`（249）；`Manager.deleteUnreferencedRunSessions`（306）；`unreferencedRunSessionIDs`（317）；`Manager.deleteSessionIDs`（349）；`taskRunSessionIDs`（371）；`normalizeTaskFields`（382）；`Manager.cancelQueuedRunsWhenPaused`（409）；`normalizeExecution`（424）；`normalizeConversationMode`（438）；`initialNextRun`（448）。

### internal/tasks/schedule.go

[internal/tasks/schedule.go](../../internal/tasks/schedule.go)

函数/方法：`normalizeTaskInput`（21）；`normalizeLimits`（47）；`normalizeSchedule`（87）；`nextOccurrence`（155）；`sameWallClock`（215）；`advanceOccurrence`（221）；`parseTimeOfDay`（238）；`containsWeekday`（254）。

### internal/tasks/store.go

[internal/tasks/store.go](../../internal/tasks/store.go)

类型：`taskDocument`（33）；`runDocument`（38）；`Store`（45）；`runRef`（54）。

函数/方法：`NewStore`（59）；`Store.Issues`（82）；`Store.ListTasks`（101）；`Store.listTasksLocked`（107）；`Store.GetTask`（155）；`Store.FindInternalTaskByOrigin`（163）；`Store.getTaskLocked`（188）；`Store.CreateTask`（206）；`Store.UpdateTask`（234）；`Store.ArchiveTask`（246）；`Store.DeleteTask`（286）；`Store.CreateRun`（314）；`Store.GetRun`（361）；`Store.getRunLocked`（367）；`Store.MutateRun`（393）；`Store.ListRuns`（438）；`Store.DeleteRun`（450）；`Store.DeleteRuns`（468）；`Store.RunBySession`（495）；`Store.ReferencesBySession`（522）；`Store.deleteRunLocked`（555）；`Store.listRunsLocked`（567）；`Store.ListDispatchableRuns`（603）；`Store.CancelQueuedAutomaticRuns`（628）；`Store.ReconcileQueuedAutomationRuns`（659）；`Store.ReconcileInterrupted`（694）；`Store.readTaskLocked`（725）；`Store.readRunLocked`（745）；`validateStoredTask`（759）；`validateStoredRun`（795）；`Store.writeTaskLocked`（829）；`Store.writeRunLocked`（836）；`Store.taskConfigPath`（843）；`Store.taskDir`（847）；`Store.runsDir`（851）；`Store.runPath`（855）。

### internal/tasks/types.go

[internal/tasks/types.go](../../internal/tasks/types.go)

类型：`ExecutionType`（9）；`ConversationMode`（21）；`TaskStatus`（28）；`ScheduleType`（36）；`MisfirePolicy`（46）；`OverlapPolicy`（53）；`Schedule`（62）；`Limits`（76）；`Task`（85）；`RunStatus`（119）；`RunTrigger`（134）；`ApprovalSnapshot`（145）；`Run`（154）；`CreateInput`（187）；`UpdateInput`（200）；`Issue`（210）；`AutomationInput`（252）。

函数/方法：`Task.EffectiveConversationMode`（220）；`Task.EffectiveExecution`（230）；`RunStatus.Terminal`（237）；`RunStatus.Active`（246）。

</details>

## 10 章节文件

实现说明：[10-workspace-backup.md](10-workspace-backup.md)。

| 文件 | 行数 | 内容摘要 | 主要声明（节选） |
| --- | ---: | --- | --- |
| [internal/databackup/archive.go](../../internal/databackup/archive.go) | 378 | `59aa6b9bfdeb` | `fileRecord`、`manifest`、`Create`、`Verify`、`Restore`、`restoreReader`、`inspect`、`inspectReader` |
| [internal/databackup/encrypted.go](../../internal/databackup/encrypted.go) | 305 | `d7e7dfd4be51` | `CreateEncrypted`、`writeEncryptedArchive`、`openEncryptedArchive`、`VerifyEncrypted`、`RestoreEncrypted`、`RestoreEncryptedAndImport`、`IsEncrypted` |
| [internal/databackup/schedule.go](../../internal/databackup/schedule.go) | 356 | `daa89f956409` | `restorePlan`、`backupPlan`、`BackupStatus`、`RestoreStatus`、`PassphraseVault`、`RestoreOptions`、`ScheduleEncryptedBackup`、`PendingBackupStatus` |
| [internal/searchindex/controller.go](../../internal/searchindex/controller.go) | 97 | `21fd2c283d5d` | `Controller`、`Status`、`refreshState`、`NewController`、`Controller.Acquire`、`Controller.refresh`、`Controller.Close` |
| [internal/searchindex/document_scan.go](../../internal/searchindex/document_scan.go) | 100 | `ad61f1829a33` | `RefreshDocuments` |
| [internal/searchindex/documents.go](../../internal/searchindex/documents.go) | 120 | `a2d32080130c` | `DocumentResult`、`Index.DocumentCurrent`、`Index.ReplaceDocument`、`Index.PruneDocuments`、`Index.SearchDocuments` |
| [internal/searchindex/index.go](../../internal/searchindex/index.go) | 231 | `be93ce34c359` | `Index`、`Session`、`Message`、`Result`、`Open`、`Index.Close`、`Index.CachedSession`、`Index.Replace` |
| [internal/searchindex/service.go](../../internal/searchindex/service.go) | 88 | `917253fce5a3` | `Service`、`NewService`、`Service.SearchSessions`、`Service.SearchDocuments`、`Service.Close` |
| [internal/searchindex/sessions.go](../../internal/searchindex/sessions.go) | 76 | `05416e0674fb` | `RefreshSessions` |
| [internal/usecases/agent_lifecycle.go](../../internal/usecases/agent_lifecycle.go) | 62 | `f5c982d19934` | `AgentLifecycle`、`NewAgentLifecycle`、`AgentLifecycle.DeleteAgent`、`AgentLifecycle.DeleteSession`、`AgentLifecycle.ClearSessionRules` |
| [internal/usecases/mcp_configuration.go](../../internal/usecases/mcp_configuration.go) | 395 | `6875591807d8` | `MCPCredentialInput`、`MCPConfigurationRequest`、`MCPConfiguration`、`MCPCredentials`、`NewMCPConfiguration`、`MCPConfiguration.Create`、`MCPConfiguration.Update`、`MCPConfiguration.Delete` |
| [internal/usecases/skill_maintenance.go](../../internal/usecases/skill_maintenance.go) | 46 | `369d46987cb8` | `SkillUsers`、`SkillRemover`、`SkillMaintenance`、`NewSkillMaintenance`、`SkillMaintenance.Remove` |
| [internal/usecases/vision.go](../../internal/usecases/vision.go) | 92 | `8f6ee7a26d03` | `VisionInspector`、`NewVisionInspector`、`VisionInspector.Inspect` |
| [internal/websearch/backends.go](../../internal/websearch/backends.go) | 395 | `44bfcd8efad0` | `Backend`、`anySearchBackend`、`anySearchEnvelope`、`anySearchItem`、`duckDuckGoBackend`、`bingBackend`、`anySearchBackend.Name`、`anySearchBackend.Search` |
| [internal/websearch/service.go](../../internal/websearch/service.go) | 505 | `643f47a28703` | `Input`、`Result`、`Attempt`、`Diagnostics`、`Output`、`searchResult`、`Service`、`Option` |
| [internal/workspace/browser.go](../../internal/workspace/browser.go) | 373 | `1f15c602f311` | `BrowseEntry`、`DirectoryListing`、`FilePreview`、`Manager.ListDirectory`、`Manager.PreviewFile`、`browseEntryType`、`displayRelativePath`、`detectPreviewMIME` |
| [internal/workspace/errors.go](../../internal/workspace/errors.go) | 55 | `e57359aedb2e` | 组件/配置/样式 |
| [internal/workspace/manager.go](../../internal/workspace/manager.go) | 915 | `aa61cd69bf06` | `Manager`、`NewManager`、`Manager.ManagedRoot`、`Manager.ManagedPath`、`Manager.Validate`、`Manager.Resolve`、`Manager.OpenRoot`、`Manager.DeleteManaged` |
| [internal/workspace/path.go](../../internal/workspace/path.go) | 139 | `dd9d3298a621` | `NormalizeRelativePath` |
| [internal/workspace/types.go](../../internal/workspace/types.go) | 49 | `27b5fb4d9161` | `Mode`、`Workspace` |
| [internal/workspaceview/service.go](../../internal/workspaceview/service.go) | 181 | `d5face681d06` | `Service`、`Overview`、`NewService`、`Service.Overview`、`Service.ListDirectory`、`Service.PreviewFile`、`Service.resolve`、`shouldSkipOverviewDirectory` |

<details>
<summary>本章 Go 顶层函数与类型定位（自动扫描）</summary>

### internal/databackup/archive.go

[internal/databackup/archive.go](../../internal/databackup/archive.go)

类型：`fileRecord`（26）；`manifest`（32）。

函数/方法：`Create`（40）；`Verify`（183）；`Restore`（189）；`restoreReader`（198）；`inspect`（247）；`inspectReader`（256）；`safeArchivePath`（356）；`within`（364）；`realDirectory`（369）。

### internal/databackup/encrypted.go

[internal/databackup/encrypted.go](../../internal/databackup/encrypted.go)

函数/方法：`CreateEncrypted`（23）；`writeEncryptedArchive`（82）；`openEncryptedArchive`（191）；`VerifyEncrypted`（226）；`RestoreEncrypted`（236）；`RestoreEncryptedAndImport`（279）；`IsEncrypted`（305）。

### internal/databackup/schedule.go

[internal/databackup/schedule.go](../../internal/databackup/schedule.go)

类型：`restorePlan`（17）；`backupPlan`（24）；`BackupStatus`（28）；`RestoreStatus`（34）；`PassphraseVault`（42）；`RestoreOptions`（220）。

函数/方法：`ScheduleEncryptedBackup`（51）；`PendingBackupStatus`（96）；`RecordBackupFailure`（116）；`CancelPendingBackup`（125）；`ApplyPendingBackup`（142）；`ScheduleEncryptedRestore`（183）；`ScheduleRestore`（226）；`PendingRestoreStatus`（253）；`CancelPendingRestore`（265）；`ApplyPendingRestore`（279）；`fileSHA256`（332）。

### internal/searchindex/controller.go

[internal/searchindex/controller.go](../../internal/searchindex/controller.go)

类型：`Controller`（11）；`Status`（24）；`refreshState`（28）。

函数/方法：`NewController`（33）；`Controller.Acquire`（40）；`Controller.refresh`（65）；`Controller.Close`（85）。

### internal/searchindex/document_scan.go

[internal/searchindex/document_scan.go](../../internal/searchindex/document_scan.go)

函数/方法：`RefreshDocuments`（15）。

### internal/searchindex/documents.go

[internal/searchindex/documents.go](../../internal/searchindex/documents.go)

类型：`DocumentResult`（11）。

函数/方法：`Index.DocumentCurrent`（17）；`Index.ReplaceDocument`（27）；`Index.PruneDocuments`（58）；`Index.SearchDocuments`（90）。

### internal/searchindex/index.go

[internal/searchindex/index.go](../../internal/searchindex/index.go)

类型：`Index`（19）；`Session`（24）；`Message`（30）；`Result`（34）。

函数/方法：`Open`（45）；`Index.Close`（104）；`Index.CachedSession`（106）；`Index.Replace`（118）；`Index.ReplaceStream`（131）；`Index.UpdateSessionMetadata`（164）；`Index.Prune`（169）；`Index.Search`（201）。

### internal/searchindex/service.go

[internal/searchindex/service.go](../../internal/searchindex/service.go)

类型：`Service`（19）。

函数/方法：`NewService`（28）；`Service.SearchSessions`（37）；`Service.SearchDocuments`（57）；`Service.Close`（86）。

### internal/searchindex/sessions.go

[internal/searchindex/sessions.go](../../internal/searchindex/sessions.go)

函数/方法：`RefreshSessions`（12）。

### internal/usecases/agent_lifecycle.go

[internal/usecases/agent_lifecycle.go](../../internal/usecases/agent_lifecycle.go)

类型：`AgentLifecycle`（15）。

函数/方法：`NewAgentLifecycle`（23）；`AgentLifecycle.DeleteAgent`（29）；`AgentLifecycle.DeleteSession`（46）；`AgentLifecycle.ClearSessionRules`（55）。

### internal/usecases/mcp_configuration.go

[internal/usecases/mcp_configuration.go](../../internal/usecases/mcp_configuration.go)

类型：`MCPCredentialInput`（19）；`MCPConfigurationRequest`（23）；`MCPConfiguration`（36）；`MCPCredentials`（43）。

函数/方法：`NewMCPConfiguration`（48）；`MCPConfiguration.Create`（53）；`MCPConfiguration.Update`（113）；`MCPConfiguration.Delete`（163）；`MCPConfiguration.prepareStdioConfig`（175）；`MCPConfiguration.prepareHTTPConfig`（234）；`MCPConfiguration.cleanupCredentials`（324）；`newMCPCredentialID`（339）；`serverCredentialIDs`（343）；`obsoleteServerCredentialIDs`（365）；`requestStdioWithoutCredentials`（379）；`requestHTTPWithoutCredentials`（390）。

### internal/usecases/skill_maintenance.go

[internal/usecases/skill_maintenance.go](../../internal/usecases/skill_maintenance.go)

类型：`SkillUsers`（13）；`SkillRemover`（16）；`SkillMaintenance`（20）。

函数/方法：`NewSkillMaintenance`（25）；`SkillMaintenance.Remove`（30）。

### internal/usecases/vision.go

[internal/usecases/vision.go](../../internal/usecases/vision.go)

类型：`VisionInspector`（19）。

函数/方法：`NewVisionInspector`（24）；`VisionInspector.Inspect`（29）。

### internal/websearch/backends.go

[internal/websearch/backends.go](../../internal/websearch/backends.go)

类型：`Backend`（27）；`anySearchBackend`（33）；`anySearchEnvelope`（143）；`anySearchItem`（152）；`duckDuckGoBackend`（169）；`bingBackend`（251）。

函数/方法：`anySearchBackend.Name`（35）；`anySearchBackend.Search`（37）；`anySearchHTTPError`（113）；`searchLanguage`（161）；`duckDuckGoBackend.Name`（171）；`duckDuckGoBackend.Search`（175）；`parseDuckDuckGoHTML`（206）；`unwrapDDGURL`（235）；`bingBackend.Name`（253）；`bingBackend.Search`（257）；`parseBingHTML`（288）；`readSearchResponseBody`（352）；`attr`（363）；`hasClass`（372）；`nodeText`（381）。

### internal/websearch/service.go

[internal/websearch/service.go](../../internal/websearch/service.go)

类型：`Input`（29）；`Result`（37）；`Attempt`（49）；`Diagnostics`（57）；`Output`（64）；`searchResult`（72）；`Service`（75）；`Option`（84）。

函数/方法：`WithBackends`（88）；`New`（94）；`Service.Search`（133）；`Service.runAuto`（193）；`Service.backendFor`（241）；`normalizeSearchProvider`（250）；`searchAttemptStatus`（258）；`classifySearchError`（268）；`isLikelyLowQualityResults`（298）；`containsCJK`（331）；`cjkQualityTerms`（340）；`normalizeQualityText`（358）；`looksLikeDictionaryResult`（428）；`normalizeAndFilterSearchResults`（438）；`matchesAnyDomain`（472）；`validateSearchDomains`（488）。

### internal/workspace/browser.go

[internal/workspace/browser.go](../../internal/workspace/browser.go)

类型：`BrowseEntry`（40）；`DirectoryListing`（53）；`FilePreview`（67）。

函数/方法：`Manager.ListDirectory`（86）；`Manager.PreviewFile`（191）；`browseEntryType`（300）；`displayRelativePath`（316）；`detectPreviewMIME`（324）；`isSafePreviewImageMIME`（341）；`looksLikeText`（350）。

### internal/workspace/errors.go

[internal/workspace/errors.go](../../internal/workspace/errors.go)

### internal/workspace/manager.go

[internal/workspace/manager.go](../../internal/workspace/manager.go)

类型：`Manager`（52）。

函数/方法：`NewManager`（66）；`Manager.ManagedRoot`（179）；`Manager.ManagedPath`（186）；`Manager.Validate`（218）；`Manager.Resolve`（287）；`Manager.OpenRoot`（390）；`Manager.DeleteManaged`（562）；`Manager.Close`（601）；`Manager.ensureOpen`（614）；`Manager.ensureManagedWorkspace`（629）；`resolveCustomPath`（734）；`normalizeMode`（854）；`normalizeAgentID`（886）。

### internal/workspace/path.go

[internal/workspace/path.go](../../internal/workspace/path.go)

函数/方法：`NormalizeRelativePath`（50）。

### internal/workspace/types.go

[internal/workspace/types.go](../../internal/workspace/types.go)

类型：`Mode`（4）；`Workspace`（41）。

### internal/workspaceview/service.go

[internal/workspaceview/service.go](../../internal/workspaceview/service.go)

类型：`Service`（34）；`Overview`（40）。

函数/方法：`NewService`（56）；`Service.Overview`（76）；`Service.ListDirectory`（133）；`Service.PreviewFile`（142）；`Service.resolve`（155）；`shouldSkipOverviewDirectory`（174）。

</details>

## 11 章节文件

实现说明：[11-frontend.md](11-frontend.md)。

| 文件 | 行数 | 内容摘要 | 主要声明（节选） |
| --- | ---: | --- | --- |
| [examples/1-tools/main.go](../../examples/1-tools/main.go) | 91 | `fc539ebc1d34` | `main` |
| [examples/2-AgenticMessage/main.go](../../examples/2-AgenticMessage/main.go) | 224 | `f4d86f727d10` | `WeatherInput`、`WeatherOutput`、`newWeatherTool`、`printAgenticMessage`、`main` |
| [examples/3-subAgent/main.go](../../examples/3-subAgent/main.go) | 27 | `d9609a487eeb` | `main` |
| [examples/4-rod/main.go](../../examples/4-rod/main.go) | 45 | `956342a76427` | `main` |
| [examples/modules/textstats.go](../../examples/modules/textstats.go) | 62 | `a15bc078dbdd` | `textStatsProvider`、`textStatsInput`、`textStatsOutput`、`InstallTextStats`、`textStatsProvider.ID`、`textStatsProvider.Selection`、`textStatsProvider.Describe`、`textStatsProvider.Resolve` |
| [frontend/assets.go](../../frontend/assets.go) | 8 | `7578ae3bc2f9` | 组件/配置/样式 |
| [frontend/index.html](../../frontend/index.html) | 27 | `80f254965954` | 组件/配置/样式 |
| [frontend/package.json](../../frontend/package.json) | 25 | `a457c856de51` | 组件/配置/样式 |
| [frontend/scripts/ensure-embed.mjs](../../frontend/scripts/ensure-embed.mjs) | 4 | `933b7b16e29b` | 组件/配置/样式 |
| [frontend/scripts/localize-vue.mjs](../../frontend/scripts/localize-vue.mjs) | 70 | `caf9357647db` | `expression`、`localizeVueTemplates` |
| [frontend/src/App.vue](../../frontend/src/App.vue) | 23 | `dee01284a0b2` | 组件/配置/样式 |
| [frontend/src/api/agents.js](../../frontend/src/api/agents.js) | 130 | `3afcd828f35e` | `listAgents`、`getAgent`、`createAgent`、`updateAgent`、`deleteAgent`、`selectWorkspaceDirectory`、`listBuiltinTools`、`getSandboxStatus` |
| [frontend/src/api/app.js](../../frontend/src/api/app.js) | 46 | `9570e675af44` | `getAppStatus`、`exportBackup`、`scheduleRestore`、`getPendingRestoreStatus`、`cancelPendingRestore`、`getPendingBackupStatus`、`cancelPendingBackup` |
| [frontend/src/api/chat.js](../../frontend/src/api/chat.js) | 93 | `e87105df7c67` | `startTurn`、`cancelTurn`、`getContextStatus`、`getContextOverview`、`compactContext`、`resolveApproval` |
| [frontend/src/api/mcp.js](../../frontend/src/api/mcp.js) | 60 | `e22e4a31cc72` | `listMCPServers`、`createMCPServer`、`updateMCPServer`、`deleteMCPServer`、`testMCPConnection`、`discoverMCPTools`、`getAgentMCPTools`、`setAgentMCPTools` |
| [frontend/src/api/models.js](../../frontend/src/api/models.js) | 50 | `40b23f5dd4f1` | `getModelState`、`createProvider`、`updateProvider`、`deleteProvider`、`createModel`、`updateModel`、`deleteModel`、`testModel` |
| [frontend/src/api/permissions.js](../../frontend/src/api/permissions.js) | 79 | `b7e8a6eb5bfb` | `getPermissionMode`、`setPermissionMode`、`getPermissionState`、`updatePermissionSettings`、`deletePermissionRule`、`deleteSessionPermissionRule`、`clearSessionPermissionRules`、`clearPersistentPermissionAllows` |
| [frontend/src/api/preferences.js](../../frontend/src/api/preferences.js) | 36 | `e818ab2d9830` | `getUserProfile`、`updateUserProfile`、`saveLanguage`、`listPersonalMemories`、`addPersonalMemory`、`addPersonalMemoryFromMessage`、`updatePersonalMemory`、`deletePersonalMemory` |
| [frontend/src/api/proactive.js](../../frontend/src/api/proactive.js) | 28 | `61778fdbcaa6` | `getProactiveSettings`、`updateProactiveSettings`、`getProactiveStatus`、`listProactiveRecords`、`listRecentNotifications`、`runProactiveHeartbeat` |
| [frontend/src/api/sessions.js](../../frontend/src/api/sessions.js) | 70 | `2f7334c90d1e` | `listSessions`、`createSession`、`renameSession`、`setSessionArchived`、`searchSessionMessages`、`deleteSession`、`listMessages`、`listMessagePage` |
| [frontend/src/api/skills.js](../../frontend/src/api/skills.js) | 223 | `3e55ecba79e1` | `getSkillState`、`getSkillDetail`、`readSkillFile`、`selectSkillDirectory`、`installSkill`、`installSkillFromURL`、`discoverSkillSource`、`discoverLocalSkillSource` |
| [frontend/src/api/tasks.js](../../frontend/src/api/tasks.js) | 50 | `17da604859ed` | `listTasks`、`listTaskRuns`、`createTask`、`updateTask`、`setTaskStatus`、`archiveTask`、`deleteTask`、`deleteTaskRun` |
| [frontend/src/api/workspace.js](../../frontend/src/api/workspace.js) | 25 | `d66fd70f616d` | `getWorkspaceOverview`、`listWorkspaceDirectory`、`previewWorkspaceFile`、`searchWorkspaceDocuments` |
| [frontend/src/arco.js](../../frontend/src/arco.js) | 66 | `ff793209e683` | `installArco` |
| [frontend/src/assets/main.css](../../frontend/src/assets/main.css) | 1051 | `3ab0443d6fe7` | 组件/配置/样式 |
| [frontend/src/components/agents/AgentSecurityEditor.vue](../../frontend/src/components/agents/AgentSecurityEditor.vue) | 475 | `9233c462cb36` | `patchSandbox`、`followGlobalSettings`、`setAgentProtection`、`setAgentNetwork`、`addWritablePath`、`removeWritablePath` |
| [frontend/src/components/chat/ActivityTimeline.vue](../../frontend/src/components/chat/ActivityTimeline.vue) | 201 | `68eaad32c3da` | `toggleDetail`、`statusText`、`fileLabel`、`emptyResultText` |
| [frontend/src/components/chat/ApprovalCard.vue](../../frontend/src/components/chat/ApprovalCard.vue) | 409 | `2086a46fd6ca` | `emitDecision`、`decide` |
| [frontend/src/components/chat/ApprovalModeSelect.vue](../../frontend/src/components/chat/ApprovalModeSelect.vue) | 106 | `670b2fc3ff2a` | `changeMode` |
| [frontend/src/components/chat/AssistantTurn.vue](../../frontend/src/components/chat/AssistantTurn.vue) | 227 | `0fc0730c997f` | `taskTime` |
| [frontend/src/components/chat/BrowserScreenshotCard.vue](../../frontend/src/components/chat/BrowserScreenshotCard.vue) | 91 | `6b38b1614c0c` | `loadImage` |
| [frontend/src/components/chat/ChatEmptyState.vue](../../frontend/src/components/chat/ChatEmptyState.vue) | 57 | `1f2b2a47989f` | `createSession` |
| [frontend/src/components/chat/ChatView.vue](../../frontend/src/components/chat/ChatView.vue) | 82 | `b83bca74a3fc` | 组件/配置/样式 |
| [frontend/src/components/chat/ComposerBar.vue](../../frontend/src/components/chat/ComposerBar.vue) | 1767 | `881a6d396d39` | `textareaElement`、`syncSkillCommand`、`chooseSkill`、`handleComposerKey`、`formatTokens`、`formatRuntimeModel`、`formatRuntimeModelRole`、`formatModelCapabilities` |
| [frontend/src/components/chat/LiveAssistantTurn.vue](../../frontend/src/components/chat/LiveAssistantTurn.vue) | 252 | `ed223cdea4e7` | 组件/配置/样式 |
| [frontend/src/components/chat/MarkdownInline.vue](../../frontend/src/components/chat/MarkdownInline.vue) | 35 | `e6e3d37bfc91` | `openLink`、`openImage` |
| [frontend/src/components/chat/MarkdownRenderer.vue](../../frontend/src/components/chat/MarkdownRenderer.vue) | 69 | `74360c1ccb82` | `copyCode` |
| [frontend/src/components/chat/MessageActions.vue](../../frontend/src/components/chat/MessageActions.vue) | 176 | `ff092e529a33` | `quoteContent`、`proposeMemory`、`saveMemory`、`copyContent`、`reuseContent` |
| [frontend/src/components/chat/MessageAttachments.vue](../../frontend/src/components/chat/MessageAttachments.vue) | 214 | `edd8485e6cd9` | `humanSize`、`base64ToBlob`、`loadImage`、`downloadAttachment`、`previewImage`、`loadVisibleImages` |
| [frontend/src/components/chat/MessageItem.vue](../../frontend/src/components/chat/MessageItem.vue) | 387 | `09fa3a1d1b0c` | 组件/配置/样式 |
| [frontend/src/components/chat/MessageList.vue](../../frontend/src/components/chat/MessageList.vue) | 842 | `bc129c1fd4e8` | `jumpToSearchResult`、`returnToLatest`、`resolveCurrentApproval`、`loadOlderMessages`、`openWorkspaceFile`、`modelNameForMessage`、`handleScroll`、`scrollToBottom` |
| [frontend/src/components/chat/SkillComposerInput.vue](../../frontend/src/components/chat/SkillComposerInput.vue) | 221 | `71a5432158d9` | `captureSelection`、`clearChips`、`paint`、`remember`、`publish`、`onInput`、`replaceSelection`、`undo` |
| [frontend/src/components/chat/SkillReference.vue](../../frontend/src/components/chat/SkillReference.vue) | 24 | `6ded7f88843b` | 组件/配置/样式 |
| [frontend/src/components/chat/UserMessageContent.vue](../../frontend/src/components/chat/UserMessageContent.vue) | 19 | `daff1fa014d7` | 组件/配置/样式 |
| [frontend/src/components/mcp/MCPToolDetailView.vue](../../frontend/src/components/mcp/MCPToolDetailView.vue) | 408 | `6f8e761dbfff` | `riskText`、`setRisk` |
| [frontend/src/components/mcp/MCPWorkspaceView.vue](../../frontend/src/components/mcp/MCPWorkspaceView.vue) | 1130 | `a6c2f0be20f4` | `normaliseSelections`、`loadServers`、`loadAgentSelection`、`selectedTools`、`isToolEnabled`、`discoveredTools`、`allDiscoveredToolsEnabled`、`unavailableSelectedTools` |
| [frontend/src/components/models/ModelCapabilityBadges.vue](../../frontend/src/components/models/ModelCapabilityBadges.vue) | 78 | `adff2e772f7b` | 组件/配置/样式 |
| [frontend/src/components/onboarding/FirstRunGuide.vue](../../frontend/src/components/onboarding/FirstRunGuide.vue) | 203 | `aa3ceef66aea` | `advance`、`explain`、`createProvider`、`createModel`、`diagnose`、`finish` |
| [frontend/src/components/settings/ArchivedSessionsSettings.vue](../../frontend/src/components/settings/ArchivedSessionsSettings.vue) | 152 | `a7a3c3c71bb9` | `refresh`、`restore`、`remove`、`formatDate`、`open` |
| [frontend/src/components/settings/DataSettings.vue](../../frontend/src/components/settings/DataSettings.vue) | 112 | `ce4d322f1a1f` | `refreshBackupStatus`、`cancelRestore`、`cancelBackup`、`saveBackup`、`restoreBackup` |
| [frontend/src/components/settings/LanguageSettings.vue](../../frontend/src/components/settings/LanguageSettings.vue) | 59 | `e08cc21f6f8a` | `choose` |
| [frontend/src/components/settings/MCPSettings.vue](../../frontend/src/components/settings/MCPSettings.vue) | 1090 | `8bfbb7e7c1ac` | `slugifyKey`、`reset`、`edit`、`addEnv`、`removeEnv`、`addHeader`、`removeHeader`、`requestPayload` |
| [frontend/src/components/settings/PermissionSettings.vue](../../frontend/src/components/settings/PermissionSettings.vue) | 813 | `f8a79ac0aa31` | `actionText`、`scopeText`、`formatCreatedAt`、`ruleTarget`、`shortFingerprint`、`applyState`、`load`、`saveSettings` |
| [frontend/src/components/settings/ProactiveSettings.vue](../../frontend/src/components/settings/ProactiveSettings.vue) | 256 | `39e1347557f3` | `defaultRules`、`copySettings`、`save`、`runHeartbeat`、`enableDesktopNotifications`、`formatTime`、`recordTitle`、`recordStatus` |
| [frontend/src/components/settings/SandboxSettings.vue](../../frontend/src/components/settings/SandboxSettings.vue) | 651 | `3cb6e52a05fe` | `applyStatus`、`load`、`save`、`useStandardProtection`、`diagnose`、`diagnosticStatusLabel` |
| [frontend/src/components/settings/SettingsView.vue](../../frontend/src/components/settings/SettingsView.vue) | 461 | `97c90fc727fa` | `validKey`、`selectItem` |
| [frontend/src/components/settings/SkillPackageSettings.vue](../../frontend/src/components/settings/SkillPackageSettings.vue) | 854 | `da11757056db` | `displayName`、`usedByAgents`、`skillStatus`、`discoveryCandidateStatus`、`normalizeState`、`normalizeDiscovery`、`resetDiscovery`、`applyDiscovery` |
| [frontend/src/components/settings/SkillSettings.vue](../../frontend/src/components/settings/SkillSettings.vue) | 1308 | `4174c1b7d41d` | `displayName`、`usedByAgents`、`enabledForSelectedAgent`、`operationKey`、`normalizeState`、`syncAgentStore`、`load`、`readCatalog` |
| [frontend/src/components/settings/UserProfileSettings.vue](../../frontend/src/components/settings/UserProfileSettings.vue) | 188 | `c89aad9e8f7b` | `refreshMemories`、`addMemory`、`saveMemory`、`forgetMemory`、`syncForm`、`fileToDataURL`、`chooseAvatar`、`save` |
| [frontend/src/components/settings/models/ModelCatalog.vue](../../frontend/src/components/settings/models/ModelCatalog.vue) | 660 | `2ac9d1cf4ddf` | `defaultCapabilityConfig`、`reset`、`edit`、`save`、`test`、`remove` |
| [frontend/src/components/settings/models/MultimediaSettings.vue](../../frontend/src/components/settings/models/MultimediaSettings.vue) | 185 | `da7db6cdc706` | `save` |
| [frontend/src/components/settings/models/ProviderSettings.vue](../../frontend/src/components/settings/models/ProviderSettings.vue) | 459 | `d15fd26ab3b5` | `reset`、`edit`、`save`、`remove` |
| [frontend/src/components/sidebar/AgentModal.vue](../../frontend/src/components/sidebar/AgentModal.vue) | 1104 | `188e40ad451b` | `roleModelOptions`、`modelForID`、`resetForm`、`chooseWorkspace`、`fileToDataURL`、`chooseAvatar`、`save`、`removeAgent` |
| [frontend/src/components/sidebar/ConversationSidebar.vue](../../frontend/src/components/sidebar/ConversationSidebar.vue) | 1483 | `ffae0b81c372` | `openSearchResult`、`archiveSession`、`readExpandedAgents`、`persistExpandedAgents`、`isAgentExpanded`、`expandAgent`、`collapseAgent`、`toggleAgent` |
| [frontend/src/components/sidebar/RenameSessionModal.vue](../../frontend/src/components/sidebar/RenameSessionModal.vue) | 160 | `3280d4038ecb` | `save` |
| [frontend/src/components/sidebar/SidebarResizer.vue](../../frontend/src/components/sidebar/SidebarResizer.vue) | 215 | `d97d7b28cd27` | `startDrag`、`handlePointerMove`、`stopDrag`、`resetWidth`、`handleKeydown` |
| [frontend/src/components/skills/SkillDetailView.vue](../../frontend/src/components/skills/SkillDetailView.vue) | 2201 | `35ce4a738475` | `formatBytes`、`formatUpdatedAt`、`shortIdentity`、`confirmMutation`、`toggleFolder`、`beginAliasEdit`、`saveAlias`、`removeSkill` |
| [frontend/src/components/skills/SkillSelector.vue](../../frontend/src/components/skills/SkillSelector.vue) | 176 | `723c473445cc` | 组件/配置/样式 |
| [frontend/src/components/skills/SkillWorkspaceView.vue](../../frontend/src/components/skills/SkillWorkspaceView.vue) | 250 | `bfa210940e08` | `normalizeSkills`、`openDetail`、`closeDetail`、`refreshSelectedSkill`、`handleDeleted`、`rememberAgent` |
| [frontend/src/components/tasks/TasksWorkspaceView.vue](../../frontend/src/components/tasks/TasksWorkspaceView.vue) | 922 | `e96e96308112` | `localTimeZone`、`emptyForm`、`toScheduleDateTime`、`applyTask`、`startCreating`、`cancelCreating`、`selectTask`、`toggleWeekday` |
| [frontend/src/components/ui/AppPageHeader.vue](../../frontend/src/components/ui/AppPageHeader.vue) | 35 | `9abbd32dd9f0` | 组件/配置/样式 |
| [frontend/src/components/ui/EmptyState.vue](../../frontend/src/components/ui/EmptyState.vue) | 29 | `27bed8d14d35` | 组件/配置/样式 |
| [frontend/src/components/ui/IdentityAvatar.vue](../../frontend/src/components/ui/IdentityAvatar.vue) | 51 | `5027ad934667` | 组件/配置/样式 |
| [frontend/src/components/ui/ImagePreviewDialog.vue](../../frontend/src/components/ui/ImagePreviewDialog.vue) | 56 | `cae4bcb059e6` | `zoom`、`handleWheel` |
| [frontend/src/components/ui/SectionCard.vue](../../frontend/src/components/ui/SectionCard.vue) | 66 | `e2b3953aa534` | 组件/配置/样式 |
| [frontend/src/components/ui/StatusPill.vue](../../frontend/src/components/ui/StatusPill.vue) | 33 | `65a5a3d0deb5` | 组件/配置/样式 |
| [frontend/src/components/window/WindowChrome.vue](../../frontend/src/components/window/WindowChrome.vue) | 25 | `47d8fdd1c2dc` | 组件/配置/样式 |
| [frontend/src/components/workspace/ContextPanel.vue](../../frontend/src/components/workspace/ContextPanel.vue) | 164 | `6e45328d6938` | `report`、`clampTreeWidth`、`beginTreeResize`、`moveTreeResize`、`endTreeResize`、`keyTreeResize`、`searchDocuments`、`openDocumentResult` |
| [frontend/src/components/workspace/WorkspaceFileTree.vue](../../frontend/src/components/workspace/WorkspaceFileTree.vue) | 338 | `0e353a26fb8c` | `handleEntryClick` |
| [frontend/src/components/workspace/WorkspacePreview.vue](../../frontend/src/components/workspace/WorkspacePreview.vue) | 390 | `16f0bfc0e3b0` | `updateViewportSize`、`onImageLoad`、`zoom`、`onImageWheel` |
| [frontend/src/features/registry.js](../../frontend/src/features/registry.js) | 16 | `5d81e7f1460d` | `createFeatureRegistry` |
| [frontend/src/features/settings.js](../../frontend/src/features/settings.js) | 128 | `e0f927ee1589` | 组件/配置/样式 |
| [frontend/src/features/workspaces.js](../../frontend/src/features/workspaces.js) | 23 | `7cb6f3e0de38` | 组件/配置/样式 |
| [frontend/src/i18n/index.js](../../frontend/src/i18n/index.js) | 46 | `24394fd7571e` | `cachedLanguage`、`setLanguage`、`t`、`formatDate` |
| [frontend/src/i18n/translations-core.js](../../frontend/src/i18n/translations-core.js) | 147 | `7453dd9c4db1` | 组件/配置/样式 |
| [frontend/src/i18n/translations-dynamic.js](../../frontend/src/i18n/translations-dynamic.js) | 93 | `96d1c046b379` | 组件/配置/样式 |
| [frontend/src/i18n/translations-models.js](../../frontend/src/i18n/translations-models.js) | 71 | `e9ee3c34c04e` | 组件/配置/样式 |
| [frontend/src/i18n/translations-notifications.js](../../frontend/src/i18n/translations-notifications.js) | 38 | `673589a733b0` | 组件/配置/样式 |
| [frontend/src/i18n/translations-remaining.js](../../frontend/src/i18n/translations-remaining.js) | 212 | `8c6a62bb5854` | 组件/配置/样式 |
| [frontend/src/i18n/translations-security.js](../../frontend/src/i18n/translations-security.js) | 120 | `065b86182102` | 组件/配置/样式 |
| [frontend/src/i18n/translations-settings.js](../../frontend/src/i18n/translations-settings.js) | 68 | `33e9f7b593ba` | 组件/配置/样式 |
| [frontend/src/i18n/translations.js](../../frontend/src/i18n/translations.js) | 207 | `d6c7c8eb1a00` | 组件/配置/样式 |
| [frontend/src/layouts/AppShell.vue](../../frontend/src/layouts/AppShell.vue) | 577 | `19f638e27d91` | `updateViewportWidth`、`beginContextResize`、`moveContextResize`、`endContextResize`、`keyContextResize`、`ensureRoomForPanel`、`setContextWidth`、`toggleRightPanel` |
| [frontend/src/main.js](../../frontend/src/main.js) | 53 | `1498ffce99ee` | 组件/配置/样式 |
| [frontend/src/runtime/projections.js](../../frontend/src/runtime/projections.js) | 174 | `e8a8973dca7c` | `normalizeStringList`、`normalizeRuntimeManifest`、`normalizeContextAssembly`、`normalizeActiveRunStatus`、`createLiveToolCall`、`normalizeContextUsage`、`stableTextHash`、`userInputSignature` |
| [frontend/src/stores/agents.js](../../frontend/src/stores/agents.js) | 120 | `90284529b24b` | `invalidateAgentList` |
| [frontend/src/stores/contextPanel.js](../../frontend/src/stores/contextPanel.js) | 39 | `69445f5a95ed` | `readPreference`、`savePreference` |
| [frontend/src/stores/layout.js](../../frontend/src/stores/layout.js) | 120 | `56c8cad6a40a` | `clampWidth`、`readStoredWidth`、`persistWidth` |
| [frontend/src/stores/mcp.js](../../frontend/src/stores/mcp.js) | 82 | `cd03fab43ff6` | 组件/配置/样式 |
| [frontend/src/stores/models.js](../../frontend/src/stores/models.js) | 203 | `1e7560a451bd` | 组件/配置/样式 |
| [frontend/src/stores/permissions.js](../../frontend/src/stores/permissions.js) | 36 | `7a53e45775cc` | 组件/配置/样式 |
| [frontend/src/stores/preferences.js](../../frontend/src/stores/preferences.js) | 53 | `d34548a852ff` | 组件/配置/样式 |
| [frontend/src/stores/proactive.js](../../frontend/src/stores/proactive.js) | 202 | `82c661141342` | `normalizeStatus`、`normalizeSettings` |
| [frontend/src/stores/runtime.js](../../frontend/src/stores/runtime.js) | 875 | `aa7845971e03` | `scheduleUIFrame`、`cancelUIFrame` |
| [frontend/src/stores/sessions.js](../../frontend/src/stores/sessions.js) | 1106 | `8dc42c20c4f8` | `loadDrafts`、`flushPersistedDrafts`、`persistDrafts`、`invalidateSessionLists` |
| [frontend/src/stores/skills.js](../../frontend/src/stores/skills.js) | 101 | `8431a6739c8a` | 组件/配置/样式 |
| [frontend/src/stores/tasks.js](../../frontend/src/stores/tasks.js) | 250 | `476f86fae060` | 组件/配置/样式 |
| [frontend/src/stores/workspace.js](../../frontend/src/stores/workspace.js) | 281 | `1a53be37d966` | `normalizeArray` |
| [frontend/src/utils/avatar.js](../../frontend/src/utils/avatar.js) | 3 | `91f87d9b048e` | 组件/配置/样式 |
| [frontend/src/utils/confirm.js](../../frontend/src/utils/confirm.js) | 92 | `72da46a93db4` | `confirmAction` |
| [frontend/src/utils/defaultBuiltinTools.js](../../frontend/src/utils/defaultBuiltinTools.js) | 12 | `aa4c6f4c85c5` | `defaultBuiltinTools` |
| [frontend/src/utils/latestRequest.js](../../frontend/src/utils/latestRequest.js) | 22 | `f76ded689801` | `beginLatestRequest`、`invalidateRequests` |
| [frontend/src/utils/markdown.js](../../frontend/src/utils/markdown.js) | 148 | `b1559076fb16` | `safeUrl`、`safeInlineImageUrl`、`parseInline`、`splitTableRow`、`isTableSeparator`、`parseMarkdown` |
| [frontend/src/utils/mcp.js](../../frontend/src/utils/mcp.js) | 59 | `eb7ef612fb1c` | `mcpTransportLabel`、`mcpRiskLabel`、`mcpConnectionStateLabel`、`mcpConnectionStateTone` |
| [frontend/src/utils/menuTooltip.js](../../frontend/src/utils/menuTooltip.js) | 30 | `18d5b1a65a88` | `useMenuTooltip` |
| [frontend/src/utils/searchPolling.js](../../frontend/src/utils/searchPolling.js) | 14 | `3a7fa05562dd` | `pollIndexedSearch` |
| [frontend/src/utils/skillCommand.js](../../frontend/src/utils/skillCommand.js) | 50 | `b3566c7fe45f` | `parseSkillCommand`、`matchingEnabledSkills`、`insertSkillReference`、`splitSkillReferences` |
| [frontend/src/utils/skillEditor.js](../../frontend/src/utils/skillEditor.js) | 57 | `ee6258c7ac99` | `readEditorText`、`readEditorSelection`、`setEditorSelection` |
| [frontend/src/utils/toolEffects.js](../../frontend/src/utils/toolEffects.js) | 261 | `23b37f4f178c` | `parseToolResultObject`、`browserNeedsHumanVerification`、`browserScreenshotOfCall`、`normalizeBrowsableWorkspacePath`、`mergeTurnFileChange`、`fileChangesOfCalls`、`scheduledTasksOfCalls` |
| [frontend/src/utils/toolProtocol.js](../../frontend/src/utils/toolProtocol.js) | 474 | `88654b1b329e` | `isObject`、`readStringProperty`、`metadataOf`、`reasoningOf`、`toolCallsOf`、`isToolCallMessage`、`isToolResultMessage`、`toolDisplayName` |
| [frontend/src/utils/toolTrace.js](../../frontend/src/utils/toolTrace.js) | 648 | `ec3b12622c89` | `createTraceCall`、`applyToolResult`、`createTraceGroup`、`appendAssistantToolCalls`、`appendToolResult`、`finishTrace`、`buildConversationBlocks`、`summarizeToolTrace` |
| [frontend/src/utils/uiMessage.js](../../frontend/src/utils/uiMessage.js) | 16 | `651d126abebf` | `show` |
| [frontend/src/utils/workspace.js](../../frontend/src/utils/workspace.js) | 33 | `17e7ae34c1a8` | `formatBytes`、`formatWorkspaceTime` |
| [frontend/src/vite-env.d.ts](../../frontend/src/vite-env.d.ts) | 1 | `65996936fbb0` | 组件/配置/样式 |
| [frontend/tsconfig.json](../../frontend/tsconfig.json) | 25 | `79bb35e20db4` | 组件/配置/样式 |
| [frontend/vite.config.js](../../frontend/vite.config.js) | 19 | `ddd5090ddaab` | 组件/配置/样式 |
| [internal/services/agentservice.go](../../internal/services/agentservice.go) | 834 | `b0e413f6ab4a` | `AgentDTO`、`SandboxPolicyDTO`、`AgentSecurityRequest`、`AgentProfileRequest`、`AgentModelRequest`、`AgentModelRolesDTO`、`AgentModelRolesRequest`、`AgentSkillsRequest` |
| [internal/services/appservice.go](../../internal/services/appservice.go) | 161 | `f0175b63fcfa` | `AppStatus`、`AppService`、`NewAppService`、`AppService.ServiceName`、`AppService.ServiceStartup`、`AppService.ServiceShutdown`、`AppService.Status`、`AppService.ExportBackup` |
| [internal/services/chatservice.go](../../internal/services/chatservice.go) | 232 | `d43ea004bd2a` | `StartTurnRequest`、`AttachmentRequest`、`ResolveApprovalRequest`、`ChatService`、`init`、`NewChatService`、`ChatService.ServiceName`、`ChatService.ServiceStartup` |
| [internal/services/dependencies.go](../../internal/services/dependencies.go) | 119 | `9ead457ce5a8` | `AgentDependencies`、`AppDependencies`、`ChatDependencies`、`MCPDependencies`、`ModelDependencies`、`PermissionDependencies`、`PreferenceDependencies`、`ProactiveDependencies` |
| [internal/services/enter.go](../../internal/services/enter.go) | 47 | `2a4d982007bd` | `All` |
| [internal/services/mcpservice.go](../../internal/services/mcpservice.go) | 483 | `37e851736d48` | `MCPHTTPHeaderDTO`、`MCPStdioEnvDTO`、`MCPServerDTO`、`MCPHTTPHeaderRequest`、`MCPStdioEnvRequest`、`MCPServerRequest`、`MCPToolSelectionDTO`、`MCPToolDTO` |
| [internal/services/modelservice.go](../../internal/services/modelservice.go) | 530 | `7e3b731c9b07` | `ProviderDTO`、`ModelCapabilityConfigDTO`、`ModelCapabilitiesDTO`、`ModelDTO`、`MultimediaConfigDTO`、`ModelSettingsState`、`CreateProviderRequest`、`UpdateProviderRequest` |
| [internal/services/permissionservice.go](../../internal/services/permissionservice.go) | 364 | `135bc0841bbc` | `PermissionRuleDTO`、`PermissionStateDTO`、`UpdatePermissionSettingsRequest`、`PermissionService`、`PermissionService.Mode`、`PermissionService.SetMode`、`NewPermissionService`、`PermissionService.ServiceName` |
| [internal/services/preferenceservice.go](../../internal/services/preferenceservice.go) | 100 | `7a4600c5e141` | `UserProfileDTO`、`PreferenceService`、`NewPreferenceService`、`PreferenceService.ServiceName`、`PreferenceService.GetUserProfile`、`PreferenceService.UpdateUserProfile`、`PreferenceService.SetLanguage`、`userProfileDTO` |
| [internal/services/proactiveservice.go](../../internal/services/proactiveservice.go) | 223 | `609208b06ae3` | `ProactiveQuietHoursDTO`、`ProactiveRuleDTO`、`ProactiveSettingsDTO`、`ProactiveStatusDTO`、`ProactiveService`、`init`、`NewProactiveService`、`ProactiveService.ServiceName` |
| [internal/services/sessionservice.go](../../internal/services/sessionservice.go) | 415 | `02790546eed6` | `SessionDTO`、`MessageDTO`、`MessageMetadataDTO`、`MessagePageDTO`、`AttachmentDTO`、`AttachmentContentDTO`、`SessionService`、`SessionSearchDTO` |
| [internal/services/skillservice.go](../../internal/services/skillservice.go) | 896 | `c0564a15c192` | `SkillAgentDTO`、`SkillFileDTO`、`SkillSourceDTO`、`SkillUpdateCheckDTO`、`SkillUpdateResultDTO`、`SkillDiagnosticDTO`、`SkillScriptRuntimeDTO`、`SkillDiscoveryCandidateDTO` |
| [internal/services/taskservice.go](../../internal/services/taskservice.go) | 389 | `7988c2476166` | `TaskScheduleDTO`、`TaskLimitsDTO`、`TaskDTO`、`TaskApprovalDTO`、`TaskRunDTO`、`SaveTaskRequest`、`TaskStatusRequest`、`TaskService` |
| [internal/services/workspaceservice.go](../../internal/services/workspaceservice.go) | 182 | `d4fc02987bf0` | `WorkspaceOverviewDTO`、`WorkspaceEntryDTO`、`WorkspaceDirectoryDTO`、`WorkspacePreviewDTO`、`WorkspaceService`、`DocumentSearchDTO`、`NewWorkspaceService`、`WorkspaceService.ServiceName` |

<details>
<summary>本章 Go 顶层函数与类型定位（自动扫描）</summary>

### examples/1-tools/main.go

[examples/1-tools/main.go](../../examples/1-tools/main.go)

函数/方法：`main`（16）。

### examples/2-AgenticMessage/main.go

[examples/2-AgenticMessage/main.go](../../examples/2-AgenticMessage/main.go)

类型：`WeatherInput`（20）；`WeatherOutput`（24）。

函数/方法：`newWeatherTool`（34）；`printAgenticMessage`（65）；`main`（156）。

### examples/3-subAgent/main.go

[examples/3-subAgent/main.go](../../examples/3-subAgent/main.go)

函数/方法：`main`（25）。

### examples/4-rod/main.go

[examples/4-rod/main.go](../../examples/4-rod/main.go)

函数/方法：`main`（11）。

### examples/modules/textstats.go

[examples/modules/textstats.go](../../examples/modules/textstats.go)

类型：`textStatsProvider`（25）；`textStatsInput`（26）；`textStatsOutput`（29）。

函数/方法：`InstallTextStats`（18）；`textStatsProvider.ID`（33）；`textStatsProvider.Selection`（34）；`textStatsProvider.Describe`（43）；`textStatsProvider.Resolve`（48）；`textStatsProvider.contribution`（51）。

### frontend/assets.go

[frontend/assets.go](../../frontend/assets.go)

### internal/services/agentservice.go

[internal/services/agentservice.go](../../internal/services/agentservice.go)

类型：`AgentDTO`（36）；`SandboxPolicyDTO`（59）；`AgentSecurityRequest`（67）；`AgentProfileRequest`（73）；`AgentModelRequest`（80）；`AgentModelRolesDTO`（85）；`AgentModelRolesRequest`（90）；`AgentSkillsRequest`（95）；`BuiltinToolDTO`（100）；`SandboxStatusDTO`（108）；`SandboxSettingsRequest`（125）；`SandboxDiagnosticCheckDTO`（134）；`SandboxDiagnosticsDTO`（142）；`CreateAgentRequest`（148）；`UpdateAgentRequest`（164）；`AgentService`（184）。

函数/方法：`NewAgentService`（189）；`AgentService.ServiceName`（192）；`AgentService.ListAgents`（197）；`AgentService.GetAgent`（225）；`AgentService.CreateAgent`（240）；`AgentService.UpdateAgent`（288）；`AgentService.UpdateAgentProfile`（349）；`AgentService.SetAgentModel`（360）；`AgentService.SetAgentModelRoles`（371）；`AgentService.SetAgentSkills`（382）；`AgentService.ListBuiltinTools`（393）；`AgentService.GetSandboxStatus`（415）；`AgentService.UpdateSandboxSettings`（439）；`AgentService.RunSandboxDiagnostics`（483）；`errorDetail`（624）；`AgentService.UpdateAgentSecurity`（632）；`AgentService.SelectSandboxDirectory`（653）；`AgentService.DeleteAgent`（661）；`AgentService.SelectWorkspaceDirectory`（677）；`selectDirectory`（681）；`AgentService.toDTO`（700）；`modelRolesFromDTO`（752）；`modelRolesDTO`（756）；`sandboxPolicyFromDTO`（760）；`sandboxPolicyDTO`（767）；`AgentService.validateBuiltinToolNames`（776）；`builtinToolCategory`（800）；`builtinToolLabel`（821）。

### internal/services/appservice.go

[internal/services/appservice.go](../../internal/services/appservice.go)

类型：`AppStatus`（20）；`AppService`（34）。

函数/方法：`NewAppService`（39）；`AppService.ServiceName`（42）；`AppService.ServiceStartup`（50）；`AppService.ServiceShutdown`（68）；`AppService.Status`（74）；`AppService.ExportBackup`（98）；`AppService.PendingBackupStatus`（119）；`AppService.CancelPendingBackup`（123）；`AppService.PendingRestoreStatus`（127）；`AppService.CancelPendingRestore`（131）；`AppService.ScheduleRestore`（136）。

### internal/services/chatservice.go

[internal/services/chatservice.go](../../internal/services/chatservice.go)

类型：`StartTurnRequest`（28）；`AttachmentRequest`（36）；`ResolveApprovalRequest`（46）；`ChatService`（74）。

函数/方法：`init`（23）；`NewChatService`（81）；`ChatService.ServiceName`（84）；`ChatService.ServiceStartup`（89）；`ChatService.ServiceShutdown`（111）；`ChatService.StartTurn`（126）；`ChatService.ContextStatus`（149）；`ChatService.ContextOverview`（164）；`ChatService.CompactContext`（178）；`ChatService.ResolveApproval`（194）；`attachmentInputs`（208）；`ChatService.contextOperationTimeout`（216）；`ChatService.CancelTurn`（227）。

### internal/services/dependencies.go

[internal/services/dependencies.go](../../internal/services/dependencies.go)

类型：`AgentDependencies`（31）；`AppDependencies`（42）；`ChatDependencies`（49）；`MCPDependencies`（57）；`ModelDependencies`（64）；`PermissionDependencies`（69）；`PreferenceDependencies`（79）；`ProactiveDependencies`（85）；`SessionDependencies`（93）；`SkillDependencies`（100）；`TaskDependencies`（108）；`WorkspaceDependencies`（116）。

### internal/services/enter.go

[internal/services/enter.go](../../internal/services/enter.go)

函数/方法：`All`（11）。

### internal/services/mcpservice.go

[internal/services/mcpservice.go](../../internal/services/mcpservice.go)

类型：`MCPHTTPHeaderDTO`（20）；`MCPStdioEnvDTO`（27）；`MCPServerDTO`（34）；`MCPHTTPHeaderRequest`（61）；`MCPStdioEnvRequest`（68）；`MCPServerRequest`（75）；`MCPToolSelectionDTO`（90）；`MCPToolDTO`（96）；`MCPPortableServerDTO`（109）；`MCPPortableBundleDTO`（121）；`MCPConnectionTestDTO`（127）；`MCPService`（134）。

函数/方法：`NewMCPService`（138）；`MCPService.ServiceName`（140）；`MCPService.ListServers`（144）；`MCPService.CreateServer`（162）；`MCPService.UpdateServer`（172）；`MCPService.ExportServersConfig`（183）；`MCPService.ImportServersConfig`（234）；`MCPService.RefreshTools`（298）；`MCPService.SetServerEnabled`（313）；`MCPService.DisconnectServer`（328）；`MCPService.DeleteServer`（337）；`MCPService.TestConnection`（344）；`MCPService.DiscoverTools`（356）；`MCPService.SetToolRisk`（380）；`MCPService.GetAgentTools`（392）；`MCPService.SetAgentTools`（407）；`toMCPServerDTO`（420）；`toMCPConfigurationRequest`（470）。

### internal/services/modelservice.go

[internal/services/modelservice.go](../../internal/services/modelservice.go)

类型：`ProviderDTO`（18）；`ModelCapabilityConfigDTO`（30）；`ModelCapabilitiesDTO`（40）；`ModelDTO`（50）；`MultimediaConfigDTO`（68）；`ModelSettingsState`（73）；`CreateProviderRequest`（81）；`UpdateProviderRequest`（89）；`SaveModelRequest`（98）；`TestModelResponse`（110）；`ModelDiagnostic`（118）；`ModelService`（131）。

函数/方法：`NewModelService`（136）；`ModelService.State`（139）；`ModelService.UpdateMultimediaConfig`（170）；`ModelService.CreateProvider`（182）；`ModelService.UpdateProvider`（208）；`ModelService.DeleteProvider`（237）；`ModelService.CreateModel`（250）；`ModelService.UpdateModel`（284）；`ModelService.DeleteModel`（320）；`ModelService.TestModel`（333）；`ModelService.DiagnoseModel`（348）；`classifyModelDiagnostic`（391）；`ModelService.modelDTOByID`（415）；`toProviderDTOs`（430）；`toProviderDTO`（440）；`toModelDTOs`（458）；`toModelDTO`（468）；`capabilityConfigFromDTO`（500）；`capabilityConfigDTO`（508）；`capabilitiesDTO`（521）；`multimediaConfigDTO`（528）。

### internal/services/permissionservice.go

[internal/services/permissionservice.go](../../internal/services/permissionservice.go)

类型：`PermissionRuleDTO`（48）；`PermissionStateDTO`（78）；`UpdatePermissionSettingsRequest`（93）；`PermissionService`（106）。

函数/方法：`PermissionService.Mode`（17）；`PermissionService.SetMode`（25）；`NewPermissionService`（111）；`PermissionService.ServiceName`（116）；`PermissionService.State`（124）；`PermissionService.UpdateSettings`（173）；`PermissionService.DeleteRule`（221）；`PermissionService.DeleteSessionRule`（239）；`PermissionService.ClearSessionRules`（257）；`PermissionService.ClearPersistentAllows`（279）；`PermissionService.validate`（292）；`PermissionService.agentNames`（299）；`PermissionService.mcpServerNames`（311）；`projectPermissionRules`（323）。

### internal/services/preferenceservice.go

[internal/services/preferenceservice.go](../../internal/services/preferenceservice.go)

类型：`UserProfileDTO`（11）；`PreferenceService`（17）。

函数/方法：`NewPreferenceService`（21）；`PreferenceService.ServiceName`（25）；`PreferenceService.GetUserProfile`（27）；`PreferenceService.UpdateUserProfile`（37）；`PreferenceService.SetLanguage`（47）；`userProfileDTO`（57）；`PreferenceService.ListPersonalMemories`（62）；`PreferenceService.AddPersonalMemory`（68）；`PreferenceService.AddPersonalMemoryFromMessage`（74）；`PreferenceService.UpdatePersonalMemory`（90）；`PreferenceService.DeletePersonalMemory`（96）。

### internal/services/proactiveservice.go

[internal/services/proactiveservice.go](../../internal/services/proactiveservice.go)

类型：`ProactiveQuietHoursDTO`（26）；`ProactiveRuleDTO`（33）；`ProactiveSettingsDTO`（41）；`ProactiveStatusDTO`（48）；`ProactiveService`（55）。

函数/方法：`init`（21）；`NewProactiveService`（61）；`ProactiveService.ServiceName`（65）；`ProactiveService.ServiceStartup`（67）；`ProactiveService.ServiceShutdown`（101）；`ProactiveService.GetSettings`（114）；`ProactiveService.UpdateSettings`（121）；`ProactiveService.Status`（140）；`ProactiveService.Notifications`（147）；`ProactiveService.Records`（157）；`ProactiveService.RunHeartbeat`（167）；`proactiveSettingsDTO`（176）；`proactiveSettingsFromDTO`（195）；`proactiveStatusDTO`（214）。

### internal/services/sessionservice.go

[internal/services/sessionservice.go](../../internal/services/sessionservice.go)

类型：`SessionDTO`（18）；`MessageDTO`（28）；`MessageMetadataDTO`（40）；`MessagePageDTO`（55）；`AttachmentDTO`（62）；`AttachmentContentDTO`（71）；`SessionService`（77）；`SessionSearchDTO`（81）。

函数/方法：`NewSessionService`（88）；`SessionService.Search`（92）；`SessionService.List`（103）；`SessionService.Create`（120）；`SessionService.Rename`（132）；`SessionService.SetArchived`（143）；`SessionService.Delete`（154）；`SessionService.Messages`（167）；`SessionService.MessagePage`（188）；`SessionService.MessageWindow`（210）；`SessionService.ReadAttachment`（249）；`sessionDTO`（259）；`messageDTO`（270）；`runtimeMessageText`（313）；`runtimeMessageAttachments`（338）；`stringExtra`（377）；`int64Extra`（384）；`runtimeMessageReasoning`（400）。

### internal/services/skillservice.go

[internal/services/skillservice.go](../../internal/services/skillservice.go)

类型：`SkillAgentDTO`（28）；`SkillFileDTO`（41）；`SkillSourceDTO`（51）；`SkillUpdateCheckDTO`（67）；`SkillUpdateResultDTO`（77）；`SkillDiagnosticDTO`（86）；`SkillScriptRuntimeDTO`（93）；`SkillDiscoveryCandidateDTO`（103）；`SkillDiscoveryDTO`（124）；`SkillDetailDTO`（135）；`SkillFileContentDTO`（160）；`SkillDTO`（170）；`SkillStateDTO`（205）；`SkillService`（223）。

函数/方法：`NewSkillService`（228）；`SkillService.ServiceName`（231）；`SkillService.State`（236）；`SkillService.SkillDetail`（289）；`SkillService.ReadSkillFile`（342）；`SkillService.SelectSkillDirectory`（360）；`SkillService.InstallSkill`（387）；`SkillService.InstallSkillFromURL`（407）；`SkillService.DiscoverSkillSource`（430）；`SkillService.DiscoverLocalSkillSource`（444）；`SkillService.InstallDiscoveredSkillsFromURL`（458）；`SkillService.InstallDiscoveredSkillsFromDirectory`（476）；`SkillService.CheckSkillUpdate`（494）；`SkillService.UpdateSkill`（516）；`SkillService.ReinstallSkill`（531）；`SkillService.ReinstallSkillFromURL`（546）；`SkillService.ReinstallSkillFromDirectory`（561）；`SkillService.SetSkillAlias`（578）；`SkillService.EnableSkillForAgent`（595）；`SkillService.DisableSkillForAgent`（611）；`SkillService.DeleteSkill`（628）；`projectSkillAgents`（637）；`buildSkillUsageMap`（659）；`projectSkillFiles`（695）；`SkillService.skillMutationTimeout`（706）；`projectSkillSource`（719）；`sanitizeSkillSourceURL`（755）；`formatSkillTime`（770）；`projectSkillUpdateResult`（777）；`SkillService.validate`（787）；`projectSkillDTO`（794）；`cloneSkillStringMap`（826）；`projectSkillDiagnostics`（837）；`projectSkillScriptRuntimes`（848）；`projectSkillDiscovery`（866）。

### internal/services/taskservice.go

[internal/services/taskservice.go](../../internal/services/taskservice.go)

类型：`TaskScheduleDTO`（22）；`TaskLimitsDTO`（33）；`TaskDTO`（42）；`TaskApprovalDTO`（63）；`TaskRunDTO`（72）；`SaveTaskRequest`（101）；`TaskStatusRequest`（112）；`TaskService`（116）。

函数/方法：`init`（18）；`NewTaskService`（122）；`TaskService.ServiceName`（123）；`TaskService.ServiceStartup`（125）；`TaskService.ServiceShutdown`（146）；`TaskService.List`（157）；`TaskService.Runs`（171）；`TaskService.Create`（198）；`TaskService.Update`（212）；`TaskService.SetStatus`（227）；`TaskService.Archive`（237）；`TaskService.Delete`（246）；`TaskService.DeleteRun`（257）；`TaskService.ClearRuns`（268）；`TaskService.clearSessionRules`（281）；`TaskService.RunNow`（292）；`TaskService.CancelRun`（302）；`scheduleFromDTO`（312）；`limitsFromDTO`（338）；`taskDTO`（342）；`scheduleDTO`（360）；`taskRunDTO`（364）；`taskApprovalDTO`（372）；`formatOptionalTime`（384）。

### internal/services/workspaceservice.go

[internal/services/workspaceservice.go](../../internal/services/workspaceservice.go)

类型：`WorkspaceOverviewDTO`（19）；`WorkspaceEntryDTO`（32）；`WorkspaceDirectoryDTO`（42）；`WorkspacePreviewDTO`（51）；`WorkspaceService`（70）；`DocumentSearchDTO`（74）。

函数/方法：`NewWorkspaceService`（80）；`WorkspaceService.ServiceName`（84）；`WorkspaceService.Overview`（87）；`WorkspaceService.ListDirectory`（98）；`WorkspaceService.PreviewFile`（109）；`WorkspaceService.SearchDocuments`（120）；`workspaceOverviewDTO`（130）；`workspaceDirectoryDTO`（144）；`workspacePreviewDTO`（163）；`formatRequiredTime`（177）。

</details>

## 测试文件地图

测试证明特定场景和断言，不自动证明平台矩阵、真实供应商或任意输入正确。关键测试解释见维护章。

### 01

- [cmd/data/main_test.go](../../cmd/data/main_test.go)
- [internal/app/lifecycle_test.go](../../internal/app/lifecycle_test.go)
- [internal/commandenv/command_test.go](../../internal/commandenv/command_test.go)
- [internal/config/config_test.go](../../internal/config/config_test.go)
- [internal/instancelock/lock_test.go](../../internal/instancelock/lock_test.go)

### 02

- [internal/runtime/approval_budget_test.go](../../internal/runtime/approval_budget_test.go)
- [internal/runtime/approval_timeout_test.go](../../internal/runtime/approval_timeout_test.go)
- [internal/runtime/extensions_test.go](../../internal/runtime/extensions_test.go)
- [internal/runtime/iterations_test.go](../../internal/runtime/iterations_test.go)
- [internal/runtime/language_test.go](../../internal/runtime/language_test.go)
- [internal/runtime/limits_test.go](../../internal/runtime/limits_test.go)
- [internal/runtime/model_accounting_test.go](../../internal/runtime/model_accounting_test.go)
- [internal/runtime/model_roles_test.go](../../internal/runtime/model_roles_test.go)
- [internal/runtime/prompt_test.go](../../internal/runtime/prompt_test.go)
- [internal/runtime/reasoning_policy_test.go](../../internal/runtime/reasoning_policy_test.go)
- [internal/runtime/service_test.go](../../internal/runtime/service_test.go)
- [internal/runtime/sources_test.go](../../internal/runtime/sources_test.go)
- [internal/runtime/terminal_test.go](../../internal/runtime/terminal_test.go)
- [internal/runtime/user_error_test.go](../../internal/runtime/user_error_test.go)

### 03

- [internal/avatar/avatar_test.go](../../internal/avatar/avatar_test.go)
- [internal/contextartifact/store_test.go](../../internal/contextartifact/store_test.go)
- [internal/contextengine/budget_test.go](../../internal/contextengine/budget_test.go)
- [internal/contextengine/estimator_test.go](../../internal/contextengine/estimator_test.go)
- [internal/contextengine/middleware_test.go](../../internal/contextengine/middleware_test.go)
- [internal/contextengine/projection_test.go](../../internal/contextengine/projection_test.go)
- [internal/contextengine/recovery_test.go](../../internal/contextengine/recovery_test.go)
- [internal/contextengine/serializer_test.go](../../internal/contextengine/serializer_test.go)
- [internal/contextengine/summarization_test.go](../../internal/contextengine/summarization_test.go)
- [internal/documenttext/extract_test.go](../../internal/documenttext/extract_test.go)
- [internal/multimodal/vision_bridge_test.go](../../internal/multimodal/vision_bridge_test.go)
- [internal/preferences/memory_test.go](../../internal/preferences/memory_test.go)
- [internal/preferences/store_test.go](../../internal/preferences/store_test.go)

### 04

- [internal/agents/deletion_test.go](../../internal/agents/deletion_test.go)
- [internal/agents/service_contract_test.go](../../internal/agents/service_contract_test.go)
- [internal/sessions/attachments_security_test.go](../../internal/sessions/attachments_security_test.go)
- [internal/sessions/attachments_test.go](../../internal/sessions/attachments_test.go)
- [internal/sessions/store_test.go](../../internal/sessions/store_test.go)
- [internal/transcript/location_advance_test.go](../../internal/transcript/location_advance_test.go)
- [internal/transcript/location_index_test.go](../../internal/transcript/location_index_test.go)
- [internal/transcript/store_test.go](../../internal/transcript/store_test.go)
- [internal/transcript/tool_result_test.go](../../internal/transcript/tool_result_test.go)

### 05

- [internal/tools/builtin/browser_proxy_test.go](../../internal/tools/builtin/browser_proxy_test.go)
- [internal/tools/builtin/browser_tool_test.go](../../internal/tools/builtin/browser_tool_test.go)
- [internal/tools/builtin/context_history_index_test.go](../../internal/tools/builtin/context_history_index_test.go)
- [internal/tools/builtin/extract_document_test.go](../../internal/tools/builtin/extract_document_test.go)
- [internal/tools/builtin/file_ops_test.go](../../internal/tools/builtin/file_ops_test.go)
- [internal/tools/builtin/file_transactions_test.go](../../internal/tools/builtin/file_transactions_test.go)
- [internal/tools/builtin/filesystem_approval_test.go](../../internal/tools/builtin/filesystem_approval_test.go)
- [internal/tools/builtin/git_readonly_test.go](../../internal/tools/builtin/git_readonly_test.go)
- [internal/tools/builtin/prompt_budget_test.go](../../internal/tools/builtin/prompt_budget_test.go)
- [internal/tools/builtin/run_command_policy_test.go](../../internal/tools/builtin/run_command_policy_test.go)
- [internal/tools/builtin/schedule_task_test.go](../../internal/tools/builtin/schedule_task_test.go)
- [internal/tools/builtin/search_files_test.go](../../internal/tools/builtin/search_files_test.go)
- [internal/tools/builtin/security_regression_test.go](../../internal/tools/builtin/security_regression_test.go)
- [internal/tools/builtin/system_tools_test.go](../../internal/tools/builtin/system_tools_test.go)
- [internal/tools/builtin/websearch_tool_test.go](../../internal/tools/builtin/websearch_tool_test.go)
- [internal/tools/capability_identity_test.go](../../internal/tools/capability_identity_test.go)
- [internal/tools/guarded_tool_test.go](../../internal/tools/guarded_tool_test.go)
- [internal/tools/reduction_test.go](../../internal/tools/reduction_test.go)
- [internal/tools/registry_selection_test.go](../../internal/tools/registry_selection_test.go)

### 06

- [internal/approval/checkpoint_store_test.go](../../internal/approval/checkpoint_store_test.go)
- [internal/approval/codec_test.go](../../internal/approval/codec_test.go)
- [internal/approval/manager_test.go](../../internal/approval/manager_test.go)
- [internal/permission/engine_test.go](../../internal/permission/engine_test.go)
- [internal/permission/modes_test.go](../../internal/permission/modes_test.go)
- [internal/permission/module_test.go](../../internal/permission/module_test.go)
- [internal/permission/presenter_test.go](../../internal/permission/presenter_test.go)
- [internal/permission/security_regression_test.go](../../internal/permission/security_regression_test.go)
- [internal/permission/store_test.go](../../internal/permission/store_test.go)
- [internal/sandbox/cache_protection_test.go](../../internal/sandbox/cache_protection_test.go)
- [internal/sandbox/fingerprint_test.go](../../internal/sandbox/fingerprint_test.go)
- [internal/sandbox/manager_test.go](../../internal/sandbox/manager_test.go)
- [internal/sandbox/pathguard_test.go](../../internal/sandbox/pathguard_test.go)
- [internal/sandbox/readonly_command_test.go](../../internal/sandbox/readonly_command_test.go)

### 07

- [internal/credential/system_test.go](../../internal/credential/system_test.go)
- [internal/models/factory_test.go](../../internal/models/factory_test.go)
- [internal/models/reasoning_replay_test.go](../../internal/models/reasoning_replay_test.go)
- [internal/models/store_test.go](../../internal/models/store_test.go)

### 08

- [internal/collaboration/store_test.go](../../internal/collaboration/store_test.go)
- [internal/mcp/einoadapter/integration_test.go](../../internal/mcp/einoadapter/integration_test.go)
- [internal/mcp/einoadapter/metadata_update_test.go](../../internal/mcp/einoadapter/metadata_update_test.go)
- [internal/mcp/einoadapter/stdio_integration_test.go](../../internal/mcp/einoadapter/stdio_integration_test.go)
- [internal/mcp/fingerprint_test.go](../../internal/mcp/fingerprint_test.go)
- [internal/mcp/manager_test.go](../../internal/mcp/manager_test.go)
- [internal/mcp/naming_test.go](../../internal/mcp/naming_test.go)
- [internal/mcp/store_test.go](../../internal/mcp/store_test.go)
- [internal/mcp/types_test.go](../../internal/mcp/types_test.go)
- [internal/skills/runtime_integration_test.go](../../internal/skills/runtime_integration_test.go)
- [internal/skills/skills_v2_test.go](../../internal/skills/skills_v2_test.go)

### 09

- [internal/notifications/service_test.go](../../internal/notifications/service_test.go)
- [internal/proactive/approval_reminders_test.go](../../internal/proactive/approval_reminders_test.go)
- [internal/proactive/automation_finalize_test.go](../../internal/proactive/automation_finalize_test.go)
- [internal/proactive/decision_test.go](../../internal/proactive/decision_test.go)
- [internal/proactive/deferred_rules_test.go](../../internal/proactive/deferred_rules_test.go)
- [internal/proactive/manager_test.go](../../internal/proactive/manager_test.go)
- [internal/proactive/retention_test.go](../../internal/proactive/retention_test.go)
- [internal/proactive/store_test.go](../../internal/proactive/store_test.go)
- [internal/proactive/workspace_monitor_test.go](../../internal/proactive/workspace_monitor_test.go)
- [internal/tasks/manager_test.go](../../internal/tasks/manager_test.go)
- [internal/tasks/once_schedule_test.go](../../internal/tasks/once_schedule_test.go)
- [internal/tasks/run_updates_test.go](../../internal/tasks/run_updates_test.go)
- [internal/tasks/schedule_test.go](../../internal/tasks/schedule_test.go)
- [internal/tasks/store_test.go](../../internal/tasks/store_test.go)

### 10

- [internal/databackup/archive_test.go](../../internal/databackup/archive_test.go)
- [internal/databackup/encrypted_test.go](../../internal/databackup/encrypted_test.go)
- [internal/databackup/schedule_encrypted_test.go](../../internal/databackup/schedule_encrypted_test.go)
- [internal/searchindex/controller_test.go](../../internal/searchindex/controller_test.go)
- [internal/searchindex/document_scan_test.go](../../internal/searchindex/document_scan_test.go)
- [internal/searchindex/index_test.go](../../internal/searchindex/index_test.go)
- [internal/usecases/mcp_configuration_test.go](../../internal/usecases/mcp_configuration_test.go)
- [internal/usecases/skill_maintenance_test.go](../../internal/usecases/skill_maintenance_test.go)
- [internal/websearch/service_test.go](../../internal/websearch/service_test.go)
- [internal/workspace/browser_test.go](../../internal/workspace/browser_test.go)
- [internal/workspace/manager_test.go](../../internal/workspace/manager_test.go)

### 11

- [frontend/src/utils/agentStateFlow.test.js](../../frontend/src/utils/agentStateFlow.test.js)
- [frontend/src/utils/appShellEvents.test.js](../../frontend/src/utils/appShellEvents.test.js)
- [frontend/src/utils/defaultBuiltinTools.test.js](../../frontend/src/utils/defaultBuiltinTools.test.js)
- [frontend/src/utils/featureRegistry.test.js](../../frontend/src/utils/featureRegistry.test.js)
- [frontend/src/utils/i18n.test.js](../../frontend/src/utils/i18n.test.js)
- [frontend/src/utils/markdown.test.js](../../frontend/src/utils/markdown.test.js)
- [frontend/src/utils/menuTooltip.test.js](../../frontend/src/utils/menuTooltip.test.js)
- [frontend/src/utils/permissionModeFlow.test.js](../../frontend/src/utils/permissionModeFlow.test.js)
- [frontend/src/utils/runtimeActivityFlow.test.js](../../frontend/src/utils/runtimeActivityFlow.test.js)
- [frontend/src/utils/runtimeErrorFlow.test.js](../../frontend/src/utils/runtimeErrorFlow.test.js)
- [frontend/src/utils/searchPolling.test.js](../../frontend/src/utils/searchPolling.test.js)
- [frontend/src/utils/sessionMessageFlow.test.js](../../frontend/src/utils/sessionMessageFlow.test.js)
- [frontend/src/utils/sessionSelectionFlow.test.js](../../frontend/src/utils/sessionSelectionFlow.test.js)
- [frontend/src/utils/settingsStateFlow.test.js](../../frontend/src/utils/settingsStateFlow.test.js)
- [frontend/src/utils/sharedStateFlow.test.js](../../frontend/src/utils/sharedStateFlow.test.js)
- [frontend/src/utils/skillCommand.test.js](../../frontend/src/utils/skillCommand.test.js)
- [frontend/src/utils/taskStateFlow.test.js](../../frontend/src/utils/taskStateFlow.test.js)
- [frontend/src/utils/toolTrace.test.js](../../frontend/src/utils/toolTrace.test.js)
- [frontend/src/utils/workspaceStateFlow.test.js](../../frontend/src/utils/workspaceStateFlow.test.js)
- [internal/services/modeldiagnostic_test.go](../../internal/services/modeldiagnostic_test.go)
- [internal/services/taskservice_test.go](../../internal/services/taskservice_test.go)

