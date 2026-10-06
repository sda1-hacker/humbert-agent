package chunker

import (
	"context"
	"fmt"

	"github.com/cloudwego/eino/components/document"
	"github.com/cloudwego/eino/schema"
	corechunker "github.com/sda1-hacker/humbert-agent/internal/rag/chunker"
	"github.com/sda1-hacker/humbert-agent/internal/rag/retrieval"
)

// 编译时确认组件满足 Eino 文档转换接口。
var _ document.Transformer = (*Transformer)(nil)

// IDGenerator 根据来源文档、输入位置和分块信息生成 ID，供业务定制。
type IDGenerator func(
	source *schema.Document,
	sourceIndex int,
	chunk corechunker.Chunk,
) string

// Config 直接复用核心切分配置，仅额外提供 Eino 文档 ID 生成规则。
type Config struct {
	Splitter    corechunker.SplitterConfig
	IDGenerator IDGenerator
}

// DefaultConfig 返回基础切分配置；空策略使用递归切分，自动策略需显式设置。
func DefaultConfig() Config {
	return Config{
		Splitter:    corechunker.DefaultConfig(),
		IDGenerator: DefaultIDGenerator,
	}
}

// Transformer 适配 Eino 文档转换接口，实际分块算法由核心 chunker 实现。
type Transformer struct {
	config Config
}

// NewTransformer 复制切片配置并设置默认 ID 生成器，避免调用方修改影响组件。
func NewTransformer(config Config) *Transformer {
	config.Splitter = config.Splitter.Clone()

	if config.IDGenerator == nil {
		config.IDGenerator = DefaultIDGenerator
	}

	return &Transformer{
		config: config,
	}
}

// transformerOptions 保存当前调用的配置覆盖，不修改组件默认值。
type transformerOptions struct {
	splitter    corechunker.SplitterConfig
	idGenerator IDGenerator
}

// WithSplitterConfig 为当前 Transform 调用覆盖切分配置，并复制可变切片。
func WithSplitterConfig(cfg corechunker.SplitterConfig) document.TransformerOption {
	cfg = cfg.Clone()

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

// Transform 切分输入文档，保留来源元数据；每个输出块使用独立的顶层元数据副本。
func (t *Transformer) Transform(
	ctx context.Context,
	src []*schema.Document,
	opts ...document.TransformerOption,
) ([]*schema.Document, error) {
	if len(src) == 0 {
		return nil, nil
	}

	// 基于独立配置副本应用本次选项，避免覆盖组件默认值。
	options := document.GetTransformerImplSpecificOptions(
		&transformerOptions{
			splitter:    t.config.Splitter.Clone(),
			idGenerator: t.config.IDGenerator,
		},
		opts...,
	)

	// 未提供 ID 生成器时使用稳定的默认规则。
	if options.idGenerator == nil {
		options.idGenerator = DefaultIDGenerator
	}

	var result []*schema.Document

	for sourceIndex, source := range src {
		// 每篇文档和每个输出块都检查取消状态。
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		// 空输入直接跳过，不生成空检索块。
		if source == nil || source.Content == "" {
			continue
		}

		// 算法只处理正文和切分配置，Eino 文档转换由适配器负责。
		chunks := corechunker.Split(source.Content, options.splitter.Clone())

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
			result = append(result, chunkDoc)
		}
	}

	return result, nil
}

// chunkToDocument 将核心分块转换为 Eino 文档，保留正文、来源身份和字符范围。
func chunkToDocument(
	source *schema.Document,
	sourceIndex int,
	chunk corechunker.Chunk,
	generator IDGenerator,
) *schema.Document {
	metadata := retrieval.CloneMetadata(source.MetaData)

	metadata[retrieval.MetaSourceDocumentIndex] = sourceIndex
	metadata[retrieval.MetaChunkIndex] = chunk.Seq
	metadata[retrieval.MetaChunkStart] = chunk.Start
	metadata[retrieval.MetaChunkEnd] = chunk.End
	metadata[retrieval.MetaContextHeader] = chunk.ContextHeader

	if source.ID != "" {
		metadata[retrieval.MetaSourceDocumentID] = source.ID
	}

	return &schema.Document{
		ID:       generator(source, sourceIndex, chunk),
		Content:  chunk.Content,
		MetaData: metadata,
	}
}

// DefaultIDGenerator 使用来源 ID 与分块序号生成稳定 ID；来源无 ID 时使用输入位置。
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
