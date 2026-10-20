// Package config 统一管理 RAG 的参数、默认值、校验和加载，不创建模型或数据库连接。
package config

import (
	"context"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/cloudwego/eino/components/document"
	"github.com/cloudwego/eino/components/embedding"
	"github.com/cloudwego/eino/components/model"

	"github.com/sda1-hacker/humbert-agent/internal/documenttext"
)

const (
	ProviderOpenAI            = "openai"                 // 使用 OpenAI 兼容的 embeddings 接口。
	ProviderOllama            = "ollama"                 // 使用 Ollama 原生 /api/embed 接口。
	ProviderAgent             = "agent"                  // 图片理解复用应用 Model Registry；不执行 Agent。
	DefaultOllamaURL          = "http://localhost:11434" // Ollama 本地服务的默认基础地址。
	StrategyLegacy            = "legacy"                 // WeKnora 递归策略的历史名称，与 recursive 等价。
	StrategyAuto              = "auto"                   // 按文档结构尝试标题、章节和递归切分。
	StrategyHeading           = "heading"                // 优先按 Markdown 标题切分，失败时递归兜底。
	StrategyHeuristic         = "heuristic"              // 优先按章节、分页等信号切分，失败时递归兜底。
	StrategyRecursive         = "recursive"              // 按分隔符递归切分，保护表格和代码块。
	FailureStrict             = "strict"                 // 混合召回任一启用通道失败，整个查询失败。
	FailureAllowPartial       = "allow_partial"          // 单路普通错误或单路超时可降级；整体取消、超时仍返回错误。
	DefaultChunkSize          = 512                      // 普通分块的目标 Unicode 字符数，不是字节数。
	DefaultChunkOverlap       = 80                       // 普通分块的最大重叠字符数。
	DefaultParentChunkSize    = 4096                     // 父块目标字符数，用于补充回答上下文。
	DefaultChildChunkSize     = 384                      // 子块目标字符数，用于索引和召回。
	DefaultEmbeddingBatchSize = 64                       // 每次向量请求的文档条数上限，仍受 token 总预算限制。
	DefaultTopK               = 50                       // 默认召回候选数，不是最终提供给回答模型的条数。
	DefaultVectorThreshold    = 0.15                     // 默认向量余弦相似度下限。
	DefaultKeywordThreshold   = 0.30                     // 默认 BM25 原始分数下限，与向量分数不可直接比较。
	DefaultRequestTimeout     = 30 * time.Second         // 通用请求超时；具体阶段可单独覆盖。
	DefaultOllamaTimeout      = 2 * time.Minute          // 本地向量请求默认允许更长时间。
	CleanupTimeout            = 5 * time.Second          // 取消后的补偿和回滚仍保留独立的短清理窗口。
	// 当前 PG HNSW 半精度索引的维度与候选上限；实际输出维度统一配置在 Embedding.Dimensions。
	PostgresMaxDimensions = 4000
	PostgresMaxTopK       = 200
)

