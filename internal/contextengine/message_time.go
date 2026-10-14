package contextengine

import (
	"time"

	"github.com/cloudwego/eino/schema"
)

// WithMessageTimestamp 将固定发送时间放在用户消息末尾，不修改原始消息。
// 调用方必须传入持久化时间，不能每次重放都使用 time.Now，否则历史前缀仍会漂移。
// 缺少合法时间的旧消息保持原样；时间属于消息尾部，不向 System 注入每轮变化的时钟。
func WithMessageTimestamp(message *schema.Message, timestamp string) *schema.Message {
	if message == nil || message.Role != schema.User {
		return message
	}
	sentAt, err := time.Parse(time.RFC3339Nano, timestamp)
	if err != nil {
		return message
	}
	// 同一发送时刻带上本机时区偏移，提醒任务可据此理解“明早”等本地时间；重放不读墙钟。
	note := "\n\n<message_context>\n  <sent_at>" + sentAt.In(time.Local).Format(time.RFC3339Nano) + "</sent_at>\n</message_context>"
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
