// 本文件是一个使用真实文档的完整 RAG 示例。
// 学习顺序：main 看流程 → exampleConfig 改参数 → newPostgresRAG 看组件组装
// → evidenceIDs / evaluate 理解召回率与检索精确率。
//
// 在项目根目录准备环境变量：
//
//	RAG_DATABASE_URL：已创建的 PostgreSQL 数据库连接地址。
//	OPENAI_API_KEY：向量模型服务密钥。
//	OPEN_BASE_URL：OpenAI 兼容 API 的基础地址，不是完整 embeddings 地址。
//	RAG_EMBEDDING_MODEL：服务实际支持且输出 1024 维的模型名。
//
// 数据库需安装 vector 和 pg_search 扩展文件；代码要求 PostgreSQL 15+、
// vector 0.7.0+、pg_search 0.25.0+，示例可以创建扩展、表和索引。
//
// 运行整个目录，或者直接运行这个文件，效果相同：
//
//	go run ./examples/5-rag /绝对路径/你的文档.pdf
//	go run ./examples/5-rag/main.go /绝对路径/你的文档.pdf
//
// 程序展示真实子块后，先输入问题和全部相关子块编号，再执行检索评测。
// 当前只评测检索，不调用 LLM 生成答案。MMR 未接入 Service.Search，
// Query Rewrite 尚未实现，修改本文件的参数不会凭空启用这两项能力。
package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/cloudwego/eino/components/document"
	"github.com/sda1-hacker/humbert-agent/internal/documentparse"
	"github.com/sda1-hacker/humbert-agent/internal/rag"
	"github.com/sda1-hacker/humbert-agent/internal/rag/chunker"
	"github.com/sda1-hacker/humbert-agent/internal/rag/embeddinginput"
	indexerpostgres "github.com/sda1-hacker/humbert-agent/internal/rag/indexer/postgres"
	tabulaloader "github.com/sda1-hacker/humbert-agent/internal/rag/loader/tabula"
	embeddingopenai "github.com/sda1-hacker/humbert-agent/internal/rag/provider/embedding/openai"
	rerankhttp "github.com/sda1-hacker/humbert-agent/internal/rag/provider/rerank/http"
	"github.com/sda1-hacker/humbert-agent/internal/rag/rerank"
	"github.com/sda1-hacker/humbert-agent/internal/rag/retrieval"
	"github.com/sda1-hacker/humbert-agent/internal/rag/retriever"
	retrieverpostgres "github.com/sda1-hacker/humbert-agent/internal/rag/retriever/postgres"
	"github.com/sda1-hacker/humbert-agent/internal/rag/search"
	"github.com/sda1-hacker/humbert-agent/internal/rag/searchcontent"
)

// 一、主流程：真实文件导入 → 人工标注 → 检索 → 指标。

