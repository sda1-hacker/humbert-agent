package contextengine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sync"
	"unicode/utf8"

	einotool "github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	"github.com/sda1-hacker/humbert-agent/internal/multimodal"
)

// Estimator 为 ContextEngine 提供可替换的 Token 估算边界。
//
// 不同 Provider 的 tokenizer 并不统一，Humbert 第一阶段也不引入每家模型专用 tokenizer。
// 默认 ApproxEstimator 使用保守的 Unicode 估算；测试可以注入确定性 Fake Estimator，
// 从而精确覆盖阈值、切点与重复压缩，而不依赖第三方 tokenizer 版本。
type Estimator interface {
	EstimateText(text string) int

	EstimateMessage(message *schema.Message) int

	EstimateMessages(messages []*schema.Message) int

	EstimateTools(ctx context.Context, tools []einotool.BaseTool) (int, error)
}

// ApproxEstimator 是 Humbert 默认 Token Estimator。
//
// 估算策略刻意偏保守：ASCII 内容约 4 字符/token；非 ASCII rune（中文、日文等）按
// 1 rune/token 计；每条消息和 ToolCall 再加入固定协议开销。它不会声称与 Provider
// tokenizer 完全一致，真正的安全性来自 Context Budget 预留的 10%-20% reserve。
type ApproxEstimator struct {
	mu     sync.RWMutex
	factor float64
}

// UsageCalibrator 是可选的真实 Provider 用量校准能力。ContextEngine 只在 Estimator 实现
// 该接口时上报首个模型请求的真实 Prompt Token，不要求其它测试/Fake Estimator 支持。
type UsageCalibrator interface {
	ObservePromptUsage(estimated int, actual int)
}

// NewApproxEstimator 创建默认 Token Estimator。
func NewApproxEstimator() *ApproxEstimator {
	return &ApproxEstimator{factor: 1}
}

func (e *ApproxEstimator) calibrationFactor() float64 {
	e.mu.RLock()
	factor := e.factor
	e.mu.RUnlock()
	if factor <= 0 {
		return 1
	}
	return factor
}

// ObservePromptUsage 使用有界指数移动平均校准默认字符估算。它只修正近似 tokenizer
// 与真实 Provider 的系统性偏差，绝不取消 ContextEngine 自己的安全预留。
func (e *ApproxEstimator) ObservePromptUsage(estimated int, actual int) {
	if estimated <= 0 || actual <= 0 {
		return
	}
	ratio := float64(actual) / float64(estimated)
	if ratio < 0.75 {
		ratio = 0.75
	}
	if ratio > 1.35 {
		ratio = 1.35
	}

	e.mu.Lock()
	current := e.factor
	if current <= 0 {
		current = 1
	}
	// 真实 usage 可能因为 Provider 自己的缓存/协议 framing 有波动，因此每次只吸收 10%。
	next := current*0.90 + current*ratio*0.10
	if next < 0.80 {
		next = 0.80
	}
	if next > 1.30 {
		next = 1.30
	}
	e.factor = next
	e.mu.Unlock()
}

// EstimateText 估算普通 UTF-8 文本的 Token 数。
func (e *ApproxEstimator) EstimateText(text string) int {
	if text == "" {
		return 0
	}

	ascii := 0
	nonASCII := 0
	for len(text) > 0 {
		r, size := utf8.DecodeRuneInString(text)
		if r == utf8.RuneError && size == 1 {
			// 非法 UTF-8 在 Go string 中仍可能存在。按一个 token 计，避免估算函数因
			// 外部 ToolResult 的异常字节而失效；Transcript 自身仍有 UTF-8 边界。
			nonASCII++
			text = text[1:]
			continue
		}
		if r <= 0x7f {
			ascii++
		} else {
			nonASCII++
		}
		text = text[size:]
	}

	asciiTokens := (ascii + 3) / 4
	raw := asciiTokens + nonASCII
	if raw == 0 {
		return 0
	}
	return maxInt(1, int(math.Ceil(float64(raw)*e.calibrationFactor())))
}

// EstimateMessage 估算一条 Eino Message 的协议占用。
func (e *ApproxEstimator) EstimateMessage(message *schema.Message) int {
	return e.estimateMessageWithAttachments(message, true, true)
}

