package builtin

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"unicode/utf8"

	einotool "github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"

	"github.com/sda1-hacker/humbert-agent/internal/sandbox"
	humberttools "github.com/sda1-hacker/humbert-agent/internal/tools"
	"github.com/sda1-hacker/humbert-agent/internal/workspace"
)

const (
	writeFileToolName = "write_file"

	writeFileToolDescription = `在 Agent Sandbox 允许写入的目录中创建或完整覆盖 UTF-8 文本文件。

相对 path 以当前 Workspace 为基准；如果用户显式配置了 Additional Write Path，也可以使用该目录的绝对路径。
Permission 允许不会扩大 Sandbox 写入边界。

默认不覆盖已经存在的文件；只有明确传入 overwrite=true 才允许完整覆盖。
如果只是修改已有文件的一小部分，应优先使用 edit_file。`
)

// WriteFileInput 是 write_file 的模型输入。
type WriteFileInput struct {
	// Path 是当前 Workspace 内的相对文件路径。
	Path string `json:"path" jsonschema:"description=Relative file path inside the current workspace."`

	// Content 是要写入文件的完整 UTF-8 内容。
	Content string `json:"content" jsonschema:"description=Complete UTF-8 text content to write."`

	// Overwrite 控制是否允许完整覆盖已经存在的普通文件。
	Overwrite bool `json:"overwrite,omitempty" jsonschema:"description=Set true only when intentionally replacing an existing file completely."`
}

// WriteFileOutput 是 write_file 的结构化返回。
type WriteFileOutput struct {
	Path string `json:"path"`

	BytesWritten int `json:"bytes_written"`

	Created bool `json:"created"`

	Overwritten bool `json:"overwritten"`
}

// WriteFileFactory 为每个 RuntimeSnapshot 创建绑定 Workspace 的 write_file。
//
// Factory 只持有长期、线程安全依赖。真正的 AgentID / Workspace Root 由 Build
// 时传入的不可变 Scope 冻结，因此用户在 Turn 执行期间切换项目不会改变本 Tool
// 的写入目录。
type WriteFileFactory struct {
	workspaces *workspace.Manager

	maxWritableBytes int64
}

// NewWriteFileFactory 创建 write_file Factory。
func NewWriteFileFactory(workspaceManager *workspace.Manager, maxWritableBytes int64) (*WriteFileFactory, error) {
	if workspaceManager == nil {
		return nil, errors.New("WriteFile WorkspaceManager 不能为空")
	}
	if maxWritableBytes <= 0 {
		return nil, errors.New("WriteFile MaxWritableBytes 必须大于 0")
	}

	return &WriteFileFactory{
		workspaces:       workspaceManager,
		maxWritableBytes: maxWritableBytes,
	}, nil
}

// Descriptor 返回 Humbert Registry 描述。
func (f *WriteFileFactory) Descriptor() humberttools.Descriptor {
	return humberttools.Descriptor{
		Name: writeFileToolName,
		Risk: humberttools.RiskWrite,
	}
}

// Build 创建绑定当前 Runtime Workspace 的 Eino Tool。
func (f *WriteFileFactory) Build(ctx context.Context, scope humberttools.Scope) (einotool.InvokableTool, error) {
	if ctx == nil {
		return nil, errors.New("构建 write_file 失败: context.Context 不能为空")
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("构建 write_file 被取消: %w", err)
	}

	return utils.InferTool(
		writeFileToolName,
		writeFileToolDescription,
		func(callCtx context.Context, input *WriteFileInput) (*WriteFileOutput, error) {
			return f.run(callCtx, scope, input)
		},
	)
}

