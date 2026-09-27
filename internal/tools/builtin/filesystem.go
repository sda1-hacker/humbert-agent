package builtin

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/cloudwego/eino/adk"
	einofs "github.com/cloudwego/eino/adk/filesystem"
	filesystemmw "github.com/cloudwego/eino/adk/middlewares/filesystem"
	einotool "github.com/cloudwego/eino/components/tool"
	"github.com/sda1-hacker/humbert-agent/internal/documenttext"
	"github.com/sda1-hacker/humbert-agent/internal/sandbox"
	humberttools "github.com/sda1-hacker/humbert-agent/internal/tools"
)

// FilesystemBackend 只实现安全 I/O。工具 schema、参数解析和结果格式由 Eino 维护。
// Backend 绑定单轮 Scope，目录授权仍通过 PathGuard + os.Root 强制执行。
type FilesystemBackend struct {
	scope            humberttools.Scope
	limits           FileLimits
	maxWritableBytes int64
}

var _ einofs.Backend = (*FilesystemBackend)(nil)

// NewFilesystemFactories 保留能力选择和权限风险，六个文件工具共用一个 Backend 实现。
func NewFilesystemFactories(limits FileLimits, maxWritableBytes int64) ([]humberttools.Factory, error) {
	if err := limits.Validate(); err != nil {
		return nil, err
	}
	if maxWritableBytes <= 0 {
		return nil, errors.New("文件写入上限必须大于 0")
	}
	var factories []humberttools.Factory
	for _, name := range []string{"list_files", "read_file", "write_file", "edit_file", "glob_files", "grep_files"} {
		factories = append(factories, &filesystemFactory{name: name, limits: limits, maxWritableBytes: maxWritableBytes})
	}
	return factories, nil
}

type filesystemFactory struct {
	name             string
	limits           FileLimits
	maxWritableBytes int64
}

func (f *filesystemFactory) Descriptor() humberttools.Descriptor {
	risk := humberttools.RiskRead
	if f.name == "write_file" || f.name == "edit_file" {
		risk = humberttools.RiskWrite
	}
	return humberttools.Descriptor{Name: f.name, Risk: risk}
}
func (f *filesystemFactory) Build(ctx context.Context, scope humberttools.Scope) (einotool.InvokableTool, error) {
	config := func(name string) *filesystemmw.ToolConfig {
		return &filesystemmw.ToolConfig{Name: name, Disable: name != f.name}
	}
	handler, err := filesystemmw.New(ctx, &filesystemmw.MiddlewareConfig{
		Backend:      &FilesystemBackend{scope: scope, limits: f.limits, maxWritableBytes: f.maxWritableBytes},
		LsToolConfig: config("list_files"), ReadFileToolConfig: config("read_file"), WriteFileToolConfig: config("write_file"), EditFileToolConfig: config("edit_file"), GlobToolConfig: config("glob_files"), GrepToolConfig: config("grep_files"),
	})
	if err != nil {
		return nil, err
	}
	// 在解析快照时取得 Eino 实际工具定义，再统一交给 Registry 的权限包装。
	_, agentCtx, err := handler.BeforeAgent(ctx, &adk.ChatModelAgentContext{})
	if err != nil {
		return nil, err
	}
	if len(agentCtx.Tools) != 1 {
		return nil, errors.New("Eino 文件工具注册数量异常")
	}
	tool, ok := agentCtx.Tools[0].(einotool.InvokableTool)
	if !ok {
		return nil, errors.New("不支持的 Eino 文件工具类型")
	}
	return tool, nil
}

func (b *FilesystemBackend) LsInfo(ctx context.Context, req *einofs.LsInfoRequest) ([]einofs.FileInfo, error) {
	target, err := openSandboxTarget(ctx, b.scope, defaultSearchPath(req.Path), sandbox.OpRead)
	if err != nil {
		return nil, err
	}
	defer target.Close()
	file, err := target.root.Open(target.relative)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	entries, err := file.ReadDir(b.limits.MaxListEntries + 1)
	if err != nil && err != io.EOF {
		return nil, err
	}
	if len(entries) > b.limits.MaxListEntries {
		return nil, errors.New("目录项过多，请使用 glob_files 缩小范围")
	}
	var result []einofs.FileInfo
	for _, entry := range entries {
		if entry.Type()&fs.ModeSymlink != 0 {
			continue
		}
		if allowed, err := target.canReadChild(filepath.Join(target.relative, entry.Name())); err != nil {
			return nil, err
		} else if !allowed {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return nil, err
		}
		result = append(result, fileInfo(displayRootChild(target, filepath.Join(target.relative, entry.Name())), info))
	}
	return result, nil
}
func fileInfo(path string, info fs.FileInfo) einofs.FileInfo {
	return einofs.FileInfo{Path: path, IsDir: info.IsDir(), Size: info.Size(), ModifiedAt: info.ModTime().UTC().Format(time.RFC3339)}
}

