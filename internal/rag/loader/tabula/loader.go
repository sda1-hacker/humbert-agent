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
	"github.com/sda1-hacker/humbert-agent/internal/rag/chunker"
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

	// 当前 Loader 只解析本地文件，远程地址须先由调用方下载为本地文件。
	ErrRemoteSourceUnsupported = errors.New("tabula loader: remote source is not supported")

	// ErrUnsupportedFormat 表示扩展名不属于当前 Tabula 支持格式。
	ErrUnsupportedFormat = errors.New("tabula loader: unsupported file format")

	// ErrSourceIsDirectory 表示 Source.URI 指向目录，而不是普通文档文件。
	ErrSourceIsDirectory = errors.New("tabula loader: source is a directory")

	// 成功解析但没有有效正文时返回明确错误，例如没有启用 OCR 的扫描 PDF。
	ErrEmptyContent = errors.New("tabula loader: extracted markdown is empty")
)

// 来源与文件信息用于追溯，解析警告和 OCR 状态用于展示诊断。

const (
	// MetaSourceURI 保存 Eino Source.URI。
	//
	// Eino 自身的 Parser/Loader 生态中也常使用 "_source"
	// 表示来源 URI。
	MetaSourceURI = "_source"

	// MetaTitle 默认使用不含扩展名的文件名。
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

	// MetaParserWarnings 保存非致命解析警告，不代表文档解析失败。
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

// IDGenerator 为解析后的原始文档分配稳定 ID，分块 ID 由后续切分阶段生成。
type IDGenerator func(src document.Source) string

// Config 控制本地文件解析、页眉页脚过滤、可选 OCR 与原文换行归一化。
type Config struct {
	ParseOptions documentparse.Options
	// ExcludeHeadersAndFooters 指定解析时是否过滤重复页眉页脚。
	ExcludeHeadersAndFooters bool

	// OCRLanguage 指定 Tesseract 语言；OCR 需要使用 ocr 构建标签及相应运行依赖。
	OCRLanguage string

	// NormalizeLineEndings 将换行统一为 LF；后续分块坐标以归一化正文为准。
	NormalizeLineEndings bool

	// IDGenerator 为解析后的原始文档分配稳定 ID，分块 ID 由后续切分阶段生成。
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

// Loader 实现 Eino 文档加载接口，将本地文件解析为完整 Markdown。
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

// Load 校验本地文件，在受限子进程中解析，返回正文和来源元数据；当前不使用调用级选项。
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

	// 解析子进程在取消时终止；构造输出前再次检查，防止已取消请求进入后续阶段。
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	if l.config.NormalizeLineEndings {
		markdown = chunker.NormalizeLineEndings(markdown)
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

// SupportedExtension 判断格式是否受支持，兼容大小写以及带点或不带点的扩展名。
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

// isRemoteURI 判断是否为 HTTP 地址；远程下载应在本地 Loader 之前完成。
func isRemoteURI(uri string) bool {
	lower := strings.ToLower(strings.TrimSpace(uri))

	return strings.HasPrefix(lower, "http://") ||
		strings.HasPrefix(lower, "https://")
}

// buildMetadata 保存文件来源、标题、格式、大小与解析诊断。
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

// warningMetadata 将解析警告转成可序列化文本，同时记录是否使用 OCR。
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

// DefaultIDGenerator 根据来源 URI 的 SHA-256 前 128 位生成稳定 ID；内容变化仍对应同一文档。
func DefaultIDGenerator(src document.Source) string {
	sum := sha256.Sum256([]byte(src.URI))

	// 取前 16 字节，即 128 位摘要。
	return fmt.Sprintf("tabula-%x", sum[:16])
}
