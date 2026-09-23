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
	ResourceType string `json:"resource_type"`
	ResourceID   string `json:"resource_id"`
	ToolName     string `json:"tool_name,omitempty"`
	Name         string `json:"name,omitempty"`
	MIMEType     string `json:"mime_type,omitempty"`
	Offset       int    `json:"offset"`
	End          int    `json:"end"`
	TotalChars   int    `json:"total_chars"`
	More         bool   `json:"more"`
	Content      string `json:"content"`
}

func (f *ContextResourceFactory) Build(ctx context.Context, scope humberttools.Scope) (einotool.InvokableTool, error) {
	return utils.InferTool(contextResourceToolName,
		"按需读取没有直接留在模型工作窗口中的当前会话资源。resource_type=artifact 读取超大工具结果；resource_type=attachment 读取较早文本附件。请使用 offset/limit 分段读取，避免再次把大资源一次性塞回上下文。",
		func(callCtx context.Context, input *ContextResourceInput) (*ContextResourceOutput, error) {
			if input == nil {
				return nil, errors.New("context_resource 输入不能为空")
			}
			resourceType := strings.ToLower(strings.TrimSpace(input.ResourceType))
			resourceID := strings.TrimSpace(input.ResourceID)
			if resourceID == "" {
				return nil, errors.New("resource_id 不能为空")
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

			switch resourceType {
			case contextResourceTypeArtifact:
				return f.readArtifact(callCtx, scope, resourceID, input.Offset, limit)
			case contextResourceTypeAttachment:
				return f.readAttachment(callCtx, scope, resourceID, input.Offset, limit)
			default:
				return nil, fmt.Errorf("resource_type 必须是 %q 或 %q", contextResourceTypeArtifact, contextResourceTypeAttachment)
			}
		})
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
	return &ContextResourceOutput{ResourceType: contextResourceTypeAttachment, ResourceID: id, Name: found.Name, MIMEType: found.MIMEType, Offset: offset, End: end, TotalChars: len(runes), More: end < len(runes), Content: string(runes[offset:end])}, nil
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