// main 按执行顺序展示整个实验，不需要切换文件才能理解流程。
// 本示例只处理一个真实文件和一个问题，保留简单的命令行交互。
// panic 用于让学习阶段的配置/输入错误立即可见；它不是产品 API 的错误协议。
func main() {
	if len(os.Args) != 2 || os.Args[1] == "-h" || os.Args[1] == "--help" {
		fmt.Println("用法：go run ./examples/5-rag /绝对路径/你的文档.pdf")
		fmt.Println("配置入口：main.go 中的 exampleConfig；详细说明：examples/5-rag/README.md")
		return
	}

	// 第一步：从命令行读取真实文件路径，并生成组件配置、导入请求、实验模式。
	// Background 作为根 context；解析、模型和查询各自仍有配置的超时。
	ctx := context.Background()
	cfg, ingest, modes := exampleConfig(os.Args[1])
	printSettings(cfg, ingest, modes)

	// 第二步：创建 RAG Service。newPostgresRAG 就在本文件中，负责组装
	// Eino Loader、Indexer、Retriever、共享向量模型和可选精排模型。
	engine, err := newPostgresRAG(ctx, cfg)
	if err != nil {
		panic(err)
	}
	// Close 在退出时释放连接池；初始化中途失败的资源由组装函数清理。
	defer engine.Close()

	// 第三步：调用 Ingest，内部执行：预留版本 → 文件解析 → 换行规范化
	// → 切分 → 构造检索文本 → 子块向量化 → 原子保存正文与两路索引。
	// 父块只保存上下文，不直接参与向量或 BM25 召回。
	ingested, err := engine.Ingest(ctx, ingest)
	if err != nil {
		panic(err)
	}
	fmt.Printf("\n导入完成：父块 %d 个，可检索子块 %d 个。\n", ingested.ParentCount, ingested.ChildCount)
	// 配置的 Strategy 表示尝试意图，Diagnostics 才解释本次选择及回退原因。
	for _, doc := range ingested.Documents {
		if diag := doc.Diagnostics; diag != nil {
			fmt.Printf("分块实际策略：%s，候选策略链：%v\n", diag.SelectedTier, diag.TierChain)
			for _, rejected := range diag.Rejected {
				fmt.Printf("  策略 %s 的校验结果：%s\n", rejected.Tier, rejected.Reason)
			}
		}
	}
	if ingest.ParentChild {
		fmt.Println("以上诊断描述父块切分；每个父块内的子块会独立选择策略。")
	}
	if cfg.Search.ExpandParents && ingested.ParentCount == 0 {
		fmt.Println("本次没有保存独立父块，查询会直接使用子块正文。")
	}

	// 第四步：读取已发布的真实子块。人工标注的单位必须与检索命中单位一致，
	// 所以 ListChunks 不把只用于上下文的父块混入集合。
	chunks, err := engine.ListChunks(ctx, ingest.CollectionID, ingest.DocumentID)
	if err != nil {
		panic(err)
	}
	// 解析器把实际 OCR 使用情况和警告保存为来源元数据，可从子块读取。
	if len(chunks) > 0 {
		metadata := chunks[0].Metadata
		fmt.Printf("解析元数据：解析器=%v，报告使用 OCR=%v\n", metadata[tabulaloader.MetaParser], metadata[tabulaloader.MetaOCRUsed])
		if warnings := metadata[tabulaloader.MetaParserWarnings]; warnings != nil {
			fmt.Printf("解析警告：%v\n", warnings)
		}
	}
	// ChunkID 包含本次版本，终端编号从 1 开始；建立映射便于结果展示。
	// StartRune/EndRune 是解析并规范化后的 Markdown 字符范围，不是 PDF 页码或字节。
	chunkNumbers := make(map[string]int, len(chunks))
	for i, chunk := range chunks {
		chunkNumbers[chunk.ID] = i + 1
		fmt.Printf("\n【分块 %d】原文字符区间 [%d, %d)，父块 ID：%s\n", i+1, chunk.StartRune, chunk.EndRune, chunk.ParentChunkID)
		fmt.Printf("标题路径：%s\n%s\n", chunk.ContextHeader, chunk.Content)
	}

	// 第五步：先标注、后检索，避免根据检索结果反过来选择正确答案。
	// 应标注所有包含相关信息的子块，重叠块也可能同时相关。
	scanner := bufio.NewScanner(os.Stdin)
	readLine := func(prompt string) string {
		fmt.Print(prompt)
		if !scanner.Scan() {
			panic("没有读取到输入")
		}
		return strings.TrimSpace(scanner.Text())
	}
	query := readLine("\n请输入针对该文档的问题：")
	labels := readLine("请输入所有相关子块编号，用空格分隔：")
	// gold 是人工标准答案集合：键为真实 ChunkID，值统一为 true。
	// 这里不能用父块 ID，因为评测针对实际命中的子块证据。
	gold := make(map[string]bool)
	for _, label := range strings.Fields(labels) {
		n, err := strconv.Atoi(label)
		if err != nil || n < 1 || n > len(chunks) {
			panic("分块编号无效：" + label)
		}
		gold[chunks[n-1].ID] = true // 重复编号只算一个相关子块。
	}
	if len(gold) == 0 {
		panic("请至少标注一个相关子块")
	}

	// 第六步：同一份文档、问题和标注对比三种召回。
	// hybrid：向量 + BM25 + RRF；semantic：向量；keyword：BM25。
	// 精排和父上下文配置保持一致，比较时避免同时修改多个因素。
	var summaries []string
	for _, mode := range modes {
		started := time.Now()
		response, err := engine.Search(ctx, rag.SearchRequest{
			CollectionID: ingest.CollectionID,
			Query:        query,
			Mode:         mode,
			Limit:        0, // 0 使用 cfg.Search.FinalTopK；正数覆盖最终上下文条数。
		})
		if err != nil {
			// 单个实验失败时继续其他模式，但不把失败冒充成零召回率。
			fmt.Printf("\n【%s】检索失败：%v\n", mode, err)
			summaries = append(summaries, fmt.Sprintf("%-8s 检索失败，未计算指标", mode))
			continue
		}
		// 耗时只覆盖 Search 调用，不包含入库、人工输入和打印。
		elapsed := time.Since(started)
		printSearch(mode, response, chunkNumbers, gold)
		// 最终上下文可能合并多个子块，evaluate 会提取所有 Evidence 并去重。
		metrics := evaluate(response.Results, gold)
		summaries = append(summaries, fmt.Sprintf(
			"%-8s 上下文=%d  子块证据=%d  相关命中=%d/%d  Recall=%.2f%%  Precision=%.2f%%  耗时=%s",
			mode, len(response.Results), metrics.Retrieved, metrics.Hits, len(gold),
			metrics.Recall*100, metrics.Precision*100, elapsed.Round(time.Millisecond),
		))
	}
	fmt.Println("\n同一问题的对比结果（按去重后的命中子块证据计算）：")
	for _, summary := range summaries {
		fmt.Println(summary)
	}
	fmt.Println("父块里的其他正文不自动算命中；Precision 衡量检索相关性，不是 LLM 回答正确率。")
}

