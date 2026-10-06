package retriever_test

import (
	"context"
	"errors"
	eino "github.com/cloudwego/eino/components/retriever"
	"github.com/cloudwego/eino/schema"
	"github.com/sda1-hacker/humbert-agent/internal/rag"
	"github.com/sda1-hacker/humbert-agent/internal/rag/retrieval"
	"github.com/sda1-hacker/humbert-agent/internal/rag/retriever"
	"github.com/sda1-hacker/humbert-agent/internal/rag/search"
	"testing"
	"time"
)

// 测试后端只实现 Eino 原生接口，可直接替换成 eino-ext 的组件。
type backend func(context.Context, string, ...eino.Option) ([]*schema.Document, error)

func (b backend) Retrieve(ctx context.Context, q string, opts ...eino.Option) ([]*schema.Document, error) {
	return b(ctx, q, opts...)
}

func TestIndependentNativeRetrieversAndModes(t *testing.T) {
	makeBackend := func(id string, score float64) backend {
		return func(_ context.Context, q string, opts ...eino.Option) ([]*schema.Document, error) {
			o := eino.GetCommonOptions(nil, opts...)
			if q != "question" || o.Index == nil || *o.Index != "kb" || o.TopK == nil || *o.TopK < 3 {
				t.Errorf("错误原生请求: %+v", o)
			}
			return []*schema.Document{(&schema.Document{ID: "shared", Content: "共享正文"}).WithScore(score), (&schema.Document{ID: id, Content: id}).WithScore(score / 2)}, nil
		}
	}
	vector, keyword := makeBackend("vector", .9), makeBackend("keyword", 20)
	h, err := retriever.NewHybrid(vector, keyword, retriever.DefaultHybridConfig())
	if err != nil {
		t.Fatal(err)
	}
	service, err := rag.New(context.Background(), rag.Dependencies{Config: rag.Config{Retriever: h, VectorRetriever: vector, KeywordRetriever: keyword, Search: search.Config{FinalTopK: 3}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"hybrid", "semantic", "keyword"} {
		result, err := service.Search(context.Background(), rag.SearchRequest{CollectionID: "kb", Query: "question", Mode: mode})
		if err != nil || result.Results[0].ChunkID != "shared" {
			t.Fatalf("%s: %+v %v", mode, result, err)
		}
		if mode == "hybrid" && (len(result.Results) != 3 || result.Results[0].MatchType != retrieval.MatchHybrid) {
			t.Fatalf("融合或去重错误: %+v", result)
		}
		if mode != "hybrid" && len(result.Results) != 2 {
			t.Fatalf("单路混入其他结果: %+v", result)
		}
	}
}

func TestNativeRankAndOptionForwarding(t *testing.T) {
	// 没有写 Score 的 Retriever 仍有明确排名，不能按 ID 重新排列。
	vector := backend(func(_ context.Context, _ string, opts ...eino.Option) ([]*schema.Document, error) {
		o := eino.GetCommonOptions(nil, opts...)
		if o.ScoreThreshold == nil || *o.ScoreThreshold != .4 || o.SubIndex == nil || *o.SubIndex != "sub" || o.DSLInfo["filter"] != "value" {
			t.Errorf("选项未传递: %+v", o)
		}
		return []*schema.Document{{ID: "z", Content: "z"}, {ID: "a", Content: "a"}}, nil
	})
	keyword := backend(func(context.Context, string, ...eino.Option) ([]*schema.Document, error) {
		return []*schema.Document{{ID: "z", Content: "z"}, {ID: "a", Content: "a"}}, nil
	})
	h, _ := retriever.NewHybrid(vector, keyword, retriever.DefaultHybridConfig())
	docs, err := h.Retrieve(context.Background(), "q", eino.WithTopK(1), eino.WithIndex("kb"), eino.WithSubIndex("sub"), eino.WithDSLInfo(map[string]any{"filter": "value"}), eino.WithScoreThreshold(.95), retriever.WithVectorOptions(eino.WithScoreThreshold(.4)))
	if err != nil || len(docs) != 1 || docs[0].ID != "z" {
		t.Fatalf("原生排名或融合阈值错误: %+v %v", docs, err)
	}
}

func TestFailurePolicyAndDeadlines(t *testing.T) {
	vector := backend(func(context.Context, string, ...eino.Option) ([]*schema.Document, error) {
		return []*schema.Document{(&schema.Document{ID: "v", Content: "v"}).WithScore(1)}, nil
	})
	unavailable := errors.New("keyword unavailable")
	keyword := backend(func(context.Context, string, ...eino.Option) ([]*schema.Document, error) { return nil, unavailable })
	cfg := retriever.DefaultHybridConfig()
	cfg.FailurePolicy = retriever.FailureAllowPartial
	h, _ := retriever.NewHybrid(vector, keyword, cfg)
	docs, diag, err := h.RetrieveWithDiagnostics(context.Background(), "q")
	if err != nil || len(docs) != 1 || !diag.Degraded || diag.ModeUsed != retrieval.MatchVector || len(diag.Channels) != 1 {
		t.Fatalf("%+v %+v %v", docs, diag, err)
	}
	cfg.FailurePolicy = retriever.FailureStrict
	h, _ = retriever.NewHybrid(vector, keyword, cfg)
	if _, err := h.Retrieve(context.Background(), "q"); !errors.Is(err, unavailable) {
		t.Fatal(err)
	}
	release := make(chan struct{})
	defer close(release)
	blocked := backend(func(context.Context, string, ...eino.Option) ([]*schema.Document, error) { <-release; return nil, nil })
	cfg.Timeout = 20 * time.Millisecond
	h, _ = retriever.NewHybrid(blocked, vector, cfg)
	if _, err := h.Retrieve(context.Background(), "q"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	service, err := rag.New(context.Background(), rag.Dependencies{Config: rag.Config{Retriever: blocked, Search: search.Config{Timeout: 20 * time.Millisecond}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Search(context.Background(), rag.SearchRequest{CollectionID: "kb", Query: "q"}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := h.Retrieve(ctx, "q"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestDisabledChannel(t *testing.T) {
	keyword := backend(func(context.Context, string, ...eino.Option) ([]*schema.Document, error) {
		return []*schema.Document{(&schema.Document{ID: "k", Content: "k"}).WithScore(2)}, nil
	})
	cfg := retriever.DefaultHybridConfig()
	cfg.RRF = retrieval.RRFConfig{KeywordWeight: 1}
	h, err := retriever.NewHybrid(nil, keyword, cfg)
	if err != nil {
		t.Fatal(err)
	}
	docs, diag, err := h.RetrieveWithDiagnostics(context.Background(), "q")
	if err != nil || len(docs) != 1 || diag.ModeUsed != retrieval.MatchKeyword {
		t.Fatalf("%+v %+v %v", docs, diag, err)
	}
}

// 单路超时必须能按配置降级，不能一直等到整体超时才丢弃另一路结果。
func TestChannelDeadlineAllowsExplicitPartialResult(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	blocked := backend(func(context.Context, string, ...eino.Option) ([]*schema.Document, error) { <-release; return nil, nil })
	ready := backend(func(context.Context, string, ...eino.Option) ([]*schema.Document, error) {
		return []*schema.Document{(&schema.Document{ID: "ready", Content: "正文"}).WithScore(.8)}, nil
	})
	cfg := retriever.DefaultHybridConfig()
	cfg.Timeout = time.Second
	cfg.ChannelTimeout = 20 * time.Millisecond
	cfg.FailurePolicy = retriever.FailureAllowPartial
	h, _ := retriever.NewHybrid(blocked, ready, cfg)
	docs, diag, err := h.RetrieveWithDiagnostics(context.Background(), "q")
	if err != nil || len(docs) != 1 || docs[0].ID != "ready" || !diag.Degraded || diag.ModeUsed != retrieval.MatchKeyword {
		t.Fatalf("%+v %+v %v", docs, diag, err)
	}
}
