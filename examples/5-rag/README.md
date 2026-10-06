# 真实文档 RAG 配置与评测示例

这个示例从你传入的真实文件开始，完成解析、分块、向量化、入库和检索。你先阅读实际分块并标注相关子块，程序再用同一问题对比混合、向量、关键词检索，打印 Recall、Precision 和执行诊断。目前只评测检索，不调用 LLM 生成答案，也不接入 Agent。

所有运行代码都集中在 [main.go](main.go) 中，包含完整的中文注释。调参统一修改 `exampleConfig`，使用现有组件的配置和 `rag.IngestRequest`。

| main.go 中的区域 | 阅读用途 |
| --- | --- |
| `main` | 按顺序学习真实文件导入、人工标注、三种检索与结果汇总 |
| `exampleConfig` | 修改解析、分块、召回、精排和上下文开关 |
| `postgresConfig / Validate` | 理解示例配置结构及参数约束 |
| `newPostgresRAG / EmbeddingProfile` | 组装 Eino 组件，绑定模型档案和文档生命周期 |
| `evidenceIDs / evaluate` | 按去重子块证据计算 Recall、Precision |
| `printSettings / printSearch` | 展示配置意图、实际诊断、命中证据和上下文 |

现有测试集中在 `main_test.go`；运行示例只需要 `main.go`。

## 1. 运行

先准备数据库：当前 PostgreSQL 适配器的代码要求 PostgreSQL 15+、pgvector 0.7+、pg_search 0.25+。数据库需要提前创建，服务器上需要安装扩展文件；`EnsureSchema=true` 只负责执行创建扩展、表和索引的 SQL，连接用户需要具备对应权限。

示例读取环境变量，不读取项目的 `.env` 文件，也不读取桌面应用的模型设置：

```bash
cd /Users/sda1_hacker/Desktop/humbert/humbert-agent

export RAG_DATABASE_URL='postgres://postgres:postgres@127.0.0.1:5432/rag_example?sslmode=disable'
export OPENAI_API_KEY='你的向量模型服务密钥'
export OPEN_BASE_URL='你的 OpenAI 兼容 API 基础地址'
export RAG_EMBEDDING_MODEL='服务实际支持且输出 1024 维的模型名'

go run ./examples/5-rag/main.go '/绝对路径/你的文档.pdf'
```

也可使用 `go run ./examples/5-rag` 运行同一个示例。

支持本地 PDF、DOCX、ODT、XLSX、PPTX、HTML、EPUB、MD、TXT、CSV 文件。传入路径中有空格时加引号。使用 `go run ./examples/5-rag --help` 可以先查看入口，不连接数据库或模型。

`OPEN_BASE_URL` 是 API 基础地址，客户端会拼接 embeddings 请求路径。未填时使用底层客户端的默认 OpenAI 地址。模型名默认沿用原示例的 `qwen3.7-text-embedding`，不代表你的服务一定支持它，建议显式填写环境变量。

当前数据库列固定为 `halfvec(1024)`，模型必须实际返回 1024 维。`Dimensions=0` 只是不在请求中发送维度参数；若模型支持指定维度，可以在配置中改成 `1024`。它不会让数据库自动适配其他维度。

运行后先看到实际分块，再输入类似下面的内容：

```text
请输入针对该文档的问题：试用期员工如何申请请假？
请输入所有相关子块编号，用空格分隔：2 3 7
```

这里的问题和编号必须由你的真实文档决定。不要先看检索结果再挑相关编号；重叠分块可能同时包含答案，应把所有相关子块都标上。

默认知识库 ID 是 `rag-file-example`，文档 ID 是 `uploaded-document`。每次重跑替换这个演示文档，不会自动累计多个文件。请使用单独的实验知识库，避免其他文档参与召回而没有对应人工标注。

## 2. 配置在哪里生效

导入流程：

```text
cfg.Loader
  → 文件解析为完整 Markdown
  → ingest.Splitter / ParentChild / ParentChunkSize / ChildChunkSize
  → 生成普通块或父子块
  → cfg.InputBuilder 组合标题、标题路径和正文
  → cfg.Embedding + cfg.Indexer 生成向量并写入数据库
```

