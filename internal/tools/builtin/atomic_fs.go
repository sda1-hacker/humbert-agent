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
)

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
	expected fileVersion,
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

	if err := expected.check(ctx, root, path); err != nil {
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
	} else if expected.info == nil || runtime.GOOS != "windows" {
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
