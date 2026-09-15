package mcp

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"

	"github.com/sda1-hacker/humbert-agent/internal/atomicfile"
)

const serversSchemaVersion = 1

type serversDocument struct {
	SchemaVersion int      `json:"schema_version"`
	Servers       []Server `json:"servers"`
}

// Store 持久化 ~/.humbert-agent/mcp/servers.json。
//
// Server 配置属于低频控制面状态，使用完整 JSON + 原子替换；任何 Secret 都禁止直接进入
// 本文件，MCP-03 会只保存 credential.Store 引用。
type Store struct {
	path string
	mu   sync.RWMutex
}

func NewStore(ctx context.Context, path string) (*Store, error) {
	if ctx == nil {
		return nil, errors.New("初始化 MCP Store 失败: context.Context 不能为空")
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("初始化 MCP Store 被取消: %w", err)
	}
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, errors.New("MCP servers.json 路径不能为空")
	}

	store := &Store{path: path}
	if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
		if err := atomicfile.WriteJSON(ctx, path, 0o600, serversDocument{
			SchemaVersion: serversSchemaVersion,
			Servers:       []Server{},
		}); err != nil {
			return nil, fmt.Errorf("初始化 MCP servers.json 失败: %w", err)
		}
	} else if err != nil {
		return nil, fmt.Errorf("检查 MCP servers.json 失败: %w", err)
	}

	if _, err := store.List(ctx); err != nil {
		return nil, fmt.Errorf("校验 MCP servers.json 失败: %w", err)
	}
	return store, nil
}

func (s *Store) List(ctx context.Context) ([]Server, error) {
	if err := validateContext(ctx, "读取 MCP Server"); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	values, err := s.listLocked(ctx)
	if err != nil {
		return nil, err
	}
	sort.Slice(values, func(i, j int) bool {
		if values[i].Name != values[j].Name {
			return values[i].Name < values[j].Name
		}
		return values[i].ID < values[j].ID
	})
	return values, nil
}

func (s *Store) Get(ctx context.Context, id string) (Server, error) {
	if err := validateContext(ctx, "读取 MCP Server"); err != nil {
		return Server{}, err
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return Server{}, errors.New("MCP Server ID 不能为空")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	values, err := s.listLocked(ctx)
	if err != nil {
		return Server{}, err
	}
	for _, value := range values {
		if value.ID == id {
			return cloneServer(value), nil
		}
	}
	return Server{}, fmt.Errorf("%w: %s", ErrServerNotFound, id)
}

func (s *Store) Create(ctx context.Context, value Server) error {
	if err := validateContext(ctx, "创建 MCP Server"); err != nil {
		return err
	}
	if err := validateServer(value); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	values, err := s.listLocked(ctx)
	if err != nil {
		return err
	}
	for _, existing := range values {
		if existing.ID == value.ID || existing.Key == value.Key {
			return fmt.Errorf("%w: id=%s key=%s", ErrServerExists, value.ID, value.Key)
		}
	}
	values = append(values, cloneServer(value))
	return s.writeLocked(ctx, values)
}

func (s *Store) Update(ctx context.Context, value Server) error {
	if err := validateContext(ctx, "更新 MCP Server"); err != nil {
		return err
	}
	if err := validateServer(value); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	values, err := s.listLocked(ctx)
	if err != nil {
		return err
	}
	found := false
	for index := range values {
		if values[index].ID != value.ID {
			if values[index].Key == value.Key {
				return fmt.Errorf("%w: key=%s", ErrServerExists, value.Key)
			}
			continue
		}
		values[index] = cloneServer(value)
		found = true
	}
	if !found {
		return fmt.Errorf("%w: %s", ErrServerNotFound, value.ID)
	}
	return s.writeLocked(ctx, values)
}

func (s *Store) Delete(ctx context.Context, id string) error {
	if err := validateContext(ctx, "删除 MCP Server"); err != nil {
		return err
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return errors.New("MCP Server ID 不能为空")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	values, err := s.listLocked(ctx)
	if err != nil {
		return err
	}
	filtered := make([]Server, 0, len(values))
	found := false
	for _, value := range values {
		if value.ID == id {
			found = true
			continue
		}
		filtered = append(filtered, value)
	}
	if !found {
		return fmt.Errorf("%w: %s", ErrServerNotFound, id)
	}
	return s.writeLocked(ctx, filtered)
}

func (s *Store) listLocked(ctx context.Context) ([]Server, error) {
	var document serversDocument
	if err := atomicfile.ReadJSON(ctx, s.path, &document); err != nil {
		return nil, fmt.Errorf("读取 MCP servers.json 失败: %w", err)
	}
	if document.SchemaVersion != serversSchemaVersion {
		return nil, fmt.Errorf("不支持的 MCP servers.json schema_version: %d", document.SchemaVersion)
	}
	ids := make(map[string]struct{}, len(document.Servers))
	keys := make(map[string]struct{}, len(document.Servers))
	result := make([]Server, 0, len(document.Servers))
	for _, value := range document.Servers {
		if err := validateServer(value); err != nil {
			return nil, fmt.Errorf("MCP Server %q 配置无效: %w", value.ID, err)
		}
		if _, exists := ids[value.ID]; exists {
			return nil, fmt.Errorf("MCP servers.json 包含重复 ID: %s", value.ID)
		}
		if _, exists := keys[value.Key]; exists {
			return nil, fmt.Errorf("MCP servers.json 包含重复 Key: %s", value.Key)
		}
		ids[value.ID] = struct{}{}
		keys[value.Key] = struct{}{}
		result = append(result, cloneServer(value))
	}
	return result, nil
}

func (s *Store) writeLocked(ctx context.Context, values []Server) error {
	document := serversDocument{
		SchemaVersion: serversSchemaVersion,
		Servers:       values,
	}
	if err := atomicfile.WriteJSON(ctx, s.path, 0o600, document); err != nil {
		return fmt.Errorf("写入 MCP servers.json 失败: %w", err)
	}
	return nil
}

func validateContext(ctx context.Context, operation string) error {
	if ctx == nil {
		return errors.New(operation + "失败: context.Context 不能为空")
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("%s 被取消: %w", operation, err)
	}
	return nil
}
