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
