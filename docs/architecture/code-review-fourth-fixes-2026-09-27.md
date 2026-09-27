# 第四轮审查修复：主动任务失败后的调度死锁

## 问题与修复

主动助手创建的 Agent 任务排队后，如果工作区在启动前被移动或删除，调度器会在持有 `cycleMu` 时同步发布 `run.failed`。原来的 Proactive 回调立即调用 `Task Manager.Archive`，再次等待同一把锁，导致调度和其他需要调度锁的操作无法继续。

本轮只调整主动任务的终态消费路径：

1. `handleTaskPayload` 收到自动任务终态时只调用 `signalWake`，不在同步回调中归档任务。
2. 现有 `Manager.loop` 在收到唤醒或定时检查时调用 `finalizeCompletedAutomations`。它读取执行中 Record 关联的 TaskRun，以已落盘终态为准，再通过原有收尾方法更新记录并归档。
3. `executeRecord` 在 Run ID 回填成功后再次唤醒，避免任务在关联建立前已经完成、第一次唤醒没找到关联记录的情况。

实现见 [manager.go](../../internal/proactive/manager.go)，模块约束见 [Proactive README](../../internal/proactive/README.md)。复用已有后台循环、唤醒通道、Record 与 TaskRun，不增加持久化格式或独立 Worker。重复事件可以合并；关闭主动规则不会阻止已有任务收尾。进程重启仍使用原有 `reconcileRecords` 恢复。

## 验证

新增 [automation_finalize_test.go](../../internal/proactive/automation_finalize_test.go)，包含 5 个顶层测试：

- 真实临时工作区被删除后，自动运行记录失败，后续通知任务正常执行，后台完成内部任务归档；主动规则关闭时同样成立。
- 发布方持有调度锁时，同步回调仍能返回；100 个重复终态事件合并唤醒，旧事件的状态不能覆盖 Task Store 中真实结果，重复消费不改写已完成记录。
- 终态事件先于 Run ID 回填时，早期消费不会错误终结尚未关联的记录，回填后的唤醒能补齐收尾。
- 从磁盘恢复时，成功与失败运行、Run ID 已回填与未回填四种组合都能完成记录收尾及归档。
- 取消的消费不修改记录；结果落盘失败时仍保留待收尾状态，恢复写入后可以重试。

以下检查均已通过：

```sh
GOCACHE=/tmp/humbert-go-cache go test -race ./internal/proactive ./internal/tasks -count=1
GOCACHE=/tmp/humbert-go-cache go test -race ./...
GOCACHE=/tmp/humbert-go-cache go vet ./...
GOCACHE=/tmp/humbert-go-cache go build -tags production -trimpath -buildvcs=false -o /tmp/humbert-agent-fourth-fixed ./cmd/desktop
git diff --check
```

测试不调用真实模型或系统通知；通知链路使用应用的内存通知服务。桌面构建使用已有前端生产资源，本轮没有改动前端代码。编译仍存在此前记录的 macOS 部署版本链接警告，未据此验证旧版 macOS 兼容性。
