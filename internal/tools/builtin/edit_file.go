package builtin

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"
	"unicode/utf8"

	einotool "github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"

	"github.com/sda1-hacker/humbert-agent/internal/sandbox"
	humberttools "github.com/sda1-hacker/humbert-agent/internal/tools"
	"github.com/sda1-hacker/humbert-agent/internal/workspace"
)

const (
	editFileToolName = "edit_file"

	editFileToolDescription = `精确编辑 Agent Sandbox 允许写入的目录中已经存在的 UTF-8 文本文件。

相对 path 以当前 Workspace 为基准；额外 Write Path 可使用绝对路径。Permission Allow 不会突破 Sandbox。
old_text 必须与文件中的原始文本完全一致；默认要求只出现一次。
如果确实需要替换所有相同片段，可以设置 replace_all=true。

当文件不存在、old_text 找不到或匹配不唯一时，工具会拒绝修改，避免模型静默改错位置。`
)

// EditFileInput 是 edit_file 的模型输入。
type EditFileInput struct {
	Path string `json:"path" jsonschema:"description=Relative UTF-8 text file path inside the current workspace."`

	OldText string `json:"old_text" jsonschema:"description=Exact existing text to replace. Include enough surrounding context to make the match unique."`

	NewText string `json:"new_text" jsonschema:"description=Replacement text."`

	ReplaceAll bool `json:"replace_all,omitempty" jsonschema:"description=Replace all exact occurrences instead of requiring one unique match."`
}

// EditFileOutput 是 edit_file 的结构化结果。
type EditFileOutput struct {
	Path string `json:"path"`

	Replacements int `json:"replacements"`

	BytesBefore int `json:"bytes_before"`

	BytesAfter int `json:"bytes_after"`

	Preview string `json:"preview,omitempty"`

	PreviewTruncated bool `json:"preview_truncated"`
}

// EditFileFactory 为 RuntimeSnapshot 创建绑定 Workspace 的 edit_file。
type EditFileFactory struct {
	workspaces *workspace.Manager

	maxFileBytes int64

	maxPreviewBytes int
}

// NewEditFileFactory 创建 edit_file Factory。
func NewEditFileFactory(
	workspaceManager *workspace.Manager,
	maxFileBytes int64,
	maxPreviewBytes int,
) (*EditFileFactory, error) {
	if workspaceManager == nil {
		return nil, errors.New(
			"EditFile WorkspaceManager 不能为空",
		)
	}

	if maxFileBytes <= 0 {
		return nil, errors.New(
			"EditFile MaxFileBytes 必须大于 0",
		)
	}

	if maxPreviewBytes <= 0 {
		return nil, errors.New(
			"EditFile MaxPreviewBytes 必须大于 0",
		)
	}

	return &EditFileFactory{
		workspaces: workspaceManager,

		maxFileBytes: maxFileBytes,

		maxPreviewBytes: maxPreviewBytes,
	}, nil
}

// Descriptor 返回 Humbert Registry 描述。
func (f *EditFileFactory) Descriptor() humberttools.Descriptor {
	return humberttools.Descriptor{
		Name: editFileToolName,

		Risk: humberttools.RiskWrite,
	}
}

// Build 创建绑定当前 Runtime Workspace 的 Eino Tool。
func (f *EditFileFactory) Build(
	ctx context.Context,
	scope humberttools.Scope,
) (
	einotool.InvokableTool,
	error,
) {
	if ctx == nil {
		return nil, errors.New(
			"构建 edit_file 失败: context.Context 不能为空",
		)
	}

	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf(
			"构建 edit_file 被取消: %w",
			err,
		)
	}

	return utils.InferTool(
		editFileToolName,
		editFileToolDescription,
		func(
			callCtx context.Context,
			input *EditFileInput,
		) (
			*EditFileOutput,
			error,
		) {
			return f.run(
				callCtx,
				scope,
				input,
			)
		},
	)
}

