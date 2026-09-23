package contextengine

import (
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"
)

func TestApproxEstimatorCountsExtractedFileText(t *testing.T) {
	t.Parallel()
	fileURL := "humbert-attachment://file-1"
	short := &schema.Message{Role: schema.User, UserInputMultiContent: []schema.MessageInputPart{{
		Type: schema.ChatMessagePartTypeFileURL,
		File: &schema.MessageInputFile{
			MessagePartCommon: schema.MessagePartCommon{URL: &fileURL, MIMEType: "text/plain"},
			Name:              "notes.txt",
		},
		Extra: map[string]any{"extracted_text": "short"},
	}}}
	long := *short
	long.UserInputMultiContent = append([]schema.MessageInputPart(nil), short.UserInputMultiContent...)
	long.UserInputMultiContent[0].Extra = map[string]any{"extracted_text": strings.Repeat("中", 5000)}

	estimator := NewApproxEstimator()
	if estimator.EstimateMessage(&long) <= estimator.EstimateMessage(short)+4000 {
		t.Fatalf("large extracted file was not reflected in token estimate")
	}
}

func TestApproxEstimatorMatchesHistoricalImageReplayWindow(t *testing.T) {
	t.Parallel()
	imageURL := "humbert-attachment://image-1"
	imageMessage := &schema.Message{Role: schema.User, UserInputMultiContent: []schema.MessageInputPart{{
		Type: schema.ChatMessagePartTypeImageURL,
		Image: &schema.MessageInputImage{MessagePartCommon: schema.MessagePartCommon{
			URL: &imageURL, MIMEType: "image/png",
		}},
		Extra: map[string]any{"name": "image.png", "attachment_id": "image-1"},
	}}}
	estimator := NewApproxEstimator()
	immediate := []*schema.Message{imageMessage, schema.AssistantMessage("seen", nil), schema.UserMessage("follow up")}
	later := append(immediate, schema.AssistantMessage("answered", nil), schema.UserMessage("new topic"))

	if immediateTokens, laterTokens := estimator.EstimateMessages(immediate), estimator.EstimateMessages(later); laterTokens >= immediateTokens {
		t.Fatalf("historical image placeholder should be cheaper than replay: immediate=%d later=%d", immediateTokens, laterTokens)
	}
}

func TestApproxEstimatorCalibratesTowardProviderUsage(t *testing.T) {
	t.Parallel()
	estimator := NewApproxEstimator()
	text := strings.Repeat("a", 4000)
	before := estimator.EstimateText(text)
	if before <= 0 {
		t.Fatalf("expected positive estimate")
	}

	// 连续观察 Provider 实际输入高于本地估算，后续估算应平滑上调，但不能一次跳到极端值。
	for i := 0; i < 8; i++ {
		estimator.ObservePromptUsage(before, before*2)
	}
	after := estimator.EstimateText(text)
	if after <= before {
		t.Fatalf("expected calibrated estimate to increase: before=%d after=%d", before, after)
	}
	if after > int(float64(before)*1.31)+1 {
		t.Fatalf("calibration exceeded safety bound: before=%d after=%d", before, after)
	}
}

func TestApproxEstimatorMessageCostsMatchProjectedTotal(t *testing.T) {
	t.Parallel()
	imageURL := "humbert-attachment://image-1"
	messages := []*schema.Message{{Role: schema.User, UserInputMultiContent: []schema.MessageInputPart{{
		Type:  schema.ChatMessagePartTypeImageURL,
		Image: &schema.MessageInputImage{MessagePartCommon: schema.MessagePartCommon{URL: &imageURL, MIMEType: "image/png"}},
		Extra: map[string]any{"attachment_id": "image-1"},
	}}}, schema.AssistantMessage("seen", nil), schema.UserMessage("follow up"), schema.UserMessage("later")}
	estimator := NewApproxEstimator()
	costs := estimator.EstimateMessageCosts(messages)
	total := 0
	for _, cost := range costs {
		total += cost
	}
	if total != estimator.EstimateMessages(messages) || costs[0] >= estimator.EstimateMessage(messages[0]) {
		t.Fatalf("planner costs drifted from projected prompt: costs=%v total=%d", costs, estimator.EstimateMessages(messages))
	}
}
