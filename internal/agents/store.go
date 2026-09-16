package agents

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/sda1-hacker/humbert-agent/internal/atomicfile"
	"github.com/sda1-hacker/humbert-agent/internal/transcript"
	"github.com/sda1-hacker/humbert-agent/internal/workspace"
)

const (
	agentConfigFileName        = "config.json"
	agentConfigSchemaVersion   = 1
	agentDeletionFileName      = ".deleting.json"
	agentDeletionSchemaVersion = 1
)

type agentDocument struct {
	SchemaVersion int `json:"schema_version"`

	Agent Agent `json:"agent"`
}

type agentDeletionDocument struct {
	SchemaVersion int `json:"schema_version"`

	Deletion DeletionState `json:"deletion"`
}

// Store 负责 Agent Profile 的文件持久化。
//
// 文件布局：
//
//	~/.humbert-agent/agents/<agent-id>/config.json
//	~/.humbert-agent/agents/<agent-id>/sessions/<session-id>/{config.json,session.jsonl}
//
// Store 管理 agents/<agent-id>/ 下的 Humbert 内部数据边界，但不校验 Model、
// 不解析 Workspace；这些属于 Agent Service。Session 数量由 TranscriptStore 统计。
//
// Agent Profile 更新属于低频控制面操作，Store 使用一个 RWMutex 串行化配置目录
// 的创建、更新和删除。这个锁不会覆盖 Session JSONL，多个 Agent Run 仍可以并行
// 写各自 Session 文件。
type Store struct {
	agentsRoot string

	transcripts *transcript.Store

	mu sync.RWMutex
}

// NewStore 创建 Agent Store。
func NewStore(
	ctx context.Context,
	agentsRoot string,
	transcripts *transcript.Store,
) (*Store, error) {
	if ctx == nil {
		return nil, errors.New("context.Context 不能为空")
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("初始化 Agent Store 被取消: %w", err)
	}

	agentsRoot = strings.TrimSpace(agentsRoot)
	if agentsRoot == "" {
		return nil, errors.New("Agent Root 不能为空")
	}
	if transcripts == nil {
		return nil, errors.New("Agent Store TranscriptStore 不能为空")
	}

	absoluteRoot, err := filepath.Abs(agentsRoot)
	if err != nil {
		return nil, fmt.Errorf("解析 Agent Root 失败: %w", err)
	}
	absoluteRoot = filepath.Clean(absoluteRoot)

	if err := os.MkdirAll(absoluteRoot, 0o700); err != nil {
		return nil, fmt.Errorf("创建 Agent Root 失败: %w", err)
	}
	if err := validateRealDirectory(absoluteRoot); err != nil {
		return nil, fmt.Errorf("验证 Agent Root 失败: %w", err)
	}

	return &Store{
		agentsRoot:  absoluteRoot,
		transcripts: transcripts,
	}, nil
}

// Create 创建 Agent Profile。
func (s *Store) Create(ctx context.Context, value Agent) error {
	if err := validateStoreContext(ctx, "创建 Agent Profile"); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	path, directory, err := s.configPath(value.ID)
	if err != nil {
		return err
	}

	if _, err := os.Lstat(path); err == nil {
		return fmt.Errorf("Agent 已存在: %s", value.ID)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("检查 Agent Profile 是否存在失败: %w", err)
	}
	deletionPath := filepath.Join(directory, agentDeletionFileName)
	if _, err := os.Lstat(deletionPath); err == nil {
		return fmt.Errorf("%w: %s", ErrDeleting, value.ID)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("检查 Agent 删除状态失败: %w", err)
	}

	if err := ensureRealDirectory(directory); err != nil {
		return fmt.Errorf("准备 Agent 目录失败: %w", err)
	}
	if err := ensureRealDirectory(filepath.Join(directory, "sessions")); err != nil {
		return fmt.Errorf("准备 Agent Session 目录失败: %w", err)
	}

	if err := atomicfile.WriteJSON(ctx, path, 0o600, agentDocument{
		SchemaVersion: agentConfigSchemaVersion,
		Agent:         value,
	}); err != nil {
		return fmt.Errorf("创建 Agent Profile 失败: %w", err)
	}

	return nil
}

