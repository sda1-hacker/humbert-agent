package runtime

import (
	"testing"

	"github.com/sda1-hacker/humbert-agent/internal/contextengine"
	"github.com/sda1-hacker/humbert-agent/internal/models"
)

func TestReasoningReplayPolicyForProvider(t *testing.T) {
	t.Parallel()
	tests := []struct {
		provider models.ProviderType
		want     contextengine.ReasoningReplayPolicy
	}{
		{models.ProviderTypeOpenAI, contextengine.ReasoningReplayOmit},
		{models.ProviderTypeOpenAICompatible, contextengine.ReasoningReplayOmit},
		{models.ProviderTypeOllama, contextengine.ReasoningReplayAuto},
		{models.ProviderType("unknown"), contextengine.ReasoningReplayOmit},
	}
	for _, test := range tests {
		if got := reasoningReplayPolicyForProvider(test.provider); got != test.want {
			t.Errorf("provider %q: got %q, want %q", test.provider, got, test.want)
		}
	}
}
