package builtin

import (
	"context"
	"errors"
	"fmt"
	"strings"

	einotool "github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"

	humberttools "github.com/sda1-hacker/humbert-agent/internal/tools"
	"github.com/sda1-hacker/humbert-agent/internal/transcript"
)

const contextResourceToolName = "context_resource"

const (
	contextResourceTypeArtifact   = "artifact"
	contextResourceTypeAttachment = "attachment"

	// context_resource 的最终 ToolResult 会被 InferTool 编码为 JSON；这里给字段名、
	// resource_id、转义等控制信息预留空间，避免内容片段本身把整个 recovery budget
	// 顶满。最终的精确额度仍由 Guard 的 reserveRecovery 做兜底。
	contextResourceEnvelopeReserveChars = 1024
)

// ContextArtifactReader 读取被上下文保护层移出模型工作窗口的完整工具结果。
type ContextArtifactReader interface {
	ReadContextArtifact(ctx context.Context, sessionID string, id string) (toolName string, content string, chars int, err error)
}

// ContextResourceFactory 把“超大工具结果读取”和“较早文本附件读取”统一为一个内部资源工具。
// 两种资源都只允许读取当前 Session 私有数据，不允许模型自行拼接磁盘路径。
type ContextResourceFactory struct {
	artifactReader ContextArtifactReader
	history        HistoryRepository
}

func NewContextResourceFactory(artifactReader ContextArtifactReader, history HistoryRepository) (*ContextResourceFactory, error) {
	if artifactReader == nil {
		return nil, errors.New("Context Artifact Reader 不能为空")
	}
	if history == nil {
		return nil, errors.New("Context Resource History Repository 不能为空")
	}
	return &ContextResourceFactory{artifactReader: artifactReader, history: history}, nil
}

func (f *ContextResourceFactory) Descriptor() humberttools.Descriptor {
	return humberttools.Descriptor{Name: contextResourceToolName, Risk: humberttools.RiskRead, Internal: true}
}

type ContextResourceInput struct {
	ResourceType string `json:"resource_type" jsonschema:"description=资源类型：artifact 表示被上下文保护层存档的超大工具结果；attachment 表示较早文本附件的提取正文。"`
	ResourceID   string `json:"resource_id" jsonschema:"description=资源编号。artifact 使用超大工具结果中的 resource_id/artifact_id；attachment 使用历史附件占位中的 attachment ID。"`
	Offset       int    `json:"offset,omitempty" jsonschema:"description=从第几个 Unicode 字符开始读取，默认 0。"`
	Limit        int    `json:"limit,omitempty" jsonschema:"description=最多读取多少字符，默认 8000，最大 16000。"`
}

type ContextResourceOutput struct {
	Status       string `json:"status,omitempty"`
	Message      string `json:"message,omitempty"`
	ResourceType string `json:"resource_type,omitempty"`
	ResourceID   string `json:"resource_id,omitempty"`
	ToolName     string `json:"tool_name,omitempty"`
	Name         string `json:"name,omitempty"`
	MIMEType     string `json:"mime_type,omitempty"`
	Offset       int    `json:"offset,omitempty"`
	End          int    `json:"end,omitempty"`
	TotalChars   int    `json:"total_chars,omitempty"`
	More         bool   `json:"more,omitempty"`
	Content      string `json:"content,omitempty"`
}

func (f *ContextResourceFactory) Build(ctx context.Context, scope humberttools.Scope) (einotool.InvokableTool, error) {
	return utils.InferTool(contextResourceToolName,
		"仅在当前任务确实需要缺失的中间内容时，按需读取当前会话资源。artifact 是被归档的超大工具结果；attachment 是较早的文本附件。按 offset/limit 分段读取；返回 more=false 后不要重复读取同一范围；status=budget_exhausted 时不要重试。",
		func(callCtx context.Context, input *ContextResourceInput) (*ContextResourceOutput, error) {
			if input == nil {
				return nil, errors.New("context_resource 输入不能为空")
			}
			resourceType := strings.ToLower(strings.TrimSpace(input.ResourceType))
			resourceID := strings.TrimSpace(input.ResourceID)
			if resourceID == "" {
				return nil, errors.New("resource_id 不能为空")
			}
			if resourceType != contextResourceTypeArtifact && resourceType != contextResourceTypeAttachment {
				return nil, fmt.Errorf("resource_type 必须是 %q 或 %q", contextResourceTypeArtifact, contextResourceTypeAttachment)
			}
			if input.Offset < 0 {
				return nil, errors.New("offset 不能小于 0")
			}

			limit := input.Limit
			if limit == 0 {
				limit = 8000
			}
			if limit < 1 || limit > 16000 {
				return nil, errors.New("limit 必须在 1-16000 之间")
			}

			// 资源读取属于恢复流量，只受“单结果上限 + RecoveryRemaining”约束。
			// 普通 ToolResult 和归档 Preview 无法消耗 recovery reserve，因此这里不会再被
			// 本轮其它大结果提前挤死。预算不足属于正常控制流，不返回 Go error。
			maxContent := limit
			if scope.ToolResultMaxChars > 0 {
				bySingleResult := scope.ToolResultMaxChars - contextResourceEnvelopeReserveChars
				if bySingleResult < 1 {
					return contextResourceBudgetExhausted(resourceType, resourceID), nil
				}
				if maxContent > bySingleResult {
					maxContent = bySingleResult
				}
			}
			if scope.ToolResultBudget != nil {
				remaining := scope.ToolResultBudget.RecoveryRemaining() - contextResourceEnvelopeReserveChars
				if remaining < 1 {
					return contextResourceBudgetExhausted(resourceType, resourceID), nil
				}
				if maxContent > remaining {
					maxContent = remaining
				}
			}
			if maxContent < 1 {
				return contextResourceBudgetExhausted(resourceType, resourceID), nil
			}
			if limit > maxContent {
				limit = maxContent
			}

			switch resourceType {
			case contextResourceTypeArtifact:
				return f.readArtifact(callCtx, scope, resourceID, input.Offset, limit)
			case contextResourceTypeAttachment:
				return f.readAttachment(callCtx, scope, resourceID, input.Offset, limit)
			}
			return nil, errors.New("无法识别的 context_resource 资源类型")
		})
}