// 二、调参入口：只修改这里就能比较配置效果。

// exampleConfig 集中展示配置入口；调参时修改这里，不需要修改组装流程。
// 返回值依次是组件配置、文档导入参数、要对比的检索模式。
// 解析或分块配置改变后要重新导入、检查并标注；查询参数改变不要求重建索引。
// 为保持例子简单，本程序每次启动仍会重新导入这个文件。
func exampleConfig(filePath string) (postgresConfig, rag.IngestRequest, []string) {
	cfg := defaultPostgresConfig()

	// 一、数据库和文档解析。数据库及扩展的安装文件需要提前准备好。
	cfg.DatabaseURL = envOr("RAG_DATABASE_URL", "postgres://postgres:postgres@127.0.0.1:5432/rag_example?sslmode=disable")
	cfg.EnsureSchema = true // 自动创建扩展和表，不会自动创建数据库。
	cfg.Loader.ParseOptions = documentparse.DefaultOptions()
	cfg.Loader.ParseOptions.Timeout = time.Minute
	cfg.Loader.ParseOptions.MaxInputBytes = 32 << 20     // 原文件最大 32 MiB。
	cfg.Loader.ParseOptions.MaxOutputBytes = 32 << 20    // 解析后的正文最大 32 MiB。
	cfg.Loader.ParseOptions.MaxExpandedBytes = 256 << 20 // 压缩文档展开后的总大小上限。
	cfg.Loader.ParseOptions.MaxArchiveEntries = 8192     // 压缩文档内部文件数量上限。
	cfg.Loader.ParseOptions.IncludePageNumbers = false   // 是否在解析结果中加入页码标记。
	cfg.Loader.ExcludeHeadersAndFooters = true           // 过滤重复页眉页脚。
	cfg.Loader.NormalizeLineEndings = true               // 将解析后的换行统一为 LF。
	cfg.Loader.OCRLanguage = ""                          // 普通构建没有 OCR；填语言不会自动开启。
	// 扫描文档需要安装 Tesseract 及语言包，并使用 go run -tags ocr。
	// 此时可以填 "chi_sim+eng"；解析器按文档情况决定是否使用 OCR。

	// 二、向量模型。沿用原示例模型名，需与你的服务实际支持的模型一致。
	cfg.Embedding.APIKey = os.Getenv("OPENAI_API_KEY")
	cfg.Embedding.BaseURL = os.Getenv("OPEN_BASE_URL") // API 基础地址，不是完整 embeddings 地址。
	cfg.Embedding.Model = envOr("RAG_EMBEDDING_MODEL", "qwen3.7-text-embedding")
	cfg.Embedding.ModelRevision = "" // 同名模型换了权重时填写新版本，并使用新知识库。
	cfg.Embedding.Dimensions = 0     // 不发送 dimensions 参数，实际输出仍必须是 1024 维。
	cfg.Embedding.Timeout = 30 * time.Second
	cfg.Embedding.InputBudget = embeddinginput.Budget{
		MaxInputTokens: 8192,  // 单条完整输入上限，包含标题、标题路径和正文。
		MaxBatchTokens: 65536, // 每批输入总量上限，需按实际模型调整。
		CountTokens:    nil,   // 默认按 UTF-8 字节数保守估算，可替换成模型分词器。
	}
	cfg.Indexer.EmbeddingBatchSize = 32 // 每批最多多少个分块，还会受总输入预算限制。
	// 统一输入预算只配置 cfg.Embedding.InputBudget，不配置 cfg.Indexer.InputBudget。
	// 默认检索文本为“文档标题 + 标题路径 + 正文”；修改规则应使用新知识库并重导入。
	cfg.InputBuilder.TitleKeys = []string{"title", "_title", "file_name"}
	cfg.InputBuilder.ContextHeaderKey = retrieval.MetaContextHeader

	// 三、分块。这里直接使用 RAG 的原生导入参数，不增加新的配置层。
	ingest := rag.IngestRequest{
		CollectionID:    "rag-file-example",  // 换模型或检索文本规则时改成新的知识库 ID。
		DocumentID:      "uploaded-document", // 重跑会替换这个 ID 对应的演示文档。
		Source:          document.Source{URI: filePath},
		Splitter:        chunker.DefaultConfig(),
		ParentChild:     true, // true：先切父块，再切子块；false：直接切普通检索块。
		ParentChunkSize: 2048, // 父块保存较完整上下文，不参与向量或 BM25 召回。
		ChildChunkSize:  384,  // 子块参与向量化和召回，也是人工标注的单位。
	}
	// 可选策略：auto、heading、heuristic、recursive、legacy。
	// auto 自动判断结构；heading 按 Markdown 标题；heuristic 识别章节标记。
	// heading/heuristic 校验不通过时退回递归切分，recursive 与 legacy 是同一算法。
	ingest.Splitter.Strategy = chunker.StrategyAuto
	ingest.Splitter.ChunkSize = 512   // 普通模式的目标字符数；父子模式改用上面的两个大小。
	ingest.Splitter.ChunkOverlap = 80 // 普通块/父块的重叠参数；0 关闭全部重叠。
	// 父子模式的子块重叠由组件设为 ChildChunkSize/5，不直接使用这里的 80。
	// 实际重叠按算法边界决定；递归可能无重叠，启发式对齐可能扩展到两倍参数。
	ingest.Splitter.Separators = []string{"\n\n", "\n", "。"} // 递归切分优先级。
	ingest.Splitter.TokenLimit = 0                           // 0 不加近似 token 目标；正数只约束普通块/子块。
	ingest.Splitter.Languages = []string{"zh"}               // 语言提示；留空时启发式切分尝试全部章节规则。
	// ChunkSize 按 Unicode 字符计数，遇到表格/代码等受保护内容可能超过目标。
	// TokenLimit 是切分目标，Embedding.InputBudget 才是完整模型输入的检查上限。

	// 四、召回和融合。此示例将 Hybrid.TopK 同时传给 Service 的 RecallTopK。
	cfg.Hybrid.TopK = 30        // 融合候选数；semantic/keyword 单路候选数也使用这个值。
	cfg.Hybrid.ChannelTopK = 50 // 混合模式每路候选数，实际至少覆盖 Hybrid.TopK。
	// Service.Search 会传入 WithTopK，因此 cfg.Vector.TopK/Keyword.TopK 会被覆盖。
	cfg.Vector.ScoreThreshold = 0  // 向量原始相似度阈值，先放宽便于观察。
	cfg.Keyword.ScoreThreshold = 0 // BM25 原始分数阈值，与向量分数不是同一量纲。
	cfg.Hybrid.RRF.K = 60          // RRF 平滑常数，越大越弱化名次差异。
	cfg.Hybrid.RRF.VectorWeight = 0.7
	cfg.Hybrid.RRF.KeywordWeight = 0.3
	// 某一路权重设为 0 会关闭混合模式的该通道。
	// 只有整个 RRFConfig{} 都为零才恢复默认；保留 K=60 而把两路权重
	// 都设为 0 会校验失败。两路不能同时关闭。
	// 权重不会关闭独立的 semantic/keyword 模式，也不会取消导入时的向量化。
	cfg.Hybrid.FailurePolicy = retriever.FailureStrict // 任一路错误即失败。
	// 改为 retriever.FailureAllowPartial 时，普通单路错误可退回成功的另一条通道。
	cfg.Hybrid.Timeout = 20 * time.Second        // 整个混合召回超时。
	cfg.Hybrid.ChannelTimeout = 15 * time.Second // 每路召回超时；0 表示共用整体超时。

	// 五、精排。默认关闭；开启时必须提供真实的精排服务地址和模型。
	enableRerank := false
	cfg.Rerank.Disabled = !enableRerank
	if enableRerank {
		provider := rerankhttp.DefaultConfig()
		provider.Endpoint = os.Getenv("RERANK_ENDPOINT") // 完整请求地址，如服务的 /v1/rerank。
		provider.APIKey = os.Getenv("RERANK_API_KEY")
		provider.Model = os.Getenv("RERANK_MODEL")
		provider.Timeout = 15 * time.Second
		cfg.RerankProvider = &provider
	}
	cfg.Rerank.MaxCandidates = 30 // 最多送多少个召回候选给精排模型。
	cfg.Rerank.Threshold = 0.3    // 按模型相关性分数过滤，不是 RRF 分数。
	cfg.Rerank.DegradeFactor = 0.7
	cfg.Rerank.DegradeFloor = 0.15    // 无结果时阈值降为 max(Threshold*Factor, Floor)。
	cfg.Rerank.FallbackMinScore = 0.1 // 降级后仍无结果，最高模型分达到此值时保留一条。
	cfg.Rerank.ModelWeight = 0.8      // 最终排序中模型分的权重。
	cfg.Rerank.BaseWeight = 0.2       // 最终排序中召回分的权重。
	cfg.Rerank.SourceWeight = 0       // 本示例没有来源可信度标注，不给额外加分。
	// 三个权重的和不能超过 1。普通模型错误会回退，取消/超时会返回错误。
	// cfg.Rerank.TopK、MMRLambda 只作用于独立 Engine.Rerank；Service.Search 不执行 MMR。
	// Query Rewrite 尚未实现，没有能够开启它的配置项。

	// 六、最终上下文。这些开关属于查询阶段，不会改变已保存的分块。
	cfg.Search.FinalTopK = 5               // 最多返回多少条上下文；请求的 Limit > 0 时可覆盖。
	cfg.Search.Timeout = 30 * time.Second  // 整个查询超时，覆盖召回、精排和父块读取。
	cfg.Search.ExpandParents = true        // 命中有父块的子块后，读取父块作为上下文。
	cfg.Search.CollapseSameParent = true   // 相同父块合并为一条，仍保留所有子块证据。
	cfg.Search.AllowParentFallback = false // 父块读取错误时是否退回子块；取消/超时不回退。
	// ExpandParents 需要导入时确实保存了父块；短文档可能不需要单独保存父块。

	// 七、同一个真实文档、问题和人工标注，对比三种召回方式。
	// 只想跑一种时保留一个值，例如 []string{"hybrid"}。
	modes := []string{"hybrid", "semantic", "keyword"}
	return cfg, ingest, modes
}