// Config 是部署和调参的唯一入口。组件只接收自己的参数，不读取文件或环境变量。
// 模型、客户端和回调以 mapstructure:"-" 排除出文件配置，由装配代码注入。
// 字段注释中的默认值以 Default/Load 为准；rag.example.yaml 的实验值可以不同。
// 直接使用零值结构体时，布尔开关不会自动套用 Default 中的 true。
type Config struct {
	// Database 是数据库连接参数；文件与环境变量按字段覆盖，密码不进入配置快照。
	Database Database `mapstructure:"database"`
	// Postgres 控制连接、SQL、锁等待、扩展安装及迁移的超时。
	Postgres Postgres `mapstructure:"postgres"`
	// EnsureSchema 默认 false；PG ingest 示例开启后会创建缺失的库、安装扩展并迁移，query 不初始化。
	// 独立迁移命令始终按命令参数执行，不受此开关限制。
	EnsureSchema bool `mapstructure:"ensure_schema"`
	// CollectionID 是逻辑知识库 ID，默认空；PG 示例必填，同一库不能混用不同向量模型档案。
	CollectionID string `mapstructure:"collection_id"`
	// SourceDirectory 保存知识库原件；空值用系统用户配置目录下的 humbert-agent/rag/sources。
	// 相对路径以进程工作目录解析；数据库只保存内容摘要命名的相对文件标识。
	SourceDirectory string `mapstructure:"source_directory"`
	// Loader 配置文档解析、图片保存及独立的图片识别；切分与调度仍由导入流程负责。
	Loader Loader `mapstructure:"loader"`
	// Ingest 限制导入并发、正文规模、子块数量和总耗时。
	Ingest Ingest `mapstructure:"ingest"`
	// Chunking 控制切分算法及父子块窗口；由装配层传给每次导入请求。
	Chunking Chunking `mapstructure:"chunking"`
	// Embedding 同时配置文档和查询向量；二者必须使用同一个模型及输入预算。
	Embedding Embedding `mapstructure:"embedding"`
	// Indexer 控制索引写入的批量大小，模型实例与档案在运行时注入。
	Indexer Indexer `mapstructure:"indexer"`
	// Vector 是内置 PG 向量检索器的参数，替换后端时使用对应 Eino 组件的配置。
	Vector Vector `mapstructure:"vector"`
	// Keyword 是内置 PG BM25 检索器的参数，不会调用向量模型。
	Keyword BM25 `mapstructure:"keyword"`
	// Hybrid 控制双路召回、失败策略及 RRF 排名融合。
	Hybrid Hybrid `mapstructure:"hybrid"`
	// InputBuilder 规定标题、上下文路径与正文如何拼成索引输入，改变规则后应重新导入。
	InputBuilder SearchText `mapstructure:"input_builder"`
	// Rerank 控制候选重评分、阈值和排序策略，默认关闭远程精排。
	Rerank Rerank `mapstructure:"rerank"`
	// RerankProvider 是可选精排 HTTP 服务；Rerank.Disabled=false 时必须配置地址和模型。
	RerankProvider *RerankHTTP `mapstructure:"rerank_provider"`
	// Search 控制最终返回条数、上下文预算和父块扩展，不是 Agent 对话压缩配置。
	Search Search `mapstructure:"search"`
}

// Database 只保存连接参数；由 DSN 交给 pgx，不持有连接或执行初始化。
type Database struct {
	// Host 是数据库主机或 Unix socket 目录，默认 localhost。
	Host string `mapstructure:"host"`
	// Port 是数据库端口，默认 5432，合法范围为 1..65535。
	Port int `mapstructure:"port"`
	// Name 是目标数据库名，默认 humbert_rag；显式初始化时自动创建缺失的库。
	Name string `mapstructure:"name"`
	// User 是已有的 PostgreSQL 角色；必须显式配置，程序不创建角色。
	User string `mapstructure:"user"`
	// Password 是连接密码，允许无密码认证；空格和特殊字符原样保留，不写入配置快照。
	Password string `mapstructure:"password" json:"-" yaml:"-"`
	// SSLMode 默认 disable，适用于本机测试；远程按服务要求设置 require/verify-full 等。
	SSLMode string `mapstructure:"sslmode"`
}

// DSN 用标准 URL 编码组合连接串，避免密码、库名含特殊字符时破坏连接参数。
// Unix socket 使用 pgx 原生 host 查询参数，不依赖当前运行平台。
func (c Database) DSN() string {
	u := url.URL{Scheme: "postgres", Host: net.JoinHostPort(c.Host, strconv.Itoa(c.Port)), User: url.UserPassword(c.User, c.Password), Path: "/" + c.Name}
	query := url.Values{"sslmode": {c.SSLMode}}
	if strings.HasPrefix(c.Host, "/") {
		u.Host = ""
		query.Set("host", c.Host)
		query.Set("port", strconv.Itoa(c.Port))
	}
	// pgx 的 URI 查询参数遵循 libpq：空格须写成 %20，不能使用表单编码的加号。
	u.RawQuery = strings.ReplaceAll(query.Encode(), "+", "%20")
	return u.String()
}

