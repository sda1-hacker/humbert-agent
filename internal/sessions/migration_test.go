package sessions

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/sda1-hacker/humbert-agent/internal/atomicfile"
)

func TestMigrateLegacySessionConfigPreservesOriginal(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	created := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	legacy := map[string]any{"schema_version": 2, "session": map[string]any{
		"id": "session-one", "agent_id": "agent-one", "project_id": "old-project",
		"title": "保留标题", "cwd": "/tmp", "created_at": created,
	}}
	if err := atomicfile.WriteJSON(context.Background(), path, 0o600, legacy); err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var raw json.RawMessage
	if err := atomicfile.ReadJSON(context.Background(), path, &raw); err != nil {
		t.Fatal(err)
	}
	if err := migrateLegacySessionConfig(context.Background(), path, raw, "agent-one", "session-one"); err != nil {
		t.Fatal(err)
	}
	backup, err := os.ReadFile(path + ".pre-v3.2")
	if err != nil {
		t.Fatal(err)
	}
	var originalJSON, backupJSON any
	if err := json.Unmarshal(original, &originalJSON); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(backup, &backupJSON); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(originalJSON, backupJSON) {
		t.Fatal("升级前副本与原配置不同")
	}
	var upgraded sessionDocument
	if err := atomicfile.ReadJSON(context.Background(), path, &upgraded); err != nil {
		t.Fatal(err)
	}
	if upgraded.SchemaVersion != 3 || upgraded.Session.Title != "保留标题" || !upgraded.Session.CreatedAt.Equal(created) {
		t.Fatalf("迁移结果错误: %+v", upgraded)
	}
}