查询流程：

```text
SearchRequest.Mode + CollectionID + Query
  → semantic：向量；keyword：BM25；hybrid：两路召回并执行 RRF
  → 可选精排和模型分数过滤
  → 可选父块上下文扩展
  → 可选同父块合并，保留子块 Evidence
  → FinalTopK 截断
  → 输出上下文、诊断和评测指标
```

选择检索模式，和启用精排、父块扩展，是不同的事情。例如 `Mode="keyword"` 仍可以在 BM25 召回之后执行精排和父块扩展。

### 解析与模型

| 配置 | 示例值 | 实际作用 |
| --- | --- | --- |
| `Loader.ExcludeHeadersAndFooters` | `true` | 解析时过滤重复页眉页脚；改它，不改 `ParseOptions` 中的同名字段，Loader 会覆盖后者 |
| `Loader.NormalizeLineEndings` | `true` | Loader 统一换行；Service 导入也会规范化，最终坐标始终以规范化正文为准 |
| `Loader.ParseOptions.Timeout` | 1 分钟 | 文件解析子进程超时 |
| `MaxInputBytes / MaxOutputBytes` | 各 32 MiB | 原文件与解析结果的大小上限；0 使用默认限制，不是无限制 |
| `MaxExpandedBytes / MaxArchiveEntries` | 256 MiB / 8192 | 压缩文档展开资源限制 |
| `Loader.ParseOptions.IncludePageNumbers` | `false` | 是否在解析结果中加入页码标记，不提供原文件坐标映射 |
| `Loader.OCRLanguage` | 空字符串 | OCR 语言提示，普通构建没有 OCR 能力；不是启用开关 |
| `Embedding.Model / BaseURL / ModelRevision` | 按实际服务填写 | 模型与版本标识；已绑定的知识库不能混用其他模型配置 |
| `Embedding.Dimensions` | `0` | 是否向服务请求指定维度，实际结果仍检查 1024 维 |
| `Embedding.Timeout` | 30 秒 | 向量服务请求超时 |
| `Embedding.InputBudget` | 8192 / 65536 | 单条完整输入与整批输入上限，不截断原文；需按实际模型调整 |
| `Indexer.EmbeddingBatchSize` | `32` | 每批最多多少条向量输入，预算检查可以进一步缩小批次 |
| `InputBuilder` | 标题 + 标题路径 + 正文 | 统一向量/BM25 的索引文本；精排默认也使用这类组合 |

`InputBudget.CountTokens=nil` 时按 UTF-8 字节数保守估算，不是模型的精确 tokenizer。中文字符通常占多个字节。可以提供真实模型计数函数。此示例统一配置 `Embedding.InputBudget`，组装时同步给索引器；不要再设置 `Indexer.InputBudget`，避免索引与查询预算不同。

扫描文档需要安装 Tesseract 及语言包、设置 `Loader.OCRLanguage="chi_sim+eng"`，再使用 `go run -tags ocr ./examples/5-rag '/路径/扫描件.pdf'`。解析器按实际文件情况决定是否调用 OCR；不能仅凭语言字段判断 OCR 已执行。示例会打印解析器元数据报告的 OCR 状态和解析警告。

### 分块策略

在 `ingest.Splitter.Strategy` 中选择：

| 值 | 行为 |
| --- | --- |
| `chunker.StrategyAuto` | 分析标题和章节信号，选择策略，必要时回退 |
| `chunker.StrategyHeading` | 优先按 Markdown 标题层次切分，校验不通过时回退递归切分 |
| `chunker.StrategyHeuristic` | 优先识别中英文等章节标记，校验不通过时回退递归切分 |
| `chunker.StrategyRecursive` | 递归文本切分 |
| `chunker.StrategyLegacy` | 与 `recursive` 使用同一算法 |
| 空字符串 | 使用 `legacy`，不是自动模式 |

程序打印配置策略和实际选择策略。`heading` 配置最终显示 `legacy` 可能是质量校验后的回退，拒绝原因也会打印。父子模式的导入诊断只描述父块策略，子块在各父块内独立选择策略。