// Postgres 统一连接、SQL、锁等待和管理操作超时；DSN 中显式设置的驱动参数优先。
type Postgres struct {
	// Dimensions 由装配层取 Embedding.Dimensions；仅完整 PG 索引初始化使用，不再单独提供 YAML 配置。
	Dimensions int `mapstructure:"-"`
	// ConnectTimeout 是单次建连超时，0 用 10s；DSN 中的 connect_timeout 优先。
	ConnectTimeout time.Duration `mapstructure:"connect_timeout"`
	// StatementTimeout 是普通 SQL 的执行超时，0 用 30s；DSN 中的 statement_timeout 优先。
	StatementTimeout time.Duration `mapstructure:"statement_timeout"`
	// LockTimeout 是 SQL 等锁超时，0 用 5s；DSN 中的 lock_timeout 优先。
	LockTimeout time.Duration `mapstructure:"lock_timeout"`
	// ExtensionTimeout 限制安装并检查数据库扩展的总时间，0 用 30s。
	ExtensionTimeout time.Duration `mapstructure:"extension_timeout"`
	// MigrationTimeout 限制迁移流程，迁移事务也用它覆盖普通 SQL 超时；0 用 5m。
	MigrationTimeout time.Duration `mapstructure:"migration_timeout"`
}

// Loader 直接复用公共解析选项，页眉页脚、OCR 和资源限制只配置一次。
type Loader struct {
	// ParseOptions 是附件与 RAG 共用的解析参数，包括文件大小、解压、OCR 和解析超时。
	ParseOptions documenttext.ParserOptions `mapstructure:"parse_options"`
	// AssetDirectory 保存转换图片的独立目录；空/auto 使用本机配置目录，数据库仅保留内容摘要文件名。
	AssetDirectory string `mapstructure:"asset_directory"`
	// RecognizeImages 默认 false；开启后在导入阶段按图处理，成功动作缓存、失败动作可重试。
	RecognizeImages bool `mapstructure:"recognize_images"`
	// ImageRecognition 集中配置本地 OCR 或视觉模型；与 Embedding 模型、Agent 凭据相互独立。
	ImageRecognition ImageRecognition `mapstructure:"image_recognition"`
	// ImageModels 由应用或示例注入统一模型注册表，不在 RAG 文件中复制用户的 API Key。
	ImageModels ImageModelResolver `mapstructure:"-" json:"-" yaml:"-"`
	// ImageText 可由 Go 注入图片识别器，最后一个参数是本次动作的提示词；不使用未知实现的持久缓存。
	ImageText func(context.Context, []byte, string, string) (string, error) `mapstructure:"-" json:"-" yaml:"-"`
	// NormalizeLineEndings 默认 true，将 CRLF/CR 统一为 LF 后再计算原文分块范围。
	NormalizeLineEndings bool `mapstructure:"normalize_line_endings"`
	// IDGenerator 仅由 Go 注入；nil 使用来源 URI 的稳定哈希生成原始文档 ID。
	IDGenerator func(document.Source) string `mapstructure:"-" json:"-" yaml:"-"`
}

// ImageModelResolver 只依赖 Eino；实现方负责选择全局图片模型并管理凭据。
// 返回基础模型、实际模型名、稳定配置指纹；client/maxOutput 保留导入任务自己的资源边界。
type ImageModelResolver interface {
	ImageModel(context.Context, *http.Client, int) (model.BaseChatModel, string, string, error)
}

