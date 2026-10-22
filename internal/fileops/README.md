# 受控文件操作

`Backend` 实现 Eino `filesystem.Backend`，只捕获本轮 `sandbox.EffectivePolicy` 和文件大小/分页限制。构造时复制路径规则；不依赖 Agent、会话、权限审批或工具注册。

| 文件 | 职责 |
| --- | --- |
| `backend.go`、`limits.go` | 六种文件操作、UTF-8 与资源限制；保留现有文档正文搜索 |
| `sandbox_fs.go` | PathGuard 裁决后打开 `os.Root`，遍历后代逐项复查权限 |
| `atomic_fs.go`、`file_transactions.go` | 原子提交、跨会话文件锁和外部修改冲突检测 |
| `search_files.go` | 有扫描上限的遍历与显示路径 |
| `document.go` | 读取受控 PDF/Office 原件；解析与会话附件由调用方管理 |

Eino 工具创建位于 `tools/builtin/filesystem.go`，仍先进入 Registry 的统一 Guard。文件授权、审批和操作系统进程隔离是独立边界，不能互相替代。相对路径、已授权外部目录、读取分页与扫描超限语义保持不变。

搜索使用 Go regexp（RE2）与 doublestar；不调用 ripgrep。PDF/Office 等支持格式会先提取文本，因此行号属于提取结果。不可读、超大、二进制、解析失败的文件会跳过；搜索结果不保证覆盖每一个文件。

`tools.files.max_search_results`、`max_search_entries`、`max_search_line_chars` 分别控制 glob/grep 结果数、遍历目录项数和 grep 单行字符数，默认 500、20000、2000。扫描或结果超限会明确返回错误，单行超限按 Unicode 字符缩短。Eino 工具描述从同一份限制生成；审批和 PathGuard 不受这些展示配置影响。
