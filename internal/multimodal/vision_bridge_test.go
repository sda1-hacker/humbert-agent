package multimodal

import (
	"context"
	"errors"
	"strings"
	"testing"

	einomodel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

type visionModelStub struct {
	messages []*schema.Message
	calls    int
	result   string
}

func (m *visionModelStub) Generate(_ context.Context, messages []*schema.Message, _ ...einomodel.Option) (*schema.Message, error) {
	m.calls++
	m.messages = messages
	return schema.AssistantMessage(m.result, nil), nil
}

func (*visionModelStub) Stream(context.Context, []*schema.Message, ...einomodel.Option) (*schema.StreamReader[*schema.Message], error) {
	return nil, errors.New("unexpected stream")
}

func (m *visionModelStub) WithTools([]*schema.ToolInfo) (einomodel.ToolCallingChatModel, error) {
	return m, nil
}

func TestBridgeImagesForTextModelInjectsObservationIntoLatestUserTurn(t *testing.T) {
	encoded := "aW1hZ2U="
	imageURL := "humbert-attachment://image-1"
	messages := []*schema.Message{
		{
			Role: schema.User,
			UserInputMultiContent: []schema.MessageInputPart{
				{Type: schema.ChatMessagePartTypeText, Text: "看看这张截图"},
				{
					Type: schema.ChatMessagePartTypeImageURL,
					Image: &schema.MessageInputImage{MessagePartCommon: schema.MessagePartCommon{
						URL: &imageURL, Base64Data: &encoded, MIMEType: "image/png",
					}},
					Extra: map[string]any{"name": "error.png", "attachment_id": "image-1"},
				},
			},
		},
		schema.AssistantMessage("我看到了页面。", nil),
		schema.UserMessage("右上角的提示是什么？"),
	}
	model := &visionModelStub{result: "图片 1：右上角显示 Request failed，页面中央显示 500 Internal Server Error。"}

	bridged, err := BridgeImagesForTextModel(context.Background(), model, messages)
	if err != nil {
		t.Fatal(err)
	}
	if model.calls != 1 {
		t.Fatalf("vision calls = %d, want 1", model.calls)
	}
	if len(model.messages) != 2 || model.messages[1].Role != schema.User {
		t.Fatalf("unexpected vision request: %#v", model.messages)
	}
	visionImages := 0
	for _, part := range model.messages[1].UserInputMultiContent {
		if part.Type == schema.ChatMessagePartTypeImageURL {
			visionImages++
		}
	}
	if visionImages != 1 {
		t.Fatalf("vision request images = %d, want 1", visionImages)
	}
	if !strings.Contains(latestUserText(model.messages), "右上角的提示是什么") {
		t.Fatalf("vision request did not include current user request: %q", latestUserText(model.messages))
	}

	for _, message := range bridged {
		if message == nil {
			continue
		}
		for _, part := range message.UserInputMultiContent {
			if part.Type == schema.ChatMessagePartTypeImageURL {
				t.Fatal("text chat model context still contains image input")
			}
		}
	}
	latest := bridged[len(bridged)-1]
	if !strings.Contains(latest.Content, "[Humbert 视觉辅助观察]") || !strings.Contains(latest.Content, "500 Internal Server Error") {
		t.Fatalf("latest user message missing vision observation: %q", latest.Content)
	}

	// Bridge 只生成当前 Turn 的派生上下文，不能修改 Transcript 投影出来的原始消息。
	if len(messages[0].UserInputMultiContent) != 2 || messages[0].UserInputMultiContent[1].Type != schema.ChatMessagePartTypeImageURL {
		t.Fatal("original messages were mutated")
	}
}

func TestBridgeImagesForTextModelSkipsModelWhenNoImagesRemain(t *testing.T) {
	model := &visionModelStub{result: "unexpected"}
	messages := []*schema.Message{schema.UserMessage("纯文本问题")}

	bridged, err := BridgeImagesForTextModel(context.Background(), model, messages)
	if err != nil {
		t.Fatal(err)
	}
	if model.calls != 0 {
		t.Fatalf("vision calls = %d, want 0", model.calls)
	}
	if len(bridged) != 1 || bridged[0].Content != "纯文本问题" {
		t.Fatalf("unexpected bridged messages: %#v", bridged)
	}
}

func TestBridgeImagesForTextModelCapsObservationForRemainingContext(t *testing.T) {
	encoded := "aW1hZ2U="
	imageURL := "humbert-attachment://image-1"
	messages := []*schema.Message{{
		Role: schema.User,
		UserInputMultiContent: []schema.MessageInputPart{{
			Type: schema.ChatMessagePartTypeImageURL,
			Image: &schema.MessageInputImage{MessagePartCommon: schema.MessagePartCommon{
				URL: &imageURL, Base64Data: &encoded, MIMEType: "image/png",
			}},
		}},
	}}
	model := &visionModelStub{result: strings.Repeat("观察", 1000)}

	bridged, err := BridgeImagesForTextModel(context.Background(), model, messages, 80)
	if err != nil {
		t.Fatal(err)
	}
	latest := latestUserText(bridged)
	if !strings.Contains(latest, "[视觉观察过长，已截断]") {
		t.Fatalf("expected truncated observation, got %q", latest)
	}
	if len([]rune(latest)) > 400 {
		t.Fatalf("bridged observation unexpectedly large: %d runes", len([]rune(latest)))
	}
}
