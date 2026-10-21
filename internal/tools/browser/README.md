# 浏览器工具

本包拥有 browser Factory、按 Agent 隔离的 Chrome 会话及代理生命周期。Eino 工具仍通过 `app/tools.go` 注册并经过统一权限 Guard；会话不借用用户日常 Chrome Profile。

`browser_tool.go` 负责 Rod/CDP、页面操作与截图；`browser_proxy.go` 负责连接转发、并发限制与关闭清理。HTTP/HTTPS、WS/WSS 的真实拨号复用 `publicweb` 的地址检查，混合公网/私网 DNS 整体拒绝；没有 DIRECT 回退。

截图通过 `tools.ImageWriter` 写入当前会话，视觉观察通过构造参数注入。网站验证页返回 `needs_human_verification`，由用户完成。此边界不等价于对 Chrome 全部原生能力的操作系统沙箱保证。
