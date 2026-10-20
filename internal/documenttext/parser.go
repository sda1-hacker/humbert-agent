// 文档解析在一次性子进程中执行；附件和知识库分别配置输入及输出限额。
package documenttext

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/tsawler/tabula"
	tabularag "github.com/tsawler/tabula/rag"
)

const workerMarker = "HUMBERT_DOCUMENT_PARSE_WORKER_V1"

var ErrResourceLimit = errors.New("document parser: resource limit exceeded")

// ParserOptions 同时供附件解析和 RAG 使用；配置键直接声明在字段上，避免重复映射。
type ParserOptions struct {
	// MaxInputBytes 是源文件字节上限，0 用 32 MiB；读取快照时也会再次限制。
	MaxInputBytes int64 `mapstructure:"max_input_bytes"`
	// MaxOutputBytes 是转换 Markdown 与提取图片的合计字节上限，0 用 32 MiB；图片识别后的正文也受此上限约束。
	MaxOutputBytes int `mapstructure:"max_output_bytes"`
	// MaxExpandedBytes 是 ZIP 文档解压后总字节上限，0 用 256 MiB；Excel 还按同一预算预检稠密单元格矩阵。
	MaxExpandedBytes uint64 `mapstructure:"max_expanded_bytes"`
	// MaxArchiveEntries 是 ZIP 文档内部文件条目上限，0 用 8192；不是工作表、幻灯片或页数。
	MaxArchiveEntries int `mapstructure:"max_archive_entries"`
	// Timeout 是一次文件解析的总时限，0 用 1m；超时会取消快照读取或终止解析子进程。
	Timeout time.Duration `mapstructure:"timeout"`
	// ExcludeHeadersAndFooters 控制 Tabula 页眉页脚过滤；解析器零值为 false，RAG.Default 设置为 true。
	ExcludeHeadersAndFooters bool `mapstructure:"exclude_headers_and_footers"`
	// OCRLanguage 是本地 Tesseract 语言，如 chi_sim+eng；空值用默认语言，需安装程序/语言包或启用已有 OCR 构建。
	OCRLanguage string `mapstructure:"ocr_language"`
	// IncludePageNumbers 控制 Markdown 中是否带页码标记，默认 false，不会改变源文件内容。
	IncludePageNumbers bool `mapstructure:"include_page_numbers"`
	// CacheDirectory 是解析缓存目录；auto 用系统用户缓存目录，空值关闭。缓存含正文，应使用本机私有目录。
	CacheDirectory string `mapstructure:"cache_directory"`
	// CacheMaxBytes 是该目录缓存文件的总字节上限，0 用 256 MiB；损坏、过期版本和超大结果会重新解析。
	CacheMaxBytes int64 `mapstructure:"cache_max_bytes"`
	// PDF 是通用 PDF 解析参数；nil 使用 WeKnora builtin 默认值，指针保证显式 false 不被默认值覆盖。
	PDF *PDFOptions `mapstructure:"pdf"`
	// PreserveImages 供 RAG 开启：转换器返回图片引用，由 Loader 保存后再切分；附件文本提取默认关闭。
	PreserveImages bool `mapstructure:"preserve_images"`
	// MaxImages 限制单篇文档的图片引用数（含扫描页），0 用 256；超限明确失败，不静默漏图。
	MaxImages int `mapstructure:"max_images"`
	// MaxImagePixels 在解码前检查图片宽高乘积，0 用 3200 万像素，避免小文件解压出超大位图。
	MaxImagePixels int64 `mapstructure:"max_image_pixels"`
	// XLSXFirstRowAsHeader 默认 false；保留首行为数据，列名 A/B/C，显式开启才使用首行标签。
	XLSXFirstRowAsHeader bool `mapstructure:"xlsx_first_row_as_header"`
	// XLSXPercentAsText 默认 false；百分比按底层数值输出，开启后保留百分号显示形式。
	XLSXPercentAsText bool `mapstructure:"xlsx_percent_as_text"`
}

func DefaultOptions() ParserOptions {
	pdf := DefaultPDFOptions()
	return ParserOptions{PDF: &pdf, MaxInputBytes: 32 << 20, MaxOutputBytes: 32 << 20, MaxExpandedBytes: 256 << 20, MaxArchiveEntries: 8192, Timeout: time.Minute, CacheMaxBytes: 256 << 20, MaxImages: 256, MaxImagePixels: 32_000_000}
}