| 配置 | 普通模式 `ParentChild=false` | 父子模式 `ParentChild=true` |
| --- | --- | --- |
| `Splitter.ChunkSize` | 普通块目标字符数，示例 512 | 实际大小改用下面两个字段 |
| `ParentChunkSize` | 不使用 | 父块目标大小，示例 2048 |
| `ChildChunkSize` | 不使用 | 子块目标大小，示例 384 |
| `Splitter.ChunkOverlap` | 最大重叠字符数，示例 80 | 父块使用此值；子块重叠为 `ChildChunkSize/5`；此值为 0 时两者都关闭重叠 |
| `Splitter.TokenLimit` | 近似 token 目标，0 不加该目标 | 只作用于子块，父块不受这个目标约束 |
| `Splitter.Separators` | 递归切分分隔符优先级 | 父子递归切分也使用 |
| `Splitter.Languages` | 如 `[]string{"zh"}` 的语言提示 | 父子切分共享语言提示 |

目标大小按 Unicode 字符计算，不是字节数，也不是精确 token 数。重叠会被限制到块大小的一半以内。表格、代码等受保护内容可能超过字符目标；完整向量输入仍要通过 `Embedding.InputBudget` 检查。`TokenLimit` 不等于向量模型请求的输入上限。

`Languages` 是切分提示，不是 BM25 分词器配置。留空时启发式切分尝试全部支持的章节规则，近似 token 预算使用混合语言估算；自动策略的文档分析会另外检测语言信号。

父块只存储上下文，子块才参与向量/BM25 索引和人工标注。如果一个父块只产生完全相同的一块子块，组件省略重复父块，所以开启父子模式也可能得到 0 个独立父块。

### 检索、候选数与融合

`main.go` 中 `exampleConfig` 最后面的 `modes` 决定运行哪些实验：

```go
// 同一个问题对比三种召回方式。
modes := []string{"hybrid", "semantic", "keyword"}

// 只想跑混合检索时，将上面的赋值改成这一行。
// modes := []string{"hybrid"}
```

| 配置 | 示例值 | 实际作用 |
| --- | --- | --- |
| `Hybrid.TopK` | `30` | 本示例同时用它配置 Service 的 `RecallTopK`，包括单路查询候选池 |
| `Hybrid.ChannelTopK` | `50` | 混合检索每路候选数，实际为 `max(ChannelTopK, 本次候选池)` |
| `Vector.ScoreThreshold` | `0` | 向量原始相似度过滤，参数范围 0～1 |
| `Keyword.ScoreThreshold` | `0` | BM25 原始分数过滤，非负；与向量阈值不是同一量纲 |
| `Hybrid.RRF.VectorWeight / KeywordWeight` | `0.7 / 0.3` | 按排名做加权 RRF，不直接相加两路原始分数 |
| `Hybrid.RRF.K` | `60` | RRF 平滑常数，越大越弱化名次差异 |
| `Hybrid.FailurePolicy` | `FailureStrict` | 单路出错即报错；`FailureAllowPartial` 可保留成功通道 |
| `Hybrid.Timeout / ChannelTimeout` | 20 秒 / 15 秒 | 整体混合召回和单路召回超时 |

数量关系需要特别注意：

```text
本次最终上限 = SearchRequest.Limit（>0 时）或 Search.FinalTopK
本次候选池   = max(Hybrid.TopK, 本次最终上限)
混合每路召回 = max(Hybrid.ChannelTopK, 本次候选池)

示例默认：每路最多 50 → 融合最多 30 → 精排/扩展/合并 → 最终最多 5 条上下文
单路默认：召回最多 30 → 精排/扩展/合并 → 最终最多 5 条上下文
```

这是 **main.go 中 newPostgresRAG 的组装方式**：Service 调用原生 Retriever 时传入 `WithTopK`，因此修改 `cfg.Vector.TopK` 或 `cfg.Keyword.TopK` 不会改变这个业务入口的候选数。这两个字段用于独立调用原生组件；本示例调候选数改 `cfg.Hybrid.TopK` 即可。PostgreSQL 组件候选上限是 200，示例还校验最终数量不能超过配置候选池。

