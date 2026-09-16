package sessions

import (
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/eino/schema"
	"github.com/sda1-hacker/humbert-agent/internal/logging"
	"github.com/sda1-hacker/humbert-agent/internal/transcript"
)

func TestAttachmentSidecarPersistsMetadataAndHydratesRuntime(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	root := t.TempDir()
	transcripts, err := transcript.NewStore(root)
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewStore(ctx, transcripts)
	if err != nil {
		t.Fatal(err)
	}
	service := &Service{store: store, logger: logging.NewBootstrap()}

	session := Session{ID: "session-attachment", AgentID: "agent-a", Title: "test", CWD: root, CreatedAt: time.Now().UTC()}
	if err := store.CreateSession(ctx, session); err != nil {
		t.Fatal(err)
	}
	payload := []byte("hello attachment")
	stored, err := service.AppendUserInput(ctx, session.ID, UserInput{Text: "inspect this", Attachments: []AttachmentInput{{Name: "hello.txt", MIMEType: "text/plain", Base64Data: base64.StdEncoding.EncodeToString(payload)}}})
	if err != nil {
		t.Fatal(err)
	}
	if stored.Message == nil || len(stored.Message.UserInputMultiContent) != 2 {
		t.Fatalf("unexpected stored message: %#v", stored.Message)
	}

	filePart := stored.Message.UserInputMultiContent[1]
	if filePart.Type != schema.ChatMessagePartTypeFileURL || filePart.File == nil || filePart.File.URL == nil || !strings.HasPrefix(*filePart.File.URL, attachmentURLPrefix) {
		t.Fatalf("attachment not persisted as sidecar reference: %#v", filePart)
	}
	attachmentID, _ := filePart.Extra["attachment_id"].(string)
	if attachmentID == "" {
		t.Fatal("missing attachment id")
	}
	if _, err := os.Stat(filepath.Join(root, session.AgentID, "sessions", session.ID, "attachments", attachmentID)); err != nil {
		t.Fatalf("sidecar missing: %v", err)
	}

	messages, err := service.BuildContext(ctx, session.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 1 || len(messages[0].UserInputMultiContent) != 2 {
		t.Fatalf("unexpected runtime messages: %#v", messages)
	}
	hydrated := messages[0].UserInputMultiContent[1]
	if hydrated.File == nil || hydrated.File.Base64Data == nil || hydrated.File.URL != nil {
		t.Fatalf("attachment was not hydrated: %#v", hydrated.File)
	}
	decoded, err := base64.StdEncoding.DecodeString(*hydrated.File.Base64Data)
	if err != nil {
		t.Fatal(err)
	}
	if string(decoded) != string(payload) {
		t.Fatalf("unexpected hydrated payload %q", decoded)
	}
}
