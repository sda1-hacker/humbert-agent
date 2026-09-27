package tools

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/adk/middlewares/reduction"
	"github.com/cloudwego/eino/schema"
)

// Reduction 将 Eino 的工具结果清理策略接到会话资源存储。
// 归档发生在截断之前，Builtin 和 MCP 使用同一条链路；恢复工具按页读取，不递归归档。
func (r *Registry) Reduction(ctx context.Context, scope Scope, names []string, threshold int, counter func([]*schema.Message, []*schema.ToolInfo) (int, error)) (adk.ChatModelAgentMiddleware, error) {
	if r.resultArchiver == nil {
		return nil, errors.New("工具结果归档器未配置")
	}
	limit := scope.ToolResultMaxChars
	if limit <= 0 {
		limit = 16000
	}
	configs := make(map[string]*reduction.ToolReductionConfig, len(names))
	for _, name := range names {
		if name == "context_resource" || name == "skill" {
			configs[name] = &reduction.ToolReductionConfig{SkipClear: true, SkipTruncation: true}
			continue
		}
		configs[name] = &reduction.ToolReductionConfig{
			TruncHandler: func(ctx context.Context, detail *reduction.ToolDetail) (*reduction.TruncResult, error) {
				content, ok := textToolResult(detail.ToolResult)
				if !ok || utf8.RuneCountInString(content) <= limit {
					return &reduction.TruncResult{}, nil
				}
				preview, err := r.archiveResult(ctx, scope, detail.ToolContext.Name, content, limit/2)
				if err != nil {
					return nil, err
				}
				return &reduction.TruncResult{NeedTrunc: true, ToolResult: stringToolResult(preview)}, nil
			},
			ClearHandler: func(ctx context.Context, detail *reduction.ToolDetail) (*reduction.ClearResult, error) {
				content, ok := textToolResult(detail.ToolResult)
				if !ok || utf8.RuneCountInString(content) < 1024 || isArchivedResult(content) {
					return &reduction.ClearResult{}, nil
				}
				preview, err := r.archiveResult(ctx, scope, detail.ToolContext.Name, content, 256)
				if err != nil {
					return nil, err
				}
				// 保留原调用参数以便审计与文件效果展示，仅清理结果正文。
				return &reduction.ClearResult{NeedClear: true, ToolArgument: detail.ToolArgument, ToolResult: stringToolResult(preview)}, nil
			},
		}
	}
	// 默认工具不做处理，已注册工具使用资源 ID 协议。ClearAtLeastTokens 让 Eino 在副本上
	// 尝试清理且只提交确实减少占用的改写，避免原地修改与原始事件共享的消息。
	return reduction.New(ctx, &reduction.Config{
		SkipTruncation: true, SkipClear: true, ToolConfig: configs,
		MaxTokensForClear: int64(max(1, threshold)), ClearRetentionSuffixLimit: 2, ClearAtLeastTokens: 1,
		TruncExcludeTools: []string{"skill", "context_resource"}, ClearExcludeTools: []string{"skill", "context_resource"},
		TokenCounter: func(_ context.Context, msgs []*schema.Message, infos []*schema.ToolInfo) (int64, error) {
			n, err := counter(msgs, infos)
			return int64(n), err
		},
	})
}

func textToolResult(result *schema.ToolResult) (string, bool) {
	if result == nil || len(result.Parts) != 1 || result.Parts[0].Type != schema.ToolPartTypeText {
		return "", false
	}
	return result.Parts[0].Text, true
}
func stringToolResult(value string) *schema.ToolResult {
	return &schema.ToolResult{Parts: []schema.ToolOutputPart{{Type: schema.ToolPartTypeText, Text: value}}}
}
func isArchivedResult(value string) bool {
	var v struct {
		Truncated bool `json:"humbert_context_result_truncated"`
	}
	return json.Unmarshal([]byte(value), &v) == nil && v.Truncated
}

func (r *Registry) archiveResult(ctx context.Context, scope Scope, name, content string, previewChars int) (string, error) {
	if isArchivedResult(content) {
		return content, nil
	}
	id, err := r.resultArchiver.Archive(ctx, scope.SessionID, name, content)
	if err != nil {
		return "", err
	}
	runes := []rune(content)
	previewChars = min(previewChars, len(runes)/2)
	raw, err := json.Marshal(map[string]any{
		"humbert_context_result_truncated": true, "resource_type": "artifact", "resource_id": id, "tool": name, "original_chars": len(runes),
		"head": string(runes[:previewChars]), "tail": string(runes[len(runes)-previewChars:]),
		"instruction": "完整结果已归档。需要原文时调用 context_resource，使用 resource_type=artifact 和 resource_id，按 offset/limit 读取。",
	})
	return strings.TrimSpace(string(raw)), err
}
