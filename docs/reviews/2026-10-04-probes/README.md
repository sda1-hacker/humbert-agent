# 非 RAG 评审探针

这些材料对应 [评审与修改规划](../2026-10-04-non-rag-review.md) 的 F1–F4，适用于基线 `3d01afd`。它们使用现有测试 fixture，后续重构 fixture 时可能需要同步调整。

代码修复后已转为各包的正式回归测试，见 [修改说明](../2026-10-04-implementation.md)。本目录保留修改前的复现材料和原始输出；日常检查运行正式测试，不维护两套相同测试。

全部使用临时人工数据；没有读取真实会话、系统凭据、Cookie 或真实外部服务。MCP 探针启动本机临时 HTTP echo 服务，不访问模型供应商。

## 运行

在项目根目录运行：

```bash
python3 docs/reviews/2026-10-04-probes/run_probes.py
python3 docs/reviews/2026-10-04-probes/run_probes.py --mcp
```

第一条只运行 Sandbox/Tasks/Runtime 三个探针；第二条只运行 MCP 探针，需要运行环境允许监听本机临时端口。需要本项目的 Go 工具链与依赖；脚本沿用已有缓存，不安装新的工具。

脚本用 `go test -overlay` 把 `.go.txt` 映射成目标包中的人工测试文件，不改写产品源码。overlay JSON 保存在临时目录，执行结束自动回收；Go 常规构建缓存仍按其正常规则使用。`.go.txt` 不进入常规 `go test` 的包扫描。

**基线上的预期结果是退出码 1，而不是通过。** 断言表达建议修复后的行为。修复后应通过，并进一步完善成正式回归测试。不要把断言反转为“允许读缓存/关闭旧工具/继续重试”来让基线变绿。

## 探针与证据边界

| 文件 | 用例 | 证明的内容 | 没有证明的内容 |
| --- | --- | --- | --- |
| [sandbox_test.go.txt](sandbox_test.go.txt) | 人工 Home 下使用真实硬保护列表和 Standard 读规则 | Transcript 原件被拒绝，敏感缓存路径仍可读 | 未用原生命令读取真实资料，未解密 Cookie |
| [tasks_test.go.txt](tasks_test.go.txt) | MaxAttempts=2、failed、ToolCalls=1 | `maybeRetry` 没有因既有工具调用阻止重试 | 未执行真实重复写入；计数本身不是副作用证明 |
| [runtime_test.go.txt](runtime_test.go.txt) | 完成事件订阅者立即预约下一轮 | 终态回调时 reservation 尚未释放 | 未统计实际 GUI/连续任务触发频率 |
| [mcp_test.go.txt](mcp_test.go.txt) | 真正的 HTTP MCP echo、Name 更新、同一旧工具再次调用 | 显示更新关闭快照引用的 session | 未覆盖 stdio；未证明所有连接更新都应该保留旧授权 |

人工 Runtime 用例直接进入 `completeTurn`，不调用真实模型；Sandbox 用例检查实际策略裁决，不创建 native 进程。Browser 的 F5 没有放入此目录，因为尚未完成受控浏览器攻击/旁路验证。

[baseline-output.txt](baseline-output.txt) 保存本次运行原始输出。输出中的 UUID 与 ID 均来自人工测试。非零结果源于上述四个断言，不是一次失败的常规测试套件。

## 后续正式测试

将合适的探针转入对应包时，保留其“原先失败、修复后通过”的行为目标，并补完整失败/取消/重启路径。尤其需要区分工具 started、审批等待、真正执行和效果 unknown；MCP 需补 lease 释放与 stdio 资源收敛；Runtime 需补 Continuous 调度和旧清理不能删除新占用的检查。
