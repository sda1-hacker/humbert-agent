package tabula

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cloudwego/eino/components/document"
	"github.com/cloudwego/eino/schema"
	"github.com/sda1-hacker/humbert-agent/internal/documentparse"
	tabulalib "github.com/tsawler/tabula"
)

// 编译期检查。
//
// 如果未来 Eino 修改了 document.Loader 接口，
// 编译器会立即告诉我们当前实现已经不兼容。
var _ document.Loader = (*Loader)(nil)

// -----------------------------------------------------------------------------
// Errors
// -----------------------------------------------------------------------------

var (
	// ErrEmptySourceURI 表示没有提供任何文件路径。
	ErrEmptySourceURI = errors.New("tabula loader: empty source URI")

	// ErrRemoteSourceUnsupported 表示当前 Loader 暂时只负责本地文件。
	//
	// Eino 的 document.Source.URI 既允许：
	//
	//     local path
	//
	// 也允许：
	//
	//     http://...
	//     https://...
	//
	// 但 Tabula 当前核心入口：
	//
	//     tabula.Open(filename)
	//
	// 是文件路径 API。
	//
	// 我们不在 Loader 里偷偷加入 HTTP 下载逻辑，
	// 因为：
	//
	//     下载
	//     鉴权
	//     重试
	//     超时
	//     临时文件管理
	//
	// 都属于另一层职责。
	//
	// 以后如果需要 URL：
	//
	//     Remote Source
	//         ↓
	//     Downloader / Object Storage Adapter
	//         ↓
	//     Local Temp File
	//         ↓
	//     Tabula Loader
	ErrRemoteSourceUnsupported = errors.New("tabula loader: remote source is not supported")

	// ErrUnsupportedFormat 表示扩展名不属于当前 Tabula 支持格式。
	ErrUnsupportedFormat = errors.New("tabula loader: unsupported file format")

	// ErrSourceIsDirectory 表示 Source.URI 指向目录，而不是普通文档文件。
	ErrSourceIsDirectory = errors.New("tabula loader: source is a directory")

	// ErrEmptyContent 表示 Tabula 成功执行了解析流程，
	// 但没有得到任何有效 Markdown。
	//
	// 这个错误特别有价值。
	//
	// 例如扫描 PDF：
	//
	//     没有 native text
	//     +
	//     当前程序没有使用 -tags ocr 构建
	//
	// 就可能最终得到空内容。
	//
	// 我们不希望它继续进入：
	//
	//     Chunker
	//     Embedder
	//     Database
	//
	// 然后静默产生“0 个知识块”。
	//
	// 将它作为明确错误返回后，
	// 后面的 Ingestion Service 就可以决定：
	//
	//     fallback AnyDoc
	//     标记 parse_failed
	//     提示需要 OCR
	ErrEmptyContent = errors.New("tabula loader: extracted markdown is empty")
)

// -----------------------------------------------------------------------------
// Metadata Keys
// -----------------------------------------------------------------------------
//
// 这里分成两类：
//
// Eino 常见 provenance metadata:
//
//     _source
//     _title
//
// 我们自己的解析信息:
//
//     rag_parser
//     rag_parser_warnings
//     rag_ocr_used
//
// 另外再保存基础文件信息，方便未来 PGIndexer 使用。

const (
	// MetaSourceURI 保存 Eino Source.URI。
	//
	// Eino 自身的 Parser/Loader 生态中也常使用 "_source"
	// 表示来源 URI。
	MetaSourceURI = "_source"

	// MetaTitle 是当前文件的默认标题。
	//
	// 例如：
	//
	//     /data/员工手册.pdf
	//
	// 得到：
	//
	//     员工手册
	//
	// 后面真正业务层如果有自己的 Document.Title，
	// 可以在进入 Loader 前后覆盖这个值。
	MetaTitle = "_title"

	// MetaFileName 保存完整文件名，包括扩展名。
	//
	//     员工手册.pdf
	MetaFileName = "file_name"

	// MetaFileExt 保存不带 "." 的小写扩展名。
	//
	//     pdf
	//     docx
	//     html
	MetaFileExt = "file_ext"

	// MetaFileSize 保存文件字节数。
	MetaFileSize = "file_size"

	// MetaParser 表示当前文档由哪个 Parser Adapter 产生。
	MetaParser = "rag_parser"

	// MetaParserWarnings 保存 Tabula 返回的非致命 warning message。
	//
	// warning 不意味着解析失败。
	//
	// 例如：
	//
	//     使用 OCR fallback
	//     PDF 布局比较 messy
	//
	// 都可能在成功得到 Markdown 的同时产生 warning。
	MetaParserWarnings = "rag_parser_warnings"

	// MetaOCRUsed 表示 Tabula 是否报告：
	//
	//     WarningOCRFallback
	//
	// 这不是我们主动猜测 PDF 是不是扫描件，
	// 而是基于 Tabula 自己的 extraction warning。
	MetaOCRUsed = "rag_ocr_used"
)

