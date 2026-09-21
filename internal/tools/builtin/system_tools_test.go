package builtin

import (
	"context"
	"encoding/json"
	"testing"

	humberttools "github.com/sda1-hacker/humbert-agent/internal/tools"
)

func TestUpdatePlanNormalizesMultipleInProgressItems(t *testing.T) {
	tool, err := NewUpdatePlanFactory().Build(context.Background(), humberttools.Scope{})
	if err != nil {
		t.Fatal(err)
	}
	output, err := tool.InvokableRun(context.Background(), `{"items":[{"content":"先检查","status":"in_progress"},{"content":"再修复","status":"in_progress"},{"content":"验证","status":"completed"}]}`)
	if err != nil {
		t.Fatalf("multiple in_progress items should be normalized: %v", err)
	}
	var result UpdatePlanOutput
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		t.Fatal(err)
	}
	if !result.Adjusted || result.Items[0].Status != "in_progress" || result.Items[1].Status != "pending" {
		t.Fatalf("unexpected normalized plan: %#v", result)
	}
	if result.Summary != "1/3 completed" || result.Notice == "" {
		t.Fatalf("missing normalization feedback: %#v", result)
	}
}
