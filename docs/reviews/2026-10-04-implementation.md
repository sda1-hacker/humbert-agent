# 非 RAG 修复与代码精简说明

日期：2026-10-04。对应 [修改前评审](2026-10-04-non-rag-review.md) 的 F1–F6。保留现有 App、Runtime、Store、Guard 和 Eino 结构，修改集中在真实的安全与生命周期缺口。未修改 `internal/rag/**`，未新增 Go/npm 依赖。

## 1. 修改结果

| 问题 | 当前行为 | 主要代码 |
| --- | --- | --- |
| F1：派生数据绕过原件保护 | 非 FullAccess 模式下，整个应用 `cache` 目录进入既有硬保护规则，覆盖搜索数据库、WAL/SHM、浏览器 Profile 和符号链接别名 | [sandbox/manager.go](../../internal/sandbox/manager.go) |
| F2：有工具调用的失败任务自动重放 | 仅 failed/timed_out 且 `ToolCalls == 0` 的运行可自动重试；重启恢复和旧重试队列启动前也使用同一判断 | [manager_events.go](../../internal/tasks/manager_events.go)、[manager_schedule.go](../../internal/tasks/manager_schedule.go) |
| F3：终态事件发出时 Session 仍 busy | 成功、失败、取消共用 `finishRun`，先完成必要收尾、生命周期通知和占用释放，再发布一次终态事件 | [runtime/events.go](../../internal/runtime/events.go)、[runs.go](../../internal/runtime/runs.go) |
| F4：MCP 更新破坏运行快照 | 仅改 Name 保留连接；连接配置变化把旧连接退休，新轮使用新配置，旧轮完成后释放旧连接 | [mcp/manager.go](../../internal/mcp/manager.go)、[backend.go](../../internal/mcp/einoadapter/backend.go) |
| F5：浏览器检查与真实拨号分离 | Chrome 使用统一 HTTP 代理，代理复用 `publicWebDialer`，校验 DNS 后直接拨号已校验 IP；新页面不依赖单页 CDP 检查 | [browser_proxy.go](../../internal/tools/builtin/browser_proxy.go)、[browser_tool.go](../../internal/tools/builtin/browser_tool.go) |
| F6：说明与代码不一致、链接缺失 | 修正命令权限说明，补全四份架构入口，清理失效文件引用，增加本地 Markdown 链接检查 | [架构入口](../architecture/README.md)、[check_links.py](../check_links.py) |

## 2. 删除、合并和复用

相对 `3d01afd`，本次 14 个业务 Go 文件合计增加 549 行、删除 882 行，**净减少 333 行**。该统计包含新增的 `browser_proxy.go`，不含测试和文档；行数减少不等于业务能力删除。

- 删除独立的错误终态处理 `handleExecutionError`、审批等待取消处理 `finishCancelledWaitingRun`，以及成功出口重复的事件/日志构造。共同逻辑统一到 `finishRun`，`sync.Once` 防止重复收尾。两个运行索引在同一临界区释放，保留身份校验。
- 删除浏览器 `Fetch.enable` 和 `Fetch.requestPaused` 分支，不再每次请求额外创建检查 goroutine。公网拨号复用已有实现，HTTP/WS 转发与协议升级使用 Go 标准库，不另写 HTTP 协议栈。
- 删除任务重启恢复中与 `maybeRetry` 重复的状态/次数判断。重试安全条件由一个小函数提供；旧队列的取消复用原调度分支。
- 重整 `webfetch_tool.go` 的过度拆行、临时变量和冗余校验。保留 URL/schema、重定向、响应上限、正文转换、UTF-8 截断、公网 DNS/IP 检查。这部分大量行数减少主要是排版和局部表达精简，不把它称为删除无用业务逻辑。
- MCP 复用现有 `RunLifecycleObserver`，没有新增另一套任务生命周期。连接关闭统一使用 `sync.Once`，替代容易与并发关闭冲突的可变 cleanup 清空操作。
- 文档入口链接到各领域 README，不复制一份完整领域设计。修改前探针保留为历史评审材料；日常维护使用正式包测试。

必要增加的逻辑只有两个明确资源边界：浏览器代理拥有连接/请求限制与关闭路径；MCP 记录运行对旧连接的持有期。没有引入通用事件框架、副作用数据库、工具幂等声明体系或新的持久化格式。

## 3. 行为变化和边界

### 3.1 缓存保护

应用自身的索引、UI 和浏览器仍通过领域服务访问缓存。通用 Agent 文件/命令能力不能因为缓存可重建就读取它；Hard BLOCKED 规则沿现有路径策略用于检查和原生策略生成。FullAccess 保留既有显式选择语义。本次没有删除用户缓存，也不需要清理本地数据。

### 3.2 自动重试

工具 started 包含审批等待、拒绝以及执行结果未知等情况，因此规则保守地停止整轮自动重放，也会拦住已调用只读工具的失败运行。失败提示说明停止原因，用户检查结果后可手动继续；现有模型内部重试机制没有改变。

