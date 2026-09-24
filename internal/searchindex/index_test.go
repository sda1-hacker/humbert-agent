package searchindex

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestSearchReadsCommittedSnapshotWhileIndexUpdates(t *testing.T) {
	ctx := context.Background()
	index, err := Open(filepath.Join(t.TempDir(), "search.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer index.Close()
	session := Session{ID: "s1", AgentID: "a1", Title: "测试", Revision: 1}
	if err := index.Replace(ctx, session, []Message{{EntryID: "old", Role: "user", Content: "旧消息内容"}}); err != nil {
		t.Fatal(err)
	}
	started, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		session.Revision = 2
		done <- index.ReplaceStream(ctx, session, func(add func(Message) error) error {
			close(started)
			<-release
			return add(Message{EntryID: "new", Role: "user", Content: "新消息内容"})
		})
	}()
	<-started
	readCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	results, err := index.Search(readCtx, "旧消息", 10)
	close(release)
	if err != nil || len(results) != 1 || results[0].EntryID != "old" {
		t.Fatalf("更新中读取快照失败: results=%+v err=%v", results, err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	results, err = index.Search(ctx, "新消息", 10)
	if err != nil || len(results) != 1 || results[0].EntryID != "new" {
		t.Fatalf("更新后索引未生效: results=%+v err=%v", results, err)
	}
}

func TestSearchReplaceAndPrune(t *testing.T) {
	ctx := context.Background()
	index, err := Open(filepath.Join(t.TempDir(), "search.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer index.Close()
	session := Session{ID: "s1", AgentID: "a1", Title: "测试", Revision: 1}
	if err := index.Replace(ctx, session, []Message{{EntryID: "e1", Role: "user", Timestamp: "2026-01-01T00:00:00Z", Content: "北京天气怎么样"}}); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{"北京", "天气怎"} {
		results, err := index.Search(ctx, query, 10)
		if err != nil || len(results) != 1 || results[0].EntryID != "e1" {
			t.Fatalf("query=%q results=%#v err=%v", query, results, err)
		}
	}
	session.Revision = 2
	if err := index.Replace(ctx, session, []Message{{EntryID: "e2", Role: "assistant", Timestamp: "2026-01-02T00:00:00Z", Content: "上海天气晴朗"}}); err != nil {
		t.Fatal(err)
	}
	results, err := index.Search(ctx, "北京", 10)
	if err != nil || len(results) != 0 {
		t.Fatalf("stale result: %#v, %v", results, err)
	}
	if err := index.Prune(ctx, map[string]bool{}); err != nil {
		t.Fatal(err)
	}
	results, err = index.Search(ctx, "上海", 10)
	if err != nil || len(results) != 0 {
		t.Fatalf("prune result: %#v, %v", results, err)
	}
}

func TestDocumentIndexIncrementalReplace(t *testing.T) {
	ctx := context.Background()
	index, err := Open(filepath.Join(t.TempDir(), "documents.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer index.Close()
	if err := index.ReplaceDocument(ctx, "a1", "/workspace", "notes.docx", 12, 1, "第一行\n需要跟进合同"); err != nil {
		t.Fatal(err)
	}
	current, err := index.DocumentCurrent(ctx, "a1", "/workspace", "notes.docx", 12, 1)
	if err != nil || !current {
		t.Fatalf("current=%v err=%v", current, err)
	}
	results, err := index.SearchDocuments(ctx, "a1", "/workspace", "合同", 10)
	if err != nil || len(results) != 1 || results[0].Path != "notes.docx" || results[0].Line != 2 {
		t.Fatalf("results=%+v err=%v", results, err)
	}
	if err := index.ReplaceDocument(ctx, "a1", "/workspace", "notes.docx", 13, 2, "会议已结束"); err != nil {
		t.Fatal(err)
	}
	results, err = index.SearchDocuments(ctx, "a1", "/workspace", "合同", 10)
	if err != nil || len(results) != 0 {
		t.Fatalf("stale=%+v err=%v", results, err)
	}
	if err := index.PruneDocuments(ctx, "a1", "/workspace", map[string]bool{}); err != nil {
		t.Fatal(err)
	}
	current, err = index.DocumentCurrent(ctx, "a1", "/workspace", "notes.docx", 13, 2)
	if err != nil || current {
		t.Fatalf("prune current=%v err=%v", current, err)
	}
}

func TestDocumentIndexRebuildsOldPlainTextProjection(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "documents.sqlite")
	index, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := index.ReplaceDocument(ctx, "a1", "/workspace", "report.pdf", 42, 1, "旧纯文本"); err != nil {
		t.Fatal(err)
	}
	if _, err := index.db.Exec(`PRAGMA user_version=0`); err != nil {
		t.Fatal(err)
	}
	if err := index.Close(); err != nil {
		t.Fatal(err)
	}
	index, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer index.Close()
	current, err := index.DocumentCurrent(ctx, "a1", "/workspace", "report.pdf", 42, 1)
	if err != nil || current {
		t.Fatalf("old projection current=%v err=%v", current, err)
	}
}
