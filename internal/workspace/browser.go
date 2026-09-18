package workspace

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	// DefaultBrowseLimit 是桌面工作区浏览器单次最多返回的直接子项数量。
	//
	// 这里不是为了隐藏文件，而是避免用户打开 node_modules 等超大目录时，
	// 一次 IPC 就把数万条目录项送到前端，造成 UI 卡顿。前端会明确展示 truncated 状态。
	DefaultBrowseLimit = 500

	// MaxPreviewTextBytes 控制文本预览的最大字节数。
	// 完整文件仍然保存在 Workspace 中，预览只负责“快速查看”，不是文件传输接口。
	MaxPreviewTextBytes int64 = 512 * 1024

	// MaxPreviewImageBytes 控制可以直接转成 Data URL 发送到 WebView 的图片大小。
	// 过大的图片仍然会显示元数据，但不会整份复制到前端内存。
	MaxPreviewImageBytes int64 = 8 * 1024 * 1024
)

// BrowseEntry 是工作区文件浏览器的一条目录项。
//
// Path 始终使用相对于 Workspace 根目录的 slash 路径。UI 永远不需要自己拼物理路径，
// 从而避免把桌面展示逻辑变成第二套路径安全实现。
type BrowseEntry struct {
	Name       string
	Path       string
	Type       string
	Size       int64
	ModifiedAt time.Time
	Hidden     bool
}

// DirectoryListing 是一次“只列一层”的安全目录浏览结果。
//
// Humbert 不在后端递归生成整棵树。前端展开哪个目录，就只读取哪个目录，
// 这样即使 Workspace 很大，首屏也不会扫描所有文件。
type DirectoryListing struct {
	Path      string
	Entries   []BrowseEntry
	Truncated bool
}

// FilePreview 是桌面工作区预览区使用的受控文件内容。
//
// Kind 目前只有 text / image / binary：
//   - text：Content 是 UTF-8 文本；
//   - image：DataURL 是安全白名单图片生成的 data: URL；
//   - binary：只返回文件元数据，不把原始二进制送进 WebView。
//
// Truncated 只表示“预览被截断”，不代表真实文件被修改。
type FilePreview struct {
	Path       string
	Name       string
	Kind       string
	MIMEType   string
	Size       int64
	ModifiedAt time.Time
	Content    string
	DataURL    string
	Truncated  bool
}

// ListDirectory 通过 Workspace Manager 已冻结的 Workspace 描述读取一层目录。
//
// 关键安全点：
//  1. relativePath 必须先经过 NormalizeRelativePath；
//  2. 真正 IO 使用 OpenRoot 返回的 *os.Root；
//  3. 符号链接只展示为 symlink，不跟随读取；
//  4. 结果数量有硬上限，防止超大目录拖垮桌面端。
func (m *Manager) ListDirectory(
	ctx context.Context,
	value Workspace,
	relativePath string,
	limit int,
) (DirectoryListing, error) {
	if ctx == nil {
		return DirectoryListing{}, errors.New("context.Context 不能为空")
	}
	if err := ctx.Err(); err != nil {
		return DirectoryListing{}, fmt.Errorf("读取 Workspace 目录被取消: %w", err)
	}
	if limit <= 0 {
		limit = DefaultBrowseLimit
	}
	if limit > 2000 {
		limit = 2000
	}

	normalized, err := NormalizeRelativePath(relativePath)
	if err != nil {
		return DirectoryListing{}, err
	}
	root, err := m.OpenRoot(ctx, value)
	if err != nil {
		return DirectoryListing{}, err
	}
	defer root.Close()

	directory, err := root.Open(normalized)
	if err != nil {
		return DirectoryListing{}, fmt.Errorf("打开 Workspace 目录 %q 失败: %w", displayRelativePath(normalized), err)
	}
	defer directory.Close()

	info, err := directory.Stat()
	if err != nil {
		return DirectoryListing{}, fmt.Errorf("读取 Workspace 目录状态失败: %w", err)
	}
	if !info.IsDir() {
		return DirectoryListing{}, fmt.Errorf("%q 不是目录", displayRelativePath(normalized))
	}

	// 多读取一项，只用来判断前端是否需要显示“目录内容已截断”。
	entries, err := directory.ReadDir(limit + 1)
	if err != nil && !errors.Is(err, io.EOF) {
		return DirectoryListing{}, fmt.Errorf("读取 Workspace 目录失败: %w", err)
	}
	truncated := len(entries) > limit
	if truncated {
		entries = entries[:limit]
	}

	result := make([]BrowseEntry, 0, len(entries))
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return DirectoryListing{}, fmt.Errorf("读取 Workspace 目录被取消: %w", err)
		}

		entryPath := normalized
		if entryPath == "." {
			entryPath = entry.Name()
		} else {
			entryPath = filepath.Join(entryPath, entry.Name())
		}
		item := BrowseEntry{
			Name:   entry.Name(),
			Path:   filepath.ToSlash(entryPath),
			Type:   browseEntryType(entry),
			Hidden: strings.HasPrefix(entry.Name(), "."),
		}
		if entryInfo, infoErr := entry.Info(); infoErr == nil {
			item.Size = entryInfo.Size()
			item.ModifiedAt = entryInfo.ModTime().UTC()
		}
		result = append(result, item)
	}

	// 文件浏览器优先显示目录，再按不区分大小写的名称排序。
	// 这样跨 macOS/Linux/Windows 的展示顺序更稳定。
	sort.SliceStable(result, func(i, j int) bool {
		iDir := result[i].Type == "directory"
		jDir := result[j].Type == "directory"
		if iDir != jDir {
			return iDir
		}
		left := strings.ToLower(result[i].Name)
		right := strings.ToLower(result[j].Name)
		if left == right {
			return result[i].Name < result[j].Name
		}
		return left < right
	})

	return DirectoryListing{
		Path:      displayRelativePath(normalized),
		Entries:   result,
		Truncated: truncated,
	}, nil
}