func (f *WriteFileFactory) run(ctx context.Context, scope humberttools.Scope, input *WriteFileInput) (*WriteFileOutput, error) {
	if ctx == nil {
		return nil, errors.New("write_file: context.Context 不能为空")
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("write_file 被取消: %w", err)
	}
	if input == nil {
		return nil, errors.New("write_file 输入不能为空")
	}
	if !utf8Text(input.Content) {
		return nil, errors.New("write_file 只允许写入 UTF-8 文本")
	}
	if int64(len(input.Content)) > f.maxWritableBytes {
		return nil, fmt.Errorf(
			"write_file 内容过大: %d bytes，最大允许 %d bytes",
			len(input.Content),
			f.maxWritableBytes,
		)
	}

	target, err := openSandboxTarget(ctx, scope, input.Path, sandbox.OpModify)
	if err != nil {
		return nil, fmt.Errorf("write_file 路径被 Sandbox 拒绝: %w", err)
	}
	defer target.Close()
	if target.relative == "." {
		return nil, errors.New("write_file 必须指定文件路径")
	}
	relativePath := target.relative
	root := target.root

	if err := ensureRootParentDirectory(root, relativePath); err != nil {
		return nil, fmt.Errorf("write_file 创建父目录失败: %w", err)
	}

	existed := false
	fileMode := fs.FileMode(0o600)
	info, statErr := root.Lstat(relativePath)
	switch {
	case statErr == nil:
		existed = true

		if info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf(
				"write_file 拒绝写入符号链接: %q",
				filepath.ToSlash(relativePath),
			)
		}

		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf(
				"write_file 目标不是普通文件: %q",
				filepath.ToSlash(relativePath),
			)
		}

		if !input.Overwrite {
			return nil, fmt.Errorf(
				"write_file 目标已经存在: %q；若确实要完整覆盖，请设置 overwrite=true",
				filepath.ToSlash(relativePath),
			)
		}

		if mode := info.Mode().Perm(); mode != 0 {
			fileMode = mode
		}

	case errors.Is(statErr, fs.ErrNotExist):
		// 正常的新建文件场景。

	default:
		return nil, fmt.Errorf(
			"write_file 检查目标状态失败: %w",
			statErr,
		)
	}

	if err := atomicWriteWorkspaceFile(
		ctx,
		root,
		relativePath,
		[]byte(input.Content),
		fileMode,
		existed,
	); err != nil {
		return nil, fmt.Errorf(
			"write_file 写入失败: %w",
			err,
		)
	}

	return &WriteFileOutput{
		Path:         target.display,
		BytesWritten: len(input.Content),
		Created:      !existed,
		Overwritten:  existed,
	}, nil
}

func normalizeWritableFilePath(input string) (string, error) {
	path, err := workspace.NormalizeRelativePath(input)
	if err != nil {
		return "", err
	}

	if path == "." {
		return "", errors.New(
			"必须指定文件路径，不能写入 Workspace 根目录本身",
		)
	}

	return path, nil
}

func ensureRootParentDirectory(root *os.Root, path string) error {
	parent := filepath.Dir(path)

	if parent == "." || parent == "" {
		return nil
	}

	if err := root.MkdirAll(parent, 0o700); err != nil {
		return fmt.Errorf(
			"创建目录 %q 失败: %w",
			filepath.ToSlash(parent),
			err,
		)
	}

	return nil
}