// ImageRecognition 只负责将图片转换为可检索文字；没有工具权限，不生成最终问答。
type ImageRecognition struct {
	// Provider 默认 agent，复用应用的图片模型；也可独立选 ollama/openai，tesseract 仅显式启用。
	Provider string `mapstructure:"provider"`
	// Pipeline 默认 default：按 Actions 执行；smart_ocr 先生成描述/文字属性，再按需转录。
	Pipeline string `mapstructure:"pipeline"`
	// Actions 默认 [caption,ocr]；tesseract 默认仅 [ocr]，不能生成图片描述。
	Actions []string `mapstructure:"actions"`
	// MaxConcurrent 是同一导入中并行处理图片的上限，默认 2；整篇导入并发仍受 ingest 限制。
	MaxConcurrent int `mapstructure:"max_concurrent"`
	// RetryAttempts 是临时网络错误/限流的总尝试次数，默认 3；1 关闭重试，配置/鉴权错误不重试。
	RetryAttempts int `mapstructure:"retry_attempts"`
	// RetryInterval 是首次退避时间，默认 250ms，随后翻倍，取消/超时立即结束等待。
	RetryInterval time.Duration `mapstructure:"retry_interval"`
	// BaseURL 是视觉服务基础地址；Ollama 默认为本机 11434，OpenAI 兼容服务必须显式填写。
	BaseURL string `mapstructure:"base_url"`
	// Model 在 agent 路由自动读取；独立服务需填写支持图像输入的模型，Embedding 不能替代图片转文字。
	Model string `mapstructure:"model"`
	// APIKey 仅发送到显式配置的 OpenAI 兼容地址，不写入处理快照或识别缓存。
	APIKey string `mapstructure:"api_key" json:"-" yaml:"-"`
	// Revision 是模型部署版本；同名模型更新后修改它，使图片识别缓存失效。
	Revision string `mapstructure:"revision"`
	// Timeout 限制单个 OCR/描述动作（含重试）的时间，0 用 1m；同时受整次导入的 context 控制。
	Timeout time.Duration `mapstructure:"timeout"`
	// MaxOutputTokens 限制视觉模型输出，0 用 4096；本地 OCR 仍受解析输出字节上限约束。
	MaxOutputTokens int `mapstructure:"max_output_tokens"`
}

// Ingest 限制单次导入规模、进程并发和包含排队在内的总时间。
type Ingest struct {
	// MaxConcurrent 是同一 Service 同时执行的导入任务数，0 用 2；其余任务等待空位。
	MaxConcurrent int `mapstructure:"max_concurrent"`
	// MaxChunks 是每篇文档最终子块/普通块的数量上限，不计父块；0 用 5000。
	MaxChunks int `mapstructure:"max_chunks"`
	// MaxTextBytes 是解析正文与图片识别文本的合计 UTF-8 字节上限，0 用 8 MiB；不同于源文件大小。
	MaxTextBytes int `mapstructure:"max_text_bytes"`
	// Timeout 包含排队、解析、分块、向量化及写入的总时间，0 用 10m。
	Timeout time.Duration `mapstructure:"timeout"`
}

// Chunking 给所有导入提供统一默认参数；文档 ID、来源路径仍是单次请求的数据。
type Chunking struct {
	// Splitter 是普通切分及推导父子窗口的基础参数。
	Splitter Splitter `mapstructure:"splitter"`
	// ParentChild 默认 true；父块提供上下文，仅子块参与向量和 BM25 索引。
	ParentChild bool `mapstructure:"parent_child"`
	// ParentChunkSize 是父块目标字符数，仅父子模式生效；0 用 4096，父块不套用 TokenLimit。
	ParentChunkSize int `mapstructure:"parent_chunk_size"`
	// ChildChunkSize 是子块目标字符数，仅父子模式生效；0 用 384，仍受完整模型输入预算限制。
	ChildChunkSize int `mapstructure:"child_chunk_size"`
}

