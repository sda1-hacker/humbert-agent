package runtime

import (
	"github.com/sda1-hacker/humbert-agent/internal/contextengine"
	"github.com/sda1-hacker/humbert-agent/internal/models"
)

// 只有 Ollama 的当前协议明确支持把 thinking 作为历史 Assistant 字段回传。
// OpenAI Chat Completions 与未知兼容端点默认只发送回答和工具事务。
func reasoningReplayPolicyForProvider(provider models.ProviderType) contextengine.ReasoningReplayPolicy {
	if provider == models.ProviderTypeOllama {
		return contextengine.ReasoningReplayAuto
	}
	return contextengine.ReasoningReplayOmit
}
