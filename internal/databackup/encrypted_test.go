package databackup

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestEncryptedBackupRoundTripAndWrongPassphrase(t *testing.T) {
	ctx := context.Background()
	parent := t.TempDir()
	root := filepath.Join(parent, "data")
	if err := os.MkdirAll(filepath.Join(root, "secrets"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "config.yaml"), []byte("app: humbert"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "secrets", "legacy.secret"), []byte("old-secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(parent, "backup.age")
	if err := CreateEncrypted(ctx, root, path, "long secure passphrase", map[string]string{"provider-id": "top-secret"}); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(content, []byte("top-secret")) || bytes.Contains(content, []byte("app: humbert")) {
		t.Fatal("加密备份泄露明文")
	}
	if err := VerifyEncrypted(ctx, path, "wrong passphrase"); err == nil {
		t.Fatal("错误口令必须失败")
	}
	if err := VerifyEncrypted(ctx, path, "long secure passphrase"); err != nil {
		t.Fatal(err)
	}
	archiveFile, archiveReader, err := openEncryptedArchive(path, "long secure passphrase")
	if err != nil {
		t.Fatal(err)
	}
	stage := filepath.Join(parent, "stage")
	if err := os.Mkdir(stage, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := inspectReader(ctx, archiveReader, stage); err != nil {
		t.Fatal(err)
	}
	if err := archiveFile.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(stage, credentialExportName)); !os.IsNotExist(err) {
		t.Fatalf("暂存目录不应写入明文凭据: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "config.yaml"), []byte("changed"), 0o600); err != nil {
		t.Fatal(err)
	}
	rollback, secrets, err := RestoreEncrypted(ctx, path, root, "long secure passphrase")
	if err != nil || rollback == "" {
		t.Fatalf("restore=%q err=%v", rollback, err)
	}
	if secrets["provider-id"] != "top-secret" {
		t.Fatal("凭据导出丢失")
	}
	data, err := os.ReadFile(filepath.Join(root, "config.yaml"))
	if err != nil || string(data) != "app: humbert" {
		t.Fatalf("恢复内容=%q err=%v", data, err)
	}
	if _, err := os.Stat(filepath.Join(root, "secrets", "legacy.secret")); !os.IsNotExist(err) {
		t.Fatalf("加密备份不应包含旧明文凭据: %v", err)
	}
}
