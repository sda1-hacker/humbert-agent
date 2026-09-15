package builtin

import (
	"context"
	"time"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
)

// GetCurrentTimeInput 是工具的入参结构体。
// eino 会根据这个结构体的字段自动推导出 JSON Schema，
// 塞进传给大模型的 tool 定义里（模型就是靠这个 schema 知道该传什么参数）。
type GetCurrentTimeInput struct {
	// jsonschema_description 这个 tag 很关键：它是写给"模型看"的参数说明，
	// 不是写给人看的普通注释，模型会读它来决定怎么填这个字段。
	TimeZone string `json:"time_zone" jsonschema_description:"IANA 时区名称，例如 Asia/Shanghai，不传则默认使用本地时区"`
}

// GetCurrentTimeOutput 是工具的返回结构体，eino 会自动把它序列化成 JSON 字符串
// 作为 tool 的执行结果，塞回给模型看。
type GetCurrentTimeOutput struct {
	CurrentTime string `json:"current_time"`
}

// NewGetCurrentTimeTool 用 eino 提供的 utils.InferTool 把一个普通 Go 函数
// "翻译"成模型能调用的 InvokableTool，不用手写 JSON Schema。
func NewGetCurrentTimeTool() (tool.InvokableTool, error) {
	return utils.InferTool(
		// 第一个参数：工具名，模型在发起 tool call 时用这个名字指定要调用谁
		"get_current_time",
		// 第二个参数：工具描述，模型靠这段话判断"什么时候该调用这个工具"，
		// 描述写得越准确，模型越不容易瞎调用/漏调用
		"获取当前的日期和时间，当用户询问现在几点、今天星期几等问题时调用",
		// 第三个参数：真正的执行逻辑，函数签名必须是
		// func(ctx, T) (D, error) 这个形状，InferTool 才能正确包装
		func(ctx context.Context, input *GetCurrentTimeInput) (*GetCurrentTimeOutput, error) {
			loc := time.Local
			if input.TimeZone != "" {
				// 模型传了一个非法时区名的话，我们不直接 panic/报错中断整个循环，
				// 而是退回本地时区继续跑——这是"harness"里很重要的一个习惯：
				// 工具内部的小错误不应该打断整个 Agent 循环。
				if l, err := time.LoadLocation(input.TimeZone); err == nil {
					loc = l
				}
			}
			now := time.Now().In(loc)
			return &GetCurrentTimeOutput{
				CurrentTime: now.Format("2006-01-02 15:04:05 MST"),
			}, nil
		},
	)
}
