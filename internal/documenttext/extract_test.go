package documenttext

import (
	"archive/zip"
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestExtractDocxMarkdown(t *testing.T) {
	var buffer bytes.Buffer
	archive := zip.NewWriter(&buffer)
	files := map[string]string{
		"[Content_Types].xml": `<?xml version="1.0"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/></Types>`,
		"_rels/.rels":         `<?xml version="1.0"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/></Relationships>`,
		"word/document.xml":   `<?xml version="1.0"?><w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p><w:pPr><w:pStyle w:val="Heading1"/></w:pPr><w:r><w:t>Humbert document test</w:t></w:r></w:p></w:body></w:document>`,
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
	got, mime, err := Extract(context.Background(), "sample.docx", "", buffer.Bytes())
	if err != nil || !strings.Contains(got, "# Humbert document test") || mime != supported[".docx"] {
		t.Fatalf("text=%q mime=%q err=%v", got, mime, err)
	}
}

func TestRejectInvalidPDF(t *testing.T) {
	if _, _, err := Extract(context.Background(), "bad.pdf", "", []byte("not a PDF")); err == nil {
		t.Fatal("应拒绝伪造的 PDF")
	}
}
