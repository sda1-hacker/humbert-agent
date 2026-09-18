package workspaceview

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/sda1-hacker/humbert-agent/internal/transcript"
	"github.com/sda1-hacker/humbert-agent/internal/workspace"
)

const maxArtifactSessions = 100

type recordedToolCall struct {
	name      string
	arguments json.RawMessage
}

// artifactCandidate 是尚未补充当前文件状态的“已证明工具文件操作”。
// 只有成功 ToolResult 才能产生 candidate；单独出现 ToolCall 绝不能算作产物。
type artifactCandidate struct {
	path       string
	operation  string
	entryID    string
	toolCallID string
	toolName   string
	occurredAt time.Time
}

// scanArtifacts 扫描最近 Session 的 Active Branch，并把成功文件工具事务投影为产物列表。
//
// 这里故意只识别 Humbert 可以确定路径语义的内置文件工具：
// write_file / edit_file / apply_patch / copy_file / move_file / delete_file。
// run_command、MCP 工具或用户外部编辑器可能修改 Workspace，但 Humbert 无法仅凭会话历史
// 可靠证明具体路径，因此不会被伪装成“Agent 产物”。
func (s *Service) scanArtifacts(ctx context.Context, current workspace.Workspace, limit int) ([]Artifact, error) {
	values, err := s.sessions.List(ctx, current.AgentID)
	if err != nil {
		return nil, err
	}
	if len(values) > maxArtifactSessions {
		values = values[:maxArtifactSessions]
	}

	root, err := s.workspaces.OpenRoot(ctx, current)
	if err != nil {
		return nil, err
	}
	defer root.Close()

	// 同一个文件可能在很多轮里重复修改。工作区“产物”面板默认展示每个路径最近一次
	// 可证明的 Agent 文件操作；完整过程仍然保存在 Session JSONL 中。
	latest := make(map[string]Artifact)
	for _, session := range values {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		// Agent 可以切换 Workspace。旧 Session 的 CWD 属于旧工作区，不能把其中的相对路径
		// 错误映射到当前新工作区，所以这里只汇总 CWD 与当前根目录一致的 Session。
		if !sameFilesystemPath(session.CWD, current.RootDir) {
			continue
		}
		document, loadErr := s.sessions.LoadTranscript(ctx, session.ID)
		if loadErr != nil {
			// 某个旧会话损坏不应该让整个工作区页面不可用。Session Store 自己会负责隔离诊断。
			continue
		}
		candidates := extractArtifactCandidates(document)
		for _, candidate := range candidates {
			normalized, ok := normalizeCurrentWorkspacePath(current.RootDir, candidate.path)
			if !ok {
				continue
			}
			artifact := Artifact{
				Path:         normalized,
				Operation:    candidate.operation,
				SessionID:    session.ID,
				SessionTitle: session.Title,
				EntryID:      candidate.entryID,
				ToolCallID:   candidate.toolCallID,
				ToolName:     candidate.toolName,
				OccurredAt:   candidate.occurredAt,
			}
			if info, statErr := root.Lstat(filepath.FromSlash(normalized)); statErr == nil && info.Mode().IsRegular() {
				artifact.Available = true
				artifact.Size = info.Size()
				artifact.ModifiedAt = info.ModTime().UTC()
			} else if statErr != nil && !errors.Is(statErr, fs.ErrNotExist) {
				// 无法读取状态时仍然保留审计来源，只把 available 视为 false。
			}

			previous, exists := latest[normalized]
			if !exists || artifact.OccurredAt.After(previous.OccurredAt) {
				latest[normalized] = artifact
			}
		}
	}

	result := make([]Artifact, 0, len(latest))
	for _, artifact := range latest {
		result = append(result, artifact)
	}
	sort.SliceStable(result, func(i, j int) bool {
		if result[i].OccurredAt.Equal(result[j].OccurredAt) {
			return result[i].Path < result[j].Path
		}
		return result[i].OccurredAt.After(result[j].OccurredAt)
	})
	if len(result) > limit {
		result = result[:limit]
	}
	return result, nil
}

// extractArtifactCandidates 在一条 Active Branch 中配对 ToolCall 与 ToolResult。
//
// 失败 ToolResult、崩溃后缺失结果、被用户拒绝的 Approval 都不会进入产物列表。
func extractArtifactCandidates(document transcript.Document) []artifactCandidate {
	calls := make(map[string]recordedToolCall)
	result := make([]artifactCandidate, 0)
	for _, entry := range document.ActiveBranch {
		message := entry.Message
		if entry.Type != transcript.EntryMessage || message == nil {
			continue
		}
		switch message.Role {
		case transcript.RoleAssistant:
			for _, block := range message.Content {
				if block.Type != transcript.ContentToolCall || strings.TrimSpace(block.ID) == "" {
					continue
				}
				calls[block.ID] = recordedToolCall{
					name:      strings.TrimSpace(block.Name),
					arguments: append(json.RawMessage(nil), block.Arguments...),
				}
			}

		case transcript.RoleToolResult:
			if message.IsError || strings.TrimSpace(message.ToolCallID) == "" {
				continue
			}
			call, exists := calls[message.ToolCallID]
			if !exists {
				continue
			}
			occurredAt, _ := time.Parse(time.RFC3339Nano, entry.Timestamp)
			text := transcriptText(message.Content)
			for _, change := range artifactChangesFromSuccessfulResult(call.name, call.arguments, text) {
				if strings.TrimSpace(change.path) == "" {
					continue
				}
				result = append(result, artifactCandidate{
					path:       change.path,
					operation:  change.operation,
					entryID:    entry.ID,
					toolCallID: message.ToolCallID,
					toolName:   call.name,
					occurredAt: occurredAt,
				})
			}
		}
	}
	return result
}

