package indexer

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sda1-hacker/humbert-agent/internal/rag"
	ragconfig "github.com/sda1-hacker/humbert-agent/internal/rag/config"
)

// DocumentConfig 固定知识库的模型档案，不包含模型凭据。
type DocumentConfig struct{ ProfileID, ProfileJSON string }

// DocumentStore 保存原件引用、管理元数据、权威分块和版本，不负责调用模型或文件存储。
// 单独使用时可与 eino-ext 的 ES/Milvus 等组件组合；Indexer 则复用它完成 PG 同库发布。
// 事务写入在 postgres_ingestion.go，版本操作在 postgres_revisions.go，分块读取在 postgres_reader.go。
type DocumentStore struct {
	db     database
	config DocumentConfig
}

// NewDocumentStore 绑定已迁移的文档库，连接池由装配方负责关闭。
func NewDocumentStore(pool *pgxpool.Pool, cfg DocumentConfig) (*DocumentStore, error) {
	if pool == nil {
		return nil, errors.New("rag document store: nil pool")
	}
	return &DocumentStore{db: poolDatabase{pool}, config: cfg}, nil
}

// 内部事务接口只封装 pgx 已有操作，便于离线验证提交、回滚和过期版本；不作为后端扩展接口。
type transaction interface {
	QueryRow(context.Context, string, ...any) pgx.Row
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	Commit(context.Context) error
	Rollback(context.Context) error
}

type database interface {
	Begin(context.Context) (transaction, error)
}

type poolDatabase struct{ pool *pgxpool.Pool }

func (d poolDatabase) Begin(ctx context.Context) (transaction, error) { return d.pool.Begin(ctx) }

// processSnapshot 固定实际输入，拒绝同一版本以不同分块或检索文本重试。
func processSnapshot(batch rag.IngestionBatch, texts []string, hashKey string) (json.RawMessage, error) {
	input, err := json.Marshal(struct {
		Title             string
		Source            rag.SourceFile
		Metadata          map[string]any
		Parents, Children []rag.ChunkRecord
		SearchContent     []string
	}{batch.Title, batch.Source, batch.Metadata, batch.Parents, batch.Children, texts})
	if err != nil {
		return nil, fmt.Errorf("encode ingestion snapshot: %w", err)
	}
	config := make(map[string]json.RawMessage)
	if len(batch.ProcessConfig) > 0 {
		if err := json.Unmarshal(batch.ProcessConfig, &config); err != nil {
			return nil, err
		}
	}
	if config == nil {
		return nil, fmt.Errorf("%w: process config must be an object", rag.ErrInvalidIngestionBatch)
	}
	config[hashKey], _ = json.Marshal(fmt.Sprintf("%x", sha256.Sum256(input)))
	return json.Marshal(config)
}