func (o ParserOptions) Validate() error {
	if o.MaxInputBytes < 0 || o.MaxOutputBytes < 0 || o.Timeout < 0 || o.MaxArchiveEntries < 0 || o.CacheMaxBytes < 0 || o.MaxImages < 0 || o.MaxImagePixels < 0 {
		return errors.New("document parser: negative resource limit")
	}
	if o.MaxOutputBytes > (int(^uint(0)>>1)-(64*4096*6+(64<<10)))/8 || o.MaxInputBytes == int64(^uint64(0)>>1) {
		return errors.New("document parser: resource limit too large")
	}
	return o.pdfOptions().Validate()
}

func (o ParserOptions) effective() ParserOptions {
	d := DefaultOptions()
	if o.MaxImages == 0 {
		o.MaxImages = d.MaxImages
	}
	if o.MaxImagePixels == 0 {
		o.MaxImagePixels = d.MaxImagePixels
	}
	if o.MaxInputBytes == 0 {
		o.MaxInputBytes = d.MaxInputBytes
	}
	if o.MaxOutputBytes == 0 {
		o.MaxOutputBytes = d.MaxOutputBytes
	}
	if o.MaxExpandedBytes == 0 {
		o.MaxExpandedBytes = d.MaxExpandedBytes
	}
	if o.MaxArchiveEntries == 0 {
		o.MaxArchiveEntries = d.MaxArchiveEntries
	}
	if o.Timeout == 0 {
		o.Timeout = d.Timeout
	}
	if o.CacheMaxBytes == 0 {
		o.CacheMaxBytes = d.CacheMaxBytes
	}
	if o.CacheDirectory == "auto" {
		o.CacheDirectory = ""
		if directory, err := os.UserCacheDir(); err == nil {
			o.CacheDirectory = filepath.Join(directory, "humbert-agent", "rag", "tabula")
		}
	}
	return o
}

type Result struct {
	Markdown    string
	Warnings    []tabula.Warning
	ContentHash string
	// SourceBlocks 的范围基于最终 Markdown 的 Unicode 字符，不是字节；不伪造 Office 页码或坐标。
	SourceBlocks []SourceBlock
	CacheHit     bool
	Images       []ImageReference // 仅转换边界内传递二进制，Loader 保存为图片文件；不写入分块元数据。
	Metadata     map[string]any   // 页数、扫描页数、版面回退等解析诊断。
}
type workerRequest struct {
	Image   []byte
	Path    string
	Options ParserOptions
}
type workerResponse struct {
	Result          Result
	Error           string
	ResourceLimited bool
}

func init() {
	if os.Getenv(workerMarker) != "1" {
		return
	}
	var req workerRequest
	if err := json.NewDecoder(io.LimitReader(os.Stdin, 48<<20)).Decode(&req); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	var result Result
	var err error
	if req.Image != nil {
		result.Markdown, err = recognizePage(req.Image, req.Options.OCRLanguage)
	} else {
		result, err = convert(req.Path, req.Options)
	}
	response := workerResponse{Result: result}
	if err != nil {
		response.Error = err.Error()
		response.ResourceLimited = errors.Is(err, ErrResourceLimit)
	}
	if err = json.NewEncoder(os.Stdout).Encode(response); err != nil {
		os.Exit(1)
	}
	os.Exit(0)
}

