package proactive

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/sda1-hacker/humbert-agent/internal/agents"
	"github.com/sda1-hacker/humbert-agent/internal/workspace"
)

const maxWorkspaceFingerprintFiles = 3000

type WorkspaceMonitor struct {
	agents     *agents.Service
	workspaces *workspace.Manager
}

func NewWorkspaceMonitor(agentService *agents.Service, workspaceManager *workspace.Manager) *WorkspaceMonitor {
	return &WorkspaceMonitor{agents: agentService, workspaces: workspaceManager}
}

func (m *WorkspaceMonitor) Scan(ctx context.Context) ([]WorkspaceSnapshot, error) {
	if m == nil || m.agents == nil || m.workspaces == nil {
		return nil, nil
	}
	values, err := m.agents.List(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]WorkspaceSnapshot, 0, len(values))
	for _, info := range values {
		agent := info.Agent
		resolved, resolveErr := m.workspaces.Resolve(ctx, agent.ID, agent.WorkspaceMode, agent.WorkspacePath)
		if resolveErr != nil {
			continue
		}
		snapshot, scanErr := scanWorkspace(ctx, agent.ID, resolved.RootDir)
		if scanErr != nil {
			continue
		}
		result = append(result, snapshot)
	}
	return result, nil
}

func scanWorkspace(ctx context.Context, agentID, root string) (WorkspaceSnapshot, error) {
	files := make(map[string]WorkspaceFileStamp)
	truncated := false
	count := 0
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if path == root {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}
		relative = filepath.ToSlash(relative)
		if entry.IsDir() {
			base := strings.ToLower(entry.Name())
			switch base {
			case ".git", ".idea", ".vscode", "node_modules", "vendor", "dist", "build", ".next", ".cache":
				return filepath.SkipDir
			}
			return nil
		}
		if count >= maxWorkspaceFingerprintFiles {
			truncated = true
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return nil
		}
		files[relative] = WorkspaceFileStamp{Size: info.Size(), ModUnix: info.ModTime().UTC().UnixNano(), Mode: uint32(info.Mode())}
		count++
		return nil
	})
	if err != nil {
		return WorkspaceSnapshot{}, err
	}
	keys := make([]string, 0, len(files))
	for key := range files {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	hash := sha256.New()
	for _, key := range keys {
		stamp := files[key]
		_, _ = fmt.Fprintf(hash, "%s\x00%d\x00%d\x00%d\n", key, stamp.Size, stamp.ModUnix, stamp.Mode)
	}
	return WorkspaceSnapshot{
		AgentID:     agentID,
		RootDir:     root,
		Files:       files,
		Fingerprint: hex.EncodeToString(hash.Sum(nil)),
		ScannedAt:   time.Now().UTC(),
		Truncated:   truncated,
	}, nil
}

func workspaceChangeSummary(previous, current WorkspaceSnapshot) string {
	added := 0
	modified := 0
	removed := 0
	for path, now := range current.Files {
		before, exists := previous.Files[path]
		if !exists {
			added++
			continue
		}
		if before != now {
			modified++
		}
	}
	for path := range previous.Files {
		if _, exists := current.Files[path]; !exists {
			removed++
		}
	}
	return fmt.Sprintf("工作区发生变化：新增 %d，修改 %d，删除 %d。", added, modified, removed)
}
