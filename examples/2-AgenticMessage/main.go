package main

import (
	"context"
	"fmt"
	"github.com/cloudwego/eino-ext/components/model/agenticopenai"
	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/tool"
	toolutils "github.com/cloudwego/eino/components/tool/utils"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
	"log"
	"os"
)

// ============================================================
// 1. 定义 Tool 的输入输出
// ============================================================

type WeatherInput struct {
	City string `json:"city" jsonschema:"description=要查询天气的城市，例如北京、上海"`
}

type WeatherOutput struct {
	City        string `json:"city"`
	Temperature int    `json:"temperature"`
	Condition   string `json:"condition"`
}

// ============================================================
// 2. 创建 Tool
// ============================================================

func newWeatherTool() tool.InvokableTool {
	t, err := toolutils.InferTool(
		"get_weather",
		"查询指定城市当前天气。当用户询问天气时必须调用此工具。",
		func(ctx context.Context, input *WeatherInput) (*WeatherOutput, error) {

			// Demo 中直接返回固定数据。
			// 实际项目这里可以调用：
			// - OpenWeather
			// - 高德天气
			// - 数据库
			// - 内部 RPC
			fmt.Printf(
				"\n[tool implementation] querying weather: city=%s\n",
				input.City,
			)

			return &WeatherOutput{
				City:        input.City,
				Temperature: 26,
				Condition:   "晴",
			}, nil
		},
	)
	if err != nil {
		log.Fatalf("create weather tool failed: %v", err)
	}

	return t
}

func printAgenticMessage(msg *schema.AgenticMessage) {
	if msg == nil {
		return
	}

	fmt.Printf("\n========== AgenticMessage ==========\n")
	fmt.Printf("role: %s\n", msg.Role)

	for i, block := range msg.ContentBlocks {
		if block == nil {
			continue
		}

		fmt.Printf("block[%d]:\n", i)

		switch {

		// -------------------------------
		// reasoning
		// -------------------------------
		case block.Reasoning != nil:
			fmt.Printf("  type: reasoning\n")
			fmt.Printf("  text: %s\n", block.Reasoning.Text)

		// -------------------------------
		// assistant text
		// -------------------------------
		case block.AssistantGenText != nil:
			fmt.Printf("  type: assistant_gen_text\n")
			fmt.Printf("  text: %s\n",
				block.AssistantGenText.Text,
			)

		// -------------------------------
		// function tool call
		// -------------------------------
		case block.FunctionToolCall != nil:
			call := block.FunctionToolCall

			fmt.Printf("  type: function_tool_call\n")
			fmt.Printf("  call_id: %s\n", call.CallID)
			fmt.Printf("  name: %s\n", call.Name)
			fmt.Printf("  arguments: %s\n", call.Arguments)

		// -------------------------------
		// function tool result
		// -------------------------------
		case block.FunctionToolResult != nil:
			result := block.FunctionToolResult

			fmt.Printf("  type: function_tool_result\n")
			fmt.Printf("  call_id: %s\n", result.CallID)
			fmt.Printf("  name: %s\n", result.Name)

			for j, content := range result.Content {
				if content == nil {
					continue
				}

				if content.Text != nil {
					fmt.Printf(
						"  content[%d]: %s\n",
						j,
						content.Text.Text,
					)
				}
			}

		// -------------------------------
		// server-side tool
		// -------------------------------
		case block.ServerToolCall != nil:
			fmt.Printf("  type: server_tool_call\n")
			fmt.Printf("  name: %s\n",
				block.ServerToolCall.Name,
			)

		case block.ServerToolResult != nil:
			fmt.Printf("  type: server_tool_result\n")
			fmt.Printf("  name: %s\n",
				block.ServerToolResult.Name,
			)

		default:
			fmt.Printf("  type: %s\n", block.Type)
		}
	}

	fmt.Printf("====================================\n")
}

func main() {
	ctx := context.Background()

	apiKey := os.Getenv("OPENAI_API_KEY")
	modelName := "qwen3.7-flash"
	baseUrl := os.Getenv("OPEN_BASE_URL")

	agenticModel, err := agenticopenai.NewResponsesModel(
		ctx,
		&agenticopenai.ResponsesConfig{
			APIKey:  apiKey,
			Model:   modelName,
			BaseURL: baseUrl,
		},
	)
	if err != nil {
		log.Fatalf("create agentic model failed: %v", err)
	}

	weatherTool := newWeatherTool()

	agent, err := adk.NewTypedChatModelAgent[*schema.AgenticMessage](ctx, &adk.TypedChatModelAgentConfig[*schema.AgenticMessage]{
		Name:        "测试 Agent",
		Description: "测试 Agent",
		Instruction: "你是一个用于测试的 Agent，可以调用工具",
		Model:       agenticModel,
		ToolsConfig: adk.ToolsConfig{
			ToolsNodeConfig: compose.ToolsNodeConfig{
				Tools: []tool.BaseTool{weatherTool},
			},
		},
	})
	if err != nil {
		log.Printf("err: %v \n", err)
	}
	runner := adk.NewTypedRunner[*schema.AgenticMessage](adk.TypedRunnerConfig[*schema.AgenticMessage]{
		Agent:           agent,
		EnableStreaming: true,
	})
	iter := runner.Query(ctx, "")

	for {
		event, ok := iter.Next()
		if !ok {
			break
		}

		if event.Err != nil {
			log.Fatalf("agent error: %v", event.Err)
		}

		if event.Output == nil {
			continue
		}

		if event.Output.MessageOutput == nil {
			continue
		}

		msg, err := event.Output.MessageOutput.GetMessage()
		if err != nil {
			log.Printf("get message failed: %v", err)
			continue
		}

		printAgenticMessage(msg)
	}

}