Runtime 终态事件携带本轮工具总数，Tasks 与已有计数取较大值，补足中间事件未成功持久化时的计数。Tasks 的工具计数已经是现有 Run 字段，没有新增数据库或迁移。旧版本已创建的 retry 在真正启动前检查父运行；不能证明安全时取消。

### 3.3 Runtime 终态

Executor 继续负责保存 partial/error 消息。`finishRun` 只负责收尾、事件与日志，不重复写 Transcript。先通知生命周期观察者收敛所属资源，再释放当前 Session 的占用，终态订阅者可立即预约下一轮。初始化在注册 activeRun 前失败时，也会触发生命周期通知以释放解析期间取得的 MCP 资源。

### 3.4 MCP 连接

主/子工具使用父 RequestID 持有连接。Name 不属于连接身份；端点、传输、命令参数、凭据引用等参与指纹的连接配置发生变化后，旧连接不再供新解析使用，已冻结工具继续使用它，最后一个所属运行结束才关闭。Tool Risk 保留原有行为：不关闭连接，仅影响后续工具描述和权限快照。

显式停用、删除、断开仍立即失效连接；旧工具正常返回连接错误。本次没有实现“整个受影响 Turn 必然被主动取消”，也不会把旧工具静默换到新端点或自动重放调用。Backend 关闭同时释放活动和退休连接，连接本身只执行一次 close/cleanup。

### 3.5 浏览器网络

代理监听本机临时端口，默认出站不使用系统代理或 DIRECT 回退。HTTP 使用 `ReverseProxy`；HTTPS 使用 CONNECT 隧道，不解密 TLS；WS 升级交给标准库。实际拨号使用已校验 IP，混合公网/私网 DNS 整体拒绝。代理限制 64 个并发请求、128 个接受连接以及响应头/空闲等待，关闭时回收包括 hijack 隧道在内的连接。

Chrome 参数移除 localhost 隐式 bypass、禁用 QUIC，并请求限制 WebRTC 非代理 UDP。**WebRTC 参数效果仍需针对实际 Chrome 版本和各平台验证；这不是操作系统级的全协议网络隔离保证。** 已验证新建页面请求仍经过代理，但没有进行真实 DNS rebinding 攻击演练、worker/弹窗/WS 的完整浏览器矩阵或可见桌面 GUI 验证。

浏览器启动失败统一回收临时 Profile 和代理；正常关闭沿 session Owner 回收。已有持久 Profile 保留，不清除用户资料。

## 4. 验证

| 检查 | 结果 |
| --- | --- |
| 显式列出全部非 RAG Go 包，`go test -count=1 -timeout=120s` | 通过，浏览器测试使用 `HUMBERT_TEST_BROWSER=1` |
| 同一非 RAG 包范围 `go vet` | 通过 |
| Runtime、MCP Adapter、Builtin、Tasks、Sandbox 新增关键用例，`go test -race -count=1` | 通过；覆盖终态、并发释放/断开、代理与新页面、重启重试、旧队列和缓存别名 |
| `frontend`：`npm test` | 82 项通过 |
| `frontend`：`npm run build` | 通过，使用已有安装依赖 |
| `python3 docs/check_links.py` | 0 个缺失本地目标；不校验远程 URL 或锚点 |
| `git diff --check` | 通过 |

关键回归测试：

- [cache_protection_test.go](../../internal/sandbox/cache_protection_test.go)：人工缓存、数据库 sidecar、其他 Agent 的浏览器 History 与符号链接别名拒绝访问。
- [manager_test.go](../../internal/tasks/manager_test.go)：恢复重试安全性/幂等、启动前取消不安全的旧 retry。
- [terminal_test.go](../../internal/runtime/terminal_test.go)：成功、失败、取消事件回调可预约下一轮，重复收尾只发布一次。
- [metadata_update_test.go](../../internal/mcp/einoadapter/metadata_update_test.go)：真实本机 MCP HTTP echo 服务验证 Name 更新、端点变更、旧轮/新轮分离、释放，以及并发断开只关闭一次。
- [browser_proxy_test.go](../../internal/tools/builtin/browser_proxy_test.go)：拒绝 IPv4/IPv6 回环和元数据地址、HTTP 转发移除代理凭据、CONNECT 转发和关闭。
- [browser_tool_test.go](../../internal/tools/builtin/browser_tool_test.go)：真实 macOS Chrome 无头交互、截图、持久 Profile，以及新建 Target 无法访问本机监听服务。

所有新增测试使用临时数据与测试 Profile；没有读取用户真实会话、Cookie 或系统凭据。未执行真实模型供应商调用、Windows/Linux 原生沙箱或发版打包。Sonic 在当前工具链回退 JSON、macOS 链接目标版本提示仍是既有非致命提示，本次没有修改工具链。

## 5. 暂不扩大重构范围

保留现有领域分层、消息事实/索引分离、审批 checkpoint 和服务接口。没有仅按文件大小拆分大模块、替换存储、删除正在使用的能力或扩大前端改造。后续性能或模块拆分应由实际热点、调用关系和测试证据驱动，避免以“减少行数”为由制造隐藏副作用。
