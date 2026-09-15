package projects

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
)

const projectSchemaVersion = 1

type projectDocument struct {
	SchemaVersion int     `json:"schema_version"`
	Project       Project `json:"project"`
}

type Store struct {
	root string
	mu   sync.RWMutex
}

func NewStore(ctx context.Context, root string) (*Store, error) {
	if ctx == nil {
		return nil, errors.New("context.Context 不能为空")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	root = strings.TrimSpace(root)
	if root == "" {
		return nil, errors.New("Project Root 不能为空")
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("解析 Project Root 失败: %w", err)
	}
	if err := os.MkdirAll(absolute, 0o700); err != nil {
		return nil, fmt.Errorf("创建 Project Root 失败: %w", err)
	}
	info, err := os.Lstat(absolute)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return nil, fmt.Errorf("Project Root 必须是真实目录: %s", absolute)
	}
	return &Store{root: filepath.Clean(absolute)}, nil
}

func (s *Store) Create(ctx context.Context, value Project) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	path, err := s.path(value.ID)
	if err != nil {
		return err
	}
	if _, err := os.Lstat(path); err == nil {
		return ErrAlreadyExists
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return atomicfile.WriteJSON(ctx, path, 0o600, projectDocument{SchemaVersion: projectSchemaVersion, Project: value})
}

func (s *Store) Update(ctx context.Context, value Project) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	path, err := s.path(value.ID)
	if err != nil {
		return err
	}
	if _, err := os.Lstat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ErrNotFound
		}
		return err
	}
	return atomicfile.WriteJSON(ctx, path, 0o600, projectDocument{SchemaVersion: projectSchemaVersion, Project: value})
}

func (s *Store) Get(ctx context.Context, id string) (Project, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	path, err := s.path(id)
	if err != nil {
		return Project{}, err
	}
	var doc projectDocument
	if err := atomicfile.ReadJSON(ctx, path, &doc); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Project{}, ErrNotFound
		}
		return Project{}, fmt.Errorf("读取 Project 失败: %w", err)
	}
	if doc.SchemaVersion != projectSchemaVersion {
		return Project{}, fmt.Errorf("Project schema_version 不支持: %d", doc.SchemaVersion)
	}
	if doc.Project.ID != strings.TrimSpace(id) {
		return Project{}, fmt.Errorf("Project ID 与文件名不一致")
	}
	return doc.Project, nil
}

func (s *Store) List(ctx context.Context) ([]Project, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	entries, err := os.ReadDir(s.root)
	if err != nil {
		return nil, err
	}
	result := make([]Project, 0, len(entries))
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), ".json")
		path, err := s.path(id)
		if err != nil {
			return nil, err
		}
		var doc projectDocument
		if err := atomicfile.ReadJSON(ctx, path, &doc); err != nil {
			return nil, fmt.Errorf("读取 Project %s 失败: %w", id, err)
		}
		if doc.SchemaVersion != projectSchemaVersion {
			return nil, fmt.Errorf("Project %s schema_version 不支持: %d", id, doc.SchemaVersion)
		}
		result = append(result, doc.Project)
	}
	sort.SliceStable(result, func(i, j int) bool {
		if !result[i].CreatedAt.Equal(result[j].CreatedAt) {
			return result[i].CreatedAt.Before(result[j].CreatedAt)
		}
		return result[i].ID < result[j].ID
	})
	return result, nil
}

func (s *Store) Delete(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	path, err := s.path(id)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ErrNotFound
		}
		return err
	}
	return nil
}

func (s *Store) path(id string) (string, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", errors.New("Project ID 不能为空")
	}
	if strings.ContainsAny(id, `/\\`) || id == "." || id == ".." {
		return "", errors.New("Project ID 非法")
	}
	path := filepath.Join(s.root, id+".json")
	if filepath.Dir(path) != s.root {
		return "", errors.New("Project Path 越界")
	}
	return path, nil
}
