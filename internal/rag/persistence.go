package rag

import (
	"context"
	"encoding/json"
	"time"

	"github.com/cloudwego/eino/components/document"

	"github.com/sda1-hacker/humbert-agent/internal/rag/retrieval"
)

// DocumentPublisher 原子发布文档元数据与父子分块，完整 Markdown 仅在处理期间用于校验。
// 只有预留版本仍有效时才能成功，失败时上一版文档应保持可见。
// 外部 Indexer 写入时必须保留分块 ID 和集合、文档、版本元数据。
type DocumentPublisher interface {
	PublishDocument(context.Context, IngestionBatch) error
}

// ChunkReader 返回当前已发布文档的普通可检索分块，按 ChunkIndex 升序排列。
type ChunkReader interface {
	ListChunks(context.Context, string, string) ([]ChunkRecord, error)
}

// PublishedChunkReader 按文档版本校验外部索引命中，并补全权威正文。
// 已删除、未发布及过期版本的命中必须丢弃；保留输入顺序和召回分数。
type PublishedChunkReader interface {
	ResolveChunks(context.Context, string, []retrieval.SearchResult) ([]retrieval.SearchResult, error)
}

// DocumentLifecycle 与存储实现无关，保留版本预留、取消和删除能力。
type DocumentLifecycle interface {
	DocumentVersioner
	InvalidateDocument(context.Context, string, string) error
	DeleteDocument(context.Context, string, string) error
}

// SourceFile 描述托管原件；Path 是存储内的相对标识，URI 保留用户导入时的来源。
// 原件摘要与 IngestionBatch.ContentHash 分别标识源文件和转换后的正文，不能混用。
type SourceFile struct {
	URI  string `json:"source_uri"`
	Path string `json:"file_path"`
	Name string `json:"file_name"`
	Type string `json:"file_type"`
	Size int64  `json:"file_size"`
	Hash string `json:"file_hash"`
}

// SourceStore 保存原格式文件，并返回供 Eino Loader 读取的托管来源；不负责解析或向量化。
type SourceStore interface {
	Save(context.Context, document.Source) (document.Source, SourceFile, error)
	// Resolve 校验托管原件的标识、大小和摘要，再返回解析来源；不能回退到任意本地路径。
	Resolve(context.Context, SourceFile) (document.Source, error)
}

const (
	DocumentProcessing = "processing"
	DocumentCompleted  = "completed"
	DocumentFailed     = "failed"
	DocumentCancelled  = "cancelled"

	StageSource    = "source"
	StageParse     = "parse"
	StageChunk     = "chunk"
	StageImages    = "images"
	StageIndex     = "index"
	StageEmbedding = "embedding"
	StageReady     = "ready"
	StagePublish   = "publish"
	StageCompleted = "completed"
)

// IngestionError 保留阶段和原始错误，调用方仍可用 errors.Is 判断取消、超时等原因。
// 通用 Eino Store 内部不可见时使用 index；原子 PG 索引器可进一步报告 embedding/publish。
type IngestionError struct {
	Stage string
	Err   error
}

func (e *IngestionError) Error() string { return "rag " + e.Stage + ": " + e.Err.Error() }
func (e *IngestionError) Unwrap() error { return e.Err }

// DocumentFailure 是本次导入的终态，不会覆盖上次已发布内容或后续任务。
type DocumentFailure struct {
	Status, Stage, Code, Message string
}

// DocumentRecord 是文档管理记录；正文在 chunks 中，全文不通过拼接分块反向恢复。
// ParseStatus 描述最近一次导入，Revision 描述已发布版本，失败重试期间两者可以不同。
type DocumentRecord struct {
	CollectionID string `json:"collection_id"`
	// ID 是稳定业务键，对应 documents.source_key；数据库内部自增主键不参与 Eino 标识。
	ID    string `json:"id"`
	Title string `json:"title"`
	Type  string `json:"type"`
	SourceFile
	// EmbeddingProfileID 返回模型档案指纹；数据库关联列保存档案行的 BIGINT 主键。
	EmbeddingProfileID string `json:"embedding_profile_id"`
	// Attempt 是最新版本栅栏，取消也会递增；Revision 才是当前已发布的版本。
	Attempt       int64           `json:"attempt"`
	Revision      int64           `json:"revision"`
	ContentHash   string          `json:"content_hash"`
	ProcessConfig json.RawMessage `json:"process_config"`
	Metadata      map[string]any  `json:"metadata"`
	ParseStatus   string          `json:"parse_status"`
	ErrorMessage  string          `json:"error_message"`
	// ProcessingAttempt 对应状态所属任务，与取消后递增的 Attempt、已发布 Revision 分别记录。
	ProcessingAttempt  int64      `json:"processing_attempt"`
	ProcessingStage    string     `json:"processing_stage"`
	ProcessingDeadline *time.Time `json:"processing_deadline,omitempty"`
	ErrorCode          string     `json:"error_code"`
	ProcessedAt        *time.Time `json:"processed_at,omitempty"`
	CreatedAt          time.Time  `json:"created_at"`
	UpdatedAt          time.Time  `json:"updated_at"`
}

// DocumentTracker 记录导入开始和失败；成功状态由发布事务写入，不能先于分块和索引提交。
// 所有状态更新必须校验 Attempt，旧任务不能覆盖新任务或已发布版本。
type DocumentTracker interface {
	StartDocument(context.Context, string, string, int64, SourceFile) error
	AdvanceDocument(context.Context, string, string, int64, string) error
	FailDocument(context.Context, string, string, int64, DocumentFailure) error
	// RecoverDocuments 仅收尾已过总截止时间的任务，并作废版本；不能按启动时间清空 processing。
	RecoverDocuments(context.Context, string) (int64, error)
	GetDocument(context.Context, string, string) (DocumentRecord, error)
}