const parserName = "tabula"

// supportedExtensions 是当前 Loader 明确接受的文件扩展名。
//
// 这些格式与当前 Tabula README 声明的 ToMarkdown 支持范围保持一致。
var supportedExtensions = map[string]struct{}{
	".pdf":  {},
	".docx": {},
	".odt":  {},
	".xlsx": {},
	".pptx": {},
	".html": {},
	".htm":  {},
	".epub": {},
	".md":   {},
	".txt":  {},
	".csv":  {},
}

// IDGenerator 负责为 Loader 输出的 source Document 生成 ID。
//
// 当前一个文件只产生一个 Markdown Document，
//
// 所以它的职责不是生成：
//
//	chunk ID
//
// 而是生成：
//
//	source document ID
//
// 后面的 Chunk Transformer 会进一步得到：
//
//	<source-id>#chunk-000001
type IDGenerator func(src document.Source) string

// Config 是 Tabula Loader 配置。
//
// 当前刻意只暴露 Loader 真正应该关心的配置，
// 不把 Tabula 全部 API 都机械映射出来。
//
// 后面如果某个真实业务场景确定需要：
//
//	Pages
//	ByColumn
//	PreserveLayout
//
// 再增加即可。
//
// 过早暴露几十个选项会让 Adapter 自己变成第二套 Tabula API。
type Config struct {
	ParseOptions documentparse.Options
	// ExcludeHeadersAndFooters 是否让 Tabula 尝试删除重复 Header/Footer。
	//
	// 对 RAG 来说通常应该打开。
	//
	// PDF 中重复出现：
	//
	//     公司内部资料
	//     Page 3 / 20
	//
	// 如果保留下来，
	// 很容易污染每一个 Chunk 和 BM25 索引。
	ExcludeHeadersAndFooters bool

	// OCRLanguage 设置 Tesseract 语言。
	//
	// 例如：
	//
	//     eng
	//     chi_sim
	//     eng+chi_sim
	//
	// 只有程序使用：
	//
	//     go build -tags ocr
	//
	// 或：
	//
	//     go test -tags ocr
	//
	// 时才真正生效。
	//
	// 空字符串表示使用 Tabula 默认 OCR 配置。
	OCRLanguage string

	// NormalizeLineEndings 是否把：
	//
	//     \r\n
	//     \r
	//
	// 统一成：
	//
	//     \n
	//
	// 我建议保持 true。
	//
	// 因为我们的 Chunker：
	//
	//     Heading
	//     Heuristic
	//     Recursive
	//
	// 都以统一 LF 文本作为最稳定的输入。
	NormalizeLineEndings bool

	// IDGenerator 控制 source Document.ID。
	IDGenerator IDGenerator
}

// DefaultConfig 返回推荐的 RAG Loader 默认配置。
func DefaultConfig() Config {
	return Config{
		ExcludeHeadersAndFooters: true,
		NormalizeLineEndings:     true,
		IDGenerator:              DefaultIDGenerator,
	}
}

// Loader 是 Eino document.Loader 的 Tabula 实现。
//
// 它只负责：
//
//	本地文件
//	    ↓
//	Tabula
//	    ↓
//	Markdown
//	    ↓
//	schema.Document
//
// 它不负责：
//
//	Chunking
//	Embedding
//	Persistence
//	Retrieval
//
// 这些能力继续由后面的独立组件负责。
type Loader struct {
	config Config
}

// NewLoader 创建一个 Tabula Loader。
func NewLoader(config Config) *Loader {
	if config.IDGenerator == nil {
		config.IDGenerator = DefaultIDGenerator
	}

	return &Loader{config: config}
}