// 三、示例配置与组件组装：数据库、模型、Eino 组件。

// 数据库配置只属于此示例，不进入 internal/rag 的业务 API。
var (
	ErrMissingDatabaseURL         = errors.New("rag: missing database url")
	ErrEmbeddingDimensionMismatch = errors.New("rag: embedding dimension does not match postgres schema")
)

// postgresConfig 将本示例用到的 PostgreSQL、解析器和模型配置集中在一起。
type postgresConfig struct {
	// DatabaseURL 指向提前创建的实验数据库。
	DatabaseURL string

	// 本地开发可自动创建扩展与表，数据库需提前创建。
	EnsureSchema bool

	// Loader、Embedding、Indexer 分别配置文件解析、向量模型和索引写入。
	Loader    tabulaloader.Config
	Embedding embeddingopenai.Config
	Indexer   indexerpostgres.Config

	// 三种召回共享知识库范围；InputBuilder 统一构造模型与索引文本。
	Hybrid       retriever.HybridConfig
	Vector       retrieverpostgres.VectorConfig
	Keyword      retrieverpostgres.BM25Config
	InputBuilder searchcontent.Builder

	// 不配置 Provider 时关闭远程精排，保留召回与融合。
	RerankProvider *rerankhttp.Config

	Rerank rerank.Config // 模型精排、阈值及评分融合。
	Search search.Config // 最终上下文数量、超时和父块扩展。
}

