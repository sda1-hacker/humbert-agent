// 查询与评测保留在同一文件，支持 GoLand 按单文件直接运行 main.go。
package main

import (
	"context"
	"fmt"
	"math"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgxpool"

	example "github.com/sda1-hacker/humbert-agent/examples/5-rag"
	"github.com/sda1-hacker/humbert-agent/internal/rag"
	ragembedding "github.com/sda1-hacker/humbert-agent/internal/rag/embedding"
	ragindexer "github.com/sda1-hacker/humbert-agent/internal/rag/indexer"
	"github.com/sda1-hacker/humbert-agent/internal/rag/retrieval"
	"github.com/sda1-hacker/humbert-agent/internal/rag/retriever"
	"github.com/sda1-hacker/humbert-agent/internal/rag/search"
)

func main() { example.Run(query) }

// query：固定问题 → 真实查询向量 + PG BM25 → RRF → 同版本父块 → 返回原文证据。
func query(ctx context.Context) error {
	cfg := example.Config()
	pool, err := example.OpenPostgres(ctx, cfg, false)
	if err != nil {
		return err
	}
	defer pool.Close()
	embedder, err := ragembedding.New(ctx, cfg.Embedding)
	if err != nil {
		return err
	}
	vectorCfg := cfg.Vector
	vectorCfg.Embedder, vectorCfg.Dimensions = embedder, cfg.Embedding.Dimensions
	vector, err := retriever.NewVectorRetriever(pool, vectorCfg)
	if err != nil {
		return err
	}
	keyword, err := retriever.NewBM25Retriever(pool, cfg.Keyword)
	if err != nil {
		return err
	}
	hybrid, err := retriever.NewHybrid(vector, keyword, cfg.Hybrid)
	if err != nil {
		return err
	}
	store, err := ragindexer.NewDocumentStore(pool, ragindexer.DocumentConfig{})
	if err != nil {
		return err
	}
	profile := ragembedding.NewProfile(cfg.Embedding, cfg.InputBuilder)
	reranker, err := example.NewReranker(cfg)
	if err != nil {
		return err
	}
	service, err := rag.New(ctx, rag.Dependencies{
		PublishedChunks: store, // 用权威分块和发布版本过滤召回结果。
		Config: rag.Config{
			Reranker:  reranker,
			Retriever: hybrid, VectorRetriever: vector, KeywordRetriever: keyword,
			RecallTopK: cfg.Hybrid.TopK, Search: cfg.Search,
			ParentLoader: func(collection string) (search.ParentLoader, error) {
				return retriever.NewParentLoader(pool, collection)
			},
			BeforeSearch: func(ctx context.Context, collection string) error {
				// 防止换了模型或输入规则后误查旧向量；维度一致仍需验证档案。
				return ragindexer.CheckCollectionProfile(ctx, pool, collection, profile.ID())
			},
		},
	})
	if err != nil {
		return err
	}
	defer service.Close()
	relevant, err := loadRelevant(ctx, pool, store, cfg.CollectionID, example.Question)
	if err != nil {
		// 标注过期或缺失只关闭评测，不阻止用户继续查看检索结果、重新校对答案范围。
		fmt.Printf("评测暂不可用：%v\n", err)
		relevant = nil
	}
	fmt.Printf("查询：%s\nK=%d，融合候选=%d，单路候选=%d，维度=%d，RRF 权重=%.2f/%.2f\n",
		example.Question, cfg.Search.FinalTopK, cfg.Hybrid.TopK, cfg.Hybrid.ChannelTopK,
		cfg.Embedding.Dimensions, cfg.Hybrid.RRF.VectorWeight, cfg.Hybrid.RRF.KeywordWeight)
	parent, child := cfg.Chunking.Splitter.ForParentChild(cfg.Chunking.ParentChunkSize, cfg.Chunking.ChildChunkSize)
	fmt.Printf("导入参数（修改后需重新 upload）：父子模式=%v，父/子块=%d/%d，子块重叠=%d\n",
		cfg.Chunking.ParentChild, parent.ChunkSize, child.ChunkSize, child.ChunkOverlap)
	labels := "未标注"
	if relevant != nil {
		labels = fmt.Sprintf("%d", len(relevant))
	}
	fmt.Printf("向量/BM25 阈值=%.2f/%.2f，相关子块=%s\n",
		cfg.Vector.ScoreThreshold, cfg.Keyword.ScoreThreshold, labels)
	fmt.Printf("切分策略=%s，父块扩展=%v，合并同一父块=%v\n",
		cfg.Chunking.Splitter.Strategy, cfg.Search.ExpandParents, cfg.Search.CollapseSameParent)
	fmt.Printf("精排开启=%v；未开启时保留召回排名。预算按 UTF-8 字节计数，分数不代表答案正确率。\n", !cfg.Rerank.Disabled)
	if relevant == nil {
		fmt.Println("当前问题没有有效标注：无法计算检索质量指标；请在 main.go 的 references 中校对答案范围。")
	}
	for _, mode := range []string{"semantic", "keyword", "hybrid"} {
		started := time.Now()
		result, err := service.Search(ctx, rag.SearchRequest{
			CollectionID: cfg.CollectionID, Query: example.Question, Mode: mode, Limit: cfg.Search.FinalTopK,
		})
		if err != nil {
			return fmt.Errorf("%s 查询失败：%w", mode, err)
		}
		fmt.Printf("\n%s：返回 %d 条，耗时 %d ms，上下文预算计数 %d，降级=%v\n",
			mode, len(result.Results), time.Since(started).Milliseconds(), result.ContextTokens, result.Retrieval.Degraded)
		fmt.Printf("精排状态：%s\n", result.Rerank.Outcome)
		if score := evaluate(result.Results, relevant, cfg.Search.FinalTopK); score != nil {
			fmt.Printf("P@%d=%.3f  R@%d=%.3f  RR@%d=%.3f  nDCG@%d=%.3f  命中相关块=%d/%d\n",
				cfg.Search.FinalTopK, score.Precision, cfg.Search.FinalTopK, score.Recall,
				cfg.Search.FinalTopK, score.RR, cfg.Search.FinalTopK, score.NDCG, score.Hits, len(relevant))
		} else if relevant != nil {
			fmt.Println("已启用父块合并，无法按独立子块排名评测；请关闭 CollapseSameParent。")
		}
		printHits(result.Results)
	}
	fmt.Println("\nP@K 不足 K 条按未命中计算；预算计数默认按 UTF-8 字节保守估算；单次耗时包含模型加载。")
	return nil
}

