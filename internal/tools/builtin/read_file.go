package builtin

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	einotool "github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"

	"github.com/sda1-hacker/humbert-agent/internal/sandbox"
	humberttools "github.com/sda1-hacker/humbert-agent/internal/tools"
	"github.com/sda1-hacker/humbert-agent/internal/workspace"
)

const (
	readFileToolName = "read_file"

	readFileToolDescription = `读取 Agent 当前 Workspace 中的 UTF-8 文本文件。

默认读取当前 Workspace；用户若在 Agent Sandbox 中显式加入额外只读/读写目录，也可读取这些目录。
相对路径以 Workspace 为基准；额外目录可以使用绝对路径。所有路径均经过 Sandbox PathGuard。

可以使用 start_line 和 line_count 分块读取较长文件。
行号从 1 开始。`
)

// ReadFileInput 是 read_file Tool 输入。
type ReadFileInput struct {
	// Path 是当前 Workspace 内相对路径。
	Path string `json:"path" jsonschema:"description=Relative UTF-8 text file path inside the current workspace."`

	// StartLine 是开始读取的行号，从 1 开始。
	//
	// 0 等价于 1。
	StartLine int `json:"start_line,omitempty" jsonschema:"description=First line to read, starting from 1. Defaults to 1."`

	// LineCount 是最多读取多少行。
	//
	// 0 使用配置中的 DefaultReadLines。
	LineCount int `json:"line_count,omitempty" jsonschema:"description=Maximum number of lines to return. Omit to use the default."`
}

// ReadFileOutput 是 read_file 的结构化结果。
type ReadFileOutput struct {
	Path string `json:"path"`

	Content string `json:"content"`

	StartLine int `json:"start_line"`

	EndLine int `json:"end_line"`

	TotalLines int `json:"total_lines"`

	// MoreLines 表示文件中仍有未返回的内容。
	MoreLines bool `json:"more_lines"`

	// OutputTruncated 表示由于 MaxReadOutputBytes，
	// 最后一行或最终输出被截断。
	OutputTruncated bool `json:"output_truncated"`
}

// ReadFileFactory 为 RuntimeSnapshot 创建 read_file Tool。
type ReadFileFactory struct {
	workspaces *workspace.Manager

	limits FileLimits
}

// NewReadFileFactory 创建 read_file Factory。
func NewReadFileFactory(
	workspaceManager *workspace.Manager,
	limits FileLimits,
) (
	*ReadFileFactory,
	error,
) {
	if workspaceManager == nil {
		return nil, errors.New(
			"ReadFile WorkspaceManager 不能为空",
		)
	}

	if err :=
		limits.Validate(); err != nil {

		return nil, fmt.Errorf(
			"ReadFile Limits 无效: %w",
			err,
		)
	}

	return &ReadFileFactory{
		workspaces: workspaceManager,

		limits: limits,
	}, nil
}

// Descriptor 返回 Tool Registry 描述。
func (f *ReadFileFactory) Descriptor() humberttools.Descriptor {
	return humberttools.Descriptor{
		Name: readFileToolName,

		Risk: humberttools.RiskRead,
	}
}

