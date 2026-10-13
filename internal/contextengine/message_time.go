package contextengine

import (
	"time"

	"github.com/cloudwego/eino/schema"
)

// WithMessageTimestamp 将固定发送时间放在用户消息末尾，不修改原始消息。
// 调用方必须传入持久化时间，不能每次重放都使用 time.Now，否则历史前缀仍会漂移。
// 缺少合法时间的旧消息保持原样；实时精确时间由 get_current_time 提供。
func WithMessageTimestamp(message *schema.Message, timestamp string) *schema.Message {
	if message == nil || message.Role != schema.User {
		return message
	}
	sentAt, err := time.Parse(time.RFC3339Nano, timestamp)
	if err != nil {
		return message
	}
	note := "\n\n<message_context>\n  <sent_at>" + sentAt.UTC().Format(time.RFC3339Nano) + "</sent_at>\n</message_context>"
	copyMessage := *message
	if len(message.UserInputMultiContent) > 0 {
		copyMessage.UserInputMultiContent = make([]schema.MessageInputPart, len(message.UserInputMultiContent), len(message.UserInputMultiContent)+1)
		copy(copyMessage.UserInputMultiContent, message.UserInputMultiContent)
		copyMessage.UserInputMultiContent = append(copyMessage.UserInputMultiContent, schema.MessageInputPart{Type: schema.ChatMessagePartTypeText, Text: note})
	} else {
		copyMessage.Content += note
	}
	return &copyMessage
}