// defaultPostgresConfig 返回示例所需的数据库、模型及检索默认参数。
func defaultPostgresConfig() postgresConfig {
	embeddingCfg := embeddingopenai.DefaultConfig()

	// 内置 schema 使用 halfvec(1024)。
	embeddingCfg.Dimensions = indexerpostgres.EmbeddingDimensions

	return postgresConfig{
		EnsureSchema: false,

		Loader: tabulaloader.DefaultConfig(),

		Embedding: embeddingCfg,

		Indexer: indexerpostgres.DefaultConfig(),

		Hybrid:       retriever.DefaultHybridConfig(),
		Vector:       retrieverpostgres.DefaultVectorConfig(),
		Keyword:      retrieverpostgres.DefaultBM25Config(),
		InputBuilder: searchcontent.DefaultBuilder(),

		Rerank: rerank.DefaultConfig(),

		Search: search.DefaultConfig(),
	}
}

// Validate 先检查本例参数之间的关系，再由底层组件验证各自配置。
// 这些检查在解析和远程模型调用前执行，便于定位错误、避免无效调用。
func (c postgresConfig) Validate() error {
	if err := c.Loader.ParseOptions.Validate(); err != nil {
		return err
	}
	if err := c.Indexer.InputBudget.Validate(); err != nil {
		return err
	}
	// 预算只配在 Embedding 上，组装时同步给索引器，确保导入与 query 共用规则。
	if c.Indexer.InputBudget.MaxInputTokens != 0 || c.Indexer.InputBudget.MaxBatchTokens != 0 || c.Indexer.InputBudget.CountTokens != nil {
		return fmt.Errorf("rag: configure Embedding.InputBudget for both index and query; Indexer.InputBudget is for standalone indexers")
	}
	if c.Indexer.EmbeddingBatchSize < 0 {
		return fmt.Errorf("rag: negative embedding batch size")
	}
	if err := c.Vector.Validate(); err != nil {
		return err
	}
	if err := c.Keyword.Validate(); err != nil {
		return err
	}
	// 通用 Hybrid 没有 PG 的 200 条限制；本例使用 PG，组装层要额外检查。
	if c.Hybrid.TopK > retrieverpostgres.MaxTopK || c.Hybrid.ChannelTopK > retrieverpostgres.MaxTopK {
		return retrieverpostgres.ErrInvalidTopK
	}
	if err := c.Hybrid.Validate(); err != nil {
		return err
	}
	if err := c.Rerank.Validate(); err != nil {
		return err
	}
	if err := c.Search.Validate(); err != nil {
		return err
	}
	if c.Search.FinalTopK > retrieverpostgres.MaxTopK {
		return fmt.Errorf("rag: final top k exceeds maximum recall size %d", retrieverpostgres.MaxTopK)
	}
	// 候选池必须足以覆盖最终上下文需求；精排启用时也不能过早缩小候选池。
	if c.Hybrid.TopK > 0 && c.Search.FinalTopK > c.Hybrid.TopK {
		return fmt.Errorf("rag: recall pool must be at least final top k")
	}
	if c.Rerank.MaxCandidates > 0 && c.RerankProvider != nil && c.Rerank.MaxCandidates < c.Search.FinalTopK {
		return fmt.Errorf("rag: rerank candidate pool must be at least final top k")
	}

	if strings.TrimSpace(c.DatabaseURL) == "" {
		return ErrMissingDatabaseURL
	}

	if err := c.Embedding.Validate(); err != nil {
		return fmt.Errorf("rag embedding config: %w", err)
	}

	// 省略请求维度时，仍校验实际模型输出与 schema 一致。
	if c.Embedding.Dimensions > 0 &&
		c.Embedding.Dimensions != indexerpostgres.EmbeddingDimensions {

		return fmt.Errorf(
			"%w: postgres=%d embedding=%d",
			ErrEmbeddingDimensionMismatch,
			indexerpostgres.EmbeddingDimensions,
			c.Embedding.Dimensions,
		)
	}

	if c.RerankProvider != nil {
		if err := c.RerankProvider.Validate(); err != nil {
			return fmt.Errorf("rag rerank config: %w", err)
		}
	}

	return nil
}