// printHits 分别展示命中证据和实际返回的上下文；同一父块在当前模式中只打印一次。
func printHits(results []retrieval.SearchResult) {
	printed := make(map[string]bool)
	for rank, hit := range results {
		fmt.Printf("\n  #%d %s [%d,%d) score=%.4f vector=%.4f BM25=%.4f\n",
			rank+1, hit.ChunkID, hit.StartRune, hit.EndRune, hit.Score, hit.VectorScore, hit.KeywordScore)
		fmt.Printf("  标题路径：%s\n  命中子块：\n%s\n", strings.ReplaceAll(hit.ContextHeader, "\n", " > "), hit.Content)
		if id := hit.EffectiveChunkID(); id != hit.ChunkID && !printed[id] {
			printed[id] = true
			fmt.Printf("  实际上下文：%s [%d,%d)，%d 字符\n%s\n", id,
				hit.ContextStartRune, hit.ContextEndRune, utf8.RuneCountInString(hit.EffectiveContent()), hit.EffectiveContent())
		}
	}
}

// reference 是人工标注的答案范围，坐标按归一化 Markdown 的 Unicode 字符计算。
// 使用原文范围而非分块 ID，重新切分后仍可找到相关块；正文变更后必须重新核对标注。
type reference struct {
	Source, ContentHash string
	Start, End          int
}