// Update 原子更新 Agent Profile。
func (s *Store) Update(ctx context.Context, value Agent) error {
	if err := validateStoreContext(ctx, "更新 Agent Profile"); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	path, directory, err := s.configPath(value.ID)
	if err != nil {
		return err
	}
	if err := validateRealDirectory(directory); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ErrNotFound
		}
		return fmt.Errorf("验证 Agent 目录失败: %w", err)
	}
	if deleting, err := deletionMarkerExists(directory); err != nil {
		return fmt.Errorf("检查 Agent 删除状态失败: %w", err)
	} else if deleting {
		return fmt.Errorf("%w: %s", ErrDeleting, value.ID)
	}
	if _, err := os.Lstat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ErrNotFound
		}
		return fmt.Errorf("检查 Agent Profile 失败: %w", err)
	}

	if err := atomicfile.WriteJSON(ctx, path, 0o600, agentDocument{
		SchemaVersion: agentConfigSchemaVersion,
		Agent:         value,
	}); err != nil {
		return fmt.Errorf("更新 Agent Profile 失败: %w", err)
	}
	return nil
}

// Get 返回指定 Agent。
//
// ModelDisplayName 由 Agent Service 使用 ModelRegistry 补充。Store 不跨领域读取
// models.json，避免重新制造类似 SQL JOIN 的持久化层耦合。
func (s *Store) Get(ctx context.Context, id string) (AgentInfo, error) {
	if err := validateStoreContext(ctx, "读取 Agent Profile"); err != nil {
		return AgentInfo{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	_, directory, err := s.configPath(id)
	if err != nil {
		return AgentInfo{}, err
	}
	if deleting, markerErr := deletionMarkerExists(directory); markerErr != nil {
		return AgentInfo{}, fmt.Errorf("检查 Agent 删除状态失败: %w", markerErr)
	} else if deleting {
		return AgentInfo{}, fmt.Errorf("%w: %s", ErrDeleting, id)
	}

	value, err := s.readAgentLocked(ctx, id)
	if err != nil {
		return AgentInfo{}, err
	}
	return AgentInfo{Agent: value}, nil
}

// List 返回全部 Agent，按 CreatedAt / ID 稳定排序。
func (s *Store) List(ctx context.Context) ([]AgentInfo, error) {
	if ctx == nil {
		return nil, errors.New("context.Context 不能为空")
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("读取 Agent 列表被取消: %w", err)
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	entries, err := os.ReadDir(s.agentsRoot)
	if err != nil {
		return nil, fmt.Errorf("读取 Agent Root 失败: %w", err)
	}

	result := make([]AgentInfo, 0, len(entries))
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("读取 Agent 列表被取消: %w", err)
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("Agent 目录不能是符号链接: %s", entry.Name())
		}
		if !entry.IsDir() {
			continue
		}
		directory := filepath.Join(s.agentsRoot, entry.Name())
		if deleting, markerErr := deletionMarkerExists(directory); markerErr != nil {
			return nil, fmt.Errorf("检查 Agent %s 删除状态失败: %w", entry.Name(), markerErr)
		} else if deleting {
			continue
		}

		configPath := filepath.Join(directory, agentConfigFileName)
		if _, err := os.Lstat(configPath); errors.Is(err, os.ErrNotExist) {
			continue
		} else if err != nil {
			return nil, fmt.Errorf("检查 Agent %s Profile 失败: %w", entry.Name(), err)
		}

		value, err := s.readAgentLocked(ctx, entry.Name())
		if err != nil {
			return nil, fmt.Errorf("读取 Agent %s 失败: %w", entry.Name(), err)
		}
		result = append(result, AgentInfo{Agent: value})
	}

	sort.SliceStable(result, func(i, j int) bool {
		if !result[i].Agent.CreatedAt.Equal(result[j].Agent.CreatedAt) {
			return result[i].Agent.CreatedAt.Before(result[j].Agent.CreatedAt)
		}
		return result[i].Agent.ID < result[j].Agent.ID
	})
	return result, nil
}

// BeginDelete 把 Agent 从 active 原子推进到 deleting。
//
// 重复调用会返回既有检查点，因此删除流程可以安全重试。标记冻结 Workspace 所有权，
// 后续即使 Profile 已被部分清理，也不会误删 Custom Workspace。
func (s *Store) BeginDelete(ctx context.Context, id string) (DeletionState, error) {
	if err := validateStoreContext(ctx, "标记 Agent 删除状态"); err != nil {
		return DeletionState{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	id = strings.TrimSpace(id)
	_, directory, err := s.configPath(id)
	if err != nil {
		return DeletionState{}, err
	}
	markerPath := filepath.Join(directory, agentDeletionFileName)
	if _, err := os.Lstat(markerPath); err == nil {
		return s.readDeletionLocked(ctx, id, markerPath)
	} else if !errors.Is(err, os.ErrNotExist) {
		return DeletionState{}, fmt.Errorf("检查 Agent 删除标记失败: %w", err)
	}

	value, err := s.readAgentLocked(ctx, id)
	if err != nil {
		return DeletionState{}, err
	}
	mode := value.WorkspaceMode
	if mode == "" {
		mode = workspace.ModeManaged
	}
	state := DeletionState{
		AgentID:       value.ID,
		WorkspaceMode: mode,
		WorkspacePath: value.WorkspacePath,
		StartedAt:     time.Now().UTC(),
	}
	if err := atomicfile.WriteJSON(ctx, markerPath, 0o600, agentDeletionDocument{
		SchemaVersion: agentDeletionSchemaVersion,
		Deletion:      state,
	}); err != nil {
		return DeletionState{}, fmt.Errorf("写入 Agent 删除标记失败: %w", err)
	}
	return state, nil
}

// ListDeleting 返回全部未完成的 Agent 删除检查点。
func (s *Store) ListDeleting(ctx context.Context) ([]DeletionState, error) {
	if err := validateStoreContext(ctx, "读取 Agent 删除状态"); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	entries, err := os.ReadDir(s.agentsRoot)
	if err != nil {
		return nil, fmt.Errorf("读取 Agent Root 失败: %w", err)
	}
	result := make([]DeletionState, 0)
	var listErr error
	for _, entry := range entries {
		if !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		markerPath := filepath.Join(s.agentsRoot, entry.Name(), agentDeletionFileName)
		if _, err := os.Lstat(markerPath); errors.Is(err, os.ErrNotExist) {
			continue
		} else if err != nil {
			listErr = errors.Join(listErr, fmt.Errorf("检查 Agent %s 删除标记失败: %w", entry.Name(), err))
			continue
		}
		state, err := s.readDeletionLocked(ctx, entry.Name(), markerPath)
		if err != nil {
			listErr = errors.Join(listErr, err)
			continue
		}
		result = append(result, state)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].AgentID < result[j].AgentID })
	return result, listErr
}

// DeleteMarked 完成 deleting -> removed；未持有删除标记时拒绝物理删除。
func (s *Store) DeleteMarked(ctx context.Context, id string) error {
	if err := validateStoreContext(ctx, "删除 Agent 数据"); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	id = strings.TrimSpace(id)
	_, directory, err := s.configPath(id)
	if err != nil {
		return err
	}
	if err := validateRealDirectory(directory); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("验证 Agent 目录失败: %w", err)
	}
	markerPath := filepath.Join(directory, agentDeletionFileName)
	if _, err := s.readDeletionLocked(ctx, id, markerPath); err != nil {
		return fmt.Errorf("验证 Agent 删除标记失败: %w", err)
	}
	if err := os.RemoveAll(directory); err != nil {
		return fmt.Errorf("删除 Agent 数据目录失败: %w", err)
	}
	return nil
}

