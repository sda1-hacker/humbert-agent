package config

import (
	"time"

	"github.com/sda1-hacker/humbert-agent/internal/documenttext"
)

const (
	defaultMaxInputTokens = 8192
	defaultMaxBatchTokens = 65536
)

// Default 是唯一预设。实验参数写入 YAML；不在代码中维护另一套示例地址或模型名。
func Default() Config {
	parser := documenttext.DefaultOptions()
	parser.ExcludeHeadersAndFooters = true
	parser.PreserveImages = true
	parser.CacheDirectory = "auto"
	return Config{
		Database: Database{Host: "localhost", Port: 5432, Name: "humbert_rag", SSLMode: "disable"},
		Postgres: (Postgres{}).Effective(),
		Loader:   Loader{ParseOptions: parser, NormalizeLineEndings: true, AssetDirectory: "auto", ImageRecognition: (ImageRecognition{}).Effective()},
		Ingest:   (Ingest{}).Effective(),
		// 对齐 WeKnora 2c0d838 新建知识库预设；低层空策略仍保留递归语义。
		Chunking: Chunking{Splitter: Splitter{Strategy: StrategyAuto, ChunkSize: DefaultChunkSize, ChunkOverlap: DefaultChunkOverlap,
			Separators: []string{"\n\n", "\n", "。", "！", "？", ";", "；"}}, ParentChild: true,
			ParentChunkSize: DefaultParentChunkSize, ChildChunkSize: DefaultChildChunkSize},
		// 地址和超时留零值，加载后按实际选择的协议补齐，避免 OpenAI 默认值影响 Ollama。
		Embedding:    Embedding{Provider: ProviderOpenAI, InputBudget: (Budget{}).Effective()},
		Indexer:      Indexer{EmbeddingBatchSize: DefaultEmbeddingBatchSize},
		Vector:       Vector{TopK: DefaultTopK, ScoreThreshold: DefaultVectorThreshold},
		Keyword:      BM25{TopK: DefaultTopK, ScoreThreshold: DefaultKeywordThreshold},
		Hybrid:       (Hybrid{}).Effective(),
		InputBuilder: (SearchText{}).Effective(),
		Rerank: (Rerank{Disabled: true, Threshold: 0.3, DegradeFactor: 0.7, DegradeFloor: 0.3,
			FallbackMinScore: 0.15, MMRLambda: 0.7}).Effective(),
		Search: (Search{ExpandParents: true}).Effective(),
	}
}

// Effective 只补齐图片识别默认值；agent 路由的模型与凭据由执行阶段注入的 Registry 负责。
func (c ImageRecognition) Effective() ImageRecognition {
	c.Provider = normalizeName(c.Provider)
	if c.Provider == "" {
		c.Provider = ProviderAgent
	}
	if c.Pipeline == "" {
		c.Pipeline = "default"
	}
	if len(c.Actions) == 0 {
		c.Actions = []string{"caption", "ocr"}
		if c.Provider == "tesseract" {
			c.Actions = []string{"ocr"}
		}
	}
	if c.MaxConcurrent == 0 {
		c.MaxConcurrent = 2
	}
	if c.RetryAttempts == 0 {
		c.RetryAttempts = 3
	}
	if c.RetryInterval == 0 {
		c.RetryInterval = 250 * time.Millisecond
	}
	c.BaseURL, c.Model = trimBaseURL(c.BaseURL), trim(c.Model)
	if c.Provider == ProviderOllama && c.BaseURL == "" {
		c.BaseURL = DefaultOllamaURL
	}
	if c.Timeout == 0 {
		c.Timeout = time.Minute
	}
	if c.MaxOutputTokens == 0 {
		c.MaxOutputTokens = 4096
	}
	return c
}

// Effective 为未指定的导入限额补上默认值。
func (c Ingest) Effective() Ingest {
	if c.MaxConcurrent == 0 {
		c.MaxConcurrent = 2
	}
	if c.MaxChunks == 0 {
		c.MaxChunks = 5000
	}
	if c.MaxTextBytes == 0 {
		c.MaxTextBytes = 8 << 20
	}
	if c.Timeout == 0 {
		c.Timeout = 10 * time.Minute
	}
	return c
}