// Load 实现 Eino document.Loader。
//
// 当前支持：
//
//	PDF
//	DOCX
//	ODT
//	XLSX
//	PPTX
//	HTML / HTM
//	EPUB
//
// -----------------------------------------------------------------------------
// 整体流程：
//
//	Source.URI
//	    ↓
//	validateSource
//	    ↓
//	tabula.Open()
//	    ↓
//	optional options
//	    ↓
//	ToMarkdown()
//	    ↓
//	warning metadata
//	    ↓
//	schema.Document
//
// -----------------------------------------------------------------------------
// opts 当前没有自定义 LoaderOption。
//
// 这里仍然保留接口参数，
// 因为必须满足 Eino document.Loader。
//
// 等未来确实有“调用级 Tabula 参数覆盖”需求时，
// 再使用 Eino LoaderOption 的 impl-specific option 机制。
// 现在不为了“可能以后需要”提前制造第二套配置层。
func (l *Loader) Load(
	ctx context.Context,
	src document.Source,
	_ ...document.LoaderOption,
) ([]*schema.Document, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	path, info, err := validateSource(src)
	if err != nil {
		return nil, err
	}

	opts := l.config.ParseOptions
	opts.ExcludeHeadersAndFooters = l.config.ExcludeHeadersAndFooters
	opts.OCRLanguage = l.config.OCRLanguage
	result, err := documentparse.ExtractFile(ctx, path, opts)
	markdown, warnings := result.Markdown, result.Warnings
	if err != nil {
		return nil, fmt.Errorf("tabula loader: extracting markdown from %q: %w", path, err)
	}

	// The disposable worker is killed on cancellation. Check again before
	// constructing output so a canceled graph cannot enter later stages.
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	if l.config.NormalizeLineEndings {
		markdown = normalizeLineEndings(markdown)
	}

	// 解析成功但没有真实内容时明确失败。
	//
	// 这对“扫描 PDF 但程序没有启用 OCR”尤其重要。
	if strings.TrimSpace(markdown) == "" {
		return nil, fmt.Errorf("%w: %s", ErrEmptyContent, path)
	}

	metadata := buildMetadata(src, path, info, warnings)
	metadata["rag_source_content_hash"] = result.ContentHash

	id := l.config.IDGenerator(src)
	if id == "" {
		id = DefaultIDGenerator(src)
	}

	return []*schema.Document{
		{
			ID:       id,
			Content:  markdown,
			MetaData: metadata,
		},
	}, nil
}

// validateSource 验证 Eino Source 是否是当前 Loader 可以处理的本地文件。
func validateSource(src document.Source) (string, os.FileInfo, error) {
	path := strings.TrimSpace(src.URI)

	if path == "" {
		return "", nil, ErrEmptySourceURI
	}

	if isRemoteURI(path) {
		return "", nil, fmt.Errorf("%w: %s", ErrRemoteSourceUnsupported, path)
	}

	info, err := os.Stat(path)
	if err != nil {
		return "", nil, fmt.Errorf("tabula loader: stat %q: %w", path, err)
	}

	if info.IsDir() {
		return "", nil, fmt.Errorf("%w: %s", ErrSourceIsDirectory, path)
	}

	if !info.Mode().IsRegular() {
		return "", nil, errors.New("tabula loader: source must be a regular file")
	}
	ext := strings.ToLower(filepath.Ext(path))

	if _, ok := supportedExtensions[ext]; !ok {
		if ext == "" {
			ext = "<none>"
		}

		return "", nil, fmt.Errorf("%w: %s", ErrUnsupportedFormat, ext)
	}

	return path, info, nil
}

// SupportedExtension 判断某个扩展名当前是否由 Tabula Loader 支持。
//
// 支持两种输入：
//
//	".pdf"
//	"pdf"
//
// 大小写不敏感。
func SupportedExtension(ext string) bool {
	ext = strings.TrimSpace(strings.ToLower(ext))

	if ext == "" {
		return false
	}

	if !strings.HasPrefix(ext, ".") {
		ext = "." + ext
	}

	_, ok := supportedExtensions[ext]
	return ok
}

// isRemoteURI 判断 Source.URI 是否是当前 Loader 不负责处理的远程资源。
//
// 当前只显式拦截 HTTP / HTTPS。
//
// 我们没有使用 url.Parse().Scheme != ""，
// 是因为 Windows 路径：
//
//	C:\data\file.pdf
//
// 会把：
//
//	C
//
// 解析成 URI scheme，造成误判。
func isRemoteURI(uri string) bool {
	lower := strings.ToLower(strings.TrimSpace(uri))

	return strings.HasPrefix(lower, "http://") ||
		strings.HasPrefix(lower, "https://")
}

