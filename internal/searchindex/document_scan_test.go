package searchindex

import (
	"archive/zip"
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/sda1-hacker/humbert-agent/internal/logging"
	"github.com/sda1-hacker/humbert-agent/internal/workspace"
)

func TestRefreshDocumentsClearsUnavailableContentAndReindexesRecovery(t *testing.T) {
	for _, tc := range []struct {
		name string
		size int64
	}{
		{name: "empty"},
		{name: "oversized", size: (12 << 20) + 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			workspaces, err := workspace.NewManager(ctx, filepath.Join(t.TempDir(), "workspaces"), logging.NewBootstrap())
			if err != nil {
				t.Fatal(err)
			}
			defer workspaces.Close()
			id := "00000000-0000-4000-8000-000000000123"
			resolved, err := workspaces.Resolve(ctx, id, workspace.ModeManaged, "")
			if err != nil {
				t.Fatal(err)
			}
			index, err := Open(filepath.Join(t.TempDir(), "search.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer index.Close()
			path := filepath.Join(resolved.RootDir, "report.docx")
			assertMatches := func(query string, count int) {
				t.Helper()
				found, err := index.SearchDocuments(ctx, id, resolved.RootDir, query, 10)
				if err != nil || len(found) != count {
					t.Fatalf("query=%s results=%+v err=%v", query, found, err)
				}
			}
			refresh := func() {
				t.Helper()
				if err := RefreshDocuments(ctx, index, workspaces, id, resolved); err != nil {
					t.Fatal(err)
				}
			}
			// 通过真实解析器建立索引，覆盖“有效文档 → 不可索引 → 有效文档”的完整生命周期。
			if err := os.WriteFile(path, searchableDOCX(t, "PreviousDocumentText"), 0600); err != nil {
				t.Fatal(err)
			}
			refresh()
			assertMatches("PreviousDocumentText", 1)
			if err := os.Truncate(path, tc.size); err != nil {
				t.Fatal(err)
			}
			refresh()
			assertMatches("PreviousDocumentText", 0)
			stat, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			current, err := index.DocumentCurrent(ctx, id, resolved.RootDir, "report.docx", stat.Size(), stat.ModTime().UnixNano())
			if err != nil || !current {
				t.Fatalf("empty projection metadata: current=%v err=%v", current, err)
			}
			if err := os.WriteFile(path, searchableDOCX(t, "RecoveredDocumentText"), 0600); err != nil {
				t.Fatal(err)
			}
			refresh()
			assertMatches("PreviousDocumentText", 0)
			assertMatches("RecoveredDocumentText", 1)
		})
	}
}

func searchableDOCX(t *testing.T, text string) []byte {
	t.Helper()
	var data bytes.Buffer
	writer := zip.NewWriter(&data)
	for name, content := range map[string]string{
		"[Content_Types].xml": `<?xml version="1.0"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/></Types>`,
		"_rels/.rels":         `<?xml version="1.0"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/></Relationships>`,
		"word/document.xml":   `<?xml version="1.0"?><w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p><w:r><w:t>` + text + `</w:t></w:r></w:p></w:body></w:document>`,
	} {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return data.Bytes()
}