某一路 RRF 权重改成 0 可以关闭混合检索的该通道。只有整个 `RRFConfig{}` 都为零才恢复默认；保留 K=60 而把两路权重都设成 0 会校验失败。单独的 `semantic`、`keyword` 模式仍能运行，导入仍然生成向量。关闭查询通道不等于停建索引。

`FailureAllowPartial` 允许普通错误或独立通道超时后使用另一条成功通道；整体超时或取消仍失败。通道成功但返回空结果不等于发生错误降级。程序打印 `ModeUsed`、`Degraded` 和通道错误信息。

### 精排开关

找到 `enableRerank := false`，改成 `true`，再提供：

```bash
export RERANK_ENDPOINT='你的精排服务完整请求地址'
export RERANK_MODEL='你的精排模型名'
export RERANK_API_KEY='你的精排服务密钥'
```

Endpoint 要填写完整地址，协议兼容当前 HTTP 适配器使用的 Jina/Cohere 请求和响应格式。是否需要密钥由服务决定。只有 `RerankProvider != nil` 且 `Rerank.Disabled=false`，此示例才启用模型精排；仅改 `Disabled` 不够。

| 配置 | 示例值 | 实际作用 |
| --- | --- | --- |
| `Rerank.MaxCandidates` | `30` | 按召回顺序截取送给模型的候选数；增加候选池时也要检查这里 |
| `Rerank.Threshold` | `0.3` | 模型相关性分数过滤，不是最终融合分数过滤 |
| `DegradeFactor / DegradeFloor` | `0.7 / 0.15` | 高阈值没有结果时降低一次：`max(0.3*0.7, 0.15)=0.21` |
| `FallbackMinScore` | `0.1` | 降低阈值仍无结果时，最高模型分达到此值则保留一条，否则返回空结果 |
| `ModelWeight / BaseWeight / SourceWeight` | `0.8 / 0.2 / 0` | 过滤之后的排序分权重；权重总和不能大于 1 |
| `RerankProvider.Timeout` | 15 秒 | 精排 HTTP 请求超时 |

默认精排正文构造使用标题、标题路径和子块正文，需要特殊输入格式时可以设置 `Rerank.PassageBuilder`，普通调参不必设置。模型普通错误回退召回结果，诊断会显示 `model_error`；取消/超时直接报错。看 `Applied` 和 `Outcome` 判断本次是否实际应用精排：`disabled` 表示关闭，`ok` 表示正常执行，`threshold_degraded` 表示阈值降级，`all_below_threshold` 表示模型过滤后无结果。

**当前 Service.Search 不执行 MMR，也没有 Query Rewrite。** `Rerank.TopK` 和 `MMRLambda` 属于独立 `Engine.Rerank` 的最终选择；业务查询调用 `RerankCandidates` 保留候选池，最终数量由 `Search.FinalTopK` 控制。修改 `MMRLambda` 不会开启本示例中的 MMR，填入类似 Query Rewrite 的自定义字段也不会生效。

### 父块扩展与最终上下文

| 配置 | 示例值 | 实际作用 |
| --- | --- | --- |
| `Search.ExpandParents` | `true` | 命中子块有父块时，将父块正文作为最终上下文 |
| `Search.CollapseSameParent` | `true` | 相同父块合为一条，保留各子块的 `Evidence` |
| `Search.AllowParentFallback` | `false` | 普通父块读取错误时是否回退子块，取消和超时始终报错；缺失父块会保留子块 |
| `Search.FinalTopK` | `5` | 扩展和合并后最多返回多少条上下文，不是固定返回 5 条 |
| `Search.Timeout` | 30 秒 | 整个查询，包括模型档案检查、召回、精排和父块读取 |
| `SearchRequest.Limit` | `0` | 本次请求最终数量覆盖；0 使用 `FinalTopK` |

`ParentChild` 是导入开关，`ExpandParents` 是查询开关，两者相互独立。关闭扩展仍可以检索子块；普通分块没有父块，开启扩展也不会凭空生成父块。关闭扩展时，同父块合并也不会把不同子块当成同一上下文合并。