func (b *FilesystemBackend) Read(ctx context.Context, req *einofs.ReadRequest) (*einofs.FileContent, error) {
	target, err := openSandboxTarget(ctx, b.scope, req.FilePath, sandbox.OpRead)
	if err != nil {
		return nil, err
	}
	defer target.Close()
	data, err := readRootFileWithLimit(ctx, target.root, target.relative, b.limits.MaxReadableFileBytes)
	if err != nil {
		return nil, err
	}
	if !utf8.Valid(data) {
		return nil, errors.New("read_file 只支持 UTF-8 文本；文档请使用 extract_document")
	}
	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	start := max(1, req.Offset) - 1
	limit := req.Limit
	if limit == 0 {
		limit = 2000
	}
	if limit < 1 {
		return nil, errors.New("limit 必须大于 0")
	}
	// Eino 的默认值也受应用文件读取上限约束。
	limit = min(limit, b.limits.MaxReadLines)
	if start >= len(lines) {
		return nil, fmt.Errorf("offset 超出文件总行数 %d", len(lines))
	}
	content := strings.Join(lines[start:min(len(lines), start+limit)], "\n")
	// 不静默截断长行。大结果由 Reduction 完整归档；磁盘读取本身由 MaxReadableFileBytes 限制。
	return &einofs.FileContent{Content: content}, nil
}

func (b *FilesystemBackend) Write(ctx context.Context, req *einofs.WriteRequest) error {
	if !utf8.ValidString(req.Content) || int64(len(req.Content)) > b.maxWritableBytes {
		return errors.New("写入内容不是 UTF-8 或超过文件大小上限")
	}
	target, err := openSandboxTarget(ctx, b.scope, req.FilePath, sandbox.OpModify)
	if err != nil {
		return err
	}
	defer target.Close()
	if target.relative == "." {
		return errors.New("必须指定文件路径")
	}
	unlock, err := lockFileTargets(ctx, target)
	if err != nil {
		return err
	}
	defer unlock()
	if err := ensureRootParentDirectory(target.root, target.relative); err != nil {
		return err
	}
	info, err := target.root.Lstat(target.relative)
	exists := err == nil
	mode := fs.FileMode(0o600)
	if exists {
		if !info.Mode().IsRegular() {
			return errors.New("拒绝写入符号链接或特殊文件")
		}
		mode = info.Mode().Perm()
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return atomicWriteWorkspaceFile(ctx, target.root, target.relative, []byte(req.Content), mode, fileVersion{info: info})
}
func (b *FilesystemBackend) Edit(ctx context.Context, req *einofs.EditRequest) error {
	if req.OldString == "" || req.OldString == req.NewString {
		return errors.New("old_string 不能为空且必须与 new_string 不同")
	}
	if !utf8.ValidString(req.OldString) || !utf8.ValidString(req.NewString) {
		return errors.New("edit_file 只支持 UTF-8 文本")
	}
	target, err := openSandboxTarget(ctx, b.scope, req.FilePath, sandbox.OpModify)
	if err != nil {
		return err
	}
	defer target.Close()
	unlock, err := lockFileTargets(ctx, target)
	if err != nil {
		return err
	}
	defer unlock()
	info, err := target.root.Lstat(target.relative)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New("拒绝编辑符号链接或特殊文件")
	}
	data, err := readRootFileWithLimit(ctx, target.root, target.relative, b.maxWritableBytes)
	if err != nil {
		return err
	}
	if !utf8.Valid(data) {
		return errors.New("目标不是 UTF-8 文件")
	}
	count := strings.Count(string(data), req.OldString)
	if count == 0 || (count > 1 && !req.ReplaceAll) {
		return fmt.Errorf("old_string 匹配 %d 次，请提供唯一文本或使用 replace_all", count)
	}
	n := 1
	if req.ReplaceAll {
		n = -1
	}
	updated := strings.Replace(string(data), req.OldString, req.NewString, n)
	if int64(len(updated)) > b.maxWritableBytes {
		return errors.New("编辑后文件超过大小上限")
	}
	return atomicWriteWorkspaceFile(ctx, target.root, target.relative, []byte(updated), info.Mode().Perm(), fileVersion{info: info, content: data})
}

