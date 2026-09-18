package contextengine

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	einomodel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// CheckpointGenerator 是持久化压缩和执行中临时压缩共用的唯一语义压缩核心。
// 两条路径只负责各自选择“哪些消息离开当前工作窗口”和“结果是否写入 Transcript”，
// 不再各自维护一套摘要、截断和模型调用算法。
type CheckpointGenerator struct {
	Model einomodel.BaseChatModel

	Estimator Estimator

	ContextWindow   int
	MaxOutputTokens int

	OperationTimeout time.Duration
	ArgumentMaxRunes int
}

// CheckpointInput 描述一次检查点生成任务。PreviousCheckpoint 可以为空；Messages 必须按
// 原始时间顺序传入。Generator 会在请求自身放不下时按顺序分段滚动压缩，不静默删除中间历史。
type CheckpointInput struct {
	PreviousCheckpoint string
	Messages           []*schema.Message
}

// Generate 把完整待压缩消息按顺序吸收到一个结构化 Markdown 检查点中。
//
// 关键保证：
//   - 不对整段历史做“保留头尾、删除中间”的全局截断；
//   - 压缩请求本身先做容量预算；
//   - 一次放不下时按顺序分段，每一段都滚动合并到上一个检查点；
//   - 单条超大消息也会被切成连续片段，而不是只保留开头或尾部。
func (g CheckpointGenerator) Generate(ctx context.Context, input CheckpointInput) (string, error) {
	if ctx == nil {
		return "", errors.New("Checkpoint Generator context.Context 不能为空")
	}
	if g.Model == nil {
		return "", errors.New("Checkpoint Generator Model 不能为空")
	}
	if g.Estimator == nil {
		return "", errors.New("Checkpoint Generator Estimator 不能为空")
	}
	if g.ContextWindow <= 0 {
		return "", errors.New("Checkpoint Generator ContextWindow 必须大于 0")
	}
	if g.MaxOutputTokens <= 0 || g.MaxOutputTokens >= g.ContextWindow {
		return "", errors.New("Checkpoint Generator MaxOutputTokens 无效")
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}

	previous := strings.TrimSpace(input.PreviousCheckpoint)
	history := serializeMessagesForCheckpoint(input.Messages, g.ArgumentMaxRunes)
	if strings.TrimSpace(history) == "" {
		if previous == "" {
			return "", ErrNothingToCompact
		}
		return previous, nil
	}

	// 给 Provider framing 和估算误差留出额外余量。输出空间使用压缩模型自己的
	// MaxOutputTokens，而不是当前聊天模型的配置。
	safety := maxInt(1024, g.ContextWindow/20)
	inputLimit := g.ContextWindow - g.MaxOutputTokens - safety
	systemTokens := g.Estimator.EstimateText(compactionSystemPrompt)
	if inputLimit <= systemTokens+512 {
		return "", fmt.Errorf("%w: 压缩模型可用输入空间不足: window=%d output=%d", ErrContextBudgetExceeded, g.ContextWindow, g.MaxOutputTokens)
	}

	remaining := history
	checkpoint := previous
	for strings.TrimSpace(remaining) != "" {
		if err := ctx.Err(); err != nil {
			return "", err
		}

		baseTokens := systemTokens + 128
		if checkpoint != "" {
			baseTokens += g.Estimator.EstimateText("[Previous checkpoint]\n" + checkpoint)
		}
		available := inputLimit - baseTokens
		if available < 256 {
			// 检查点自身变得过大时，先把检查点收敛一次，再继续吸收后续历史。这里没有
			// 丢弃原信息，而是显式调用模型重新整理现有检查点。
			shrunk, err := g.shrinkCheckpoint(ctx, checkpoint, inputLimit-systemTokens-256)
			if err != nil {
				return "", err
			}
			if strings.TrimSpace(shrunk) == strings.TrimSpace(checkpoint) {
				return "", fmt.Errorf("%w: 检查点自身占满压缩模型输入窗口", ErrContextBudgetExceeded)
			}
			checkpoint = shrunk
			continue
		}

		chunk, rest := splitTextByEstimatedTokens(remaining, available, g.Estimator)
		if strings.TrimSpace(chunk) == "" {
			return "", fmt.Errorf("%w: 无法为压缩请求切出有效历史片段", ErrContextBudgetExceeded)
		}
		next, err := g.generateOnce(ctx, checkpoint, chunk)
		if err != nil {
			return "", err
		}
		checkpoint = next
		remaining = rest
	}

	return checkpoint, nil
}

func (g CheckpointGenerator) generateOnce(ctx context.Context, previous string, chunk string) (string, error) {
	var prompt strings.Builder
	if strings.TrimSpace(previous) != "" {
		prompt.WriteString("[Previous checkpoint]\n")
		prompt.WriteString(strings.TrimSpace(previous))
		prompt.WriteString("\n\n")
	}
	prompt.WriteString("[New history to merge]\n")
	prompt.WriteString(strings.TrimSpace(chunk))

	timeout := g.OperationTimeout
	if timeout <= 0 {
		timeout = 2 * time.Minute
	}
	operationCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	response, err := g.Model.Generate(operationCtx, []*schema.Message{
		schema.SystemMessage(compactionSystemPrompt),
		schema.UserMessage(prompt.String()),
	})
	if err != nil {
		return "", fmt.Errorf("调用模型生成上下文检查点失败: %w", err)
	}
	if response == nil {
		return "", errors.New("压缩模型返回空 Message")
	}
	return normalizeSummaryResult(messageVisibleText(response))
}