// Splitter 的大小按 Unicode 字符计数，TokenLimit 是切分目标；完整模型输入另受 Budget 限制。
type Splitter struct {
	// ChunkSize 是普通块的目标 Unicode 字符数，0 用 512；结构保护可能保留更大的完整块。
	ChunkSize int `mapstructure:"chunk_size"`
	// ChunkOverlap 是最大重叠字符数，Default 为 80，显式 0 关闭；业务入口最多取块大小的一半。
	// 父子模式中，正值会让子块重叠按 ChildChunkSize/5 推导，不直接采用此数值。
	ChunkOverlap int `mapstructure:"chunk_overlap"`
	// Separators 按顺序尝试字面分隔符，空列表用空行、换行、中文句号；分隔符不会从原文丢失。
	Separators []string `mapstructure:"separators"`
	// Strategy 可选 auto/heading/heuristic/recursive（legacy 同 recursive）；空值使用 recursive，前两种结构策略可递归兜底。
	Strategy string `mapstructure:"strategy"`
	// TokenLimit 是按语言比例估算的切分目标，0 关闭；不是模型真实 tokenizer 的硬上限。
	TokenLimit int `mapstructure:"token_limit"`
	// Languages 可填 zh/en/de/mixed，影响章节规则及近似 token 估算；首项用于 token 目标。
	// 空列表尝试全部支持的章节规则，并按正文判断估算语言。
	Languages []string `mapstructure:"languages"`
}

// Clone 隔离分隔符和语言切片，允许同一组件安全应用单次调用的配置覆盖。
func (c Splitter) Clone() Splitter {
	c.Separators, c.Languages = slices.Clone(c.Separators), slices.Clone(c.Languages)
	return c
}

// Budget 按完整模型输入限制单条和单批 token；计数器不参与文件配置。
type Budget struct {
	// MaxInputTokens 限制单条完整输入（标题、路径、正文），0 用 8192；不是只统计分块正文。
	MaxInputTokens int `mapstructure:"max_input_tokens"`
	// MaxBatchTokens 限制一次向量请求的输入总量，0 用 65536；条数上限同时生效。
	MaxBatchTokens int `mapstructure:"max_batch_tokens"`
	// CountTokens 仅由 Go 注入，文档和查询应共用；nil 按 UTF-8 字节数保守估算，不是精确 token 数。
	CountTokens func(string) int `mapstructure:"-" json:"-" yaml:"-"`
}

// Embedding 统一 OpenAI 兼容协议和原生 Ollama 参数；不会把密钥写入配置快照。
type Embedding struct {
	// Provider 可选 openai（兼容协议）或 ollama（原生协议），空值默认 openai。
	Provider string `mapstructure:"provider"`
	// InputBudget 对文档和查询的完整输入统一限额，超长输入不会交给服务端静默截断。
	InputBudget Budget `mapstructure:"input_budget"`
	// APIKey 用于 OpenAI 兼容服务，建议通过环境变量注入；Ollama 原生请求不使用此字段。
	APIKey string `mapstructure:"api_key" json:"-" yaml:"-"`
	// BaseURL 是 HTTP(S) 基础地址，不是完整请求路径；OpenAI 默认 https://api.openai.com/v1。
	// Ollama 默认 http://localhost:11434，不附加 /v1 或 /api/embed。
	BaseURL string `mapstructure:"base_url"`
	// Model 是服务实际接受的模型标识，创建 Embedder 时必填；程序不自动下载或猜测模型名。
	Model string `mapstructure:"model"`
	// ModelRevision 是手动维护的模型权重版本，参与知识库档案；同名模型换权重时修改并重新导入新库。
	ModelRevision string `mapstructure:"model_revision"`
	// Dimensions 是目标输出维度，文档和查询共用；两种协议均请求此值并校验实际返回长度。
	// 0 使用模型原生输出，仅供独立 Embedder；PG 装配必须显式填写 1..4000。
	// 支持 MRL 的模型由服务端截断并重新归一化；任意模型不一定支持降维。
	Dimensions int `mapstructure:"dimensions"`
	// Timeout 是单次向量请求超时，0 按协议取 OpenAI 30s / Ollama 2m，不是整篇导入超时。
	Timeout time.Duration `mapstructure:"timeout"`
	// HTTPClient 仅由 Go 注入，可定制传输；复制后使用，已有非零客户端超时优先。
	HTTPClient *http.Client `mapstructure:"-" json:"-" yaml:"-"`
	// RetryAttempts 是当前 Embedding 请求的总尝试次数，0 用 3，1 关闭重试，最多 5。
	// 仅重试限流、指定临时服务错误和可恢复网络错误，不重跑解析、分块或整篇导入。
	RetryAttempts int `mapstructure:"retry_attempts"`
	// RetryInterval 是首次退避时长，0 用 250ms；之后翻倍但不超过 10s，可被 context 立即中断。
	RetryInterval time.Duration `mapstructure:"retry_interval"`
	// KeepAlive 仅 Ollama 生效：nil 用服务默认值，0 请求后卸载，正值指定请求后继续驻留时长。
	KeepAlive *time.Duration `mapstructure:"keep_alive"`
}