// StartDocument 在解析前保留可查询的管理记录；更新已有文档时保留已发布版本的原件引用。
// 新上传原件暂存到 metadata.rag_pending_source，失败后仍可定位；发布成功时由新元数据替换。
// 与发布/删除共用版本行锁，避免旧任务在删除后重新创建文档。
func (p *DocumentStore) StartDocument(ctx context.Context, collectionID, documentID string, attempt int64, source rag.SourceFile) error {
	if strings.TrimSpace(collectionID) == "" || strings.TrimSpace(documentID) == "" || attempt <= 0 || source.Size < 0 {
		return errors.New("rag: document tracking requires scope and positive attempt")
	}
	tx, err := p.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer rollbackTransaction(ctx, tx)
	tag, err := tx.Exec(ctx, `UPDATE rag_document_heads SET attempt=attempt WHERE collection_id=$1 AND document_id=$2 AND attempt=$3 AND NOT deleted`, collectionID, documentID, attempt)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrStaleIngestion
	}
	sourceJSON, err := json.Marshal(source)
	if err != nil {
		return err
	}
	deadline, ok := ctx.Deadline()
	if !ok {
		// 独立组件调用也必须有恢复期限；Service 已提供包含排队在内的总截止时间。
		deadline = time.Now().Add((ragconfig.Ingest{}).Effective().Timeout)
	}
	tag, err = tx.Exec(ctx, `INSERT INTO documents(collection_id,id,title,document_type,source_uri,file_path,file_name,file_type,file_size,file_hash,embedding_profile_id,parse_status,processing_attempt,processing_stage,processing_deadline)
VALUES($1,$2,$3,'file',$4,$5,$6,$7,$8,$9,$10,'processing',$12,'source',$13)
ON CONFLICT(collection_id,id) DO UPDATE SET
 title=CASE WHEN documents.revision=0 THEN EXCLUDED.title ELSE documents.title END,
 source_uri=CASE WHEN documents.revision=0 THEN EXCLUDED.source_uri ELSE documents.source_uri END,
 file_path=CASE WHEN documents.revision=0 THEN EXCLUDED.file_path ELSE documents.file_path END,
 file_name=CASE WHEN documents.revision=0 THEN EXCLUDED.file_name ELSE documents.file_name END,
 file_type=CASE WHEN documents.revision=0 THEN EXCLUDED.file_type ELSE documents.file_type END,
 file_size=CASE WHEN documents.revision=0 THEN EXCLUDED.file_size ELSE documents.file_size END,
 file_hash=CASE WHEN documents.revision=0 THEN EXCLUDED.file_hash ELSE documents.file_hash END,
 embedding_profile_id=CASE WHEN documents.revision=0 THEN EXCLUDED.embedding_profile_id ELSE documents.embedding_profile_id END,
 metadata=CASE WHEN documents.revision>0 THEN jsonb_set(documents.metadata,'{rag_pending_source}',$11::jsonb,true) ELSE documents.metadata END,
 parse_status='processing',error_message='',error_code='',processing_attempt=EXCLUDED.processing_attempt,
 processing_stage='source',processing_deadline=CASE WHEN documents.processing_attempt=EXCLUDED.processing_attempt
 THEN LEAST(documents.processing_deadline,EXCLUDED.processing_deadline) ELSE EXCLUDED.processing_deadline END,updated_at=NOW()
WHERE documents.processing_attempt<EXCLUDED.processing_attempt
 OR (documents.processing_attempt=EXCLUDED.processing_attempt AND documents.parse_status='processing')`,
		collectionID, documentID, strings.TrimSuffix(source.Name, filepath.Ext(source.Name)), source.URI, source.Path, source.Name, source.Type, source.Size, source.Hash, p.config.ProfileID, string(sourceJSON), attempt, deadline)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrStaleIngestion
	}
	return tx.Commit(ctx)
}

// AdvanceDocument 在进入耗时阶段前检查版本、状态和截止时间；跨进程取消后不会继续下一阶段。
func (p *DocumentStore) AdvanceDocument(ctx context.Context, collectionID, documentID string, attempt int64, stage string) error {
	switch stage {
	case rag.StageParse, rag.StageChunk, rag.StageIndex, rag.StageReady, rag.StagePublish:
	default:
		return errors.New("rag: invalid processing stage")
	}
	tx, err := p.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer rollbackTransaction(ctx, tx)
	tag, err := tx.Exec(ctx, `UPDATE rag_document_heads SET attempt=attempt WHERE collection_id=$1 AND document_id=$2 AND attempt=$3 AND NOT deleted`, collectionID, documentID, attempt)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrStaleIngestion
	}
	tag, err = tx.Exec(ctx, `UPDATE documents SET processing_stage=$4,updated_at=NOW()
WHERE collection_id=$1 AND id=$2 AND processing_attempt=$3 AND parse_status='processing' AND processing_deadline>clock_timestamp()`, collectionID, documentID, attempt, stage)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrStaleIngestion
	}
	return tx.Commit(ctx)
}