func (e *ApproxEstimator) estimateMessageWithAttachments(message *schema.Message, includeImages bool, includeFiles bool) int {
	if message == nil {
		return 0
	}

	// role/JSON framing/message separators 的近似固定开销。
	tokens := 6
	tokens += e.EstimateText(message.Content)
	tokens += e.EstimateText(message.ReasoningContent)
	tokens += e.EstimateText(message.ToolName)
	tokens += e.EstimateText(message.ToolCallID)

	for _, call := range message.ToolCalls {
		tokens += 8
		tokens += e.EstimateText(call.ID)
		tokens += e.EstimateText(call.Function.Name)
		tokens += e.EstimateText(call.Function.Arguments)
	}

	for _, part := range message.UserInputMultiContent {
		switch part.Type {
		case schema.ChatMessagePartTypeText:
			if message.Content == "" {
				tokens += e.EstimateText(part.Text)
			}
		case schema.ChatMessagePartTypeImageURL:
			if includeImages {
				// Vision tokenization varies by provider. Reserve a conservative fixed budget
				// without ever counting Base64 characters as prompt text.
				tokens += 1024
			} else {
				tokens += e.EstimateText(multimodal.HistoricalImagePlaceholder(part))
			}
		case schema.ChatMessagePartTypeFileURL:
			if !includeFiles {
				tokens += e.EstimateText(multimodal.HistoricalFilePlaceholder(part))
				break
			}
			// 近期文本附件仍按真实提取正文估算；更早附件由 FileReplayMask 转为元数据占位。
			if extracted := extraStringValue(part.Extra, "extracted_text"); extracted != "" {
				tokens += 12 + e.EstimateText(extracted)
			} else {
				tokens += 2048
			}
		}
	}

	for _, part := range message.AssistantGenMultiContent {
		switch part.Type {
		case schema.ChatMessagePartTypeText:
			// 如果兼容字段 Content 已经存在，结构化 Text 通常是同一份内容，不重复计。
			if message.Content == "" {
				tokens += e.EstimateText(part.Text)
			}
		case schema.ChatMessagePartTypeReasoning:
			if message.ReasoningContent == "" && part.Reasoning != nil {
				tokens += e.EstimateText(part.Reasoning.Text)
			}
		}
	}

	return tokens
}

func extraStringValue(extra map[string]any, key string) string {
	if extra == nil {
		return ""
	}
	value, _ := extra[key].(string)
	return value
}

// EstimateMessages 估算完整消息序列。
func (e *ApproxEstimator) EstimateMessages(messages []*schema.Message) int {
	total := 0
	for _, cost := range e.EstimateMessageCosts(messages) {
		total += cost
	}
	return total
}

// EstimateMessageCosts uses the same attachment replay masks as EstimateMessages,
// so a compaction planner can select a boundary from the actual prompt costs.
func (e *ApproxEstimator) EstimateMessageCosts(messages []*schema.Message) []int {
	costs := make([]int, len(messages))
	imageReplayMask := multimodal.ImageReplayMask(messages)
	fileReplayMask := multimodal.FileReplayMask(messages)
	for index, message := range messages {
		costs[index] = e.estimateMessageWithAttachments(message, imageReplayMask[index], fileReplayMask[index])
	}
	return costs
}

// EstimateTools 读取 Eino ToolInfo，并估算 Tool Definition 在模型请求中的占用。
//
// ToolInfo 获取可能来自未来 MCP/Skill 动态 Tool，因此必须接收 context.Context 且不忽略
// Info 错误。JSON 只在内存中用于估算，绝不会写日志，避免 Schema 中未来出现敏感默认值
// 时被日志扩散。
func (e *ApproxEstimator) EstimateTools(ctx context.Context, tools []einotool.BaseTool) (int, error) {
	if ctx == nil {
		return 0, errors.New("context.Context 不能为空")
	}

	total := 0
	for index, tool := range tools {
		if err := ctx.Err(); err != nil {
			return 0, fmt.Errorf("估算 Tool Context 被取消: %w", err)
		}
		if tool == nil {
			continue
		}

		info, err := tool.Info(ctx)
		if err != nil {
			return 0, fmt.Errorf("读取第 %d 个 ToolInfo 失败: %w", index, err)
		}
		if info == nil {
			continue
		}

		encoded, err := json.Marshal(info)
		if err != nil {
			return 0, fmt.Errorf("编码第 %d 个 ToolInfo 用于 Token 估算失败: %w", index, err)
		}
		total += 12 + e.EstimateText(string(encoded))
	}
	return total, nil
}