// Indexer 的模型和档案由装配层生成；完整输入预算统一配置在 Embedding.InputBudget。
type Indexer struct {
	// Dimensions 由装配层取 Embedding.Dimensions；用于检查模型结果与 PG 向量列是否一致。
	Dimensions int `mapstructure:"-"`
	// ProfileID 是向量空间档案哈希，由装配层生成；非空时禁止请求临时替换模型。
	ProfileID string `mapstructure:"-"`
	// ProfileJSON 是与 ProfileID 对应的档案正文，只保存模型身份及输入规则，不包含密钥。
	ProfileJSON string `mapstructure:"-"`
	// VersionedDocuments 要求先预留文档版本再写入；PG 示例开启，避免旧任务覆盖新版本。
	VersionedDocuments bool `mapstructure:"-"`
	// CollectionID 是独立调用 Indexer.Store 时的默认库，可由 Eino WithIndex 按请求提供。
	CollectionID string `mapstructure:"-"`
	// Embedder 是运行时注入的 Eino 模型实例，须与查询侧使用同一向量空间。
	Embedder embedding.Embedder `mapstructure:"-" json:"-" yaml:"-"`
	// EmbeddingBatchSize 是单次请求的子块条数上限，0 用 64，仍受 InputBudget.MaxBatchTokens 限制。
	EmbeddingBatchSize int `mapstructure:"embedding_batch_size"`
	// InputBudget 仅供独立索引器注入；顶层配置统一填写 Embedding.InputBudget，再由装配层传入。
	InputBudget Budget `mapstructure:"-"`
}

// Vector 的 Embedder 必须和入库共享同一个向量空间。
type Vector struct {
	// CollectionID 是独立检索器的默认库；Service 查询通过 Eino WithIndex 传入当前库。
	CollectionID string `mapstructure:"-"`
	// Embedder 是查询向量模型，运行时注入；必须与入库模型及其版本一致。
	Embedder embedding.Embedder `mapstructure:"-" json:"-" yaml:"-"`
	// TopK 是直接使用 PG 向量检索器的默认候选数，0 用 50，范围 1..200；每次请求可覆盖。
	TopK int `mapstructure:"top_k"`
	// ScoreThreshold 是余弦相似度下限，范围 0..1，Default 为 0.15；显式 0 仍会过滤负相似度。
	ScoreThreshold float64 `mapstructure:"score_threshold"`
	// Dimensions 由装配层取 Embedding.Dimensions，必须与当前 PG 向量列一致，不再重复配置。
	Dimensions int `mapstructure:"-"`
}

// BM25 不调用 Embedding；原始分数没有固定上界，不能复用向量阈值。
type BM25 struct {
	// CollectionID 是独立关键词检索器的默认库；Service 查询按请求传入，不跨库召回。
	CollectionID string `mapstructure:"-"`
	// TopK 是直接使用 PG BM25 检索器的默认候选数，0 用 50，范围 1..200；每次请求可覆盖。
	TopK int `mapstructure:"top_k"`
	// ScoreThreshold 是非负 BM25 原始分数下限，Default 为 0.30；无固定上界，与向量或精排阈值不同。
	ScoreThreshold float64 `mapstructure:"score_threshold"`
}

