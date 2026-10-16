package config

import (
	"errors"
	"fmt"
	"math"
	"net/url"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

// 配置错误统一定义；业务调用方直接用 errors.Is 判断，不再跨包重复导出。
var (
	ErrInputBudget                = errors.New("rag embedding: input exceeds token budget")
	ErrMissingModel               = errors.New("rag embedding: missing model")
	ErrInvalidDimensions          = errors.New("rag embedding: invalid dimensions")
	ErrInvalidTimeout             = errors.New("rag embedding: invalid timeout")
	ErrUnsupportedProvider        = errors.New("rag embedding: unsupported provider")
	ErrInvalidTopK                = errors.New("postgres retriever: invalid top k")
	ErrInvalidThreshold           = errors.New("postgres retriever: invalid score threshold")
	ErrMissingEndpoint            = errors.New("rerank http: missing endpoint")
	ErrRerankMissingModel         = errors.New("rerank http: missing model")
	ErrInvalidEndpoint            = errors.New("rerank http: invalid endpoint")
	ErrInvalidDatabase            = errors.New("rag: invalid database configuration")
	ErrEmbeddingDimensionMismatch = errors.New("rag: embedding dimension does not match postgres schema")
)

func trim(s string) string          { return strings.TrimSpace(s) }
func trimBaseURL(s string) string   { return strings.TrimRight(trim(s), "/") }
func normalizeName(s string) string { return strings.ToLower(trim(s)) }
func finite(v float64) bool         { return !math.IsNaN(v) && !math.IsInf(v, 0) }

func (c Postgres) Validate() error {
	for _, d := range []time.Duration{c.ConnectTimeout, c.StatementTimeout, c.LockTimeout, c.ExtensionTimeout, c.MigrationTimeout} {
		if d < 0 || (d > 0 && d < time.Millisecond) {
			return errors.New("rag config postgres: timeout must be zero (default) or at least 1ms")
		}
	}
	return nil
}

func (c Ingest) Validate() error {
	if c.MaxConcurrent < 0 || c.MaxChunks < 0 || c.MaxTextBytes < 0 || c.Timeout < 0 {
		return errors.New("rag: negative ingestion limit")
	}
	return nil
}

func (c Splitter) Validate() error {
	if c.ChunkSize < 0 || c.ChunkOverlap < 0 || c.TokenLimit < 0 {
		return fmt.Errorf("rag chunker: sizes, overlap and token limit must be non-negative")
	}
	switch c.Strategy {
	case "", StrategyAuto, StrategyHeading, StrategyHeuristic, StrategyRecursive:
	default:
		return fmt.Errorf("rag chunker: unknown strategy %q", c.Strategy)
	}
	for _, s := range c.Separators {
		if s == "" {
			return fmt.Errorf("rag chunker: empty separator")
		}
	}
	return nil
}

func (b Budget) Validate() error {
	if b.MaxInputTokens < 0 || b.MaxBatchTokens < 0 {
		return fmt.Errorf("rag embedding: negative token budget")
	}
	return nil
}

// ValidateOptions 允许迁移工具只加载参数；缺失模型由真正创建 Embedder 时检查。
func (c Embedding) ValidateOptions() error {
	c = c.Effective()
	if c.Provider != ProviderOpenAI && c.Provider != ProviderOllama {
		return fmt.Errorf("%w: %q", ErrUnsupportedProvider, c.Provider)
	}
	u, err := url.Parse(c.BaseURL)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("rag embedding: BaseURL must be an HTTP(S) API base without embedded credentials, query or fragment")
	}
	if c.Provider == ProviderOllama && (u.Path == "/v1" || strings.HasSuffix(u.Path, "/api/embed") || strings.HasSuffix(u.Path, "/api/embeddings")) {
		return errors.New("rag embedding: native Ollama BaseURL must be the server base, e.g. http://localhost:11434")
	}
	if err := c.InputBudget.Validate(); err != nil {
		return err
	}
	if c.Dimensions < 0 {
		return fmt.Errorf("%w: %d", ErrInvalidDimensions, c.Dimensions)
	}
	if c.Timeout < 0 {
		return fmt.Errorf("%w: %s", ErrInvalidTimeout, c.Timeout)
	}
	if c.RetryAttempts < 1 || c.RetryAttempts > 5 || c.RetryInterval < time.Millisecond || c.RetryInterval > 10*time.Second {
		return errors.New("rag embedding: retry_attempts must be in 1..5 and retry_interval in 1ms..10s")
	}
	return nil
}