func (f *EditFileFactory) run(
	ctx context.Context,
	scope humberttools.Scope,
	input *EditFileInput,
) (
	*EditFileOutput,
	error,
) {
	if ctx == nil {
		return nil, errors.New(
			"edit_file: context.Context 不能为空",
		)
	}

	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf(
			"edit_file 被取消: %w",
			err,
		)
	}

	if input == nil {
		return nil, errors.New(
			"edit_file 输入不能为空",
		)
	}

	if input.OldText == "" {
		return nil, errors.New(
			"edit_file old_text 不能为空",
		)
	}

	if input.OldText == input.NewText {
		return nil, errors.New(
			"edit_file old_text 与 new_text 完全相同，没有可执行的变更",
		)
	}

	if !utf8.ValidString(input.OldText) ||
		!utf8.ValidString(input.NewText) {
		return nil, errors.New(
			"edit_file 只允许处理 UTF-8 文本",
		)
	}

	target, err := openSandboxTarget(ctx, scope, input.Path, sandbox.OpModify)
	if err != nil {
		return nil, fmt.Errorf("edit_file 路径被 Sandbox 拒绝: %w", err)
	}
	defer target.Close()
	if target.relative == "." {
		return nil, errors.New("edit_file 必须指定文件路径")
	}
	relativePath := target.relative
	root := target.root

	info, err :=
		root.Lstat(
			relativePath,
		)

	if err != nil {
		if errors.Is(
			err,
			fs.ErrNotExist,
		) {
			return nil, fmt.Errorf(
				"edit_file 文件不存在: %q",
				target.display,
			)
		}

		return nil, fmt.Errorf(
			"edit_file 检查文件失败: %w",
			err,
		)
	}

	if info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf(
			"edit_file 拒绝编辑符号链接: %q",
			target.display,
		)
	}

	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf(
			"edit_file 目标不是普通文件: %q",
			target.display,
		)
	}

	if info.Size() > f.maxFileBytes {
		return nil, fmt.Errorf(
			"edit_file 文件过大: %d bytes，最大允许 %d bytes",
			info.Size(),
			f.maxFileBytes,
		)
	}

	originalBytes, err :=
		readRootFileWithLimit(
			ctx,
			root,
			relativePath,
			f.maxFileBytes,
		)

	if err != nil {
		return nil, fmt.Errorf(
			"edit_file 读取文件失败: %w",
			err,
		)
	}

	if !utf8.Valid(originalBytes) {
		return nil, fmt.Errorf(
			"edit_file 只允许编辑 UTF-8 文本文件: %q",
			target.display,
		)
	}

	original :=
		string(originalBytes)

	matchCount :=
		strings.Count(
			original,
			input.OldText,
		)

	if matchCount == 0 {
		return nil, fmt.Errorf(
			"edit_file old_text 在 %q 中不存在",
			target.display,
		)
	}

	if matchCount > 1 &&
		!input.ReplaceAll {
		return nil, fmt.Errorf(
			"edit_file old_text 在 %q 中出现 %d 次；请提供更多上下文使匹配唯一，或明确设置 replace_all=true",
			target.display,
			matchCount,
		)
	}

	replacements := 1

	updated := ""

	if input.ReplaceAll {
		replacements =
			matchCount

		updated =
			strings.ReplaceAll(
				original,
				input.OldText,
				input.NewText,
			)
	} else {
		updated =
			strings.Replace(
				original,
				input.OldText,
				input.NewText,
				1,
			)
	}

	if int64(len(updated)) >
		f.maxFileBytes {
		return nil, fmt.Errorf(
			"edit_file 修改后的文件过大: %d bytes，最大允许 %d bytes",
			len(updated),
			f.maxFileBytes,
		)
	}

	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf(
			"edit_file 被取消: %w",
			err,
		)
	}

	mode :=
		info.Mode().Perm()

	if mode == 0 {
		mode = 0o600
	}

	if err :=
		atomicWriteWorkspaceFile(
			ctx,
			root,
			relativePath,
			[]byte(updated),
			mode,
			true,
		); err != nil {
		return nil, fmt.Errorf(
			"edit_file 提交修改失败: %w",
			err,
		)
	}

	preview,
		previewTruncated :=
		editPreview(
			input.OldText,
			input.NewText,
			f.maxPreviewBytes,
		)

	return &EditFileOutput{
		Path: target.display,

		Replacements: replacements,

		BytesBefore: len(originalBytes),

		BytesAfter: len(updated),

		Preview: preview,

		PreviewTruncated: previewTruncated,
	}, nil
}

func readRootFileWithLimit(
	ctx context.Context,
	root *os.Root,
	path string,
	maxBytes int64,
) ([]byte, error) {
	file, err :=
		root.Open(path)

	if err != nil {
		return nil, err
	}
	defer file.Close()

	reader :=
		io.LimitReader(
			file,
			maxBytes+1,
		)

	data, err :=
		io.ReadAll(reader)

	if err != nil {
		return nil, err
	}

	if int64(len(data)) >
		maxBytes {
		return nil, fmt.Errorf(
			"文件超过 %d bytes 限制",
			maxBytes,
		)
	}

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	return data, nil
}

func editPreview(
	oldText string,
	newText string,
	maxBytes int,
) (
	string,
	bool,
) {
	preview :=
		fmt.Sprintf(
			"- %s\n+ %s",
			oldText,
			newText,
		)

	if len(preview) <= maxBytes {
		return preview, false
	}

	if maxBytes <= 32 {
		return safeUTF8Prefix(
				preview,
				maxBytes,
			),
			true
	}

	budget :=
		maxBytes -
			len(
				"\n... [preview truncated]",
			)

	if budget < 0 {
		budget = 0
	}

	preview =
		safeUTF8Prefix(
			preview,
			budget,
		)

	return preview +
			"\n... [preview truncated]",
		true
}

func safeUTF8Prefix(
	value string,
	maxBytes int,
) string {
	if maxBytes <= 0 {
		return ""
	}

	if len(value) <= maxBytes {
		return value
	}

	value =
		value[:maxBytes]

	for len(value) > 0 &&
		!utf8.ValidString(value) {
		value =
			value[:len(value)-1]
	}

	return value
}
