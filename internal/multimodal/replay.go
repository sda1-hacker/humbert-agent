package multimodal

import (
	"fmt"
	"strings"

	"github.com/cloudwego/eino/schema"
)

const maxImageReplayFollowUpUserTurns = 1

// ImageReplayMask 返回每条 Provider Context Message 是否仍应携带图片二进制。
// 最新 User Message 以及紧邻它的上一个 User Message 保留图片，以支持自然的图片追问；
// 再早的图片应由调用方转换为 HistoricalImagePlaceholder。
func ImageReplayMask(messages []*schema.Message) []bool {
	result := make([]bool, len(messages))
	laterUserTurns := 0
	for index := len(messages) - 1; index >= 0; index-- {
		message := messages[index]
		if message == nil || message.Role != schema.User {
			continue
		}
		result[index] = laterUserTurns <= maxImageReplayFollowUpUserTurns
		laterUserTurns++
	}
	return result
}

// HistoricalImagePlaceholder 保留已停止重放二进制的图片身份，但不把 sidecar 内容送入
// Provider。Context 估算与真正的附件水合共同使用本函数，避免 Usage 与请求内容漂移。
func HistoricalImagePlaceholder(part schema.MessageInputPart) string {
	name := extraString(part.Extra, "name")
	if name == "" {
		name = "unnamed image"
	}
	mimeType := ""
	if part.Image != nil {
		mimeType = strings.TrimSpace(part.Image.MIMEType)
	}
	return fmt.Sprintf(
		"[Historical image attachment omitted from binary replay; name: %s; MIME: %s; attachment ID: %s. Use the surrounding conversation for prior observations.]",
		name,
		mimeType,
		extraString(part.Extra, "attachment_id"),
	)
}

func extraString(values map[string]any, key string) string {
	if values == nil {
		return ""
	}
	value, _ := values[key].(string)
	return strings.TrimSpace(value)
}