// ExtractFile copies the already-authorized source before validating and
// parsing, so file replacement cannot invalidate the preflight checks.
func ExtractFile(ctx context.Context, path string, opts ParserOptions) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if err := opts.Validate(); err != nil {
		return Result{}, err
	}
	opts = opts.effective()
	ctx, cancel := context.WithTimeout(ctx, opts.Timeout)
	defer cancel()
	info, err := os.Stat(path)
	if err != nil {
		return Result{}, err
	}
	if !info.Mode().IsRegular() {
		return Result{}, errors.New("document parser: source must be a regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return Result{}, err
	}
	defer file.Close()
	info, err = file.Stat()
	if err != nil {
		return Result{}, err
	}
	if !info.Mode().IsRegular() {
		return Result{}, errors.New("document parser: source must be a regular file")
	}
	if info.Size() > opts.MaxInputBytes {
		return Result{}, fmt.Errorf("%w: input exceeds %d bytes", ErrResourceLimit, opts.MaxInputBytes)
	}
	snapshot, err := os.CreateTemp("", "humbert-parser-*"+strings.ToLower(filepath.Ext(path)))
	if err != nil {
		return Result{}, err
	}
	defer os.Remove(snapshot.Name())
	defer snapshot.Close()
	hash := sha256.New()
	n, err := io.Copy(io.MultiWriter(snapshot, hash), io.LimitReader(&contextReader{ctx: ctx, reader: file}, opts.MaxInputBytes+1))
	if err != nil {
		return Result{}, err
	}
	if n > opts.MaxInputBytes {
		return Result{}, fmt.Errorf("%w: input exceeds %d bytes", ErrResourceLimit, opts.MaxInputBytes)
	}
	if err = snapshot.Close(); err != nil {
		return Result{}, err
	}
	contentHash := fmt.Sprintf("%x", hash.Sum(nil))
	cacheKey := parseCacheKey(contentHash, filepath.Ext(snapshot.Name()), opts)
	// 同一进程的相同文件只转换一次；等待者取消不会取消正在转换的另一份请求。
	release, err := acquireParse(ctx, opts.CacheDirectory, cacheKey)
	if err != nil {
		return Result{}, err
	}
	defer release()
	if result, ok := readParseCache(ctx, cacheKey, contentHash, opts); ok {
		return result, ctx.Err()
	}
	if err = ctx.Err(); err != nil {
		return Result{}, err
	}
	result, err := callWorker(ctx, workerRequest{Path: snapshot.Name(), Options: opts})
	if err != nil {
		return Result{}, err
	}
	result.ContentHash = contentHash
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	writeParseCache(ctx, cacheKey, result, opts)
	return result, nil
}

// callWorker 统一文件转换与 OCR 的进程边界，超时/取消均终止解析进程树。
func callWorker(ctx context.Context, req workerRequest) (Result, error) {
	opts := req.Options.effective()
	executable, err := os.Executable()
	if err != nil {
		return Result{}, err
	}
	request, err := json.Marshal(req)
	if err != nil {
		return Result{}, err
	}
	command := exec.CommandContext(ctx, executable)
	configureWorker(command)
	// 临时目录由父进程持有：即使取消时强制终止 worker，渲染器留下的文件也会清理。
	workdir, err := os.MkdirTemp("", "humbert-parser-work-*")
	if err != nil {
		return Result{}, err
	}
	defer os.RemoveAll(workdir)
	command.Env = []string{workerMarker + "=1", "PATH=" + os.Getenv("PATH"), "TMPDIR=" + workdir, "TMP=" + workdir, "TEMP=" + workdir}
	if value := os.Getenv("SystemRoot"); value != "" {
		command.Env = append(command.Env, "SystemRoot="+value)
	}
	// OCR 语言包目录可以传入子进程；不继承模型密钥等其他环境变量。
	if value := os.Getenv("TESSDATA_PREFIX"); value != "" {
		command.Env = append(command.Env, "TESSDATA_PREFIX="+value)
	}
	command.Stdin = bytes.NewReader(request)
	command.WaitDelay = time.Second
	output := &boundedWriter{limit: parserResponseLimit(opts)}
	stderr := &boundedWriter{limit: 4096}
	command.Stdout, command.Stderr = output, stderr
	if err = command.Run(); err != nil {
		if ctx.Err() != nil {
			return Result{}, ctx.Err()
		}
		return Result{}, fmt.Errorf("document parser: worker failed: %s: %w", strings.TrimSpace(stderr.String()), err)
	}
	if output.exceeded {
		return Result{}, fmt.Errorf("%w: worker response too large", ErrResourceLimit)
	}
	var response workerResponse
	if err = json.Unmarshal(output.Bytes(), &response); err != nil {
		return Result{}, fmt.Errorf("document parser: invalid worker response: %w", err)
	}
	if response.ResourceLimited {
		return Result{}, fmt.Errorf("%w: %s", ErrResourceLimit, response.Error)
	}
	if response.Error != "" {
		return Result{}, fmt.Errorf("document parser: %s", strings.TrimPrefix(response.Error, "document parser: "))
	}
	return checkedResult(response.Result, opts)
}