type artifactChange struct {
	path      string
	operation string
}

func artifactChangesFromSuccessfulResult(toolName string, arguments json.RawMessage, resultText string) []artifactChange {
	switch strings.TrimSpace(toolName) {
	case "write_file":
		var output struct {
			Path        string `json:"path"`
			Created     bool   `json:"created"`
			Overwritten bool   `json:"overwritten"`
		}
		if json.Unmarshal([]byte(resultText), &output) != nil || strings.TrimSpace(output.Path) == "" {
			return nil
		}
		operation := "modified"
		if output.Created {
			operation = "created"
		}
		return []artifactChange{{path: output.Path, operation: operation}}

	case "edit_file":
		var output struct {
			Path string `json:"path"`
		}
		if json.Unmarshal([]byte(resultText), &output) != nil || strings.TrimSpace(output.Path) == "" {
			return nil
		}
		return []artifactChange{{path: output.Path, operation: "modified"}}

	case "apply_patch":
		var output struct {
			Files []string `json:"files"`
		}
		if json.Unmarshal([]byte(resultText), &output) != nil {
			return nil
		}
		created := make(map[string]bool)
		var input struct {
			Changes []struct {
				Path   string `json:"path"`
				Create bool   `json:"create"`
			} `json:"changes"`
		}
		_ = json.Unmarshal(arguments, &input)
		for _, change := range input.Changes {
			if change.Create {
				created[filepath.ToSlash(filepath.Clean(change.Path))] = true
			}
		}
		changes := make([]artifactChange, 0, len(output.Files))
		for _, path := range output.Files {
			operation := "modified"
			if created[filepath.ToSlash(filepath.Clean(path))] {
				operation = "created"
			}
			changes = append(changes, artifactChange{path: path, operation: operation})
		}
		return changes

	case "copy_file":
		var output struct {
			Destination string `json:"destination"`
		}
		if json.Unmarshal([]byte(resultText), &output) != nil || strings.TrimSpace(output.Destination) == "" {
			return nil
		}
		return []artifactChange{{path: output.Destination, operation: "copied"}}

	case "move_file":
		var output struct {
			Destination string `json:"destination"`
		}
		if json.Unmarshal([]byte(resultText), &output) != nil || strings.TrimSpace(output.Destination) == "" {
			return nil
		}
		return []artifactChange{{path: output.Destination, operation: "moved"}}

	case "delete_file":
		var output struct {
			Path string `json:"path"`
		}
		if json.Unmarshal([]byte(resultText), &output) != nil || strings.TrimSpace(output.Path) == "" {
			return nil
		}
		return []artifactChange{{path: output.Path, operation: "deleted"}}
	default:
		return nil
	}
}

func transcriptText(blocks []transcript.ContentBlock) string {
	var builder strings.Builder
	for _, block := range blocks {
		if block.Type == transcript.ContentText {
			builder.WriteString(block.Text)
		}
	}
	return strings.TrimSpace(builder.String())
}

// normalizeCurrentWorkspacePath 同时接受工具结果中的相对路径和绝对路径。
// 绝对路径只有确实位于当前 Workspace Root 下才会被转换为相对路径；Additional Write Path
// 等 Workspace 外路径不会出现在当前工作区产物面板。
func normalizeCurrentWorkspacePath(rootDir, input string) (string, bool) {
	input = strings.TrimSpace(input)
	if input == "" {
		return "", false
	}
	path := filepath.FromSlash(input)
	if filepath.IsAbs(path) || filepath.VolumeName(path) != "" {
		relative, err := filepath.Rel(rootDir, filepath.Clean(path))
		if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return "", false
		}
		path = relative
	}
	normalized, err := workspace.NormalizeRelativePath(path)
	if err != nil || normalized == "." {
		return "", false
	}
	return filepath.ToSlash(normalized), true
}

func sameFilesystemPath(left, right string) bool {
	left = filepath.Clean(strings.TrimSpace(left))
	right = filepath.Clean(strings.TrimSpace(right))
	if left == "" || right == "" {
		return false
	}
	if runtime.GOOS == "windows" {
		return strings.EqualFold(left, right)
	}
	return left == right
}
