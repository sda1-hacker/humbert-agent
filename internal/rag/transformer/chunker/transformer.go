package chunker

import (
	"context"
	"fmt"

	"github.com/cloudwego/eino/components/document"
	"github.com/cloudwego/eino/schema"

	"github.com/sda1-hacker/humbert-agent/internal/rag/application"
	corechunker "github.com/sda1-hacker/humbert-agent/internal/rag/chunker"
)

// 编译期接口检查。
//
// 如果未来 Eino 修改了 document.Transformer 接口，
// 或者我们不小心改坏了 Transform 方法签名，
// 编译器会在这里直接报错，而不是运行时才发现。
var _ document.Transformer = (*Transformer)(nil)

// -----------------------------------------------------------------------------
// Metadata Keys
// -----------------------------------------------------------------------------
//
// schema.Document.MetaData 是开放的 map[string]any。
//
// 为了避免我们的字段和 Loader / Parser / Indexer 的 metadata 冲突，
// 所有 RAG Chunker 字段统一使用 "rag_" 前缀。
//
// 后面的 PGIndexer 可以直接依赖这些常量，
// 不需要到处手写字符串。
const (
	// MetaSourceDocumentID 保存 Chunk 来源的 Eino Document.ID。
	//
	// 例如输入：
	//
	//     Document.ID = "doc-123"
	//
	// 输出 Chunk：
	//
	//     rag_source_document_id = "doc-123"
	//
	// 后面即使 Chunk 自己拥有新的 ID，
	// 仍然可以追溯到原始 Document。
	MetaSourceDocumentID = "rag_source_document_id"

	// MetaSourceDocumentIndex 表示当前 source Document
	// 在本次 Transform(src) 输入数组中的位置。
	//
	// 它主要解决一种情况：
	//
	//     source.ID == ""
	//
	// 即使 Loader 没有提供 ID，
	// 本批处理内部仍然可以知道这个 Chunk 来源于第几个 Document。
	MetaSourceDocumentIndex = "rag_source_document_index"

	// MetaChunkIndex 是 Chunk 在当前 source Document 中的顺序。
	//
	// 对应：
	//
	//     corechunker.Chunk.Seq
	MetaChunkIndex = "rag_chunk_index"

	// MetaChunkStart / MetaChunkEnd 是原始 source Document.Content
	// 中的 Unicode rune offset。
	//
	// 区间约定：
	//
	//     [start, end)
	//
	// 注意不是 byte offset。
	MetaChunkStart = "rag_chunk_start"
	MetaChunkEnd   = "rag_chunk_end"

	// MetaContextHeader 保存 Heading Splitter 生成的 Breadcrumb。
	//
	// 例如：
	//
	//     # 产品手册
	//     ## 安装
	//     ### Linux
	//
	// Content 仍然保持真实原文；
	// ContextHeader 是额外检索上下文。
	MetaContextHeader = "rag_context_header"
)

// IDGenerator 决定一个 core Chunk 转成 schema.Document 后使用什么 ID。
//
// 参数：
//
//	source
//	    原始 Eino Document。
//
//	sourceIndex
//	    source 在本次 Transform 输入数组里的下标。
//
//	chunk
//	    RAG Core 产生的 Chunk。
//
// 为什么把 ID 生成策略暴露出来？
//
// 因为不同系统对 ID 的要求差别很大：
//
// Demo：
//
//	doc-1#chunk-000003
//
// PostgreSQL：
//
//	可能直接使用 UUID
//
// 已有业务系统：
//
//	可能需要:
//	tenant/document/version/chunk
//
// Adapter 不应该把这种业务决策写死。
type IDGenerator func(
	source *schema.Document,
	sourceIndex int,
	chunk corechunker.Chunk,
) string

