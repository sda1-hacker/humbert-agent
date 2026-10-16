package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/cloudwego/eino/components/document"

	"github.com/sda1-hacker/humbert-agent/internal/rag"
	ragembedding "github.com/sda1-hacker/humbert-agent/internal/rag/embedding"
	ragindexer "github.com/sda1-hacker/humbert-agent/internal/rag/indexer"
)

// runIngest 展示：保存原件 → 记录处理状态 → 解析分块 → Embedding → 元数据/分块/双路索引原子发布。
func runIngest(ctx context.Context, args []string, out io.Writer) error {
	set := flag.NewFlagSet("ingest", flag.ContinueOnError)
	set.SetOutput(out)
	file := set.String("file", "", "上传后文件的本地路径，支持 PDF、Office、Markdown 等现有解析格式")
	id := set.String("document-id", "", "稳定文档 ID；留空使用绝对文件路径的哈希，相同 ID 再次导入会更新版本")
	retry := set.Bool("retry", false, "从托管原件重试失败/取消的导入；需提供 document-id")
	cfg, err := loadConfig(set, args)
	if err != nil {
		return err
	}
	if *retry && (strings.TrimSpace(*id) == "" || strings.TrimSpace(*file) != "") {
		return errors.New("retry 需要 -document-id，不接受 -file")
	}
	if !*retry && strings.TrimSpace(*file) == "" {
		return errors.New("请通过 -file 指定文档")
	}
	var path string
	if !*retry {
		path, err = filepath.Abs(*file)
		if err != nil {
			return err
		}
		info, err := os.Stat(path)
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return errors.New("-file 必须指向普通文件")
		}
	}

	pool, err := openPostgres(ctx, cfg, true)
	if err != nil {
		return err
	}
	defer pool.Close()
	sources, err := rag.NewLocalSourceStore(cfg.SourceDirectory, cfg.Loader.ParseOptions.MaxInputBytes)
	if err != nil {
		return err
	}
	defer sources.Close()
	// 直接使用 eino-ext 的真实 Embedding 组件，完整输入预算在模型请求前生效。
	embedder, err := ragembedding.New(ctx, cfg.Embedding)
	if err != nil {
		return err
	}
	profile := embeddingProfile(cfg)
	indexerCfg := cfg.Indexer
	indexerCfg.Dimensions = cfg.Embedding.Dimensions
	indexerCfg.Embedder, indexerCfg.InputBudget = embedder, cfg.Embedding.InputBudget
	indexerCfg.ProfileID, indexerCfg.ProfileJSON = profile.ID(), profile.JSON()
	indexerCfg.VersionedDocuments = true // 先预留版本，防止迟到的旧任务覆盖新文档。
	indexer, err := ragindexer.NewIndexer(pool, indexerCfg)
	if err != nil {
		return err
	}
	service, err := rag.New(ctx, rag.Dependencies{
		Loader: rag.NewLoader(cfg.Loader), Indexer: indexer,
		Lifecycle: indexer, ChunkReader: indexer, Documents: indexer, Sources: sources,
		Config: rag.Config{Ingest: cfg.Ingest, InputBudget: cfg.Embedding.InputBudget, InputBuilder: cfg.InputBuilder},
	})
	if err != nil {
		return err
	}
	defer service.Close()
	// 首次导入入口收尾过期残留；RetryDocument 自带恢复检查，避免重试时重复扫描。
	if !*retry {
		if _, err := service.RecoverDocuments(ctx, cfg.CollectionID); err != nil {
			return err
		}
	}
	source := document.Source{URI: path}
	documentID := strings.TrimSpace(*id)
	if documentID == "" {
		documentID = rag.DefaultIDGenerator(source)
	}
	// 仍走正式 Service：预留版本、解析预算、完整原文范围和父子关系校验都不跳过。
	ingest := service.Ingest
	if *retry {
		ingest = service.RetryDocument
	}
	result, err := ingest(ctx, rag.IngestRequest{
		CollectionID: cfg.CollectionID, DocumentID: documentID, Source: source,
		Splitter: cfg.Chunking.Splitter, ParentChild: cfg.Chunking.ParentChild,
		ParentChunkSize: cfg.Chunking.ParentChunkSize, ChildChunkSize: cfg.Chunking.ChildChunkSize,
	})
	if err != nil {
		// 默认 ID 由路径生成，失败时也输出它，方便检查状态或从托管原件重试。
		return fmt.Errorf("文档 %q 导入失败: %w", documentID, err)
	}
	// 从数据库回读，便于确认返回的块确实已持久化，而不只是内存里的切分结果。
	chunks, err := service.ListChunks(ctx, cfg.CollectionID, documentID)
	if err != nil {
		return err
	}
	stored, err := service.GetDocument(ctx, cfg.CollectionID, documentID)
	if err != nil {
		return err
	}
	if *retry {
		path = stored.URI
	}
	return writeJSON(out, struct {
		CollectionID    string             `json:"collection_id"`
		SourcePath      string             `json:"source_path"`
		Ingestion       rag.IngestResponse `json:"ingestion"`
		StoredChunks    []rag.ChunkRecord  `json:"stored_chunks"`
		Document        rag.DocumentRecord `json:"document"`
		SourceDirectory string             `json:"source_directory"`
	}{cfg.CollectionID, path, result, chunks, stored, sources.Directory()})
}

// runRecover 只执行数据库状态恢复，不创建 Embedder、不解析文件，不发起任何模型请求。
func runRecover(ctx context.Context, args []string, out io.Writer) error {
	set := flag.NewFlagSet("recover", flag.ContinueOnError)
	set.SetOutput(out)
	cfg, err := loadConfig(set, args)
	if err != nil {
		return err
	}
	pool, err := openPostgres(ctx, cfg, false)
	if err != nil {
		return err
	}
	defer pool.Close()
	store, err := ragindexer.NewDocumentStore(pool, ragindexer.DocumentConfig{})
	if err != nil {
		return err
	}
	// 管理操作也有明确上限；超时后已提交的批次保持完成，重复执行会继续恢复剩余任务。
	ctx, cancel := context.WithTimeout(ctx, cfg.Postgres.Effective().StatementTimeout)
	defer cancel()
	n, err := store.RecoverDocuments(ctx, cfg.CollectionID)
	if err != nil {
		return err
	}
	return writeJSON(out, struct {
		CollectionID string `json:"collection_id"`
		Recovered    int64  `json:"recovered"`
	}{cfg.CollectionID, n})
}