// parserExecutable 优先使用显式路径；GUI/IDE 的 PATH 常缺少 Homebrew 等目录，按平台补查已安装的工具。
// 不启动 Shell 或修改全局 PATH；显式路径错误时直接报错，避免悄悄执行另一份程序。
func parserExecutable(name, configured string) (string, error) {
	if configured != "" {
		if !filepath.IsAbs(configured) {
			return "", fmt.Errorf("document parser: %s executable path must be absolute", name)
		}
		return exec.LookPath(configured)
	}
	path, err := exec.LookPath(name)
	if err == nil {
		return filepath.Abs(path)
	}
	if !errors.Is(err, exec.ErrNotFound) {
		return "", err
	}
	var directories []string
	switch runtime.GOOS {
	case "darwin":
		directories = []string{"/opt/homebrew/bin", "/usr/local/bin", "/opt/local/bin", "/usr/bin", "/bin"}
	case "linux":
		directories = []string{"/usr/local/bin", "/usr/bin", "/bin"}
	}
	for _, directory := range directories {
		if path, findErr := exec.LookPath(filepath.Join(directory, name)); findErr == nil {
			return path, nil
		}
	}
	return "", err
}

func convert(path string, opts ParserOptions) (Result, error) {
	// ZIP/XML 预检也放在受控进程中，大文件扫描和稀疏表格检查同样能被立即取消。
	if err := preflight(path, opts); err != nil {
		return Result{}, err
	}
	ext := strings.ToLower(filepath.Ext(path))
	if IsImageExtension(ext) {
		return convertImage(path, opts)
	}
	if opts.PreserveImages && (ext == ".html" || ext == ".htm") {
		return convertHTMLImages(path, opts)
	}
	if ext == ".md" || ext == ".txt" || ext == ".csv" {
		data, err := os.ReadFile(path)
		if err != nil {
			return Result{}, err
		}
		data = bytes.TrimPrefix(data, []byte{0xef, 0xbb, 0xbf})
		if !utf8.Valid(data) || bytes.ContainsRune(data, 0) {
			return Result{}, errors.New("document parser: text must be UTF-8 without NUL bytes")
		}
		if len(data) > opts.MaxOutputBytes {
			return Result{}, ErrResourceLimit
		}
		markdown := strings.ReplaceAll(strings.ReplaceAll(string(data), "\r\n", "\n"), "\r", "\n")
		if ext == ".csv" {
			markdown, err = csvMarkdown(markdown, opts.MaxOutputBytes)
			if err != nil {
				return Result{}, err
			}
		} else {
			markdown = normalizeMarkdown(markdown)
		}
		if len(markdown) > opts.MaxOutputBytes {
			return Result{}, ErrResourceLimit
		}
		result := Result{Markdown: markdown, SourceBlocks: textSourceBlocks(markdown, ext)}
		if ext == ".md" && opts.PreserveImages {
			if err := extractMarkdownImages(&result, opts); err != nil {
				return Result{}, err
			}
		}
		return checkedResult(result, opts)
	}
	if ext == ".pdf" {
		result, err := convertPDF(path, opts)
		if err != nil {
			return Result{}, err
		}
		return checkedResult(result, opts)
	}
	extractor := tabula.Open(path)
	if opts.ExcludeHeadersAndFooters {
		extractor = extractor.ExcludeHeadersAndFooters()
	}
	if opts.OCRLanguage != "" {
		extractor = extractor.OCRLanguage(opts.OCRLanguage)
	}
	markdownOpts := tabularag.DefaultMarkdownOptions()
	markdownOpts.IncludePageNumbers = opts.IncludePageNumbers
	// Office 保持格式转换与来源定位；正文只在下游 RAG 中切分。
	markdown, warnings, blocks, err := convertStructured(path, extractor, markdownOpts, opts)
	if err != nil {
		return Result{}, err
	}
	markdown = normalizeMarkdown(markdown)
	result := Result{Markdown: markdown, Warnings: warnings, SourceBlocks: alignSourceBlocks(markdown, blocks)}
	if opts.PreserveImages {
		if err := extractOfficeImages(path, opts, &result); err != nil {
			return Result{}, err
		}
	}
	return checkedResult(result, opts)
}