func contextResourceBudgetExhausted(resourceType, resourceID string) *ContextResourceOutput {
	return &ContextResourceOutput{
		Status:       "budget_exhausted",
		Message:      "本轮上下文资源读取额度已用完。不要再次调用 context_resource；请根据已获得的信息回答，或明确说明仍缺少什么。",
		ResourceType: resourceType,
		ResourceID:   resourceID,
	}
}

func (f *ContextResourceFactory) readArtifact(ctx context.Context, scope humberttools.Scope, id string, offset, limit int) (*ContextResourceOutput, error) {
	toolName, content, totalChars, err := f.artifactReader.ReadContextArtifact(ctx, scope.SessionID, id)
	if err != nil {
		return nil, err
	}
	runes := []rune(content)
	if totalChars <= 0 || totalChars != len(runes) {
		totalChars = len(runes)
	}
	end, err := contextResourceRange(offset, limit, len(runes), "工具结果")
	if err != nil {
		return nil, err
	}
	return &ContextResourceOutput{
		Status:       "ok",
		ResourceType: contextResourceTypeArtifact,
		ResourceID:   id,
		ToolName:     toolName,
		Offset:       offset,
		End:          end,
		TotalChars:   totalChars,
		More:         end < len(runes),
		Content:      string(runes[offset:end]),
	}, nil
}

func (f *ContextResourceFactory) readAttachment(ctx context.Context, scope humberttools.Scope, id string, offset, limit int) (*ContextResourceOutput, error) {
	var found *transcript.ContentBlock
	visit := func(entry transcript.Entry) bool {
		if entry.Message == nil {
			return true
		}
		for _, block := range entry.Message.Content {
			if block.Type != transcript.ContentFile || block.AttachmentID != id {
				continue
			}
			found = &block
			return false
		}
		return true
	}
	if indexed, ok := f.history.(indexedHistoryRepository); ok {
		if err := indexed.VisitActiveBranchReverse(ctx, scope.SessionID, visit); err != nil {
			return nil, err
		}
	} else {
		document, err := f.history.LoadTranscript(ctx, scope.SessionID)
		if err != nil {
			return nil, err
		}
		for i := len(document.ActiveBranch) - 1; i >= 0; i-- {
			if !visit(document.ActiveBranch[i]) {
				break
			}
		}
	}
	if found == nil {
		return nil, fmt.Errorf("attachment resource_id 不在当前有效会话分支中: %s", id)
	}
	if found.DocumentOnDemand {
		return nil, errors.New("文档附件请使用 extract_document 和相同的 attachment_id 按需读取 Markdown")
	}
	runes := []rune(found.ExtractedText)
	end, err := contextResourceRange(offset, limit, len(runes), "附件")
	if err != nil {
		return nil, err
	}
	return &ContextResourceOutput{
		Status:       "ok",
		ResourceType: contextResourceTypeAttachment,
		ResourceID:   id,
		Name:         found.Name,
		MIMEType:     found.MIMEType,
		Offset:       offset,
		End:          end,
		TotalChars:   len(runes),
		More:         end < len(runes),
		Content:      string(runes[offset:end]),
	}, nil
}

func contextResourceRange(offset, limit, total int, label string) (int, error) {
	if offset > total {
		return 0, fmt.Errorf("offset=%d 超过%s长度 %d", offset, label, total)
	}
	end := offset + limit
	if end > total {
		end = total
	}
	return end, nil
}
