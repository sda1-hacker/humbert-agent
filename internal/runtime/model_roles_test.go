package runtime

import (
	"context"
	"testing"

	"github.com/cloudwego/eino/schema"

	"github.com/sda1-hacker/humbert-agent/internal/agents"
	"github.com/sda1-hacker/humbert-agent/internal/models"
)

type modelRoleResolverStub struct {
	snapshots  map[string]models.RuntimeSnapshot
	multimedia models.MultimediaConfig
}

func (s modelRoleResolverStub) ResolveSnapshot(_ context.Context, id string) (models.RuntimeSnapshot, error) {
	return s.snapshots[id], nil
}

func (s modelRoleResolverStub) MultimediaConfig(_ context.Context) (models.MultimediaConfig, error) {
	return s.multimedia, nil
}

func TestExtractedTextFileDoesNotRequireNativeFileCapability(t *testing.T) {
	t.Parallel()
	fileURL := "humbert-attachment://file-1"
	message := &schema.Message{Role: schema.User, UserInputMultiContent: []schema.MessageInputPart{{
		Type: schema.ChatMessagePartTypeFileURL,
		File: &schema.MessageInputFile{
			MessagePartCommon: schema.MessagePartCommon{URL: &fileURL, MIMEType: "text/plain"},
			Name:              "notes.txt",
		},
		Extra: map[string]any{"extracted_text": "hello"},
	}}}

	requirements := requirementsFromMessage(message)
	if requirements.Files || requirements.Vision {
		t.Fatalf("requirements = %#v", requirements)
	}

	delete(message.UserInputMultiContent[0].Extra, "extracted_text")
	requirements = requirementsFromMessage(message)
	if !requirements.Files {
		t.Fatalf("native file without extraction must require Files capability: %#v", requirements)
	}
}

func TestHistoricalImageOnlyRequiresVisionForImmediateFollowUp(t *testing.T) {
	t.Parallel()
	imageURL := "humbert-attachment://image-1"
	imageMessage := &schema.Message{Role: schema.User, UserInputMultiContent: []schema.MessageInputPart{{
		Type: schema.ChatMessagePartTypeImageURL,
		Image: &schema.MessageInputImage{MessagePartCommon: schema.MessagePartCommon{
			URL: &imageURL, MIMEType: "image/png",
		}},
		Extra: map[string]any{"name": "image.png", "attachment_id": "image-1"},
	}}}

	immediateFollowUp := []*schema.Message{
		imageMessage,
		schema.AssistantMessage("I inspected it.", nil),
		schema.UserMessage("What about the background?"),
	}
	if requirements := requirementsFromMessages(immediateFollowUp); !requirements.Vision {
		t.Fatalf("immediate image follow-up must keep Vision requirement: %#v", requirements)
	}

	laterContext := append(immediateFollowUp,
		schema.AssistantMessage("The background is light.", nil),
		schema.UserMessage("Summarize the discussion."),
	)
	if requirements := requirementsFromMessages(laterContext); requirements.Vision {
		t.Fatalf("omitted historical image must not keep Vision requirement: %#v", requirements)
	}
}

func TestResolveModelRolesUsesGlobalImageFallback(t *testing.T) {
	t.Parallel()
	chat := models.RuntimeSnapshot{ModelConfigID: "chat", Capabilities: models.Capabilities{Tools: true}}
	image := models.RuntimeSnapshot{ModelConfigID: "image", Capabilities: models.Capabilities{Tools: true, Vision: true}}
	resolver := &Resolver{models: modelRoleResolverStub{
		snapshots:  map[string]models.RuntimeSnapshot{"chat": chat, "image": image},
		multimedia: models.MultimediaConfig{ImageModelID: "image"},
	}}

	roles, err := resolver.resolveModelRoles(context.Background(), agents.Agent{ModelID: "chat"}, turnInputRequirements{Vision: true})
	if err != nil {
		t.Fatal(err)
	}
	if roles.active.ModelConfigID != "image" || roles.activeRole != modelRoleImage {
		t.Fatalf("active model = %q (%q), want image (%q)", roles.active.ModelConfigID, roles.activeRole, modelRoleImage)
	}
	if roles.imageModelID != "image" {
		t.Fatalf("image model id = %q", roles.imageModelID)
	}
}

func TestResolveModelRolesKeepsVisionCapableChatModel(t *testing.T) {
	t.Parallel()
	chat := models.RuntimeSnapshot{ModelConfigID: "chat", Capabilities: models.Capabilities{Tools: true, Vision: true}}
	image := models.RuntimeSnapshot{ModelConfigID: "image", Capabilities: models.Capabilities{Tools: true, Vision: true}}
	resolver := &Resolver{models: modelRoleResolverStub{
		snapshots:  map[string]models.RuntimeSnapshot{"chat": chat, "image": image},
		multimedia: models.MultimediaConfig{ImageModelID: "image"},
	}}

	roles, err := resolver.resolveModelRoles(context.Background(), agents.Agent{ModelID: "chat"}, turnInputRequirements{Vision: true})
	if err != nil {
		t.Fatal(err)
	}
	if roles.active.ModelConfigID != "chat" || roles.activeRole != modelRoleChat {
		t.Fatalf("active model = %q (%q), want chat (%q)", roles.active.ModelConfigID, roles.activeRole, modelRoleChat)
	}
}
