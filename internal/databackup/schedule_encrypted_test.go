package databackup

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

type fakeVault map[string]string

func (v fakeVault) Set(id, value string) error { v[id] = value; return nil }
func (v fakeVault) Get(id string) (string, error) {
	value, ok := v[id]
	if !ok {
		return "", errors.New("missing password")
	}
	return value, nil
}
func (v fakeVault) Delete(id string) error { delete(v, id); return nil }

func TestScheduledEncryptedBackupUsesNextStartupSnapshot(t *testing.T) {
	ctx := context.Background()
	parent := t.TempDir()
	root := filepath.Join(parent, "data")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, "config.yaml")
	if err := os.WriteFile(file, []byte("before"), 0o600); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(parent, "backup.age")
	vault := fakeVault{}
	if err := ScheduleEncryptedBackup(ctx, root, archive, "long secure passphrase", vault); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("after"), 0o600); err != nil {
		t.Fatal(err)
	}
	path, err := ApplyPendingBackup(ctx, root, vault, func(context.Context) (map[string]string, error) {
		return map[string]string{"secret-id": "secret-value"}, nil
	})
	if err != nil || path != archive {
		t.Fatalf("backup=%q err=%v", path, err)
	}
	if _, err := os.Stat(filepath.Join(root, pendingBackupName)); !os.IsNotExist(err) {
		t.Fatalf("计划未清除: %v", err)
	}
	if _, err := vault.Get(backupVaultID); err == nil {
		t.Fatal("临时口令未清除")
	}
	if err := os.WriteFile(file, []byte("changed"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ScheduleEncryptedRestore(ctx, root, archive, "long secure passphrase", vault); err != nil {
		t.Fatal(err)
	}
	imported := false
	rollback, err := ApplyPendingRestore(ctx, root, RestoreOptions{Vault: vault, ImportCredentials: func(_ context.Context, _ string, values map[string]string) error {
		imported = values["secret-id"] == "secret-value"
		return nil
	}})
	if err != nil || rollback == "" || !imported {
		t.Fatalf("restore=%q imported=%v err=%v", rollback, imported, err)
	}
	data, err := os.ReadFile(file)
	if err != nil || string(data) != "after" {
		t.Fatalf("恢复应使用重启前快照: %q %v", data, err)
	}
}

func TestEncryptedRestoreImportFailureRollsBackDirectory(t *testing.T) {
	ctx := context.Background()
	parent := t.TempDir()
	root := filepath.Join(parent, "data")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, "config.yaml")
	if err := os.WriteFile(file, []byte("backup"), 0o600); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(parent, "backup.age")
	if err := CreateEncrypted(ctx, root, archive, "long secure passphrase", map[string]string{"id": "value"}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("current"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := RestoreEncryptedAndImport(ctx, archive, root, "long secure passphrase", func(context.Context, string, map[string]string) error { return errors.New("keychain unavailable") })
	if err == nil {
		t.Fatal("导入失败必须报告")
	}
	data, readErr := os.ReadFile(file)
	if readErr != nil || string(data) != "current" {
		t.Fatalf("原数据未恢复: %q %v", data, readErr)
	}
}

func TestPendingRestoreCanBeInspectedAndCancelled(t *testing.T) {
	ctx := context.Background()
	parent := t.TempDir()
	root := filepath.Join(parent, "data")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(parent, "backup.age")
	if err := CreateEncrypted(ctx, root, archive, "long secure passphrase", nil); err != nil {
		t.Fatal(err)
	}
	vault := fakeVault{}
	if err := ScheduleEncryptedRestore(ctx, root, archive, "long secure passphrase", vault); err != nil {
		t.Fatal(err)
	}
	status, err := PendingRestoreStatus(ctx, root)
	if err != nil || status.Archive != archive || !status.Encrypted {
		t.Fatalf("status=%+v err=%v", status, err)
	}
	if err := CancelPendingRestore(ctx, root, vault); err != nil {
		t.Fatal(err)
	}
	status, err = PendingRestoreStatus(ctx, root)
	if err != nil || status.Archive != "" {
		t.Fatalf("cancelled status=%+v err=%v", status, err)
	}
	if _, err := vault.Get(restoreVaultID); err == nil {
		t.Fatal("暂存口令未清理")
	}
}

func TestFailedScheduledBackupCanBeInspectedAndCancelled(t *testing.T) {
	ctx := context.Background()
	parent := t.TempDir()
	root := filepath.Join(parent, "data")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	vault := fakeVault{}
	archive := filepath.Join(parent, "backup.age")
	if err := ScheduleEncryptedBackup(ctx, root, archive, "long secure passphrase", vault); err != nil {
		t.Fatal(err)
	}
	if err := RecordBackupFailure(ctx, root, errors.New("destination unavailable")); err != nil {
		t.Fatal(err)
	}
	status, err := PendingBackupStatus(ctx, root)
	if err != nil || status.Destination != archive || status.LastError != "destination unavailable" {
		t.Fatalf("status=%#v err=%v", status, err)
	}
	if err := CancelPendingBackup(ctx, root, vault); err != nil {
		t.Fatal(err)
	}
	status, err = PendingBackupStatus(ctx, root)
	if err != nil || status.Destination != "" {
		t.Fatalf("cancelled status=%#v err=%v", status, err)
	}
	if _, err := vault.Get(backupVaultID); err == nil {
		t.Fatal("cancel did not remove passphrase")
	}
}