// checkedResult 是转换边界的最后校验，禁止损坏正文或超大图片结果进入缓存、向量化和数据库。
func checkedResult(result Result, opts ParserOptions) (Result, error) {
	opts = opts.effective()
	if len(result.Images) > opts.MaxImages {
		return Result{}, ErrResourceLimit
	}
	if !utf8.ValidString(result.Markdown) || strings.ContainsRune(result.Markdown, 0) {
		return Result{}, errors.New("document parser: converted text must be UTF-8 without NUL bytes")
	}
	size := len(result.Markdown)
	for _, image := range result.Images {
		if len(image.Data) > opts.MaxOutputBytes-size {
			return Result{}, ErrResourceLimit
		}
		size += len(image.Data)
	}
	if size > opts.MaxOutputBytes {
		return Result{}, ErrResourceLimit
	}
	if len(result.Warnings) > 64 {
		result.Warnings = result.Warnings[:64]
	}
	for i := range result.Warnings {
		if len(result.Warnings[i].Message) > 4096 {
			result.Warnings[i].Message = string([]rune(result.Warnings[i].Message)[:min(1024, len([]rune(result.Warnings[i].Message)))])
		}
	}
	return result, nil
}

func parserResponseLimit(opts ParserOptions) int {
	// 正文、JSON 转义与位置元数据共用硬上限，避免子进程 stdout 无限增长。
	return opts.MaxOutputBytes*8 + 64*4096*6 + (64 << 10)
}

func preflight(path string, opts ParserOptions) error {
	ext := strings.ToLower(filepath.Ext(path))
	if ext == ".pdf" {
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		defer file.Close()
		magic := make([]byte, 5)
		if _, err = io.ReadFull(file, magic); err != nil || !bytes.Equal(magic, []byte("%PDF-")) {
			return errors.New("document parser: invalid PDF signature")
		}
		return nil
	}
	switch ext {
	case ".docx", ".xlsx", ".pptx", ".odt", ".epub":
	default:
		return nil
	}
	archive, err := zip.OpenReader(path)
	if err != nil {
		return fmt.Errorf("document parser: invalid ZIP container: %w", err)
	}
	defer archive.Close()
	if len(archive.File) > opts.MaxArchiveEntries {
		return fmt.Errorf("%w: too many archive entries", ErrResourceLimit)
	}
	var total uint64
	for _, file := range archive.File {
		if file.Mode()&os.ModeSymlink != 0 || strings.HasPrefix(file.Name, "/") || strings.Contains(file.Name, "\\") || strings.Contains(file.Name, "../") {
			return errors.New("document parser: invalid archive entry")
		}
		if file.UncompressedSize64 > opts.MaxExpandedBytes-total {
			return fmt.Errorf("%w: expanded archive too large", ErrResourceLimit)
		}
		total += file.UncompressedSize64
	}
	if ext == ".xlsx" {
		for _, file := range archive.File {
			if strings.HasPrefix(file.Name, "xl/worksheets/") && strings.HasSuffix(file.Name, ".xml") {
				if err := preflightSpreadsheet(file, opts); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}

type boundedWriter struct {
	bytes.Buffer
	limit    int
	exceeded bool
}

func (w *boundedWriter) Write(p []byte) (int, error) {
	n := len(p)
	remaining := w.limit - w.Len()
	if len(p) > remaining {
		p = p[:remaining]
		w.exceeded = true
	}
	_, _ = w.Buffer.Write(p)
	return n, nil
}

// OutputLimit 供图片引用改写/OCR 后的 Loader 继续使用同一输出上限。
func (o ParserOptions) OutputLimit() int { return o.effective().MaxOutputBytes }

// Snapshot 记录实际生效的解析参数；缓存路径属于本机运行状态，不扩散到数据库。
func (o ParserOptions) Snapshot() ParserOptions {
	o = o.effective()
	pdf := o.pdfOptions()
	o.PDF = &pdf
	o.CacheDirectory = ""
	o.CacheMaxBytes = 0
	return o
}
