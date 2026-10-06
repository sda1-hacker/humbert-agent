package tabula

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudwego/eino/components/document"
	"github.com/sda1-hacker/humbert-agent/internal/rag/chunker"
	tabulalib "github.com/tsawler/tabula"
)

// TestSupportedExtension
//
// 固定当前 Loader 支持格式。
func TestSupportedExtension(t *testing.T) {
	supported := []string{
		"pdf",
		".PDF",
		"docx",
		"odt",
		"xlsx",
		"pptx",
		"html",
		"htm",
		"epub",
		"txt",
		"md",
		"csv",
	}

	for _, ext := range supported {
		if !SupportedExtension(ext) {
			t.Fatalf("应该支持扩展名: %q", ext)
		}
	}

	unsupported := []string{
		"",
		"jpg",
		"png",
		"zip",
	}

	for _, ext := range unsupported {
		if SupportedExtension(ext) {
			t.Fatalf("当前不应该支持扩展名: %q", ext)
		}
	}
}

// TestLoaderHTMLToMarkdown
//
// HTML 是非常适合 Loader 单元测试的格式：
//
//	不需要构造复杂 PDF 二进制
//	不需要 Office ZIP fixture
//	不需要 OCR
//
// 但它仍然完整经过：
//
//	tabula.Open()
//	    ↓
//	ToMarkdown()
//
// 所以能够验证我们和真实 Tabula API 的集成。
func TestLoaderHTMLToMarkdown(t *testing.T) {
	path := writeTempHTML(t, "manual.html", `
<!doctype html>
<html>
<head>
	<title>Example</title>
</head>
<body>
	<h1>产品手册</h1>
	<p>这是第一段正文。</p>

	<h2>安装</h2>
	<p>执行安装命令。</p>
</body>
</html>
`)

	loader := NewLoader(DefaultConfig())

	docs, err := loader.Load(
		context.Background(),
		document.Source{URI: path},
	)

	if err != nil {
		t.Fatalf("Load HTML 失败: %v", err)
	}

	if len(docs) != 1 {
		t.Fatalf("一个文件应该产生一个 Markdown Document: got=%d", len(docs))
	}

	doc := docs[0]

	if doc.Content == "" {
		t.Fatal("Markdown Content 不应该为空")
	}

	if !strings.Contains(doc.Content, "产品手册") {
		t.Fatalf("Markdown 应包含 H1 内容:\n%s", doc.Content)
	}

	if !strings.Contains(doc.Content, "安装") {
		t.Fatalf("Markdown 应包含 H2 内容:\n%s", doc.Content)
	}

	if !strings.Contains(doc.Content, "执行安装命令") {
		t.Fatalf("Markdown 丢失正文:\n%s", doc.Content)
	}

	if doc.ID == "" {
		t.Fatal("Loader 应生成 source Document ID")
	}
}

// TestLoaderMetadata
//
// 验证文件级 provenance metadata。
func TestLoaderMetadata(t *testing.T) {
	path := writeTempHTML(t, "employee-handbook.html", `
<html>
<body>
	<h1>员工手册</h1>
	<p>正文内容。</p>
</body>
</html>
`)

	loader := NewLoader(DefaultConfig())

	docs, err := loader.Load(
		context.Background(),
		document.Source{URI: path},
	)

	if err != nil {
		t.Fatal(err)
	}

	if len(docs) != 1 {
		t.Fatalf("预期一个 Document: got=%d", len(docs))
	}

	metadata := docs[0].MetaData

	if metadata[MetaSourceURI] != path {
		t.Fatalf(
			"Source URI 错误: want=%q got=%v",
			path,
			metadata[MetaSourceURI],
		)
	}

	if metadata[MetaTitle] != "employee-handbook" {
		t.Fatalf(
			"Title 错误: want=%q got=%v",
			"employee-handbook",
			metadata[MetaTitle],
		)
	}

	if metadata[MetaFileName] != "employee-handbook.html" {
		t.Fatalf(
			"FileName 错误: got=%v",
			metadata[MetaFileName],
		)
	}

	if metadata[MetaFileExt] != "html" {
		t.Fatalf(
			"FileExt 错误: want=html got=%v",
			metadata[MetaFileExt],
		)
	}

	if metadata[MetaParser] != parserName {
		t.Fatalf(
			"Parser metadata 错误: want=%q got=%v",
			parserName,
			metadata[MetaParser],
		)
	}

	size, ok := metadata[MetaFileSize].(int64)
	if !ok || size <= 0 {
		t.Fatalf(
			"FileSize metadata 异常: type=%T value=%v",
			metadata[MetaFileSize],
			metadata[MetaFileSize],
		)
	}

	ocrUsed, ok := metadata[MetaOCRUsed].(bool)
	if !ok {
		t.Fatalf(
			"OCRUsed 应该是 bool: type=%T",
			metadata[MetaOCRUsed],
		)
	}

	if ocrUsed {
		t.Fatal("普通 HTML 不应该标记 OCRUsed=true")
	}
}