// newPostgresRAG 是组件组装入口，不负责处理具体文件或具体问题。
// 组装关系：Tabula Loader → PG Indexer；PG 向量/BM25 → Hybrid → 精排与上下文。
// 创建好的组件实现 Eino 原生接口，替换后端时在这里替换相应实现。
func newPostgresRAG(ctx context.Context, cfg postgresConfig) (*rag.Service, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// 初始化数据库扩展、连接池和表结构。
	if cfg.EnsureSchema {
		if err := indexerpostgres.EnsureExtensions(ctx, cfg.DatabaseURL); err != nil {
			return nil, fmt.Errorf("bootstrap postgres extensions: %w", err)
		}
	}

	pool, err := indexerpostgres.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		return nil, err
	}

	// 创建池后，任何后续步骤失败都要关闭池，防止初始化失败造成资源泄漏。
	// 成功返回后，Close 的责任转交给 Service。
	success := false

	defer func() {
		if !success {
			pool.Close()
		}
	}()

	if cfg.EnsureSchema {
		if err := indexerpostgres.EnsureSchema(ctx, pool); err != nil {
			return nil, fmt.Errorf("ensure rag postgres schema: %w", err)
		}
	}

	if err := indexerpostgres.CheckSchema(ctx, pool); err != nil {
		return nil, err
	}
	// 向量模型只创建一次，文档向量和 query 向量共享同一空间。
	// 即使只比较 keyword，本例导入仍建立两路索引，因此也需要向量模型。
	embedder, err := embeddingopenai.New(ctx, cfg.Embedding)
	if err != nil {
		return nil, fmt.Errorf("create embedding provider: %w", err)
	}

	// 索引器固定使用同一个模型及输入预算，并保存知识库模型档案。
	indexerCfg := cfg.Indexer
	indexerCfg.Embedder = embedder
	indexerCfg.InputBudget = cfg.Embedding.InputBudget
	profile := cfg.EmbeddingProfile()
	indexerCfg.ProfileID = profile.ID()
	indexerCfg.ProfileJSON = profile.JSON()
	// 要求每次导入先预留版本；较早任务晚完成时不能覆盖较新的文档。
	indexerCfg.VersionedDocuments = true

	pgIndexer, err := indexerpostgres.NewIndexer(pool, indexerCfg)
	if err != nil {
		return nil, fmt.Errorf("create postgres indexer: %w", err)
	}

	// 配置精排模型；没有服务配置时显式关闭。
	rerankCfg := cfg.Rerank

	var scorer rerank.Scorer

	if cfg.RerankProvider != nil {
		httpScorer, err := rerankhttp.NewClient(*cfg.RerankProvider)
		if err != nil {
			return nil, fmt.Errorf("create rerank provider: %w", err)
		}

		scorer = httpScorer
	} else {
		// 诊断显示 disabled，明确区分未启用与模型调用失败。
		rerankCfg.Disabled = true
	}

	// Engine 处理模型阈值、组合分和错误回退；Service 通过 RerankCandidates
	// 保留候选，最终条数交给 Search，而不是在这里先截断或执行 MMR。
	rerankEngine := rerank.NewEngine(scorer, rerankCfg)

	// 组装向量、BM25 和混合检索器，向量召回复用索引使用的模型。
	vectorCfg := cfg.Vector
	vectorCfg.Embedder = embedder
	vector, err := retrieverpostgres.NewVectorRetriever(pool, vectorCfg)
	if err != nil {
		return nil, err
	}
	keyword, err := retrieverpostgres.NewBM25Retriever(pool, cfg.Keyword)
	if err != nil {
		return nil, err
	}
	hybrid, err := retriever.NewHybrid(vector, keyword, cfg.Hybrid)
	if err != nil {
		return nil, err
	}
	// 最后把组件和文档管理能力注入唯一的业务入口 rag.Service。
	runtime, err := rag.New(ctx, rag.Dependencies{
		Loader:  tabulaloader.NewLoader(cfg.Loader), // 真实文件解析为完整 Markdown。
		Indexer: pgIndexer,                          // 子块向量化，并原子保存完整文档及索引。
		Config: rag.Config{
			Retriever:        hybrid,           // 空 Mode/hybrid 使用的默认检索器。
			VectorRetriever:  vector,           // semantic 模式使用。
			KeywordRetriever: keyword,          // keyword 模式使用。
			RecallTopK:       cfg.Hybrid.TopK,  // 三种模式进入后处理前的候选数量。
			Search:           cfg.Search,       // 最终条数、父块扩展和查询超时。
			Reranker:         rerankEngine,     // 远程精排可关闭，但诊断仍明确保留。
			InputBuilder:     cfg.InputBuilder, // 向量和 BM25 共用的检索文本规则。
			ParentLoader: func(collection string) (search.ParentLoader, error) {
				// 按当前请求 collection 读取父块，避免跨知识库补上下文。
				return retrieverpostgres.NewParentLoader(pool, collection)
			},
			BeforeSearch: func(ctx context.Context, collection string) error {
				// 查询前验证模型档案；同维度的不同模型也不能混用已有索引。
				return indexerpostgres.EnsureCollectionProfile(ctx, pool, collection, profile.ID(), profile.JSON())
			},
		},
		ChunkReader: pgIndexer,  // ListChunks 展示当前已发布子块，供人工标注。
		Lifecycle:   pgIndexer,  // 预留版本、取消发布和删除文档。
		Close:       pool.Close, // 成功组装后，由 Service.Close 释放数据库池。
		// PG 把正文、父子块和两路索引放在同一事务内发布，检索 SQL 只读当前块，
		// 因此本例无需独立 Publisher 和 PublishedChunks 二次过滤。
	})
	if err != nil {
		return nil, err
	}
	success = true
	return runtime, nil
}