// Config 是 Transformer 的创建时配置。
//
// 当前刻意非常薄，只包含：
//
//	Splitter
//	IDGenerator
//
// 不在这里重新复制：
//
//	ChunkSize
//	ChunkOverlap
//	Strategy
//
// 等字段。
//
// 因为这些已经由：
//
//	corechunker.SplitterConfig
//
// 定义。
//
// Adapter 应该复用 Core Model，而不是制造第二套配置模型。
type Config struct {
	Splitter    corechunker.SplitterConfig
	IDGenerator IDGenerator
}

// DefaultConfig 返回 Transformer 的基础默认配置。
//
// 注意：
//
// corechunker.DefaultConfig() 当前的：
//
//	Strategy == ""
//
// 根据我们之前对齐的 WeKnora 兼容行为，
// 空 Strategy 表示 Legacy，而不是 Auto。
//
// 如果你想启用 Adaptive Chunking：
//
//	cfg := chunkertransformer.DefaultConfig()
//	cfg.Splitter.Strategy = corechunker.StrategyAuto
func DefaultConfig() Config {
	return Config{
		Splitter:    corechunker.DefaultConfig(),
		IDGenerator: DefaultIDGenerator,
	}
}

// Transformer 把 Eino schema.Document 转换成
// 经过我们 RAG Core Chunker 分块后的 schema.Document。
//
// 它本身不包含任何分块算法。
//
// 真正算法全部来自：
//
//	internal/rag/chunker
//
// 因此这个类型只是：
//
//	Eino <-> RAG Core
//
// 的 Adapter。
type Transformer struct {
	config Config
}

// NewTransformer 创建 Eino Chunk Transformer。
//
// Config 会被复制到 Transformer 内部。
//
// 其中 SplitterConfig 的 slice 字段：
//
//	Separators
//	Languages
//
// 也会被复制，避免调用方在 Transformer 创建后继续修改原 slice，
// 导致 Transformer 行为发生隐式变化。
func NewTransformer(config Config) *Transformer {
	config.Splitter = cloneSplitterConfig(config.Splitter)

	if config.IDGenerator == nil {
		config.IDGenerator = DefaultIDGenerator
	}

	return &Transformer{
		config: config,
	}
}

// transformerOptions 是 Transform 调用级配置。
//
// 为什么除了 Transformer.Config 以外还需要它？
//
// 因为 Eino 的组件接口天然支持：
//
//	Transform(ctx, docs, opts...)
//
// 也就是说同一个 Transformer 实例可以有默认配置，
// 但某一次调用又可以临时覆盖部分行为。
//
// 当前支持两个调用级覆盖：
//
//	WithSplitterConfig
//	WithIDGenerator
//
// 这正是 Eino TransformerOption 设计的用途。
type transformerOptions struct {
	splitter    corechunker.SplitterConfig
	idGenerator IDGenerator
}

// WithSplitterConfig 临时覆盖某一次 Transform 的 SplitterConfig。
//
// 例如 Transformer 默认是：
//
//	StrategyAuto
//
// 但某次导入希望强制 Legacy：
//
//	transformer.Transform(
//	    ctx,
//	    docs,
//	    WithSplitterConfig(legacyCfg),
//	)
//
// 只影响这一轮调用，不修改 Transformer 自己的默认配置。
func WithSplitterConfig(cfg corechunker.SplitterConfig) document.TransformerOption {
	cfg = cloneSplitterConfig(cfg)

	return document.WrapTransformerImplSpecificOptFn(func(options *transformerOptions) {
		options.splitter = cfg
	})
}

// WithIDGenerator 临时覆盖某一次 Transform 的 ID 生成规则。
func WithIDGenerator(generator IDGenerator) document.TransformerOption {
	return document.WrapTransformerImplSpecificOptFn(func(options *transformerOptions) {
		if generator != nil {
			options.idGenerator = generator
		}
	})
}