// TestLoaderDefaultIDIsDeterministic
//
// 同一个 Source.URI 多次加载应该得到相同 ID。
func TestLoaderDefaultIDIsDeterministic(t *testing.T) {
	path := writeTempHTML(t, "same.html", `
<html>
<body>
	<p>稳定 ID 测试。</p>
</body>
</html>
`)

	loader := NewLoader(DefaultConfig())
	source := document.Source{URI: path}

	first, err := loader.Load(context.Background(), source)
	if err != nil {
		t.Fatal(err)
	}

	second, err := loader.Load(context.Background(), source)
	if err != nil {
		t.Fatal(err)
	}

	if first[0].ID == "" {
		t.Fatal("默认 ID 不应该为空")
	}

	if first[0].ID != second[0].ID {
		t.Fatalf(
			"同一 Source 应产生稳定 ID: first=%q second=%q",
			first[0].ID,
			second[0].ID,
		)
	}
}

// TestLoaderDifferentSourcesProduceDifferentIDs
//
// 文件名即使相同，只要 URI 不同，也不应该产生相同 ID。
func TestLoaderDifferentSourcesProduceDifferentIDs(t *testing.T) {
	dir := t.TempDir()

	firstDir := filepath.Join(dir, "a")
	secondDir := filepath.Join(dir, "b")

	if err := os.MkdirAll(firstDir, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.MkdirAll(secondDir, 0o755); err != nil {
		t.Fatal(err)
	}

	firstPath := filepath.Join(firstDir, "manual.html")
	secondPath := filepath.Join(secondDir, "manual.html")

	content := []byte(`<html><body><p>正文。</p></body></html>`)

	if err := os.WriteFile(firstPath, content, 0o600); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(secondPath, content, 0o600); err != nil {
		t.Fatal(err)
	}

	firstID := DefaultIDGenerator(document.Source{URI: firstPath})
	secondID := DefaultIDGenerator(document.Source{URI: secondPath})

	if firstID == secondID {
		t.Fatalf(
			"不同 URI 不应该产生相同 ID: %q",
			firstID,
		)
	}
}

// TestLoaderCustomIDGenerator
//
// 验证业务可以自己控制 Source Document ID。
func TestLoaderCustomIDGenerator(t *testing.T) {
	path := writeTempHTML(t, "custom.html", `
<html>
<body>
	<p>Custom ID。</p>
</body>
</html>
`)

	cfg := DefaultConfig()

	cfg.IDGenerator = func(src document.Source) string {
		return "business-document-id"
	}

	loader := NewLoader(cfg)

	docs, err := loader.Load(
		context.Background(),
		document.Source{URI: path},
	)

	if err != nil {
		t.Fatal(err)
	}

	if docs[0].ID != "business-document-id" {
		t.Fatalf(
			"Custom IDGenerator 没有生效: got=%q",
			docs[0].ID,
		)
	}
}

// TestLoaderRejectsEmptySource
func TestLoaderRejectsEmptySource(t *testing.T) {
	loader := NewLoader(DefaultConfig())

	_, err := loader.Load(
		context.Background(),
		document.Source{},
	)

	if !errors.Is(err, ErrEmptySourceURI) {
		t.Fatalf(
			"空 Source 应返回 ErrEmptySourceURI: got=%v",
			err,
		)
	}
}

// TestLoaderRejectsRemoteSource
//
// 当前 Tabula Loader 明确只处理 local file。
func TestLoaderRejectsRemoteSource(t *testing.T) {
	loader := NewLoader(DefaultConfig())

	_, err := loader.Load(
		context.Background(),
		document.Source{
			URI: "https://example.com/manual.pdf",
		},
	)

	if !errors.Is(err, ErrRemoteSourceUnsupported) {
		t.Fatalf(
			"远程 URL 应返回 ErrRemoteSourceUnsupported: got=%v",
			err,
		)
	}
}

// TestLoaderRejectsUnsupportedFormat
//
// 当前不直接支持 txt / md。
//
// 原因不是 Tabula Loader 不能读文本，
// 而是这一 Adapter 的职责是封装 Tabula 当前声明的文档格式。
//
// 纯文本/Markdown 未来可以直接走一个非常简单的 Eino File Loader，
// 没必要绕一遍 Tabula。
func TestLoaderRejectsUnsupportedFormat(t *testing.T) {
	path := filepath.Join(t.TempDir(), "demo.jpg")

	if err := os.WriteFile(path, []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}

	loader := NewLoader(DefaultConfig())

	_, err := loader.Load(
		context.Background(),
		document.Source{URI: path},
	)

	if !errors.Is(err, ErrUnsupportedFormat) {
		t.Fatalf(
			"jpg 应返回 ErrUnsupportedFormat: got=%v",
			err,
		)
	}
}

// TestLoaderRejectsDirectory
func TestLoaderRejectsDirectory(t *testing.T) {
	loader := NewLoader(DefaultConfig())

	_, err := loader.Load(
		context.Background(),
		document.Source{URI: t.TempDir()},
	)

	if !errors.Is(err, ErrSourceIsDirectory) {
		t.Fatalf(
			"目录应返回 ErrSourceIsDirectory: got=%v",
			err,
		)
	}
}

// TestLoaderMissingFile
//
// 不存在的文件应该保留 os.ErrNotExist 错误链，
// 方便上层使用 errors.Is 判断。
func TestLoaderMissingFile(t *testing.T) {
	path := filepath.Join(
		t.TempDir(),
		"does-not-exist.pdf",
	)

	loader := NewLoader(DefaultConfig())

	_, err := loader.Load(
		context.Background(),
		document.Source{URI: path},
	)

	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf(
			"不存在文件应该保留 os.ErrNotExist: got=%v",
			err,
		)
	}
}