// PreviewFile 返回一个适合桌面 UI 展示的受限预览。
//
// UI 浏览是“只读能力”，因此这里不经过 Permission Approval；但仍然严格限定在
// Agent 当前 Workspace Root 内，并拒绝目录、符号链接、设备、FIFO 等特殊文件。
func (m *Manager) PreviewFile(
	ctx context.Context,
	value Workspace,
	relativePath string,
) (FilePreview, error) {
	if ctx == nil {
		return FilePreview{}, errors.New("context.Context 不能为空")
	}
	if err := ctx.Err(); err != nil {
		return FilePreview{}, fmt.Errorf("预览 Workspace 文件被取消: %w", err)
	}
	normalized, err := NormalizeRelativePath(relativePath)
	if err != nil {
		return FilePreview{}, err
	}
	if normalized == "." {
		return FilePreview{}, errors.New("文件预览必须指定一个文件")
	}

	root, err := m.OpenRoot(ctx, value)
	if err != nil {
		return FilePreview{}, err
	}
	defer root.Close()

	// 先用 Lstat 拒绝符号链接，再 Open。即使符号链接最终仍位于 Workspace 内，
	// UI 也不跟随它，避免浏览器展示结果随着外部链接目标变化而产生歧义。
	info, err := root.Lstat(normalized)
	if err != nil {
		return FilePreview{}, fmt.Errorf("读取文件 %q 状态失败: %w", filepath.ToSlash(normalized), err)
	}
	if info.Mode()&fs.ModeSymlink != 0 {
		return FilePreview{}, errors.New("工作区预览不跟随符号链接")
	}
	if !info.Mode().IsRegular() {
		return FilePreview{}, errors.New("工作区预览只支持普通文件")
	}

	file, err := root.Open(normalized)
	if err != nil {
		return FilePreview{}, fmt.Errorf("打开文件 %q 失败: %w", filepath.ToSlash(normalized), err)
	}
	defer file.Close()

	result := FilePreview{
		Path:       filepath.ToSlash(normalized),
		Name:       filepath.Base(normalized),
		Size:       info.Size(),
		ModifiedAt: info.ModTime().UTC(),
	}

	// MIME 判断需要少量文件头。读取文件头不会改变后续 Seek 位置的可靠性，
	// 因为 *os.File 支持 Seek；如果未来 os.Root 返回其他实现，这里失败也会安全退出。
	header := make([]byte, 512)
	headerSize, readErr := file.Read(header)
	if readErr != nil && !errors.Is(readErr, io.EOF) {
		return FilePreview{}, fmt.Errorf("读取文件头失败: %w", readErr)
	}
	header = header[:headerSize]
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return FilePreview{}, fmt.Errorf("重置文件预览游标失败: %w", err)
	}

	mimeType := detectPreviewMIME(normalized, header)
	result.MIMEType = mimeType

	if isSafePreviewImageMIME(mimeType) {
		result.Kind = "image"
		if info.Size() > MaxPreviewImageBytes {
			result.Truncated = true
			return result, nil
		}
		data, err := io.ReadAll(io.LimitReader(file, MaxPreviewImageBytes+1))
		if err != nil {
			return FilePreview{}, fmt.Errorf("读取图片预览失败: %w", err)
		}
		if int64(len(data)) > MaxPreviewImageBytes {
			result.Truncated = true
			return result, nil
		}
		result.DataURL = "data:" + mimeType + ";base64," + base64.StdEncoding.EncodeToString(data)
		return result, nil
	}

	// 文本判断同时使用 MIME 和 UTF-8 内容。很多源码扩展名在不同系统上没有 MIME 注册，
	// 因此只靠 mime.TypeByExtension 会把 .go/.vue/.yaml 等错误当成二进制。
	data, err := io.ReadAll(io.LimitReader(file, MaxPreviewTextBytes+1))
	if err != nil {
		return FilePreview{}, fmt.Errorf("读取文本预览失败: %w", err)
	}
	if looksLikeText(mimeType, data) {
		result.Kind = "text"
		if int64(len(data)) > MaxPreviewTextBytes {
			data = data[:MaxPreviewTextBytes]
			result.Truncated = true
		}
		// 截断可能恰好落在 UTF-8 多字节字符中间，需要向前收缩到合法边界，
		// 避免 WebView 出现替换字符并误导用户以为文件内容损坏。
		for len(data) > 0 && !utf8.Valid(data) {
			data = data[:len(data)-1]
		}
		result.Content = string(data)
		return result, nil
	}

	result.Kind = "binary"
	return result, nil
}

