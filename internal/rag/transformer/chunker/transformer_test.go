package chunker

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"
	corechunker "github.com/sda1-hacker/humbert-agent/internal/rag/chunker"
	"github.com/sda1-hacker/humbert-agent/internal/rag/retrieval"
)

// TestTransformerImplementsEinoContract
//
// 真正的接口检查已经在生产代码里通过：
//
//	var _ document.Transformer = (*Transformer)(nil)
//
// 完成。
//
// 这里主要做最基础的行为测试。
func TestTransformerBasicSplit(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Splitter.ChunkSize = 30
	cfg.Splitter.ChunkOverlap = 0
	cfg.Splitter.Strategy = corechunker.StrategyLegacy
	cfg.Splitter.Separators = []string{"\n\n", "。"}

	transformer := NewTransformer(cfg)

	source := &schema.Document{
		ID: "doc-001",
		Content: "第一段内容比较长，用来测试 Eino Transformer。这里继续增加一些文字。\n\n" +
			"第二段内容也比较长，这里继续测试分块能力。\n\n" +
			"第三段作为最后一部分。",
		MetaData: map[string]any{
			"file_name": "manual.md",
			"source":    "/data/manual.md",
		},
	}

	docs, err := transformer.Transform(context.Background(), []*schema.Document{source})
	if err != nil {
		t.Fatalf("Transform 不应该返回错误: %v", err)
	}

	if len(docs) < 2 {
		t.Fatalf("测试文档应该被切成多个 Chunk: got=%d", len(docs))
	}

	for i, doc := range docs {
		if doc == nil {
			t.Fatalf("Chunk Document[%d] 不应该为 nil", i)
		}

		if doc.Content == "" {
			t.Fatalf("Chunk Document[%d] Content 不应该为空", i)
		}

		if doc.ID == "" {
			t.Fatalf("Chunk Document[%d] ID 不应该为空", i)
		}
	}
}

// TestTransformerPreservesSourceMetadata
//
// Eino 明确要求 Transformer 保留已有 metadata。
//
// 因此 Loader / Parser 写入的：
//
//	file_name
//	source
//	mime_type
//
// 等字段经过 Chunk Transformer 后不能消失。
func TestTransformerPreservesSourceMetadata(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Splitter.Strategy = corechunker.StrategyLegacy

	transformer := NewTransformer(cfg)

	source := &schema.Document{
		ID:      "doc-001",
		Content: "这是一段普通文档。",
		MetaData: map[string]any{
			"file_name": "demo.md",
			"mime_type": "text/markdown",
			"custom":    "keep-me",
		},
	}

	docs, err := transformer.Transform(context.Background(), []*schema.Document{source})
	if err != nil {
		t.Fatal(err)
	}

	if len(docs) != 1 {
		t.Fatalf("短文档应该产生一个 Chunk: got=%d", len(docs))
	}

	metadata := docs[0].MetaData

	if metadata["file_name"] != "demo.md" {
		t.Fatalf("file_name metadata 丢失: %v", metadata)
	}

	if metadata["mime_type"] != "text/markdown" {
		t.Fatalf("mime_type metadata 丢失: %v", metadata)
	}

	if metadata["custom"] != "keep-me" {
		t.Fatalf("custom metadata 丢失: %v", metadata)
	}
}

// TestTransformerDoesNotMutateSourceMetadata
//
// 这是 Adapter 中非常重要的测试。
//
// 输出 Chunk 新增：
//
//	rag_chunk_index
//
// 绝对不能同时污染原 source.MetaData。
func TestTransformerDoesNotMutateSourceMetadata(t *testing.T) {
	cfg := DefaultConfig()
	transformer := NewTransformer(cfg)

	source := &schema.Document{
		ID:      "doc-001",
		Content: "这是一段普通文档。",
		MetaData: map[string]any{
			"origin": "loader",
		},
	}

	docs, err := transformer.Transform(context.Background(), []*schema.Document{source})
	if err != nil {
		t.Fatal(err)
	}

	if len(docs) != 1 {
		t.Fatalf("预期一个 Chunk: got=%d", len(docs))
	}

	if _, exists := source.MetaData[retrieval.MetaChunkIndex]; exists {
		t.Fatal("Transformer 不应该修改 source.MetaData")
	}

	if source.MetaData["origin"] != "loader" {
		t.Fatal("原 source metadata 被修改")
	}
}

