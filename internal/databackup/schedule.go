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
	"time"

	"github.com/sda1-hacker/humbert-agent/internal/atomicfile"
)

type restorePlan struct {
	Archive     string `json:"archive"`
	SHA256      string `json:"sha256"`
	Encrypted   bool   `json:"encrypted,omitempty"`
	ScheduledAt string `json:"scheduledAt,omitempty"`
}

type backupPlan struct {
	Destination string `json:"destination"`
}

type BackupStatus struct {
	Destination string `json:"destination"`
	LastError   string `json:"lastError"`
}

// RestoreStatus 只暴露待执行计划的非敏感信息，口令始终留在系统凭据库。
type RestoreStatus struct {
	Archive     string `json:"archive"`
	Encrypted   bool   `json:"encrypted"`
	ScheduledAt string `json:"scheduledAt,omitempty"`
}

const backupErrorName = ".pending-backup-error.json"

type PassphraseVault interface {
	Set(id, value string) error
	Get(id string) (string, error)
	Delete(id string) error
}

const backupVaultID = "pending-backup"
const restoreVaultID = "pending-restore"

func ScheduleEncryptedBackup(ctx context.Context, root, destination, passphrase string, vault PassphraseVault) error {
	if vault == nil {
		return errors.New("系统凭据库不可用")
	}
	if len([]rune(passphrase)) < 12 {
		return errors.New("备份口令至少需要 12 个字符")
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	destination, err = filepath.Abs(destination)
	if err != nil {
		return err
	}
	if err := realDirectory(root); err != nil {
		return err
	}
	if within(root, destination) {
		return errors.New("备份文件不能位于数据目录内")
	}
	if err := realDirectory(filepath.Dir(destination)); err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(root, pendingBackupName)); err == nil {
		return errors.New("已有待执行的数据备份，请先取消")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if _, err := os.Stat(filepath.Join(root, pendingRestoreName)); err == nil {
		return errors.New("已有待执行的数据恢复")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := vault.Set(backupVaultID, passphrase); err != nil {
		return fmt.Errorf("暂存备份口令失败: %w", err)
	}
	if err := atomicfile.WriteJSON(ctx, filepath.Join(root, pendingBackupName), 0o600, backupPlan{Destination: destination}); err != nil {
		_ = vault.Delete(backupVaultID)
		return err
	}
	_ = os.Remove(filepath.Join(root, backupErrorName))
	return nil
}

func PendingBackupStatus(ctx context.Context, root string) (BackupStatus, error) {
	var plan backupPlan
	if err := atomicfile.ReadJSON(ctx, filepath.Join(root, pendingBackupName), &plan); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return BackupStatus{}, nil
		}
		return BackupStatus{}, err
	}
	status := BackupStatus{Destination: plan.Destination}
	var recorded struct {
		Message string `json:"message"`
	}
	if err := atomicfile.ReadJSON(ctx, filepath.Join(root, backupErrorName), &recorded); err != nil && !errors.Is(err, os.ErrNotExist) {
		return BackupStatus{}, err
	}
	status.LastError = recorded.Message
	return status, nil
}

// RecordBackupFailure keeps the plan so the next offline startup can retry.
func RecordBackupFailure(ctx context.Context, root string, cause error) error {
	if cause == nil {
		return nil
	}
	return atomicfile.WriteJSON(ctx, filepath.Join(root, backupErrorName), 0o600, struct {
		Message string `json:"message"`
	}{Message: cause.Error()})
}