func (g CheckpointGenerator) shrinkCheckpoint(ctx context.Context, checkpoint string, maxInputTokens int) (string, error) {
	checkpoint = strings.TrimSpace(checkpoint)
	if checkpoint == "" {
		return "", nil
	}
	if maxInputTokens < 256 {
		return "", fmt.Errorf("%w: 没有足够空间重新整理检查点", ErrContextBudgetExceeded)
	}
	chunk, rest := splitTextByEstimatedTokens(checkpoint, maxInputTokens, g.Estimator)
	if strings.TrimSpace(rest) != "" {
		// 检查点已经是上一轮语义压缩产物。极端情况下再分段滚动合并，而不是截断。
		return g.Generate(ctx, CheckpointInput{Messages: []*schema.Message{schema.UserMessage(checkpoint)}})
	}
	return g.generateOnce(ctx, "", "[Existing checkpoint to condense]\n"+chunk)
}

// serializeMessagesForCheckpoint 把消息转换成可跨 Provider 使用的内部文本协议。与旧
// serializer 不同，这里不截断用户/工具正文；超长内容由 Generate 的分段算法完整吸收。
func serializeMessagesForCheckpoint(messages []*schema.Message, argumentMaxRunes int) string {
	if argumentMaxRunes < 256 {
		argumentMaxRunes = 4096
	}
	var builder strings.Builder
	for _, message := range messages {
		if message == nil {
			continue
		}
		switch message.Role {
		case schema.User:
			builder.WriteString("\n[User]\n")
			builder.WriteString(messageVisibleText(message))
			for _, part := range message.UserInputMultiContent {
				switch part.Type {
				case schema.ChatMessagePartTypeImageURL:
					builder.WriteString("\n[Image attachment]")
				case schema.ChatMessagePartTypeFileURL:
					name := "file"
					if part.File != nil && strings.TrimSpace(part.File.Name) != "" {
						name = part.File.Name
					}
					builder.WriteString("\n[File attachment: ")
					builder.WriteString(name)
					builder.WriteString("]")
					if extracted := extraStringValue(part.Extra, "extracted_text"); strings.TrimSpace(extracted) != "" {
						builder.WriteString("\n[Extracted file text]\n")
						builder.WriteString(extracted)
					}
				}
			}
		case schema.Assistant:
			if strings.TrimSpace(message.ReasoningContent) != "" {
				builder.WriteString("\n[Assistant reasoning]\n")
				builder.WriteString(message.ReasoningContent)
			}
			if strings.TrimSpace(messageVisibleText(message)) != "" {
				builder.WriteString("\n[Assistant]\n")
				builder.WriteString(messageVisibleText(message))
			}
			for _, call := range message.ToolCalls {
				builder.WriteString("\n[Assistant tool call id=")
				builder.WriteString(call.ID)
				builder.WriteString("]\n")
				builder.WriteString(call.Function.Name)
				builder.WriteString("(")
				builder.WriteString(summarizeToolArguments([]byte(call.Function.Arguments), argumentMaxRunes))
				builder.WriteString(")")
			}
		case schema.Tool:
			builder.WriteString("\n[Tool result id=")
			builder.WriteString(message.ToolCallID)
			builder.WriteString(" name=")
			builder.WriteString(message.ToolName)
			builder.WriteString("]\n")
			builder.WriteString(messageVisibleText(message))
		}
		builder.WriteByte('\n')
	}
	return strings.TrimSpace(builder.String())
}

// splitTextByEstimatedTokens 返回 value 的连续前缀和剩余后缀，保证前缀估算不超过 maxTokens。
// 它按 rune 二分查找，因此不会产生非法 UTF-8，也不会像旧全局截断一样跳过中间内容。
func splitTextByEstimatedTokens(value string, maxTokens int, estimator Estimator) (string, string) {
	value = strings.TrimSpace(value)
	if value == "" || maxTokens <= 0 {
		return "", value
	}
	if estimator.EstimateText(value) <= maxTokens {
		return value, ""
	}
	runes := []rune(value)
	low, high := 1, len(runes)
	best := 0
	for low <= high {
		mid := low + (high-low)/2
		candidate := string(runes[:mid])
		if estimator.EstimateText(candidate) <= maxTokens {
			best = mid
			low = mid + 1
		} else {
			high = mid - 1
		}
	}
	if best <= 0 {
		// 极端 estimator 下也至少前进一个 rune，调用方随后会判断实际预算。
		best = 1
	}
	prefix := strings.TrimSpace(string(runes[:best]))
	rest := strings.TrimSpace(string(runes[best:]))
	return prefix, rest
}

// ensureValidUTF8 只用于测试/诊断，保证分段函数永远按 rune 边界工作。
func ensureValidUTF8(value string) bool { return utf8.ValidString(value) }