func (c Embedding) Validate() error {
	if err := c.ValidateOptions(); err != nil {
		return err
	}
	if trim(c.Model) == "" {
		return ErrMissingModel
	}
	return nil
}

func optionalTopK(k int) error {
	if k < 0 || k > PostgresMaxTopK {
		return ErrInvalidTopK
	}
	return nil
}
func (c Vector) Validate() error {
	if err := optionalTopK(c.TopK); err != nil {
		return err
	}
	if !finite(c.ScoreThreshold) || c.ScoreThreshold < 0 || c.ScoreThreshold > 1 {
		return ErrInvalidThreshold
	}
	if c.Dimensions < 0 || c.Dimensions > PostgresMaxDimensions {
		return fmt.Errorf("%w: postgres vector dimensions must be in 1..%d", ErrInvalidDimensions, PostgresMaxDimensions)
	}
	return nil
}
func (c BM25) Validate() error {
	if err := optionalTopK(c.TopK); err != nil {
		return err
	}
	if !finite(c.ScoreThreshold) || c.ScoreThreshold < 0 {
		return ErrInvalidThreshold
	}
	return nil
}
func (c RRF) Validate() error {
	if c == (RRF{}) {
		return nil
	}
	if c.K < 0 || c.K > 1_000_000 || !finite(c.VectorWeight) || !finite(c.KeywordWeight) || c.VectorWeight < 0 || c.KeywordWeight < 0 || c.VectorWeight+c.KeywordWeight <= 0 || !finite(c.VectorWeight+c.KeywordWeight) {
		return fmt.Errorf("rag RRF: invalid k or weights")
	}
	return nil
}
func (c Hybrid) Validate() error {
	if c.TopK < 0 || c.ChannelTopK < 0 || c.Timeout < 0 || c.ChannelTimeout < 0 {
		return errors.New("rag hybrid: negative size or timeout")
	}
	if c.FailurePolicy != "" && c.FailurePolicy != FailureStrict && c.FailurePolicy != FailureAllowPartial {
		return fmt.Errorf("rag hybrid: invalid failure policy %q", c.FailurePolicy)
	}
	return c.RRF.Validate()
}
func (c Search) Validate() error {
	if c.FinalTopK < 0 || c.MaxContextTokens < 0 || c.Timeout < 0 {
		return fmt.Errorf("rag search: negative result limit, context budget or timeout")
	}
	return nil
}
func (c Rerank) Validate() error {
	if c.TopK < 0 || c.MaxCandidates < 0 {
		return fmt.Errorf("rag rerank: negative candidate limit")
	}
	for _, v := range []float64{c.Threshold, c.DegradeFactor, c.DegradeFloor, c.FallbackMinScore, c.ModelWeight, c.BaseWeight, c.SourceWeight, c.MMRLambda} {
		if !finite(v) || v < 0 || v > 1 {
			return fmt.Errorf("rag rerank: scores, weights and diversity must be finite values in [0,1]")
		}
	}
	if c.ModelWeight+c.BaseWeight+c.SourceWeight > 1.000000001 {
		return fmt.Errorf("rag rerank: total weight exceeds 1")
	}
	return nil
}
func (c RerankHTTP) Validate() error {
	if trim(c.Endpoint) == "" {
		return ErrMissingEndpoint
	}
	u, err := url.ParseRequestURI(trim(c.Endpoint))
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return ErrInvalidEndpoint
	}
	if trim(c.Model) == "" {
		return ErrRerankMissingModel
	}
	if c.Timeout < 0 {
		return fmt.Errorf("rerank http: invalid timeout %s", c.Timeout)
	}
	return nil
}