// TestTransformerChunksDoNotShareMetadataMap
//
// 不同 Chunk 不能共享同一张 MetaData map。
//
// 否则：
//
//	修改 Chunk0 metadata
//
// 会同时改变：
//
//	Chunk1 metadata
func TestTransformerChunksDoNotShareMetadataMap(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Splitter.ChunkSize = 20
	cfg.Splitter.ChunkOverlap = 0
	cfg.Splitter.Strategy = corechunker.StrategyLegacy
	cfg.Splitter.Separators = []string{"。"}

	transformer := NewTransformer(cfg)

	source := &schema.Document{
		ID:      "doc",
		Content: strings.Repeat("这是一句话。", 20),
		MetaData: map[string]any{
			"source": "test",
		},
	}

	docs, err := transformer.Transform(context.Background(), []*schema.Document{source})
	if err != nil {
		t.Fatal(err)
	}

	if len(docs) < 2 {
		t.Fatalf("测试至少需要两个 Chunk: got=%d", len(docs))
	}

	docs[0].MetaData["only_chunk_zero"] = true

	if _, exists := docs[1].MetaData["only_chunk_zero"]; exists {
		t.Fatal("不同 Chunk 不应该共享 MetaData map")
	}
}

// TestTransformerAddsChunkMetadata
//
// 固定 Core Chunk → Eino Document 的字段映射。
func TestTransformerAddsChunkMetadata(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Splitter.Strategy = corechunker.StrategyLegacy

	transformer := NewTransformer(cfg)

	source := &schema.Document{
		ID:      "document-42",
		Content: "中文测试内容。",
	}

	docs, err := transformer.Transform(context.Background(), []*schema.Document{source})
	if err != nil {
		t.Fatal(err)
	}

	if len(docs) != 1 {
		t.Fatalf("预期一个 Chunk: got=%d", len(docs))
	}

	doc := docs[0]

	if doc.MetaData[retrieval.MetaSourceDocumentID] != "document-42" {
		t.Fatalf(
			"Source Document ID 错误: want=%q got=%v",
			"document-42",
			doc.MetaData[retrieval.MetaSourceDocumentID],
		)
	}

	if doc.MetaData[retrieval.MetaSourceDocumentIndex] != 0 {
		t.Fatalf(
			"Source Document Index 错误: want=0 got=%v",
			doc.MetaData[retrieval.MetaSourceDocumentIndex],
		)
	}

	if doc.MetaData[retrieval.MetaChunkIndex] != 0 {
		t.Fatalf(
			"Chunk Index 错误: want=0 got=%v",
			doc.MetaData[retrieval.MetaChunkIndex],
		)
	}

	if doc.MetaData[retrieval.MetaChunkStart] != 0 {
		t.Fatalf(
			"Chunk Start 错误: want=0 got=%v",
			doc.MetaData[retrieval.MetaChunkStart],
		)
	}

	if doc.MetaData[retrieval.MetaChunkEnd] != corechunker.RuneLen(source.Content) {
		t.Fatalf(
			"Chunk End 错误: want=%d got=%v",
			corechunker.RuneLen(source.Content),
			doc.MetaData[retrieval.MetaChunkEnd],
		)
	}
}

// TestTransformerPreservesRuneOffsets
//
// 中文 UTF-8 一个汉字通常占多个 byte。
//
// Transformer metadata 中的 Start / End
// 必须继续保持 Core 的 rune offset，不能变成 byte offset。
func TestTransformerPreservesRuneOffsets(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Splitter.ChunkSize = 8
	cfg.Splitter.ChunkOverlap = 0
	cfg.Splitter.Strategy = corechunker.StrategyLegacy
	cfg.Splitter.Separators = []string{"\n\n"}

	transformer := NewTransformer(cfg)

	source := &schema.Document{
		ID: "cn",
		Content: "第一部分。\n\n" +
			"第二部分。",
	}

	docs, err := transformer.Transform(context.Background(), []*schema.Document{source})
	if err != nil {
		t.Fatal(err)
	}

	sourceRunes := []rune(source.Content)

	for i, doc := range docs {
		start, ok := doc.MetaData[retrieval.MetaChunkStart].(int)
		if !ok {
			t.Fatalf("Chunk[%d] start 不是 int: %T", i, doc.MetaData[retrieval.MetaChunkStart])
		}

		end, ok := doc.MetaData[retrieval.MetaChunkEnd].(int)
		if !ok {
			t.Fatalf("Chunk[%d] end 不是 int: %T", i, doc.MetaData[retrieval.MetaChunkEnd])
		}

		if start < 0 || end < start || end > len(sourceRunes) {
			t.Fatalf("Chunk[%d] rune range 非法: [%d,%d)", i, start, end)
		}

		if string(sourceRunes[start:end]) != doc.Content {
			t.Fatalf(
				"Chunk[%d] rune offset 与 Content 不一致\nsource=%q\nchunk =%q",
				i,
				string(sourceRunes[start:end]),
				doc.Content,
			)
		}
	}
}

