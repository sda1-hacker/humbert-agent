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

	"github.com/sda1-hacker/humbert-agent/internal/atomicfile"
	"github.com/sda1-hacker/humbert-agent/internal/transcript"
)

const (
	agentConfigFileName      = "config.json"
	agentConfigSchemaVersion = 1
)

type agentDocument struct {
	SchemaVersion int `json:"schema_version"`

	Agent Agent `json:"agent"`
}

// Store 负责 Agent Profile 的文件持久化。
//
// 文件布局：
//
//	~/.humbert-agent/agents/<agent-id>/config.json
//	~/.humbert-agent/agents/<agent-id>/sessions/<session-id>/{config.json,session.jsonl}
//
// Store 只管理 Agent Profile 文件，不校验 Model、不创建 Workspace；这些仍属于
// Agent Service。Session 数量由 TranscriptStore 统计，因此 Agent 删除保护不再
// 依赖数据库外键。
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

		configPath := filepath.Join(s.agentsRoot, entry.Name(), agentConfigFileName)
		if _, err := os.Lstat(configPath); errors.Is(err, os.ErrNotExist) {
			// 允许未来 memory/desk 等内部目录在 Profile 删除后暂时保留；没有
			// config.json 的目录不再代表一个有效 Agent。
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

// Delete 删除指定 Agent Profile。
//
// Workspace 和 Agent 目录都不会在这里递归删除。Service 会先确认 Session 数量为 0；
// Store 只删除 config.json，使未来 memory/desk/skills 等 Agent 数据即使已经存在也不会
// 因为删除 Profile 而被误删。没有 config.json 的目录不再被 List 识别为有效 Agent。
func (s *Store) Delete(ctx context.Context, id string) error {
	if err := validateStoreContext(ctx, "删除 Agent Profile"); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	path, directory, err := s.configPath(id)
	if err != nil {
		return err
	}
	if err := validateRealDirectory(directory); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ErrNotFound
		}
		return fmt.Errorf("验证 Agent 目录失败: %w", err)
	}

	if err := os.Remove(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ErrNotFound
		}
		return fmt.Errorf("删除 Agent Profile 失败: %w", err)
	}

	return nil
}

// CountSessions 返回指定 Agent 当前拥有的 Session 数量。
func (s *Store) CountSessions(ctx context.Context, agentID string) (int, error) {
	values, err := s.transcripts.ListSessionRefs(ctx, agentID)
	if err != nil {
		return 0, fmt.Errorf("统计 Agent Session 数量失败: %w", err)
	}
	return len(values), nil
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
