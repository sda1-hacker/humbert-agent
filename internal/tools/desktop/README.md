# 桌面观察与操作工具

本包提供 computer Factory，管理单轮观察身份、桌面占用、输入前复核和截图结果。系统驱动仍由 `internal/computer` 拥有；截图通过 `tools.ImageWriter` 保存，视觉模型调用由装配层注入。

工具名称、参数、每轮限额、接管/取消和跨会话隔离保持不变。Registry 继续提供权限与审批；桌面应用自身的文件/网络行为不由文件 Backend 隔离。
