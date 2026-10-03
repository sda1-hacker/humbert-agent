# Go RAG Foundation

> 一个以 Go 为核心、强调模块解耦、可替换 Provider、可测试和可扩展的 RAG 基础工程。
>
> 本文档描述项目 **第 1～16 阶段已经完成的能力**。你尚未接入的第 17 阶段 Evaluation / E2E Test 不纳入当前基线。

---

## 目录

- [1. 项目目标](#1-项目目标)
- [2. 当前能力总览](#2-当前能力总览)
- [3. 总体架构](#3-总体架构)
- [4. 完整数据流](#4-完整数据流)
- [5. OCR：是否支持、如何工作](#5-ocr是否支持如何工作)
- [6. Chunk 模型与核心约定](#6-chunk-模型与核心约定)
- [7. Chunking 全流程](#7-chunking-全流程)
- [8. Overlap 到底作用在哪里](#8-overlap-到底作用在哪里)
- [9. Protected Span](#9-protected-span)
- [10. Markdown Table Header 处理](#10-markdown-table-header-处理)
- [11. Document Profiler](#11-document-profiler)
- [12. Validator](#12-validator)
- [13. Strategy Resolver](#13-strategy-resolver)
- [14. Heading Splitter](#14-heading-splitter)
- [15. Heuristic Splitter](#15-heuristic-splitter)
- [16. Parent-Child Chunking](#16-parent-child-chunking)
- [17. Eino Chunk Transformer](#17-eino-chunk-transformer)
- [18. Tabula Loader](#18-tabula-loader)
- [19. SearchContent](#19-searchcontent)
- [20. PostgreSQL 数据模型](#20-postgresql-数据模型)
- [21. Embedding](#21-embedding)
- [22. Dense Retrieval](#22-dense-retrieval)
- [23. BM25 Retrieval](#23-bm25-retrieval)
- [24. Hybrid RRF](#24-hybrid-rrf)
- [25. Reranker](#25-reranker)
- [26. Parent Context Expansion](#26-parent-context-expansion)
- [27. Application Service](#27-application-service)
- [28. Composition Root：NewRAG](#28-composition-rootnewrag)
- [29. 从零构建自己的 RAG](#29-从零构建自己的-rag)
- [30. 模块级使用手册](#30-模块级使用手册)
- [31. 关键配置参考](#31-关键配置参考)
- [32. 测试方式](#32-测试方式)
- [33. 已知边界与当前未实现能力](#33-已知边界与当前未实现能力)
- [34. 推荐的工程演进方向](#34-推荐的工程演进方向)
- [35. FAQ](#35-faq)

---

# 1. 项目目标

这个工程的目标不是做一个“只能运行一次的 RAG Demo”，而是建立一个可以继续扩展为知识库系统的 **RAG Foundation**。

核心目标：

```text
Raw File
   ↓
Parser / Loader
   ↓
Markdown
   ↓
Adaptive Chunking
   ↓
Parent / Child
   ↓
SearchContent
   ↓
Embedding
   ↓
PostgreSQL
   ├── pgvector Dense
   └── ParadeDB BM25
   ↓
Hybrid Retrieval
   ↓
RRF
   ↓
Reranker
   ↓
MMR
   ↓
Parent Context Expansion
   ↓
SearchResult
```

项目刻意保持以下依赖方向：

```text
Application
    ↓
RAG Core
    ↓
Ports / Interfaces
    ↑
Infrastructure Adapters
```

其中：

- `internal/rag/chunker` 不知道 Eino。
- `internal/rag/chunker` 不知道 PostgreSQL。
- `internal/rag/chunker` 不知道 Tabula。
- Retrieval Core 不知道 pgvector。
- Rerank Core 不知道具体 Provider。
- Application Service 不直接知道 ParadeDB SQL。
- Provider 可以替换，不污染核心算法。

---

# 2. 当前能力总览

截至第 16 阶段，目前已经完成：

| 模块 | 能力 |
|---|---|
| Parser | Tabula 多格式解析 |
| OCR | 扫描 PDF 自动 OCR fallback（可选 build tag） |
| Normalization | 换行统一、Markdown 输入 |
| Recursive Split | 多级 separator recursive splitting |
| Protected Span | Code / Math / Link / Table 等保护 |
| Semantic Overlap | 段落 / 换行 / 句子边界 overlap |
| Table | 跨 Chunk 表头恢复 |
| Profiler | 文档结构与语言分析 |
| Validator | 分块结果质量检查 |
| Strategy | Auto / Heading / Heuristic / Legacy |
| Heading | Markdown Heading-aware splitting |
| Heuristic | 无 Markdown Heading 文档的结构边界切分 |
| Parent-Child | 小块检索、大块阅读 |
| Eino Adapter | `schema.Document` ↔ Chunk |
| SearchContent | title + breadcrumb + body |
| Embedding | Eino OpenAI-compatible Embedder |
| Storage | PostgreSQL |
| Dense | pgvector `halfvec(1024)` + HNSW |
| Keyword | ParadeDB BM25 |
| Fusion | Weighted RRF |
| Rerank | Provider-independent rerank engine |
| Diversity | MMR |
| Parent Expansion | Child hit → Parent context |
| Application | `Ingest()` / `Search()` |
| Composition | `NewRAG()` |

---

# 3. 总体架构

推荐目录：

```text
internal/rag/
├── application/
│   └── service.go
│
├── chunker/
│   ├── chunk.go
│   ├── config.go
│   ├── normalize.go
│   ├── protected_span.go
│   ├── recursive.go
│   ├── unit.go
│   ├── header_tracker.go
│   ├── overlap.go
│   ├── merge.go
│   ├── splitter.go
│   ├── patterns.go
│   ├── tokens.go
│   ├── profiler.go
│   ├── validator.go
│   ├── strategy.go
│   ├── heading_hierarchy.go
│   ├── heading_splitter.go
│   ├── heuristic_splitter.go
│   └── parent_child.go
│
├── transformer/
│   └── chunker/
│       └── transformer.go
│
├── loader/
│   └── tabula/
│       └── loader.go
│
├── searchcontent/
│   └── builder.go
│
├── indexer/
│   └── postgres/
│       ├── pool.go
│       ├── schema.go
│       ├── indexer.go
│       └── ingestion.go
│
├── retrieval/
│   ├── result.go
│   └── rrf.go
│
├── retriever/
│   └── postgres/
│       ├── retriever.go
│       ├── context_loader.go
│       └── pipeline_factory.go
│
├── rerank/
│   └── reranker.go
│
├── search/
│   └── pipeline.go
│
├── provider/
│   ├── embedding/
│   │   └── openai/
│   │       └── embedder.go
│   └── rerank/
│       └── http/
│           └── client.go
│
└── rag.go
```

依赖关系：

```text
                          ┌─────────────────────┐
                          │ HTTP / CLI / Worker │
                          └──────────┬──────────┘
                                     ↓
                          ┌─────────────────────┐
                          │ application.Service │
                          │ Ingest / Search     │
                          └──────────┬──────────┘
                                     ↓
             ┌───────────────────────┼───────────────────────┐
             ↓                       ↓                       ↓
      Loader / Parser          Chunking Core          Search Pipeline
             ↓                       ↓                       ↓
        Tabula Adapter       Pure Go Chunker          RRF / Rerank
                                                            ↓
                                                     Parent Expansion
             └───────────────────────┬───────────────────────┘
                                     ↓
                               PostgreSQL
                         ┌───────────┴───────────┐
                         ↓                       ↓
                     pgvector                ParadeDB
```

---

# 4. 完整数据流

## 4.1 Ingestion

```text
File
 ↓
Tabula Loader
 ↓
schema.Document
 ↓
完整 Markdown
 ├────────────────────────────→ documents.markdown
 │
 ↓
Chunking
 ├── Heading
 ├── Heuristic
 └── Legacy
 ↓
Parent-Child（可选）
 ├── Parent
 │    └── chunks only
 │
 └── Child
      ├── chunks
      ├── SearchContent
      ├── Embedding
      └── retrieval_index
```

## 4.2 Retrieval

```text
Query
  │
  ├────────────────────────────┐
  ↓                            ↓
Embedding                    Keyword
  ↓                            ↓
pgvector                    ParadeDB
  ↓                            ↓
Dense TopN                  BM25 TopN
  └────────────┬───────────────┘
               ↓
          Weighted RRF
               ↓
            Reranker
               ↓
         Composite Score
               ↓
              MMR
               ↓
             Top K
               ↓
        Parent Expansion
               ↓
         SearchResult[]
```

---

# 5. OCR：是否支持、如何工作

## 5.1 结论

**目前支持 OCR。**

但要准确理解：

> 当前 OCR 不是我们自己单独实现的 OCR Service，而是使用 Tabula 对“扫描 PDF 页面”的自动 OCR fallback。

也就是说调用代码仍然只是：

```go
markdown, warnings, err := tabula.Open(path).
    ExcludeHeadersAndFooters().
    ToMarkdown()
```

不会写：

```text
if scanned:
    call OCR()
```

Tabula 内部负责判断。

## 5.2 OCR 启用条件

普通构建：

```bash
go build ./...
```

没有开启扫描 PDF OCR。

需要：

```bash
go build -tags ocr ./...
```

测试：

```bash
go test -tags ocr ./...
```

## 5.3 OCR 流程

扫描 PDF 的流程可以理解为：

```text
PDF Page
   ↓
尝试 Native Text Extraction
   ↓
┌─────────────────────────────┐
│ 有足够原生文本？            │
└────────────┬────────────────┘
             │
       Yes   │   No
        ↓    │    ↓
 Native Text │  OCR Fallback
             │    ↓
             │ 尝试整页 Rasterize
             │    ↓
             │ pdftoppm / Poppler
             │    ↓
             │ ~300 DPI Bitmap
             │    ↓
             │ Tesseract
             │    ↓
             │ OCR Text
             │
             └────────────┐
                          ↓
                      Markdown
                          ↓
                       Chunker
```

更具体地说：

1. Tabula 先尝试 PDF 自带的 text layer。
2. 如果页面没有 native text，或者只有少量 stamp / watermark，而主体其实是扫描图像，则进入 OCR fallback。
3. 如果系统存在 `pdftoppm`，优先把整个页面 rasterize 成位图。
4. 再交给 Tesseract。
5. 如果没有 `pdftoppm`，Tabula 会退回 embedded-image 路径。
6. OCR 成功后，得到的文字继续进入 `ToMarkdown()`。
7. Loader 不需要修改后面的 Chunking 流程。

因此 OCR 对后面的模块是透明的：

```text
Native PDF
      │
      ├────────────┐
      │            │
Scanned PDF        │
      │            │
     OCR           │
      │            │
      └─────┬──────┘
            ↓
         Markdown
            ↓
         Chunker
```

## 5.4 我们如何知道 OCR 被用了

Tabula 会返回：

```text
WarningOCRFallback
```

我们的 Loader 将其转换为 metadata：

```text
rag_ocr_used = true
```

同时 warning message 会放进：

```text
rag_parser_warnings
```

因此数据库里的 Document / Chunk 可以继续保留 provenance。

## 5.5 OCR Language

Loader Config 当前支持：

```go
OCRLanguage string
```

例如：

```go
cfg := tabula.DefaultConfig()
cfg.OCRLanguage = "eng+chi_sim"
```

需要系统中存在对应 Tesseract tessdata。

## 5.6 OCR 依赖

开启 `-tags ocr` 后通常需要：

```text
Tesseract
Leptonica
JBIG2 decoder
OpenJPEG
```

如果希望整页 rasterization：

```text
poppler-utils
└── pdftoppm
```

也是推荐依赖。

## 5.7 当前 OCR 不是什么

当前 OCR **不等于 VLM**。

我们现在还没有完整实现：

```text
PDF Figure
 ↓
Extract Asset
 ↓
Store Image
 ↓
VLM
 ↓
Caption / Description
 ↓
Inject Chunk Context
```

也就是说：

### 已支持

```text
扫描 PDF 上的文字
→ OCR
→ Markdown
```

### 还未实现

```text
架构图
流程图
统计图表
照片
截图
示意图
```

的视觉语义理解。

OCR 解决的是：

> 图像里的字符是什么？

VLM 解决的是：

> 这张图表达了什么？

两者不能混为一谈。

## 5.8 当前 OCR 的格式边界

当前主要针对：

```text
scanned PDF
```

不要默认认为：

```text
DOCX 内嵌截图
PPTX 流程图
HTML image
独立 PNG / JPG
```

都会自动进入 OCR。

未来应该单独建立：

```text
Asset Pipeline
```

处理这些内容。

---

# 6. Chunk 模型与核心约定

核心模型：

```go
type Chunk struct {
    Content       string
    ContextHeader string

    Seq int

    Start int
    End   int
}
```

## 6.1 Content

`Content` 是权威正文。

原则：

```text
Content
=
真实 Chunk 内容
```

不要把：

```text
title
breadcrumb
retrieval keywords
```

永久写进 Content。

## 6.2 ContextHeader

例如：

```text
# 产品手册
## Installation
### Linux
```

它是 Chunk 的结构上下文。

Embedding 时可以：

```text
ContextHeader + Content
```

但业务正文仍然保持独立。

## 6.3 Start / End

使用：

```text
Unicode rune offset
```

而不是 UTF-8 byte offset。

区间：

```text
[Start, End)
```

普通 Chunk 通常：

```text
End - Start == RuneLen(Content)
```

## 6.4 Synthetic Content 例外

表格 Chunk 可能自动补：

```text
| Name | Value |
| --- | --- |
```

这是 synthetic repeated table header。

因此某些 Chunk：

```text
End - Start != RuneLen(Content)
```

是允许的。

这不是 source mapping bug。

---

# 7. Chunking 全流程

普通 Legacy Path：

```text
text
 ↓
Normalize
 ↓
protectedSpans()
 ↓
buildUnitsWithProtection()
 ↓
[]splitUnit
 ↓
mergeUnits()
 ↓
semantic overlap
 ↓
table header handling
 ↓
[]Chunk
```

Adaptive Path：

```text
text
 ↓
ProfileDocument
 ↓
resolve Strategy Chain
 ↓
┌────────────┬─────────────┬──────────┐
│ Heading    │ Heuristic   │ Legacy   │
└─────┬──────┴──────┬──────┴────┬─────┘
      ↓             ↓           ↓
   Chunks        Chunks      Chunks
      ↓             ↓           ↓
   Validator     Validator    Validator
      ↓
first acceptable tier
```

## 7.1 Recursive splitting

默认 separator：

```go
[]string{
    "\n\n",
    "\n",
    "。",
}
```

它不是简单：

```go
strings.Split(text, separators...)
```

而是有 separator priority：

```text
paragraph
 ↓
line
 ↓
sentence
```

只有超过 `ChunkSize` 的 piece 才继续使用更低一级 separator 拆分。

因此：

```text
完整段落
```

会优先保持完整。

## 7.2 splitUnit

内部中间模型：

```go
type splitUnit struct {
    text  string
    start int
    end   int
}
```

它仍然保存 source rune offset。

`splitUnit` 是非常关键的中间层：

```text
Source Text
   ↓
splitUnit
   ↓
Chunk
```

它让算法可以：

- 保留位置；
- 保护 code/table/math；
- 做 semantic overlap；
- 合并 Chunk；
- 保持 source mapping。

---

# 8. Overlap 到底作用在哪里

这是这个实现里非常容易误解的地方。

## 8.1 最准确的答案

> **Overlap 从效果上是 Chunk overlap；从算法实现上，是在 `mergeUnits()` 构造 Chunk 时对 `[]splitUnit` 计算和保留后缀。**

所以不是二选一：

```text
“对 Chunk”
vs
“对 splitUnit”
```

而是：

```text
语义层：
Chunk overlap

实现层：
splitUnit-based overlap
```

## 8.2 实际流程

假设已经累积：

```text
current units:

U1
U2
U3
U4
U5
```

此时：

```text
U1 + ... + U5 + next
```

会超过 ChunkSize。

算法先 flush 当前 Chunk：

```text
Chunk A =
U1 U2 U3 U4 U5
```

然后调用类似：

```go
computeOverlap(
    current,
    chunkOverlap,
    chunkSize,
    nextLen,
)
```

从：

```text
U1 U2 U3 U4 U5
```

里找一个合理 suffix。

例如最终选中：

```text
U4 U5
```

那么下一个 Chunk 开始时：

```text
Chunk B =
U4 U5 + U6 ...
```

所以：

```text
Chunk A: [ U1 U2 U3 U4 U5 ]
Chunk B:             [ U4 U5 U6 U7 ]
                     └─────┘
                      overlap
```

## 8.3 不是粗暴复制最后 N 字符

没有简单做：

```go
previousText[len(previousText)-80:]
```

因为这种方式可能切在：

```text
代码块中间
Markdown link 中间
数学公式中间
表格中间
半句话中间
UTF-8 中间
```

我们的实现是 semantic overlap。

## 8.4 overlap boundary priority

当前 overlap 寻找边界时优先：

```text
1. Paragraph break
2. Newline
3. Sentence ending
```

也就是说，如果目标是 overlap 80 rune：

不会强制：

```text
Exactly 80
```

而是：

> 尽量找到接近目标大小且语义合理的边界。

## 8.5 Protected-aware

如果目标位置落在：

```text
fenced code
math
Markdown link
Markdown table
```

中间，则不会直接从内部切开。

## 8.6 Synthetic Unit

Synthetic table header：

```text
start == end
text != ""
```

属于 hard barrier。

Overlap 不应该跨过它做不合理复制。

## 8.7 为什么设计成 unit-based

因为 `splitUnit` 已经同时拥有：

```text
Text
Source Position
Protected Structure
Semantic Boundary
```

所以它比：

```text
final Chunk string
```

更适合作为 overlap 的算法载体。

## 8.8 `ChunkOverlap=0` 的注意事项

当前实现有一个需要特别注意的配置语义。

底层：

```go
SplitText()
```

中：

```text
ChunkOverlap = 0
```

代表：

```text
真的关闭 overlap
```

但是 top-level：

```go
Split()
NormalizeSplitterConfig()
```

当前把：

```text
ChunkOverlap <= 0
```

视为：

```text
没有配置
```

因此会恢复默认：

```text
80
```

所以：

> 如果你调用顶层 Adaptive `Split()`，当前实现中 `0` 并不能表达“显式关闭 overlap”。

这是目前配置模型的一个已知语义限制。

未来如果需要严格区分：

```text
unset
vs
explicit zero
```

推荐改成：

```go
*int
```

或者增加：

```go
DisableOverlap bool
```

。

---

# 9. Protected Span

当前保护的结构包括：

```text
fenced code
inline code
block math
Markdown image
Markdown link
Markdown table row
Markdown table header/separator
```

核心思想：

```text
先识别结构
 ↓
把结构标成 protected ranges
 ↓
普通区域可以 recursive split
 ↓
protected 区域尽量 atomic
```

## 9.1 为什么需要 Protected Span

例如：

```markdown
[OpenAI documentation](https://example.com/very/long/url)
```

不能切成：

```text
[OpenAI documentation](https://exam
```

和：

```text
ple.com/very/long/url)
```

。

代码块同理。

## 9.2 Protected Span 过大怎么办

不能永远 atomic。

当前有 absolute protection upper bound。

如果一个 fenced code block 本身有数千 rune：

```text
> maxProtectedUnitSize
```

会强制切开。

但是会尽量在：

```text
newline
space
```

附近切。

因此：

```text
保护结构完整
```

优先级高，但：

```text
不能产生无限大 Chunk
```

优先级更高。

---

# 10. Markdown Table Header 处理

跨 Chunk 表格是传统 Recursive Splitter 很容易处理错的结构。

例如：

```markdown
| Name | Price |
| --- | --- |
| A | 1 |
| B | 2 |
| C | 3 |
| D | 4 |
```

如果中间切开：

```text
Chunk 1

| Name | Price |
| --- | --- |
| A | 1 |
| B | 2 |
```

Chunk 2 如果变成：

```text
| C | 3 |
| D | 4 |
```

LLM / Embedding 不知道列含义。

所以我们会恢复：

```text
Chunk 2

| Name | Price |
| --- | --- |
| C | 3 |
| D | 4 |
```

## 10.1 Synthetic Header

这个补出来的表头：

```text
不是 Source Text 的新范围
```

因此它的 source range 使用 synthetic semantics。

这也是：

```text
Content length
```

与：

```text
End - Start
```

偶尔不一致的主要来源之一。

---

# 11. Document Profiler

Auto Strategy 不应该盲猜。

先执行：

```go
ProfileDocument(text)
```

得到类似：

```go
type DocProfile struct {
    TotalChars int
    TotalLines int

    MdHeadingCounts map[int]int
    MdHeadingTotal int

    NumberedSectionCount int
    AllCapsShortLineCount int

    FormFeedCount int
    VisualSepCount int

    GermanChapterCount int
    EnglishChapterCount int
    ChineseChapterCount int

    HasTables bool
    HasCode bool
    CodeRatio float64

    DetectedLangs []string
}
```

## 11.1 Profiler 的用途

它不是为了做业务 analytics。

它只回答：

> “这个 Document 适合怎么切？”

例如：

```text
大量 Markdown headings
→ Heading Strategy
```

```text
没有 Markdown headings
但大量：
CHAPTER
1.1
第X章
PAGE BREAK
ALL CAPS
→ Heuristic
```

```text
都没有
→ Legacy
```

---

# 12. Validator

一个 Strategy 能产生 Chunks，不代表 Chunks 就是好的。

例如：

```text
一篇 10000 字文档
→ 只有1个 Chunk
```

显然不能接受。

Validator 目前检查：

1. 是否没有 Chunk；
2. 大文档是否只得到一个 Chunk；
3. tiny chunk 是否过多；
4. 所有 Chunk 是否远低于 target；
5. Chunk 是否超过 target 的 2 倍。

如果：

```text
Heading
```

结果不好：

```text
reject
 ↓
Heuristic
```

再失败：

```text
Legacy
```

。

---

# 13. Strategy Resolver

支持：

```go
StrategyAuto
StrategyHeading
StrategyHeuristic
StrategyRecursive
StrategyLegacy
```

## 13.1 Auto

典型链路：

```text
ProfileDocument
 ↓
根据文档特征选择：
 ↓
Heading?
 ↓
Heuristic?
 ↓
Legacy
```

每层都经过 Validator。

## 13.2 Heading explicit

```text
Heading
 ↓
Validator fail?
 ↓
Legacy
```

## 13.3 Heuristic explicit

```text
Heuristic
 ↓
Validator fail?
 ↓
Legacy
```

## 13.4 Legacy

直接：

```text
Recursive units
↓
Merge
↓
Overlap
```

---

# 14. Heading Splitter

用于：

```markdown
# Product

## Install

### Linux
...
```

核心逻辑：

```text
Profile
 ↓
DominantHeadingLevel
 ↓
Find Heading Boundaries
 ↓
Build Heading Hierarchy
 ↓
Section
 ↓
oversized?
 ├── no  → direct Chunk
 └── yes → Legacy Split inside Section
```

## 14.1 ContextHeader

例如当前位置：

```markdown
# Product
## Install
### Linux
```

则 Breadcrumb：

```text
# Product
## Install
### Linux
```

不会写进 Content，而是：

```go
ContextHeader
```

。

## 14.2 Heading Hierarchy

进入：

```text
# A
## B
### C
```

状态：

```text
H1=A
H2=B
H3=C
```

后面：

```text
## D
```

会清除：

```text
H3=C
```

变成：

```text
H1=A
H2=D
```

。

## 14.3 Fenced Code

代码块中的：

```text
# not heading
```

不能被误判为 Heading。

---

# 15. Heuristic Splitter

用于：

```text
CHAPTER 1

1. Introduction

SOME TITLE

----------------

Page 5 of 10
```

这种没有规范 Markdown Heading、但仍然存在明显结构的文档。

典型 marker：

```text
form feed
numbered section
chapter marker
ALL CAPS short heading
visual separator
page footer
large blank region
```

会按优先级产生 heuristic boundaries。

然后：

```text
boundaries
 ↓
protected-span filtering
 ↓
deduplicate same offset
 ↓
blocks
 ↓
greedy packing
 ↓
oversized block → Legacy Split
```

---

# 16. Parent-Child Chunking

核心思想：

> 小块负责找，大块负责读。

例如：

```text
Parent = 4096
Child  = 384
```

流程：

```text
Document
 ↓
Split(parentCfg)
 ↓
Parents
 ↓
对每个 Parent.Content 再调用 Split(childCfg)
 ↓
Children
```

## 16.1 Parent 不是机械大块

Parent 仍然使用完整 Strategy：

```text
Heading
Heuristic
Legacy
```

因此它尽量保持大结构完整。

## 16.2 Child 也不是强制 Recursive

Child 同样调用：

```go
Split(parent.Content, childCfg)
```

所以：

```text
child Strategy = auto
```

仍然可以在 Parent 内重新发现更深结构。

## 16.3 ParentIndex

Chunking 阶段：

```go
ChildChunk{
    ParentIndex: 2,
}
```

这里只是内存关系。

Application Service 会转换为：

```text
ParentChunkID
```

例如：

```text
doc-1#parent-000002
```

数据库不保存 ParentIndex。

## 16.4 冗余 Parent 删除

如果：

```text
Parent
 ↓
唯一 Child
```

并且：

```text
Parent.Content == Child.Content
```

则 Parent 没有任何上下文扩展价值。

最终：

```text
Parent 不保存
Child.ParentIndex = -1
```

。

## 16.5 Parent 与 Child 的数据库角色

Parent：

```text
chunks
✅

retrieval_index
❌

embedding
❌
```

Child：

```text
chunks
✅

retrieval_index
✅

embedding
✅
```

。

---

# 17. Eino Chunk Transformer

Eino Adapter：

```text
schema.Document
 ↓
internal/rag/chunker.Split()
 ↓
Chunk
 ↓
schema.Document[]
```

核心原则：

```text
Chunker Core
```

不 import Eino。

## 17.1 Metadata

Chunk Document 会增加：

```text
rag_source_document_id
rag_source_document_index
rag_chunk_index
rag_chunk_start
rag_chunk_end
rag_context_header
```

Loader 原 metadata 不会被覆盖，而是 clone 后继续保留。

## 17.2 Content 不使用 EmbeddingContent

不能：

```text
Document.Content =
ContextHeader + Content
```

因为它会污染权威正文。

应该：

```text
Document.Content =
Chunk.Content
```

检索表示由后续 SearchContent Builder 单独生成。

---

# 18. Tabula Loader

职责：

```text
File
 ↓
Tabula
 ↓
Markdown
 ↓
schema.Document
```

当前支持的主要格式：

```text
PDF
DOCX
ODT
XLSX
PPTX
HTML
HTM
EPUB
```

当前没有直接把：

```text
TXT
Markdown
```

绕进 Tabula。

这类纯文本格式可以未来增加一个简单 Loader。

## 18.1 为什么只调用 ToMarkdown

Tabula 自己也提供 Chunking 能力。

但这里刻意不用：

```go
tabula.Open(...).Chunks()
```

因为我们已经拥有自己的：

```text
Adaptive Chunker
```

如果使用 Tabula Chunking：

```text
File
 ↓
Tabula Chunk
 ↓
Our Chunk
```

会双重分块。

正确：

```text
File
 ↓
Tabula ToMarkdown()
 ↓
Our Chunker
```

。

## 18.2 一个文件一个 Source Document

目前：

```text
one file
=
one schema.Document
```

而不是：

```text
one PDF page
=
one schema.Document
```

原因是避免：

```text
物理分页
```

破坏：

```text
语义章节
```

。

---

# 19. SearchContent

业务原文：

```text
chunks.content
```

和检索表示：

```text
retrieval_index.search_content
```

分开。

默认：

```text
title

context header

body
```

例如：

```text
员工差旅制度

# 差旅制度
## 日本地区
### 酒店标准

东京地区酒店住宿标准为……
```

Embedding 与 BM25 都使用它。

## 19.1 为什么分离

以后想改：

```text
title
+
keywords
+
breadcrumb
+
body
```

只需要重建 Retrieval Index。

不用修改：

```text
chunks.content
```

。

---

# 20. PostgreSQL 数据模型

当前：

```text
documents
 └── chunks
      └── retrieval_index
```

## 20.1 documents

保存：

```text
collection_id
document id
title
完整 markdown
metadata
```

完整 Markdown 来自 Loader。

不能从 Chunk 反向拼回。

原因：

```text
overlap
synthetic table headers
```

都会导致：

```text
chunks != lossless partition
```

。

## 20.2 chunks

保存：

```text
id
document_id
chunk_type
chunk_index
content
context_header
start_rune
end_rune
parent_chunk_id
metadata
```

类型：

```text
text
parent_text
```

。

## 20.3 Parent / Child Seq

Parent 和 Child 的 Seq 是独立空间。

因此唯一索引必须包含：

```text
chunk_type
```

当前：

```text
collection_id
document_id
chunk_type
chunk_index
```

唯一。

## 20.4 retrieval_index

保存：

```text
search_content
embedding HALFVEC(1024)
enabled
metadata
```

只有真正参与 retrieval 的 Chunk 进入。

通常就是：

```text
Child / normal text chunk
```

。

---

# 21. Embedding

当前使用 Eino 官方 OpenAI-compatible Embedder。

接口：

```text
/v1/embeddings
```

所以可以连接：

```text
OpenAI-compatible API
vLLM
企业内部 Gateway
```

。

当前数据库固定：

```text
1024 dimensions
```

对应第一版 BGE-M3 Dense。

## 21.1 为什么固定 1024

第一版不支持：

```text
collection A → 768
collection B → 1024
collection C → 1536
```

因为这会大幅复杂化：

```text
schema
index
retriever
migration
```

。

先固定一个 model profile 更可靠。

## 21.2 Index 与 Query 必须同模型

Composition Root 会创建：

```text
one Embedder instance
```

并同时注入：

```text
Indexer
Retriever
```

从架构上避免：

```text
入库模型 A
查询模型 B
```

。

---

# 22. Dense Retrieval

PostgreSQL：

```sql
HALFVEC(1024)
```

HNSW：

```text
halfvec_cosine_ops
```

距离：

```text
embedding <=> query
```

最终 similarity：

```text
1 - cosine_distance
```

。

## 22.1 HNSW Query 形状

ANN 查询需要保持：

```text
ORDER BY embedding <=> query
LIMIT N
```

核心排序形状。

所以 Dense 查询先选 nearest rows，再 join chunks 获取正文。

---

# 23. BM25 Retrieval

使用 ParadeDB。

数据源：

```text
retrieval_index.search_content
```

而不是：

```text
chunks.content
```

。

原因是结构上下文也应该参与 lexical retrieval。

## 23.1 key_field

ParadeDB 搜索行需要独立 unique key。

所以 `retrieval_index` 中额外有：

```text
BIGINT IDENTITY id
```

。

它是数据库内部搜索 ID，不是业务 ChunkID。

---

# 24. Hybrid RRF

Vector 与 BM25 的 raw score 不在同一个空间。

例如：

```text
cosine:
0.85

BM25:
12.7
```

不能直接：

```text
0.7*0.85 + 0.3*12.7
```

。

所以用 Rank Fusion。

当前默认：

```text
k = 60

vector weight = 0.7

keyword weight = 0.3
```

公式：

```text
vectorWeight / (k + vectorRank)
+
keywordWeight / (k + keywordRank)
```

然后按理论最大值归一化。

## 24.1 Rank 必须先排序

不能把 Retriever 返回 slice 的偶然顺序直接视为 rank。

当前会：

```text
deduplicate
 ↓
score DESC
 ↓
assign rank
 ↓
RRF
```

。

---

# 25. Reranker

Rerank Core 自己定义：

```go
type Scorer interface {
    Score(
        ctx context.Context,
        query string,
        passages []string,
    ) ([]float64, error)
}
```

所以核心不依赖：

```text
Cohere
Jina
BGE
vLLM
OpenAI
```

。

## 25.1 Passage

默认：

```text
title

context_header

content
```

。

## 25.2 Threshold

先使用：

```text
model threshold
```

如果高阈值没有任何结果：

```text
max(
  threshold * 0.7,
  0.3,
)
```

降级一次。

如果仍然为空，但最高模型分：

```text
>= 0.15
```

保留 Top1。

## 25.3 Composite Score

当前：

```text
0.6 * modelScore
+
0.3 * baseRetrievalScore
+
0.1 * sourceWeight
```

。

其中：

```text
baseRetrievalScore
```

通常是 RRF Score。

## 25.4 Model Error

Reranker 是 quality enhancement。

不应该成为 single point of failure。

如果模型服务错误：

```text
RRF results
 ↓
reranker error
 ↓
保留 retrieval order
```

继续返回。

但是：

```text
context.Canceled
context.DeadlineExceeded
```

会正常向上传播。

---

# 26. Parent Context Expansion

顺序：

```text
Child Retrieval
 ↓
Child Rerank
 ↓
确定相关 Child
 ↓
Load Parent
 ↓
Final Context
```

而不是：

```text
Child Retrieval
 ↓
立即替换 Parent
 ↓
Rerank Parent
```

。

## 26.1 为什么

Child：

```text
Redis Sentinel deployment...
```

可能高度精准。

Parent：

```text
Linux
Docker
Redis
PostgreSQL
Monitoring
```

会稀释 reranker signal。

所以必须：

> 先判断小块是否相关，再扩大上下文。

## 26.2 SearchResult

仍保留：

```text
Content
=
matched child
```

同时：

```text
ContextContent
=
parent content
```

。

因此：

```go
result.Content
```

用于：

```text
citation
debug
retrieval evidence
```

。

而：

```go
result.EffectiveContent()
```

用于：

```text
LLM context
```

。

---

# 27. Application Service

对业务层暴露：

```go
Ingest(...)
Search(...)
```

。

上层不再需要自己操作：

```text
Tabula
Chunker
Indexer
Retriever
Reranker
ParentLoader
```

。

## 27.1 Ingest

```text
Source
 ↓
Loader
 ↓
Markdown
 ↓
Chunk
 ↓
Parent / Child
 ↓
IngestionBatch
 ↓
Store.ReplaceDocument
```

。

## 27.2 Replace semantics

不是：

```text
append chunks
```

而是：

> 当前 Batch 是 Document 的完整新版本。

所以：

```text
upsert document
 ↓
delete old chunks
 ↓
cascade delete old retrieval_index
 ↓
insert new parent / child
 ↓
insert new retrieval rows
```

。

否则修改 ChunkSize 后会留下 stale chunks。

---

# 28. Composition Root：NewRAG

最终运行时初始化：

```go
engine, err := rag.NewRAG(ctx, cfg)
```

它负责：

1. PostgreSQL extension bootstrap；
2. pgvector type registration；
3. Schema；
4. Embedding Provider；
5. PGIndexer；
6. Rerank Provider；
7. Hybrid Retriever；
8. Parent Loader；
9. Search Pipeline；
10. Tabula Loader；
11. Application Service。

最终只暴露：

```go
engine.Ingest(...)
engine.Search(...)
engine.Close()
```

。

---

# 29. 从零构建自己的 RAG

下面是一套推荐流程。

## 29.1 Go 依赖

示例：

```bash
go get github.com/cloudwego/eino
go get github.com/cloudwego/eino-ext/components/embedding/openai
go get github.com/tsawler/tabula
go get github.com/jackc/pgx/v5
go get github.com/pgvector/pgvector-go
go get github.com/pgvector/pgvector-go/pgx

go mod tidy
```

## 29.2 数据库

需要：

```text
PostgreSQL
pgvector
pg_search / ParadeDB
```

开发环境可以让：

```go
cfg.EnsureSchema = true
```

自动确保 extensions/schema。

生产环境推荐：

```go
cfg.EnsureSchema = false
```

由 migration / DBA 管理。

## 29.3 OCR 环境（可选）

如果需要扫描 PDF：

```bash
go build -tags ocr ./...
```

测试：

```bash
go test -tags ocr ./...
```

并安装：

```text
Tesseract
Leptonica
jbig2dec
openjpeg
poppler-utils（推荐）
```

。

## 29.4 Embedding 服务

如果使用 OpenAI-compatible endpoint：

```go
cfg.Embedding.BaseURL =
    "http://127.0.0.1:8001/v1"

cfg.Embedding.APIKey =
    "EMPTY"

cfg.Embedding.Model =
    "BAAI/bge-m3"

cfg.Embedding.Dimensions =
    1024
```

。

## 29.5 Reranker

```go
rerankProvider :=
    rerankhttp.DefaultConfig()

rerankProvider.Endpoint =
    "http://127.0.0.1:8002/v1/rerank"

rerankProvider.APIKey =
    "EMPTY"

rerankProvider.Model =
    "BAAI/bge-reranker-v2-m3"

cfg.RerankProvider =
    &rerankProvider
```

如果不需要 rerank：

```go
cfg.RerankProvider = nil
```

Hybrid Retrieval 仍然可以正常工作。

## 29.6 初始化

完整示例：

```go
ctx := context.Background()

cfg := rag.DefaultConfig()

cfg.DatabaseURL =
    "postgres://postgres:postgres@127.0.0.1:5432/rag?sslmode=disable"

cfg.EnsureSchema = true

cfg.Embedding.BaseURL =
    "http://127.0.0.1:8001/v1"

cfg.Embedding.APIKey =
    "EMPTY"

cfg.Embedding.Model =
    "BAAI/bge-m3"

cfg.Embedding.Dimensions =
    1024

cfg.Hybrid.TopK = 30
cfg.Hybrid.ChannelTopK = 30

cfg.Hybrid.VectorThreshold = 0.15
cfg.Hybrid.KeywordThreshold = 0.30

rerankProvider :=
    rerankhttp.DefaultConfig()

rerankProvider.Endpoint =
    "http://127.0.0.1:8002/v1/rerank"

rerankProvider.APIKey =
    "EMPTY"

rerankProvider.Model =
    "BAAI/bge-reranker-v2-m3"

cfg.RerankProvider =
    &rerankProvider

cfg.Rerank.TopK = 5
cfg.Rerank.Threshold = 0.3

cfg.Search.FinalTopK = 5
cfg.Search.ExpandParents = true

engine, err := rag.NewRAG(
    ctx,
    cfg,
)
if err != nil {
    log.Fatal(err)
}

defer engine.Close()
```

## 29.7 导入普通文档

```go
result, err := engine.Ingest(
    ctx,
    application.IngestRequest{
        CollectionID:
            "kb-demo",

        Source:
            document.Source{
                URI: "/data/manual.pdf",
            },

        Splitter:
            chunker.SplitterConfig{
                ChunkSize:
                    512,

                ChunkOverlap:
                    80,

                Strategy:
                    chunker.StrategyAuto,
            },
    },
)
```

。

## 29.8 推荐：Parent-Child

```go
result, err := engine.Ingest(
    ctx,
    application.IngestRequest{
        CollectionID:
            "kb-demo",

        Source:
            document.Source{
                URI: "/data/manual.pdf",
            },

        Splitter:
            chunker.SplitterConfig{
                ChunkSize:
                    512,

                ChunkOverlap:
                    80,

                Strategy:
                    chunker.StrategyAuto,

                TokenLimit:
                    512,

                Languages:
                    []string{
                        chunker.LangChinese,
                    },
            },

        ParentChild:
            true,

        ParentChunkSize:
            4096,

        ChildChunkSize:
            384,
    },
)
```

注意：

```text
ParentChunkSize
```

是阅读上下文粒度。

```text
ChildChunkSize
```

是检索粒度。

## 29.9 搜索

```go
response, err := engine.Search(
    ctx,
    application.SearchRequest{
        CollectionID:
            "kb-demo",

        Query:
            "Linux 环境如何安装？",
    },
)
if err != nil {
    log.Fatal(err)
}
```

读取：

```go
for _, item := range response.Results {
    fmt.Println(
        "matched child:",
        item.Content,
    )

    fmt.Println(
        "final context:",
        item.EffectiveContent(),
    )

    fmt.Println(
        "score:",
        item.Score,
    )
}
```

。

---

# 30. 模块级使用手册

你不一定必须使用 `NewRAG()`。

每一个模块都可以独立使用。

## 30.1 只使用 Chunker

```go
cfg := chunker.SplitterConfig{
    ChunkSize:    512,
    ChunkOverlap: 80,
    Strategy:     chunker.StrategyAuto,
}

chunks :=
    chunker.Split(
        markdown,
        cfg,
    )
```

适合：

```text
Chunk Preview
debug
本地测试
不使用 Eino 的应用
```

。

## 30.2 查看 Strategy Diagnostics

```go
chunks, diag :=
    chunker.SplitWithDiagnostics(
        markdown,
        cfg,
    )
```

可以查看：

```text
Profile
TierChain
SelectedTier
Rejected tiers
```

。

## 30.3 Parent-Child

```go
parentCfg, childCfg :=
    chunker.DeriveParentChildConfigs(
        baseCfg,
        4096,
        384,
    )

result :=
    chunker.SplitParentChild(
        markdown,
        parentCfg,
        childCfg,
    )
```

。

## 30.4 只使用 Loader

```go
loader :=
    tabula.NewLoader(
        tabula.DefaultConfig(),
    )

docs, err :=
    loader.Load(
        ctx,
        document.Source{
            URI: "/data/manual.pdf",
        },
    )
```

输出：

```text
[]*schema.Document
```

Content 是 Markdown。

## 30.5 只使用 SearchContent

```go
builder :=
    searchcontent.DefaultBuilder()

text :=
    builder.Build(doc)
```

适合：

```text
Embedding preview
BM25 debug
inspection
```

。

## 30.6 只使用 RRF

```go
fused :=
    retrieval.FuseRRF(
        vectorResults,
        keywordResults,
        retrieval.DefaultRRFConfig(),
    )
```

。

## 30.7 只使用 Reranker

```go
engine :=
    rerank.NewEngine(
        scorer,
        rerank.DefaultConfig(),
    )

result, err :=
    engine.Rerank(
        ctx,
        query,
        candidates,
    )
```

。

---

# 31. 关键配置参考

## Chunker

| 参数 | 默认/推荐 | 含义 |
|---|---:|---|
| ChunkSize | 512 | 普通目标 Chunk 大小 |
| ChunkOverlap | 80 | 语义 overlap 预算 |
| Strategy | 视调用方式 | auto / heading / heuristic / legacy |
| TokenLimit | 0 | 可选 embedding token budget |
| Separators | `\n\n`, `\n`, `。` | Recursive separator priority |

## Parent-Child

| 参数 | 推荐起点 |
|---|---:|
| ParentChunkSize | 4096 |
| ChildChunkSize | 384 |
| ChildOverlap | childSize / 5 |

Parent 不继承 `TokenLimit`。

Child 继承。

## Dense

| 参数 | 当前 |
|---|---:|
| Dimensions | 1024 |
| Type | halfvec |
| Metric | cosine |
| HNSW m | 16 |
| HNSW ef_construction | 64 |

## Hybrid

| 参数 | 当前起点 |
|---|---:|
| ChannelTopK | 30～50 |
| VectorThreshold | 0.15 |
| KeywordThreshold | 0.30 |
| RRF k | 60 |
| VectorWeight | 0.7 |
| KeywordWeight | 0.3 |

这些是起点，不是永恒最佳值。

## Rerank

| 参数 | 当前起点 |
|---|---:|
| Final TopK | 5 |
| Threshold | 0.3 |
| DegradeFactor | 0.7 |
| DegradeFloor | 0.3 |
| FallbackMinScore | 0.15 |
| ModelWeight | 0.6 |
| BaseWeight | 0.3 |
| SourceWeight | 0.1 |
| MMR Lambda | 0.7 |

---

# 32. 测试方式

Chunker：

```bash
go test ./internal/rag/chunker/... -v
```

Loader：

```bash
go test ./internal/rag/loader/tabula/... -v
```

OCR：

```bash
go test -tags ocr ./internal/rag/loader/tabula/... -v
```

Transformer：

```bash
go test ./internal/rag/transformer/chunker/... -v
```

Indexer：

```bash
go test ./internal/rag/indexer/postgres/... -v
```

Retriever：

```bash
go test ./internal/rag/retriever/postgres/... -v
```

Reranker：

```bash
go test ./internal/rag/rerank/... -v
```

Search：

```bash
go test ./internal/rag/search/... -v
```

全部：

```bash
go test ./internal/rag/... -v
```

带 OCR 全部：

```bash
go test -tags ocr ./internal/rag/... -v
```

---

# 33. 已知边界与当前未实现能力

目前已经是完整 RAG Foundation，但还不是完整知识库产品。

## 33.1 Images / VLM

还没有：

```text
assets table
image extraction pipeline
image storage
VLM caption
visual retrieval
multimodal answer
```

。

## 33.2 General OCR Asset Pipeline

OCR 目前主要是：

```text
Tabula scanned PDF fallback
```

还不是：

```text
generic image OCR service
```

。

## 33.3 Async Ingestion

当前：

```text
Ingest()
```

是同步 Application Service。

还没有：

```text
queue
worker
retry
job progress
task timeline
```

。

## 33.4 ACL / Tenant

目前核心只有：

```text
CollectionID
```

没有：

```text
User
Role
ACL
Document permission
Tenant isolation policy
```

。

## 33.5 Query Rewrite

目前 Query 直接进入：

```text
Dense + BM25
```

还没有：

```text
LLM query rewrite
HyDE
multi-query
keyword extraction
```

。

## 33.6 Metadata Filter

Retriever 当前没有完整通用 DSL。

后面可以加入：

```text
document type
source
date
tenant
tags
custom metadata
```

过滤。

## 33.7 Dynamic Embedding Dimension

目前固定：

```text
1024
```

。

## 33.8 BM25 Tokenizer Evaluation

当前第一版使用基础 tokenizer 配置。

对于：

```text
中文
日文
API identifiers
error codes
```

最终应该通过 Evaluation 决定是否采用：

```text
ICU
ngram
其他 analyzer
```

。

---

# 34. 推荐的工程演进方向

建议顺序：

```text
1. 先稳定当前 Core
2. 建真实 Integration Test
3. 建 Evaluation Dataset
4. Recall@K / MRR / NDCG
5. 调 Chunk / RRF / Threshold
6. 再加 Query Rewrite
7. 再加 Image / VLM
8. 再加异步任务
9. 再加 ACL / 多租户
10. 再扩展成完整知识库产品
```

不要优先：

```text
Agent
Memory
GraphRAG
MCP
```

而基础 retrieval quality 还没有测量。

---

# 35. FAQ

## Q1：现在到底支持 OCR 吗？

支持。

但必须：

```bash
-tags ocr
```

并安装 Tesseract 等 native dependencies。

它目前是 Tabula 对扫描 PDF 的自动 fallback。

## Q2：不开 OCR tag 会怎样？

Native PDF：

```text
正常
```

Scanned-only PDF：

```text
没有 native text
→ 可能提取为空
```

如果整个 Markdown 为空，我们的 Loader 会返回：

```text
ErrEmptyContent
```

避免静默建立一个空知识文档。

对于“部分页面 native text + 部分页面扫描”的混合 PDF，建议部署环境统一启用 OCR，否则扫描页内容可能缺失。

## Q3：OCR 会识别架构图的含义吗？

不会。

它主要识别：

```text
字符
```

不是：

```text
视觉语义
```

架构图、流程图应该用 VLM。

## Q4：Overlap 是对 Chunk 还是 splitUnit？

两者都可以说，但层级不同：

```text
业务语义：
Chunk overlap

算法实现：
splitUnit suffix reuse
```

。

最终确实得到：

```text
Chunk A
       └──── overlap ────┐
                         ↓
                    Chunk B
```

但是 overlap 不是对最终 Chunk 字符串做粗暴 substring，而是在 merge 阶段通过 splitUnit 选择语义合理的 suffix。

## Q5：Overlap 会严格等于 80 字吗？

不会。

80 是：

```text
budget / target
```

实际会尽量选择：

```text
paragraph
newline
sentence
```

边界。

因此可能：

```text
62
74
91
```

而不是强制80。

## Q6：为什么不用最后80个 rune？

因为可能破坏：

```text
代码
数学公式
Markdown Link
表格
句子
```

。

## Q7：为什么 Parent 不 Embedding？

因为 Parent 可能很大且主题更多。

它用于：

```text
LLM reading context
```

而不是：

```text
retrieval precision
```

。

## Q8：为什么 Child Rerank 后才拿 Parent？

因为小 Child 的语义更集中。

先替换 Parent 会让 Reranker 再次面对大量无关上下文。

## Q9：为什么 SearchContent 不直接覆盖 Content？

因为：

```text
Content
=
业务事实

SearchContent
=
检索 representation
```

它们有不同生命周期。

## Q10：如果不想用 Eino 可以吗？

Chunker、RRF、Reranker、SearchResult 等核心都可以独立使用。

Eino 目前主要作为：

```text
Loader / Transformer / Embedding 组件协议
```

之一。

## Q11：如果不想用 Parent-Child 呢？

直接：

```go
ParentChild: false
```

使用普通 Chunk。

## Q12：应该默认使用 Auto Strategy 吗？

对于未知来源文档，推荐：

```go
StrategyAuto
```

因为它可以：

```text
Markdown → Heading
弱结构文档 → Heuristic
普通文本 → Legacy
```

。

如果你明确知道输入已经高度规范，也可以显式指定 Strategy。

---

# 最后的系统认知

这个项目最重要的并不是某一个具体参数：

```text
512
384
4096
0.7 / 0.3
```

而是建立了下面这些稳定边界：

```text
Parser ≠ Chunker

Content ≠ SearchContent

Chunk ≠ ContextHeader

Retrieval Chunk ≠ Reading Context

Dense Score ≠ BM25 Score

Recall ≠ Rerank

Core ≠ Framework Adapter

Application ≠ Infrastructure
```

当这些边界稳定以后，未来替换：

```text
Tabula
Embedding Model
PostgreSQL
Reranker
BM25 implementation
```

都不需要推翻整个 RAG 系统。

这才是这个 RAG Foundation 真正应该提供的价值。
