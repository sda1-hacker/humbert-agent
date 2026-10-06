package retriever

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	eino "github.com/cloudwego/eino/components/retriever"
	"github.com/cloudwego/eino/schema"
	"github.com/sda1-hacker/humbert-agent/internal/logging"
	"github.com/sda1-hacker/humbert-agent/internal/rag/retrieval"
)

const (
	FailureStrict       = "strict"
	FailureAllowPartial = "allow_partial"
)

// HybridConfig 只配置召回组合策略，不包含具体后端的维度或分数阈值。
type HybridConfig struct {
	// TopK 融合后保留的候选数量，业务入口可通过 Eino 选项覆盖。
	TopK int
	// ChannelTopK 每路原始召回数量，至少覆盖本次融合数量。
	ChannelTopK int
	// Timeout 整个混合检索的超时，零值使用默认值。
	Timeout time.Duration
	// ChannelTimeout 单路召回超时，零值共用整体超时。
	ChannelTimeout time.Duration
	// FailurePolicy 默认任一路失败即返回错误；allow_partial 允许保留另一条成功通道。
	FailurePolicy string
	// RRF 两路排名融合的权重与平滑参数。
	RRF retrieval.RRFConfig
}

// DefaultHybridConfig 返回两路候选数量与 RRF 默认配置。
func DefaultHybridConfig() HybridConfig {
	return HybridConfig{TopK: 50, ChannelTopK: 50, RRF: retrieval.DefaultRRFConfig()}
}

// Validate 检查候选数量、超时、失败策略和融合权重。
func (c HybridConfig) Validate() error {
	if c.TopK < 0 || c.ChannelTopK < 0 || c.Timeout < 0 || c.ChannelTimeout < 0 {
		return errors.New("rag hybrid: negative size or timeout")
	}
	if c.FailurePolicy != "" && c.FailurePolicy != FailureStrict && c.FailurePolicy != FailureAllowPartial {
		return fmt.Errorf("rag hybrid: invalid failure policy %q", c.FailurePolicy)
	}
	return c.RRF.Validate()
}

func (c HybridConfig) effective() HybridConfig {
	defaults := DefaultHybridConfig()
	if c.TopK == 0 {
		c.TopK = defaults.TopK
	}
	if c.ChannelTopK == 0 {
		c.ChannelTopK = defaults.ChannelTopK
	}
	if c.Timeout == 0 {
		c.Timeout = 30 * time.Second
	}
	c.RRF = c.RRF.Effective()
	return c
}

// Hybrid 只负责并发召回、失败策略和 RRF；两路通道可以来自不同的库。
type Hybrid struct {
	vector  eino.Retriever
	keyword eino.Retriever
	config  HybridConfig
}

// NewHybrid 组合 Eino 原生向量与关键词检索器，不绑定具体索引库。
func NewHybrid(vector, keyword eino.Retriever, cfg HybridConfig) (*Hybrid, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	cfg = cfg.effective()
	if cfg.RRF.VectorWeight > 0 && vector == nil {
		return nil, errors.New("rag hybrid: missing vector channel")
	}
	if cfg.RRF.KeywordWeight > 0 && keyword == nil {
		return nil, errors.New("rag hybrid: missing keyword channel")
	}
	return &Hybrid{vector: vector, keyword: keyword, config: cfg}, nil
}

// 编译期保证混合检索器可以直接加入 Eino Graph。
var _ eino.Retriever = (*Hybrid)(nil)

// Options 给两路组件分别传递原生调用选项，阈值和专属过滤语法由各后端解释。
type Options struct{ Vector, Keyword []eino.Option }

// WithVectorOptions 为向量通道单独设置 Eino 选项，知识库范围仍由公共选项约束。
func WithVectorOptions(opts ...eino.Option) eino.Option {
	copied := append([]eino.Option(nil), opts...)
	return eino.WrapImplSpecificOptFn(func(o *Options) { o.Vector = copied })
}

// WithKeywordOptions 为关键词通道单独设置 Eino 选项，避免混用两路原始分数阈值。
func WithKeywordOptions(opts ...eino.Option) eino.Option {
	copied := append([]eino.Option(nil), opts...)
	return eino.WrapImplSpecificOptFn(func(o *Options) { o.Keyword = copied })
}

// Retrieve 并发召回两路结果，执行 RRF 融合并返回原生 Eino 文档。
func (h *Hybrid) Retrieve(ctx context.Context, query string, opts ...eino.Option) ([]*schema.Document, error) {
	docs, _, err := h.RetrieveWithDiagnostics(ctx, query, opts...)
	return docs, err
}