// RRF 按排名融合两路候选，不直接相加 BM25 原始分数和向量相似度。
// 贡献为 weight/(K+rank)，rank 从 1 开始；只有整组为零才使用默认 60、0.7、0.3。
type RRF struct {
	// K 是排名平滑常数，越大越弱化相邻名次差异；允许 0，上限 1000000。
	K int `mapstructure:"k"`
	// VectorWeight 是向量通道的非负相对权重，0 关闭该混合通道；不要求两路权重之和为 1。
	VectorWeight float64 `mapstructure:"vector_weight"`
	// KeywordWeight 是关键词通道的非负相对权重，0 关闭该混合通道；配置后的两路不能同时为 0。
	KeywordWeight float64 `mapstructure:"keyword_weight"`
}

// Hybrid 只规定两路召回的组合策略，不创建或选择数据库实现。
type Hybrid struct {
	// TopK 是融合后保留的候选条数，0 用 50；PG 示例也用它设置 semantic/keyword 模式的候选池。
	TopK int `mapstructure:"top_k"`
	// ChannelTopK 是每路默认召回条数，0 用 50；实际至少取 max(ChannelTopK, 本次 TopK)。
	ChannelTopK int `mapstructure:"channel_top_k"`
	// Timeout 是双路召回与融合的总超时，0 用 30s，仍受外层 Search.Timeout 约束。
	Timeout time.Duration `mapstructure:"timeout"`
	// ChannelTimeout 是每路独立超时；0 不另设上限，只遵循 Timeout 和外层上下文。
	ChannelTimeout time.Duration `mapstructure:"channel_timeout"`
	// FailurePolicy 可选 strict/allow_partial，空值等同 strict；整体取消或超时不可降级。
	FailurePolicy string `mapstructure:"failure_policy"`
	// RRF 规定两路排名融合及通道开关，仅影响 hybrid 模式。
	RRF RRF `mapstructure:"rrf"`
}

// SearchText 只定义输入取值规则；正文拼接在 retrieval 包中执行。
// 保留原来的 JSON 字段名，避免既有知识库的模型档案哈希改变。
type SearchText struct {
	// TitleKeys 按优先级取第一个非空字符串标题；空列表用 title、_title、file_name。
	TitleKeys []string `mapstructure:"title_keys"`
	// ContextHeaderKey 指定子块标题路径的元数据键，空值用 rag_context_header；与标题、正文去重拼接。
	ContextHeaderKey string `mapstructure:"context_header_key"`
}

// Rerank 保存数值策略；PassageBuilder 是业务回调，仍由精排组件注入。
type Rerank struct {
	// Disabled 默认 true；关闭远程精排时保留召回顺序，改为 false 必须配置 RerankProvider。
	Disabled bool `mapstructure:"disabled"`
	// TopK 是独立 Engine.Rerank 输出上限，0 用 5；Service 保留候选，由 Search.FinalTopK 最终截断。
	TopK int `mapstructure:"top_k"`
	// Threshold 是模型相关性分数下限，范围 0..1，Default 为 0.3；不是组合排序分数阈值。
	Threshold float64 `mapstructure:"threshold"`
	// MaxCandidates 是送模型评分前的候选数上限，0 用 200；精排开启时不得小于最终返回条数。
	MaxCandidates int `mapstructure:"max_candidates"`
	// DegradeFactor 是首次无候选时降低一次阈值的倍率，范围 0..1，Default 为 0.7；不会重试模型请求。
	DegradeFactor float64 `mapstructure:"degrade_factor"`
	// DegradeFloor 是降级阈值下限，Default 为 0.3；Threshold 不高于此值时不执行降级。
	DegradeFloor float64 `mapstructure:"degrade_floor"`
	// FallbackMinScore 是阈值筛选仍为空时保留最佳一条的最低模型分，Default 为 0.15。
	FallbackMinScore float64 `mapstructure:"fallback_min_score"`
	// ModelWeight 是模型分在组合排序分中的系数，Default 为 0.6。
	// 三项权重各在 0..1 内且总和不超过 1；整组为 0 时补为默认的 0.6、0.3、0.1。
	ModelWeight float64 `mapstructure:"model_weight"`
	// BaseWeight 是召回分在组合排序分中的系数，Default 为 0.3，不是 RRF 的向量权重。
	BaseWeight float64 `mapstructure:"base_weight"`
	// SourceWeight 是来源权重的系数，Default 为 0.1；来源值取结果或 rag_source_weight 元数据，缺失为 0。
	SourceWeight float64 `mapstructure:"source_weight"`
	// MMRLambda 控制独立 Rerank 的相关性/去重权衡，范围 0..1，Default 为 0.7；越大越重相关性。
	// Service.Search 不执行 MMR，本字段在该路径不生效。
	MMRLambda float64 `mapstructure:"mmr_lambda"`
}

