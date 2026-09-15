package main

import (
	"context"
	"errors"
	"fmt"
	"github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
	"io"
	"os"
)

func main() {

	apiKey := os.Getenv("OPENAI_API_KEY")
	baseUrl := os.Getenv("OPEN_BASE_URL")
	//modelName := os.Getenv("OPENAI_MODEL_NAME")
	modelName := "qwen3.8-flash"

	ctx := context.Background()

	chatModel, _ := openai.NewChatModel(ctx, &openai.ChatModelConfig{
		Model:   modelName,
		BaseURL: baseUrl,
		APIKey:  apiKey,
	})

	//searchTool := agenttool.WebSearchTool{}
	//invokableTool, _ := searchTool.Tool()

	tools := []tool.BaseTool{}

	agent, _ := adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{
		Name:        "tools Agent",
		Description: "你是一个测试的 Agent，可以调用注册的工具.",
		Model:       chatModel,
		ToolsConfig: adk.ToolsConfig{
			ToolsNodeConfig: compose.ToolsNodeConfig{
				Tools: tools,
			},
		},
	})

	//msgs := []*schema.Message{schema.UserMessage("当前都有什么可以调用的工具呢?")}
	msgs := []*schema.Message{schema.UserMessage("请你帮我搜索一下国内当前的一些热点新闻.")}

	runner := adk.NewRunner(ctx, adk.RunnerConfig{
		Agent:           agent,
		EnableStreaming: true,
	})

	iter := runner.Run(ctx, msgs)

	for {
		event, ok := iter.Next()
		if !ok { // 流结束
			break
		}
		if event.Err != nil { // 运行出错
			fmt.Println("run error:", event.Err)
			return
		}

		mo := event.Output.MessageOutput
		if mo == nil {
			continue
		}

		switch {
		case mo.MessageStream != nil: // 流式输出（因为开了 EnableStreaming）
			for {
				chunk, err := mo.MessageStream.Recv()
				if errors.Is(err, io.EOF) {
					fmt.Println()
					break
				}
				if err != nil {
					fmt.Println("recv error:", err)
					return
				}
				fmt.Print(chunk.Content) // 逐段打印增量
			}
		case mo.Message != nil: // 兜底：非流式消息
			fmt.Println(mo.Message.Content)
		}
	}

}
