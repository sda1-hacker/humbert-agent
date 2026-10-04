package documentparse

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestTextSnapshotAndSourceHash(t *testing.T) {
	path := filepath.Join(t.TempDir(), "note.md")
	data := []byte("\xef\xbb\xbf# 中文😀\r\n原文")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	r, err := ExtractFile(context.Background(), path, Options{})
	if err != nil || r.Markdown != "# 中文😀\r\n原文" || r.ContentHash != fmt.Sprintf("%x", sha256.Sum256(data)) {
		t.Fatalf("%+v %v", r, err)
	}
}

func TestParserResourceAndEncodingLimits(t *testing.T) {
	path := filepath.Join(t.TempDir(), "note.txt")
	if err := os.WriteFile(path, []byte("12345"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, opts := range []Options{{MaxInputBytes: 4}, {MaxOutputBytes: 4}} {
		if _, err := ExtractFile(context.Background(), path, opts); !errors.Is(err, ErrResourceLimit) {
			t.Fatalf("missing resource error: %v", err)
		}
	}
	if err := os.WriteFile(path, []byte{0xff}, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ExtractFile(context.Background(), path, Options{}); err == nil {
		t.Fatal("invalid UTF-8 accepted")
	}
}

func TestParserDeadlineAndArchivePreflight(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.docx")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	w := zip.NewWriter(f)
	entry, err := w.Create("word/document.xml")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = entry.Write([]byte("12345"))
	if err = w.Close(); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	if _, err = ExtractFile(context.Background(), path, Options{MaxExpandedBytes: 4}); !errors.Is(err, ErrResourceLimit) {
		t.Fatalf("archive budget ignored: %v", err)
	}
	if _, err = ExtractFile(context.Background(), path, Options{Timeout: time.Nanosecond}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("deadline ignored: %v", err)
	}
}