// atomicWriteWorkspaceFile 使用同目录临时文件提交 Workspace 文本变更。
//
// 正常路径使用 Rename 原子替换。部分 Windows 文件系统不允许 Rename 直接覆盖
// 已存在目标，此时使用 backup -> replace -> cleanup 的回退流程，并在提交失败时
// 尽可能恢复旧文件。
func atomicWriteWorkspaceFile(
	ctx context.Context,
	root *os.Root,
	path string,
	data []byte,
	perm fs.FileMode,
	targetExists bool,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	tempPath, err := siblingTemporaryPath(
		path,
		".humbert-write-",
	)
	if err != nil {
		return err
	}

	file, err := root.OpenFile(
		tempPath,
		os.O_WRONLY|os.O_CREATE|os.O_EXCL,
		perm,
	)
	if err != nil {
		return fmt.Errorf(
			"创建临时文件失败: %w",
			err,
		)
	}

	committed := false

	defer func() {
		_ = file.Close()

		if !committed {
			_ = root.Remove(tempPath)
		}
	}()

	if err := writeAllWithContext(
		ctx,
		file,
		data,
	); err != nil {
		return fmt.Errorf(
			"写入临时文件失败: %w",
			err,
		)
	}

	if err := file.Sync(); err != nil {
		return fmt.Errorf(
			"同步临时文件失败: %w",
			err,
		)
	}

	if err := file.Close(); err != nil {
		return fmt.Errorf(
			"关闭临时文件失败: %w",
			err,
		)
	}

	if err := ctx.Err(); err != nil {
		return err
	}

	if err := root.Rename(
		tempPath,
		path,
	); err == nil {
		committed = true

		return syncRootParent(
			root,
			path,
		)
	} else if !targetExists {
		return fmt.Errorf(
			"提交新文件失败: %w",
			err,
		)
	}

	backupPath, err := siblingTemporaryPath(
		path,
		".humbert-backup-",
	)
	if err != nil {
		return err
	}

	if err := root.Rename(
		path,
		backupPath,
	); err != nil {
		return fmt.Errorf(
			"备份旧文件失败: %w",
			err,
		)
	}

	restored := false

	defer func() {
		if !committed && !restored {
			_ = root.Rename(
				backupPath,
				path,
			)
		}
	}()

	if err := root.Rename(
		tempPath,
		path,
	); err != nil {
		if restoreErr := root.Rename(
			backupPath,
			path,
		); restoreErr != nil {
			return fmt.Errorf(
				"提交新文件失败: %v；恢复旧文件也失败: %w",
				err,
				restoreErr,
			)
		}

		restored = true

		return fmt.Errorf(
			"提交新文件失败，旧文件已恢复: %w",
			err,
		)
	}

	committed = true

	if err := root.Remove(
		backupPath,
	); err != nil &&
		!errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf(
			"清理旧文件备份失败: %w",
			err,
		)
	}

	return syncRootParent(
		root,
		path,
	)
}

func siblingTemporaryPath(path, prefix string) (string, error) {
	var random [8]byte

	if _, err := rand.Read(random[:]); err != nil {
		return "", fmt.Errorf(
			"生成临时文件名失败: %w",
			err,
		)
	}

	name := fmt.Sprintf(
		"%s%x.tmp",
		prefix,
		random[:],
	)

	parent := filepath.Dir(path)

	if parent == "." || parent == "" {
		return name, nil
	}

	return filepath.Join(
		parent,
		name,
	), nil
}

func writeAllWithContext(
	ctx context.Context,
	writer io.Writer,
	data []byte,
) error {
	const chunkSize = 64 * 1024

	for len(data) > 0 {
		if err := ctx.Err(); err != nil {
			return err
		}

		size := chunkSize

		if len(data) < size {
			size = len(data)
		}

		n, err := writer.Write(
			data[:size],
		)
		if err != nil {
			return err
		}

		if n <= 0 {
			return io.ErrShortWrite
		}

		data = data[n:]
	}

	return nil
}

func syncRootParent(root *os.Root, path string) error {
	if runtime.GOOS == "windows" {
		return nil
	}

	parent := filepath.Dir(path)

	if parent == "" {
		parent = "."
	}

	directory, err := root.Open(parent)
	if err != nil {
		return fmt.Errorf(
			"打开父目录用于同步失败: %w",
			err,
		)
	}
	defer directory.Close()

	if err := directory.Sync(); err != nil {
		return fmt.Errorf(
			"同步父目录失败: %w",
			err,
		)
	}

	return nil
}

func utf8Text(value string) bool {
	// Go string 本身可以包含任意字节，因此仍需要显式校验 UTF-8。
	return utf8.ValidString(value)
}