// CountAgentsByModel 返回当前 Agent Profile 中引用指定 Model（Chat 或任意 Model Role）的数量。
//
// 该方法由 models.Registry 在删除 Model 前调用。它只读取 Agent config.json，
// 不调用 ModelRegistry，因此不会形成 Agent <-> Model 的运行时递归依赖。扫描发生
// 在用户主动删除模型的低频控制面路径，不会影响 Agent Run 的并发性能。
func (s *Store) CountAgentsByModel(ctx context.Context, modelID string) (int, error) {
	modelID = strings.TrimSpace(modelID)
	if modelID == "" {
		return 0, errors.New("Model ID 不能为空")
	}

	values, err := s.List(ctx)
	if err != nil {
		return 0, fmt.Errorf("统计 Model 的 Agent 引用失败: %w", err)
	}

	count := 0
	for _, value := range values {
		agent := value.Agent
		if agent.ModelID == modelID ||
			agent.ModelRoles.UtilityModelID == modelID ||
			agent.ModelRoles.MemoryModelID == modelID ||
			agent.ModelRoles.VisionModelID == modelID {
			count++
		}
	}
	return count, nil
}

func (s *Store) readAgentLocked(ctx context.Context, id string) (Agent, error) {
	path, _, err := s.configPath(id)
	if err != nil {
		return Agent{}, err
	}

	var document agentDocument
	if err := atomicfile.ReadJSON(ctx, path, &document); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Agent{}, ErrNotFound
		}
		return Agent{}, fmt.Errorf("读取 Agent Profile 失败: %w", err)
	}

	if document.SchemaVersion != agentConfigSchemaVersion {
		return Agent{}, fmt.Errorf(
			"Agent %s config.json schema_version 不支持: %d",
			id,
			document.SchemaVersion,
		)
	}
	if document.Agent.ID != id {
		return Agent{}, fmt.Errorf(
			"Agent config.json ID 与目录不一致: file=%s content=%s",
			id,
			document.Agent.ID,
		)
	}

	return document.Agent, nil
}

