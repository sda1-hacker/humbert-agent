package builtin

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/sda1-hacker/humbert-agent/internal/sandbox"
	humberttools "github.com/sda1-hacker/humbert-agent/internal/tools"
	"github.com/sda1-hacker/humbert-agent/internal/workspace"
)

func TestCopyFileFromSessionAttachment(t *testing.T) {
	root, err := sandbox.CanonicalRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	scope := humberttools.Scope{SessionID: "session-1", Workspace: workspace.Workspace{RootDir: root}}
	png := []byte("\x89PNG\r\n\x1a\noriginal")
	reader := &documentAttachmentStub{data: png}
	input := &CopyFileInput{AttachmentID: "attachment-1", Destination: "images/renamed.png"}
	result, err := copyFile(context.Background(), scope, input, 1024, false, reader)
	if err != nil {
		t.Fatal(err)
	}
	if result.AttachmentID != input.AttachmentID || result.Destination != input.Destination || result.Bytes != int64(len(png)) {
		t.Fatalf("unexpected copy result: %+v", result)
	}
	stored, err := os.ReadFile(filepath.Join(root, "images", "renamed.png"))
	if err != nil || !bytes.Equal(stored, png) {
		t.Fatalf("copied bytes differ: %v", err)
	}
	if reader.reads != 1 {
		t.Fatalf("attachment read count = %d", reader.reads)
	}
	factory, err := NewCopyFileFactory(1024, reader)
	if err != nil {
		t.Fatal(err)
	}
	tool, err := factory.Build(context.Background(), scope)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := tool.InvokableRun(context.Background(), `{"attachment_id":"attachment-1","destination":"images/from-tool.png"}`)
	if err != nil {
		t.Fatal(err)
	}
	var toolResult FileOperationOutput
	if err := json.Unmarshal([]byte(encoded), &toolResult); err != nil || toolResult.AttachmentID != "attachment-1" {
		t.Fatalf("tool result = %q, err = %v", encoded, err)
	}
	if _, err := copyFile(context.Background(), scope, input, 1024, false, reader); err == nil {
		t.Fatal("existing destination was overwritten without permission")
	}
	input.Overwrite = true
	if _, err := copyFile(context.Background(), scope, input, 1024, false, reader); err != nil {
		t.Fatalf("explicit overwrite failed: %v", err)
	}
}

func TestCopyAttachmentRequiresExclusiveSourceAndSandboxDestination(t *testing.T) {
	root, err := sandbox.CanonicalRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	scope := humberttools.Scope{SessionID: "session-1", Workspace: workspace.Workspace{RootDir: root}}
	reader := &documentAttachmentStub{data: []byte("png")}
	for _, input := range []*CopyFileInput{
		{Destination: "missing.png"},
		{Source: "source.png", AttachmentID: "attachment-1", Destination: "both.png"},
		{AttachmentID: "attachment-1", Destination: "../outside.png"},
	} {
		if _, err := copyFile(context.Background(), scope, input, 1024, false, reader); err == nil {
			t.Fatalf("invalid copy accepted: %+v", input)
		}
	}
	reader.data = make([]byte, maxBrowserScreenshotBytes+1)
	if _, err := copyFile(context.Background(), scope, &CopyFileInput{AttachmentID: "attachment-1", Destination: "large.png"}, 2, false, reader); err == nil {
		t.Fatal("oversized attachment copied")
	}
	if _, err := copyFile(context.Background(), scope, &CopyFileInput{AttachmentID: "attachment-1", Destination: "move.png"}, 1024, true, reader); err == nil {
		t.Fatal("move_file accepted attachment source")
	}
}
