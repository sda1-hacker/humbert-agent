# 组合与替换 Eino 组件

业务入口仍是一个 `rag.Service`。索引与召回采用 Eino `indexer.Indexer` / `retriever.Retriever`；原件、文档版本、发布、取消和权威正文由显式业务能力补齐。更换索引组件不会自动获得完整文档管理。

## 基础组合

以下是装配片段，参数中的 Loader、Indexer、Retriever 已由调用方构造：

```go
return rag.New(ctx, rag.Dependencies{
    Loader: loader,
    Indexer: idx,
    Config: rag.Config{
        Retriever: recall,
        RecallTopK: 50,
        Search: ragconfig.Search{FinalTopK: 5},
    },
})
```

需导入 Eino document/indexer/retriever 及项目 rag/config 等实际类型；完整装配参见 [knowledge/engine.go](../knowledge/engine.go)。允许只导入（Loader+Indexer）或只检索（默认 Retriever），Loader/Indexer 不能只提供其中一个。原生组件使用的物理集合应在构造时固定，同时确认其对 `WithIndex`、ID、Metadata 和分数的支持。

## 原子 PostgreSQL 模式

[上传示例](../../examples/5-rag/upload/main.go) 与桌面引擎创建共享 Embedder、PG Indexer 和文档能力：

```go
rag.Dependencies{
    Loader: loader, Images: loader, ImageStates: pgIndexer,
    Indexer: pgIndexer, Lifecycle: pgIndexer,
    Documents: pgIndexer, Sources: sources,
    ChunkReader: pgIndexer, PublishedChunks: pgIndexer,
    Config: runtimeConfig,
    Close: func() { _ = sources.Close(); pool.Close() },
}
```

PG Indexer 通过 `rag.WithIngestionBatch` 在同一事务内保存管理元数据、父子块和两路索引，`Publisher` 留空。PublishedChunks 在桌面和独立查询中做权威回查；原生 PG 查询本身也限定集合并关联文档/分块。模型请求在事务前执行，最终写入取得版本锁；旧 attempt 不能覆盖新发布内容。

创建 `rag.Service` 失败时资源仍由装配方释放；创建成功后再由指定 Close 回调接管。已有池的所有者也可以不传 Close，由外层按顺序关闭，不能在请求内部关闭服务。

## 分离索引与权威文档库

```text
预留版本 → 托管/解析/切分 → 全部 Indexer.Store 成功
    → 可选 BeforePublish 等待可检索 → Publisher 发布
    → 清理旧版本索引
```

组合的关键依赖：

| 依赖 | 必须承担的职责 |
| --- | --- |
| `Indexer` | 可用 `indexer.NewMulti(vectorIndexer, keywordIndexer)`；保存确定的版本化 ID 与 Metadata，不提前发布权威文档 |
| `Lifecycle` | 解析前预留正整数 attempt；取消、删除使旧版本失效 |
| `Publisher` | 原子发布原件引用、父子块和成功状态，拒绝失效 attempt |
| `ChunkReader` | 读取旧发布块，用于替换成功后的索引清理 |
| `PublishedChunks` | 过滤集合/文档/版本不符及删除、未发布命中；恢复权威正文，保留输入顺序和分数 |
| `BeforePublish` | 可选；按所选 SDK 的刷新/一致性语义等待新索引可检索 |
| `DiscardAttempt` | 原子确认本版本尚未发布并作废；已发布返回 false，阻止误删有效索引 |
| `RemoveIndex` | 用实际后端 SDK 按集合和 ID 幂等清理所有外部索引 |
| `Sources`、`Documents` | 可选文档管理；Sources 要求 Documents，Documents 要求 Lifecycle |

`rag.New` 在 Publisher 模式下要求 Lifecycle、PublishedChunks、ChunkReader、DiscardAttempt 和 RemoveIndex；缺少清理能力会直接失败。现有 PG `DocumentStore.DiscardAttempt` 可承担版本作废，但外部索引删除仍要由具体客户端实现，仓库没有自动提供 ES/Milvus 客户端。

建议先对各路 Retriever 包装 `rag.WithPublishedChunks`，再组合 Hybrid，避免无效候选占融合名额；最终 Service 也包装默认、向量和关键词检索器，保护所有 Mode。Hybrid 使用 `retriever.NewHybrid(vector, keyword, cfg.Hybrid)`，没有额外的后端注册器。

网络错误可能发生在提交之后。失败补偿先调用 DiscardAttempt，只有确认未发布才清理新 ID；发布成功后旧索引清理失败作为 warning 返回，不重跑整篇向量化。不同数据库不存在共同事务，长期残留的回收仍需所选后端负责。

## 需要保留的身份与文本

`rag.IndexDocuments` 生成统一检索文本，至少保留以下 MetaData：

| 键 | 语义 |
| --- | --- |
| `rag_collection_id`、`rag_document_id` | 逻辑集合与稳定业务文档 ID |
| `rag_document_revision` | 十进制字符串版本，避免 JSON bigint 精度损失 |
| `rag_raw_content` | 权威子块正文，不能用标题拼接输入代替 |
| `rag_parent_chunk_id`、`rag_chunk_index` | 父关系和子块顺序 |
| `rag_chunk_start`、`rag_chunk_end` | 处理文本的 rune 左闭右开范围 |
| `rag_context_header` | 标题路径 |

业务 ID 与 PG 内部 BIGINT 主键分别管理。版本化 chunk ID 同时包含知识库摘要、文档和 attempt，后端必须保留返回 ID，不能重命名或重复。来源定位、图片识别状态与发布版本也必须一同保留。

原生 Store/Retrieve 可以使用 Eino Option；业务 Ingest/Search 不接受任意调用级配置覆盖。实际模型、集合隔离、分数阈值和输入档案必须在装配时核对。详细失败路径见 [RAG 源码详解](../../docs/RAG源码详解.md#backends)。
