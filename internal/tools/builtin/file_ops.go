package builtin

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path/filepath"
	"runtime"
	"strings"

	einotool "github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"

	"github.com/sda1-hacker/humbert-agent/internal/sandbox"
	humberttools "github.com/sda1-hacker/humbert-agent/internal/tools"
)

const (
	copyFileToolName   = "copy_file"
	moveFileToolName   = "move_file"
	deleteFileToolName = "delete_file"
)

type CopyFileInput struct {
	Source       string `json:"source,omitempty" jsonschema:"description=Source file path. Use either source or attachment_id."`
	AttachmentID string `json:"attachment_id,omitempty" jsonschema:"description=Current-session attachment ID to copy into the destination. Use instead of source."`
	Destination  string `json:"destination" jsonschema:"description=Destination file. Relative paths are resolved from the workspace."`
	Overwrite    bool   `json:"overwrite,omitempty" jsonschema:"description=Allow replacing an existing destination file."`
}

type MoveFileInput struct {
	Source      string `json:"source" jsonschema:"description=Source file. Relative paths are resolved from the workspace."`
	Destination string `json:"destination" jsonschema:"description=Destination file. Relative paths are resolved from the workspace."`
	Overwrite   bool   `json:"overwrite,omitempty" jsonschema:"description=Allow replacing an existing destination file."`
}
type FileOperationOutput struct {
	Source       string `json:"source,omitempty"`
	AttachmentID string `json:"attachment_id,omitempty"`
	Destination  string `json:"destination,omitempty"`
	Path         string `json:"path,omitempty"`
	Bytes        int64  `json:"bytes,omitempty"`
}

type CopyFileFactory struct {
	maxBytes    int64
	attachments DocumentAttachmentReader
}

func NewCopyFileFactory(maxBytes int64, attachments DocumentAttachmentReader) (*CopyFileFactory, error) {
	if maxBytes <= 0 {
		return nil, errors.New("copy_file maxBytes 必须大于 0")
	}
	if attachments == nil {
		return nil, errors.New("copy_file 需要会话附件读取器")
	}
	return &CopyFileFactory{maxBytes: maxBytes, attachments: attachments}, nil
}
func (f *CopyFileFactory) Descriptor() humberttools.Descriptor {
	return humberttools.Descriptor{Name: copyFileToolName, Risk: humberttools.RiskWrite}
}
func (f *CopyFileFactory) Build(ctx context.Context, scope humberttools.Scope) (einotool.InvokableTool, error) {
	return utils.InferTool(copyFileToolName, "Copy a regular file or a current-session attachment to a writable Sandbox destination. Set exactly one of source (file path) or attachment_id (conversation attachment, including browser screenshots), plus destination. Set overwrite=true only when replacement is intended.", func(callCtx context.Context, input *CopyFileInput) (*FileOperationOutput, error) {
		return copyFile(callCtx, scope, input, f.maxBytes, false, f.attachments)
	})
}

type MoveFileFactory struct{ maxBytes int64 }

func NewMoveFileFactory(maxBytes int64) (*MoveFileFactory, error) {
	if maxBytes <= 0 {
		return nil, errors.New("move_file maxBytes 必须大于 0")
	}
	return &MoveFileFactory{maxBytes: maxBytes}, nil
}
func (f *MoveFileFactory) Descriptor() humberttools.Descriptor {
	return humberttools.Descriptor{Name: moveFileToolName, Risk: humberttools.RiskWrite}
}
func (f *MoveFileFactory) Build(ctx context.Context, scope humberttools.Scope) (einotool.InvokableTool, error) {
	return utils.InferTool(moveFileToolName, "Move one regular file. Both the source and destination must have FULL path access under the Agent Sandbox.", func(callCtx context.Context, input *MoveFileInput) (*FileOperationOutput, error) {
		if input == nil {
			return nil, errors.New("move_file 输入不能为空")
		}
		return copyFile(callCtx, scope, &CopyFileInput{Source: input.Source, Destination: input.Destination, Overwrite: input.Overwrite}, f.maxBytes, true, nil)
	})
}

