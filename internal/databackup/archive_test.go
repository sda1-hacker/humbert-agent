package databackup

import (
	"archive/zip"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBackupVerifyRestore(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "data")
	if err := os.MkdirAll(filepath.Join(root, "agents", "one"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "agents", "one", "config.json"), []byte(`{"name":"one"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(parent, "backup.zip")
	if err := Create(context.Background(), root, archive); err != nil {
		t.Fatal(err)
	}
	if err := Verify(context.Background(), archive); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "agents", "one", "config.json"), []byte("changed"), 0o600); err != nil {
		t.Fatal(err)
	}
	rollback, err := Restore(context.Background(), archive, root)
	if err != nil {
		t.Fatal(err)
	}
	if rollback == "" {
		t.Fatal("原数据没有保留")
	}
	data, err := os.ReadFile(filepath.Join(root, "agents", "one", "config.json"))
	if err != nil || string(data) != `{"name":"one"}` {
		t.Fatalf("恢复内容不正确: %q %v", data, err)
	}
}

func TestLegacyPendingRestoreCanBeCancelled(t *testing.T) {
	ctx := context.Background()
	parent := t.TempDir()
	root := filepath.Join(parent, "data")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(parent, "backup.zip")
	if err := Create(ctx, root, archive); err != nil {
		t.Fatal(err)
	}
	if err := ScheduleRestore(ctx, root, archive); err != nil {
		t.Fatal(err)
	}
	status, err := PendingRestoreStatus(ctx, root)
	if err != nil || status.Archive != archive || status.Encrypted {
		t.Fatalf("status=%+v err=%v", status, err)
	}
	if err := CancelPendingRestore(ctx, root, nil); err != nil {
		t.Fatal(err)
	}
	if rollback, err := ApplyPendingRestore(ctx, root); err != nil || rollback != "" {
		t.Fatalf("取消后仍执行恢复: rollback=%q err=%v", rollback, err)
	}
}

func TestRejectTraversal(t *testing.T) {
	parent := t.TempDir()
	archive := filepath.Join(parent, "evil.zip")
	file, err := os.Create(archive)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(file)
	entry, err := writer.Create("../outside")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := entry.Write([]byte("bad")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if err := Verify(context.Background(), archive); err == nil || !strings.Contains(err.Error(), "不安全") {
		t.Fatalf("应拒绝路径穿越: %v", err)
	}
}

func TestScheduledRestoreAndCleanInstall(t *testing.T) {
	ctx := context.Background()
	parent := t.TempDir()
	root := filepath.Join(parent, "data")
	if rollback, err := ApplyPendingRestore(ctx, root); err != nil || rollback != "" {
		t.Fatalf("全新安装不应恢复数据: %q %v", rollback, err)
	}
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "config.json")
	if err := os.WriteFile(path, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(parent, "backup.zip")
	if err := Create(ctx, root, archive); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("modified"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ScheduleRestore(ctx, root, archive); err != nil {
		t.Fatal(err)
	}
	rollback, err := ApplyPendingRestore(ctx, root)
	if err != nil || rollback == "" {
		t.Fatalf("恢复失败: %q %v", rollback, err)
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != "original" {
		t.Fatalf("恢复内容: %q %v", got, err)
	}
	old, err := os.ReadFile(filepath.Join(rollback, "config.json"))
	if err != nil || string(old) != "modified" {
		t.Fatalf("回退内容: %q %v", old, err)
	}
	if _, err := os.Stat(filepath.Join(root, pendingRestoreName)); !os.IsNotExist(err) {
		t.Fatalf("恢复计划应移除: %v", err)
	}
	if again, err := ApplyPendingRestore(ctx, root); err != nil || again != "" {
		t.Fatalf("不应重复恢复: %q %v", again, err)
	}
}

func TestScheduledRestoreRejectsChangedArchive(t *testing.T) {
	ctx := context.Background()
	parent := t.TempDir()
	root := filepath.Join(parent, "data")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "config.json"), []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(parent, "backup.zip")
	if err := Create(ctx, root, archive); err != nil {
		t.Fatal(err)
	}
	if err := ScheduleRestore(ctx, root, archive); err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(archive, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write([]byte("changed")); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := ApplyPendingRestore(ctx, root); err == nil || !strings.Contains(err.Error(), "发生变化") {
		t.Fatalf("应拒绝变更的归档: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(root, "config.json"))
	if err != nil || string(got) != "old" {
		t.Fatalf("原数据不应变更: %q %v", got, err)
	}
}