// Effective 按实际协议补齐服务地址和超时，不猜测模型名或凭据。
func (c Embedding) Effective() Embedding {
	if c.RetryAttempts == 0 {
		c.RetryAttempts = 3
	}
	if c.RetryInterval == 0 {
		c.RetryInterval = 250 * time.Millisecond
	}
	c.Provider = normalizeName(c.Provider)
	if c.Provider == "" {
		c.Provider = ProviderOpenAI
	}
	c.BaseURL, c.Model = trimBaseURL(c.BaseURL), trim(c.Model)
	if c.BaseURL == "" {
		c.BaseURL = "https://api.openai.com/v1"
		if c.Provider == ProviderOllama {
			c.BaseURL = DefaultOllamaURL
		}
	}
	if c.Timeout == 0 {
		c.Timeout = DefaultRequestTimeout
		if c.Provider == ProviderOllama {
			c.Timeout = DefaultOllamaTimeout
		}
	}
	return c
}

// Effective 补齐数据库操作超时；DSN 的显式参数由连接池装配时优先保留。
func (c Postgres) Effective() Postgres {
	if c.ConnectTimeout == 0 {
		c.ConnectTimeout = 10 * time.Second
	}
	if c.StatementTimeout == 0 {
		c.StatementTimeout = DefaultRequestTimeout
	}
	if c.LockTimeout == 0 {
		c.LockTimeout = 5 * time.Second
	}
	if c.ExtensionTimeout == 0 {
		c.ExtensionTimeout = DefaultRequestTimeout
	}
	if c.MigrationTimeout == 0 {
		c.MigrationTimeout = 5 * time.Minute
	}
	return c
}

// Effective 仅在整组参数为零时使用默认融合权重，保留单通道显式的零权重。
func (c RRF) Effective() RRF {
	if c == (RRF{}) {
		return RRF{K: 60, VectorWeight: 0.7, KeywordWeight: 0.3}
	}
	return c
}

// Effective 补齐召回规模、总超时和融合策略，不改变单通道超时与失败策略。
func (c Hybrid) Effective() Hybrid {
	if c.TopK == 0 {
		c.TopK = DefaultTopK
	}
	if c.ChannelTopK == 0 {
		c.ChannelTopK = DefaultTopK
	}
	if c.Timeout == 0 {
		c.Timeout = DefaultRequestTimeout
	}
	c.RRF = c.RRF.Effective()
	return c
}

// Effective 补齐搜索限额；ExpandParents 等布尔开关保持调用方的显式取值。
func (c Search) Effective() Search {
	if c.FinalTopK == 0 {
		c.FinalTopK = 5
	}
	if c.MaxContextTokens == 0 {
		c.MaxContextTokens = 16384
	}
	if c.Timeout == 0 {
		c.Timeout = DefaultRequestTimeout
	}
	if c.CountTokens == nil {
		c.CountTokens = func(s string) int { return len(s) }
	}
	return c
}

// Effective 只补齐候选规模和整组缺省权重，保留零阈值与禁用状态。
func (c Rerank) Effective() Rerank {
	if c.TopK == 0 {
		c.TopK = 5
	}
	if c.MaxCandidates == 0 {
		c.MaxCandidates = 200
	}
	if c.ModelWeight == 0 && c.BaseWeight == 0 && c.SourceWeight == 0 {
		c.ModelWeight, c.BaseWeight, c.SourceWeight = 0.6, 0.3, 0.1
	}
	return c
}

// Effective 为缺省的标题键和上下文键提供统一规则。
func (c SearchText) Effective() SearchText {
	if len(c.TitleKeys) == 0 {
		c.TitleKeys = []string{"title", "_title", "file_name"}
	}
	if c.ContextHeaderKey == "" {
		c.ContextHeaderKey = "rag_context_header"
	}
	return c
}

// Effective 补齐分块大小和分隔符；显式的零重叠仍表示关闭重叠。
func (c Splitter) Effective() Splitter {
	if c.ChunkSize <= 0 {
		c.ChunkSize = DefaultChunkSize
	}
	if c.ChunkOverlap < 0 {
		c.ChunkOverlap = 0
	}
	if len(c.Separators) == 0 {
		c.Separators = []string{"\n\n", "\n", "。"}
	}
	return c
}

// ForParentChild 从同一份配置推导父子窗口，复制切片以隔离后续修改。
func (c Splitter) ForParentChild(parentSize, childSize int) (parent, child Splitter) {
	if parentSize <= 0 {
		parentSize = DefaultParentChunkSize
	}
	if childSize <= 0 {
		childSize = DefaultChildChunkSize
	}
	parent, child = c.Clone(), c.Clone()
	parent.ChunkSize, parent.TokenLimit = parentSize, 0 // 父块补充上下文，不受子块近似 token 目标限制。
	child.ChunkSize, child.ChunkOverlap = childSize, childSize/5
	if c.ChunkOverlap == 0 {
		child.ChunkOverlap = 0
	}
	return parent, child
}
