package builtin

import (
	"archive/zip"
	"bytes"
	"context"
	"strings"
	"testing"

	humberttools "github.com/sda1-hacker/humbert-agent/internal/tools"
	"github.com/sda1-hacker/humbert-agent/internal/transcript"
)

type documentAttachmentStub struct {
	data  []byte
	reads int
}

func (s *documentAttachmentStub) ReadAttachment(_ context.Context, _, _ string) ([]byte, error) {
	s.reads++
	return s.data, nil
}

func makeToolTestDocx(t *testing.T) []byte {
	t.Helper()
	var buffer bytes.Buffer
	archive := zip.NewWriter(&buffer)
	files := map[string]string{
		"[Content_Types].xml": `<?xml version="1.0"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/></Types>`,
		"_rels/.rels":         `<?xml version="1.0"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/></Relationships>`,
		"word/document.xml":   `<?xml version="1.0"?><w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p><w:pPr><w:pStyle w:val="Heading1"/></w:pPr><w:r><w:t>On demand report</w:t></w:r></w:p></w:body></w:document>`,
	}
	for name, content := range files {
		entry, err := archive.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func TestExtractDocumentAttachmentMarkdownAndBranchGuard(t *testing.T) {
	repo := &indexedHistoryStub{entries: []transcript.Entry{{Type: transcript.EntryMessage, ID: "user", Message: &transcript.AgentMessage{Role: transcript.RoleUser, Content: []transcript.ContentBlock{{Type: transcript.ContentFile, AttachmentID: "allowed", Name: "report.docx", MIMEType: "application/vnd.openxmlformats-officedocument.wordprocessingml.document", DocumentOnDemand: true}}}}}}
	reader := &documentAttachmentStub{data: makeToolTestDocx(t)}
	factory := &ExtractDocumentFactory{history: repo, attachments: reader}
	scope := humberttools.Scope{SessionID: "session"}
	if _, err := factory.Build(context.Background(), scope); err != nil {
		t.Fatalf("Eino Tool schema build failed: %v", err)
	}
	first, err := factory.run(context.Background(), scope, &ExtractDocumentInput{AttachmentID: "allowed", Limit: 8})
	if err != nil || first.Format != "markdown" || !strings.HasPrefix(first.Content, "# ") || !first.More {
		t.Fatalf("first=%#v err=%v", first, err)
	}
	second, err := factory.run(context.Background(), scope, &ExtractDocumentInput{AttachmentID: "allowed", Offset: first.End})
	if err != nil || first.Content+second.Content != "# On demand report" || second.More {
		t.Fatalf("second=%#v err=%v", second, err)
	}
	if _, err := factory.run(context.Background(), scope, &ExtractDocumentInput{AttachmentID: "missing"}); err == nil {
		t.Fatal("unknown attachment should be rejected")
	}
	if _, err := factory.run(context.Background(), scope, &ExtractDocumentInput{Path: "report.docx"}); err == nil || !strings.Contains(err.Error(), "未启用") {
		t.Fatalf("workspace access must honor disabled file tools: %v", err)
	}
	if reader.reads != 2 || repo.fullLoads != 0 {
		t.Fatalf("reads=%d fullLoads=%d", reader.reads, repo.fullLoads)
	}
}