func TestTransformerMapsContextHeader(t *testing.T) {
	body := strings.Repeat("这里是用于增加章节长度的正文。", 15)

	source := &schema.Document{
		ID: "heading-doc",
		Content: "# 产品手册\n" +
			"## 概述\n" + body + "\n" +
			"## 安装\n" + body + "\n" +
			"## 配置\n" + body + "\n" +
			"## 部署\n" + body,
	}

	cfg := DefaultConfig()
	cfg.Splitter.Strategy = corechunker.StrategyHeading
	cfg.Splitter.ChunkSize = 300
	cfg.Splitter.ChunkOverlap = 20

	transformer := NewTransformer(cfg)

	docs, err := transformer.Transform(context.Background(), []*schema.Document{source})
	if err != nil {
		t.Fatal(err)
	}

	if len(docs) == 0 {
		t.Fatal("Heading Transformer 不应该返回空结果")
	}

	foundInstall := false

	for _, doc := range docs {
		header, ok := doc.MetaData[retrieval.MetaContextHeader].(string)
		if !ok {
			t.Fatalf(
				"ContextHeader metadata 类型错误: got=%T value=%v",
				doc.MetaData[retrieval.MetaContextHeader],
				doc.MetaData[retrieval.MetaContextHeader],
			)
		}

		// 我们验证的是：
		//
		//     包含“安装 Section”的 Chunk
		//
		// 是否正确把 Heading Breadcrumb
		// 映射到了 Eino MetaData。
		if !strings.Contains(doc.Content, "## 安装") {
			continue
		}

		foundInstall = true

		if !strings.Contains(header, "# 产品手册") {
			t.Fatalf(
				"安装 Chunk 的 ContextHeader 缺少 H1\nheader=%q\ncontent=%q",
				header,
				doc.Content,
			)
		}

		if !strings.Contains(header, "## 安装") {
			t.Fatalf(
				"安装 Chunk 的 ContextHeader 缺少 H2\nheader=%q\ncontent=%q",
				header,
				doc.Content,
			)
		}
	}

	if !foundInstall {
		t.Fatal("没有找到包含 ## 安装 的 Chunk")
	}
}

// TestTransformerDoesNotPutEmbeddingContentIntoContent
//
// 我们刻意保持：
//
//	Document.Content == Chunk.Content
//
// 而不是：
//
//	ContextHeader + Chunk.Content
//
// 否则业务原文和检索表示会被混在一起。
func TestTransformerDoesNotPutEmbeddingContentIntoContent(t *testing.T) {
	body := strings.Repeat("这里是正文。", 30)

	source := &schema.Document{
		ID: "doc",
		Content: "# 文档\n" +
			"## 第一节\n" + body + "\n" +
			"## 第二节\n" + body + "\n" +
			"## 第三节\n" + body,
	}

	cfg := DefaultConfig()
	cfg.Splitter.Strategy = corechunker.StrategyHeading
	cfg.Splitter.ChunkSize = 250

	transformer := NewTransformer(cfg)

	docs, err := transformer.Transform(context.Background(), []*schema.Document{source})
	if err != nil {
		t.Fatal(err)
	}

	for _, doc := range docs {
		header, _ := doc.MetaData[retrieval.MetaContextHeader].(string)

		if header == "" {
			continue
		}

		// 如果 Content 被错误改成：
		//
		//     header + "\n\n" + body
		//
		// 那 Content 很可能直接以前面的 ContextHeader 开始。
		//
		// 当前 Adapter 不允许这样做。
		if strings.HasPrefix(doc.Content, header+"\n\n") {
			t.Fatalf(
				"Document.Content 不应该被替换成 EmbeddingContent\nheader=%q\ncontent=%q",
				header,
				doc.Content,
			)
		}
	}
}

