# 03 上下文、记忆与多模态

主要源码：[engine.go](../../internal/contextengine/engine.go)、[budget.go](../../internal/contextengine/budget.go)、[projection.go](../../internal/contextengine/projection.go)、[middleware.go](../../internal/contextengine/middleware.go)、[compactor.go](../../internal/contextengine/compactor.go)、[estimator.go](../../internal/contextengine/estimator.go)。

## 上下文投影

`Engine.Build` 从 Session 的当前 ActiveBranch 构建模型上下文，不把 JSONL 所有历史行直接发送给 Provider。`projectActiveBranch` 找到最近 Compaction，定位 FirstKeptEntryID，将摘要作为 User checkpoint 放到保留历史之前，再 DecodeMessage。控制类 Entry、非当前分支和已经被摘要覆盖的正文不会重复作为普通消息发送。

每个恢复消息携带内部 EntryID，供提交摘要时精确定位。Thinking 是否回放由 Provider 策略决定；Assistant 的持久化 thinking 不自动等于下一次请求内容。针对中断后缺少 ToolResult 的历史，Eino patchtoolcalls 生成 `status: unknown` 的占位结果并要求先核实状态；占位用于模型上下文，不证明工具未执行，也不自动重放。

投影后检查 ToolCall/ToolResult 事务对应关系。压缩边界不能从一个 ToolResult 中间开始，必须向前找到产生它的 Assistant ToolCall。

## Token 预算的计算

设模型窗口 W、最大输出 O。`CalculateBudget` 对 W < 64Ki 的小窗口预留 `max(ceil(0.20W), O)`；大窗口预留 `max(ceil(0.10W), 16Ki, O)`。硬阈值 H = W − reserve。初始软阈值约为 0.85H。

近期保留目标为 `clamp(ceil(0.20W), 8Ki, 32Ki)`，太接近硬阈值时再缩小。摘要预算从约 0.04W 起算，通常夹在 2048–8192 Token，并继续受小窗口容量限制。这些数字是代码配置默认值，不是所有模型的理论通用最优值。

`ResolveBudgetForFixedContext` 扣除 System 和 Tool Schema 的固定成本；可用于历史的预算 = H − fixed。它根据摘要预留调整近期目标，软阈值改为 fixed + 0.80 × 可压缩容量，并约束在 checkpoint 成本与硬阈值之间。工具太多或系统指令太大时，不能靠压缩历史解决固定成本超额。

`Usage` 分别返回 system/tool/checkpoint/message、reserve、硬/软阈值、百分比和 NeedsCompaction；`Assembly` 返回实际消息/工具数量、checkpoint 和窗口身份，供 ContextPanel 展示。

## 估算器

ApproxEstimator 不是精确 tokenizer：ASCII 约四字符一个 Token，非 ASCII 约一个字符一个 Token；再加消息、ToolCall 和 Schema 开销。图片按固定近似成本，旧附件占位与实际 Hydrate 使用同一 replay mask。

收到真实 prompt Usage 后，以真实/估算比例做温和校准：比例限制 0.75–1.35，按旧 factor 的 90% 与本次的 10% 更新，总 factor 限制 0.80–1.30。校准存在进程内，不能宣称不同 Provider 的精确计费。Usage 是容量指导，供应商 Usage 是执行计账来源。

## 压缩的三个时机

| 时机 | 实现 |
| --- | --- |
| Turn 开始前 / 用户手动 | `Engine.Compact` → Build → Eino Summarize → Commit → Build，产生持久化 checkpoint |
| ReAct 运行中 | `MidRunCompactor.BeforeModelRewriteState` 改写内存消息窗口，保存 pendingSummary，不在模型循环中直接追加 JSONL |
| Turn 维护阶段 | Runtime 的 MaintainAfterTurn 提交已经准备好的摘要，FirstKept 必须已在当前持久化分支中可定位 |

`summaryBoundary` 从尾部累加近期 Token，尽量移到完整 User turn 起点；遇到 ToolResult 回退到工具事务起点。输入摘要模型的材料使用受限序列化，参数中的敏感内容被处理。摘要必须非空、减少 Token 且保留合法工具事务；若当前用户请求被移出近期区，会把请求原文重新放入摘要。已加载 Skill 的定义也保留为参考资料。