func (b *FilesystemBackend) GlobInfo(ctx context.Context, req *einofs.GlobInfoRequest) ([]einofs.FileInfo, error) {
	if req.Pattern == "" || !doublestar.ValidatePattern(req.Pattern) {
		return nil, errors.New("glob pattern 无效")
	}
	target, err := openSandboxTarget(ctx, b.scope, defaultSearchPath(req.Path), sandbox.OpSearch)
	if err != nil {
		return nil, err
	}
	defer target.Close()
	var result []einofs.FileInfo
	err = walkRoot(ctx, target, func(rel string, entry fs.DirEntry) error {
		candidate := entry.Name()
		if strings.Contains(req.Pattern, "/") {
			candidate, _ = filepath.Rel(target.relative, rel)
			candidate = filepath.ToSlash(candidate)
		}
		if !doublestar.MatchUnvalidated(req.Pattern, candidate) {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		result = append(result, fileInfo(displayRootChild(target, rel), info))
		if len(result) > 500 {
			return errors.New("匹配超过 500 项，请缩小搜索范围")
		}
		return nil
	})
	return result, err
}

func (b *FilesystemBackend) GrepRaw(ctx context.Context, req *einofs.GrepRequest) ([]einofs.GrepMatch, error) {
	if req.Pattern == "" {
		return nil, errors.New("grep pattern 不能为空")
	}
	pattern := req.Pattern
	if req.CaseInsensitive {
		pattern = "(?i)" + pattern
	}
	if req.EnableMultiline {
		pattern = "(?ms)" + pattern
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, err
	}
	glob := req.Glob
	if glob != "" && !doublestar.ValidatePattern(glob) {
		return nil, errors.New("glob 无效")
	}
	target, err := openSandboxTarget(ctx, b.scope, defaultSearchPath(req.Path), sandbox.OpSearch)
	if err != nil {
		return nil, err
	}
	defer target.Close()
	var result []einofs.GrepMatch
	err = walkRoot(ctx, target, func(rel string, entry fs.DirEntry) error {
		if entry.IsDir() || !entry.Type().IsRegular() {
			return nil
		}
		candidate, _ := filepath.Rel(target.relative, rel)
		candidate = filepath.ToSlash(candidate)
		if glob != "" && !doublestar.MatchUnvalidated(glob, candidate) && !doublestar.MatchUnvalidated(glob, entry.Name()) {
			return nil
		}
		if req.FileType != "" && !matchesFileType(entry.Name(), req.FileType) {
			return nil
		}
		data, err := readRootFileWithLimit(ctx, target.root, rel, b.limits.MaxReadableFileBytes)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return nil
		}
		content := string(data)
		if mime := documenttext.MIMEForName(entry.Name()); mime != "" {
			content, _, err = documenttext.Extract(ctx, entry.Name(), mime, data)
			if err != nil {
				return nil
			}
		} else if !utf8.Valid(data) || strings.ContainsRune(content, 0) {
			return nil
		}
		lines := strings.Split(content, "\n")
		selected := make(map[int]bool)
		if req.EnableMultiline {
			for _, match := range re.FindAllStringIndex(content, 501) {
				a := strings.Count(content[:match[0]], "\n")
				z := a + strings.Count(content[match[0]:match[1]], "\n")
				for i := max(0, a-req.BeforeLines); i <= min(len(lines)-1, z+req.AfterLines); i++ {
					selected[i] = true
				}
			}
		} else {
			for i, line := range lines {
				if re.MatchString(line) {
					for j := max(0, i-req.BeforeLines); j <= min(len(lines)-1, i+req.AfterLines); j++ {
						selected[j] = true
					}
				}
			}
		}
		for i, line := range lines {
			if !selected[i] {
				continue
			}
			if len([]rune(line)) > 2000 {
				line = string([]rune(line)[:2000]) + "…"
			}
			result = append(result, einofs.GrepMatch{Path: displayRootChild(target, rel), Line: i + 1, Content: line})
			if len(result) > 500 {
				return errors.New("搜索超过 500 行，请缩小范围")
			}
		}
		return ctx.Err()
	})
	return result, err
}
func matchesFileType(name, kind string) bool {
	extensions := map[string]string{"js": ".js .jsx .mjs .cjs", "ts": ".ts .tsx .mts .cts", "rust": ".rs", "python": ".py", "py": ".py", "go": ".go", "markdown": ".md .mdx"}
	allowed := extensions[strings.ToLower(kind)]
	if allowed == "" {
		allowed = "." + strings.TrimPrefix(kind, ".")
	}
	for _, ext := range strings.Fields(allowed) {
		if strings.EqualFold(filepath.Ext(name), ext) {
			return true
		}
	}
	return false
}

// 文件句柄由 os.Root 打开且读取前后都有大小检查，普通文件以外的对象直接拒绝。
func readRootFileWithLimit(ctx context.Context, root *os.Root, path string, maxBytes int64) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	info, err := root.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("目标不是普通文件或是符号链接")
	}
	if info.Size() > maxBytes {
		return nil, errors.New("文件超过读取大小上限")
	}
	file, err := root.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxBytes {
		return nil, errors.New("文件在读取时超过大小上限")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return data, nil
}