// TestTransformerDefaultIDGenerator
//
// 固定默认 ID 行为。
func TestTransformerDefaultIDGenerator(t *testing.T) {
	source := &schema.Document{
		ID:      "doc-abc",
		Content: "普通内容。",
	}

	cfg := DefaultConfig()
	transformer := NewTransformer(cfg)

	docs, err := transformer.Transform(context.Background(), []*schema.Document{source})
	if err != nil {
		t.Fatal(err)
	}

	if len(docs) != 1 {
		t.Fatalf("预期一个 Chunk: got=%d", len(docs))
	}

	want := "doc-abc#chunk-000000"

	if docs[0].ID != want {
		t.Fatalf("默认 Chunk ID 错误: want=%q got=%q", want, docs[0].ID)
	}
}

// TestTransformerDefaultIDGeneratorWithoutSourceID
//
// Loader 没给 Document.ID 时，
// 仍然需要产生唯一的批次内 Chunk ID。
func TestTransformerDefaultIDGeneratorWithoutSourceID(t *testing.T) {
	cfg := DefaultConfig()
	transformer := NewTransformer(cfg)

	src := []*schema.Document{
		{Content: "第一个文档。"},
		{Content: "第二个文档。"},
	}

	docs, err := transformer.Transform(context.Background(), src)
	if err != nil {
		t.Fatal(err)
	}

	if len(docs) != 2 {
		t.Fatalf("预期两个结果: got=%d", len(docs))
	}

	if docs[0].ID == docs[1].ID {
		t.Fatalf("无 Source ID 时不同文档的 Chunk ID 仍应该不同: %q", docs[0].ID)
	}

	if docs[0].ID != "source-000000#chunk-000000" {
		t.Fatalf("第一个 fallback ID 异常: %q", docs[0].ID)
	}

	if docs[1].ID != "source-000001#chunk-000000" {
		t.Fatalf("第二个 fallback ID 异常: %q", docs[1].ID)
	}
}

// TestTransformerCustomIDGenerator
//
// 验证创建时自定义 ID Generator。
func TestTransformerCustomIDGenerator(t *testing.T) {
	cfg := DefaultConfig()

	cfg.IDGenerator = func(
		source *schema.Document,
		sourceIndex int,
		chunk corechunker.Chunk,
	) string {
		return "custom-id"
	}

	transformer := NewTransformer(cfg)

	docs, err := transformer.Transform(
		context.Background(),
		[]*schema.Document{
			{
				ID:      "source",
				Content: "内容。",
			},
		},
	)

	if err != nil {
		t.Fatal(err)
	}

	if docs[0].ID != "custom-id" {
		t.Fatalf("Custom IDGenerator 没有生效: %q", docs[0].ID)
	}
}

// TestTransformerCallOptionOverridesSplitterConfig
//
// 验证 Eino TransformerOption 真正生效。
//
// Transformer 默认一个大 ChunkSize，
// 本次调用临时改成很小的 ChunkSize，
// 应该得到更多 Chunk。
func TestTransformerCallOptionOverridesSplitterConfig(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Splitter.ChunkSize = 1000
	cfg.Splitter.Strategy = corechunker.StrategyLegacy

	transformer := NewTransformer(cfg)

	source := &schema.Document{
		Content: strings.Repeat("这是一句话。", 50),
	}

	defaultDocs, err := transformer.Transform(
		context.Background(),
		[]*schema.Document{source},
	)

	if err != nil {
		t.Fatal(err)
	}

	override := cfg.Splitter
	override.ChunkSize = 30
	override.ChunkOverlap = 0
	override.Separators = []string{"。"}

	overrideDocs, err := transformer.Transform(
		context.Background(),
		[]*schema.Document{source},
		WithSplitterConfig(override),
	)

	if err != nil {
		t.Fatal(err)
	}

	if len(overrideDocs) <= len(defaultDocs) {
		t.Fatalf(
			"调用级 SplitterConfig 应产生更多 Chunk: default=%d override=%d",
			len(defaultDocs),
			len(overrideDocs),
		)
	}
}

// TestTransformerCallOptionOverridesIDGenerator
//
// 验证调用级 ID Generator 覆盖。
func TestTransformerCallOptionOverridesIDGenerator(t *testing.T) {
	transformer := NewTransformer(DefaultConfig())

	source := &schema.Document{
		ID:      "original",
		Content: "正文。",
	}

	docs, err := transformer.Transform(
		context.Background(),
		[]*schema.Document{source},
		WithIDGenerator(func(
			source *schema.Document,
			sourceIndex int,
			chunk corechunker.Chunk,
		) string {
			return "call-level-id"
		}),
	)

	if err != nil {
		t.Fatal(err)
	}

	if docs[0].ID != "call-level-id" {
		t.Fatalf("调用级 IDGenerator 没有生效: %q", docs[0].ID)
	}
}