// RerankHTTP 配置接受 query/documents 并返回带 index、relevance_score 结果的精排服务。
type RerankHTTP struct {
	// Endpoint 是 HTTP(S) 完整请求地址（例如 /v1/rerank），不会像 Embedding.BaseURL 那样追加路径。
	Endpoint string `mapstructure:"endpoint"`
	// APIKey 非空时使用 Bearer 鉴权，建议由环境变量提供；不会写入配置快照。
	APIKey string `mapstructure:"api_key" json:"-" yaml:"-"`
	// Model 是服务接受的精排模型名；启用精排时与 Endpoint 一起必填。
	Model string `mapstructure:"model"`
	// Timeout 是单次精排 HTTP 请求超时，0 用 30s，仍受 Search.Timeout 约束。
	Timeout time.Duration `mapstructure:"timeout"`
	// HTTPClient 仅由 Go 注入；复制后使用，已有非零客户端超时优先于 Timeout。
	HTTPClient *http.Client `mapstructure:"-" json:"-" yaml:"-"`
	// Headers 是额外请求头，可通过 YAML 配置；复制后使用，不写入配置快照。
	Headers map[string]string `mapstructure:"headers" json:"-" yaml:"-"`
}

// Search 的 token 预算只统计返回块正文及上下文路径，调用方需另留提示词、引用和输出空间。
type Search struct {
	// Timeout 是召回、精排、父块扩展及预算筛选的总超时，0 用 30s，不含导入时间。
	Timeout time.Duration `mapstructure:"timeout"`
	// FinalTopK 是最终返回的上下文条数上限，0 用 5；受预算影响可更少，不是召回候选池大小。
	FinalTopK int `mapstructure:"final_top_k"`
	// MaxContextTokens 是返回正文及 ContextSourceHeader 的总预算，0 用 16384；父块过大时先退子块，再跳过。
	// 不包含消息角色、引用包装、系统提示词及输出预算，调用方必须另留空间。
	MaxContextTokens int `mapstructure:"max_context_tokens"`
	// CountTokens 仅由 Go 注入；nil 按 UTF-8 字节数保守估算，非模型真实 token 数。
	CountTokens func(string) int `mapstructure:"-" json:"-" yaml:"-"`
	// ExpandParents 默认 true；命中子块后尝试读取同文档、同版本父块，未注入父块读取器时不生效。
	ExpandParents bool `mapstructure:"expand_parents"`
	// CollapseSameParent 默认 false；开启后合并共享上下文的命中，保留子块证据，减少重复返回同一父块。
	CollapseSameParent bool `mapstructure:"collapse_same_parent"`
	// AllowParentFallback 默认 false；父块读取发生普通错误时可保留子块继续，取消/超时不会被忽略。
	// 父块缺失或版本不匹配本来就保留子块，不依赖此开关；预算不足时退子块也不依赖此开关。
	AllowParentFallback bool `mapstructure:"allow_parent_fallback"`
}