// FailDocument 只结束本次未发布任务。已提交但客户端收不到确认时，不能把成功记录改成失败。
// 分离索引补偿可能刚作废本次 attempt；仅允许匹配的墓碑，不能影响后续新任务。
func (p *DocumentStore) FailDocument(ctx context.Context, collectionID, documentID string, attempt int64, failure rag.DocumentFailure) error {
	if attempt <= 0 || (failure.Status != rag.DocumentFailed && failure.Status != rag.DocumentCancelled) {
		return errors.New("rag: invalid document failure state")
	}
	tx, err := p.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer rollbackTransaction(ctx, tx)
	// 先锁定匹配的版本行，再更新状态，不能只用快照中的 EXISTS 判断当前任务。
	tag, err := tx.Exec(ctx, `UPDATE rag_document_heads SET attempt=attempt WHERE collection_id=$1 AND document_id=$2
AND ((attempt=$3 AND NOT deleted) OR (attempt=$3+1 AND deleted))`, collectionID, documentID, attempt)
	if err != nil || tag.RowsAffected() == 0 {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE documents SET parse_status=$4,error_message=$5,error_code=$6,
processing_stage=$7,processing_deadline=NULL,updated_at=NOW()
WHERE collection_id=$1 AND id=$2 AND revision<>$3 AND processing_attempt=$3 AND parse_status='processing'`,
		collectionID, documentID, attempt, failure.Status, failure.Message, failure.Code, failure.Stage); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// RecoverDocuments 收尾进程退出、断电或超时后残留的任务，不自动重新调用模型。
// 每批锁定至多 256 个版本行且跳过正被其他事务修改的文档；重复调用安全。
// 锁后用新语句重新检查状态，避免把等待行锁期间刚发布成功的文档误判为中断。
func (p *DocumentStore) RecoverDocuments(ctx context.Context, collectionID string) (int64, error) {
	collectionID = strings.TrimSpace(collectionID)
	if collectionID == "" {
		return 0, errors.New("rag: collection id required for recovery")
	}
	db, ok := p.db.(poolDatabase)
	if !ok {
		return 0, errors.New("rag: interruption recovery requires a PostgreSQL pool")
	}
	var total int64
	for {
		n, scanned, err := func() (int64, int, error) {
			tx, err := db.pool.Begin(ctx)
			if err != nil {
				return 0, 0, err
			}
			defer rollbackTransaction(ctx, tx)
			rows, err := tx.Query(ctx, `SELECT h.document_id,d.processing_attempt
FROM rag_document_heads h JOIN documents d ON d.collection_id=h.collection_id AND d.id=h.document_id
WHERE h.collection_id=$1 AND d.parse_status='processing' AND d.processing_deadline<=clock_timestamp()
AND h.attempt>=d.processing_attempt ORDER BY h.document_id LIMIT 256 FOR UPDATE OF h SKIP LOCKED`, collectionID)
			if err != nil {
				return 0, 0, err
			}
			type candidate struct {
				id      string
				attempt int64
			}
			var pending []candidate
			for rows.Next() {
				var c candidate
				if err := rows.Scan(&c.id, &c.attempt); err != nil {
					rows.Close()
					return 0, 0, err
				}
				pending = append(pending, c)
			}
			rows.Close()
			if err := rows.Err(); err != nil {
				return 0, 0, err
			}
			var changed int64
			for _, c := range pending {
				tag, err := tx.Exec(ctx, `UPDATE documents SET parse_status='failed',error_code='interrupted',
error_message='上次导入未在截止时间前完成，可能因进程退出或超时中断，请重试',processing_deadline=NULL,updated_at=NOW()
WHERE collection_id=$1 AND id=$2 AND processing_attempt=$3 AND parse_status='processing' AND processing_deadline<=clock_timestamp()`, collectionID, c.id, c.attempt)
				if err != nil {
					return 0, 0, err
				}
				if tag.RowsAffected() == 0 {
					continue
				}
				// 状态可能属于已被新版本取代的旧任务；只作废它自己的版本，不能伤及新预留。
				if _, err := tx.Exec(ctx, `UPDATE rag_document_heads SET attempt=attempt+1,deleted=true WHERE collection_id=$1 AND document_id=$2 AND attempt=$3`, collectionID, c.id, c.attempt); err != nil {
					return 0, 0, err
				}
				changed++
			}
			if err := tx.Commit(ctx); err != nil {
				return 0, 0, err
			}
			return changed, len(pending), nil
		}()
		total += n
		if err != nil || scanned < 256 {
			return total, err
		}
	}
}

// marshalMetadata 保持空元数据为 JSON 对象，避免 SQL 中的 JSON 合并遇到 null。
func marshalMetadata(metadata map[string]any) (string, error) {
	if metadata == nil {
		return "{}", nil
	}
	data, err := json.Marshal(metadata)
	return string(data), err
}

// nullableString 将不存在的父块 ID 保存为 NULL，以满足外键语义。
func nullableString(value string) any {
	if value = strings.TrimSpace(value); value == "" {
		return nil
	}
	return value
}
