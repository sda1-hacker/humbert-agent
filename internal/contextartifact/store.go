package contextartifact

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/sda1-hacker/humbert-agent/internal/atomicfile"
)

const currentVersion = 1

// SessionDirectoryResolver 只允许 Store 从 SessionService 获取受控目录，避免模型或 Tool
// 参数直接参与磁盘路径拼接。
type SessionDirectoryResolver interface {
	SessionDirectory(ctx context.Context, sessionID string) (string, error)
}

// Artifact 是被上下文保护层从模型工作窗口移出的完整大结果。模型只收到短引用，原文仍
// 保存在 Session 私有 sidecar 中，可通过 context_resource 按需读取。
type Artifact struct {
	Version   int       `json:"version"`
	ID        string    `json:"id"`
	SessionID string    `json:"sessionId"`
	ToolName  string    `json:"toolName,omitempty"`
	Content   string    `json:"content"`
	Chars     int       `json:"chars"`
	CreatedAt time.Time `json:"createdAt"`
}

type Store struct {
	directories SessionDirectoryResolver
	mu          sync.Mutex
}

func NewStore(directories SessionDirectoryResolver) (*Store, error) {
	if directories == nil {
		return nil, errors.New("Context Artifact Store SessionDirectoryResolver 不能为空")
	}
	return &Store{directories: directories}, nil
}

func (s *Store) Archive(ctx context.Context, sessionID string, toolName string, content string) (string, error) {
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
	path, err := s.path(ctx, sessionID, id)
	if err != nil {
		return "", err
	}
	document := Artifact{
		Version: currentVersion, ID: id, SessionID: sessionID, ToolName: strings.TrimSpace(toolName),
		Content: content, Chars: len([]rune(content)), CreatedAt: time.Now().UTC(),
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := atomicfile.WriteJSON(ctx, path, 0o600, document); err != nil {
		return "", fmt.Errorf("保存 Context Artifact 失败: %w", err)
	}
	return id, nil
}

func (s *Store) Read(ctx context.Context, sessionID string, id string) (Artifact, error) {
	path, err := s.path(ctx, sessionID, id)
	if err != nil {
		return Artifact{}, err
	}
	var document Artifact
	if err := atomicfile.ReadJSON(ctx, path, &document); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Artifact{}, fmt.Errorf("Context Artifact 不存在: %s", id)
		}
		return Artifact{}, err
	}
	if document.Version != currentVersion || document.SessionID != sessionID || document.ID != id {
		return Artifact{}, errors.New("Context Artifact 身份校验失败")
	}
	return document, nil
}

func (s *Store) path(ctx context.Context, sessionID string, id string) (string, error) {
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
	directory, err := s.directories.SessionDirectory(ctx, sessionID)
	if err != nil {
		return "", err
	}
	return filepath.Join(directory, "context-artifacts", id+".json"), nil
}

// ReadContextArtifact 适配内部 context_resource Tool 的最小工具结果读取接口。
func (s *Store) ReadContextArtifact(ctx context.Context, sessionID string, id string) (string, string, int, error) {
	document, err := s.Read(ctx, sessionID, id)
	if err != nil {
		return "", "", 0, err
	}
	return document.ToolName, document.Content, document.Chars, nil
}
