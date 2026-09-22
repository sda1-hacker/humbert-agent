package models

import (
	"context"
	"testing"

	einomodel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

type captureReasoningModel struct{ received []*schema.Message }

func (m *captureReasoningModel) Generate(_ context.Context, input []*schema.Message, _ ...einomodel.Option) (*schema.Message, error) {
	m.received = input
	return schema.AssistantMessage("ok", nil), nil
}

func (m *captureReasoningModel) Stream(_ context.Context, input []*schema.Message, _ ...einomodel.Option) (*schema.StreamReader[*schema.Message], error) {
	m.received = input
	return nil, nil
}

func (m *captureReasoningModel) WithTools(_ []*schema.ToolInfo) (einomodel.ToolCallingChatModel, error) {
	return m, nil
}

func TestOpenAIOutboundReasoningIsOmittedWithoutChangingHistory(t *testing.T) {
	inner := &captureReasoningModel{}
	model := &reasoningOmittingModel{inner: inner}
	assistant := &schema.Message{
		Role: schema.Assistant, Content: "visible answer", ReasoningContent: "private thinking",
		AssistantGenMultiContent: []schema.MessageOutputPart{
			{Type: schema.ChatMessagePartTypeReasoning, Reasoning: &schema.MessageOutputReasoning{Text: "signed thinking", Signature: "signature"}},
			{Type: schema.ChatMessagePartTypeText, Text: "visible answer"},
		},
		ToolCalls: []schema.ToolCall{{ID: "call-1", Type: "function", Function: schema.FunctionCall{Name: "read_file", Arguments: `{}`}}},
	}
	input := []*schema.Message{schema.UserMessage("question"), assistant, schema.ToolMessage("result", "call-1")}

	bound, err := model.WithTools(nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bound.Generate(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	assertCleanOutbound := func() {
		t.Helper()
		got := inner.received[1]
		if got.ReasoningContent != "" || len(got.AssistantGenMultiContent) != 1 {
			t.Fatalf("thinking sent to OpenAI adapter: %#v", got)
		}
		if got.Content != "visible answer" || len(got.ToolCalls) != 1 || inner.received[2].ToolCallID != "call-1" {
			t.Fatal("visible answer or tool transaction was changed")
		}
		if assistant.ReasoningContent != "private thinking" || len(assistant.AssistantGenMultiContent) != 2 {
			t.Fatal("source history was mutated")
		}
	}
	assertCleanOutbound()
	if _, err := bound.Stream(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	assertCleanOutbound()
}
