# Humbert Agent RAG 源码详解

> 阅读基准：2026-10-06 工作区中的当前实现，包括尚未提交的文件。
> 项目根目录：`/Users/sda1_hacker/Desktop/humbert/humbert-agent`。
> 范围：`internal/rag` 的全部 57 个生产 Go 文件，以及它依赖的文档解析器、迁移命令、真实文档示例和测试契约。
> 本文解释代码实际执行的行为；“已经实现”“仅可独立调用”“尚未实现”分别标明，不把未来计划当成现有能力。

## 阅读导航

1. [模块范围与设计目标](#scope)
2. [目录结构与组件接口](#architecture)
3. [数据模型、身份和坐标](#models)
4. [启动组装与资源管理](#assembly)
5. [文档导入主流程](#ingest)
6. [真实文件解析与子进程](#loader)
7. [分块配置、文档画像与策略选择](#strategy)
8. [递归分块、保护区域、表头与重叠](#recursive)
9. [标题分块与标题路径](#heading)
10. [启发式章节分块](#heuristic)
11. [近似 token 目标与父子分块](#parent-child)
12. [Eino Transformer 与统一检索文本](#representation)
13. [Embedding 输入预算与模型档案](#embedding)
14. [数据库结构、索引和迁移](#database)
15. [PostgreSQL 索引写入与原子事务](#indexing)
16. [版本预留、取消、删除与幂等](#lifecycle)
17. [Search 入口与 Eino 适配](#search)
18. [向量和 BM25 检索](#retrievers)
19. [并发混合检索与 RRF](#hybrid)
20. [精排模型、阈值与评分融合](#rerank)
21. [独立 MMR 的具体实现](#mmr)
22. [父块上下文、分组与引用证据](#context)
23. [替换后端与外部索引发布](#backends)
24. [配置生效规则与完整示例](#configuration)
25. [Recall、Precision 与评测边界](#evaluation)
26. [测试、异常定位和实现限制](#verification)
27. [逐文件、逐函数源码索引](#source-index)

<a id="scope"></a>

## 1. 模块范围与设计目标

### 1.1 当前 RAG 到哪一步

当前模块完成的是“把文档变成可检索证据，再返回可用上下文”。默认入口是 `rag.Service`，它提供 `Ingest`、`Search`、`ListChunks`、`CancelDocument`、`DeleteDocument` 和 `Close`。

`Search` 的最终输出是检索结果、分数、出处、上下文和执行诊断，当前流程没有把上下文送给聊天模型生成答案。项目中的 Agent、Wails 和 Vue 也没有在当前代码中调用这套 RAG 服务。因此，理解本文时，应把“检索底座”和“之后的问答业务”分开。

| 能力 | 当前状态 | 实现位置 |
| --- | --- | --- |
| 本地真实文档解析为 Markdown | 已实现 | `loader/tabula`、`internal/documentparse` |
| 递归、标题、启发式、自动分块 | 已实现 | `chunker` |
| 父子分块、标题路径、表头补充、字符坐标 | 已实现 | `chunker`、`service.go` |
| Embedding、批次和输入预算 | 已实现 | `provider/embedding/openai`、`embeddinginput` |
| PostgreSQL 向量和 BM25 索引 | 已实现 | `indexer/postgres` |
| 向量、关键词、混合召回与 RRF | 已实现 | `retriever`、`retrieval/rrf.go` |
| 模型精排、阈值降级、错误回退 | 已实现，可配置 | `rerank`、`provider/rerank/http` |
| 父块扩展、相同上下文合并、Evidence | 已实现，可配置 | `search/pipeline.go` |
| 原文发布、版本过滤、取消、删除 | 已实现 | 根包的业务接口与 PostgreSQL 实现 |
| 外部向量库与外部关键词库组合 | 组合能力已实现，具体后端需自行注入 | `indexer/multi.go`、`published.go` |
| MMR | 算法已实现，仅独立 `Engine.Rerank` 路径使用 | `rerank/reranker.go` |
| Query Rewrite | 尚未实现 | 无配置项和执行阶段 |
| 邻居块扩展、GraphRAG、Wiki 生成 | 尚未实现 | 无对应流程 |
| RAG REST/SSE、知识库管理 UI、Agent 工具 | 尚未接入 | 当前模块没有这些业务入口 |

### 1.2 三条贯穿整个实现的设计约定

第一，组件边界使用 Eino 原生接口。索引器实现 `indexer.Indexer`，检索器实现 `retriever.Retriever`，文档转换器实现 `document.Transformer`。项目没有为向量库再创造一套与 Eino 平行的 Store/Search 接口。

第二，文档业务与检索索引分开。Eino 规定如何存储或召回 `schema.Document`，没有规定整篇原文、父块、导入版本和发布状态。项目用独立业务能力补齐这些事项，不能假设换上一个原生 Indexer 就自动拥有文档管理。

第三，原文证据与检索表示分开。标题和标题路径能够提高召回效果，但它们不是原文里新增的字符；父块能提供更完整上下文，也不能替换掉真正命中的子块身份。这一约定决定了正文、坐标、ID、Metadata 和评测指标的组织方式。

<a id="architecture"></a>

## 2. 目录结构与组件接口

### 2.1 分层

```text
internal/rag/
├── rag.go / service.go        业务服务、依赖注入、导入和查询编排
├── indexing.go               批次传递、统一索引文本、版本化 ID
├── persistence.go            原文、发布、生命周期和权威回查接口
├── published.go              原生 Retriever 的发布状态过滤包装
├── validation.go             文档批次及父子关系校验
├── loader/tabula/            本地真实文档 Loader
├── chunker/                  无数据库依赖的文本切分算法
├── transformer/chunker/      将切分算法适配成 Eino Transformer
├── searchcontent/            统一标题、标题路径、正文组合规则
├── embeddinginput/           输入预算和向量空间档案
├── provider/embedding/openai/ OpenAI 兼容 Embedder 适配
├── provider/rerank/http/     精排 HTTP 协议适配
├── indexer/multi.go          组合多个 Eino Indexer
├── indexer/postgres/         原文、分块、两路索引、事务和版本管理
├── retriever/hybrid.go       与具体后端无关的两路并发召回
├── retriever/postgres/       pgvector、BM25 和父块读取
├── retrieval/               检索结果、元数据转换、诊断与 RRF
├── rerank/                  精排、评分融合及独立 MMR
└── search/                  召回后的统一处理流水线

internal/documentparse/       RAG 与其他文档功能共用的解析子进程
examples/5-rag/               PostgreSQL 具体组装、参数实验和人工评测
cmd/rag-migrate/              数据库扩展安装与版本迁移命令
```

### 2.2 依赖方向

```mermaid
flowchart TD
    Example["示例或未来业务启动代码"] --> Service["rag.Service"]
    Example --> Concrete["PostgreSQL / Tabula / 模型适配器"]
    Concrete -. "实现" .-> Eino["Eino Loader / Indexer / Retriever"]
    Service --> Eino
    Service --> Chunker["纯切分算法"]
    Service --> Pipeline["search.Pipeline"]
    Pipeline --> Result["retrieval.SearchResult"]
    Pipeline --> Rerank["rerank.Engine"]
    Service --> Contracts["原文 / 发布 / 生命周期业务契约"]
    Concrete -. "实现" .-> Contracts
```

根包不直接导入 PostgreSQL、Tabula 或某个模型实现。具体组合放在 `examples/5-rag/main.go`。增加后端时通常更换注入的组件，而不是创建新的 `rag.NewXXX` 或第二个 RAG Service。

### 2.3 Eino 原生接口的作用

当前项目 `go.mod` 使用 Eino `v0.9.19`。接口形状可概括为：

```go
// 这里只摘出理解本项目所需的接口形状。
type Indexer interface {
    Store(ctx context.Context, docs []*schema.Document, opts ...indexer.Option) ([]string, error)
}
type Retriever interface {
    Retrieve(ctx context.Context, query string, opts ...retriever.Option) ([]*schema.Document, error)
}
```

文档加载和转换对应 `Loader.Load` 与 `Transformer.Transform`。Eino 的 `schema.Document` 提供 `ID`、`Content`、`MetaData`；相关性分数通过 `WithScore` 写入，通过 `Score()` 读取。

项目使用两类 Option：公共 Option 如 `WithIndex`、`WithTopK`、`WithScoreThreshold`；实现专属 Option 如 `rag.WithIngestionBatch`、`postgres.WithEmbeddingOptions`、Hybrid 的两路专属选项。后者使用 Eino 官方的实现专属选项包装机制，不要求所有后端认识同一个具体结构。

### 2.4 `Dependencies` 中每项依赖做什么

| 依赖 | 用途 | 是否必需 |
| --- | --- | --- |
| `Loader` | 解析真实来源 | 导入时必需 |
| `Indexer` | 保存子块索引 | 导入时必需 |
| `Config.Retriever` | 默认查询组件，示例中为 Hybrid | 只查询服务必须提供 |
| `VectorRetriever / KeywordRetriever` | 为两个单路 Mode 提供组件 | 使用对应 Mode 时必需 |
| `ChunkReader` | 展示已发布可检索块 | `ListChunks` 时必需 |
| `Lifecycle` | 解析前预留版本、取消、删除 | 使用这些文档管理能力时必需 |
| `Publisher` | 外部索引全部成功后发布原文和父子块 | 分离索引完整模式必需 |
| `PublishedChunks` | 过滤未发布、过期、已删除的外部命中 | 外部发布模式必需 |
| `Config.ParentLoader` | 按知识库创建父块读取器 | 扩展父块且有父块关系时使用 |
| `Config.BeforeSearch` | 查询前执行业务检查 | 示例用于模型档案检查 |
| `Close` | 释放调用方资源 | 可选，成功初始化后由 Service 接管 |

`rag.New` 检查 Loader/Indexer 必须成对出现；也允许只导入或只查询。若没有导入组件，又没有默认 Retriever，则初始化失败。只设置 `VectorRetriever` 而不设置默认 Retriever 也不满足这个初始化条件。

当配置了 Publisher，必须同时配置 Lifecycle 和 PublishedChunks。若存在 PublishedChunks，`New` 会包装默认、向量和关键词三个非空检索器，使各查询模式都经过发布状态检查。

这些检查不自动探测后端，也不验证数据库已安装。底层连接、模型参数和 schema 在具体组装层检查。

<a id="models"></a>

## 3. 数据模型、身份和坐标

### 3.1 不同阶段为什么使用不同结构

| 结构 | 所在阶段 | 主要职责 |
| --- | --- | --- |
| `schema.Document` | Eino 组件边界 | 传入来源正文、索引文本或召回文档 |
| `chunker.Chunk` | 切分算法内部 | 正文、标题路径、序号、原文字符范围 |
| `chunker.ChildChunk` | 父子切分 | 在 Chunk 基础上保存 `ParentIndex` |
| `rag.ChunkRecord` | 权威文档业务 | 稳定分块 ID、父关系、类型、正文和坐标 |
| `rag.IngestionBatch` | 一个文档版本的发布 | 完整 Markdown、父子块、版本、hash、配置快照 |
| `retrieval.SearchResult` | 召回与后处理 | 排名分数、原始证据、扩展上下文、引用信息 |
| `retrieval.HitEvidence` | 上下文分组后 | 保留各实际命中的子块 ID、正文、范围和分数 |
| `search.Response` | 查询返回 | 最终结果、召回诊断、精排诊断、流程诊断 |

数据转换是明确的阶段边界：来源文档 → 算法分块 → 发布批次 → 原生索引文档 → 原生召回文档 → 内部检索结果。不能用最后一种结构替代所有前面的职责。

### 3.2 三种身份

`CollectionID` 表示知识库范围。SQL、原生 `WithIndex`、父块读取和权威回查都使用它。它目前不是用户权限校验；未来公开 API 仍需由业务层校验用户是否能访问该知识库。

`DocumentID` 表示同一份业务文档，更新文档时应保持不变。Tabula 的默认 ID 来自来源 URI 的 SHA-256 前 128 位，但在有 Lifecycle 的导入中，调用方必须显式提供业务 ID，避免临时上传路径变化导致文档被误认为另一份。

`ChunkID` 表示具体版本的具体块。版本化导入时格式如下：

```text
uploaded-document#v7#parent-000000
uploaded-document#v7#chunk-000000
uploaded-document#v7#chunk-000001
```

没有版本预留时为 `document#parent-000000` 或 `document#chunk-000000`。各块的 `ChunkIndex` 从 0 开始，示例打印的人工编号从 1 开始。

`SearchResult.IdentityKey()` 使用知识库、文档、版本、分块 ID 的组合身份；这些范围字段全为空且版本为 0 时退回单纯 ChunkID。因此 RRF 去重不会把不同知识库或不同版本的同名块混为一条。

### 3.3 四种文本

| 文本 | 含义 | 是否可以直接拿来计算原文坐标 |
| --- | --- | --- |
| `batch.Markdown` | Loader 输出并规范化后的完整原文 | 是，坐标参照物 |
| `chunk.Content / ChunkRecord.Content` | 命中正文，可能补充表头 | 需要考虑合成前缀，长度不一定等于原文区间 |
| 索引 `schema.Document.Content` | 标题 + 标题路径 + 正文的检索表示 | 不可以 |
| `result.ContextContent` | 最终上下文，可能为父块正文 | 使用 Context 对应的范围与 ID，不是原子块范围 |

完整 Markdown 必须直接来自 Loader。重叠块会重复部分内容，表头补充会新增显示文本，拼接块不能可靠恢复原文。

### 3.4 字符坐标的严格含义

所有公开分块范围使用 Unicode rune 的左闭右开区间 `[Start, End)`。`你好A` 有 3 个 rune，通常有 7 个 UTF-8 字节；不能用 `string[start:end]` 直接按 rune 坐标截取，应先转为 `[]rune`。

坐标参照的是解析后的、LF 规范化的 Markdown，不是 PDF 字节、Word XML 偏移，也不是页码。`NormalizeLineEndings` 先把 CRLF 变成 LF，再把单独 CR 变成 LF。

保护区域正则返回 byte offset，`protectedSpansRune` 或 unit 构建过程把它转换成 rune offset。父子切分中，子块局部范围必须加上父块原文起点。

补充表头使用 `start == end` 的合成 unit，不占新的原文范围。例如某个块的正文为“复制来的列名 + 原文第 100～180 字符”，其来源区间仍是 `[100,180)`，不能把复制表头长度加到 End。

### 3.5 检索分数的含义会随阶段变化

`Score` 是当前用于排序的分数；`BaseScore` 保留进入精排的召回分；`ModelScore` 是精排模型相关性；`VectorScore`、`KeywordScore` 保存原始通道分数，`VectorRank`、`KeywordRank` 保存融合时的名次。

向量的 Score 为 `1 - cosine distance`；独立 BM25 的 Score 是原始 BM25；两路 RRF 的 Score 是归一化排名融合值；成功精排之后则变成模型分、召回分、来源权重的组合。它们都不表示“答案正确的概率”。

`ChunkID/Content/StartRune/EndRune` 保持命中子块身份。`ContextChunkID/ContextContent/ContextStartRune/ContextEndRune` 表示最终上下文。`EffectiveContent()` 和 `EffectiveChunkID()` 优先取上下文，缺失时回退子块。


### 3.6 原生文档的完整 Metadata 约定

这些键定义在 `retrieval/documents.go`。外部后端应保存它们，而不是依赖某个数据库专有字段名。

| 常量 | 实际键 | 含义 |
| --- | --- | --- |
| MetaSourceDocumentID | rag_source_document_id | 分块的来源文档 ID |
| MetaSourceDocumentIndex | rag_source_document_index | Transformer 输入文档数组下标 |
| MetaChunkType | rag_chunk_type | text 或 parent_text |
| MetaCollectionID | rag_collection_id | 检索范围 |
| MetaDocumentID | rag_document_id | 稳定文档身份 |
| MetaRevision | rag_document_revision | 版本，跨组件传递时使用十进制字符串 |
| MetaRawContent | rag_raw_content | 原始块正文，区别于加标题后的索引文本 |
| MetaParentChunkID | rag_parent_chunk_id | 父块引用 |
| MetaChunkIndex | rag_chunk_index | 同类型块的序号 |
| MetaChunkStart / MetaChunkEnd | rag_chunk_start / rag_chunk_end | 原文 rune 范围 |
| MetaContextHeader | rag_context_header | 标题路径 |
| MetaMatchType | rag_match_type | vector、keyword 或 hybrid |
| MetaVectorScore / MetaKeywordScore | rag_vector_score / rag_keyword_score | 原始通道分数 |
| MetaVectorRank / MetaKeywordRank | rag_vector_rank / rag_keyword_rank | 通道排名 |

`Documents` / `Results` 主要用于召回组件边界，不是 SearchResult 全字段的无损序列化协议：精排状态、ModelScore、父上下文和 Evidence 不在这次转换里完整传递。完整最终响应应直接使用 search.Response。

`CloneMetadata` 只复制顶层 map，嵌套对象按只读约定共享。业务若要修改嵌套 map 或 slice，需要自己复制，不能假设它已经做了深拷贝。

<a id="assembly"></a>

## 4. 启动组装与资源管理

### 4.1 业务层与组件组装层的分工

`rag.New` 接收已经创建好的组件。它不读取环境变量，不创建 PostgreSQL，不选择模型，也不自动执行迁移。当前实际组装代码放在 `examples/5-rag/main.go` 的 `newPostgresRAG`，便于学习和替换。

组装顺序如下：

1. `postgresConfig.Validate` 检查解析预算、模型参数、召回候选数和最终返回数之间的关系。
2. 若 `EnsureSchema` 为 true，先创建数据库扩展。
3. 创建连接池；创建失败直接返回，后续初始化失败则由 defer 关闭连接池。
4. 按需要迁移，然后检查当前 schema 版本。
5. 创建一个 Embedding 实例，索引与向量检索复用它。
6. 根据模型、维度和检索文本规则生成 collection 模型档案。
7. 创建 PostgreSQL Indexer，启用文档版本控制，并绑定模型档案与输入预算。
8. 创建可选的 HTTP 精排服务及 `rerank.Engine`。
9. 分别创建向量、BM25 检索器，再组合成 Hybrid。
10. 注入 `rag.Service`，同时提供父块读取工厂、查询前的档案检查、生命周期能力和资源关闭函数。

```mermaid
flowchart TD
    A[示例配置校验] --> B[扩展与连接池]
    B --> C[迁移和 schema 检查]
    C --> D[共享 Embedding]
    D --> E[PostgreSQL Indexer]
    D --> F[向量 Retriever]
    C --> G[BM25 Retriever]
    F --> H[Hybrid]
    G --> H
    I[可选 HTTP Scorer] --> J[Rerank Engine]
    E --> K[rag.Service]
    H --> K
    J --> K
    C --> L[父块读取与模型档案检查]
    L --> K
```

### 4.2 `rag.New` 实际检查什么

它检查 context 是否已取消；Loader 与 Indexer 必须同时存在或同时缺失；既没有 Loader，也没有默认 Retriever 时不能创建 Service。这里检查的是 `Config.Retriever`，仅注入 `VectorRetriever` 并不能满足这个初始化条件。

它还检查搜索配置、负数 RecallTopK，以及独立 Publisher 的安全依赖：配置 Publisher 时必须同时提供 Lifecycle 和 PublishedChunks。若注入 PublishedChunks，会包装默认、向量、关键词三种检索入口，过滤未发布或过期版本。

这些检查不替代每个组件自己的配置校验。`rag.New` 不验证数据库扩展、Embedding 维度，也不替你修正所有精排配置。

### 4.3 关闭与初始化失败

`Service.Close` 通过 `sync.Once` 执行注入的 Close，因此可以多次调用。nil Service 或没有关闭函数时也安全。

初始化失败时，资源仍由创建组件的一方负责。例如 `newPostgresRAG` 用 `success` 标记，只在成功返回 Service 后移交连接池的管理。这个模式避免“创建了连接池，但创建模型失败后忘记关闭”。

<a id="ingest"></a>

## 5. 文档导入主流程

入口是 `Service.Ingest(ctx, IngestRequest)`，主要代码在 `internal/rag/service.go`。它按文档执行完整导入；一个 Loader 返回多篇文档时，不承诺全部文档共同提交或共同回滚。

### 5.1 请求中各字段的作用

| 字段 | 用途 |
| --- | --- |
| `CollectionID` | 知识库/索引范围，必须非空 |
| `DocumentID` | 稳定业务文档 ID，版本控制时必须提前提供 |
| `Source` | Eino `document.Source`，当前文件 Loader 使用 `URI` |
| `Splitter` | 基础分块策略、大小、重叠、分隔符、语言和近似 token 目标 |
| `ParentChild` | 是否先切大父块，再切检索子块 |
| `ParentChunkSize` | 父块目标 rune 数，0 使用父块默认值 |
| `ChildChunkSize` | 子块目标 rune 数，0 使用子块默认值 |

### 5.2 完整执行顺序

```mermaid
flowchart TD
    A[校验请求和 context] --> B{提供 Lifecycle?}
    B -->|是| C[按稳定文档 ID 预留 attempt]
    B -->|否| D[Loader.Load]
    C --> D
    D --> E[复制文档并规范化换行]
    E --> F{ParentChild?}
    F -->|否| G[SplitWithDiagnostics]
    F -->|是| H[派生父子配置并分别切分]
    G --> I[构造 IngestionBatch]
    H --> I
    I --> J[版本化块 ID 与内容哈希]
    J --> K[ValidateBatch]
    K --> L[构造原生 Eino 索引文档]
    L --> M[Indexer.Store]
    M --> N{配置独立 Publisher?}
    N -->|是| O[校验完整返回 ID 并发布正文]
    N -->|否| P[返回导入结果]
    O --> P
```

实际细节需要按这个顺序理解：

- 先校验分块参数和父子大小，负数直接失败。不会把非法请求静默变成默认配置。
- 有 Lifecycle 时，在解析前调用 `ReserveDocument`。这保证新旧导入的先后关系以“任务启动顺序”为准，而不是以模型请求完成顺序为准。
- 版本控制要求稳定 DocumentID。解析失败也会消耗一次 attempt；attempt 允许有间隙。
- 如果指定一个稳定 DocumentID，而 Loader 返回超过一篇文档，会拒绝请求，避免多篇文档覆盖同一个业务身份。
- 对 Loader 文档做副本及元数据浅拷贝，不直接修改 Loader 返回的对象。nil 文档跳过；规范化后只有空白的正文跳过。
- 当前业务入口把换行统一成 LF，再做分块和内容哈希；因此偏移都对应这一份规范化 Markdown。
- 每篇文档分别分块、建索引、发布。前一篇已成功、后一篇失败时，前一篇的结果不会自动撤回。
- 没有任何可处理正文时返回 `ErrEmptyDocument`。

### 5.3 平铺块与父子块如何变成持久化记录

`buildFlatBatch` 把每个普通 Chunk 变成一个 `text` 子块，其 ID 由源文档 ID 和块序号组成。

`buildParentChildBatch` 先创建 `parent_text` 记录，再创建 `text` 子块记录，并根据 `ParentIndex` 建立引用。ParentIndex 是实际保存的父块数组下标；父块自身 Seq 可能存在间隙，两者不能混用。

`buildChunkRecord` 保存正文、标题路径、rune 范围和类型，并添加对应的元数据。批次 Title 由 `documentTitle` 使用 `searchcontent.DefaultBuilder()` 从元数据提取，找不到时回退到文档 ID；定制 Config.InputBuilder 会影响索引文本，但不会自动改写这个批次标题规则。

如果 attempt 大于 0，`qualifyChunkIDs` 会把父块和子块 ID 一起改成带版本的 ID，再改写所有父块引用。这样外部索引的旧任务即使完成写入，也不会覆盖新版本的块。

### 5.4 内容哈希、处理快照与诊断不是同一件事

`ContentHash` 是规范化 Markdown 的 SHA-256，用于识别正文变化。

`ProcessConfig` 是 JSON 对象，版本标记为 `humbert-rag-v2`，记录基础分块参数、父子开关及派生的父子配置。它不等于所有运行时状态的完整日志：实际自动选择了哪种策略、哪些策略被拒绝等信息放在返回的分块 Diagnostics 中，不会由 Service 自动完整写入数据库。

PostgreSQL 还会把索引输入或发布正文的摘要加入处理快照，后面会解释同一 attempt 的幂等约束。

### 5.5 `ValidateBatch` 是写入前的最后一道业务约束

它检查以下内容：

- CollectionID、DocumentID 非空，attempt 非负。
- 提供了 ContentHash 时必须匹配 Markdown；ProcessConfig 必须是 JSON 对象。
- 块 ID 非空且全批次唯一；块 DocumentID 必须与批次一致。
- 同类型块的 Index 唯一且非负，不要求父块序号连续。
- 块正文非空；范围满足 `0 <= Start < End <= 文档 rune 长度`。
- 父块只能为 `parent_text`，不能再次引用父块。
- 子块只能为 `text`；有父块引用时，父块必须存在，且子块范围包含在父块原文范围内。

它不要求 `Content` 必须逐字等于原文切片，因为正文可能补上表头；也不直接证明整篇文档的覆盖率。来源覆盖、保护区域和重叠等行为主要由分块算法及其测试保证。

### 5.6 原生 Indexer 收到的是什么

`IndexDocuments` 只把子块送入索引器。每个 `schema.Document` 的 Content 是“标题 + 标题路径 + 子块正文”的检索文本，Metadata 另存原始子块正文、collection、文档 ID、版本、父块 ID、范围和类型。

`WithIngestionBatch(batch)` 是一个 Eino 实现专有选项，携带完整批次。普通 Eino Indexer 不需要识别它；本项目 PostgreSQL Indexer 识别它，才能把整篇文档、父块、子块和检索索引一起原子提交。

独立 Publisher 模式下，Service 会校验 Store 返回的 ID 是否与预期集合完全一致：顺序可以不同，缺少、重复、额外或被改名的 ID 都失败。未配置独立 Publisher 时，Service 不执行这层发布前检查，具体保证由 Indexer 自身承担。

<a id="loader"></a>

## 6. 真实文件解析与子进程

### 6.1 文件 Loader 的输入与输出

`internal/rag/loader/tabula/loader.go` 实现原生 Eino `document.Loader`。当前支持本地文件：PDF、DOCX、ODT、XLSX、PPTX、HTML/HTM、EPUB、MD、TXT、CSV。

Source.URI 不能为空，不能是 HTTP/HTTPS URL，也不能是目录。它没有实现下载网页、读取对象存储或上传接口；UI/HTTP 收到上传文件后，需要先保存本地文件，再把路径传给 Loader。

MD、TXT、CSV 走 UTF-8 文本读取：去掉 BOM，拒绝非法 UTF-8 和 NUL。CSV 目前保持原始文本，不自动转换成 Markdown 表格。其余支持格式由 Tabula 转换成 Markdown。

Loader 输出一篇 `schema.Document`，并补充元数据：

| 元数据键 | 内容 |
| --- | --- |
| `_source` | 调用时的原始文件路径 |
| `_title` | 去掉扩展名的文件名 |
| `file_name`、`file_ext`、`file_size` | 文件基本信息 |
| `rag_parser` | Tabula 或 `utf8-text` |
| `rag_parser_warnings` | 解析警告消息数组 |
| `rag_ocr_used` | 是否出现 OCR fallback 警告 |
| `rag_source_content_hash` | 实际读取的源文件字节 SHA-256 |

默认文档 ID 是 URI 的 SHA-256 截取前 16 字节并加前缀，因此移动文件会改变默认 ID。版本控制的导入应明确使用业务 DocumentID，而不是依赖路径生成身份。

### 6.2 Loader 配置的几个容易误解之处

`DefaultConfig()` 默认排除页眉页脚、规范化换行。ParseOptions 的零值会在解析器里补上预算。

`OCRLanguage` 只是语言参数，不能单独启用 OCR；OCR 还需要 `ocr` build tag、Tesseract 及相应语言包。当前没有 VLM、ASR 或多模态 embedding 流程。

调用 Load 时传入的 Eino LoaderOption 当前没有被使用。ParseOptions 中的页码设置可以生效，但排除页眉页脚和 OCR 语言会被 Loader 自身字段覆盖。

### 6.3 为什么还有 `internal/documentparse/parser.go`

文件解析单独放在这个包里，承担输入验证、资源预算和子进程协议，RAG Loader 只负责把结果转换成 Eino 文档。

默认预算是：

| 项目 | 默认值 |
| --- | ---: |
| 源文件最大大小 | 32 MiB |
| 最终 Markdown 最大大小 | 32 MiB |
| ZIP 声明的解压后总大小 | 256 MiB |
| ZIP 条目数 | 8192 |
| 单次解析超时 | 1 分钟 |

这些字段的 0 表示使用默认值，不表示无限制。

### 6.4 为什么先复制临时快照

`ExtractFile` 先检查文件类型，再打开文件并检查真实文件句柄。随后把文件复制到保留扩展名的临时快照，同时计算字节哈希。

复制时检查 context，最多读取 `MaxInputBytes + 1`，以判断是否超过预算。后续预检查和解析都读取这一份快照，避免原文件在校验之后、解析之前被替换。结束时清理快照。

源文件哈希与 RAG ContentHash 含义不同：前者哈希原始 PDF/Office 等字节，后者哈希转换后的规范化 Markdown。

### 6.5 解析前的格式检查

PDF 检查 `%PDF-` 文件头。Office ZIP 格式检查条目数量、声明解压大小，并拒绝符号链接、绝对路径、反斜杠路径、`../` 等路径形式。

这是一层格式与预算检查，没有实现完整操作系统沙箱或进程内存上限。ZIP 声明大小检查也不等于对所有第三方解析器内部内存分配的精确控制。

### 6.6 子进程是如何工作的

父进程使用 `os.Executable()` 启动当前可执行文件，在环境变量中加入 worker 标记，通过 stdin 写 JSON 请求。解析包的 `init` 检测到标记后执行 worker 分支，读取请求、调用转换函数，把 JSON 结果写到 stdout，然后退出，不再进入应用 main。

子进程只保留必要标记与 PATH，避免继承所有应用配置。stdin 请求受 64 KiB 限制；stdout 使用有界 writer；stderr 最多保留 4096 字节。stdout 上限为最终 Markdown 预算的 6 倍，加警告与协议余量，用来容纳 JSON 转义膨胀。解析警告最多 64 条，每条最多 4096 字节。

`exec.CommandContext` 让 context 超时或取消时结束子进程，WaitDelay 为 1 秒。父进程还会检查返回 JSON、资源限制错误及最终 Markdown 大小。

这解决了取消和进程间输出预算的问题，但不能承诺第三方解析器永远不会消耗大量 CPU/内存。

<a id="strategy"></a>

## 7. 分块配置、文档画像与策略选择

### 7.1 配置归一化分为几层

不要把 `DefaultConfig()`、`NormalizeSplitterConfig()` 和 `ensureDefaults()` 当成同一个函数。

- `DefaultConfig()` 创建显式默认值：ChunkSize=512，ChunkOverlap=80，分隔符为 `\n\n`、`\n`、`。`；Strategy 为空。
- `NormalizeSplitterConfig()` 补上非正的 ChunkSize 和空分隔符，并把负数重叠归零。Service 在此之前已经校验非法负数。
- `ensureDefaults()` 再根据 TokenLimit 收紧目标大小，并把重叠上限限制到目标大小的一半。
- 高层 `Split`、`SplitWithDiagnostics` 使用上述完整流程。直接调用底层 `SplitText` 不会自动完成全部策略和 token 处理。

显式 `ChunkOverlap=0` 保持关闭。使用零值结构体不会自动得到 80 字符重叠；想要项目默认行为，应从 DefaultConfig 修改。

### 7.2 每一种 Strategy 的实际执行链

| Strategy | 尝试链 |
| --- | --- |
| 空字符串、`legacy`、`recursive` | legacy |
| `heading` | heading → legacy |
| `heuristic` | heuristic → legacy |
| `auto` | 根据画像选择 heading/heuristic，最后 legacy |

`recursive` 是 legacy 的公开别名，没有另写一套递归实现。未知策略会被业务配置校验拒绝；不要依赖底层未校验函数的默认分支。

### 7.3 `ProfileDocument` 统计了什么

画像包含总 rune 数、行数、行长均值和标准差、Markdown 各级标题数、章节标记、编号标记、大写标题、视觉分隔符、分页符、空白段落、页脚信号、表格信号、代码比例和语言估计。

实现是轻量文本扫描，并不是 Markdown AST：

- Markdown 标题识别 ATX 形式，例如 `## 安装`；不完整支持 Setext 标题等所有写法。
- 以三个反引号开头的行切换代码围栏状态，围栏内不会被误当成普通标题。
- 代码字符统计不包含围栏本身和换行；行长统计排除围栏内的代码行。
- `HasTables` 根据管道表格形式判断。
- 连续空行使用 `strings.Count(text, "\n\n\n")` 等规则，并非统计所有空白段的完整语义。
- 页脚计数表示匹配到多少页脚式行，不是严格验证了重复页脚。
- 语言采样有 byte 长度上限，不是对全文调用 tokenizer。

`DominantHeadingLevel()` 也不是“数量最多的标题级别”：优先选最浅、且出现至少 3 次的级别；若没有这样的级别，则回退到存在标题的最深级别。

### 7.4 自动选择的准确条件

heading 被加入候选链，需要同时满足 Markdown 标题总数至少 3、标题密度大于 0.005、主标题级别大于 0。

heuristic 被加入候选链，满足以下任意条件即可：启发式标记总数至少 5；存在 form-feed 分页符；存在中/英/德章节标记。

legacy 始终在最后。所以 auto 不等于永远使用最复杂的切分方式；普通正文经常直接进入 legacy。

### 7.5 为什么一种算法执行后还可能被拒绝

`ValidateChunks` 用目标大小做质量启发式检查：

| 检查 | 拒绝条件 |
| --- | --- |
| 空结果 | 没有任何块 |
| 长文被切成一个块 | 文档长度大于目标的 2 倍，但只有一个块 |
| 过多很小的块 | 不计最后一块，长度小于 50 的块数量同时大于 `块数/4` 和 2 |
| 所有块太小 | 最大块小于目标的 1/4，且文档长于目标 |
| 超大块 | 最大块大于目标的 2 倍 |

这是策略选择的质量规则，不是持久化范围校验。最后一个 legacy 即使质量不达标，只要有结果也会保留，优先避免原文被丢掉。

Diagnostics 的 TierChain 是候选链，Rejected 是已遇到的拒绝原因。最终 token 调整发生在策略质量校验之后。某个 heading/heuristic 函数内部也可能自行回退到 SplitText，因此 SelectedTier 表示外层选中的策略分支，不能证明每个最终块都完全由该策略独立生成。

<a id="recursive"></a>

## 8. 递归分块、保护区域、表头与重叠

这是 Chunker 最底层、也是细节最多的部分。主要调用关系是：

```text
SplitText
  → buildUnits
      → 查找 protected spans
      → 普通正文递归拆分
      → 建立带原文坐标的 unit
  → mergeUnits
      → 表头追踪
      → 目标大小判断
      → overlap 选择
      → 合成 Chunk
```

### 8.1 为什么先切 unit，再合并 Chunk

直接按 512 个字符切会切断代码、公式、链接或表格。unit 是更小的结构单元：普通段落可以继续细分，受保护内容尽量作为整体。每个 unit 同时携带正文和原文 rune 范围。

`recursiveSplit` 按分隔符顺序尝试，先用段落，再用换行，最后用句号。分隔符按字面量匹配，不作为用户正则执行。

虽然正则 Split 会删除分隔符，代码会重新找出分隔符并作为独立片段插回去，所以拼接结果保持原文。只对仍然太大的片段递归使用下一级分隔符。

没有任何可用分隔符时，不会为了 ChunkSize 强行逐字符切断；可能得到超过目标大小的 unit。后续有绝对上限和可选 token 处理。

### 8.2 哪些区域受保护

`protected_span.go` 识别双美元公式、Markdown 图片和链接、管道表格、代码围栏及行内代码等形式。多个范围排序后，重叠范围保留先选中的范围，避免重复建立 unit。

正则有长度和格式边界，不能把它当成完整 Markdown 解析器。它处理常见语法，但不保证所有嵌套、转义和特殊格式都识别正确。

受保护 unit 可以超过 ChunkSize，前提是未超过绝对上限 7500 rune。超过 7500 的内容会强制切分，并在切点附近最多向前寻找约 200 字符的空白或换行。结构完整性因此是尽量保持，而不是无条件保持。

### 8.3 目标大小与绝对上限

`mergeUnits` 在当前块加上下一个 unit 和需要补充的表头超出目标时，先输出当前块，再决定重叠和表头。

ChunkSize 是软目标。单个结构 unit 比目标大时可以保留整体。7500 rune 是另一层硬约束；token 目标又可能在最后把较大的块继续拆开。

这三层的优先级应理解为：常规目标帮助形成合理块；保护结构允许适度超长；绝对上限及完整模型输入预算限制异常输入。

### 8.4 表头如何跨块保留

`header_tracker.go` 当前实际注册的是 Markdown 表头追踪。它检测“列名行 + 分隔行”，保存活动表头；后续表格被切到下一块时，尝试补入同一表头。

实现会处理空白导致的边界、新表格、列数变化和部分空表头。例如只有管道和空格的表头，可能由后续数据行补成可读表头。这里是结构规则，没有调用 LLM。

补入的表头是 `start == end` 的合成 unit，不改变来源范围。若补充表头放不进目标，会先减掉部分重叠；表头本身过长时不会强制重复。

`headerAlreadyPresent` 避免已有表头再次复制；`headerColumnMismatch` 避免不同列数表格串在一起。当前列数计算主要靠 `strings.Split("|")` 并处理两侧空列，不是完整的转义管道/代码内管道解析，复杂 Markdown 表格仍可能误判。

### 8.5 递归算法的重叠不是“永远取末尾 80 字”

`computeOverlap` 首先计算允许的最大重叠：

```text
maxOverlap = min(配置重叠, 目标大小 - 下一个 unit 长度)
```

再在尾部窗口查找可读边界，优先级为段落 → 换行 → 句子。中文句号、问号、感叹号和符合条件的英文句末会参与识别。边界不能落在保护区域内部，保留内容也不能只有空白。

同一优先级下，选择能满足预算的较早起点，保留尽可能完整的尾部。如果没有合适边界，返回无重叠，而不是机械截取尾部字符。

合成表头或正文长度与来源范围不一致的 unit 会阻断重叠提取，避免把复制内容当作原文再次计算偏移。

### 8.6 学习时可手工验证的一个例子

设目标 100 rune、重叠 20。当前块有 90 rune，下一个 unit 有 35 rune，则合并超限。算法先输出当前块，再尝试从当前块末尾找至多 20 rune 的自然边界。如果最后一个完整句子有 17 rune，可以带到下一块；如果只有一个 40 rune 的不可分结构且找不到合法边界，则下一块直接从新 unit 开始。

因此配置重叠 20 代表上限，不代表每一对块都恰好重复 20 rune。

<a id="heading"></a>

## 9. 标题分块与标题路径

### 9.1 标题层级状态

`HeadingHierarchy` 用 6 个槽位记录 `#` 到 `######`。遇到一个新标题时更新该层，并清空更深层的旧标题。层级可以跳跃；代码不强迫每个 H3 都必须有 H2。

它可以输出 `A > B > C` 路径，也可以输出带 `#` 的多行标题路径。后者保存在 ContextHeader，方便模型知道子块属于哪一节。

### 9.2 如何确定章节边界

`splitByHeadings` 从画像中选择主标题级别，扫描不在代码围栏内、且级别不深于主级别的标题，建立章节边界。开头没有标题的前言也保留。

没有可用主标题，或者边界不足以形成分节时，内部直接回退到 SplitText。

章节正文加标题路径能放入目标时，直接成为一个块。否则章节内部继续调用 SplitText，并用 `buildSectionBreadcrumbIndex` 记录更深层标题的出现位置，为每个子块按起点补上正确的路径。

子块切出的局部范围加章节原文起点，才能转换成全篇范围。

### 9.3 小章节为何可以合并

小块目标为 `max(ChunkSize/2, 200)`。相邻小块只有在原文连续、合并后不超目标、且存在共同标题前缀时才会合并。

合并后的 ContextHeader 使用共同前缀，避免把后一节错误标成前一节的更深层标题。两个不同顶层 H1 不会因为都很短就合并。

标题行本身仍可能保留在正文中，ContextHeader 是额外的上下文线索，不是先把正文标题删除再重建。检索文本构造器只去掉完全相同的独立部分，不做任意标题子串删除。

<a id="heuristic"></a>

## 10. 启发式章节分块

### 10.1 它适合什么正文

PDF/Office 解析后的文本未必保留 Markdown 标题，但可能出现“第一章”“Chapter 2”“1.2 安装”、全大写短标题、分页符或分隔线。heuristic 用这些信号找章节边界。

语言配置决定尝试中、英、德哪些章节正则；未指定时尝试所有支持规则。传入的 DocProfile 目前不直接参与这个函数的切点计算，切点由正文再次扫描得到。

### 10.2 边界如何排序

| 信号 | 优先级 |
| --- | ---: |
| form-feed 分页符 | 100 |
| 编号小节 | 90 |
| 章节标记 | 85 |
| 全大写标题 | 70 |
| 视觉分隔线 | 60 |
| 页脚式行 | 50 |
| 连续空白结束 | 40 |

先按位置升序，再按同位置优先级降序，重复位置只保留一项。但逐行识别时章节规则先判断，匹配成功后不会继续把同一行识别成编号小节，所以不能只按优先级数字推断所有规则的执行顺序。

代码围栏内的普通行不作为标题标记；严格位于保护区域内部的边界也被删除，保护区两端仍可作为边界。form-feed 的查找使用 rune 位置；`allRuneIndices` 当前实际上按单 rune 比较，主要用于这个单字符标记。

### 10.3 结构块如何合并

短文档直接使用 SplitText；找不到边界也回退。

找到边界后补上文档起点和终点，按结构块累计大小。当前累计至少达到 `max(ChunkSize/4, 50)` 且再加入下一块会超目标时，输出当前块。单个结构块本身超大时，直接交给 SplitText，再把范围映射回全篇。

页脚信号仅作为边界，不负责删除页脚；实际排除页眉页脚发生在 Loader/Tabula 转换阶段。

### 10.4 与递归重叠规则的区别

heuristic 的 `applyOverlapAligned` 优先在当前终点前、最多 `2 × ChunkOverlap` 的窗口内选择最近的已有结构边界，找不到再向前找换行，最后才用机械字符位置。

因此它的实际重叠可能达到配置值的两倍。这和递归算法“最多保留配置值、找不到自然边界就不保留”的规则不同。比较策略时，不能只把同一个 ChunkOverlap 数字看成完全相同的切分行为。

<a id="parent-child"></a>

## 11. 近似 token 目标与父子分块

### 11.1 rune 大小为什么还需要 token 目标

中文、英文和德文相同字符数对应的 token 数不同。`tokens.go` 使用估计比例：英文约 4 rune/token，德文约 4.5，中文约 1.7，混合语言约 3。

`ApproxTokenCount` 用字符数除比例并取整；`CharsForTokenLimit` 把 token 目标换算成字符预算，再乘 0.9 留余量。它不是模型 tokenizer，也不能证明模型一定接受输入。

语言检测统计 CJK 与拉丁字符比例，并用德文变音字符和部分常见词做判断。配置 Languages 时，近似预算优先使用第一个语言提示。自动画像中的语言结果不会统一写回配置，几个调用点的默认行为不能视为一次全局语言决策。

`SentenceSeparators` 提供语言对应句末符号，但当前默认递归配置没有自动调用它来替换 Separators；想改变分隔符需要显式配置。

### 11.2 最后的 `enforceTokenTarget` 做什么

高层 Split 先产生结构块，再检查每个块的 `EmbeddingContent()`，也就是标题路径加正文。超出近似目标时，取原文范围对应的 body，保留可能复制来的表头 prefix，再扣掉 prefix 和标题路径的长度，得到可继续切分的正文空间。

切点尽量向前寻找换行、中文句号或空格；新片段重用标题路径和表头，范围仍对应原文，Seq 重新编号。此阶段可以切开原先受保护的大块，因为 token 目标优先于局部结构完整性。

如果标题和表头已经耗尽预算，函数保留原块，不会偷偷删标题或截断正文；后续完整输入预算会报错。因此 TokenLimit 是切分目标，不能替代 Embedding Budget。

这里还没有包含文档 Title，索引时构造完整检索文本后仍需再次校验。

### 11.3 父子配置如何派生

`DeriveParentChildConfigs(base, parentSize, childSize)` 复制基础配置，避免共享 Separators/Languages 切片。

- 父块大小：默认 4096；继承策略与基础重叠；强制 TokenLimit=0，因为父块不直接送 Embedding。
- 子块大小：默认 384；继承策略和 token 目标；重叠默认改为 `childSize/5`。
- 基础 ChunkOverlap 为 0 时，子块也保持 0。
- 后续父、子各自经过 ensureDefaults，所以重叠还会被各自目标大小约束。

开启父子模式后，基础 ChunkSize 不再直接决定最终父/子窗口，明确的 ParentChunkSize 和 ChildChunkSize 更重要。

### 11.4 父子切分的准确步骤

1. 对全篇执行父块 Split。
2. 根据父块 Start/End 从全篇原文取 body，不能直接用带复制表头的 parent.Content 再切。
3. 对 body 使用子块配置执行 Split；auto 可根据这个局部 body 重新选择策略。
4. 把父块补充表头在必要时传给子块，并避免重复或列数不一致。
5. 若父块只有一个、正文完全相同的子块，则不单独保存父块，ParentIndex=-1。
6. 有多个子块，或单一子块正文与父块不同时，保存父块，并记实际保存数组中的 ParentIndex。
7. 子块范围加父块原文起点；合并父标题路径和子标题路径。
8. 加入表头和路径后，再执行一次近似 token 检查。
9. 为所有子块分配全篇连续 Seq。

合并标题路径仅删除连接处“父最后一行 == 子第一行”的重复，不做全路径任意去重。

父子模式的 Diagnostics 主要描述父块策略，子块独立选择策略但没有逐个把完整诊断汇总到导入结果。不能凭一个 SelectedTier 推断所有子块都采用相同算法。

### 11.5 为什么父块不直接建立检索索引

小子块负责精确召回，父块负责补充完整语境。如果父块也直接参与同一个索引，长正文更容易包含多个主题，检索和最终证据数量的含义也更难解释。

当前父块只保存为 parent_text；Search 先命中 text 子块，再按 ParentChunkID 回查父块。不会因为某个父块含有答案，就自动认为其全部子块都被召回。

<a id="representation"></a>

## 12. Eino Transformer 与统一检索文本

### 12.1 原生 Transformer 与业务 Service 是两个入口

`internal/rag/transformer/chunker/transformer.go` 实现 `document.Transformer`，用于 Eino 常规 Load → Transform → Store 流程。

它逐篇调用核心 Split，生成子文档并保存 source index、chunk index、范围和标题路径等元数据。默认 ID 用源 ID（缺失时用 source 序号）加块序号。输出 Content 保持原始分块正文，不提前改成检索文本。

`WithSplitterConfig`、`WithIDGenerator` 是实现专有调用选项；配置中的切片会复制，避免一次调用修改其他调用的默认配置。

Transformer 不提供完整文档版本、父子持久化和发布事务，也不会自行规范化换行。当前 `Service.Ingest` 直接使用核心 Chunker，以便构造完整 Batch，没有再实例化这个 Transformer。两者复用分块算法，但承担不同层次的职责。

### 12.2 `searchcontent.Builder` 如何生成输入

默认标题键按顺序尝试 `title`、`_title`、`file_name`，默认标题路径键是 `rag_context_header`。只接受字符串值。

`BuildText(title, header, body)` 把每部分 TrimSpace，删除空部分及完全相同的独立部分，用两个换行连接：

```text
设备手册

# 维护
## 更换电池

按住释放按钮，然后取出电池……
```

它不会做摘要、关键词抽取或语义去重。

同一份索引文本同时送入 Embedding 和 BM25 search_content，保证两路检索看到相同的语义线索。原始 body 保存在另一个字段，不因为加了标题就改变引用范围。

### 12.3 自定义 Builder 的一致性问题

Service 的 InputBuilder 影响索引文本与模型档案。默认精排 PassageBuilder 也复用 BuildText，但使用自己的默认标题键，不会自动继承 Service 中定制的 TitleKeys。

因此如果改成例如 `display_name` 作为标题来源，且希望精排看到相同标题，需要同时定制 `Rerank.PassageBuilder`。只修改索引 Builder 不会自动改变全部阶段。

<a id="embedding"></a>

## 13. Embedding 输入预算与模型档案

### 13.1 OpenAI 兼容 Provider 的职责

`provider/embedding/openai` 基于 Eino 的 OpenAI Embedder 创建实例，支持 APIKey、BaseURL、Model、ModelRevision、Dimensions、Timeout、HTTPClient 和 InputBudget。

BaseURL 可空，非空时必须是合法 HTTP/HTTPS 地址；不能夹带 URL 用户凭据、query 或 fragment。Model 必须非空；维度、超时不能为负。默认超时 30 秒。

Dimensions 大于 0 才把维度参数传给服务；为 0 表示省略该请求参数，并不表示本项目数据库维度变成可变。PostgreSQL 仍要求实际返回 1024 维。

ModelRevision 不发送给模型 API，用于识别向量空间身份。Provider 创建不等于已成功访问真实模型。

### 13.2 输入预算如何预检和分批

`embeddinginput.Budget` 默认单条 8192、单批 65536。这是项目本地保护值，不是任何模型的官方限制。CountTokens 缺失时直接使用 UTF-8 字节数 `len(text)` 作为保守估计；这与分块阶段按 rune 比例估计是两种不同规则。

`Budget.Plan` 在发送任何请求前扫描全部输入：

1. 计数器返回负数则失败。
2. 单条超过单条预算或单批总预算则失败。
3. 在不超过 batchSize 条数的同时，控制累计 token 预算。
4. 用 `tokens > limit - total` 判断是否超总预算，避免加法溢出。
5. 返回 `[Start,End)` 批次区间，不截断文字。

`Limit` 包装原生 Embedder，按这些区间调用 delegate，并校验返回向量数量，保持输入顺序。PostgreSQL Indexer 还有自己的预检与 batchSize；示例默认每批 32 条，Indexer 单独使用时默认 64 条。

完整检索文本超长会明确报错，而不是让模型只看到正文的前半段。分块 TokenLimit 和模型 Budget 需要配合调整。

### 13.3 为什么同维度也不能随意切模型

两个模型都输出 1024 维，并不代表向量处于同一个语义空间。旧文档用模型 A，新 query 用模型 B，余弦距离可能失去意义。

`embeddinginput.Profile` 包含 Provider、Endpoint、Model、Revision、实际 Dimensions、InputVersion 和 SearchBuilder。固定字段 JSON 的 SHA-256 形成 ProfileID。

APIKey、HTTP 超时、单批条数和本地预算不属于向量空间身份，不进入这个档案。相反，标题键及标题路径规则会影响 Embedding 输入，因此进入档案。

示例输入版本为 `humbert-search-content-v1`，实际维度固定 1024。模型别名背后的权重更新时，开发者需要同步修改 ModelRevision；程序不能自动检测服务商悄悄替换权重。

### 13.4 collection 档案何时绑定

`EnsureCollectionProfile` 在事务和 collection 专属 advisory lock 下执行：

- collection 没有档案、也没有历史检索索引时，插入当前档案。
- 已有档案相同则通过。
- 已有档案不同，返回 `ErrEmbeddingProfileMismatch`。
- 没有档案但已经有历史检索索引，返回 `ErrUnboundLegacyIndex`，避免武断地认定旧数据与当前模型相同。

示例的预留版本、写入及 BeforeSearch 都检查档案；BeforeSearch 对 keyword 模式也执行，不是只检查向量模式。

删除一篇文档不会删除 collection 档案。要更换模型，最直观的实验方式是使用新的 CollectionID 并重新导入。

<a id="database"></a>

## 14. 数据库结构、索引和迁移

### 14.1 当前后端的实际要求

连接检查要求 PostgreSQL 至少 15，vector 至少 0.7.0，pg_search 至少 0.25.0。版本解析比较严格，无法解析的版本字符串会失败。

`EnsureExtensions` 使用独立连接创建扩展。`NewPool` 默认连接超时 10 秒，设置 statement_timeout=30 秒、lock_timeout=5 秒，每个新连接检查扩展并注册 pgvector 类型，最后 Ping。失败会关闭池。

这些是当前代码的检查门槛；实际数据库还需要具备安装扩展的环境和权限。EnsureSchema 不会替你创建数据库本身。

### 14.2 表之间的关系

```mermaid
erDiagram
    documents ||--o{ chunks : contains
    chunks ||--o| retrieval_index : indexes_child
    documents ||--o{ retrieval_index : owns
    chunks o|--o{ chunks : parent_of
    documents {
        text collection_id PK
        text id PK
        text markdown
        bigint revision
        text content_hash
        jsonb process_config
    }
    chunks {
        text collection_id PK
        text id PK
        text document_id FK
        text chunk_type
        int chunk_index
        text content
        text context_header
        int start_rune
        int end_rune
        text parent_chunk_id FK
    }
    retrieval_index {
        bigint id UK
        text collection_id PK
        text chunk_id PK
        text document_id FK
        text search_content
        halfvec embedding
        boolean enabled
    }
```

### 14.3 `documents`：当前已发布的整篇文档

主键是 `(collection_id,id)`，保存 title、完整 markdown、metadata、revision、content_hash、process_config 和时间戳。

同一个稳定文档 ID 对应一份当前版本，数据库没有单独保存所有历史正文。旧版本任务的安全主要依赖 heads 中的 attempt，而不是一个历史版本表。

### 14.4 `chunks`：正文、父子关系和来源范围

主键是 `(collection_id,id)`；文档外键删除时级联删除。类型和序号唯一约束是 `(collection_id,document_id,chunk_type,chunk_index)`，让父、子各自可以从 0 编号。

ParentChunkID 的外键限定同 collection，父块删除时只将 parent_chunk_id 置空。SQL 外键并没有独立保证父块与子块属于同一 DocumentID；业务 Batch 校验负责这个更强条件。

SQL 检查允许 `end_rune >= start_rune`，业务校验更严格，要求真实块 `End > Start` 且不超整篇文档。

### 14.5 `retrieval_index`：仅索引可召回子块

每条记录同时保存 search_content 和 `HALFVEC(1024)`。父块没有对应记录。

`id` 是独立 BIGINT identity 唯一字段，用作 ParadeDB key_field；业务身份仍是 `(collection_id,chunk_id)`。enabled 参与查询条件，但 Service 没有独立暴露启停单个索引记录的业务 API。

向量索引使用 HNSW、halfvec_cosine_ops，构建参数 m=16、ef_construction=64。当前没有业务配置控制查询 ef_search。

BM25 索引使用 `USING paradedb`，包含 id、search_content、collection、enabled、chunk/document ID。当前 schema 没有显式指定 BM25 tokenizer/analyzer；分块 Languages 不会自动配置 ParadeDB 的中文分词。

### 14.6 三张辅助表

| 表 | 用途 |
| --- | --- |
| `rag_collection_profiles` | collection 的向量模型和输入规则档案 |
| `rag_document_heads` | 当前允许发布的 attempt 与 deleted 标记 |
| `rag_schema_migrations` | schema 版本、迁移名称、校验和和执行时间 |

heads 故意不依赖 documents 外键。文档删除之后仍必须保留最新 attempt，防止旧任务再次把文档写回来。

### 14.7 迁移如何避免并发与历史 SQL 漂移

当前 SchemaVersion=2。Migrate 开启一个事务，把 statement_timeout 临时设为 5 分钟，取得数据库级迁移 advisory lock，然后检查历史。

版本 1 建立基础表/索引，并接纳部分旧表字段；版本 2 增加文档版本、模型档案、heads 和修正后的父块外键。若数据库版本比程序支持版本新，会拒绝运行。

每版语句用 NUL 分隔后计算 SHA-256。已执行版本的 checksum 不匹配时失败，因此未来修改 schema 应新增迁移，不能直接修改已执行的旧迁移语句。

CheckSchema 检查历史版本的最大值及数量是否与当前版本一致，不对所有实际列定义和索引参数进行完整比对。

独立迁移入口是 `cmd/rag-migrate`，使用环境变量 `HUMBERT_RAG_DATABASE_URL`；示例使用的是 `RAG_DATABASE_URL`，两者名字不同。

<a id="indexing"></a>

## 15. PostgreSQL 索引写入与原子事务

### 15.1 Store 有两种输入模式

有 `WithIngestionBatch` 时，Indexer 验证 collection、子块数量、每个位置的 ID 和元数据中的原始正文是否与 Batch 一致，再使用完整批次写入。

没有 Batch 时，可以把普通 Eino Document 当作“一篇完整文档 + 一个块”保存。但如果元数据声明它来自某篇文档的分块，Indexer 会返回 `ErrIncompleteSource`，避免把局部正文冒充完整文档覆盖已有来源。

原生多文档 Store 是逐文档处理，不是跨所有文档一个大事务。

配置固定 ProfileID 时，不允许调用方通过 Eino option 临时覆盖 Embedder 或 EmbeddingOptions；否则 collection 档案会与实际向量来源不一致。

### 15.2 模型请求发生在事务之前

`storeDocument` 的顺序是：

```text
ValidateBatch
→ 检查文本数量、版本和模型档案
→ 构造处理快照
→ 提前序列化父子块 metadata
→ 预检全部检索文本预算
→ 分批请求 Embedding
→ 校验并转换 halfvec
→ 开数据库事务替换当前文档
```

这样不会在等待远程模型的几十秒里占着文档写事务。非法范围、metadata 无法序列化或预算超限，也会尽量在模型请求之前失败。

模型已经成功而数据库写入失败时，模型调用费用不会被回滚；这就是外部调用与事务分离的实际边界。

### 15.3 向量的数值校验

每个向量必须恰好 1024 维，没有 NaN/Inf，分量绝对值不超过 half float 最大值 65504，并且转换到 half 精度后不能成为全零向量。代码用接近 `2^-25` 的下溢界限判断有效非零分量。

没有额外把向量归一化为单位长度；余弦运算由数据库负责。

数量、维度和数值任何一项不合格都不会进入写入事务。这个检查同时保护文档向量和 query 向量。

### 15.4 原子替换事务的内部顺序

`replaceDocument` 在一个事务中完成：

1. attempt>0 时更新对应 heads 行，条件要求 attempt 完全相等且 deleted=false；不满足返回 `ErrStaleIngestion`。
2. UPSERT documents，应用 revision、内容哈希和处理快照约束。
3. 删除旧 chunks；外键级联删除旧 retrieval_index。
4. 先插入所有父块，再插入子块，满足父子外键。
5. 插入子块的 search_content、halfvec 和检索 metadata。
6. Commit。

失败时全部回滚，读者不会看到“正文已经是新版本，但索引还剩旧版本”的中间状态。回滚使用 `context.WithoutCancel` 派生的 5 秒 context，避免请求刚被取消就连清理事务也无法执行。

Commit 已成功之后再观察到 context 取消，不可能把数据库时间倒回去。调用方看到取消错误时，必要时应查询当前版本确认是否已发布。

### 15.5 同一 attempt 的幂等条件

documents UPSERT 允许非版本模式双方 revision=0、新 revision 更高，或同 revision 且 ContentHash 与 ProcessConfig 相同。

处理快照不仅有分块参数，还包含标题、元数据、父子记录和完整检索文本摘要。相同 attempt 但换了索引输入会失败，避免“相同版本其实对应不同数据”。

它不是提前返回的 no-op：相同输入重试可能再次请求模型、删除并重建块。因此幂等是最终业务内容一致，并不是没有额外费用或时间戳变化。

向量本身不进入输入摘要。模型别名悄悄更换权重的问题仍需要 ModelRevision 档案解决。

### 15.6 独立 `PublishDocument` 与 Store 的区别

PG Indexer 同时可以实现 DocumentPublisher。单独 PublishDocument 保存文档、父子块及版本，但不请求 Embedding，也不写 PG retrieval_index，适合“外部索引已经写好，PG 只保存当前正文”的模式。

它要求正数 attempt，并使用正文发布输入快照。不要先用一个会立即原子发布的 PG Store，再让其他外部索引写入后才称作“全部索引成功后发布”，那会提前暴露新版本。

<a id="lifecycle"></a>

## 16. 版本预留、取消、删除与幂等

### 16.1 attempt 与 revision 的关系

attempt 是 heads 中“最新允许完成的任务编号”；revision 是 documents 中“已发布正文的版本编号”。同一次任务成功时 revision=attempt，但任务失败时两个数字可以不同。

```text
已有发布 revision=4
启动新任务 → heads.attempt=5，旧正文仍是 revision=4
新任务成功 → documents.revision=5
新任务失败 → documents.revision 仍是 4，heads.attempt 仍是 5
```

ReserveDocument 用 UPSERT 原子递增，同一 collection/document 共享一条 heads 行。

### 16.2 为什么旧任务晚完成不能覆盖新任务

```text
任务 A 预留 attempt=6，开始解析/向量化
任务 B 预留 attempt=7，开始解析/向量化
任务 B 提交成功，正文 revision=7
任务 A 到达写事务，发现 heads.attempt 已是 7
任务 A 返回 ErrStaleIngestion，不删除 B 的数据
```

事务中的 heads 条件更新会锁住这一行，与并发 Reserve、Invalidate、Delete 串行化。当前允许发布的任务可能先提交、然后新任务才预留，这也合理：读者先看完整旧版本，等新版本成功才替换。

### 16.3 CancelDocument 的准确含义

Service.CancelDocument 调用 InvalidateDocument：递增 attempt，并设 deleted=true，从版本上禁止当前旧任务继续发布。

它没有保存某个 Ingest 的 cancel function，也不会主动终止已经运行的解析器或模型请求。要结束那些工作，需要调用方取消相应 context。

它也不删除当前已发布文档。这里 heads.deleted 表示发布门闩，不等于检索层的“当前正文不可见”。旧的已发布版本仍可检索。

### 16.4 DeleteDocument 的准确含义

DeleteDocument 在事务中先提高 attempt、设置 deleted=true，再删除 documents；外键级联删除块与 PG 检索记录，heads 保留。

删除后旧任务无法重新发布。之后用户明确开始一次新导入，Reserve 会给更高 attempt 并清除 deleted，允许重新建立这篇文档。

外部索引场景下，删除当前正文会让 PublishedChunks 过滤旧命中，但不会自动调用所有外部后端删除物理记录，物理清理需要对应适配器或上层流程补充。

### 16.5 ListChunks 读的是什么

ListChunks 查询当前已发布文档的子块，提供原始正文、标题路径、父块关系和范围，用于展示、人工标注和证据定位。ChunkRecord 没有独立 revision 字段；SearchResult 的 revision 来自检索/权威回查时关联 documents。

它不是查询历史版本，也不是读取未发布任务的候选块。调用前检查 capability、context 和非空 collection/document。

<a id="search"></a>

## 17. Search 入口与 Eino 适配

### 17.1 Service 对查询做了哪些处理

`Service.Search` 接收 Query、CollectionID、Mode 和 Limit。Query 和 collection 去首尾空白后必须非空，Limit 不能为负。

| Mode | 选择的组件 |
| --- | --- |
| 空字符串、`hybrid` | Config.Retriever |
| `semantic` | Config.VectorRetriever |
| `keyword` | Config.KeywordRetriever |

Mode 当前按上述字符串精确匹配，没有统一转小写或去除 Mode 空格。默认入口叫什么与实际组件是两回事：若调用方注入其他原生 Retriever，`Mode="hybrid"` 仍调用那个组件，不会自动再造两路召回。

请求 Limit>0 覆盖 Search.FinalTopK；否则使用配置值，配置为 0 时补成 5。召回数量为：

```text
recallTopK = max(Config.RecallTopK, 最终 FinalTopK)
```

Service 把它作为 `retriever.WithTopK`，把 CollectionID 作为 `retriever.WithIndex` 传给组件。业务 Search 没有暴露任意 Eino option、任意 metadata filter 或全文 DSL。

### 17.2 查询前检查和父块工厂

BeforeSearch 在检索前调用，示例用于 collection 模型档案检查。启用 ExpandParents 且提供 ParentLoader 工厂时，再按 collection 创建读取器。

工厂初始化失败直接终止搜索；AllowParentFallback 只作用于后面 `LoadParents` 的普通读取错误，不会吞掉 BeforeSearch 或工厂错误。

Service 和 Pipeline 都会创建超时 context，默认 30 秒。嵌套 context 只能使截止时间更早，不会把外层超时延长。

### 17.3 原生文档如何变成内部结果

`NewEinoPipeline` 只在组件边界做转换。优先调用可选的 RetrieveWithDiagnostics，否则调用普通 Retrieve，再由 `retrieval.Results` 转成 SearchResult。

适配器用缓冲 channel 和 goroutine 等待组件响应；外层 context 到期可及时退出，即使某个组件暂时不遵守 context。它不能强制结束组件内部网络资源或永久阻塞的 goroutine，后端仍必须正确使用 context。

检索响应即使恰好返回空结果，也会再检查取消状态，避免把超时误报成一次成功的空召回。

### 17.4 Metadata 转换为何较严格

`retrieval.Documents` 把内部结果写回原生文档，复制可修改的顶层 metadata，保存原始正文和各类来源字段，并通过 `WithScore` 保存分数。

`retrieval.Results` 拒绝 nil 文档、空 ID、非有限 Score 和不合法数值元数据。

revision 支持整数、int64、整数 json.Number 和十进制字符串，必须非负；明确拒绝 float64 revision，避免 JSON 浮点丢失大整数版本精度。范围和排名字段允许有限、非负、整数形式的常见数字类型，缺失通常回退到 0。

普通 Eino 文档没有这些 metadata 仍可用于基础检索，但不自动具备可靠的文档版本、父子引用和来源坐标。跨 JSON/外部索引保存 revision 时，推荐十进制字符串。

设置 WithIndex 时，普通适配器可以补齐缺失 CollectionID，但会拒绝已有的其他 collection。独立发布过滤器要求命中本身携带正确 collection，条件更严格。

<a id="retrievers"></a>

## 18. 向量和 BM25 检索

### 18.1 PostgreSQL 两路的公共限制

默认 TopK=50，最大 TopK=200。构造配置 TopK=0 可以补默认；实际调用的 TopK 必须在 1～200。

两路都要求有效 collection、非空 query，过滤 `enabled=true`，并关联当前 chunks/documents 读取正文和 revision。当前不支持 SubIndex 和通用 DSL；传入这些能力会明确失败，而不是默默忽略。

CollectionID 实现数据范围隔离，调用方仍需自行做用户权限检查。

### 18.2 向量检索的具体 SQL 思路

首先对原始 query 请求一个向量。query 不会加文档标题，也没有 Query Rewrite。检查返回数量恰好为 1，再校验维度和 halfvec 数值。

查询用 MATERIALIZED CTE 先取最近候选：

```sql
-- 仅示意结构；真实字段和完整 SQL 以 retriever.go 为准。
WITH nearest AS MATERIALIZED (
    SELECT chunk_id, embedding <=> $1 AS distance
    FROM retrieval_index
    WHERE collection_id = $2 AND enabled = TRUE
    ORDER BY embedding <=> $1
    LIMIT $3
)
SELECT ..., 1 - nearest.distance AS score
FROM nearest
JOIN chunks ...
JOIN documents ...
ORDER BY score DESC, chunk_id ASC;
```

`<=>` 是余弦距离，Score=`1-distance`。HNSW 为近似检索，召回候选并不是穷举全部向量后的严格真值。

默认阈值 0.15，示例为了观察完整候选改成 0。阈值允许范围 0～1，所以负余弦相似度的候选会被过滤。先拿 TopK 再在 Go 中过滤阈值，不会自动补满被过滤的空位。

### 18.3 BM25 检索的具体过程

query 使用 SQL 参数绑定，交给 pg_search 的 `|||` 文本检索算子；排名来自 `pdb.score(ri.id)`。

```sql
-- 同样是流程示意。
SELECT ..., pdb.score(ri.id) AS score
FROM retrieval_index ri
JOIN chunks ...
JOIN documents ...
WHERE ri.collection_id = $1
  AND ri.enabled = TRUE
  AND ri.search_content ||| $2::text
ORDER BY pdb.score(ri.id) DESC, chunk_id ASC
LIMIT $3;
```

BM25 不调用 Embedding。默认阈值 0.30，示例设为 0。阈值与分数为非负实数，没有 1 的上限。

它搜索的是带标题与路径的 search_content；返回的 Content 则是 chunks 中的原始正文。传入 query 参数避免了应用层字符串拼接 SQL，但检索表达式如何解释仍由 pg_search 决定。

### 18.4 原始通道分数保留在哪里

向量结果 MatchType=vector，并保存 VectorScore；关键词结果 MatchType=keyword，并保存 KeywordScore。后续融合不会用同一个字段覆盖掉两路原始分数。

两路 SQL 都把 documents.revision 作为文本写入 metadata，以便进入原生 Eino 文档后仍准确还原大整数版本。

<a id="hybrid"></a>

## 19. 并发混合检索与 RRF

### 19.1 Hybrid 不依赖 PostgreSQL

`retriever/hybrid.go` 的输入是两个原生 Eino Retriever，因此可以把 PG 向量替成其他向量库，把 PG BM25 替成其他关键词后端，融合算法无需改变。

Hybrid 的整体 timeout 默认 30 秒，可额外设置 ChannelTimeout。两个通道并发执行；权重为 0 的通道不执行，也不需要提供对应组件。

### 19.2 TopK 与 option 的传播

每个通道的基础候选数是 `max(ChannelTopK, 本次 Hybrid TopK)`。每路的实现专有 Options 追加在公共选项之后，所以可以覆盖通道 TopK。全局 Index 最后追加，防止一路专有配置把 collection 改到其他范围。

公共 Index、SubIndex、Embedding 和 DSL 会传播到通道。公共 ScoreThreshold 不会作为同一阈值传播到两路，因为余弦相似度和 BM25 量纲不同；它作用于融合后的 Score。每路自己的阈值使用各自 Options 或后端配置。

最终 RRF 后按 Hybrid TopK 截断，才进入精排和父块扩展。

### 19.3 失败策略的精确差别

| 情况 | strict | allow_partial |
| --- | --- | --- |
| 一路普通后端错误，另一路成功 | 整体失败 | 使用成功一路并记录降级 |
| 一路自己的 ChannelTimeout，整体 context 仍有效 | 整体失败 | 使用另一路并记录降级 |
| 整体 context 取消/到期 | 整体失败 | 整体失败 |
| 通道返回 context.Canceled | 整体失败 | 整体失败 |
| 两个有效通道都失败 | 整体失败 | 整体失败 |
| 一路被权重 0 关闭 | 使用另一有效路 | 使用另一有效路 |
| 一路成功但结果为空 | 正常融合 | 正常融合 |

“结果为空”不是“通道失败”。两路都成功时 ModeUsed 仍可能显示 hybrid，即使只有一路实际贡献结果。禁用一路也不应被诊断成错误降级。

失败信息经 SafeErrorText 处理并限制长度；Diagnostics 包含 ModeUsed、Degraded 和 Channels，便于示例解释实际用了什么。

### 19.4 RRF 为什么用排名而不用原始分直接相加

向量分通常在较窄范围，BM25 可能大于 1，直接做 `0.7*cosine + 0.3*BM25` 会让分值尺度影响权重。

RRF 使用各通道排名。默认 K=60，VectorWeight=0.7，KeywordWeight=0.3。排名从 1 开始：

```text
rawRRF(d) = wv / (K + vectorRank(d)) + wk / (K + keywordRank(d))
缺失某一路时，该项为 0

maxRRF = (wv + wk) / (K + 1)
Score(d) = rawRRF(d) / maxRRF
```

例如一个块向量第 1、关键词第 3，则默认 Score 约为：

```text
(0.7/61 + 0.3/63) / (1/61) ≈ 0.99048
```

K 越大，靠前名次之间的差距通常越平缓；权重表示通道对排名融合的影响。融合分不是概率。

### 19.5 排名、去重和稳定排序

`prepareRankedChannel` 保留 Retriever 输入顺序，不根据 Score 重新排序；去掉空 ID、非有限分及重复复合身份，排名在过滤后重新从 1 连续编号。

原生 Retriever 必须按相关性提供结果；即使文档没有分数，只要顺序正确也可以参与 RRF。

去重键包含 collection、document、revision 和 chunk。融合时保留两路分数、名次和 MatchType；分数相等时按复合身份排序，保证稳定结果。

### 19.6 单路情况下分数处理不同

只有向量时，保留向量 Score。只有关键词时，若第一名原始分大于 1，会除以第一名分数；否则不做这次缩放，非正值归零。原始 KeywordScore 仍保留。

因此独立 keyword 模式的 Score 是原始 BM25，Hybrid 退回关键词单路时的 Score 可能经过缩放。对比模式时要看 KeywordScore，不能假设所有 Score 的量纲一样。

### 19.7 RRF 零值规则

只有整个 `RRFConfig{}` 都为零时才恢复默认 `{K:60, VectorWeight:0.7, KeywordWeight:0.3}`。

如果保留 K=60，却把两路权重都设成 0，校验会失败；两路不能同时禁用。一个权重可以为 0，权重总和必须为正，但不要求恰好为 1。K 必须在 0～1,000,000 之间，权重必须有限且非负。

NewHybrid 对非法配置返回错误；直接调用 FuseRRF 对非法配置返回 nil。正常业务应从有校验的构造入口使用。

<a id="rerank"></a>

## 20. 精排模型、阈值与评分融合

### 20.1 Retriever、Scorer、Engine 各负责什么

Retriever 缩小候选范围；Scorer 对 query 与每段文本打相关性分；Engine 处理模型错误、阈值、排序与诊断。

`Scorer.Score(ctx, query, passages)` 必须按输入顺序返回等长分数。HTTP Provider 将服务端排序结果重新对齐输入下标，因此 Engine 不依赖远程服务返回顺序。

默认 PassageBuilder 使用标题、标题路径和子块正文。父块扩展在精排之后，所以模型评估的是实际命中子块，默认没有用更大的父正文重新打分。

### 20.2 HTTP Provider 发了什么请求

`provider/rerank/http` POST 完整 Endpoint，JSON 结构为：

```json
{
  "model": "配置的精排模型",
  "query": "用户问题",
  "documents": ["候选 0 的完整文本", "候选 1 的完整文本"],
  "top_n": 2
}
```

top_n 要求返回全部候选评分。响应 results 中每项需要 index 和 relevance_score，数量必须等于输入，index 不能重复或越界，评分必须有限且在 [0,1]。

默认 HTTP 超时 30 秒。提供自定义 HTTPClient 时，以该 client 行为为准。自定义 header 会复制，不能覆盖 Content-Type 或 Authorization 等受保护字段。成功响应读取上限 4 MiB；非 2xx 错误消息限制到 1000 字节。

### 20.3 `NewEngine` 为什么没有返回 error

构造时先 Validate，再保存 configErr；执行 Rerank 时才返回该错误。示例在组装前主动 Validate，所以配置错误仍能提早发现。

normalize 只补 TopK、MaxCandidates、默认 PassageBuilder，以及“三个组合权重同时为 0”时的默认权重。不会把所有 0 都替成默认值。Threshold=0、FallbackMinScore=0 等可以明确生效。

### 20.4 模型关闭、缺失与故障的路径

- 没有候选：Outcome=no_candidates。
- Disabled=true：保留召回顺序，Outcome=disabled。
- 没有 Scorer：保留召回顺序，Outcome=no_model。
- 普通模型错误、评分数量错误或非有限评分：保留候选顺序，Outcome=model_error，并记录错误。
- context 取消或 deadline：返回错误，不转成成功降级。

Disabled/NoModel 在 MaxCandidates 截断之前返回；真正启用模型时才把输入限制到 MaxCandidates。模型故障时回退的是这份已截断候选池。

HTTP Provider 对超出 [0,1] 的评分报错；其他自定义 Scorer 若返回有限的越界数，Engine 会 clamp 到 [0,1]。两层行为不同。

### 20.5 阈值降低与 Top1 兜底

先保留 `ModelScore >= Threshold` 的候选。只有全部不满足且 Threshold>DegradeFloor 时，降低一次：

```text
effectiveThreshold = max(Threshold × DegradeFactor, DegradeFloor)
```

仍无结果时，如果最高模型分达到 FallbackMinScore，则仅保留这一个；否则返回空结果，Outcome=all_below_threshold。

默认 Threshold=0.3，DegradeFloor=0.3，因此默认配置不会因为 DegradeFactor=0.7 就实际降低阈值。示例把 floor 改成 0.15，才可能从 0.3 降到 0.21。

如果原阈值已选中任意结果，不会再降低阈值凑满 TopK。

### 20.6 最终组合分数

通过模型阈值之后，对候选计算：

```text
BaseScore = clamp01(进入精排时的 Score)
Score = clamp01(
    ModelWeight × ModelScore
  + BaseWeight  × BaseScore
  + SourceWeight × 候选来源权重
)
```

默认权重为 0.6、0.3、0.1；示例使用 0.8、0.2、0。

来源权重优先读取 SearchResult.SourceWeight，否则尝试 metadata 的 `rag_source_weight`，最后 clamp；默认普通文件没有这项权重时为 0。配置 SourceWeight 是来源项的系数，不是给每个文档自动赋一个来源值。

组合权重均在 [0,1]，总和不能大于 1；全为 0 则恢复默认，不表示“所有分数清零”。按组合分降序、ChunkID 升序排列。

独立 BM25 的原始分大于 1 时会被 clamp，因此直接 keyword 精排与 Hybrid 精排的 BaseScore 贡献可能不同。

### 20.7 统一 Pipeline 为什么用 `RerankCandidates`

Pipeline 需要先聚合父上下文，再限制最终上下文数量。因此调用 RerankCandidates，临时把 Engine.TopK 设成输入候选数，保留全部通过模型阈值的候选。

这条路径不会执行有效的 MMR 多样性选择，也不会由 Rerank.TopK 提前截成 5 条。最终数量由 Search.FinalTopK 决定。这个区别是配置学习中必须掌握的一点。

<a id="mmr"></a>

## 21. 独立 MMR 的具体实现

### 21.1 代码已经实现，当前统一 Search 没有启用

`rerank.Engine.Rerank` 的成功精排路径会调用 applyMMR。`Service.Search → Pipeline.Search → RerankCandidates` 把 TopK 放大后，候选数不超过 TopK，applyMMR 直接返回，所以不会做多样性筛选。

修改示例 `Rerank.MMRLambda` 不会改变统一 Search 的结果。Query Rewrite 当前也没有实现；没有隐藏的 LLM 调用替用户改写问题。

### 21.2 当前 MMR 使用什么相似度

代码使用文本 token 集合的 Jaccard，不是 Embedding 向量余弦。

英文等连续字母和数字组成词，转成小写；CJK 字符按单字进入集合；重复次数不影响集合。两个集合相似度为交集大小除以并集大小，空集合相似度为 0。

它能减少高度重复文本，但不等于识别语义相同的所有说法。

### 21.3 贪心选择公式

先选组合相关性最高的一条。之后对剩余候选逐一计算：

```text
MMR(d) = lambda × relevance(d)
       - (1-lambda) × max(Jaccard(d, 已选每一条))
```

每轮选择 MMR 最大项，平分时按 ChunkID，直到 TopK。lambda 越大越偏向相关性；越小越偏向避免重复。但第一条始终按最高相关性选择，即使 lambda=0。

MMR 只改变选择顺序和集合，不把 Score 改写成 MMR 值，所以输出列表可能不再按原 Score 单调下降。

### 21.4 范围与成本

只有候选多于 TopK 才需要选择；精排关闭、没有模型或模型失败的回退路径不执行 MMR。

token 集合按 ChunkID 缓存，但候选与每条已选项的相似度仍重复遍历，候选数与 TopK 很大时成本会增加。当前没有专用 pairwise 相似度缓存，也没有 MMR 的独立业务开关。

如果以后接入统一 Search，应明确放在子块选择前还是父块分组后，并考虑与 FinalTopK、Evidence 的关系；当前文档只描述已有行为，不把这个建议当作已实现功能。

<a id="context"></a>

## 22. 父块上下文、分组与引用证据

### 22.1 为什么不直接把 child.Content 改成 parent.Content

Pipeline 先为每条结果把 Context 字段初始化成子块。启用扩展时，仅替换 Context 字段，保留原始 ChunkID、Content、StartRune、EndRune。

这样下游可以用 EffectiveContent 获取回答上下文，用原始字段显示真实命中证据。换成父正文后，子块身份不会消失。

### 22.2 批量加载与版本一致性

收集所有不重复 ParentChunkID 后，ParentLoader 用一次 `ANY(ids)` 查询同 collection 的 parent_text，并关联 documents 读 revision。

缺少父块时保留子块。加载到父块后，若父 document/collection 明确与子块不同，或 revision 不相等，则不扩展，并写入 parent_revision 诊断。

这处理“召回完成之后，文档恰好被替换”的竞争窗口，防止用旧子块证据拼新版本父正文。

### 22.3 读取失败何时可以回退

AllowParentFallback=true 时，普通父块读取错误退回子块，并记录 parent_expansion；context.Canceled 和 DeadlineExceeded 始终返回错误。

找不到某个父 ID 与整个数据库读取失败是不同情况：前者可以局部保留子块，后者需要按配置判断。

### 22.4 先分组，再截断

CollapseSameParent 实际按 EffectiveChunkID 的复合身份分组。开启扩展后，同一父块下的子块可合并；关闭扩展后，EffectiveChunkID 仍为子块 ID，不能仅凭 ParentChunkID 自动合并。

一个组保留输入顺序中第一条作为代表，通常是排序最高的命中；不取均值、不累加分数。所有实际命中的子块追加到 Evidence。

再按 FinalTopK 截断组，因此 FinalTopK 是最终上下文组数量，不一定等于子块命中数量。

```text
子块 C1、C2 → 父块 P1
子块 C3     → 父块 P2
FinalTopK=2，开启扩展与分组

结果 1：上下文 P1，证据 C1、C2
结果 2：上下文 P2，证据 C3
```

若先截前两条子块再分组，就只能得到 P1，这正是当前流程先分组的原因。

### 22.5 Evidence 的边界

Evidence 只聚合召回并经处理保留下来的子块，不枚举父块所有子块。父正文里恰好出现相关内容，不能凭此计为那个未命中子块的召回。

分组函数不主动按 Evidence ID 去重；示例评测额外去重。输入只有一条时，分组函数直接返回，Evidence 可能仍为空，评测应同时把主 ChunkID 计入证据。

finalize 会把 Rerank.Diagnostics.ResultCount 更新为最终上下文组数，CandidateCount 仍描述此前模型处理候选。理解两个数时需要看阶段。

<a id="backends"></a>

## 23. 替换后端与外部索引发布

### 23.1 PG 一体化模式

当前示例使用 PG Indexer 原子保存文档、块、向量和 BM25。Retriever 直接关联当前文档与块，天然读取已发布版本；不需要额外 PublishedChunks 回查。

Service 注入 Loader、Indexer、默认/向量/关键词 Retriever、ChunkReader、Lifecycle 和 ParentLoader，即可运行。

### 23.2 外部索引模式为什么多一个发布阶段

假设向量库 A 和 BM25 库 B 与正文数据库不同，三个后端不能共享 PG 事务。使用下面流程：

```text
预留版本
→ 构造带版本 ID 的索引文档
→ A.Store
→ B.Store
→ 校验两者完整返回 ID
→ Publisher 保存当前正文、父子关系与 revision
→ 检索时回查当前正文并过滤未发布/旧版本命中
```

`indexer.NewMulti` 依次调用多个原生 Indexer，每次复制文档和顶层 metadata，校验返回 ID 完整且不被更名。失败即停止；之前成功的外部写入不会回滚。

版本化 ID 和发布过滤使这些残留不会自动变成用户可见的新版本，但它们仍占外部存储空间。

### 23.3 `WithPublishedChunks` 与 `ResolveChunks`

过滤包装器要求 WithIndex 范围非空、命中 metadata 的 collection 正确，再由 PublishedChunkReader 回查当前版本。

PG ResolveChunks 检查 document、revision 与当前正文一致，并返回当前原始正文、范围、父关系和 metadata，同时保留输入排名与分数。未发布、已删除或旧 revision 的候选被过滤。

Hybrid 的每路可先包装一次，防止非法候选进入 RRF；Service 的三种入口再包装一层，保护业务级最终结果。具体组装时要知道这可能产生额外回查，而不是误以为它完全免费。

### 23.4 切换 Eino 组件时必须保存的约定

接口对齐不代表所有后端无需配置即可拥有相同语义。至少检查：

| 约定 | 为什么需要 |
| --- | --- |
| Store 保留输入 ID，并返回完整唯一集合 | 发布与版本身份依赖它 |
| 保存 collection、document、revision 等 metadata | 过滤当前版本和隔离范围 |
| revision 使用整数安全形式 | JSON float64 可能丢失精度 |
| 保存 raw body 与 ParentChunkID | 索引文本不能冒充引用正文 |
| WithIndex 与 WithTopK 的后端语义明确 | 控制知识库范围和候选数 |
| Retriever 输出相关性有序 | RRF 使用排名，不重排通道 |
| query/document 使用相同 Embedding 空间 | 避免模型混用 |
| 后端遵守 context | 释放网络资源与后台工作 |
| 原文/父块存储继续提供生命周期能力 | 原生索引接口不负责整篇文档发布 |

因此无需在 `internal/rag` 根目录继续新增 `qdrant.go`、`milvus.go` 等组装文件。组件适配放入对应 indexer/retriever/provider 子包或直接采用 eino-ext；具体组合放在程序入口。

### 23.5 外部模式尚未覆盖的工程工作

发布过滤后不重新补满 TopK；大量旧版本残留可能挤占外部召回候选，导致有效召回变少。没有通用的外部索引清理、跨后端事务、自动重试队列或历史版本列表。

增加新后端时要同时规划残留清理和候选过采样，不能只把 Store/Retrieve 编译通过当成完整迁移完成。

<a id="configuration"></a>

## 24. 配置生效规则与完整示例

本节把前面各层的配置放在同一处。当前没有统一的 RAG YAML 配置加载器；示例直接用 Go 结构体，环境变量只提供连接信息和模型参数。

### 24.1 按配置来源分类

| 配置来源 | 配置内容 | 何时生效 |
| --- | --- | --- |
| `rag.Dependencies` | Loader、Indexer、读取、发布、生命周期和关闭能力 | 创建 Service |
| `rag.Config` | 各 Retriever、RecallTopK、Reranker、InputBuilder、Search、回调 | 创建 Service，并用于每次调用 |
| `rag.IngestRequest` | 来源、身份、分块、父子大小 | 本次导入 |
| `rag.SearchRequest` | query、collection、mode、limit | 本次查询 |
| 原生 Eino options | Index、TopK、Embedding 等 | 直接调用对应组件或 Service 内部适配 |
| `postgresConfig` | 把上述组件构造参数集中起来 | 仅属于当前 example 的组装代码 |

不要给业务请求随意添加一个 `EnableMMR` 或 `EnableRewrite` 字段就认为会生效；这些能力必须有实际调用路径。

### 24.2 当前 example 的全部主要参数

| 参数 | 当前示例值 | 影响 |
| --- | --- | --- |
| DatabaseURL | 环境变量，缺失时本地 rag_example | 连接实验数据库 |
| EnsureSchema | true | 创建扩展和迁移，不创建数据库 |
| Loader.ParseOptions.Timeout | 1 分钟 | 单次文件解析时间 |
| MaxInputBytes / MaxOutputBytes | 32 MiB / 32 MiB | 文件与 Markdown 预算 |
| MaxExpandedBytes / MaxArchiveEntries | 256 MiB / 8192 | Office 等压缩输入预算 |
| IncludePageNumbers | false | 页码标记是否进入正文 |
| ExcludeHeadersAndFooters | true | 解析时排除页眉页脚 |
| NormalizeLineEndings | true | Loader 阶段换行统一；Service 也统一 |
| OCRLanguage | 空字符串 | OCR 语言提示，不是 OCR 构建开关 |
| Embedding.APIKey / BaseURL / Model | 环境变量 | OpenAI 兼容模型服务 |
| Embedding.ModelRevision | 空字符串 | 模型权重版本身份 |
| Embedding.Dimensions | 0 | 省略请求 dimensions，实际必须 1024 维 |
| Embedding.Timeout | 30 秒 | 模型请求超时 |
| Embedding.InputBudget | 单条 8192、单批 65536 | 完整文本预算，默认按 UTF-8 字节计数 |
| Indexer.EmbeddingBatchSize | 32 | 模型批量条数上限 |
| InputBuilder.TitleKeys | title、_title、file_name | 标题优先级 |
| InputBuilder.ContextHeaderKey | rag_context_header | 标题路径来源 |
| CollectionID | rag-file-example | 实验知识库 |
| DocumentID | uploaded-document | 每次重跑替换同一业务文档 |
| ParentChild | true | 父子模式 |
| ParentChunkSize / ChildChunkSize | 2048 / 384 | 父窗口与子窗口 |
| Splitter.Strategy | auto | 自动尝试策略 |
| Splitter.ChunkSize | 512 | 普通模式目标大小 |
| Splitter.ChunkOverlap | 80 | 普通块/父块基础重叠；0 关闭 |
| Splitter.Separators | 段落、换行、中文句号 | 递归优先级 |
| Splitter.TokenLimit | 0 | 不附加近似 token 目标 |
| Splitter.Languages | zh | 子父分块语言提示 |
| Hybrid.TopK | 30 | 示例同时作为 Service.RecallTopK |
| Hybrid.ChannelTopK | 50 | 混合每路候选上限 |
| Vector.ScoreThreshold | 0 | 向量原始分过滤 |
| Keyword.ScoreThreshold | 0 | BM25 原始分过滤 |
| Hybrid.RRF | K=60，权重 0.7/0.3 | 排名融合 |
| Hybrid.FailurePolicy | strict | 单路故障整体失败 |
| Hybrid.Timeout / ChannelTimeout | 20 秒 / 15 秒 | 混合整体与单路超时 |
| enableRerank | false | 示例构造远程 Scorer 的开关 |
| RerankProvider.Timeout | 开启时 15 秒 | 精排 HTTP 超时 |
| Rerank.MaxCandidates | 30 | 送模型候选数量 |
| Rerank.Threshold | 0.3 | 模型相关性过滤 |
| DegradeFactor / DegradeFloor | 0.7 / 0.15 | 降低一次阈值 |
| FallbackMinScore | 0.1 | 无结果时 Top1 最低模型分 |
| ModelWeight / BaseWeight / SourceWeight | 0.8 / 0.2 / 0 | 精排后的组合权重 |
| Search.FinalTopK | 5 | 最终上下文组数 |
| Search.Timeout | 30 秒 | 整个业务查询 |
| ExpandParents / CollapseSameParent | true / true | 扩展父正文并聚合证据 |
| AllowParentFallback | false | 普通父块读取错误默认失败 |
| SearchRequest.Limit | 0 | 使用配置的最终数量 |
| modes | hybrid、semantic、keyword | 同一问题做三种对比 |

示例要求预算统一写在 Embedding.InputBudget；若另外填写 Indexer.InputBudget，会被 postgresConfig.Validate 拒绝。组装时统一把前者传入 Indexer，避免索引与查询预算不一致。

### 24.3 哪些 0 表示默认，哪些 0 表示关闭

| 配置 | 0 的实际含义 |
| --- | --- |
| Splitter.ChunkSize | 使用默认 512 |
| Splitter.ChunkOverlap | 关闭重叠 |
| Splitter.TokenLimit | 不执行近似 token 目标 |
| Parent/ChildChunkSize | 使用默认 4096/384 |
| Embedding.Dimensions | 不发送维度请求参数 |
| Budget.MaxInputTokens / MaxBatchTokens | 使用本地默认预算 |
| Indexer.EmbeddingBatchSize | 使用默认条数 |
| Search.FinalTopK | 补成默认 5 |
| SearchRequest.Limit | 不覆盖配置 |
| Search.Timeout / Hybrid.Timeout | 执行时使用默认超时 |
| Hybrid.ChannelTimeout | 共用整体截止时间 |
| 原始 Vector/BM25 ScoreThreshold | 合法的显式 0 阈值 |
| RRF 某一路权重 | 关闭该混合通道，另一权重必须有效 |
| 整个 RRFConfig 零值 | 恢复默认 RRF 配置 |
| Rerank.Threshold / DegradeFloor / FallbackMinScore | 合法的显式 0 |
| Rerank.TopK / MaxCandidates | normalize 补默认 |
| 三个 Rerank 组合权重全为 0 | 恢复默认组合权重 |
| Rerank.MMRLambda | 独立 Rerank 的合法多样性参数 0 |

bool 没有“没填”和“明确 false”的区分。`search.Config{}` 的 ExpandParents=false；`search.DefaultConfig()` 才显式设为 true。建议从每个 DefaultConfig 开始修改，让意图清晰。

### 24.4 TopK 的逐层覆盖

```text
example:
  Hybrid.TopK=30 → Service.RecallTopK=30
  Search.FinalTopK=5

hybrid:
  vector 最多 50 + keyword 最多 50
  → 去重/RRF 后最多 30
  → 可选模型精排，最多评分 30
  → 父块扩展、分组
  → 最多 5 个上下文组

semantic / keyword:
  Service.WithTopK(30) 覆盖组件自己的配置 TopK
  → 同样的后续精排/上下文处理
  → 最多 5 个上下文组
```

如果把本次 Limit 改成 40，Service 会把 recallTopK 提到至少 40；但仍受具体后端 TopK<=200 和模型 MaxCandidates 等限制。FinalTopK 是上限，不能保证凑满。

特别地，示例把 Hybrid.TopK 也传给 Service.RecallTopK。若将它设为 0，Service 使用 `max(0,FinalTopK)`，不会必然按原生 Hybrid 构造默认召回 50。最终生效要看调用 options，而不仅看构造默认值。

### 24.5 跑真实文档的完整操作

先准备满足扩展要求的数据库并创建 `rag_example`，再在项目根目录执行：

```bash
cd /Users/sda1_hacker/Desktop/humbert/humbert-agent

export RAG_DATABASE_URL='postgres://postgres:postgres@127.0.0.1:5432/rag_example?sslmode=disable'
export OPENAI_API_KEY='你的向量模型服务密钥'
export OPEN_BASE_URL='你的 OpenAI 兼容 API 基础地址'
export RAG_EMBEDDING_MODEL='服务实际支持且输出 1024 维的模型名'

go run ./examples/5-rag '/绝对路径/你的文档.pdf'
```

OPEN_BASE_URL 是 API 基础地址；RERANK_ENDPOINT 是完整请求地址。示例不会自动读取 `.env` 或桌面应用的模型设置。

main.go 的 exampleConfig 中缺省模型名沿用 `qwen3.7-text-embedding`，不能据此认定真实服务支持这个名字，应显式设置实际模型。

程序按下面顺序执行：

1. 打印配置开关。
2. 初始化组件并导入真实文件。
3. 打印分块策略、父子数量及解析警告。
4. ListChunks 展示所有当前子块，附来源范围和标题路径。
5. 输入针对这篇文档的问题。
6. 输入全部相关子块的展示编号，程序转换成本次版本真实 ID。
7. 依次执行配置 modes，并打印分数、诊断、证据、最终上下文和指标。

```text
请输入针对该文档的问题：文档中的设备如何恢复出厂设置？
请输入所有相关子块编号，用空格分隔：3 4 9
```

编号必须根据实际展示内容选择。实验 collection 最好只放这篇文档；否则其他文档参与召回却没有标注，会影响 Precision。

每次启动仍会重新导入，当前没有“仅查询”子命令。相同内容和切分参数可以复用人工编号，但必须核对新的块内容；版本化 ID 每次可能变化。

### 24.6 从最简单配置开始理解

在 exampleConfig 中修改已有赋值，避免前面赋值后被后面覆盖：

```go
// 第一轮：普通递归块，不扩展父正文，不开启远程精排。
ingest.ParentChild = false
ingest.Splitter.Strategy = chunker.StrategyRecursive
ingest.Splitter.ChunkSize = 512
ingest.Splitter.ChunkOverlap = 80
cfg.Search.ExpandParents = false
cfg.Search.CollapseSameParent = false
// enableRerank 保持 false。
```

理解这条链之后，再分别改 auto/heading、父子分块、RRF 权重和精排。开启精排时把 enableRerank 改成 true，并设置 RERANK_ENDPOINT、RERANK_MODEL、RERANK_API_KEY；只改 Disabled 而没有创建 Provider 不够。

### 24.7 改参数后需要做什么

| 改动 | 是否重导入 | 是否重标注 |
| --- | --- | --- |
| 文件、解析规则、页码标记 | 是 | 是，范围和正文可能变化 |
| Strategy、大小、重叠、父子模式、TokenLimit | 是 | 是，相关子块集合变化 |
| Embedding 模型/版本/地址、输入文本规则 | 新 collection 重导入 | 核对并重新绑定当前版本 ID |
| 召回模式、TopK、通道阈值、RRF | 索引本身不要求 | 保持同一分块标注 |
| 精排开关、阈值、权重、候选数 | 索引本身不要求 | 保持同一分块标注 |
| 扩展、分组、FinalTopK、请求 Limit | 索引本身不要求 | 保持子块标注，注意指标单位 |
| HTTP 超时和预算 | 不改变已有向量空间 | 核对失败与正常结果的区别 |

同一实验每次尽量只改一个因素。分块变了之后，直接横比单个问题 Recall 时还应注意“相关子块数量也变了”，分母不是天然固定的。

### 24.8 用业务 API 串起流程的最小阅读片段

以下代码对应 example main 的核心逻辑，放在现有 example 的包内理解；省略的是打印和人工输入，不是另一个需要新建的工程：

```go
// 配置与真实文件来源。
cfg, request, _ := exampleConfig("/绝对路径/你的文档.pdf")

// setup 组装 Eino 原生组件及文档生命周期能力。
service, err := newPostgresRAG(ctx, cfg)
if err != nil {
    return err
}
defer service.Close()

// 导入：解析、切分、构造文本、向量化、原子发布。
_, err = service.Ingest(ctx, request)
if err != nil {
    return err
}

// 读取当前可检索子块，用于展示和标注。
chunks, err := service.ListChunks(ctx, request.CollectionID, request.DocumentID)
if err != nil {
    return err
}
_ = chunks // 实际 example 会逐个打印，并让用户标注相关编号。

// 查询：召回、融合、可选精排、父块扩展及最终选择。
response, err := service.Search(ctx, rag.SearchRequest{
    CollectionID: request.CollectionID,
    Query:        "针对真实文档的问题",
    Mode:         "hybrid",
    Limit:        5,
})
if err != nil {
    return err
}
for _, hit := range response.Results {
    // 给 LLM 的上下文从这里取；当前 example 只打印，不调用 LLM。
    fmt.Println(hit.EffectiveContent())
}
```

这段假设外层是可返回 error 的函数，并已导入 context、fmt 和 rag；实际可直接运行的完整程序在 examples/5-rag 中。

<a id="evaluation"></a>

## 25. Recall、Precision 与评测边界

### 25.1 标注和返回集合如何构造

人工 gold 是本次文档所有相关子块 ID 的集合，map 值均为 true。重复输入相同编号只计一次。标注在检索之前完成，避免只把已检出的块挑成标准答案。

`evidenceIDs` 从每条最终结果收集主 ChunkID 和所有 Evidence.ChunkID，跳过空 ID，按首次出现顺序去重。ContextChunkID 可能是父块，不能拿它与子块标注比较。

### 25.2 计算公式与一个可手算例子

```text
G = 全部人工相关子块
R = 最终结果的去重命中子块证据
TP = |G ∩ R|

Recall = TP / |G|
Precision = TP / |R|
```

例如 G={C2,C3,C7,C8}，R={C1,C2,C3,C5,C7}，则 TP=3，Recall=3/4=75%，Precision=3/5=60%。

有标注但空结果时两者为 0。检索失败另报失败，不算成一次正常零召回。

### 25.3 为什么这里的“准确率”是 Precision

通俗说法中的检索准确率通常指“检出来的内容中有多少相关”，对应 Precision。分类 Accuracy=`(TP+TN)/(TP+FP+FN+TN)` 需要定义未检索的负例全集；本例没有计算它。

更没有计算 LLM 回答正确率，因为当前流程不生成答案。Rerank.ModelScore 也不能直接当成答案正确率。

### 25.4 父块合并后的指标单位

FinalTopK=5 时，最多 5 条上下文组，但可能有 8 个子块证据。Precision 分母是 8，不是 5。若直接取代表 ChunkID 而忽略 Evidence，会漏掉真实命中的相关子块。

反过来，父正文覆盖了 20 个子块，不表示检索命中了这 20 个。只计实际证据，不能扩大召回率。

普通不分组模式接近子块 Recall@K/Precision@K；分组模式更准确的描述是“最终上下文中实际命中子块证据的召回率/精确率”。上下文完整性可能改善，但子块指标保持不变，这并不矛盾。

### 25.5 当前例子的评测范围

每次运行只评测一个用户输入的问题。耗时从 Search 调用前到成功返回，不包含入库、人工标注和打印。

它适合观察流程和单参数变化，不是稳定的多问题基准。实际准备长期调参时，可以固定真实文件、问题集和标注，并分别记录候选阶段与最终阶段指标；这属于后续扩展，当前程序没有自动做这些统计。

改变分块参数后，重叠可能导致同一答案出现于多个子块，标注应覆盖所有相关块。gold map 若被外部代码填入 false 值，当前函数仍用 len(gold) 当分母；当前人工标注入口只写 true，所以正常运行不触发这个歧义。

<a id="verification"></a>

## 26. 测试、异常定位和实现限制

### 26.1 测试覆盖了哪些核心约束

| 测试位置 | 关注点 |
| --- | --- |
| root 的 service/backend/ingestion/publication 测试 | 原生 Eino 接口、请求校验、解析前预留版本、外部完整发布与版本化父子引用 |
| chunker 下测试 | 原文覆盖、rune 坐标、保护区域、表头、重叠、标题路径、策略 fallback、父子映射和 token 目标 |
| documentparse 测试 | 真实临时文件、预算、输入格式、子进程转换与取消 |
| Transformer/Loader 测试 | Eino 文档映射、选项覆盖、默认 ID、文件元数据 |
| embeddinginput 测试 | 完整输入预检、批次预算、模型调用之前失败 |
| PG Indexer 测试 | 写入顺序、半精度向量、完整来源、取消/过期版本、同 attempt 快照、固定模型档案 |
| PG Retriever 测试 | 参数、SQL 范围、两路分数、父块批量加载 |
| retrieval 测试 | 原生文档转换、整数版本、复合身份、RRF 排名与单路行为 |
| hybrid 测试 | 并发通道、失败策略、超时、权重 0、options 范围传播 |
| HTTP Provider 测试 | 本地 HTTP 服务、错误响应、分数完整性和服务端排序回对齐 |
| rerank 测试 | 组合分、降阈值、Top1、全部拒绝、故障回退、取消及独立 MMR |
| search 测试 | 父块先分组后截断、跨版本保护、读取回退、没有扩展时的最终数量 |
| example 测试 | 配置关系、模型档案、Evidence 去重及父正文不虚增命中 |

多数数据库单元测试使用假的事务/行结果，验证调用和数据约束。真实数据库集成测试位于 indexer/postgres/integration_test.go，受 `HUMBERT_RAG_TEST_DATABASE_URL` 控制，未配置时跳过。

这些集成测试包含生命周期、向量/BM25 搜索、collection 隔离、外部索引发布过滤、父子外键与回滚、旧 schema 升级和并发 attempt 等场景。

### 26.2 如何运行检查

```bash
cd /Users/sda1_hacker/Desktop/humbert/humbert-agent

go test ./internal/documentparse ./internal/rag/... ./examples/5-rag

go test -race ./internal/rag/... ./examples/5-rag

# 不连接数据库或模型，仅查看示例入口。
go run ./examples/5-rag --help
```

只有正确提供真实测试数据库，才会执行对应 integration 用例。运行 example 的真实文件流程，还需要可用的模型服务和合法 1024 维输出。

本学习文档是源码分析产物，没有据此宣称某个真实数据库或远程模型已经连通。

### 26.3 按错误定位代码

| 现象/错误 | 先查哪里 | 常见解释 |
| --- | --- | --- |
| Unsupported extension / 空解析结果 | Loader、documentparse | 格式不支持，扫描件没有可用 OCR，或正文为空 |
| input exceeds token budget | embeddinginput.Plan | 完整输入含标题/表头超预算，不只是 body 长 |
| dimension mismatch / 无效向量 | PG makeHalfVector、queryToHalfVector | 输出维度不是 1024、非有限数或全零 |
| ErrIncompleteSource | PG Store | 把带 source metadata 的局部分块直接当完整文档保存 |
| ErrStaleIngestion | heads 与 replaceDocument | 更晚任务、取消或删除已使旧 attempt 失效 |
| ErrEmbeddingProfileMismatch | collection profile | 当前模型或文本构造与已绑定 collection 不同 |
| ErrUnboundLegacyIndex | collection profile | 历史索引没有模型档案，程序不能自动认定兼容 |
| migration checksum mismatch | migrations.go | 已执行历史迁移语句被修改 |
| another collection | Hybrid/Eino/Published 包装器 | 后端返回了不属于请求范围的数据 |
| 召回少于 TopK | Retriever/RRF/阈值/发布过滤 | TopK 是上限，阈值或旧版本过滤后不补齐 |
| 精排配置开启但 Applied=false | Engine Diagnostics | disabled/no_model/model_error 等路径 |
| 精排后全空 | Engine 阈值 | 模型评分未满足阈值与 Top1 最低分 |
| ExpandParents=true 但没有父上下文 | 导入数量与 ParentLoader | 没有独立父块、父块缺失或版本变化 |
| 修改 Vector.TopK 无效果 | Service.WithTopK | 本入口 options 覆盖组件构造配置 |
| 修改 MMRLambda 无效果 | RerankCandidates | 统一 Search 未执行多样性选择 |

### 26.4 当前实现的明确边界

模块已经具备文档解析、结构分块、父子关系、统一文本、向量/BM25、RRF、可选精排、父上下文、版本安全及简单人工评测。但还要区分下面这些能力：

- 没有独立的知识库管理实体/API、Tag 管理、用户权限体系或完整文件存储服务；CollectionID 和 metadata 是基础，不等于完整知识管理产品。
- 当前数据库保存解析 Markdown，没有自动长期保存原始 PDF/Office 文件。
- 没有 RAG 的 LLM 答案生成、流式回答、引用渲染协议，也没有 Query Rewrite。
- 独立 MMR 已实现，统一 Search 暂未接入。
- 上下文扩展目前主要是直接父块，不是通用邻近块窗口、文档全文读取或 token 总预算装配器。
- 没有 GraphRAG、实体关系抽取、自动 Wiki、VLM 或 ASR。
- 没有作为 Agent 工具接入，也没有 RAG 的 HTTP/SSE/Wails UI 入口；当前 example 是 CLI。
- 没有通用任务队列、失败重试、外部索引清理、全链路 tracing 或历史版本浏览。
- halfvec 维度、部分索引参数和 BM25 分词规则仍是当前后端实现选择，尚非全部可配置。
- 核心 Chunker 没有 context 参数，Service 在分块前后检查取消；一次较大的 CPU 分块任务内部不会逐步响应取消。
- 正则分块、估算 token 和 Jaccard 都是工程近似，不是完整 Markdown parser、模型精确 tokenizer 或语义相似度模型。

这些边界不妨碍当前真实文档检索例子跑通，但要在以后加入能力时选择正确层次，而不是继续把所有逻辑堆入 Service。

### 26.5 建议的源码学习顺序

1. 先读 example 的 main.go，按 main 和 exampleConfig 查看流程与配置，知道输入、输出和配置如何串起来。
2. 读 rag.go、service.go、persistence.go，掌握业务流程和可选能力边界。
3. 读 Chunk/Batch/SearchResult 和 metadata 转换，掌握身份与坐标。
4. 从 Split → SplitText → unit/merge/overlap 读底层，再读标题、启发式和父子策略。
5. 读 IndexDocuments、Builder、Budget，知道模型实际收到什么。
6. 读 PG schema、Indexer、事务、heads 和 profile，理解发布安全。
7. 读 PG Retriever → Hybrid → RRF → Engine → Pipeline，追踪每次 Score 的变化。
8. 最后用实际文件运行 example，把每个字段与诊断对照，再读对应测试验证边界。

<a id="source-index"></a>

## 27. 逐文件、逐函数源码索引

下面覆盖 `internal/rag` 全部 57 个生产 Go 文件，并追加解析器、迁移入口和 example。每个类型/函数链接定位到当前声明行；代码之后变动时行号可能变化。职责列帮助定位阅读入口，具体分支和限制以前面各章的执行说明为准。

接口方法列在相应类型定义中，不属于独立 `func` 声明；常量、正则和 SQL 的关键语义已经在分块、metadata、数据库及配置章节解释。

### 27.1 业务服务与接口

#### [internal/rag/rag.go](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/rag.go:1)

业务入口、依赖声明、能力校验、文档管理和资源关闭。

| 声明（点击定位） | 类别 | 中文职责 |
| --- | --- | --- |
| [Dependencies](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/rag.go:18) | 类型 | Dependencies 声明运行所需组件，不根据 Indexer 的实际类型猜测业务能力。 |
| [Service](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/rag.go:41) | 类型 | Service 是唯一的业务入口。提供导入、检索及文档管理，不再包装另一层 Service。 |
| [New](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/rag.go:54) | 函数 | New 创建业务服务，允许只配置完整导入流程或只配置检索流程。 |
| [Service.ListChunks](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/rag.go:92) | 函数 | ListChunks 返回已发布的普通分块，供展示和人工标注使用。 |
| [Service.CancelDocument](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/rag.go:105) | 函数 | CancelDocument 作废正在处理的版本；上一次已发布版本仍可检索。 |
| [Service.DeleteDocument](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/rag.go:117) | 函数 | DeleteDocument 删除权威文档；外部索引残留通过版本检查过滤。 |
| [Service.Close](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/rag.go:129) | 函数 | Close 可重复调用，底层资源只释放一次。 |
| [documentScope](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/rag.go:141) | 函数 | documentScope 检查请求上下文并规范化知识库与文档 ID。 |

#### [internal/rag/service.go](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/service.go:1)

业务数据结构，以及完整文档导入和查询编排。

| 声明（点击定位） | 类别 | 中文职责 |
| --- | --- | --- |
| [ChunkRecord](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/service.go:40) | 类型 | ChunkRecord 保存权威正文和原文坐标；它不承担检索库接口的职责。 |
| [IngestionBatch](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/service.go:63) | 类型 | IngestionBatch 是一个完整文档版本，由原子 Indexer 或独立 Publisher 保存。 |
| [DocumentVersioner](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/service.go:87) | 类型 | DocumentVersioner 是 Eino 未提供的业务能力：解析前预留文档版本。 |
| [Config](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/service.go:93) | 类型 | Config 直接接受 Eino 组件。Retriever 是默认检索器，可以是下面的两路混合结果， |
| [IngestRequest](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/service.go:115) | 类型 | IngestRequest 是单次文档导入请求。 |
| [IngestDocumentResult](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/service.go:137) | 类型 | IngestDocumentResult 描述一个 Loader 输出 Document 的处理结果。 |
| [IngestResponse](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/service.go:152) | 类型 | IngestResponse 各文档的导入结果及父子块总数。 |
| [SearchRequest](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/service.go:161) | 类型 | SearchRequest 是最终查询入口。 |
| [Service.Ingest](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/service.go:173) | 函数 | Ingest 完成解析、切分与索引。业务调用不能覆盖索引范围或 Embedding 模型。 |
| [Service.Search](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/service.go:344) | 函数 | Search 选择 Eino 检索器，再复用精排与父块扩展流水线。 |
| [buildFlatBatch](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/service.go:421) | 函数 | buildFlatBatch 将普通 Split() 结果转换成持久化模型。 |
| [buildParentChildBatch](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/service.go:454) | 函数 | buildParentChildBatch 转换父子切分结果，保存父块身份与子块的关联关系。 |
| [buildChunkRecord](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/service.go:507) | 函数 | buildChunkRecord 保存正文、原文坐标和来源元数据，不生成模型向量。 |
| [documentTitle](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/service.go:542) | 函数 | documentTitle 按统一标题优先级取值，缺失时使用文档 ID。 |

#### [internal/rag/indexing.go](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/indexing.go:1)

版本化块 ID、完整返回 ID 校验及 Eino 索引输入转换。

| 声明（点击定位） | 类别 | 中文职责 |
| --- | --- | --- |
| [qualifyChunkIDs](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/indexing.go:15) | 函数 | qualifyChunkIDs 让不同版本的分块拥有独立 ID。 |
| [validateStoredIDs](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/indexing.go:33) | 函数 | validateStoredIDs 检查索引器返回的 ID 与全部子块一一对应，拒绝改名或重复。 |
| [IngestionOptions](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/indexing.go:52) | 类型 | IngestionOptions 补充 Eino Store 没有定义的“完整原文和父块”信息。 |
| [WithIngestionBatch](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/indexing.go:55) | 函数 | WithIngestionBatch 通过 Eino 实现专属选项传递完整文档批次。 |
| [IndexDocuments](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/indexing.go:61) | 函数 | IndexDocuments 只把子块交给索引组件，所有后端接收相同的检索文本。 |

#### [internal/rag/persistence.go](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/persistence.go:1)

原文发布、当前块读取、权威回查及文档生命周期接口。

| 声明（点击定位） | 类别 | 中文职责 |
| --- | --- | --- |
| [DocumentPublisher](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/persistence.go:12) | 类型 | DocumentPublisher 保存并发布完整原文与父子分块。 |
| [ChunkReader](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/persistence.go:17) | 类型 | ChunkReader 返回当前已发布文档的普通可检索分块，按 ChunkIndex 升序排列。 |
| [PublishedChunkReader](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/persistence.go:23) | 类型 | PublishedChunkReader 按文档版本校验外部索引命中，并补全权威正文。 |
| [DocumentLifecycle](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/persistence.go:28) | 类型 | DocumentLifecycle 与存储实现无关，保留版本预留、取消和删除能力。 |

#### [internal/rag/published.go](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/published.go:1)

把任意原生 Retriever 包装成只返回当前已发布版本的组件。

| 声明（点击定位） | 类别 | 中文职责 |
| --- | --- | --- |
| [published](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/published.go:12) | 类型 | 持有原生 Retriever 与 PublishedChunkReader 的过滤包装器。 |
| [WithPublishedChunks](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/published.go:19) | 函数 | WithPublishedChunks 包装原生 Eino Retriever，按权威文档库过滤未发布和过期版本。 |
| [published.Retrieve](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/published.go:27) | 函数 | Retrieve 执行原生检索，再过滤未发布、已删除和过期版本的分块。 |
| [published.RetrieveWithDiagnostics](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/published.go:33) | 函数 | 版本过滤保留混合检索的降级诊断，不能让包装隐藏某一路失败的信息。 |

#### [internal/rag/validation.go](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/validation.go:1)

完整文档批次、内容哈希、块类型、范围和父子关系校验。

| 声明（点击定位） | 类别 | 中文职责 |
| --- | --- | --- |
| [ValidateIngestionBatch](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/validation.go:19) | 函数 | ValidateIngestionBatch 在调用模型或存储前检查文档身份、分块范围和父子关系。 |

### 27.2 文件加载与解析

#### [internal/rag/loader/tabula/loader.go](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/loader/tabula/loader.go:1)

本地文件加载、格式校验、来源 ID、解析元数据与 OCR 警告转换。

| 声明（点击定位） | 类别 | 中文职责 |
| --- | --- | --- |
| [IDGenerator](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/loader/tabula/loader.go:108) | 类型 | IDGenerator 为解析后的原始文档分配稳定 ID，分块 ID 由后续切分阶段生成。 |
| [Config](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/loader/tabula/loader.go:111) | 类型 | Config 控制本地文件解析、页眉页脚过滤、可选 OCR 与原文换行归一化。 |
| [DefaultConfig](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/loader/tabula/loader.go:127) | 函数 | DefaultConfig 返回推荐的 RAG Loader 默认配置。 |
| [Loader](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/loader/tabula/loader.go:136) | 类型 | Loader 实现 Eino 文档加载接口，将本地文件解析为完整 Markdown。 |
| [NewLoader](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/loader/tabula/loader.go:141) | 函数 | NewLoader 创建一个 Tabula Loader。 |
| [Loader.Load](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/loader/tabula/loader.go:150) | 函数 | Load 校验本地文件，在受限子进程中解析，返回正文和来源元数据；当前不使用调用级选项。 |
| [validateSource](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/loader/tabula/loader.go:207) | 函数 | validateSource 验证 Eino Source 是否是当前 Loader 可以处理的本地文件。 |
| [SupportedExtension](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/loader/tabula/loader.go:244) | 函数 | SupportedExtension 判断格式是否受支持，兼容大小写以及带点或不带点的扩展名。 |
| [isRemoteURI](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/loader/tabula/loader.go:260) | 函数 | isRemoteURI 判断是否为 HTTP 地址；远程下载应在本地 Loader 之前完成。 |
| [buildMetadata](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/loader/tabula/loader.go:268) | 函数 | buildMetadata 保存文件来源、标题、格式、大小与解析诊断。 |
| [warningMetadata](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/loader/tabula/loader.go:300) | 函数 | warningMetadata 将解析警告转成可序列化文本，同时记录是否使用 OCR。 |
| [DefaultIDGenerator](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/loader/tabula/loader.go:322) | 函数 | DefaultIDGenerator 根据来源 URI 的 SHA-256 前 128 位生成稳定 ID；内容变化仍对应同一文档。 |

#### [internal/documentparse/parser.go](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/documentparse/parser.go:1)

临时文件快照、格式预检查、预算、解析子进程和 IPC 协议。

| 声明（点击定位） | 类别 | 中文职责 |
| --- | --- | --- |
| [Options](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/documentparse/parser.go:29) | 类型 | 输入、输出、ZIP 展开、超时、页眉页脚、OCR 和页码转换配置。 |
| [DefaultOptions](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/documentparse/parser.go:40) | 函数 | 返回文件、输出、解压、条目数和时间的默认预算。 |
| [Options.Validate](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/documentparse/parser.go:44) | 函数 | 拒绝负数和可能导致预算算术溢出的参数。 |
| [Options.effective](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/documentparse/parser.go:54) | 函数 | 把零值资源限制补为默认，不表示无限制。 |
| [Result](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/documentparse/parser.go:74) | 类型 | 解析 Markdown、Tabula 警告及源文件字节哈希。 |
| [workerRequest](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/documentparse/parser.go:79) | 类型 | 父进程传给子进程的快照路径及有效解析配置。 |
| [workerResponse](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/documentparse/parser.go:83) | 类型 | 子进程返回的解析结果、错误文本及资源超限标记。 |
| [init](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/documentparse/parser.go:89) | 函数 | 检测 worker 环境标记，执行 JSON 解析协议并在应用 main 前退出。 |
| [ExtractFile](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/documentparse/parser.go:112) | 函数 | 复制授权源文件快照、哈希、预检查，再调用受限解析子进程。 |
| [convert](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/documentparse/parser.go:209) | 函数 | 读取 UTF-8 文本或调用 Tabula 转 Markdown，并限制警告与正文输出。 |
| [preflight](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/documentparse/parser.go:252) | 函数 | 检查 PDF 标识及 ZIP 路径、条目数和声明解压大小。 |
| [contextReader](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/documentparse/parser.go:292) | 类型 | 在源文件 reader 外包一层 context 检查，用于可取消复制。 |
| [contextReader.Read](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/documentparse/parser.go:297) | 函数 | 在每次源文件读取前检查 context，使快照复制可以取消。 |
| [boundedWriter](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/documentparse/parser.go:304) | 类型 | 有界 bytes.Buffer、限制值与 exceeded 标记，用于控制 IPC 输出。 |
| [boundedWriter.Write](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/documentparse/parser.go:310) | 函数 | 限制保留的子进程输出字节，记录超限并消费完整写入。 |

### 27.3 核心分块算法

#### [internal/rag/chunker/chunk.go](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/chunker/chunk.go:1)

核心 Chunk、ChildChunk、父子结果及正文/标题路径组合。

| 声明（点击定位） | 类别 | 中文职责 |
| --- | --- | --- |
| [Chunk](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/chunker/chunk.go:11) | 类型 | Chunk 表示经过文档分块之后的一个文本块 是整个 RAG 核心的数据结构 |
| [Chunk.EmbeddingContent](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/chunker/chunk.go:35) | 函数 | EmbeddingContent 返回用于检索的文本 |
| [ChildChunk](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/chunker/chunk.go:44) | 类型 | ChildChunk 表示父子分块下的子块 |
| [ParentChildResult](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/chunker/chunk.go:53) | 类型 | ParentChildResult 表示父子分块的最终结果 |

#### [internal/rag/chunker/config.go](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/chunker/config.go:1)

策略常量、默认值、归一化、父子配置派生和切片复制。

| 声明（点击定位） | 类别 | 中文职责 |
| --- | --- | --- |
| [SplitterConfig](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/chunker/config.go:46) | 类型 | SplitterConfig 定义整个 Chunker 的分块配置。 |
| [DefaultSeparators](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/chunker/config.go:73) | 函数 | DefaultSeparators 返回默认递归分隔符。 |
| [DefaultConfig](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/chunker/config.go:85) | 函数 | DefaultConfig 返回一份默认 SplitterConfig。 |
| [NormalizeSplitterConfig](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/chunker/config.go:94) | 函数 | NormalizeSplitterConfig 对调用者传进来的配置应用基础默认值。 |
| [DeriveParentChildConfigs](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/chunker/config.go:113) | 函数 | DeriveParentChildConfigs 根据一份基础配置，生成 Parent 和 Child 配置。 |
| [SplitterConfig.Clone](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/chunker/config.go:140) | 函数 | Clone 复制配置中的切片，避免父子配置或 Eino 调用选项共享可变数组。 |

#### [internal/rag/chunker/validation.go](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/chunker/validation.go:1)

拒绝非法分块请求参数。

| 声明（点击定位） | 类别 | 中文职责 |
| --- | --- | --- |
| [SplitterConfig.Validate](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/chunker/validation.go:6) | 函数 | Validate 检查负数预算、未知策略和空分隔符；零值仍允许使用默认配置。 |

#### [internal/rag/chunker/normalize.go](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/chunker/normalize.go:1)

将 CRLF 和 CR 换行规范化为 LF。

| 声明（点击定位） | 类别 | 中文职责 |
| --- | --- | --- |
| [NormalizeLineEndings](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/chunker/normalize.go:10) | 函数 | NormalizeLineEndings 统一文档中的换行符 |
| [RuneLen](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/chunker/normalize.go:26) | 函数 | RuneLen 返回字符串中的 Unicode rune 数量。 |

#### [internal/rag/chunker/strategy.go](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/chunker/strategy.go:1)

高层 Split、自动策略链、回退和策略诊断。

| 声明（点击定位） | 类别 | 中文职责 |
| --- | --- | --- |
| [StrategyTier](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/chunker/strategy.go:4) | 类型 | StrategyTier 表示实际执行的分块算法；recursive 配置复用 legacy 算法。 |
| [TierRejection](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/chunker/strategy.go:13) | 类型 | TierRejection 记录某种分块策略被质量校验拒绝的原因。 |
| [Diagnostics](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/chunker/strategy.go:19) | 类型 | Diagnostics 记录最终策略、尝试顺序与拒绝原因，便于调整分块参数。 |
| [SelectStrategy](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/chunker/strategy.go:28) | 函数 | SelectStrategy 根据标题、章节和分页信号选择策略，递归切分始终作为最后的兜底。 |
| [ensureDefaults](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/chunker/strategy.go:45) | 函数 | ensureDefaults 补齐基础配置，再根据近似 token 预算收紧块大小和重叠范围。 |
| [resolveChainWithProfile](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/chunker/strategy.go:63) | 函数 | resolveChainWithProfile 将显式策略或自动策略转换成实际执行顺序。 |
| [Split](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/chunker/strategy.go:78) | 函数 | Split 执行分块和最终 token 预算处理；空策略使用递归切分。 |
| [SplitWithDiagnostics](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/chunker/strategy.go:87) | 函数 | SplitWithDiagnostics 与 Split 使用同一条执行路径，并额外返回策略诊断。 |
| [splitConfigured](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/chunker/strategy.go:95) | 函数 | splitConfigured 依次尝试策略；最后一种策略即使质量不达标也保留结果，避免丢失原文。 |
| [runTier](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/chunker/strategy.go:121) | 函数 | runTier 直接调用对应算法，不再通过 init 注册可变函数。 |

#### [internal/rag/chunker/profiler.go](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/chunker/profiler.go:1)

扫描文档结构与语言信号，计算用于策略选择的画像。

| 声明（点击定位） | 类别 | 中文职责 |
| --- | --- | --- |
| [DocProfile](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/chunker/profiler.go:9) | 类型 | DocProfile 记录文档长度、标题及章节信号，供自动选择分块策略使用。 |
| [DocProfile.HeadingDensity](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/chunker/profiler.go:71) | 函数 | HeadingDensity 返回标题行数与总行数的比例。 |
| [DocProfile.DominantHeadingLevel](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/chunker/profiler.go:80) | 函数 | DominantHeadingLevel 选择用于划分主章节的标题层级。 |
| [DocProfile.HeuristicMarkerTotal](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/chunker/profiler.go:101) | 函数 | HeuristicMarkerTotal 返回启发式结构信号的总数。 |
| [ProfileDocument](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/chunker/profiler.go:112) | 函数 | ProfileDocument 扫描文档结构，统计标题、章节、表格、代码及分页信号。 |
| [matchHeading](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/chunker/profiler.go:219) | 函数 | matchHeading 判断 Markdown ATX 标题并累计对应层级的数量。 |
| [calculateLineStats](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/chunker/profiler.go:238) | 函数 | calculateLineStats 计算平均行长与总体标准差。 |
| [detectProfileLanguages](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/chunker/profiler.go:263) | 函数 | detectProfileLanguages 仅采样文档前 4096 字节，以限制语言判断的扫描成本。 |

#### [internal/rag/chunker/patterns.go](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/chunker/patterns.go:1)

章节、标题、分页、空白、页脚正则及结构信号优先级。

| 声明（点击定位） | 类别 | 中文职责 |
| --- | --- | --- |
| [SentenceSeparators](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/chunker/patterns.go:80) | 函数 | SentenceSeparators 返回适合各语言的句末分隔符；英文要求标点后有空格，避免误切版本号和域名。 |
| [ChapterPatternsForLangs](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/chunker/patterns.go:92) | 函数 | ChapterPatternsForLangs 根据语言提示选择章节正则；未指定或无法识别时启用全部规则。 |
| [allChapterPatterns](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/chunker/patterns.go:118) | 函数 | allChapterPatterns 统一返回所有章节匹配规则。 |

#### [internal/rag/chunker/validator.go](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/chunker/validator.go:1)

按目标大小评估分块质量，帮助策略链决定是否回退。

| 声明（点击定位） | 类别 | 中文职责 |
| --- | --- | --- |
| [ValidationResult](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/chunker/validator.go:24) | 类型 | ValidationResult 保存分块质量是否合格及拒绝原因。 |
| [ValidateChunks](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/chunker/validator.go:39) | 函数 | ValidateChunks 检查空结果、未充分切分、过多小块和严重超长块，供策略回退使用。 |

#### [internal/rag/chunker/splitter.go](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/chunker/splitter.go:1)

递归算法对外入口，组织 unit 构建与合并。

| 声明（点击定位） | 类别 | 中文职责 |
| --- | --- | --- |
| [SplitText](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/chunker/splitter.go:4) | 函数 | SplitText 先识别保护区域，再递归切分普通文本，最后合并为带原文范围的块。 |

#### [internal/rag/chunker/recursive.go](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/chunker/recursive.go:1)

按分隔符优先级递归拆分普通正文，并保留分隔符。

| 声明（点击定位） | 类别 | 中文职责 |
| --- | --- | --- |
| [splitBySeparators](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/chunker/recursive.go:6) | 函数 | splitBySeparators 按分隔符优先级递归切分，完整保留分隔符以恢复原文。 |

#### [internal/rag/chunker/protected_span.go](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/chunker/protected_span.go:1)

保护公式、代码、链接、图片和表格，并转换 byte/rune 范围。

| 声明（点击定位） | 类别 | 中文职责 |
| --- | --- | --- |
| [span](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/chunker/protected_span.go:13) | 类型 | span 表示文本中的一个区间。 代码块、表格、latex 公式、链接、图片引用这些内容 |
| [protectedSpans](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/chunker/protected_span.go:70) | 函数 | protectedSpans 扫描文本，找到所有不能轻易拆分的区域 --> 找到文本中所有的 protected span |
| [protectedSpansRune](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/chunker/protected_span.go:154) | 函数 | protectedSpansRune 把 byte offset 的 Protected Span 转成 rune offset |

#### [internal/rag/chunker/unit.go](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/chunker/unit.go:1)

将普通正文与保护区域变成带来源范围的原子 unit。

| 声明（点击定位） | 类别 | 中文职责 |
| --- | --- | --- |
| [splitUnit](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/chunker/unit.go:18) | 类型 | splitUnit 保存切分中间单元；范围使用原文字符坐标，合成表头的起止位置相同。 |
| [buildUnitsWithProtection](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/chunker/unit.go:31) | 函数 | buildUnitsWithProtection 递归切分普通区域，保留保护区域，并转换成字符坐标。 |

#### [internal/rag/chunker/merge.go](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/chunker/merge.go:1)

合并 unit，应用软目标、绝对上限、表头与重叠。

| 声明（点击定位） | 类别 | 中文职责 |
| --- | --- | --- |
| [mergeUnits](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/chunker/merge.go:9) | 函数 | mergeUnits 按目标大小合并单元，并处理表头补充、重叠和原文范围。 |
| [buildChunk](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/chunker/merge.go:226) | 函数 | buildChunk 将若干 splitUnit 合成为最终 Chunk。 |
| [headerAlreadyPresent](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/chunker/merge.go:242) | 函数 | headerAlreadyPresent 判断正文或重叠部分是否已经包含表头，避免重复补充。 |
| [headerColumnRow](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/chunker/merge.go:271) | 函数 | headerColumnRow 从 Header 中找到真正的“列名行”。 |

#### [internal/rag/chunker/header_tracker.go](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/chunker/header_tracker.go:1)

跟踪 Markdown 表头、表格终止、列数变化和空表头补充。

| 声明（点击定位） | 类别 | 中文职责 |
| --- | --- | --- |
| [headerTrackerHook](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/chunker/header_tracker.go:19) | 类型 | headerTrackerHook 描述一种“上下文 Header”的识别规则。 |
| [headerTracker](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/chunker/header_tracker.go:61) | 类型 | headerTracker 跟踪当前有效的表头，在跨块时补充列名。 |
| [newHeaderTracker](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/chunker/header_tracker.go:94) | 函数 | newHeaderTracker 创建 Header Tracker。 |
| [headerTracker.update](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/chunker/header_tracker.go:104) | 函数 | update 根据当前单元更新表头，返回是否开始新表。 |
| [headerTracker.getHeaders](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/chunker/header_tracker.go:216) | 函数 | getHeaders 返回当前所有 Active Header。 |
| [isEmptyTableHeaderRow](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/chunker/header_tracker.go:257) | 函数 | isEmptyTableHeaderRow 判断表头是否没有有效列名。 |
| [extractSeparatorLine](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/chunker/header_tracker.go:284) | 函数 | extractSeparatorLine 从 Markdown Table Header 中 |
| [headerTracker.clearTableHeader](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/chunker/header_tracker.go:296) | 函数 | clearTableHeader 清除当前 Markdown Table Header。 |
| [headerTracker.endTableHeaderOnColumnMismatch](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/chunker/header_tracker.go:303) | 函数 | endTableHeaderOnColumnMismatch 在当前行与表头列数不一致时结束旧表头。 |
| [splitEndsWithParagraphBreak](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/chunker/header_tracker.go:324) | 函数 | splitEndsWithParagraphBreak 判断文本是否以空行结束。 |
| [tableRowColumnCount](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/chunker/header_tracker.go:330) | 函数 | 按管道拆分并处理首尾空列，估算列数；不完整支持转义管道语法。 |
| [firstTableRowColumnCount](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/chunker/header_tracker.go:353) | 函数 | firstTableRowColumnCount 从一段文本中找到 |
| [headerTableColumnCount](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/chunker/header_tracker.go:371) | 函数 | headerTableColumnCount 从表头中找到真实列数。 |
| [headerColumnMismatch](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/chunker/header_tracker.go:386) | 函数 | headerColumnMismatch 判断正文第一行的列数是否与待补充表头冲突。 |

#### [internal/rag/chunker/overlap.go](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/chunker/overlap.go:1)

从尾部按段落/行/句子边界提取有来源坐标的自然重叠。

| 声明（点击定位） | 类别 | 中文职责 |
| --- | --- | --- |
| [semanticOverlapBoundary](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/chunker/overlap.go:19) | 类型 | semanticOverlapBoundary 表示候选语义边界。 |
| [unitsText](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/chunker/overlap.go:26) | 函数 | unitsText 把 splitUnit 文本按顺序拼起来。 |
| [computeOverlap](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/chunker/overlap.go:35) | 函数 | computeOverlap 在空间预算内选择上一块的连续原文尾部，优先从语义边界开始。 |
| [semanticOverlapWindow](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/chunker/overlap.go:109) | 函数 | semanticOverlapWindow 提取连续原文尾部；合成表头和不连续区间会阻断窗口。 |
| [findSemanticOverlapBoundary](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/chunker/overlap.go:178) | 函数 | findSemanticOverlapBoundary 是方便测试和内部使用的入口。 |
| [findSemanticOverlapBoundaryEndingAtOrAfter](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/chunker/overlap.go:184) | 函数 | findSemanticOverlapBoundaryEndingAtOrAfter 查找指定位置之后的段落、换行或句末边界。 |
| [trimUnitsPrefix](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/chunker/overlap.go:324) | 函数 | trimUnitsPrefix 从 []splitUnit 开头删除 prefixLen 个 source rune。 |

#### [internal/rag/chunker/heading_hierarchy.go](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/chunker/heading_hierarchy.go:1)

维护六级标题状态，输出路径并清理旧深层标题。

| 声明（点击定位） | 类别 | 中文职责 |
| --- | --- | --- |
| [HeadingHierarchy](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/chunker/heading_hierarchy.go:6) | 类型 | HeadingHierarchy 按层级保存当前 Markdown 标题路径。 |
| [NewHeadingHierarchy](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/chunker/heading_hierarchy.go:12) | 函数 | NewHeadingHierarchy 创建一个空的 HeadingHierarchy。 |
| [HeadingHierarchy.Observe](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/chunker/heading_hierarchy.go:17) | 函数 | Observe 读取一个标题，同时清除已经失效的更深层标题。 |
| [HeadingHierarchy.Breadcrumb](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/chunker/heading_hierarchy.go:50) | 函数 | Breadcrumb 返回以分隔符连接的标题路径，供展示使用。 |
| [HeadingHierarchy.BreadcrumbWithHashes](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/chunker/heading_hierarchy.go:67) | 函数 | BreadcrumbWithHashes 返回保留标题层级标记的路径，供检索补充上下文。 |
| [HeadingHierarchy.Depth](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/chunker/heading_hierarchy.go:92) | 函数 | Depth 返回当前最深有效 Heading Level。 |
| [HeadingHierarchy.Reset](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/chunker/heading_hierarchy.go:97) | 函数 | Reset 清空整个 Heading 上下文。 |

#### [internal/rag/chunker/heading_splitter.go](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/chunker/heading_splitter.go:1)

按章节切分、定位子标题路径，并合并同路径小块。

| 声明（点击定位） | 类别 | 中文职责 |
| --- | --- | --- |
| [headingBoundary](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/chunker/heading_splitter.go:6) | 类型 | headingBoundary 保存章节起点；文档前言使用空标题。 |
| [splitByHeadings](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/chunker/heading_splitter.go:12) | 函数 | splitByHeadings 按主要标题层级划分章节，过大章节交给递归切分。 |
| [findHeadingBoundaries](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/chunker/heading_splitter.go:113) | 函数 | findHeadingBoundaries 查找主要标题层级及更高层级的章节起点，忽略代码块。 |
| [observeDeeperHeadings](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/chunker/heading_splitter.go:173) | 函数 | observeDeeperHeadings 更新章节内部更深层标题的上下文。 |
| [sectionBreadcrumb](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/chunker/heading_splitter.go:206) | 类型 | sectionBreadcrumb 表示: 从 runeStart 开始，后面的内容进入 breadcrumb 所描述的标题上下文。 |
| [buildSectionBreadcrumbIndex](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/chunker/heading_splitter.go:212) | 函数 | buildSectionBreadcrumbIndex 记录长章节内标题上下文的变化位置。 |
| [breadcrumbAtOffset](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/chunker/heading_splitter.go:274) | 函数 | breadcrumbAtOffset 查找指定字符位置生效的标题路径。 |
| [coalesceTinyHeadingChunks](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/chunker/heading_splitter.go:289) | 函数 | coalesceTinyHeadingChunks 合并相邻小块，仅合并原文连续且上下文兼容的片段。 |
| [commonHeadingPrefix](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/chunker/heading_splitter.go:350) | 函数 | commonHeadingPrefix 返回两条标题路径共有的完整标题行。 |

#### [internal/rag/chunker/heuristic_splitter.go](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/chunker/heuristic_splitter.go:1)

扫描非标准章节信号、累计结构块和对齐重叠。

| 声明（点击定位） | 类别 | 中文职责 |
| --- | --- | --- |
| [boundary](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/chunker/heuristic_splitter.go:9) | 类型 | boundary 保存结构边界的字符位置与优先级；同一位置保留最可信的信号。 |
| [splitByHeuristics](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/chunker/heuristic_splitter.go:15) | 函数 | splitByHeuristics 根据章节、分页和分隔线划分结构块，再按大小合并与递归拆分。 |
| [findHeuristicBoundaries](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/chunker/heuristic_splitter.go:129) | 函数 | findHeuristicBoundaries 查找章节、分页、全大写标题、页脚及空白分隔信号。 |
| [dropBoundsInsideSpans](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/chunker/heuristic_splitter.go:270) | 函数 | dropBoundsInsideSpans 删除严格位于保护区域内部的边界，保留区域两端。 |
| [allRuneIndices](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/chunker/heuristic_splitter.go:303) | 函数 | allRuneIndices 返回指定文本所有出现位置的字符偏移，供结构边界计算使用。 |
| [appendChunk](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/chunker/heuristic_splitter.go:324) | 函数 | appendChunk 追加原文区间对应的分块，并更新顺序编号。 |
| [appendOversizeBlock](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/chunker/heuristic_splitter.go:352) | 函数 | appendOversizeBlock 递归拆分过大结构块，将局部坐标转换成整篇原文坐标。 |
| [applyOverlapAligned](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/chunker/heuristic_splitter.go:386) | 函数 | applyOverlapAligned 计算下一块的重叠起点，优先对齐结构或换行边界，并保证前进。 |

#### [internal/rag/chunker/tokens.go](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/chunker/tokens.go:1)

语言估计和字符/token 的近似换算。

| 声明（点击定位） | 类别 | 中文职责 |
| --- | --- | --- |
| [ApproxTokenCount](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/chunker/tokens.go:25) | 函数 | ApproxTokenCount 根据字符数量与语言比例近似估算 token 数。 |
| [ApproxTokenCountFromRuneLen](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/chunker/tokens.go:34) | 函数 | ApproxTokenCountFromRuneLen 使用已知字符数估算 token，避免重复扫描正文。 |
| [DetectLanguage](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/chunker/tokens.go:54) | 函数 | DetectLanguage 根据字符分布粗略判断语言，用于切分和预算估算。 |
| [isGermanUmlaut](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/chunker/tokens.go:105) | 函数 | isGermanUmlaut 判断是否是典型德语字符。 |
| [hasGermanWords](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/chunker/tokens.go:115) | 函数 | hasGermanWords 只扫描前 512 字节的常见德语词，作为拉丁文本的辅助语言信号。 |
| [containsLower](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/chunker/tokens.go:143) | 函数 | containsLower 执行 ASCII 大小写不敏感查找，不创建全文小写副本。 |
| [CharsForTokenLimit](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/chunker/tokens.go:173) | 函数 | CharsForTokenLimit 将近似 token 预算换算成字符预算。 |

#### [internal/rag/chunker/budget.go](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/chunker/budget.go:1)

对加入表头和标题路径后的块执行最终近似 token 目标。

| 声明（点击定位） | 类别 | 中文职责 |
| --- | --- | --- |
| [enforceTokenTarget](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/chunker/budget.go:6) | 函数 | enforceTokenTarget 为无法继续切分的段落和保护块提供最终近似预算处理；索引器仍检查完整模型输入。 |

#### [internal/rag/chunker/parent_child.go](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/chunker/parent_child.go:1)

父块再切子块、映射全篇坐标、省略重复父块和合并路径。

| 声明（点击定位） | 类别 | 中文职责 |
| --- | --- | --- |
| [SplitParentChild](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/chunker/parent_child.go:6) | 函数 | SplitParentChild 先切父块，再切用于检索的子块；完全重复的单一父块不保存。 |
| [SplitParentChildWithDiagnostics](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/chunker/parent_child.go:12) | 函数 | SplitParentChildWithDiagnostics 返回父子分块和父块策略诊断，子块仍独立选择策略。 |
| [splitParentChild](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/chunker/parent_child.go:20) | 函数 | splitParentChild 共用父子切分流程，并将子块范围映射回整篇原文。 |
| [mergeBreadcrumbs](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/chunker/parent_child.go:123) | 函数 | mergeBreadcrumbs 合并父子标题路径，并去除连接处重复的标题行。 |

### 27.4 Eino 转换器、文本与模型输入

#### [internal/rag/transformer/chunker/transformer.go](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/transformer/chunker/transformer.go:1)

把核心 Chunker 适配成原生 Eino Transformer。

| 声明（点击定位） | 类别 | 中文职责 |
| --- | --- | --- |
| [IDGenerator](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/transformer/chunker/transformer.go:17) | 类型 | IDGenerator 根据来源文档、输入位置和分块信息生成 ID，供业务定制。 |
| [Config](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/transformer/chunker/transformer.go:24) | 类型 | Config 直接复用核心切分配置，仅额外提供 Eino 文档 ID 生成规则。 |
| [DefaultConfig](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/transformer/chunker/transformer.go:30) | 函数 | DefaultConfig 返回基础切分配置；空策略使用递归切分，自动策略需显式设置。 |
| [Transformer](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/transformer/chunker/transformer.go:38) | 类型 | Transformer 适配 Eino 文档转换接口，实际分块算法由核心 chunker 实现。 |
| [NewTransformer](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/transformer/chunker/transformer.go:43) | 函数 | NewTransformer 复制切片配置并设置默认 ID 生成器，避免调用方修改影响组件。 |
| [transformerOptions](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/transformer/chunker/transformer.go:56) | 类型 | transformerOptions 保存当前调用的配置覆盖，不修改组件默认值。 |
| [WithSplitterConfig](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/transformer/chunker/transformer.go:62) | 函数 | WithSplitterConfig 为当前 Transform 调用覆盖切分配置，并复制可变切片。 |
| [WithIDGenerator](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/transformer/chunker/transformer.go:71) | 函数 | WithIDGenerator 临时覆盖某一次 Transform 的 ID 生成规则。 |
| [Transformer.Transform](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/transformer/chunker/transformer.go:80) | 函数 | Transform 切分输入文档，保留来源元数据；每个输出块使用独立的顶层元数据副本。 |
| [chunkToDocument](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/transformer/chunker/transformer.go:138) | 函数 | chunkToDocument 将核心分块转换为 Eino 文档，保留正文、来源身份和字符范围。 |
| [DefaultIDGenerator](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/transformer/chunker/transformer.go:164) | 函数 | DefaultIDGenerator 使用来源 ID 与分块序号生成稳定 ID；来源无 ID 时使用输入位置。 |

#### [internal/rag/searchcontent/builder.go](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/searchcontent/builder.go:1)

统一构造标题、标题路径和正文的检索表示。

| 声明（点击定位） | 类别 | 中文职责 |
| --- | --- | --- |
| [Builder](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/searchcontent/builder.go:11) | 类型 | Builder 为向量、关键词和精排构造统一文本表示，原文范围由正文模型单独保存。 |
| [DefaultBuilder](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/searchcontent/builder.go:21) | 函数 | DefaultBuilder 返回当前推荐的检索文本构造规则。 |
| [Builder.Build](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/searchcontent/builder.go:33) | 函数 | Build 拼接标题、标题路径与正文；返回值用于模型输入，不用于原文坐标映射。 |
| [BuildText](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/searchcontent/builder.go:43) | 函数 | BuildText 将标题、标题路径和正文拼成模型输入，只去除完全相同的部分。 |
| [Builder.Title](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/searchcontent/builder.go:55) | 函数 | Title 根据配置依次寻找文档标题。 |
| [Builder.ContextHeader](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/searchcontent/builder.go:76) | 函数 | ContextHeader 返回 Chunk Breadcrumb。 |
| [metadataString](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/searchcontent/builder.go:91) | 函数 | metadataString 只接受字符串元数据，避免把复杂对象意外格式化成检索文本。 |

#### [internal/rag/embeddinginput/budget.go](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/embeddinginput/budget.go:1)

完整模型输入预检、批次规划和原生 Embedder 包装。

| 声明（点击定位） | 类别 | 中文职责 |
| --- | --- | --- |
| [Budget](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/embeddinginput/budget.go:15) | 类型 | Budget 限制完整模型输入与单批请求的 token 数。可注入真实分词器；默认以 UTF-8 字节数作保守估算，限制值需按实际模型配置。 |
| [DefaultBudget](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/embeddinginput/budget.go:25) | 函数 | DefaultBudget 返回本地默认保护值，不代表任何模型的官方上下文长度。 |
| [Budget.Validate](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/embeddinginput/budget.go:28) | 函数 | Validate 拒绝负数预算，零值在 Effective 中补齐。 |
| [Budget.Effective](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/embeddinginput/budget.go:36) | 函数 | Effective 补齐预算和计数函数，不修改调用方配置。 |
| [Batch](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/embeddinginput/budget.go:51) | 类型 | Batch 一次模型请求的左闭右开输入区间。 |
| [Budget.Plan](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/embeddinginput/budget.go:54) | 函数 | Plan 在请求模型前校验所有输入，同时限制每批条数与总 token 数。 |
| [limitedEmbedder](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/embeddinginput/budget.go:84) | 类型 | 持有原生 Embedder 和完整输入预算的分批调用包装器。 |
| [Limit](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/embeddinginput/budget.go:90) | 函数 | Limit 包装 Eino Embedder，拒绝超长输入并按批请求，不截断原文。 |
| [limitedEmbedder.EmbedStrings](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/embeddinginput/budget.go:95) | 函数 | EmbedStrings 按预算分批生成向量，保持输入顺序并校验向量数量。 |

#### [internal/rag/embeddinginput/profile.go](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/embeddinginput/profile.go:1)

向量空间配置快照及稳定 SHA-256 身份。

| 声明（点击定位） | 类别 | 中文职责 |
| --- | --- | --- |
| [Profile](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/embeddinginput/profile.go:12) | 类型 | Profile 描述向量空间身份，不包含凭据和请求限额。模型别名背后的权重变化时，应同步修改 Revision。 |
| [Profile.JSON](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/embeddinginput/profile.go:23) | 函数 | JSON 返回稳定字段顺序的配置快照，供模型档案保存与比较。 |
| [Profile.ID](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/embeddinginput/profile.go:26) | 函数 | ID 根据配置快照计算向量空间标识，防止不同模型混用索引。 |

#### [internal/rag/provider/embedding/openai/embedder.go](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/provider/embedding/openai/embedder.go:1)

配置并创建 Eino OpenAI 兼容 Embedder，加入输入预算。

| 声明（点击定位） | 类别 | 中文职责 |
| --- | --- | --- |
| [Config](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/provider/embedding/openai/embedder.go:24) | 类型 | Config 描述兼容 OpenAI 协议的向量服务及请求设置。 |
| [DefaultConfig](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/provider/embedding/openai/embedder.go:44) | 函数 | DefaultConfig 返回请求超时等默认设置，模型与凭据由调用方提供。 |
| [Config.Validate](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/provider/embedding/openai/embedder.go:51) | 函数 | Validate 检查模型、地址、维度与请求超时。 |
| [New](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/provider/embedding/openai/embedder.go:77) | 函数 | New 校验配置并创建 Eino 原生 Embedder，复用 eino-ext 的协议与回调实现。 |

#### [internal/rag/provider/rerank/http/client.go](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/provider/rerank/http/client.go:1)

精排 HTTP 协议、响应预算、分数完整性和输入顺序还原。

| 声明（点击定位） | 类别 | 中文职责 |
| --- | --- | --- |
| [Config](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/provider/rerank/http/client.go:37) | 类型 | Config 描述兼容 Jina 或 Cohere 协议的精排服务，Endpoint 必须是完整请求地址。 |
| [DefaultConfig](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/provider/rerank/http/client.go:54) | 函数 | DefaultConfig 返回精排服务的默认请求超时。 |
| [Config.Validate](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/provider/rerank/http/client.go:61) | 函数 | Validate 检查服务地址、模型与请求超时。 |
| [Client](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/provider/rerank/http/client.go:93) | 类型 | Client 精排 HTTP 客户端，按输入顺序还原服务返回的相关性分数。 |
| [NewClient](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/provider/rerank/http/client.go:102) | 函数 | NewClient 校验配置并创建精排客户端，优先使用调用方提供的 HTTPClient。 |
| [rerankRequest](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/provider/rerank/http/client.go:136) | 类型 | 精排 POST 的 model、query、documents、top_n 请求体。 |
| [rerankResponse](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/provider/rerank/http/client.go:143) | 类型 | 包含 results 数组的精排 JSON 响应。 |
| [rerankResult](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/provider/rerank/http/client.go:147) | 类型 | 单个输入下标 index 与 relevance_score。 |
| [Client.Score](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/provider/rerank/http/client.go:153) | 函数 | Score 请求精排分数，并按响应中的 index 对齐输入顺序；不能直接使用服务返回的排序。 |

### 27.5 索引组合与 PostgreSQL 写入

#### [internal/rag/indexer/multi.go](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/indexer/multi.go:1)

顺序组合多个原生 Indexer，保护输入并校验完整 ID 集合。

| 声明（点击定位） | 类别 | 中文职责 |
| --- | --- | --- |
| [multi](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/indexer/multi.go:14) | 类型 | 保存按顺序执行的多个原生 Eino Indexer。 |
| [NewMulti](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/indexer/multi.go:20) | 函数 | NewMulti 将同一批子块依次写入独立向量索引和关键词索引。 |
| [multi.Store](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/indexer/multi.go:33) | 函数 | Store 将同一批分块写入所有索引，保护输入元数据并校验后端保留全部分块 ID。 |

#### [internal/rag/indexer/postgres/pool.go](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/indexer/postgres/pool.go:1)

创建扩展、连接池和 pgvector 类型注册。

| 声明（点击定位） | 类别 | 中文职责 |
| --- | --- | --- |
| [EnsureExtensions](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/indexer/postgres/pool.go:14) | 函数 | EnsureExtensions 使用普通连接安装 pgvector 与 ParadeDB，再创建注册向量类型的连接池。 |
| [NewPool](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/indexer/postgres/pool.go:43) | 函数 | NewPool 创建连接池，并为每条连接注册 pgvector 编解码器；数据库扩展须先安装。 |

#### [internal/rag/indexer/postgres/health.go](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/indexer/postgres/health.go:1)

数据库/扩展最低版本及 schema 历史检查。

| 声明（点击定位） | 类别 | 中文职责 |
| --- | --- | --- |
| [rowQueryer](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/indexer/postgres/health.go:12) | 类型 | 只要求 QueryRow 的最小查询接口，供启动检查及其测试使用。 |
| [BackendStatus](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/indexer/postgres/health.go:17) | 类型 | BackendStatus 数据库与扩展版本，用于启动检查。 |
| [CheckExtensions](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/indexer/postgres/health.go:24) | 函数 | CheckExtensions 检查 PostgreSQL、pgvector 和 ParadeDB 是否满足当前 SQL 的要求。 |
| [BackendStatus.Validate](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/indexer/postgres/health.go:36) | 函数 | Validate 校验数据库与扩展的最低支持版本。 |
| [versionAtLeast](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/indexer/postgres/health.go:50) | 函数 | versionAtLeast 比较三段数字版本；无法识别的版本视为不满足要求。 |
| [CheckSchema](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/indexer/postgres/health.go:73) | 函数 | CheckSchema 检查数据库是否完成当前版本的 RAG 迁移。 |

#### [internal/rag/indexer/postgres/schema.go](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/indexer/postgres/schema.go:1)

文档、块、检索表及 HNSW/BM25 索引定义。

| 声明（点击定位） | 类别 | 中文职责 |
| --- | --- | --- |
| [EnsureSchema](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/indexer/postgres/schema.go:195) | 函数 | EnsureSchema 为开发环境执行版本化迁移。 |

#### [internal/rag/indexer/postgres/migrations.go](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/indexer/postgres/migrations.go:1)

带咨询锁、事务、版本和 checksum 的迁移。

| 声明（点击定位） | 类别 | 中文职责 |
| --- | --- | --- |
| [migration](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/indexer/postgres/migrations.go:16) | 类型 | 一版迁移的 version、name 与有序 SQL statements。 |
| [initialSchema](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/indexer/postgres/migrations.go:23) | 函数 | initialSchema 拼接初始表结构与索引语句，用于第一版迁移。 |
| [Migrate](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/indexer/postgres/migrations.go:64) | 函数 | Migrate 在数据库咨询锁保护下，以一个事务执行带版本和校验和的迁移，保留已识别旧表中的数据。 |

#### [internal/rag/indexer/postgres/indexer.go](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/indexer/postgres/indexer.go:1)

原生 Store 入口、完整来源识别、预算与 Embedding、halfvec 校验。

| 声明（点击定位） | 类别 | 中文职责 |
| --- | --- | --- |
| [Config](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/indexer/postgres/indexer.go:40) | 类型 | Config 只包含 PostgreSQL 实现所需的设置，检索文本在应用层统一构造。 |
| [DefaultConfig](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/indexer/postgres/indexer.go:56) | 函数 | DefaultConfig 返回默认向量请求批次大小。 |
| [storeOptions](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/indexer/postgres/indexer.go:58) | 类型 | 当前 Store 调用需要转发给 Embedder 的实现专有选项。 |
| [WithEmbeddingOptions](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/indexer/postgres/indexer.go:61) | 函数 | WithEmbeddingOptions 使用 Eino 实现专属 Option 向模型转发调用参数。 |
| [transaction](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/indexer/postgres/indexer.go:67) | 类型 | 这些内部接口用于测试事务，不是新增的检索库接口。 |
| [database](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/indexer/postgres/indexer.go:72) | 类型 | 只要求 Begin 的内部事务创建接口，帮助模拟数据库写入测试。 |
| [poolDatabase](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/indexer/postgres/indexer.go:75) | 类型 | 将实际 pgxpool.Pool 适配到内部 database 接口。 |
| [poolDatabase.Begin](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/indexer/postgres/indexer.go:78) | 函数 | Begin 将连接池事务适配到内部测试接口。 |
| [Indexer](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/indexer/postgres/indexer.go:81) | 类型 | Indexer Eino 原生索引器，在 PostgreSQL 中保存完整文档和检索索引。 |
| [NewIndexer](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/indexer/postgres/indexer.go:87) | 函数 | NewIndexer 校验配置并绑定连接池，连接池的关闭由组装层负责。 |
| [newIndexerWithDatabase](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/indexer/postgres/indexer.go:99) | 函数 | 使用可替换的内部数据库接口初始化 Indexer；供池封装和测试复用。 |
| [Indexer.Store](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/indexer/postgres/indexer.go:109) | 函数 | Store 是唯一索引入口。普通 Eino 文档以“一份原文、一个分块”保存；应用层切分的 |
| [embedTexts](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/indexer/postgres/indexer.go:191) | 函数 | embedTexts 按输入预算分批生成向量，校验数量、维度和半精度有效范围。 |
| [makeHalfVector](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/indexer/postgres/indexer.go:259) | 函数 | makeHalfVector 校验 Eino float64 embedding， |
| [marshalMetadata](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/indexer/postgres/indexer.go:309) | 函数 | marshalMetadata 将元数据编码为 JSON，空元数据仍保存为对象。 |
| [nullableString](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/indexer/postgres/indexer.go:323) | 函数 | nullableString 将空字符串映射为数据库 NULL。 |

#### [internal/rag/indexer/postgres/ingestion.go](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/indexer/postgres/ingestion.go:1)

原子替换事务、父子批次准备，以及外部索引完成后的正文发布。

| 声明（点击定位） | 类别 | 中文职责 |
| --- | --- | --- |
| [preparedIngestionChunk](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/indexer/postgres/ingestion.go:14) | 类型 | preparedIngestionChunk 是数据库写入前的内部模型。 |
| [Indexer.storeDocument](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/indexer/postgres/ingestion.go:20) | 函数 | storeDocument 共用唯一的文档替换事务，向量化仅由原生 Store 触发。 |
| [Indexer.PublishDocument](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/indexer/postgres/ingestion.go:67) | 函数 | PublishDocument 只保存权威原文和分块，可与外部向量库、BM25 库组合。 |
| [Indexer.replaceStoredDocument](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/indexer/postgres/ingestion.go:96) | 函数 | 同库原子入库和分离索引发布共用同一套事务、版本检查和分块保存规则。 |
| [prepareStoredChunks](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/indexer/postgres/ingestion.go:203) | 函数 | prepareStoredChunks 预先序列化分块元数据，避免进入事务后才发现编码错误。 |
| [insertStoredChunk](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/indexer/postgres/ingestion.go:226) | 函数 | insertStoredChunk 在当前事务内保存分块；检索索引仅由子块生成。 |

#### [internal/rag/indexer/postgres/revisions.go](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/indexer/postgres/revisions.go:1)

版本预留、作废、删除墓碑及独立 context 回滚。

| 声明（点击定位） | 类别 | 中文职责 |
| --- | --- | --- |
| [rollbackTransaction](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/indexer/postgres/revisions.go:13) | 函数 | rollbackTransaction 使用独立短超时回滚事务，避免请求取消后无法清理。 |
| [Indexer.ReserveDocument](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/indexer/postgres/revisions.go:20) | 函数 | ReserveDocument 在解析前预留新版本，使旧任务无法发布；上一版已发布文档仍可检索。 |
| [Indexer.InvalidateDocument](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/indexer/postgres/revisions.go:39) | 函数 | InvalidateDocument 作废正在处理的版本，保留上一版已发布内容。 |
| [Indexer.DeleteDocument](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/indexer/postgres/revisions.go:53) | 函数 | DeleteDocument 删除正文并留下墓碑，阻止迟到的入库任务复活文档。 |
| [Indexer.TombstoneDocument](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/indexer/postgres/revisions.go:59) | 函数 | TombstoneDocument 原子隐藏正文并返回删除截止版本，供分离索引清理使用。 |

#### [internal/rag/indexer/postgres/profile.go](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/indexer/postgres/profile.go:1)

collection 模型档案绑定与一致性检查。

| 声明（点击定位） | 类别 | 中文职责 |
| --- | --- | --- |
| [EnsureCollectionProfile](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/indexer/postgres/profile.go:17) | 函数 | EnsureCollectionProfile 绑定知识库的向量空间，不推断历史向量模型，也不删除现有索引数据。 |
| [Indexer.ensureProfile](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/indexer/postgres/profile.go:57) | 函数 | ensureProfile 校验知识库绑定的向量空间档案，防止更换模型后继续使用旧索引。 |

#### [internal/rag/indexer/postgres/snapshot.go](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/indexer/postgres/snapshot.go:1)

正文/索引输入摘要，并入同 attempt 幂等快照。

| 声明（点击定位） | 类别 | 中文职责 |
| --- | --- | --- |
| [processSnapshot](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/indexer/postgres/snapshot.go:12) | 函数 | 固定当前版本的实际输入，拒绝同一版本用不同分块或检索文本重试。 |

#### [internal/rag/indexer/postgres/reader.go](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/indexer/postgres/reader.go:1)

展示当前子块与回查外部命中的当前已发布正文。

| 声明（点击定位） | 类别 | 中文职责 |
| --- | --- | --- |
| [Indexer.ListChunks](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/indexer/postgres/reader.go:17) | 函数 | ListChunks 按原文顺序返回已发布文档的可检索分块，父分块不参与评估。 |
| [Indexer.ResolveChunks](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/indexer/postgres/reader.go:63) | 函数 | ResolveChunks 一次读取外部命中的权威分块，只保留当前已发布版本。 |
| [resolvePublishedChunks](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/indexer/postgres/reader.go:121) | 函数 | 版本匹配与正文替换共用此函数，便于独立验证删除、旧版本和跨集合命中。 |

### 27.6 结果模型、召回、融合与上下文

#### [internal/rag/retrieval/result.go](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/retrieval/result.go:1)

命中、上下文、Evidence、分数和复合身份数据模型。

| 声明（点击定位） | 类别 | 中文职责 |
| --- | --- | --- |
| [MatchType](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/retrieval/result.go:6) | 类型 | MatchType 表示一个 Chunk 是通过哪一路 Retriever 命中的。 |
| [SearchResult](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/retrieval/result.go:15) | 类型 | SearchResult 保存召回子块、分数和最终上下文；原始证据与父块内容分别保留，便于引用和评测。 |
| [HitEvidence](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/retrieval/result.go:71) | 类型 | HitEvidence 原始命中子块正文、分数与范围，父块分组后仍保留引用和评测依据。 |
| [SearchResult.EffectiveContent](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/retrieval/result.go:80) | 函数 | EffectiveContent 优先返回扩展后的上下文，没有上下文时返回子块正文。 |
| [SearchResult.EffectiveChunkID](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/retrieval/result.go:89) | 函数 | EffectiveChunkID 返回 EffectiveContent 对应的 Chunk ID。 |
| [SearchResult.IdentityKey](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/retrieval/result.go:98) | 函数 | IdentityKey 结合知识库、文档、版本和分块 ID 去重；仅有分块 ID 的独立结果仍可使用。 |

#### [internal/rag/retrieval/documents.go](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/retrieval/documents.go:1)

原生 Eino 文档与内部结果的严格双向映射。

| 声明（点击定位） | 类别 | 中文职责 |
| --- | --- | --- |
| [Documents](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/retrieval/documents.go:35) | 函数 | Documents 把内部的引用信息写入 Eino 文档；相关性分数使用官方 WithScore。 |
| [Results](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/retrieval/documents.go:54) | 函数 | Results 接收任意 Eino Retriever 的输出。没有 RAG 元数据时仍可进行基本检索； |
| [Revision](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/retrieval/documents.go:93) | 函数 | Revision 不接受 float64，防止大版本号在 JSON 反序列化后悄悄损失精度。 |
| [text](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/retrieval/documents.go:117) | 函数 | text 读取字符串元数据，缺失或类型不符时返回空字符串。 |
| [number](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/retrieval/documents.go:120) | 函数 | number 读取数值元数据；缺失视为零，类型错误返回非数供上层拒绝。 |
| [CloneMetadata](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/retrieval/documents.go:142) | 函数 | CloneMetadata 返回可写的顶层副本，避免不同分块或检索组件相互修改元数据。 |

#### [internal/rag/retrieval/diagnostics.go](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/retrieval/diagnostics.go:1)

实际召回通道及错误降级诊断数据。

| 声明（点击定位） | 类别 | 中文职责 |
| --- | --- | --- |
| [ChannelDiagnostic](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/retrieval/diagnostics.go:4) | 类型 | ChannelDiagnostic 单路召回失败信息，供混合检索降级诊断使用。 |
| [Diagnostics](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/retrieval/diagnostics.go:10) | 类型 | Diagnostics 实际检索方式与通道降级信息。 |

#### [internal/rag/retrieval/rrf.go](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/retrieval/rrf.go:1)

通道去重、原始排名保留、单路处理与加权 RRF。

| 声明（点击定位） | 类别 | 中文职责 |
| --- | --- | --- |
| [RRFConfig](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/retrieval/rrf.go:10) | 类型 | RRFConfig 控制两路召回权重与排名平滑常数；融合分数按理论最大值归一化。 |
| [DefaultRRFConfig](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/retrieval/rrf.go:17) | 函数 | DefaultRRFConfig 返回当前推荐默认值。 |
| [FuseRRF](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/retrieval/rrf.go:28) | 函数 | FuseRRF 按 Eino 返回的相关性顺序计算加权排名融合。 |
| [vectorOnlyResults](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/retrieval/rrf.go:141) | 函数 | vectorOnlyResults 保留 cosine similarity 本身。 |
| [keywordOnlyResults](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/retrieval/rrf.go:155) | 函数 | keywordOnlyResults 保留关键词召回顺序，并按当前结果最大分数归一化，不改写原始关键词分数。 |
| [Limit](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/retrieval/rrf.go:187) | 函数 | Limit 截取最终 TopK。 返回新的 slice header，底层 SearchResult 不会被修改。 |
| [normalizeRRFConfig](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/retrieval/rrf.go:195) | 函数 | 仅在整个 RRFConfig 为零时恢复默认，保留显式单路权重。 |
| [RRFConfig.Effective](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/retrieval/rrf.go:205) | 函数 | Effective 补齐排名平滑常数和默认权重。 |
| [RRFConfig.Validate](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/retrieval/rrf.go:208) | 函数 | Validate 检查排名常数与权重，拒绝负数和非有限值。 |
| [sortResults](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/retrieval/rrf.go:220) | 函数 | 按分数降序排列，同分按分块 ID 升序，保证结果稳定。 |
| [prepareRankedChannel](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/retrieval/rrf.go:231) | 函数 | 同一路重复命中保留第一个，即排名最高的结果。 |

#### [internal/rag/retriever/postgres/retriever.go](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/retriever/postgres/retriever.go:1)

向量和 BM25 原生 Retriever、调用 options、SQL 与结果扫描。

| 声明（点击定位） | 类别 | 中文职责 |
| --- | --- | --- |
| [rows](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/retriever/postgres/retriever.go:49) | 类型 | rows 是 Retriever 真正需要的最小 RowSet 接口。 |
| [queryer](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/retriever/postgres/retriever.go:57) | 类型 | queryer 是最小查询接口。 |
| [poolQueryer](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/retriever/postgres/retriever.go:66) | 类型 | poolQueryer 把 pgxpool.Pool 适配到 queryer。 |
| [poolQueryer.Query](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/retriever/postgres/retriever.go:71) | 函数 | Query 将连接池查询适配到内部行读取接口。 |
| [VectorConfig](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/retriever/postgres/retriever.go:82) | 类型 | VectorConfig 配置 Dense Retriever。 |
| [DefaultVectorConfig](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/retriever/postgres/retriever.go:104) | 函数 | DefaultVectorConfig 返回向量召回的候选数量、维度与默认阈值。 |
| [VectorRetriever](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/retriever/postgres/retriever.go:113) | 类型 | VectorRetriever 使用 pgvector 的半精度向量与 HNSW 索引进行余弦相似度召回。 |
| [NewVectorRetriever](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/retriever/postgres/retriever.go:119) | 函数 | NewVectorRetriever 校验模型和数据库配置，创建 Eino 向量检索器。 |
| [newVectorRetriever](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/retriever/postgres/retriever.go:133) | 函数 | 补默认配置并构造向量 Retriever 的内部实例。 |
| [VectorRetriever.Retrieve](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/retriever/postgres/retriever.go:149) | 函数 | 校验调用选项，执行对应通道查询，并转换为原生 Eino 文档。 |
| [VectorRetriever.searchResults](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/retriever/postgres/retriever.go:163) | 函数 | 整理 BM25 检索调用参数，验证范围后查询内部结果。 |
| [VectorRetriever.search](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/retriever/postgres/retriever.go:200) | 函数 | 执行本通道 SQL、扫描记录，过滤原始分数阈值并保持排序。 |
| [BM25Config](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/retriever/postgres/retriever.go:281) | 类型 | BM25Config 控制关键词召回的知识库范围、数量与原始分数阈值。 |
| [DefaultBM25Config](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/retriever/postgres/retriever.go:294) | 函数 | DefaultBM25Config 返回关键词召回的默认候选数量。 |
| [BM25Retriever](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/retriever/postgres/retriever.go:302) | 类型 | BM25Retriever 通过 ParadeDB 进行关键词召回的 Eino 检索器。 |
| [NewBM25Retriever](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/retriever/postgres/retriever.go:308) | 函数 | NewBM25Retriever 校验配置并绑定 PostgreSQL 连接池。 |
| [newBM25Retriever](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/retriever/postgres/retriever.go:322) | 函数 | 补默认配置并构造 BM25 Retriever 的内部实例。 |
| [BM25Retriever.Retrieve](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/retriever/postgres/retriever.go:333) | 函数 | 校验调用选项，执行对应通道查询，并转换为原生 Eino 文档。 |
| [BM25Retriever.searchResults](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/retriever/postgres/retriever.go:346) | 函数 | 整理 BM25 检索调用参数，验证范围后查询内部结果。 |
| [BM25Retriever.search](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/retriever/postgres/retriever.go:377) | 函数 | 执行本通道 SQL、扫描记录，过滤原始分数阈值并保持排序。 |
| [scanSearchResults](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/retriever/postgres/retriever.go:491) | 函数 | scanSearchResults 将数据库行映射为统一召回结果，保留原始通道分数。 |
| [validateCommonOptions](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/retriever/postgres/retriever.go:573) | 函数 | validateCommonOptions 校验 Eino 通用选项，拒绝不支持的子索引和查询表达式。 |
| [validateTopK](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/retriever/postgres/retriever.go:607) | 函数 | validateTopK 限制数据库请求的候选数量，防止无界召回。 |
| [filterByScore](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/retriever/postgres/retriever.go:621) | 函数 | filterByScore 保留达到原始通道阈值的结果并限制数量，保持数据库排序。 |
| [toHalfVector](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/retriever/postgres/retriever.go:650) | 函数 | toHalfVector 校验维度、有限值、半精度范围和非零向量，再转换为数据库向量类型。 |

#### [internal/rag/retriever/postgres/validation.go](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/retriever/postgres/validation.go:1)

TopK、维度和通道阈值的合法性检查。

| 声明（点击定位） | 类别 | 中文职责 |
| --- | --- | --- |
| [finite](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/retriever/postgres/validation.go:9) | 函数 | finite 判断分数是否为有限数值。 |
| [optionalTopK](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/retriever/postgres/validation.go:12) | 函数 | optionalTopK 检查调用级候选数量，零值由组件补齐默认值。 |
| [validateDimensions](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/retriever/postgres/validation.go:20) | 函数 | validateDimensions 确认向量维度与当前数据库列一致。 |
| [VectorConfig.Validate](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/retriever/postgres/validation.go:28) | 函数 | Validate 检查检索数量、阈值、维度或模型输入预算。 |
| [BM25Config.Validate](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/retriever/postgres/validation.go:39) | 函数 | Validate 检查检索数量、阈值、维度或模型输入预算。 |

#### [internal/rag/retriever/postgres/context_loader.go](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/retriever/postgres/context_loader.go:1)

按 collection 批量读取父块及去重父 ID。

| 声明（点击定位） | 类别 | 中文职责 |
| --- | --- | --- |
| [ParentLoader](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/retriever/postgres/context_loader.go:15) | 类型 | ParentLoader 绑定知识库范围，批量读取已发布父块用于上下文扩展。 |
| [NewParentLoader](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/retriever/postgres/context_loader.go:21) | 函数 | NewParentLoader 校验连接池和知识库 ID，创建父块读取器。 |
| [newParentLoader](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/retriever/postgres/context_loader.go:43) | 函数 | 根据内部查询接口构造父块读取器，便于数据库测试。 |
| [ParentLoader.LoadParents](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/retriever/postgres/context_loader.go:54) | 函数 | LoadParents 使用一次查询读取所有父块，避免逐条子块查询数据库。 |
| [uniqueStrings](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/retriever/postgres/context_loader.go:156) | 函数 | uniqueStrings 去除空值与重复 ID，保留首次出现顺序。 |

#### [internal/rag/retriever/hybrid.go](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/retriever/hybrid.go:1)

并发两路召回、超时与失败策略、option 传播及融合。

| 声明（点击定位） | 类别 | 中文职责 |
| --- | --- | --- |
| [HybridConfig](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/retriever/hybrid.go:23) | 类型 | HybridConfig 只配置召回组合策略，不包含具体后端的维度或分数阈值。 |
| [DefaultHybridConfig](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/retriever/hybrid.go:39) | 函数 | DefaultHybridConfig 返回两路候选数量与 RRF 默认配置。 |
| [HybridConfig.Validate](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/retriever/hybrid.go:44) | 函数 | Validate 检查候选数量、超时、失败策略和融合权重。 |
| [HybridConfig.effective](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/retriever/hybrid.go:54) | 函数 | 补齐候选数、整体超时、失败策略和有效 RRF 默认值。 |
| [Hybrid](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/retriever/hybrid.go:70) | 类型 | Hybrid 只负责并发召回、失败策略和 RRF；两路通道可以来自不同的库。 |
| [NewHybrid](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/retriever/hybrid.go:77) | 函数 | NewHybrid 组合 Eino 原生向量与关键词检索器，不绑定具体索引库。 |
| [Options](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/retriever/hybrid.go:95) | 类型 | Options 给两路组件分别传递原生调用选项，阈值和专属过滤语法由各后端解释。 |
| [WithVectorOptions](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/retriever/hybrid.go:98) | 函数 | WithVectorOptions 为向量通道单独设置 Eino 选项，知识库范围仍由公共选项约束。 |
| [WithKeywordOptions](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/retriever/hybrid.go:104) | 函数 | WithKeywordOptions 为关键词通道单独设置 Eino 选项，避免混用两路原始分数阈值。 |
| [Hybrid.Retrieve](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/retriever/hybrid.go:110) | 函数 | Retrieve 并发召回两路结果，执行 RRF 融合并返回原生 Eino 文档。 |
| [Hybrid.RetrieveWithDiagnostics](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/retriever/hybrid.go:116) | 函数 | RetrieveWithDiagnostics 附加失败诊断，普通调用仍使用 Eino 的 Retrieve 方法。 |

#### [internal/rag/rerank/reranker.go](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/rerank/reranker.go:1)

精排阈值、组合分、故障回退、诊断与独立 MMR。

| 声明（点击定位） | 类别 | 中文职责 |
| --- | --- | --- |
| [Scorer](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/rerank/reranker.go:20) | 类型 | Scorer 按输入片段顺序返回精排分数；返回数量须与片段数一致。 |
| [PassageBuilder](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/rerank/reranker.go:26) | 类型 | PassageBuilder 决定某一个 SearchResult |
| [Outcome](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/rerank/reranker.go:29) | 类型 | Outcome 精排成功、阈值降级或模型不可用等执行状态。 |
| [Diagnostics](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/rerank/reranker.go:45) | 类型 | Diagnostics 用于解释一次 Rerank 到底发生了什么。 |
| [Config](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/rerank/reranker.go:65) | 类型 | Config 控制整个 Rerank Pipeline。 |
| [DefaultConfig](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/rerank/reranker.go:101) | 函数 | DefaultConfig 返回精排阈值、候选上限、融合权重和独立精排的 MMR 参数。 |
| [Result](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/rerank/reranker.go:121) | 类型 | Result 保存一次完整 Rerank 的输出。 |
| [Engine](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/rerank/reranker.go:135) | 类型 | Engine 模型精排引擎，负责评分、阈值处理和排序。 |
| [NewEngine](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/rerank/reranker.go:142) | 函数 | NewEngine 保存配置与校验结果；配置错误在执行时返回。 |
| [Engine.RerankCandidates](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/rerank/reranker.go:154) | 函数 | RerankCandidates 保留通过阈值的候选池，供父块分组后统一截断；此路径不执行 MMR 多样性选择。 |
| [Engine.Rerank](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/rerank/reranker.go:161) | 函数 | Rerank 执行评分、过滤、排序与独立调用的 MMR 选择。普通模型错误退回召回顺序，取消和超时直接返回。 |
| [DefaultPassageBuilder](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/rerank/reranker.go:346) | 函数 | DefaultPassageBuilder 复用统一文本构造规则，将标题、标题路径与子块正文交给精排模型。 |
| [applyMMR](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/rerank/reranker.go:352) | 函数 | applyMMR 使用相关性与文本集合的 Jaccard 相似度选择尽量不重复的结果，不修改相关性分数。 |
| [tokenSet](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/rerank/reranker.go:430) | 函数 | tokenSet 将拉丁字母和数字按词归一化，将中日韩字符按单字收集，用于轻量相似度计算。 |
| [isCJK](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/rerank/reranker.go:464) | 函数 | isCJK 判断字符是否属于当前支持的中日韩文字范围。 |
| [jaccard](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/rerank/reranker.go:472) | 函数 | jaccard 计算两个 token 集合的交并比，空集合返回零。 |
| [sourceWeight](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/rerank/reranker.go:495) | 函数 | sourceWeight 优先读取结果中的来源权重，再读取元数据中的可选权重。 |
| [indicesAboveThreshold](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/rerank/reranker.go:525) | 函数 | indicesAboveThreshold 返回达到相关性阈值的输入位置，保持原顺序。 |
| [indexOfMax](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/rerank/reranker.go:538) | 函数 | indexOfMax 返回最高分位置，同分时保留第一个。 |
| [fallbackResults](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/rerank/reranker.go:555) | 函数 | fallbackResults 保留召回排序并标记未精排，按独立调用的 TopK 截断。 |
| [clamp01](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/rerank/reranker.go:571) | 函数 | clamp01 将有效分数限制在零到一之间。 |
| [normalizeConfig](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/rerank/reranker.go:585) | 函数 | normalizeConfig 补齐候选数量、默认权重与模型输入构造函数。 |

#### [internal/rag/rerank/validation.go](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/rerank/validation.go:1)

精排数量、阈值、组合权重和 lambda 校验。

| 声明（点击定位） | 类别 | 中文职责 |
| --- | --- | --- |
| [Config.Validate](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/rerank/validation.go:9) | 函数 | Validate 检查候选数量、阈值、权重和 MMR 参数是否合法。 |

#### [internal/rag/search/eino.go](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/search/eino.go:1)

Eino 检索器适配、诊断传播、context 等待与结果转换。

| 声明（点击定位） | 类别 | 中文职责 |
| --- | --- | --- |
| [diagnosticRetriever](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/search/eino.go:14) | 类型 | 带诊断的检索器仍然实现原生 Retriever，普通 eino-ext 组件无需实现此能力。 |
| [NewEinoPipeline](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/search/eino.go:19) | 函数 | NewEinoPipeline 在组件边界转换一次数据，后续引用、精排与父块扩展使用内部结果。 |

#### [internal/rag/search/pipeline.go](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/search/pipeline.go:1)

候选精排、父块扩展、版本验证、Evidence 分组和最终数量。

| 声明（点击定位） | 类别 | 中文职责 |
| --- | --- | --- |
| [RetrieveFunc](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/search/pipeline.go:22) | 类型 | RetrieveFunc 是精排和引用处理的内部输入；外部 Eino 组件通过 NewEinoPipeline 接入。 |
| [ParentLoader](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/search/pipeline.go:31) | 类型 | ParentLoader 负责批量加载 Parent Chunk。 |
| [Config](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/search/pipeline.go:36) | 类型 | Config 控制检索超时、最终数量与父块扩展策略。 |
| [DefaultConfig](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/search/pipeline.go:53) | 函数 | DefaultConfig 默认返回五条结果，并在提供父块读取器时扩展上下文。 |
| [Response](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/search/pipeline.go:65) | 类型 | Response 不只返回最终结果， 也保留 Rerank Diagnostics。 |
| [Diagnostic](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/search/pipeline.go:74) | 类型 | Diagnostic 流程阶段与可恢复问题的说明。 |
| [Pipeline](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/search/pipeline.go:80) | 类型 | Pipeline 召回、可选精排与父块扩展组成的检索流程。 |
| [NewPipeline](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/search/pipeline.go:89) | 函数 | NewPipeline 校验配置并组合检索函数、精排引擎与父块读取器。 |
| [Config.Validate](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/search/pipeline.go:118) | 函数 | Validate 拒绝负数结果数量和超时。 |
| [NewPipelineWithDiagnostics](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/search/pipeline.go:126) | 函数 | NewPipelineWithDiagnostics 接收带通道诊断的检索函数，保留失败降级信息。 |
| [Pipeline.Search](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/search/pipeline.go:141) | 函数 | Search 执行召回、可选精排、父块扩展与最终选择；当前统一流程不执行 MMR 或问题改写。 |
| [Pipeline.finalize](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/search/pipeline.go:278) | 函数 | finalize 按需合并父块并统一截断最终结果，保留子块命中证据。 |
| [uniqueParentIDs](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/search/pipeline.go:295) | 函数 | uniqueParentIDs 收集不重复的父块 ID，供批量读取。 |
| [collapseSameContext](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/search/pipeline.go:316) | 函数 | collapseSameContext 按最终上下文身份分组，聚合全部命中子块的引用证据。 |

### 27.7 迁移命令与真实文档实验

#### [cmd/rag-migrate/main.go](/Users/sda1_hacker/Desktop/humbert/humbert-agent/cmd/rag-migrate/main.go:1)

读取迁移环境变量，安装扩展并执行 schema 迁移。

| 声明（点击定位） | 类别 | 中文职责 |
| --- | --- | --- |
| [main](/Users/sda1_hacker/Desktop/humbert/humbert-agent/cmd/rag-migrate/main.go:14) | 函数 | 调用迁移流程，安全打印错误并设置退出码。 |
| [run](/Users/sda1_hacker/Desktop/humbert/humbert-agent/cmd/rag-migrate/main.go:20) | 函数 | 读取 HUMBERT_RAG_DATABASE_URL，创建扩展、池并执行迁移。 |

#### [examples/5-rag/main.go](/Users/sda1_hacker/Desktop/humbert/humbert-agent/examples/5-rag/main.go:1)

单文件包含真实文档实验的全部运行代码：主流程、配置、组件组装、模型档案、指标与结果展示。

| 声明（点击定位） | 类别 | 中文职责 |
| --- | --- | --- |
| [main](/Users/sda1_hacker/Desktop/humbert/humbert-agent/examples/5-rag/main.go:57) | 函数 | main 按执行顺序展示整个实验，不需要切换文件才能理解流程。 |
| [exampleConfig](/Users/sda1_hacker/Desktop/humbert/humbert-agent/examples/5-rag/main.go:194) | 函数 | exampleConfig 集中展示配置入口；调参时修改这里，不需要修改组装流程。 |
| [postgresConfig](/Users/sda1_hacker/Desktop/humbert/humbert-agent/examples/5-rag/main.go:319) | 类型 | postgresConfig 将本示例用到的 PostgreSQL、解析器和模型配置集中在一起。 |
| [defaultPostgresConfig](/Users/sda1_hacker/Desktop/humbert/humbert-agent/examples/5-rag/main.go:345) | 函数 | defaultPostgresConfig 返回示例所需的数据库、模型及检索默认参数。 |
| [postgresConfig.Validate](/Users/sda1_hacker/Desktop/humbert/humbert-agent/examples/5-rag/main.go:373) | 函数 | Validate 先检查本例参数之间的关系，再由底层组件验证各自配置。 |
| [newPostgresRAG](/Users/sda1_hacker/Desktop/humbert/humbert-agent/examples/5-rag/main.go:449) | 函数 | newPostgresRAG 是组件组装入口，不负责处理具体文件或具体问题。 |
| [postgresConfig.EmbeddingProfile](/Users/sda1_hacker/Desktop/humbert/humbert-agent/examples/5-rag/main.go:584) | 函数 | EmbeddingProfile 描述 collection 的向量空间身份，作为稳定 JSON 计算 SHA-256。 |
| [evaluation](/Users/sda1_hacker/Desktop/humbert/humbert-agent/examples/5-rag/main.go:607) | 类型 | evaluation 按最终结果中的子块证据计算指标，避免父块合并后漏算或重复计算。 |
| [evidenceIDs](/Users/sda1_hacker/Desktop/humbert/humbert-agent/examples/5-rag/main.go:617) | 函数 | evidenceIDs 收集主命中和同父块合并保留的全部子块 ID，按出现顺序去重。 |
| [evaluate](/Users/sda1_hacker/Desktop/humbert/humbert-agent/examples/5-rag/main.go:640) | 函数 | evaluate 中的“准确率”使用检索 Precision；分母是实际子块证据数。 |
| [printSettings](/Users/sda1_hacker/Desktop/humbert/humbert-agent/examples/5-rag/main.go:660) | 函数 | printSettings 打印配置意图，例如启用了哪种分块和父上下文。 |
| [printSearch](/Users/sda1_hacker/Desktop/humbert/humbert-agent/examples/5-rag/main.go:675) | 函数 | printSearch 展示实际执行状态，再分别展示子块证据和最终上下文。 |
| [envOr](/Users/sda1_hacker/Desktop/humbert/humbert-agent/examples/5-rag/main.go:704) | 函数 | envOr 读取环境变量，未填写时使用示例默认值。 |

### 27.8 测试与说明文档入口

生产代码中的不变量，应结合测试阅读。各目录的 `*_test.go` 与同目录生产文件相邻；下面是最适合优先阅读的代表入口。

| 路径 | 阅读重点 |
| --- | --- | --- |
| [internal/rag/backend_test.go](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/backend_test.go:1) | 不修改 Eino 原生接口也能替换组件。 |
| [internal/rag/publication_test.go](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/publication_test.go:1) | 完整外部写入后发布、返回 ID 校验和当前版本过滤。 |
| [internal/rag/ingestion_regression_test.go](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/ingestion_regression_test.go:1) | 解析前稳定 ID 与版本预留。 |
| [internal/rag/chunker/regression_test.go](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/chunker/regression_test.go:1) | 表头与来源坐标、连续正文覆盖和零重叠父子派生。 |
| [internal/rag/chunker/heading_splitter_test.go](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/chunker/heading_splitter_test.go:1) | 标题路径、长章节内子标题和禁止跨顶层标题合并。 |
| [internal/rag/chunker/heuristic_splitter_test.go](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/chunker/heuristic_splitter_test.go:1) | 结构边界、保护区与重叠。 |
| [internal/documentparse/parser_test.go](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/documentparse/parser_test.go:1) | 预算和解析子进程的真实文件行为。 |
| [internal/rag/embeddinginput/budget_test.go](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/embeddinginput/budget_test.go:1) | 先检查全部输入，再请求模型。 |
| [internal/rag/indexer/postgres/regression_test.go](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/indexer/postgres/regression_test.go:1) | 非法输入在远程调用前拒绝、旧任务不能删除当前版本、档案与快照。 |
| [internal/rag/indexer/postgres/integration_test.go](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/indexer/postgres/integration_test.go:1) | 真实 PG 事务、扩展搜索、隔离、生命周期与迁移。 |
| [internal/rag/retrieval/documents_test.go](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/retrieval/documents_test.go:1) | 元数据及整数版本转换。 |
| [internal/rag/retrieval/rrf_test.go](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/retrieval/rrf_test.go:1) | 输入排名、复合身份和单路分数。 |
| [internal/rag/retriever/hybrid_test.go](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/retriever/hybrid_test.go:1) | 并发召回、独立超时和降级诊断。 |
| [internal/rag/provider/rerank/http/client_test.go](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/provider/rerank/http/client_test.go:1) | HTTP 结果按输入下标还原与完整评分约束。 |
| [internal/rag/rerank/reranker_test.go](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/rerank/reranker_test.go:1) | 阈值、组合分与独立 MMR。 |
| [internal/rag/search/pipeline_test.go](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/search/pipeline_test.go:1) | 父上下文分组和最后 TopK。 |
| [internal/rag/search/regression_test.go](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/search/regression_test.go:1) | 取消、父块读取回退及跨版本混合保护。 |
| [examples/5-rag/main_test.go](/Users/sda1_hacker/Desktop/humbert/humbert-agent/examples/5-rag/main_test.go:1) | 配置约束、模型档案及子块 Evidence 评测，父正文不凭空增加召回。 |
| [internal/rag/README.md](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/README.md:1) | 模块入口和用法概览。 |
| [internal/rag/BACKENDS.md](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/BACKENDS.md:1) | 原子索引与外部分离索引的组装边界。 |
| [examples/5-rag/README.md](/Users/sda1_hacker/Desktop/humbert/humbert-agent/examples/5-rag/README.md:1) | 真实文件运行及调参操作。 |