// Transform 实现 Eino document.Transformer。
//
// 输入：
//
//	[]*schema.Document
//
// 例如 Tabula Loader 后可能得到：
//
//	Document{
//	    ID:      "doc-123",
//	    Content: "# 产品手册...",
//	    MetaData: {
//	        "file_name": "manual.pdf",
//	        "source": "/data/manual.pdf",
//	    },
//	}
//
// 输出：
//
//	Chunk Document 0
//	Chunk Document 1
//	Chunk Document 2
//	...
//
// 每一个 Chunk：
//
//	Content
//	    = core Chunk.Content
//
//	MetaData
//	    = 原 source metadata 的副本
//	      +
//	      RAG Chunk metadata
//
//	ID
//	    = 新 Chunk ID
//
// -----------------------------------------------------------------------------
// 非常重要：
//
// 这里绝对不能：
//
//	chunkDoc.MetaData = source.MetaData
//
// 然后直接往里面写。
//
// 因为 map 是引用类型。
//
// 那样写会导致：
//
//	修改 Chunk metadata
//
// 同时修改：
//
//	原 source Document metadata
//
// 不同 Chunk 之间也会共享同一张 map。
//
// 所以每一个输出 Document 都必须 clone metadata。
func (t *Transformer) Transform(
	ctx context.Context,
	src []*schema.Document,
	opts ...document.TransformerOption,
) ([]*schema.Document, error) {
	if len(src) == 0 {
		return nil, nil
	}

	// 以 Transformer 创建时配置作为 base，
	// 再应用这一次 Transform 的 opts。
	options := document.GetTransformerImplSpecificOptions(
		&transformerOptions{
			splitter:    cloneSplitterConfig(t.config.Splitter),
			idGenerator: t.config.IDGenerator,
		},
		opts...,
	)

	// option 中的 generator 理论上不会为 nil，
	// 这里继续做一道防御。
	if options.idGenerator == nil {
		options.idGenerator = DefaultIDGenerator
	}

	var result []*schema.Document

	for sourceIndex, source := range src {
		// Transformer 本身没有网络操作，
		// 但是分块大文档仍然可能花一定 CPU。
		//
		// 每处理一个 Document 前检查一次 Context，
		// 让 Graph / Workflow 被取消时能够尽快停止。
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		// Eino pipeline 理论上通常不会传 nil，
		// 但 Adapter 边界不应该因为一个 nil Document panic。
		if source == nil {
			continue
		}

		// 空 Content 没有任何可分块内容。
		//
		// 作为 Splitter Transformer，
		// 我们选择跳过，而不是人为制造一个空 Chunk。
		if source.Content == "" {
			continue
		}

		// -----------------------------------------------------------------
		// 这里是 Adapter 与 RAG Core 唯一真正的算法连接点。
		//
		// Eino 不参与 Chunking Algorithm。
		//
		// Core 也不知道 schema.Document。
		// -----------------------------------------------------------------

		chunks := corechunker.Split(source.Content, cloneSplitterConfig(options.splitter))

		for _, chunk := range chunks {
			if err := ctx.Err(); err != nil {
				return nil, err
			}

			chunkDoc := chunkToDocument(
				source,
				sourceIndex,
				chunk,
				options.idGenerator,
			)
			chunkDoc.MetaData[application.MetaSourceMarkdown] = source.Content
			chunkDoc.MetaData[application.MetaSourceChunkCount] = len(chunks)
			result = append(result, chunkDoc)
		}
	}

	return result, nil
}