// Build 创建绑定当前 Workspace Snapshot 的 Eino Tool。
func (f *ReadFileFactory) Build(
	ctx context.Context,
	scope humberttools.Scope,
) (
	einotool.InvokableTool,
	error,
) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf(
			"构建 read_file 被取消: %w",
			err,
		)
	}

	return utils.InferTool(
		readFileToolName,
		readFileToolDescription,
		func(
			callCtx context.Context,
			input *ReadFileInput,
		) (
			*ReadFileOutput,
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

func (f *ReadFileFactory) run(
	ctx context.Context,
	scope humberttools.Scope,
	input *ReadFileInput,
) (
	*ReadFileOutput,
	error,
) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf(
			"read_file 被取消: %w",
			err,
		)
	}

	if input == nil {
		return nil, errors.New(
			"read_file 输入不能为空",
		)
	}

	target, err := openSandboxTarget(ctx, scope, input.Path, sandbox.OpRead)
	if err != nil {
		return nil, fmt.Errorf("read_file 路径被 Sandbox 拒绝: %w", err)
	}
	defer target.Close()
	if target.relative == "." {
		return nil, errors.New("read_file 必须指定一个文件")
	}

	startLine :=
		input.StartLine

	if startLine == 0 {
		startLine = 1
	}

	if startLine < 1 {
		return nil, errors.New(
			"start_line 必须大于等于 1",
		)
	}

	lineCount :=
		input.LineCount

	if lineCount == 0 {
		lineCount =
			f.limits.
				DefaultReadLines
	}

	if lineCount < 1 {
		return nil, errors.New(
			"line_count 必须大于等于 1",
		)
	}

	if lineCount >
		f.limits.MaxReadLines {

		return nil, fmt.Errorf(
			"%w: line_count=%d，最大允许 %d",
			humberttools.ErrLimitExceeded,
			lineCount,
			f.limits.MaxReadLines,
		)
	}

	file, err := target.root.Open(target.relative)
	if err != nil {
		return nil, fmt.Errorf("打开文件 %q 失败: %w", target.display, err)
	}
	defer file.Close()

	info, err :=
		file.Stat()

	if err != nil {
		return nil, fmt.Errorf(
			"读取文件 %q 状态失败: %w",
			target.display,
			err,
		)
	}

	if info.IsDir() {
		return nil, fmt.Errorf(
			"%q 是目录，请使用 list_files",
			target.display,
		)
	}

	// os.Root 本身并不禁止 FIFO、Socket、Device 等特殊文件。
	//
	// read_file 只允许普通文件，防止读取特殊设备或无限阻塞的 FIFO。
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf(
			"%w: %q 不是普通文件",
			humberttools.ErrUnsupportedFile,
			target.display,
		)
	}

	if info.Size() >
		f.limits.
			MaxReadableFileBytes {

		return nil, fmt.Errorf(
			"%w: 文件大小 %d 字节，read_file 最大允许 %d 字节",
			humberttools.ErrLimitExceeded,
			info.Size(),
			f.limits.MaxReadableFileBytes,
		)
	}

	// 即使 Stat 和 Read 之间文件被外部程序增大，
	// LimitReader 仍然提供第二层大小保护。
	reader :=
		io.LimitReader(
			file,
			f.limits.
				MaxReadableFileBytes+
				1,
		)

	contentBytes, err :=
		io.ReadAll(
			reader,
		)

	if err != nil {
		return nil, fmt.Errorf(
			"读取文件 %q 失败: %w",
			target.display,
			err,
		)
	}

	if int64(
		len(contentBytes),
	) >
		f.limits.
			MaxReadableFileBytes {

		return nil, fmt.Errorf(
			"%w: 文件在读取过程中超过最大允许大小",
			humberttools.ErrLimitExceeded,
		)
	}

	if !utf8.Valid(
		contentBytes,
	) {
		return nil, fmt.Errorf(
			"%w: %q 不是有效 UTF-8 文本文件",
			humberttools.ErrUnsupportedFile,
			target.display,
		)
	}

	content :=
		string(contentBytes)

	lines :=
		splitTextLines(
			content,
		)

	totalLines :=
		len(lines)

	if totalLines == 0 {
		return &ReadFileOutput{
			Path: target.display,

			Content: "",

			StartLine: 0,

			EndLine: 0,

			TotalLines: 0,

			MoreLines: false,

			OutputTruncated: false,
		}, nil
	}

	if startLine >
		totalLines {

		return nil, fmt.Errorf(
			"start_line=%d 超出文件总行数 %d",
			startLine,
			totalLines,
		)
	}

	startIndex :=
		startLine - 1

	endIndex :=
		startIndex +
			lineCount

	if endIndex > totalLines {
		endIndex =
			totalLines
	}

	selected := lines[startIndex:endIndex]

	resultContent,
		outputTruncated,
		actualLineCount :=
		joinLinesWithByteLimit(
			selected,
			f.limits.
				MaxReadOutputBytes,
		)

	actualEndLine :=
		startLine +
			actualLineCount -
			1

	if actualLineCount == 0 {
		actualEndLine =
			startLine - 1
	}

	moreLines :=
		actualEndLine <
			totalLines ||
			outputTruncated

	return &ReadFileOutput{
		Path: target.display,

		Content: resultContent,

		StartLine: startLine,

		EndLine: actualEndLine,

		TotalLines: totalLines,

		MoreLines: moreLines,

		OutputTruncated: outputTruncated,
	}, nil
}

// splitTextLines 将文本转换为用于 Tool Line Range 的逻辑行。
//
// 文件最后的换行符不额外算作一个空内容行。
func splitTextLines(
	content string,
) []string {
	if content == "" {
		return nil
	}

	// 统一 Windows CRLF，避免 Content 中多出 "\r"。
	content =
		strings.ReplaceAll(
			content,
			"\r\n",
			"\n",
		)

	lines :=
		strings.Split(
			content,
			"\n",
		)

	if strings.HasSuffix(
		content,
		"\n",
	) {
		lines = lines[:len(lines)-1]
	}

	return lines
}

// joinLinesWithByteLimit 在保证 UTF-8 完整性的情况下限制输出大小。
//
// actualLineCount 表示至少返回了多少个逻辑行。
// 如果最后一行只返回了一部分，该行仍然计入 actualLineCount。
func joinLinesWithByteLimit(
	lines []string,
	maxBytes int,
) (
	content string,
	truncated bool,
	actualLineCount int,
) {
	if len(lines) == 0 ||
		maxBytes <= 0 {

		return "", len(lines) > 0, 0
	}

	var builder strings.Builder

	for index, line := range lines {

		prefix :=
			""

		if index > 0 {
			prefix = "\n"
		}

		required :=
			len(prefix) +
				len(line)

		remaining :=
			maxBytes -
				builder.Len()

		if required <=
			remaining {

			builder.WriteString(
				prefix,
			)

			builder.WriteString(
				line,
			)

			actualLineCount++

			continue
		}

		if remaining <= 0 {
			return builder.String(),
				true,
				actualLineCount
		}

		if prefix != "" {
			if remaining <
				len(prefix) {

				return builder.String(),
					true,
					actualLineCount
			}

			builder.WriteString(
				prefix,
			)

			remaining -=
				len(prefix)
		}

		if remaining > 0 {
			builder.WriteString(
				truncateUTF8(
					line,
					remaining,
				),
			)

			actualLineCount++
		}

		return builder.String(),
			true,
			actualLineCount
	}

	return builder.String(),
		false,
		actualLineCount
}

// truncateUTF8 按字节限制截断字符串，同时保证结果仍是合法 UTF-8。
func truncateUTF8(
	value string,
	maxBytes int,
) string {
	if maxBytes <= 0 {
		return ""
	}

	if len(value) <= maxBytes {
		return value
	}

	end :=
		maxBytes

	for end > 0 &&
		!utf8.ValidString(
			value[:end],
		) {

		end--
	}

	return value[:end]
}