MidRun 摘要失败先保留原窗口并记录原因；随后仍检查硬阈值，超过时返回 ErrContextBudgetExceeded，不能无上限继续。`Commit` 使用 EntryID/ToolCallID 定位保留边界，加载当前分支并携带 ExpectedLeafID，避免摘要提交到已经变化的历史。手动压缩由 Runtime reservation 排除与正在运行的会话并发。

## 大工具结果与按需读取

[tools/reduction.go](../../internal/tools/reduction.go) 在模型窗口截断/清理前归档单段文本结果。截断结果保留短预览和 Artifact ID；历史清理保留工具参数，避免丢失文件效果展示和审计身份。`skill`、`context_resource` 排除清理/截断，读取工具不会递归归档自己的回复。

[contextartifact/store.go](../../internal/contextartifact/store.go) 将完整内容保存到当前 Session 的 `context-artifacts/artifact_<uuid>.json`，字段含 version、ID、SessionID、ToolName、Content、Chars、CreatedAt。路径只通过 SessionDirectoryResolver 获取；Read 校验文档身份和版本。Artifact 是完整大结果的受控副本，删除 Session 一并删除。

模型使用 `context_resource` 按 offset/limit 读取 Artifact 或当前会话旧附件；`session_history` 在当前 ActiveBranch 中恢复历史。精简的是工作窗口，原文没有变成不可找回的纯摘要。

## 用户记忆与语言

[preferences/store.go](../../internal/preferences/store.go) 保存 name/avatar/language；支持 zh-CN、en-US、ja-JP、ko-KR。独立 [memory.go](../../internal/preferences/memory.go) 保存 `personal-memory.json`，最多 20 条、每条 1–300 个字符，来源 manual 或 conversation，后者带 SessionID/EntryID。

用户选择会话文本保存记忆时，桌面服务验证来源；后续 Resolver 将这些明确保存的内容加入指令。没有自动从所有对话提取永久个人记忆的后台模型。Context 摘要是会话派生记忆，两者的所有权和用途不同。

## 附件回放与视觉桥接

[multimodal/replay.go](../../internal/multimodal/replay.go) 从末尾数用户输入，只对当前用户输入和紧邻一次追问重放完整图片/文本附件；更早的材料变成包含名称、MIME、附件 ID 的占位。文件需要时通过资源工具取回，图片以历史观察为参考。Estimator 与 Hydrate 共用规则，减少预算与实际请求的漂移。

[vision_bridge.go](../../internal/multimodal/vision_bridge.go) 只在 Chat 不支持视觉时调用视觉辅助模型。它发送本次用户请求和选中图片，取得有限长度的事实观察，替换图片部分并追加观察块；不会把最终问答交给视觉模型。支持视觉的 Chat 直接接收图片。浏览器截图的观察由 [usecases/vision.go](../../internal/usecases/vision.go) 使用同一模型配置和计账链路。

## 文档解析和头像

[documenttext/extract.go](../../internal/documenttext/extract.go) 明确支持 PDF、DOCX、XLSX、PPTX 扩展名/MIME，上传先 Validate，不立刻提取整份正文。PDF 检查签名，Office 检查 ZIP 结构/展开大小。Extract 通过当前程序的专用 worker 模式调用 Tabula 转 Markdown：输入最多 12MiB、输出最多 512KiB、Office 展开最多 64MiB，解析超时 20 秒；PDF 关闭 OCR，扫描件无文字时返回需要 OCR 的错误。

worker 环境标识为 `HUMBERT_DOCUMENT_WORKER_FILE`，与普通 Agent Runtime 分离。解析超时和输出上限不等于操作系统内存/网络沙箱；本文不把它描述成完整恶意文档隔离。第三方包路径中的 rag 名称仅属于解析依赖 API，不分析本项目排除的 `internal/rag`。

[avatar/avatar.go](../../internal/avatar/avatar.go) 验证受支持的 base64 图片 DataURL、实际 MIME 与大小，最大 2MiB；用户与 Agent 头像共用此规则，不接受任意 HTML/SVG 作为头像注入。
