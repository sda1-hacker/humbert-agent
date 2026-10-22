# DocumentText：共享文档解析与来源定位

[项目源码详解](../../docs/项目源码详解.md#multimodal) · [RAG 解析流程](../../docs/RAG源码详解.md#loader) · [会话附件](../sessions/README.md)

本包统一附件与 RAG 的文档转换边界：输入快照、格式检查、一次性解析进程、输出限额、缓存、图片提取和来源定位。它不负责知识库绑定、Embedding、切分或数据库发布。

## 两个入口

| 调用方 | 入口与返回 | 限额与行为 |
| --- | --- | --- |
| 会话附件、`extract_document`、工作区文档搜索 | `MIMEForName`、`Validate`、`Extract`；返回 Markdown/MIME | PDF、DOCX、XLSX、PPTX；12 MiB 输入、512 KiB 输出、20 秒、Office 解压 64 MiB/2048 条目；不保留待识别图片 |
| RAG Loader | `ExtractFile(ctx, path, ParserOptions)`；返回 Markdown、图片、SourceBlocks 和诊断 | 默认输入/输出各 32 MiB，ZIP 解压 256 MiB/8192 条目，1 分钟，最多 256 张图片、单图 3200 万像素；调用方可配置 |

聊天上传 PDF/Office 只验证并保存原件，不提前转换全文；Agent 调用 `extract_document` 时按需读取。普通 UTF-8 文本附件有独立处理路径。RAG 的 `PreserveImages` 让 Loader 保存提取图片，再由 RAG 图片流水线生成 OCR/描述证据；聊天附件不会使用这条模型识别链路。

## 转换流程

```mermaid
flowchart LR
  A[已授权源文件] --> S[限额内复制快照]
  S --> V[格式与 ZIP 预检]
  V --> C{有效缓存}
  C -->|命中| R[Markdown / 图片 / 来源]
  C -->|未命中| W[一次性解析子进程]
  W --> B[有界输出与结果校验]
  B --> R
```

路径授权由调用方完成；解析前复制与再次预检防止检查后源文件被替换。Worker 复用当前可执行程序，通过环境标记进入解析入口，限制 stdout/stderr；取消和超时终止进程树。子进程只接收必要的运行环境，不继承模型密钥。资源限制包括文件、输出、解压、图片与时间预算，不能把它描述成完整的操作系统沙箱或硬内存限制。

PDF 使用 Tabula 文本层与布局处理，扫描页可由内嵌 PDFium 渲染。未保留图片的附件路径会尝试本地 OCR；构建未提供 OCR 支持或识别无文字时明确失败。RAG 保留图片时，把扫描页/插图交给后续图片处理，不在文档转换中调用回答 Agent。显式配置 PaddleOCR-VL 后会把 PDF 原件发送到该结构化服务，失败直接返回，不静默切回另一种解析结果。

`SourceLocator` 按格式记录 PDF 页、DOCX 块、幻灯片、工作表/行和图片；`Start/End` 是最终处理文本的 rune 范围。没有可靠信息时只返回转换 Markdown 的位置，不推造页码或 BBox。RAG 按分块裁剪定位信息。

解析缓存默认总预算 256 MiB；空目录关闭缓存，`auto` 使用系统用户缓存目录，桌面 RAG 固定在 `knowledge/parse-cache/`。缓存含正文与图片，键包含原件内容和解析选项；损坏、版本不符或超限会重新解析。图片识别动作缓存属于 RAG，不属于此解析缓存。

## 文件职责

| 文件 | 职责 |
| --- | --- |
| `extract.go` | 附件格式/容器校验和固定限额入口 |
| `parser.go`、`worker_unix.go`、`worker_windows.go` | 通用选项、快照、Worker 通信和取消 |
| `options.go`、`pdf*.go` | PDF 布局、文本清洗、扫描判定、图片渲染和可选结构化服务 |
| `source.go`、`normalize.go` | 来源映射与范围校准 |
| `spreadsheet*.go`、`table.go` | 工作表顺序、数值格式、行列与表格转换 |
| `images.go`、`office_images.go`、`markdown.go` | 图片格式/像素校验和文档内图片引用 |
| `cache.go` | 缓存键、并发占用、校验及容量清理 |

改动时重点验证损坏文档、ZIP 膨胀、超限位图、扫描页、取消、缓存失效和来源范围。逐函数与字段索引见 [RAG 源码索引](../../docs/RAG源码详解.md#source-index)。