// TestLoaderRespectsCancelledContext
//
// 如果调用开始之前 Context 已取消，
// 不应该再执行文件解析。
func TestLoaderRespectsCancelledContext(t *testing.T) {
	path := writeTempHTML(t, "cancelled.html", `
<html>
<body>
	<p>不应该解析到这里。</p>
</body>
</html>
`)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	loader := NewLoader(DefaultConfig())

	_, err := loader.Load(
		ctx,
		document.Source{URI: path},
	)

	if !errors.Is(err, context.Canceled) {
		t.Fatalf(
			"取消 Context 应返回 context.Canceled: got=%v",
			err,
		)
	}
}

// TestNormalizeLineEndings
//
// Loader 输出进入 Chunker 前应该拥有统一 LF 换行。
func TestNormalizeLineEndings(t *testing.T) {
	input := "line1\r\nline2\rline3\n"

	got := chunker.NormalizeLineEndings(input)
	want := "line1\nline2\nline3\n"

	if got != want {
		t.Fatalf(
			"换行归一化错误\nwant=%q\ngot =%q",
			want,
			got,
		)
	}
}

// TestWarningMetadata
//
// Tabula Warning 是“非致命问题”，
// Loader 需要把 message 保存下来，
// 同时单独识别 OCR fallback。
func TestWarningMetadata(t *testing.T) {
	warnings := []tabulalib.Warning{
		{
			Code:    tabulalib.WarningOCRFallback,
			Message: "OCR fallback was used",
		},
		{
			Code:    tabulalib.WarningMessyPDF,
			Message: "PDF layout is fragmented",
		},
	}

	messages, ocrUsed := warningMetadata(warnings)

	if !ocrUsed {
		t.Fatal("WarningOCRFallback 应设置 OCRUsed=true")
	}

	if len(messages) != 2 {
		t.Fatalf(
			"应该保存两个 Warning Message: got=%v",
			messages,
		)
	}

	if messages[0] != "OCR fallback was used" {
		t.Fatalf(
			"第一个 Warning Message 错误: %q",
			messages[0],
		)
	}

	if messages[1] != "PDF layout is fragmented" {
		t.Fatalf(
			"第二个 Warning Message 错误: %q",
			messages[1],
		)
	}
}

// TestLoaderOutputCanFeedChunkTransformerContract
//
// 这个测试不直接 import 我们的 Chunk Transformer，
// 避免 Loader package 与 Transformer package 发生测试层循环依赖。
//
// 这里只验证 Loader 输出满足后续 Transformer 所需要的基本 contract：
//
//	Content 非空
//	ID 非空
//	MetaData 非空
//	_source 存在
//
// 真正 Loader → Transformer Integration Test
// 后面放到 pipeline/integration package 会更合理。
func TestLoaderOutputCanFeedChunkTransformerContract(t *testing.T) {
	path := writeTempHTML(t, "pipeline.html", `
<html>
<body>
	<h1>RAG 文档</h1>
	<h2>安装</h2>
	<p>这里是安装说明。</p>
</body>
</html>
`)

	loader := NewLoader(DefaultConfig())

	docs, err := loader.Load(
		context.Background(),
		document.Source{URI: path},
	)

	if err != nil {
		t.Fatal(err)
	}

	if len(docs) != 1 {
		t.Fatalf("预期一个 source Document: got=%d", len(docs))
	}

	doc := docs[0]

	if doc.ID == "" {
		t.Fatal("Source Document.ID 不应该为空")
	}

	if strings.TrimSpace(doc.Content) == "" {
		t.Fatal("Source Document.Content 不应该为空")
	}

	if doc.MetaData == nil {
		t.Fatal("Source Document.MetaData 不应该为空")
	}

	if doc.MetaData[MetaSourceURI] != path {
		t.Fatalf(
			"下游必须可以追踪 Source URI: %v",
			doc.MetaData,
		)
	}
}

// writeTempHTML 是测试辅助函数。
//
// 每个测试自己创建临时文件，
// 不依赖仓库 testdata，
// 这样当前测试可以直接运行。
func writeTempHTML(t *testing.T, name, content string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), name)

	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("写测试 HTML 失败: %v", err)
	}

	return path
}