func (s *Store) readDeletionLocked(ctx context.Context, id string, path string) (DeletionState, error) {
	var document agentDeletionDocument
	if err := atomicfile.ReadJSON(ctx, path, &document); err != nil {
		return DeletionState{}, fmt.Errorf("读取 Agent 删除标记失败: %w", err)
	}
	if document.SchemaVersion != agentDeletionSchemaVersion {
		return DeletionState{}, fmt.Errorf("Agent %s 删除标记 schema_version 不支持: %d", id, document.SchemaVersion)
	}
	if document.Deletion.AgentID != id {
		return DeletionState{}, fmt.Errorf("Agent 删除标记 ID 与目录不一致: directory=%s marker=%s", id, document.Deletion.AgentID)
	}
	if document.Deletion.WorkspaceMode != workspace.ModeManaged && document.Deletion.WorkspaceMode != workspace.ModeCustom {
		return DeletionState{}, fmt.Errorf("Agent %s 删除标记 WorkspaceMode 无效: %q", id, document.Deletion.WorkspaceMode)
	}
	if document.Deletion.StartedAt.IsZero() {
		return DeletionState{}, fmt.Errorf("Agent %s 删除标记 started_at 不能为空", id)
	}
	return document.Deletion, nil
}

func deletionMarkerExists(directory string) (bool, error) {
	info, err := os.Lstat(filepath.Join(directory, agentDeletionFileName))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return false, errors.New("Agent 删除标记不是安全普通文件")
	}
	return true, nil
}

func (s *Store) configPath(id string) (string, string, error) {
	id = strings.TrimSpace(id)
	if err := validateAgentID(id); err != nil {
		return "", "", err
	}

	directory := filepath.Clean(filepath.Join(s.agentsRoot, id))
	relative, err := filepath.Rel(s.agentsRoot, directory)
	if err != nil {
		return "", "", fmt.Errorf("校验 Agent 路径失败: %w", err)
	}
	if relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", "", errors.New("Agent 路径越界")
	}

	return filepath.Join(directory, agentConfigFileName), directory, nil
}

// ensureRealDirectory 创建或验证 Agent 内部目录。
//
// Agent Profile、Session、未来 memory/desk/skills 都属于 Humbert Internal Data，
// 因此 Agent 目录不允许被符号链接重定向。已存在目录只做验证，不会删除其中任何
// 用户数据。
func ensureRealDirectory(path string) error {
	info, err := os.Lstat(path)
	if err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return errors.New("Agent 内部目录不能是符号链接")
		}
		if !info.IsDir() {
			return errors.New("Agent 内部路径不是目录")
		}
		return nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return err
	}

	if err := os.Mkdir(path, 0o700); err != nil {
		if errors.Is(err, os.ErrExist) {
			return validateRealDirectory(path)
		}
		return err
	}
	return validateRealDirectory(path)
}

// validateRealDirectory 验证 Agent 内部路径是已存在的真实目录。
func validateRealDirectory(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return errors.New("Agent 内部目录不能是符号链接")
	}
	if !info.IsDir() {
		return errors.New("Agent 内部路径不是目录")
	}
	return nil
}

// validateStoreContext 在 Agent Store 执行文件系统操作前统一校验 Context。
//
// Agent Store 的底层 atomicfile/transcript 同样会检查 Context，但这里在获取互斥锁和
// 执行 Lstat/Remove 之前提前失败，避免已经取消的请求继续占用控制面锁或修改 Profile。
func validateStoreContext(ctx context.Context, operation string) error {
	if ctx == nil {
		return errors.New("context.Context 不能为空")
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("%s 被取消: %w", operation, err)
	}
	return nil
}

func validateAgentID(id string) error {
	if id == "" || id == "." || id == ".." {
		return errors.New("Agent ID 不能为空或使用特殊路径名称")
	}
	if strings.ContainsRune(id, 0) ||
		strings.Contains(id, "/") ||
		strings.Contains(id, `\`) ||
		filepath.IsAbs(id) ||
		filepath.VolumeName(id) != "" ||
		filepath.Clean(id) != id {
		return fmt.Errorf("非法 Agent ID: %q", id)
	}
	return nil
}