// EmbeddingProfile 描述 collection 的向量空间身份，作为稳定 JSON 计算 SHA-256。
// 模型、实际维度、输入规则改变会产生新档案；密钥和请求超时不进入档案。
// 同名模型换权重时要修改 ModelRevision 并使用新 collection 重新导入。
func (c postgresConfig) EmbeddingProfile() embeddinginput.Profile {
	endpoint := strings.TrimRight(strings.TrimSpace(c.Embedding.BaseURL), "/")
	if endpoint == "" {
		endpoint = "https://api.openai.com/v1"
	}
	builder := c.InputBuilder
	if len(builder.TitleKeys) == 0 && builder.ContextHeaderKey == "" {
		builder = searchcontent.DefaultBuilder()
	}
	return embeddinginput.Profile{
		Provider:      "openai-compatible",
		Endpoint:      endpoint,
		Model:         strings.TrimSpace(c.Embedding.Model),
		Revision:      c.Embedding.ModelRevision,
		Dimensions:    indexerpostgres.EmbeddingDimensions, // 记录实际数据库维度，不是请求参数 0。
		InputVersion:  "humbert-search-content-v1",         // 标识检索文本构造版本。
		SearchBuilder: builder,
	}
}

// 四、结果展示与评测：保留真实命中证据，按集合计算指标。

// evaluation 按最终结果中的子块证据计算指标，避免父块合并后漏算或重复计算。
type evaluation struct {
	Retrieved int     // 去重后的实际命中子块数。
	Hits      int     // 命中子块中属于人工标注集合的数量。
	Recall    float64 // 召回率：相关命中数 / 全部人工相关子块数。
	Precision float64 // 检索精确率：相关命中数 / 实际返回的去重子块证据数。
}

// evidenceIDs 收集主命中和同父块合并保留的全部子块 ID，按出现顺序去重。
// ContextChunkID 是上下文身份，可能是父块，不能拿它与子块标注比较。
// 一条父块上下文可能保留多个子块证据，重复出现在主命中和 Evidence 中也只算一次。
func evidenceIDs(results []retrieval.SearchResult) []string {
	var ids []string
	seen := make(map[string]bool)
	for _, result := range results {
		// 未分组或只有一条结果时 Evidence 可能为空，所以主命中始终加入。
		candidates := []string{result.ChunkID}
		for _, evidence := range result.Evidence {
			candidates = append(candidates, evidence.ChunkID)
		}
		for _, id := range candidates {
			if id != "" && !seen[id] {
				seen[id] = true
				ids = append(ids, id)
			}
		}
	}
	return ids
}

// evaluate 中的“准确率”使用检索 Precision；分母是实际子块证据数。
// 设 G 为全部相关子块，R 为实际命中证据：Recall=|R∩G|/|G|，Precision=|R∩G|/|R|。
// 例如相关块有 4 个，返回证据有 5 个且相关 3 个，则 Recall=75%、Precision=60%。
// 本例不生成答案，因此这些数字不能解释为 LLM 回答正确率。
func evaluate(results []retrieval.SearchResult, gold map[string]bool) evaluation {
	ids := evidenceIDs(results)
	m := evaluation{Retrieved: len(ids)}
	for _, id := range ids {
		if gold[id] {
			m.Hits++
		}
	}
	// 防止除以零；没有返回证据时指标保持零，查询失败则在 main 中另行报告。
	if len(gold) > 0 {
		m.Recall = float64(m.Hits) / float64(len(gold))
	}
	if m.Retrieved > 0 {
		m.Precision = float64(m.Hits) / float64(m.Retrieved)
	}
	return m
}

