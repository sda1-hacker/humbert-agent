# AtomicFile：小型 JSON 文档的安全读写

[总目录](../../docs/architecture/README.md) · [Config](../config/README.md)

`WriteJSON` 在目标同目录创建临时文件，编码 JSON、完整写入、fsync、关闭后 rename 原子替换；Unix 上收紧权限。它拒绝覆盖符号链接或目录。`ReadJSON` 严格解码，未知字段报错，缺文件仍返回 `os.ErrNotExist` 供调用方初始化默认值。

```mermaid
flowchart LR
  V[业务值] --> M[JSON 编码]
  M --> T[同目录临时文件]
  T --> S[fsync + close]
  S --> R[rename 提交]
  R --> J[config.json]
```

这是单文件原子提交，不是多文件事务，也不提供读改写互斥。Agent、Task、权限等 Store 必须自己持锁，避免两个并发更新互相覆盖。路径应由受控数据目录推导，不能把用户任意路径直接交给本包。代码集中在 `json.go` 的 `WriteJSON`、`ReadJSON`、目录验证和 `replaceFile`。
