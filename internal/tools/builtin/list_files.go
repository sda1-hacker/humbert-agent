package builtin

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"

	einotool "github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"

	"github.com/sda1-hacker/humbert-agent/internal/sandbox"
	humberttools "github.com/sda1-hacker/humbert-agent/internal/tools"
	"github.com/sda1-hacker/humbert-agent/internal/workspace"
)

const (
	listFilesToolName = "list_files"

	listFilesToolDescription = `列出 Agent 当前 Workspace 中指定目录的直接子项。

默认只能访问当前 Workspace；如果用户在 Agent Sandbox 中显式加入额外只读/读写目录，也可以访问那些目录。
相对路径仍以 Workspace 为基准；访问额外目录时可以传入它的绝对路径。所有路径都会再次经过 Sandbox PathGuard。

该工具只列出一层目录，不会递归遍历。`
)

// ListFilesInput 是 list_files 的模型输入。
type ListFilesInput struct {
	// Path 是相对于当前 Agent Workspace 的目录路径。
	//
	// 为空时等价于 "."。
	Path string `json:"path,omitempty" jsonschema:"description=Relative directory path inside the current workspace. Use '.' for workspace root."`
}

// ListFilesEntry 是单个目录项的稳定返回结构。
type ListFilesEntry struct {
	Name string `json:"name"`

	Path string `json:"path"`

	Type string `json:"type"`

	Size int64 `json:"size,omitempty"`

	ModifiedAt string `json:"modified_at,omitempty"`
}

// ListFilesOutput 是 list_files 返回给模型的数据。
type ListFilesOutput struct {
	Path string `json:"path"`

	Entries []ListFilesEntry `json:"entries"`

	// Truncated 表示目录项超过 MaxListEntries，
	// 当前结果只包含前一部分。
	Truncated bool `json:"truncated"`
}

// ListFilesFactory 为每个 RuntimeSnapshot 创建绑定 Workspace 的
// list_files Tool。
type ListFilesFactory struct {
	workspaces *workspace.Manager

	limits FileLimits
}

// NewListFilesFactory 创建 list_files Factory。
func NewListFilesFactory(
	workspaceManager *workspace.Manager,
	limits FileLimits,
) (
	*ListFilesFactory,
	error,
) {
	if workspaceManager == nil {
		return nil, errors.New(
			"ListFiles WorkspaceManager 不能为空",
		)
	}

	if err :=
		limits.Validate(); err != nil {

		return nil, fmt.Errorf(
			"ListFiles Limits 无效: %w",
			err,
		)
	}

	return &ListFilesFactory{
		workspaces: workspaceManager,

		limits: limits,
	}, nil
}

// Descriptor 返回 Humbert Registry 描述。
func (f *ListFilesFactory) Descriptor() humberttools.Descriptor {
	return humberttools.Descriptor{
		Name: listFilesToolName,

		Risk: humberttools.RiskRead,
	}
}

// Build 创建绑定当前 Runtime Workspace 的 Eino Tool。
func (f *ListFilesFactory) Build(
	ctx context.Context,
	scope humberttools.Scope,
) (
	einotool.InvokableTool,
	error,
) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf(
			"构建 list_files 被取消: %w",
			err,
		)
	}

	return utils.InferTool(
		listFilesToolName,
		listFilesToolDescription,
		func(
			callCtx context.Context,
			input *ListFilesInput,
		) (
			*ListFilesOutput,
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

func (f *ListFilesFactory) run(
	ctx context.Context,
	scope humberttools.Scope,
	input *ListFilesInput,
) (
	*ListFilesOutput,
	error,
) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf(
			"list_files 被取消: %w",
			err,
		)
	}

	if input == nil {
		input =
			&ListFilesInput{}
	}

	target, err := openSandboxTarget(ctx, scope, input.Path, sandbox.OpList)
	if err != nil {
		return nil, fmt.Errorf("list_files 路径被 Sandbox 拒绝: %w", err)
	}
	defer target.Close()

	directory, err :=
		target.root.Open(target.relative)

	if err != nil {
		return nil, fmt.Errorf(
			"打开目录 %q 失败: %w",
			target.display,
			err,
		)
	}

	defer directory.Close()

	info, err :=
		directory.Stat()

	if err != nil {
		return nil, fmt.Errorf(
			"读取目录 %q 状态失败: %w",
			target.display,
			err,
		)
	}

	if !info.IsDir() {
		return nil, fmt.Errorf(
			"%q 不是目录",
			target.display,
		)
	}

	// 多读取一项，用于判断是否发生截断。
	entries, err :=
		directory.ReadDir(
			f.limits.
				MaxListEntries +
				1,
		)

	if err != nil &&
		!errors.Is(
			err,
			io.EOF,
		) {

		return nil, fmt.Errorf(
			"读取目录 %q 失败: %w",
			target.display,
			err,
		)
	}

	truncated :=
		len(entries) >
			f.limits.MaxListEntries

	if truncated {
		entries =
			entries[:f.limits.MaxListEntries]
	}

	result :=
		make(
			[]ListFilesEntry,
			0,
			len(entries),
		)

	for _, entry := range entries {

		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf(
				"list_files 被取消: %w",
				err,
			)
		}

		item :=
			ListFilesEntry{
				Name: entry.Name(),

				Path: filepath.ToSlash(
					filepath.Join(
						target.display,
						entry.Name(),
					),
				),

				Type: fileEntryType(
					entry,
				),
			}

		entryInfo, infoErr :=
			entry.Info()

		if infoErr == nil {
			item.Size =
				entryInfo.Size()

			item.ModifiedAt =
				entryInfo.
					ModTime().
					UTC().
					Format(
						time.RFC3339,
					)
		}

		result = append(
			result,
			item,
		)
	}

	// 返回顺序稳定，避免文件系统目录枚举顺序影响模型行为和测试结果。
	sort.Slice(
		result,
		func(
			i int,
			j int,
		) bool {
			return result[i].Name <
				result[j].Name
		},
	)

	return &ListFilesOutput{
		Path: target.display,

		Entries: result,

		Truncated: truncated,
	}, nil
}

func fileEntryType(
	entry os.DirEntry,
) string {
	mode :=
		entry.Type()

	switch {
	case mode&os.ModeSymlink != 0:
		return "symlink"

	case entry.IsDir():
		return "directory"

	case mode.IsRegular():
		return "file"

	default:
		return "other"
	}
}
