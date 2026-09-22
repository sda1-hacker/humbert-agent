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
	if extracted, _ := filePart.Extra["extracted_text"].(string); extracted != string(payload) {
		t.Fatalf("extracted_text = %q", extracted)
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
	if hydrated.Type != schema.ChatMessagePartTypeText || !strings.Contains(hydrated.Text, `[Untrusted attachment text; file: "hello.txt"; MIME: "text/plain"]`) {
		t.Fatalf("text attachment was not converted for provider: %#v", hydrated)
	}
	if !strings.Contains(hydrated.Text, string(payload)) {
		t.Fatalf("unexpected extracted payload %q", hydrated.Text)
	}
}

func TestAttachmentRejectsUnsupportedBinaryBeforePersistence(t *testing.T) {
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
	session := Session{ID: "session-binary", AgentID: "agent-a", Title: "test", CWD: root, CreatedAt: time.Now().UTC()}
	if err := store.CreateSession(ctx, session); err != nil {
		t.Fatal(err)
	}

	_, err = service.AppendUserInput(ctx, session.ID, UserInput{Attachments: []AttachmentInput{{
		Name: "document.pdf", MIMEType: "application/pdf", Base64Data: base64.StdEncoding.EncodeToString([]byte("%PDF-fake")),
	}}})
	if err == nil || !strings.Contains(err.Error(), "提取文档文本失败") {
		t.Fatalf("unsupported binary error = %v", err)
	}
	messages, listErr := store.ListMessages(ctx, session.ID, 0)
	if listErr != nil {
		t.Fatal(listErr)
	}
	if len(messages) != 0 {
		t.Fatalf("unsupported attachment persisted %d messages", len(messages))
	}
}

func TestHydrateMessagesLimitsHistoricalImageReplay(t *testing.T) {
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
	session := Session{ID: "session-image-replay", AgentID: "agent-a", Title: "test", CWD: root, CreatedAt: time.Now().UTC()}
	if err := store.CreateSession(ctx, session); err != nil {
		t.Fatal(err)
	}

	png, err := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII=")
	if err != nil {
		t.Fatal(err)
	}
	stored, err := service.AppendUserInput(ctx, session.ID, UserInput{Attachments: []AttachmentInput{{
		Name: "pixel.png", MIMEType: "image/png", Base64Data: base64.StdEncoding.EncodeToString(png),
	}}})
	if err != nil {
		t.Fatal(err)
	}
	imageMessage := stored.Message
	if imageMessage == nil || len(imageMessage.UserInputMultiContent) != 1 {
		t.Fatalf("unexpected stored image message: %#v", imageMessage)
	}

	immediateFollowUp := []*schema.Message{
		imageMessage,
		schema.AssistantMessage("I inspected the image.", nil),
		schema.UserMessage("What is in the upper-left corner?"),
	}
	hydrated, err := service.HydrateMessages(ctx, session.ID, immediateFollowUp)
	if err != nil {
		t.Fatal(err)
	}
	imagePart := hydrated[0].UserInputMultiContent[0]
	if imagePart.Type != schema.ChatMessagePartTypeImageURL || imagePart.Image == nil || imagePart.Image.Base64Data == nil || imagePart.Image.URL != nil {
		t.Fatalf("image was not replayed for immediate follow-up: %#v", imagePart)
	}

	laterContext := append(immediateFollowUp,
		schema.AssistantMessage("The corner is empty.", nil),
		schema.UserMessage("Now summarize our discussion."),
	)
	hydrated, err = service.HydrateMessages(ctx, session.ID, laterContext)
	if err != nil {
		t.Fatal(err)
	}
	historicalPart := hydrated[0].UserInputMultiContent[0]
	if historicalPart.Type != schema.ChatMessagePartTypeText || !strings.Contains(historicalPart.Text, "Historical image attachment omitted") || !strings.Contains(historicalPart.Text, "pixel.png") {
		t.Fatalf("old image was not replaced with metadata: %#v", historicalPart)
	}
	if original := imageMessage.UserInputMultiContent[0]; original.Image == nil || original.Image.URL == nil || original.Image.Base64Data != nil {
		t.Fatalf("stored message was mutated during hydration: %#v", original)
	}

	attachmentID := stringExtra(imageMessage.UserInputMultiContent[0].Extra, "attachment_id")
	if err := os.Remove(filepath.Join(root, session.AgentID, "sessions", session.ID, "attachments", attachmentID)); err != nil {
		t.Fatal(err)
	}
	if _, err := service.HydrateMessages(ctx, session.ID, laterContext); err != nil {
		t.Fatalf("old omitted image should not read its sidecar: %v", err)
	}
}