// buildMetadata 构造原始 source Document 的 metadata。
//
// 这里保存的是：
//
//	文件级 provenance
//
// 而不是 Chunk metadata。
//
// 后面的：
//
//	transformer/chunker
//
// 会 clone 这些 metadata，
// 再加入：
//
//	rag_chunk_index
//	rag_chunk_start
//	rag_chunk_end
//	rag_context_header
func buildMetadata(
	src document.Source,
	path string,
	info os.FileInfo,
	warnings []tabulalib.Warning,
) map[string]any {
	ext := strings.ToLower(filepath.Ext(path))
	title := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))

	messages, ocrUsed := warningMetadata(warnings)

	metadata := map[string]any{
		MetaSourceURI: src.URI,
		MetaTitle:     title,
		MetaFileName:  filepath.Base(path),
		MetaFileExt:   strings.TrimPrefix(ext, "."),
		MetaFileSize:  info.Size(),
		MetaParser:    parserName,
		MetaOCRUsed:   ocrUsed,
	}
	if ext == ".txt" || ext == ".md" || ext == ".csv" {
		metadata[MetaParser] = "utf8-text"
	}

	if len(messages) > 0 {
		metadata[MetaParserWarnings] = messages
	}

	return metadata
}

// warningMetadata 把 Tabula Warning 转成适合 schema.Document.MetaData
// 保存的简单结构。
//
// 我们没有把整个 tabula.Warning 对象直接塞进去。
//
// 原因是 metadata 后面通常还需要：
//
//	JSON
//	PostgreSQL JSONB
//	Log
//
// []string 的兼容性和可读性更稳定。
//
// OCR 是否发生则单独保存 bool。
func warningMetadata(warnings []tabulalib.Warning) ([]string, bool) {
	if len(warnings) == 0 {
		return nil, false
	}

	messages := make([]string, 0, len(warnings))
	ocrUsed := false

	for _, warning := range warnings {
		if warning.Message != "" {
			messages = append(messages, warning.Message)
		}

		if warning.Code == tabulalib.WarningOCRFallback {
			ocrUsed = true
		}
	}

	return messages, ocrUsed
}

// normalizeLineEndings 把不同平台换行统一成 LF。
//
// 这里没有 import internal/rag/chunker。
//
// 原因是依赖方向应该保持：
//
//	Loader
//	   ↓
//	schema.Document
//
//	Transformer
//	   ↓
//	Chunker
//
// Tabula Loader 没必要为了一个两行文本规范化函数依赖整个 Chunker Package。
//
// 如果未来多个模块大量需要类似文本工具，
// 再单独抽：
//
//	internal/rag/textutil
//
// 会比形成 Loader → Chunker 的反向依赖更干净。
func normalizeLineEndings(text string) string {
	if !strings.Contains(text, "\r") {
		return text
	}

	text = strings.ReplaceAll(text, "\r\n", "\n")
	return strings.ReplaceAll(text, "\r", "\n")
}

// DefaultIDGenerator 为原始 source Document 生成稳定 ID。
//
// 当前使用：
//
//	SHA-256(source URI)
//
// 的前 16 bytes。
//
// 例如同一个：
//
//	/data/manual.pdf
//
// 每次 Load 都会得到同一个 Document.ID。
//
// 为什么不用：
//
//	filepath.Base(path)
//
// 因为：
//
//	/team-a/manual.pdf
//	/team-b/manual.pdf
//
// 会产生冲突。
//
// 为什么这里不用随机 UUID？
//
// 因为 Loader 层的稳定 ID 对：
//
//	测试
//	重跑 ingestion
//	Debug
//
// 都更友好。
//
// 真正业务数据库的 Document UUID
// 以后仍然可以由 PostgreSQL / Application Service 单独生成。
func DefaultIDGenerator(src document.Source) string {
	sum := sha256.Sum256([]byte(src.URI))

	// 16 bytes = 128 bit。
	//
	// 对 Loader 内部 source identity 已经足够，
	// 同时比完整 64 hex 字符更紧凑。
	return fmt.Sprintf("tabula-%x", sum[:16])
}