// 评测按待验证文档人工添加答案范围，默认不夹带任何特定文档的标注。
// 没有标注的问题只展示结果；解析规则改变后需重新核对 ContentHash 与字符范围。
var references = map[string][]reference{}

// loadRelevant 根据完整已发布分块建立相关块集合，不能只从本次命中结果反推召回率分母。
func loadRelevant(ctx context.Context, pool *pgxpool.Pool, store *ragindexer.DocumentStore, collection, question string) (map[string]bool, error) {
	labels := references[strings.Join(strings.Fields(question), "")]
	if len(labels) == 0 {
		return nil, nil
	}
	relevant := make(map[string]bool)
	for _, label := range labels {
		if label.Start < 0 || label.End <= label.Start || label.ContentHash == "" {
			return nil, fmt.Errorf("评测标注范围或正文摘要无效：%s", label.Source)
		}
		path, err := filepath.Abs(label.Source)
		if err != nil {
			return nil, err
		}
		var documentID, contentHash string
		err = pool.QueryRow(ctx, `SELECT source_key,content_hash FROM documents
WHERE collection_id=$1 AND source_uri=$2 AND revision>0 ORDER BY id LIMIT 1`, collection, path).Scan(&documentID, &contentHash)
		if err != nil {
			return nil, fmt.Errorf("读取评测文档失败，请先上传 %s：%w", path, err)
		}
		if contentHash != label.ContentHash {
			return nil, fmt.Errorf("评测文档正文已变化，请重新核对 references：%s", path)
		}
		chunks, err := store.ListChunks(ctx, collection, documentID)
		if err != nil {
			return nil, err
		}
		matched, end := false, 0
		for _, chunk := range chunks {
			end = max(end, chunk.EndRune)
			// 人工标记的答案范围与子块原文范围有交集，即视为相关块。
			if chunk.StartRune < label.End && chunk.EndRune > label.Start {
				relevant[chunk.ID], matched = true, true
			}
		}
		if !matched || label.End > end {
			return nil, fmt.Errorf("标注范围没有完整落在已发布正文中，请核对 references：%s", path)
		}
	}
	return relevant, nil
}

// metrics 使用二元相关性标注；它衡量检索结果，不代表回答模型的正确率或忠实度。
type metrics struct {
	Hits                        int
	Precision, Recall, RR, NDCG float64
}

// evaluate 评估最终返回的前 K 个独立子块；父块补充的其他正文不自动算作召回命中。
// P@K 分母固定为 K，不足 K 条按未命中计算；单问题的 RR 在多问题取均值后才叫 MRR。
func evaluate(results []retrieval.SearchResult, relevant map[string]bool, k int) *metrics {
	if len(relevant) == 0 || k <= 0 {
		return nil
	}
	score := &metrics{}
	seen := make(map[string]bool)
	rank, dcg := 0, 0.0
	for _, result := range results {
		if len(result.Evidence) > 1 {
			return nil // 合并后无法直接恢复独立子块排名，本例要求关闭 CollapseSameParent。
		}
		if seen[result.ChunkID] {
			continue
		}
		seen[result.ChunkID] = true
		rank++
		if relevant[result.ChunkID] {
			score.Hits++
			dcg += 1 / math.Log2(float64(rank+1))
			if score.RR == 0 {
				score.RR = 1 / float64(rank)
			}
		}
		if rank == k {
			break
		}
	}
	ideal := 0.0
	for rank := 1; rank <= min(k, len(relevant)); rank++ {
		ideal += 1 / math.Log2(float64(rank+1))
	}
	score.Precision = float64(score.Hits) / float64(k)
	score.Recall = float64(score.Hits) / float64(len(relevant))
	score.NDCG = dcg / ideal
	return score
}