func copyFile(ctx context.Context, scope humberttools.Scope, input *CopyFileInput, maxBytes int64, move bool, attachments DocumentAttachmentReader) (*FileOperationOutput, error) {
	if input == nil {
		return nil, errors.New("文件操作输入不能为空")
	}
	sourcePath, attachmentID := strings.TrimSpace(input.Source), strings.TrimSpace(input.AttachmentID)
	if move && attachmentID != "" {
		return nil, errors.New("move_file 不支持 attachment_id；请使用 copy_file")
	}
	if (sourcePath == "") == (attachmentID == "") {
		return nil, errors.New("必须且只能指定 source 或 attachment_id")
	}
	var src *sandboxTarget
	var err error
	if sourcePath != "" {
		operation := sandbox.OpRead
		if move {
			operation = sandbox.OpMoveSource
		}
		src, err = openSandboxTarget(ctx, scope, sourcePath, operation)
		if err != nil {
			return nil, fmt.Errorf("source 被 Sandbox 拒绝: %w", err)
		}
		defer src.Close()
	}
	destinationOperation := sandbox.OpModify
	if move {
		destinationOperation = sandbox.OpMoveTarget
	}
	dst, err := openSandboxTarget(ctx, scope, input.Destination, destinationOperation)
	if err != nil {
		return nil, fmt.Errorf("destination 被 Sandbox 拒绝: %w", err)
	}
	defer dst.Close()
	if dst.relative == "." || (src != nil && src.relative == ".") {
		return nil, errors.New("source/destination 必须是文件")
	}
	if src != nil && sameSandboxPath(src.absolute, dst.absolute) {
		return nil, errors.New("source 与 destination 不能指向同一个文件")
	}
	var data []byte
	limit := maxBytes
	if attachmentID != "" {
		if attachments == nil || scope.SessionID == "" {
			return nil, errors.New("当前会话附件不可用")
		}
		// 截图工具允许生成 8 MiB PNG。即便普通文件写入上限较低，复制附件也必须
		// 能完整保存同一工具链生成的截图；其余路径文件仍遵守 maxBytes。
		limit = max(limit, maxBrowserScreenshotBytes)
		data, err = attachments.ReadAttachment(ctx, scope.SessionID, attachmentID)
		if err != nil {
			return nil, fmt.Errorf("读取当前会话附件失败: %w", err)
		}
	} else {
		in, openErr := src.root.Open(src.relative)
		if openErr != nil {
			return nil, fmt.Errorf("打开 source 失败: %w", openErr)
		}
		defer in.Close()
		info, statErr := in.Stat()
		if statErr != nil {
			return nil, statErr
		}
		if !info.Mode().IsRegular() {
			return nil, errors.New("source 必须是普通文件")
		}
		if info.Size() > maxBytes {
			return nil, fmt.Errorf("文件大小 %d 超过限制 %d", info.Size(), maxBytes)
		}
		data, err = io.ReadAll(io.LimitReader(in, maxBytes+1))
		if err != nil {
			return nil, err
		}
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("文件超过限制 %d", limit)
	}
	if err := ensureRootParentDirectory(dst.root, dst.relative); err != nil {
		return nil, err
	}
	existed := false
	mode := fs.FileMode(0o600)
	di, statErr := dst.root.Lstat(dst.relative)
	switch {
	case statErr == nil:
		existed = true
		if di.Mode()&fs.ModeSymlink != 0 || !di.Mode().IsRegular() {
			return nil, errors.New("destination 必须是普通文件且不能是符号链接")
		}
		if !input.Overwrite {
			return nil, errors.New("destination 已存在；如确实要覆盖请设置 overwrite=true")
		}
		if di.Mode().Perm() != 0 {
			mode = di.Mode().Perm()
		}
	case errors.Is(statErr, fs.ErrNotExist):
	default:
		return nil, statErr
	}
	if err := atomicWriteWorkspaceFile(ctx, dst.root, dst.relative, data, mode, existed); err != nil {
		return nil, err
	}
	if move {
		if err := src.root.Remove(src.relative); err != nil {
			return nil, fmt.Errorf("目标已写入，但删除 source 失败: %w", err)
		}
	}
	output := &FileOperationOutput{AttachmentID: attachmentID, Destination: dst.display, Bytes: int64(len(data))}
	if src != nil {
		output.Source = src.display
	}
	return output, nil
}

type DeleteFileInput struct {
	Path string `json:"path" jsonschema:"description=Regular file to delete. Relative paths are resolved from the workspace."`
}
type DeleteFileFactory struct{}

func NewDeleteFileFactory() *DeleteFileFactory { return &DeleteFileFactory{} }
func (f *DeleteFileFactory) Descriptor() humberttools.Descriptor {
	return humberttools.Descriptor{Name: deleteFileToolName, Risk: humberttools.RiskWrite}
}
func (f *DeleteFileFactory) Build(ctx context.Context, scope humberttools.Scope) (einotool.InvokableTool, error) {
	return utils.InferTool(deleteFileToolName, "Delete one regular file inside a FULL-access Agent Sandbox path. Directories and symbolic links are refused.", func(callCtx context.Context, input *DeleteFileInput) (*FileOperationOutput, error) {
		if input == nil {
			return nil, errors.New("delete_file 输入不能为空")
		}
		target, err := openSandboxTarget(callCtx, scope, input.Path, sandbox.OpDelete)
		if err != nil {
			return nil, fmt.Errorf("delete_file 路径被 Sandbox 拒绝: %w", err)
		}
		defer target.Close()
		if target.relative == "." {
			return nil, errors.New("不能删除 Sandbox 根目录")
		}
		info, err := target.root.Lstat(target.relative)
		if err != nil {
			return nil, err
		}
		if info.Mode()&fs.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return nil, errors.New("delete_file 只允许删除普通文件，拒绝目录和符号链接")
		}
		if err := target.root.Remove(target.relative); err != nil {
			return nil, err
		}
		return &FileOperationOutput{Path: target.display}, nil
	})
}

func sameSandboxPath(a, b string) bool {
	a = filepath.Clean(a)
	b = filepath.Clean(b)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}