// chunkToDocument 把一个 RAG Core Chunk
// 映射为 Eino schema.Document。
//
// 映射原则:
//
//	Chunk.Content
//	    → Document.Content
//
//	Chunk.ContextHeader
//	    → MetaData["rag_context_header"]
//
//	Chunk.Seq
//	    → MetaData["rag_chunk_index"]
//
//	Chunk.Start / End
//	    → MetaData["rag_chunk_start/end"]
//
// 注意：
//
// 我们没有把：
//
//	chunk.EmbeddingContent()
//
// 写入 Document.Content。
//
// 原因非常重要：
//
// Document.Content 应继续代表权威原始 Chunk 内容。
//
// 后面的：
//
//	SearchContent Transformer
//
// 才负责构造：
//
//	title
//	+
//	context header
//	+
//	body
//
// 用于 Embedding / BM25。
//
// 这保证了：
//
//	business content
//
// 和：
//
//	retrieval representation
//
// 始终分离。
func chunkToDocument(
	source *schema.Document,
	sourceIndex int,
	chunk corechunker.Chunk,
	generator IDGenerator,
) *schema.Document {
	metadata := cloneMetadata(source.MetaData)

	metadata[MetaSourceDocumentIndex] = sourceIndex
	metadata[MetaChunkIndex] = chunk.Seq
	metadata[MetaChunkStart] = chunk.Start
	metadata[MetaChunkEnd] = chunk.End
	metadata[MetaContextHeader] = chunk.ContextHeader

	if source.ID != "" {
		metadata[MetaSourceDocumentID] = source.ID
	}

	return &schema.Document{
		ID:       generator(source, sourceIndex, chunk),
		Content:  chunk.Content,
		MetaData: metadata,
	}
}

// DefaultIDGenerator 是默认 Chunk Document ID 生成器。
//
// source 有 ID：
//
//	doc-123
//
// Chunk 3：
//
//	doc-123#chunk-000003
//
// -----------------------------------------------------------------------------
// source 没有 ID 时：
//
// 第2个输入 Document：
//
//	source-000002
//
// Chunk 3：
//
//	source-000002#chunk-000003
//
// -----------------------------------------------------------------------------
// 为什么这里不直接生成 UUID？
//
// 因为 Transformer 最好保持：
//
//	deterministic
//
// 相同输入 + 相同配置：
//
//	得到相同 Chunk ID。
//
// 这对：
//
//	Debug
//	Differential Test
//	重跑 ingestion
//
// 都比较友好。
//
// 真正 PostgreSQL 主键以后仍然可以由数据库生成 UUID，
// 不要求直接复用 schema.Document.ID。
func DefaultIDGenerator(
	source *schema.Document,
	sourceIndex int,
	chunk corechunker.Chunk,
) string {
	base := source.ID

	if base == "" {
		base = fmt.Sprintf("source-%06d", sourceIndex)
	}

	return fmt.Sprintf("%s#chunk-%06d", base, chunk.Seq)
}

// cloneMetadata 对 Eino MetaData 做浅拷贝。
//
// map[string]any 本身必须重新创建，
// 保证每个 Chunk 拥有自己独立的 metadata map。
//
// 注意这是“浅拷贝”：
//
// 如果某个 metadata value 本身是：
//
//	map
//	slice
//	pointer
//
// 那个 value 仍然共享引用。
//
// 这里这样做是刻意的。
//
// Transformer 只会新增/覆盖我们自己的顶层 RAG Metadata Key，
// 不会修改 Loader 放进来的复杂 metadata value。
//
// 因此没有必要做昂贵、语义不确定的通用 deep copy。
func cloneMetadata(src map[string]any) map[string]any {
	if len(src) == 0 {
		return make(map[string]any)
	}

	dst := make(map[string]any, len(src)+6)

	for key, value := range src {
		dst[key] = value
	}

	return dst
}

// cloneSplitterConfig 复制 SplitterConfig 中的 slice 字段。
//
// SplitterConfig 本身是 value struct，
// 但：
//
//	Separators
//	Languages
//
// 是 slice。
//
// 如果只做：
//
//	dst := src
//
// 两个 Config 仍会共享底层 slice。
//
// Adapter 层在保存长期 Config 或调用级 Config 时，
// 最好消除这种意外共享。
func cloneSplitterConfig(src corechunker.SplitterConfig) corechunker.SplitterConfig {
	dst := src

	if src.Separators != nil {
		dst.Separators = append([]string(nil), src.Separators...)
	}

	if src.Languages != nil {
		dst.Languages = append([]string(nil), src.Languages...)
	}

	return dst
}
