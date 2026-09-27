# 第六轮修复：会话选择与审批超时

本轮只处理上次审查确认的两处缺陷，保留现有数据和其他开发改动。

## 1. Agent 切换与侧栏读取互相干扰

之前主界面和侧栏各自调用 `listSessions`。新请求使旧请求返回 `null` 后，主界面的选择流程直接退出，可能出现 Agent 已切换到 B、选中的会话仍属于 A 的状态。

实现调整：

- `frontend/src/stores/sessions.js` 按 Store 实例和 Agent 保存进行中的读取，主界面和侧栏共享结果。显式强制刷新仍读取新的快照，旧请求不能覆盖新数据或清除新请求。
- 切换开始时立即解除旧会话选择。后台强制刷新替换读取后，仍有效的选择流程等待最新结果；删除、归档、重命名等操作依旧通过请求和选择序列使旧流程失效。
- `frontend/src/components/chat/ComposerBar.vue` 在发送前检查当前 Agent、会话归属和加载状态，覆盖 Agent watcher 尚未运行的短暂间隙。
- `frontend/src/components/chat/ChatView.vue` 在加载期间保留输入组件，避免临时清空选择时销毁未发送的附件草稿。

回归测试：`frontend/src/utils/sessionSelectionFlow.test.js`。

覆盖两种加载先后顺序、强制刷新两种返回顺序、侧栏与 AppShell 同时选择、失败后重试、快速切换与删除，以及真实 Composer 脚本的发送目标和文字草稿保留。

## 2. 权限保存失败跨过审批截止时间

之前到期时审批处于 `resolving`，超时 worker 就退出。若权限保存随后失败、状态恢复 `pending`，会话将一直被占用，而且审批已过期，用户无法重新批准。

实现调整：

- `internal/runtime/service.go` 保留每次审批原有的唯一超时 worker；遇到保存中的审批，继续等待处理结果。
- 权限保存失败后用缓冲通知唤醒原 worker。到期前仍允许用户重试；到期后重新领取超时处理责任。始终沿用原来的截止时间。
- 保存成功结束等待并恢复执行；取消结束等待并清理会话占用。没有新增轮询或重试 goroutine。
- 公共审批 API 的生命周期管理与内部处理函数分开，处理函数仍使用真实 Approval Manager 和 Permission Engine。

回归测试：`internal/runtime/approval_timeout_test.go`。

通过测试 Context 固定并发顺序，并用临时策略文件的真实读取失败模拟持久化错误。覆盖截止前保存失败、截止后允许/拒绝保存失败、截止后保存成功、保存期间取消。检查审批状态释放、Session 占用释放和唯一终态事件。测试不连接模型，Resume 的输入校验失败同样必须正确收尾。

## 验证

- `go test -race ./...`：通过。
- `go vet ./...`：通过。
- `npm test`：65 项通过，其中 8 项为本轮新增。
- `npm run build`：通过。
- 桌面入口 Go 构建：通过。

本轮未进行真实模型调用或桌面窗口端到端操作；上述验证覆盖状态流、并发收尾、发送入口以及编译构建。