// Validate 集中检查组件参数及跨阶段约束，不进行 I/O，也不要求迁移命令配置模型。
func (c Config) Validate() error {
	checks := []struct {
		name string
		err  error
	}{
		{"postgres", c.Postgres.Validate()},
		{"loader.parse_options", c.Loader.ParseOptions.Validate()}, {"ingest", c.Ingest.Validate()}, {"chunking.splitter", c.Chunking.Splitter.Validate()},
		{"embedding", c.Embedding.ValidateOptions()}, {"vector", c.Vector.Validate()}, {"keyword", c.Keyword.Validate()},
		{"hybrid", c.Hybrid.Validate()}, {"rerank", c.Rerank.Validate()}, {"search", c.Search.Validate()},
	}
	for _, check := range checks {
		if check.err != nil {
			return fmt.Errorf("rag config %s: %w", check.name, check.err)
		}
	}
	if c.Chunking.ParentChunkSize < 0 || c.Chunking.ChildChunkSize < 0 {
		return errors.New("rag config chunking: negative parent/child size")
	}
	if c.Indexer.EmbeddingBatchSize < 0 {
		return errors.New("rag config indexer: negative embedding batch size")
	}
	if c.Indexer.InputBudget.MaxInputTokens != 0 || c.Indexer.InputBudget.MaxBatchTokens != 0 || c.Indexer.InputBudget.CountTokens != nil {
		return errors.New("rag: configure Embedding.InputBudget for both index and query; Indexer.InputBudget is for standalone indexers")
	}
	if c.Hybrid.Effective().TopK < c.Search.Effective().FinalTopK {
		return errors.New("rag: recall pool must be at least final top k")
	}
	if !c.Rerank.Disabled {
		if c.RerankProvider == nil {
			return errors.New("rag config rerank_provider: required when rerank is enabled")
		}
		if err := c.RerankProvider.Validate(); err != nil {
			return fmt.Errorf("rag config rerank_provider: %w", err)
		}
		if c.Rerank.Effective().MaxCandidates < c.Search.Effective().FinalTopK {
			return errors.New("rag: rerank candidate pool must be at least final top k")
		}
	}
	return nil
}

// ValidateDatabase 检查字段并使用 pgx 原生解析器验证组合结果，不连接数据库。
// 不输出原始连接串或驱动解析错误，避免把其中的密码回显到终端。
func (c Config) ValidateDatabase() error {
	db := c.Database
	for _, field := range []struct{ name, value string }{
		{"host", db.Host}, {"name", db.Name}, {"user", db.User},
	} {
		if trim(field.value) == "" || strings.ContainsRune(field.value, 0) {
			return fmt.Errorf("%w: 请填写 database.%s，不能为空或包含空字符", ErrInvalidDatabase, field.name)
		}
	}
	if !strings.HasPrefix(db.Host, "/") && strings.ContainsAny(db.Host, "/\\@?# \t\r\n") {
		return fmt.Errorf("%w: database.host 请填写主机名、IP 或 Unix socket 目录，不是 URL", ErrInvalidDatabase)
	}
	if db.Port < 1 || db.Port > 65535 {
		return fmt.Errorf("%w: database.port 必须在 1..65535 之间", ErrInvalidDatabase)
	}
	if len(db.Name) > 63 {
		return fmt.Errorf("%w: database.name 不能超过 63 字节", ErrInvalidDatabase)
	}
	if strings.ContainsRune(db.Password, 0) {
		return fmt.Errorf("%w: database.password 不能包含空字符", ErrInvalidDatabase)
	}
	parsed, err := pgconn.ParseConfig(db.DSN())
	if err != nil || parsed.Host != db.Host {
		return fmt.Errorf("%w: 请检查 database.host、port、name、user 和 sslmode", ErrInvalidDatabase)
	}
	return nil
}

// ValidatePostgres 仅供当前 PG 装配入口使用，固定维度与候选上限不影响未来 Eino 外部存储。
func (c Config) ValidatePostgres() error {
	if err := c.Validate(); err != nil {
		return err
	}
	if err := c.ValidateDatabase(); err != nil {
		return err
	}
	if trim(c.CollectionID) == "" {
		return errors.New("rag config collection_id: required")
	}
	if err := c.Embedding.Validate(); err != nil {
		return fmt.Errorf("rag embedding config: %w", err)
	}
	if c.Embedding.Dimensions <= 0 || c.Embedding.Dimensions > PostgresMaxDimensions {
		return fmt.Errorf("%w: 请填写 embedding.dimensions，PG halfvec HNSW 支持 1..%d，且必须与模型实际输出一致", ErrInvalidDimensions, PostgresMaxDimensions)
	}
	if c.Hybrid.TopK > PostgresMaxTopK || c.Hybrid.ChannelTopK > PostgresMaxTopK || c.Search.FinalTopK > PostgresMaxTopK {
		return ErrInvalidTopK
	}
	return nil
}
