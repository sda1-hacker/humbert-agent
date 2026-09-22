package databackup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/sda1-hacker/humbert-agent/internal/atomicfile"
)

type restorePlan struct {
	Archive string `json:"archive"`
	SHA256  string `json:"sha256"`
}

// ScheduleRestore 在运行中的应用内只记录经校验的恢复计划，真正切换留给下次启动。
func ScheduleRestore(ctx context.Context, root, archive string) error {
	root, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	archive, err = filepath.Abs(archive)
	if err != nil {
		return err
	}
	if err := realDirectory(root); err != nil {
		return err
	}
	if err := Verify(ctx, archive); err != nil {
		return err
	}
	digest, err := fileSHA256(ctx, archive)
	if err != nil {
		return err
	}
	return atomicfile.WriteJSON(ctx, filepath.Join(root, pendingRestoreName), 0o600, restorePlan{Archive: archive, SHA256: digest})
}

// ApplyPendingRestore 在桌面 Core 初始化前执行，确保没有任何 Store 或调度器打开旧数据。
func ApplyPendingRestore(ctx context.Context, root string) (string, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	if err := realDirectory(root); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		return "", err
	}
	path := filepath.Join(root, pendingRestoreName)
	var plan restorePlan
	if err := atomicfile.ReadJSON(ctx, path, &plan); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		return "", fmt.Errorf("读取待恢复计划失败: %w", err)
	}
	if plan.Archive == "" || plan.SHA256 == "" {
		return "", errors.New("待恢复计划无效")
	}
	digest, err := fileSHA256(ctx, plan.Archive)
	if err != nil {
		return "", err
	}
	if digest != plan.SHA256 {
		return "", errors.New("备份归档在选择后发生变化，拒绝恢复")
	}
	rollback, err := Restore(ctx, plan.Archive, root)
	if err != nil {
		return "", err
	}
	return rollback, nil
}

func fileSHA256(ctx context.Context, path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	buffer := make([]byte, 256<<10)
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		n, err := file.Read(buffer)
		if n > 0 {
			_, _ = hash.Write(buffer[:n])
		}
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", err
		}
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}