`result.Content` 始终是原始命中子块，`result.EffectiveContent()` 才是实际最终上下文，可能为父块。示例分别打印证据编号和最终上下文，并根据上下文 ID 与命中 ID 是否不同显示“父块实际扩展”。坐标是解析、规范化后的 Markdown 字符区间，不是原 PDF 文件的字节范围或页码。

## 3. 从简单配置逐步调整

修改 `main.go` 的 `exampleConfig` 中对应字段的已有赋值，避免在前面新增赋值后又被后面的代码覆盖。

第一轮可以用普通递归分块，关闭父块和精排，观察最直接的召回：

```go
// 普通块大小由 ChunkSize 控制。
ingest.ParentChild = false
ingest.Splitter.Strategy = chunker.StrategyRecursive
ingest.Splitter.ChunkSize = 512
ingest.Splitter.ChunkOverlap = 80
// enableRerank 保持 false。
cfg.Search.ExpandParents = false
cfg.Search.CollapseSameParent = false
```

第二轮只把策略改成 `chunker.StrategyAuto` 或 `chunker.StrategyHeading`，重新导入并标注，观察实际策略、分块边界和召回指标。

第三轮启用父子分块，并比较是否扩展/合并上下文：

```go
// 父子模式下不再由 Splitter.ChunkSize 控制这两个窗口。
ingest.ParentChild = true
ingest.ParentChunkSize = 2048
ingest.ChildChunkSize = 384
cfg.Search.ExpandParents = true
cfg.Search.CollapseSameParent = true
```

第四轮在同一份标注下调整 `Hybrid.TopK`、`ChannelTopK`、RRF 权重、阈值或精排开关。每次尽量只改一个因素，才能判断指标变化来自哪里。

| 改动 | 是否需要重导入/重标注 |
| --- | --- |
| 解析规则、分块策略、大小、重叠、父子模式、TokenLimit | 需要重导入，重新检查并标注相关子块 |
| Embedding 模型、地址、版本、检索文本构造规则 | 使用新的 CollectionID 并重导入，避免模型档案冲突和向量混用 |
| 召回模式、候选数、分数阈值、RRF、精排、父块扩展开关 | 配置本身不要求重建索引；同一份分块可继续评测 |
| 请求 Limit 或 FinalTopK | 不要求重建索引，改变最终上下文数量 |

为保持示例简单，程序每次启动仍会导入文件一次，没有单独的“只查询”子命令。仅改检索参数时，如果原文件和切分参数没变，可以使用相同的相关块编号，但要核对打印出的分块。程序会把本次编号转换为本次版本的真实 ID。

## 4. 指标口径

```text
G = 人工标注的相关子块 ID 集合
R = 最终返回结果中的所有命中子块 ID 集合，去重
    包括 result.ChunkID 及同父块合并保留的 result.Evidence

Recall    = |R ∩ G| / |G|
Precision = |R ∩ G| / |R|
```

例如标注了 4 个相关子块，实际返回 5 个不同的子块证据，其中 3 个相关：Recall 为 75%，Precision 为 60%。没有返回证据时两个指标记为 0；检索失败单独报告，不计算成零分。

开启同父块合并时，5 条上下文可能保留超过 5 个子块证据，所以示例同时报告上下文数和证据数，不把 Precision 的分母写成上下文数。父块正文里虽然包含其他相关段落，但没有对应的命中子块证据，就不算额外召回。

关闭合并时，一条结果一般对应一个子块，指标就是常见的子块 Recall@K / Precision@K。开启合并后的指标属于“最终上下文中命中子块证据的召回率与准确率”。父块扩展本身不改变命中身份，因此同样的结果扩展前后指标可能相同；要评测上下文是否足够回答问题，需要后续增加答案质量评测。

最后汇总同时显示三种模式的上下文数、证据数、命中数、Recall、Precision 和耗时。每次仅评测你输入的一个问题；它能用于观察参数影响，不能替代多问题评测集。

## 5. 验证

```bash
go test ./examples/5-rag ./internal/rag/...
```

指标测试覆盖父块合并、重复证据、扩展正文不凭空增加命中，以及空结果。纯 Go 测试不代表真实数据库和模型已经连通，完整流程需要使用上面的真实文档运行命令。
