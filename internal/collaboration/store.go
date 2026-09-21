package collaboration

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/sda1-hacker/humbert-agent/internal/atomicfile"
)

const runSchemaVersion = 1

type Store struct {
	sessions SessionDirectoryResolver
	mu       sync.Mutex
}

func NewStore(sessions SessionDirectoryResolver) (*Store, error) {
	if sessions == nil {
		return nil, errors.New("Collaboration Store SessionDirectoryResolver 不能为空")
	}
	return &Store{sessions: sessions}, nil
}

func (s *Store) Load(ctx context.Context, sessionID string, runID string) (Run, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	path, err := s.runPath(ctx, sessionID, runID)
	if err != nil {
		return Run{}, false, err
	}
	var value Run
	if err := atomicfile.ReadJSON(ctx, path, &value); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Run{}, false, nil
		}
		return Run{}, false, err
	}
	if value.SchemaVersion != runSchemaVersion || value.ID != runID || value.ParentSessionID != sessionID {
		return Run{}, false, errors.New("子 Agent 运行记录身份或 schema_version 无效")
	}
	return value, true, nil
}

func (s *Store) Save(ctx context.Context, value Run) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	value.SchemaVersion = runSchemaVersion
	path, err := s.runPath(ctx, value.ParentSessionID, value.ID)
	if err != nil {
		return err
	}
	if err := atomicfile.WriteJSON(ctx, path, 0o600, value); err != nil {
		return fmt.Errorf("保存子 Agent 运行记录失败: %w", err)
	}
	return nil
}

func (s *Store) runPath(ctx context.Context, sessionID string, runID string) (string, error) {
	if sessionID == "" || runID == "" {
		return "", errors.New("子 Agent SessionID/RunID 不能为空")
	}
	if filepath.Base(runID) != runID || strings.Contains(runID, "..") {
		return "", errors.New("子 Agent RunID 不是安全文件名")
	}
	directory, err := s.sessions.SessionDirectory(ctx, sessionID)
	if err != nil {
		return "", fmt.Errorf("解析父 Session 目录失败: %w", err)
	}
	return filepath.Join(directory, "subagents", runID+".json"), nil
}