func CancelPendingBackup(ctx context.Context, root string, vault PassphraseVault) error {
	if vault == nil {
		return errors.New("系统凭据库不可用")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.Remove(filepath.Join(root, pendingBackupName)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	_ = os.Remove(filepath.Join(root, backupErrorName))
	if err := vault.Delete(backupVaultID); err != nil {
		return err
	}
	return nil
}

func ApplyPendingBackup(ctx context.Context, root string, vault PassphraseVault, exportCredentials func(context.Context) (map[string]string, error)) (string, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	if err := realDirectory(root); errors.Is(err, os.ErrNotExist) {
		return "", nil
	} else if err != nil {
		return "", err
	}
	var plan backupPlan
	path := filepath.Join(root, pendingBackupName)
	if err := atomicfile.ReadJSON(ctx, path, &plan); errors.Is(err, os.ErrNotExist) {
		return "", nil
	} else if err != nil {
		return "", err
	}
	if plan.Destination == "" || vault == nil || exportCredentials == nil {
		return "", errors.New("待备份计划无效")
	}
	passphrase, err := vault.Get(backupVaultID)
	if err != nil {
		return "", err
	}
	credentials, err := exportCredentials(ctx)
	if err != nil {
		return "", err
	}
	if err := CreateEncrypted(ctx, root, plan.Destination, passphrase, credentials); err != nil {
		return "", err
	}
	if err := os.Remove(path); err != nil {
		return "", err
	}
	if err := vault.Delete(backupVaultID); err != nil {
		return plan.Destination, err
	}
	_ = os.Remove(filepath.Join(root, backupErrorName))
	return plan.Destination, nil
}

func ScheduleEncryptedRestore(ctx context.Context, root, archive, passphrase string, vault PassphraseVault) error {
	if vault == nil {
		return errors.New("系统凭据库不可用")
	}
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
	if _, err := os.Stat(filepath.Join(root, pendingBackupName)); err == nil {
		return errors.New("已有待执行的数据备份")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := VerifyEncrypted(ctx, archive, passphrase); err != nil {
		return err
	}
	digest, err := fileSHA256(ctx, archive)
	if err != nil {
		return err
	}
	if err := vault.Set(restoreVaultID, passphrase); err != nil {
		return err
	}
	if err := atomicfile.WriteJSON(ctx, filepath.Join(root, pendingRestoreName), 0o600, restorePlan{Archive: archive, SHA256: digest, Encrypted: true, ScheduledAt: time.Now().Format(time.RFC3339)}); err != nil {
		_ = vault.Delete(restoreVaultID)
		return err
	}
	return nil
}

type RestoreOptions struct {
	Vault             PassphraseVault
	ImportCredentials func(context.Context, string, map[string]string) error
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
	if _, err := os.Stat(filepath.Join(root, pendingBackupName)); err == nil {
		return errors.New("已有待执行的数据备份")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := Verify(ctx, archive); err != nil {
		return err
	}
	digest, err := fileSHA256(ctx, archive)
	if err != nil {
		return err
	}
	return atomicfile.WriteJSON(ctx, filepath.Join(root, pendingRestoreName), 0o600, restorePlan{Archive: archive, SHA256: digest, ScheduledAt: time.Now().Format(time.RFC3339)})
}

func PendingRestoreStatus(ctx context.Context, root string) (RestoreStatus, error) {
	var plan restorePlan
	if err := atomicfile.ReadJSON(ctx, filepath.Join(root, pendingRestoreName), &plan); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return RestoreStatus{}, nil
		}
		return RestoreStatus{}, err
	}
	return RestoreStatus{Archive: plan.Archive, Encrypted: plan.Encrypted, ScheduledAt: plan.ScheduledAt}, nil
}

// CancelPendingRestore 在应用仍运行时撤销离线恢复，并清理暂存口令。
func CancelPendingRestore(ctx context.Context, root string, vault PassphraseVault) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.Remove(filepath.Join(root, pendingRestoreName)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if vault != nil {
		return vault.Delete(restoreVaultID)
	}
	return nil
}

// ApplyPendingRestore 在桌面 Core 初始化前执行，确保没有任何 Store 或调度器打开旧数据。
func ApplyPendingRestore(ctx context.Context, root string, options ...RestoreOptions) (string, error) {
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
	if plan.Encrypted {
		if len(options) == 0 || options[0].Vault == nil || options[0].ImportCredentials == nil {
			return "", errors.New("加密恢复缺少系统凭据库")
		}
		passphrase, err := options[0].Vault.Get(restoreVaultID)
		if err != nil {
			return "", err
		}
		rollback, err := RestoreEncryptedAndImport(ctx, plan.Archive, root, passphrase, options[0].ImportCredentials)
		if err != nil {
			return "", err
		}
		if err := options[0].Vault.Delete(restoreVaultID); err != nil {
			return rollback, err
		}
		return rollback, nil
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
