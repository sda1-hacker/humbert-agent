package multimodal

import (
	"fmt"
	"strings"

	"github.com/cloudwego/eino/schema"
)

const maxAttachmentReplayFollowUpUserTurns = 1

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
		result[index] = laterUserTurns <= maxAttachmentReplayFollowUpUserTurns
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

// FileReplayMask 与图片使用同样的“当前用户输入 + 紧邻一次追问”策略。更早的文本附件
// 不再把完整提取正文重复发送给 Provider，而改为元数据占位；模型需要旧文件细节时使用
// context_resource 按需读取，完整提取文本仍保存在 Transcript 中。
func FileReplayMask(messages []*schema.Message) []bool {
	result := make([]bool, len(messages))
	laterUserTurns := 0
	for index := len(messages) - 1; index >= 0; index-- {
		message := messages[index]
		if message == nil || message.Role != schema.User {
			continue
		}
		result[index] = laterUserTurns <= maxAttachmentReplayFollowUpUserTurns
		laterUserTurns++
	}
	return result
}

func HistoricalFilePlaceholder(part schema.MessageInputPart) string {
	name := extraString(part.Extra, "name")
	if name == "" && part.File != nil {
		name = part.File.Name
	}
	if name == "" {
		name = "unnamed file"
	}
	mimeType := ""
	if part.File != nil {
		mimeType = strings.TrimSpace(part.File.MIMEType)
	}
	return fmt.Sprintf(
		"[Historical text attachment omitted from full replay; name: %s; MIME: %s; attachment ID: %s. Use context_resource with resource_type=attachment and this attachment ID when exact earlier file content is needed.]",
		name, mimeType, extraString(part.Extra, "attachment_id"),
	)
}