func browseEntryType(entry fs.DirEntry) string {
	if entry.Type()&fs.ModeSymlink != 0 {
		return "symlink"
	}
	if entry.IsDir() {
		return "directory"
	}
	if entry.Type().IsRegular() {
		return "file"
	}
	if info, err := entry.Info(); err == nil && info.Mode().IsRegular() {
		return "file"
	}
	return "other"
}

func displayRelativePath(path string) string {
	path = filepath.ToSlash(filepath.Clean(path))
	if path == "" || path == "." {
		return "."
	}
	return path
}

func detectPreviewMIME(path string, header []byte) string {
	byExtension := strings.TrimSpace(mime.TypeByExtension(strings.ToLower(filepath.Ext(path))))
	if separator := strings.IndexByte(byExtension, ';'); separator >= 0 {
		byExtension = strings.TrimSpace(byExtension[:separator])
	}
	if byExtension != "" {
		return byExtension
	}
	if len(header) > 0 {
		return http.DetectContentType(header)
	}
	return "application/octet-stream"
}

// 只允许浏览器天然以位图方式展示的图片类型。
// SVG 明确不进入这一白名单，而是按文本预览，避免把用户 Workspace 中的 SVG
// 当成可执行文档直接嵌入页面。
func isSafePreviewImageMIME(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "image/png", "image/jpeg", "image/gif", "image/webp", "image/bmp":
		return true
	default:
		return false
	}
}

func looksLikeText(mimeType string, data []byte) bool {
	mimeType = strings.ToLower(strings.TrimSpace(mimeType))
	if strings.HasPrefix(mimeType, "text/") || strings.Contains(mimeType, "json") || strings.Contains(mimeType, "xml") || strings.Contains(mimeType, "javascript") || strings.Contains(mimeType, "yaml") {
		return true
	}
	if len(data) == 0 {
		return true
	}
	if !utf8.Valid(data) || bytes.IndexByte(data, 0) >= 0 {
		return false
	}
	// 对未知 MIME 的 UTF-8 文件使用一个保守的可打印字符比例判断。
	// 这能覆盖 Go/Markdown/TOML 等无稳定 MIME 的源码，同时避免把 UTF-8 编码的
	// 高熵二进制误判为文本。
	printable := 0
	total := 0
	for _, r := range string(data) {
		total++
		if r == '\n' || r == '\r' || r == '\t' || (r >= 0x20 && r != 0x7f) {
			printable++
		}
	}
	return total == 0 || printable*100/total >= 90
}