// RetrieveWithDiagnostics 附加失败诊断，普通调用仍使用 Eino 的 Retrieve 方法。
func (h *Hybrid) RetrieveWithDiagnostics(ctx context.Context, query string, opts ...eino.Option) ([]*schema.Document, retrieval.Diagnostics, error) {
	diag := retrieval.Diagnostics{ModeUsed: retrieval.MatchHybrid}
	if err := ctx.Err(); err != nil {
		return nil, diag, err
	}
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, diag, errors.New("rag hybrid: empty query")
	}
	topK := h.config.TopK
	common := eino.GetCommonOptions(&eino.Options{TopK: &topK}, opts...)
	if *common.TopK <= 0 {
		return nil, diag, errors.New("rag hybrid: invalid top k")
	}
	topK = *common.TopK
	if common.ScoreThreshold != nil && (math.IsNaN(*common.ScoreThreshold) || math.IsInf(*common.ScoreThreshold, 0)) {
		return nil, diag, errors.New("rag hybrid: invalid score threshold")
	}
	// RRF 阈值作用于融合后的分数，不能误用作原始 BM25 或向量阈值。
	shared := []eino.Option{}
	if common.Index != nil {
		shared = append(shared, eino.WithIndex(*common.Index))
	}
	if common.SubIndex != nil {
		shared = append(shared, eino.WithSubIndex(*common.SubIndex))
	}
	if common.Embedding != nil {
		shared = append(shared, eino.WithEmbedding(common.Embedding))
	}
	if common.DSLInfo != nil {
		shared = append(shared, eino.WithDSLInfo(common.DSLInfo))
	}
	specific := eino.GetImplSpecificOptions(&Options{}, opts...)
	channelOptions := func(own []eino.Option) []eino.Option {
		options := append(append([]eino.Option(nil), shared...), eino.WithTopK(max(h.config.ChannelTopK, topK)))
		// 默认使用候选池大小，允许每一路显式覆盖；全局 Index 最后确定。
		options = append(options, own...)
		if common.Index != nil {
			options = append(options, eino.WithIndex(*common.Index))
		}
		return options
	}
	ctx, cancel := context.WithTimeout(ctx, h.config.Timeout)
	defer cancel()

	type reply struct {
		channel retrieval.MatchType
		results []retrieval.SearchResult
		err     error
	}
	// 缓冲区允许超时后返回的通道退出，不阻塞在发送结果上。
	out := make(chan reply, 2)
	run := func(channel eino.Retriever, kind retrieval.MatchType, weight float64, own []eino.Option) {
		if weight == 0 {
			out <- reply{channel: kind}
			return
		}
		channelCtx := ctx
		stop := func() {}
		if h.config.ChannelTimeout > 0 {
			channelCtx, stop = context.WithTimeout(ctx, h.config.ChannelTimeout)
		}
		defer stop()
		// 单路截止时间也必须生效，允许另一路结果在显式降级模式下继续使用。
		type response struct {
			docs []*schema.Document
			err  error
		}
		completed := make(chan response, 1)
		go func() {
			docs, err := channel.Retrieve(channelCtx, query, channelOptions(own)...)
			completed <- response{docs, err}
		}()
		var docs []*schema.Document
		var err error
		select {
		case r := <-completed:
			docs, err = r.docs, r.err
		case <-channelCtx.Done():
			err = channelCtx.Err()
		}
		var results []retrieval.SearchResult
		if err == nil {
			results, err = retrieval.Results(docs)
		}
		if err == nil && common.Index != nil {
			for i := range results {
				if results[i].CollectionID != "" && results[i].CollectionID != *common.Index {
					err = errors.New("rag hybrid: backend returned another collection")
					break
				}
				results[i].CollectionID = *common.Index
			}
		}
		// 即使适配器忽略取消，也不接受超时后才返回的结果。
		if channelCtx.Err() != nil {
			err = channelCtx.Err()
		}
		out <- reply{channel: kind, results: results, err: err}
	}
	go run(h.vector, retrieval.MatchVector, h.config.RRF.VectorWeight, specific.Vector)
	go run(h.keyword, retrieval.MatchKeyword, h.config.RRF.KeywordWeight, specific.Keyword)

	var vector, keyword reply
	for i := 0; i < 2; i++ {
		select {
		case received := <-out:
			if received.channel == retrieval.MatchVector {
				vector = received
			} else {
				keyword = received
			}
			if received.err != nil {
				if ctx.Err() != nil {
					return nil, diag, ctx.Err()
				}
				if errors.Is(received.err, context.Canceled) || h.config.FailurePolicy != FailureAllowPartial {
					return nil, diag, fmt.Errorf("hybrid %s channel: %w", received.channel, received.err)
				}
				diag.Degraded = true
				diag.Channels = append(diag.Channels, retrieval.ChannelDiagnostic{Channel: received.channel, Error: logging.SafeErrorText(received.err, 2048)})
			}
		case <-ctx.Done():
			return nil, diag, ctx.Err()
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, diag, err
	}
	vectorFailed := vector.err != nil || h.config.RRF.VectorWeight == 0
	keywordFailed := keyword.err != nil || h.config.RRF.KeywordWeight == 0
	if vectorFailed && keywordFailed {
		return nil, diag, errors.Join(vector.err, keyword.err)
	}
	if vectorFailed {
		vector.results = nil
		diag.ModeUsed = retrieval.MatchKeyword
	}
	if keywordFailed {
		keyword.results = nil
		diag.ModeUsed = retrieval.MatchVector
	}
	results := retrieval.FuseRRF(vector.results, keyword.results, h.config.RRF)
	if common.ScoreThreshold != nil {
		filtered := results[:0]
		for _, r := range results {
			if r.Score >= *common.ScoreThreshold {
				filtered = append(filtered, r)
			}
		}
		results = filtered
	}
	return retrieval.Documents(retrieval.Limit(results, topK)), diag, nil
}
