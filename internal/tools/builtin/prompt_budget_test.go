package builtin

import (
	"testing"

	"github.com/sda1-hacker/humbert-agent/internal/contextengine"
)

func TestCommonToolDescriptionBudget(t *testing.T) {
	descriptions := webSearchToolDescription + webFetchToolDescription + runCommandToolDescription + scheduleTaskDescription + installSkillToolDescription
	tokens := contextengine.NewApproxEstimator().EstimateText(descriptions)
	t.Logf("common tool descriptions: %d estimated tokens", tokens)
	if tokens > 750 {
		t.Fatalf("common tool descriptions exceed budget: %d", tokens)
	}
}