// TestTransformerSkipsNilAndEmptyDocuments
//
// 一个 batch 中的 nil / empty source
// 不应该导致整个 pipeline panic。
func TestTransformerSkipsNilAndEmptyDocuments(t *testing.T) {
	transformer := NewTransformer(DefaultConfig())

	src := []*schema.Document{
		nil,
		{ID: "empty", Content: ""},
		{ID: "valid", Content: "有效正文。"},
	}

	docs, err := transformer.Transform(context.Background(), src)
	if err != nil {
		t.Fatal(err)
	}

	if len(docs) != 1 {
		t.Fatalf("应该只输出有效 Document 的 Chunk: got=%d", len(docs))
	}

	if docs[0].MetaData[retrieval.MetaSourceDocumentID] != "valid" {
		t.Fatalf("输出来源错误: %v", docs[0].MetaData)
	}
}

// TestTransformerRespectsCancelledContext
//
// Transformer 应尊重 Eino Graph / Workflow 的 context cancellation。
func TestTransformerRespectsCancelledContext(t *testing.T) {
	transformer := NewTransformer(DefaultConfig())

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := transformer.Transform(
		ctx,
		[]*schema.Document{
			{
				Content: "正文。",
			},
		},
	)

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("应该返回 context.Canceled: got=%v", err)
	}
}

// TestTransformerProcessesMultipleSourceDocumentsInOrder
//
// Transformer 的输出顺序应该保持：
//
//	source 0 的所有 Chunk
//	source 1 的所有 Chunk
//	source 2 的所有 Chunk
//
// 不在 Adapter 中并发重排。
//
// 这对 Debug、确定性测试和批量入库映射都更友好。
func TestTransformerProcessesMultipleSourceDocumentsInOrder(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Splitter.Strategy = corechunker.StrategyLegacy

	transformer := NewTransformer(cfg)

	src := []*schema.Document{
		{
			ID:      "a",
			Content: "A文档。",
		},
		{
			ID:      "b",
			Content: "B文档。",
		},
	}

	docs, err := transformer.Transform(context.Background(), src)
	if err != nil {
		t.Fatal(err)
	}

	if len(docs) != 2 {
		t.Fatalf("预期两个 Chunk: got=%d", len(docs))
	}

	if docs[0].MetaData[retrieval.MetaSourceDocumentID] != "a" {
		t.Fatalf("第一个输出应来自 source a: %v", docs[0].MetaData)
	}

	if docs[1].MetaData[retrieval.MetaSourceDocumentID] != "b" {
		t.Fatalf("第二个输出应来自 source b: %v", docs[1].MetaData)
	}
}

func TestTransformerContextHeaderReflectsHeadingCoalesce(t *testing.T) {
	body := strings.Repeat("这里是用于增加章节长度的正文。", 15)

	source := &schema.Document{
		ID: "heading-doc",
		Content: "# 产品手册\n" +
			"## 安装\n" + body + "\n" +
			"## 配置\n" + body + "\n" +
			"## 部署\n" + body,
	}

	cfg := DefaultConfig()
	cfg.Splitter.Strategy = corechunker.StrategyHeading
	cfg.Splitter.ChunkSize = 300
	cfg.Splitter.ChunkOverlap = 20

	transformer := NewTransformer(cfg)

	docs, err := transformer.Transform(context.Background(), []*schema.Document{source})
	if err != nil {
		t.Fatal(err)
	}

	for _, doc := range docs {
		if !strings.Contains(doc.Content, "## 安装") {
			continue
		}

		header, _ := doc.MetaData[retrieval.MetaContextHeader].(string)

		// H1 小块和“安装”块发生 Tiny Coalesce 后，
		// 两者共同有效的 Heading 只有：
		//
		//     # 产品手册
		//
		// 因此这里不应该错误地强制要求：
		//
		//     ## 安装
		if !strings.Contains(header, "# 产品手册") {
			t.Fatalf("合并后的 Chunk 应保留共同 H1: %q", header)
		}

		return
	}

	t.Fatal("没有找到包含安装章节的 Chunk")
}
