# 组合与替换 Eino 组件

业务层使用一个 `rag.Service`。组件层直接采用 `indexer.Indexer` 和 `retriever.Retriever`，不增加平行的索引接口，也不为每个后端创建另一种 RAG。

## 最小组合

```go
func Build(ctx context.Context, loader document.Loader, idx indexer.Indexer, recall retriever.Retriever) (*rag.Service, error) {
    return rag.New(ctx, rag.Dependencies{
        Loader: loader,
        Indexer: idx,
        Config: rag.Config{
            Retriever: recall,
            RecallTopK: 20,
            Search: search.Config{FinalTopK: 5},
        },
    })
}
```

使用 Eino 的 document/indexer/retriever 包、项目的 rag/search 包即可。idx 和 recall 可直接传入 eino-ext 组件。初始化必须提供完整导入组件，或至少提供默认 Retriever；支持只导入或只检索的服务。

这个最小组合只有基础导入和检索。Eino 不定义原文保存、父块、文档发布和删除，不能假设一个普通 Indexer 自动完成这些能力。其对集合、ID、元数据和 Option 的支持也需要确认。完整的文档业务使用下面的显式能力。

## 原子 PostgreSQL 模式

本地示例在 `examples/5-rag/main.go` 组装 PostgreSQL Indexer、pgvector Retriever、ParadeDB BM25 Retriever、Tabula 和共享 Embedder。

```go
// pgIndexer 的原生 Store 同时发布原文、分块和检索索引。
rag.Dependencies{
    Loader: loader,
    Indexer: pgIndexer,
    Config: config,
    Lifecycle: pgIndexer,
    ChunkReader: pgIndexer,
    Close: pool.Close,
}
```

不要再设置 Publisher，否则会重复发布。PostgreSQL 查询本身只返回已发布分块，也无需额外 PublishedChunks 回查。模型调用在事务前执行，写入时再检查预留版本。取消、删除或新版本预留后，旧任务不能覆盖当前文档。

索引和查询共享相同的 Embedder；collection profile 固定模型、版本、维度及检索文本构造方式。绑定 profile 的 PostgreSQL Indexer 会拒绝调用级 Embedding 覆盖。

## 独立向量库与关键词库

只有索引成功才发布权威文档，业务 Service 已实现如下顺序：

```text
预留版本 → 解析与切分 → 所有 Eino Indexer.Store 成功 → Publisher 发布
```

```go
// vectorIndexer 和 keywordIndexer 只写索引，不能提前发布权威文档。
// multiindexer 是 internal/rag/indexer 的导入别名。
idx, err := multiindexer.NewMulti(vectorIndexer, keywordIndexer)
if err != nil { return nil, err }

// 两路在融合前过滤不可见命中；这里的文档库不必与向量库相同。
vector, err := rag.WithPublishedChunks(vectorRetriever, documentStore)
if err != nil { return nil, err }
keyword, err := rag.WithPublishedChunks(keywordRetriever, documentStore)
if err != nil { return nil, err }

// ragretriever 是 internal/rag/retriever 的导入别名。
hybrid, err := ragretriever.NewHybrid(vector, keyword, ragretriever.DefaultHybridConfig())
if err != nil { return nil, err }

return rag.New(ctx, rag.Dependencies{
    Loader: loader,
    Indexer: idx,
    Config: rag.Config{
        Retriever: hybrid,
        VectorRetriever: vector,
        KeywordRetriever: keyword,
        RecallTopK: 50,
        Search: search.Config{FinalTopK: 5},
        // 需要父块扩展时，再设置 ParentLoader 和 ExpandParents。
    },
    Lifecycle: documentStore,
    Publisher: documentStore,
    PublishedChunks: documentStore,
    ChunkReader: documentStore,
})
```

documentStore 可以使用 PostgreSQL 已有的文档能力，此时它只提供 Publisher 和文档读取，不作为 multi 的索引组件。Service 不通过类型断言推测 Indexer 是否管理版本，而是使用显式 Lifecycle，在解析前预留正整数版本。最终入口的 PublishedChunks 检查保护所有搜索模式；融合前的两路包装避免无效命中占据融合名额。

每个版本具有独立分块 ID，后端必须保留 ID 和下表中的元数据。任一路写入或发布失败，不发布新版本，也不覆盖旧版 ID。返回的 ID 缺失、重复或被改写会报错。多个数据库不存在共同事务；部分写入可能留下不可见记录，仍需选定后端后添加清理。过多残留也可能占据召回候选，需要结合后端过滤及清理处理。

## 文档元数据与业务能力

`rag.IndexDocuments` 生成统一检索文本。索引需要保存并返回：

| 元数据 | 用途 |
| --- | --- |
| `rag_collection_id` / `rag_document_id` | 集合与稳定文档身份 |
| `rag_document_revision` | 十进制字符串版本号，避免 JSON bigint 精度损失 |
| `rag_raw_content` | 原始子块正文 |
| `rag_parent_chunk_id` | 父块引用 |
| `rag_chunk_index` | 子块顺序 |
| `rag_chunk_start` / `rag_chunk_end` | 原文 rune 范围 |
| `rag_context_header` | 标题面包屑 |

| 可选接口 | 业务职责 |
| --- | --- |
| `rag.DocumentLifecycle` | 预留版本、取消、删除 |
| `rag.DocumentPublisher` | 原文和父子块发布；必须拒绝失效版本 |
| `rag.ChunkReader` | 已发布分块的展示与标注 |
| `rag.PublishedChunkReader` | 校验命中文档及版本，恢复权威正文，保留排名和分数 |
| `search.ParentLoader` | 读取父块扩展上下文 |

这些接口处理 Eino 未定义的文档业务，并不替代 Eino Indexer/Retriever。普通原生组件不需要实现它们，可以注入单独的文档库。

开发者直接调用原生 Retrieve 时，仍可通过 Eino Option 设置阈值和具体过滤语法；Hybrid 的融合阈值与原始通道阈值分别由 WithScoreThreshold、WithVectorOptions 和 WithKeywordOptions 指定。业务 API 的 Ingest/Search 不接受这些选项，避免改变已固定的模型和知识库配置。
