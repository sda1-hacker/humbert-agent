package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/cloudwego/eino/components/document"
	"github.com/jackc/pgx/v5"

	example "github.com/sda1-hacker/humbert-agent/examples/5-rag"
	"github.com/sda1-hacker/humbert-agent/internal/rag"
	ragembedding "github.com/sda1-hacker/humbert-agent/internal/rag/embedding"
	ragindexer "github.com/sda1-hacker/humbert-agent/internal/rag/indexer"
)

func main() { example.Run(upload) }

// upload：固定文件 → 保存原件 → 解析 → 父子分块 → 真实 Embedding → PG 原子发布。
func upload(ctx context.Context) error {
	cfg := example.Config()
	path, err := filepath.Abs(example.FilePath)
	if err != nil {
		return err
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("上传路径必须是普通文件：%s", path)
	}
	pool, err := example.OpenPostgres(ctx, cfg, true)
	if err != nil {
		return err
	}
	defer pool.Close()
	source := document.Source{URI: path}
	documentID := rag.DefaultIDGenerator(source)
	// 不同路径使用不同文档 ID；同一路径复用已有记录，也保留旧示例写入的业务 ID。
	// 固定使用 sample-document 会让换文件被误认为更新上一篇文档。
	err = pool.QueryRow(ctx, `SELECT source_key FROM documents
WHERE collection_id=$1 AND source_uri=$2 ORDER BY id LIMIT 1`, cfg.CollectionID, path).Scan(&documentID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	sources, err := rag.NewLocalSourceStore(cfg.SourceDirectory, cfg.Loader.ParseOptions.MaxInputBytes)
	if err != nil {
		return err
	}
	defer sources.Close()
	embedder, err := ragembedding.New(ctx, cfg.Embedding)
	if err != nil {
		return err
	}
	profile := ragembedding.NewProfile(cfg.Embedding, cfg.InputBuilder)
	indexerCfg := cfg.Indexer
	indexerCfg.Embedder, indexerCfg.Dimensions = embedder, cfg.Embedding.Dimensions
	indexerCfg.InputBudget = cfg.Embedding.InputBudget
	indexerCfg.ProfileID, indexerCfg.ProfileJSON = profile.ID(), profile.JSON()
	indexerCfg.VersionedDocuments = true // 正式版本控制仍保留，旧任务不能覆盖新版本。
	indexer, err := ragindexer.NewIndexer(pool, indexerCfg)
	if err != nil {
		return err
	}
	loader, err := example.NewLoader(ctx, cfg.Loader)
	if err != nil {
		return err
	}
	service, err := rag.New(ctx, rag.Dependencies{
		Loader: loader, Images: loader, ImageStates: indexer, Indexer: indexer,
		Lifecycle: indexer, ChunkReader: indexer, Documents: indexer, Sources: sources,
		Config: rag.Config{Ingest: cfg.Ingest, InputBudget: cfg.Embedding.InputBudget, InputBuilder: cfg.InputBuilder},
	})
	if err != nil {
		return err
	}
	defer service.Close()
	if _, err := service.RecoverDocuments(ctx, cfg.CollectionID); err != nil {
		return err
	}
	result, err := service.Ingest(ctx, rag.IngestRequest{
		CollectionID: cfg.CollectionID, DocumentID: documentID,
		Source: source, Splitter: cfg.Chunking.Splitter,
		ParentChild:     cfg.Chunking.ParentChild,
		ParentChunkSize: cfg.Chunking.ParentChunkSize, ChildChunkSize: cfg.Chunking.ChildChunkSize,
	})
	if err != nil {
		return err
	}
	for _, document := range result.Documents {
		for _, warning := range document.Warnings {
			fmt.Printf("解析提示：%s\n", warning)
		}
	}
	// 从 PG 回读，确认原件引用、状态和分块确实已经持久化。
	stored, err := service.GetDocument(ctx, cfg.CollectionID, documentID)
	if err != nil {
		return err
	}
	chunks, err := service.ListChunks(ctx, cfg.CollectionID, documentID)
	if err != nil {
		return err
	}
	fmt.Printf("上传完成：父块 %d，子块 %d，PG 回读分块 %d；原件目录：%s\n",
		result.ParentCount, result.ChildCount, len(chunks), sources.Directory())
	fmt.Printf("文档 ID：%s；不同文件追加，同一路径重传更新版本。\n", documentID)
	for _, chunk := range chunks {
		fmt.Printf("子块 %d [%d,%d)：%.100s\n", chunk.ChunkIndex, chunk.StartRune, chunk.EndRune, chunk.Content)
	}
	return example.PrintJSON(stored)
}
