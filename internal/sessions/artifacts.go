package sessions

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/sda1-hacker/humbert-agent/internal/atomicfile"
)

const contextArtifactVersion = 1

// contextArtifact 是被上下文保护层从模型工作窗口移出的完整大结果。模型只收到短引用，原文仍
// 保存在 Session 私有 sidecar 中，可通过 context_resource 按需读取。
type contextArtifact struct {
	Version   int       `json:"version"`
	ID        string    `json:"id"`
	SessionID string    `json:"sessionId"`
	ToolName  string    `json:"toolName,omitempty"`
	Content   string    `json:"content"`
	Chars     int       `json:"chars"`
	CreatedAt time.Time `json:"createdAt"`
}

// Archive 保存不可变的完整工具结果，实现 Tools 的 ResultArchiver 契约。
// 每次创建独立 UUID 文档，不读取/修改旧结果；原子落盘已保证完整性，无需全局写锁。
// 目录直接由 SessionService 解析，归档和附件随同一 Session 删除。
func (s *Service) Archive(ctx context.Context, sessionID string, toolName string, content string) (string, error) {
	if ctx == nil {
		return "", errors.New("context.Context 不能为空")
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return "", errors.New("Session ID 不能为空")
	}
	id := "artifact_" + uuid.NewString()
	path, err := s.contextArtifactPath(ctx, sessionID, id)
	if err != nil {
		return "", err
	}
	document := contextArtifact{
		Version: contextArtifactVersion, ID: id, SessionID: sessionID, ToolName: strings.TrimSpace(toolName),
		Content: content, Chars: len([]rune(content)), CreatedAt: time.Now().UTC(),
	}
	if err := atomicfile.WriteJSON(ctx, path, 0o600, document); err != nil {
		return "", fmt.Errorf("保存 Context Artifact 失败: %w", err)
	}
	return id, nil
}

// ReadContextArtifact 只读取当前 Session 的归档，保留版本、SessionID 与 ID 校验。
// 返回最小工具契约，不向调用方暴露物理路径或内部持久化结构。
func (s *Service) ReadContextArtifact(ctx context.Context, sessionID string, id string) (string, string, int, error) {
	path, err := s.contextArtifactPath(ctx, sessionID, id)
	if err != nil {
		return "", "", 0, err
	}
	var document contextArtifact
	if err := atomicfile.ReadJSON(ctx, path, &document); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", "", 0, fmt.Errorf("Context Artifact 不存在: %s", id)
		}
		return "", "", 0, err
	}
	if document.Version != contextArtifactVersion || document.SessionID != sessionID || document.ID != id {
		return "", "", 0, errors.New("Context Artifact 身份校验失败")
	}
	return document.ToolName, document.Content, document.Chars, nil
}

func (s *Service) contextArtifactPath(ctx context.Context, sessionID string, id string) (string, error) {
	if ctx == nil {
		return "", errors.New("context.Context 不能为空")
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	id = strings.TrimSpace(id)
	if !strings.HasPrefix(id, "artifact_") || strings.ContainsAny(id, `/\\`) || len(id) > 96 {
		return "", errors.New("Context Artifact ID 无效")
	}
	directory, err := s.SessionDirectory(ctx, sessionID)
	if err != nil {
		return "", err
	}
	return filepath.Join(directory, "context-artifacts", id+".json"), nil
}