// printSettings 打印配置意图，例如启用了哪种分块和父上下文。
// 实际算法可能回退、模型可能失败、短文档可能省略父块，要继续看本次诊断。
func printSettings(cfg postgresConfig, ingest rag.IngestRequest, modes []string) {
	fmt.Printf("配置：知识库=%s，文档=%s，检索模式=%v\n", ingest.CollectionID, ingest.DocumentID, modes)
	fmt.Printf("解析：过滤页眉页脚=%t，OCR 语言=%q，超时=%s\n", cfg.Loader.ExcludeHeadersAndFooters, cfg.Loader.OCRLanguage, cfg.Loader.ParseOptions.Timeout)
	fmt.Printf("分块：策略=%s，普通块大小=%d，重叠=%d，近似 token 目标=%d\n", ingest.Splitter.Strategy, ingest.Splitter.ChunkSize, ingest.Splitter.ChunkOverlap, ingest.Splitter.TokenLimit)
	fmt.Printf("父子分块=%t，父块大小=%d，子块大小=%d\n", ingest.ParentChild, ingest.ParentChunkSize, ingest.ChildChunkSize)
	fmt.Printf("召回：候选池=%d，每路候选=%d，向量阈值=%.2f，BM25 阈值=%.2f\n", cfg.Hybrid.TopK, cfg.Hybrid.ChannelTopK, cfg.Vector.ScoreThreshold, cfg.Keyword.ScoreThreshold)
	rrf := cfg.Hybrid.RRF.Effective()
	fmt.Printf("混合通道：向量=%t，关键词=%t，权重=%.2f/%.2f，RRF K=%d，失败策略=%s\n", rrf.VectorWeight > 0, rrf.KeywordWeight > 0, rrf.VectorWeight, rrf.KeywordWeight, rrf.K, cfg.Hybrid.FailurePolicy)
	fmt.Printf("精排配置开启=%t，父块扩展=%t，同父块合并=%t，父块读取回退=%t，最多返回=%d\n", cfg.RerankProvider != nil && !cfg.Rerank.Disabled, cfg.Search.ExpandParents, cfg.Search.CollapseSameParent, cfg.Search.AllowParentFallback, cfg.Search.FinalTopK)
	fmt.Println("MMR：未接入 Service.Search；Query Rewrite：未实现。")
}

// printSearch 展示实际执行状态，再分别展示子块证据和最终上下文。
// Score 随阶段变化，VectorScore/KeywordScore 保留原始通道分数，ModelScore 是精排分。
// EffectiveContent 可能为父块正文，但 Content/ChunkID 始终保留真实命中子块。
func printSearch(mode string, response search.Response, numbers map[string]int, gold map[string]bool) {
	fmt.Printf("\n【%s】实际召回通道=%s，降级=%t\n", mode, response.Retrieval.ModeUsed, response.Retrieval.Degraded)
	for _, channel := range response.Retrieval.Channels {
		fmt.Printf("通道 %s：%s\n", channel.Channel, channel.Error)
	}
	fmt.Printf("精排：实际应用=%t，状态=%s，候选数=%d，模型阈值=%.2f，生效阈值=%.2f\n", response.Rerank.Applied, response.Rerank.Outcome, response.Rerank.CandidateCount, response.Rerank.Threshold, response.Rerank.EffectiveThreshold)
	if response.Rerank.Error != "" {
		fmt.Printf("精排回退原因：%s\n", response.Rerank.Error)
	}
	for _, diag := range response.Diagnostics {
		fmt.Printf("流程诊断 [%s]：%s\n", diag.Stage, diag.Message)
	}
	for i, result := range response.Results {
		fmt.Printf("\n【上下文 %d】分数=%.4f，向量原始分=%.4f，BM25 原始分=%.4f，精排模型分=%.4f\n", i+1, result.Score, result.VectorScore, result.KeywordScore, result.ModelScore)
		fmt.Printf("父块实际扩展=%t，上下文字符区间=[%d, %d)\n", result.EffectiveChunkID() != result.ChunkID, result.ContextStartRune, result.ContextEndRune)
		for _, id := range evidenceIDs([]retrieval.SearchResult{result}) {
			if number, ok := numbers[id]; ok {
				fmt.Printf("  命中子块 %d：相关=%t\n", number, gold[id])
			} else {
				fmt.Printf("  命中未标注的其他子块：%s，请检查实验知识库是否只有当前文档\n", id)
			}
		}
		fmt.Printf("最终上下文：\n%s\n", result.EffectiveContent())
	}
}

// 五、环境变量读取。

// envOr 读取环境变量，未填写时使用示例默认值。
func envOr(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
