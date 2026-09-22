package credential

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/sda1-hacker/humbert-agent/internal/atomicfile"
	keyring "github.com/zalando/go-keyring"
)

const keyringService = "com.sda1hacker.humbertagent.credentials"
const indexName = "keyring-index.json"

type systemKeyring interface {
	Set(service, user, value string) error
	Get(service, user string) (string, error)
	Delete(service, user string) error
}

type osKeyring struct{}

func (osKeyring) Set(service, user, value string) error    { return keyring.Set(service, user, value) }
func (osKeyring) Get(service, user string) (string, error) { return keyring.Get(service, user) }
func (osKeyring) Delete(service, user string) error        { return keyring.Delete(service, user) }

// NewSystem 供桌面应用使用。旧 .secret 文件在初始化时迁移到系统凭据库。
// 系统凭据库不可用时直接报错，不回退到明文写入。
func NewSystem(root string) (*Store, error) { return newSystemWithBackend(root, osKeyring{}) }

func newSystemWithBackend(root string, backend systemKeyring) (*Store, error) {
	store, err := New(root)
	if err != nil {
		return nil, err
	}
	store.system = backend
	if err := store.MigrateLegacy(context.Background()); err != nil {
		return nil, err
	}
	return store, nil
}

func (s *Store) MigrateLegacy(ctx context.Context) error {
	entries, err := os.ReadDir(s.root)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".secret") {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), ".secret")
		if _, err := s.Get(ctx, id); err != nil {
			return fmt.Errorf("迁移旧凭据 %s 失败: %w", id, err)
		}
	}
	return nil
}

func (s *Store) putSystem(ctx context.Context, id, value string) error {
	path, err := s.pathFor(id)
	if err != nil {
		return err
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return errors.New("凭据内容不能为空")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.readIndex(ctx); err != nil {
		return err
	}
	previous, getErr := s.system.Get(keyringService, id)
	if getErr != nil && !errors.Is(getErr, keyring.ErrNotFound) {
		return fmt.Errorf("读取系统凭据库失败: %w", getErr)
	}
	if err := s.system.Set(keyringService, id, value); err != nil {
		return fmt.Errorf("写入系统凭据库失败: %w", err)
	}
	if err := s.addIndex(ctx, id); err != nil {
		var rollbackErr error
		if errors.Is(getErr, keyring.ErrNotFound) {
			rollbackErr = s.system.Delete(keyringService, id)
		} else {
			rollbackErr = s.system.Set(keyringService, id, previous)
		}
		return errors.Join(err, rollbackErr)
	}
	if err := removeLegacy(path); err != nil {
		return err
	}
	return nil
}

func (s *Store) getSystem(ctx context.Context, id string) (string, error) {
	path, err := s.pathFor(id)
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	value, err := s.system.Get(keyringService, id)
	if err == nil {
		if err := s.addIndex(ctx, id); err != nil {
			return "", err
		}
		if err := removeLegacy(path); err != nil {
			return "", err
		}
		return value, nil
	}
	if !errors.Is(err, keyring.ErrNotFound) {
		return "", fmt.Errorf("读取系统凭据库失败: %w", err)
	}
	if err := validateCredentialTarget(path, false); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("%w: %s", ErrNotFound, id)
		}
		return "", err
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	value = strings.TrimSpace(string(content))
	if value == "" {
		return "", errors.New("旧凭据文件为空")
	}
	if err := s.system.Set(keyringService, id, value); err != nil {
		return "", fmt.Errorf("迁移凭据到系统凭据库失败: %w", err)
	}
	if err := s.addIndex(ctx, id); err != nil {
		return "", err
	}
	if err := os.Remove(path); err != nil {
		return "", err
	}
	return value, nil
}

func (s *Store) deleteSystem(ctx context.Context, id string) error {
	path, err := s.pathFor(id)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.system.Delete(keyringService, id); err != nil && !errors.Is(err, keyring.ErrNotFound) {
		return err
	}
	if err := removeLegacy(path); err != nil {
		return err
	}
	ids, err := s.readIndex(ctx)
	if err != nil {
		return err
	}
	remaining := make([]string, 0, len(ids))
	for _, current := range ids {
		if current != id {
			remaining = append(remaining, current)
		}
	}
	return atomicfile.WriteJSON(ctx, filepath.Join(s.root, indexName), 0o600, remaining)
}

func removeLegacy(path string) error {
	if err := validateCredentialTarget(path, true); err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func (s *Store) readIndex(ctx context.Context) ([]string, error) {
	var ids []string
	if err := atomicfile.ReadJSON(ctx, filepath.Join(s.root, indexName), &ids); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []string{}, nil
		}
		return nil, err
	}
	for _, id := range ids {
		if !credentialIDPattern.MatchString(id) {
			return nil, errors.New("凭据索引包含非法 ID")
		}
	}
	return ids, nil
}

func (s *Store) addIndex(ctx context.Context, id string) error {
	ids, err := s.readIndex(ctx)
	if err != nil {
		return err
	}
	for _, current := range ids {
		if current == id {
			return nil
		}
	}
	ids = append(ids, id)
	sort.Strings(ids)
	return atomicfile.WriteJSON(ctx, filepath.Join(s.root, indexName), 0o600, ids)
}

// ExportAll 迁移并导出所有已登记的凭据；仅供加密备份使用。
func (s *Store) ExportAll(ctx context.Context) (map[string]string, error) {
	if s.system == nil {
		return nil, errors.New("未启用系统凭据库")
	}
	entries, err := os.ReadDir(s.root)
	if err != nil {
		return nil, err
	}
	ids, err := s.readIndex(ctx)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		seen[id] = true
	}
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".secret") {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), ".secret")
		if !credentialIDPattern.MatchString(id) {
			return nil, errors.New("旧凭据文件名无效")
		}
		seen[id] = true
	}
	values := make(map[string]string, len(seen))
	for id := range seen {
		value, err := s.Get(ctx, id)
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		values[id] = value
	}
	return values, nil
}

// ImportAll 在用户明确恢复备份后将凭据重新写入系统凭据库。
func (s *Store) ImportAll(ctx context.Context, values map[string]string) error {
	if s.system == nil {
		return errors.New("未启用系统凭据库")
	}
	if _, err := s.readIndex(ctx); err != nil {
		return err
	}
	previous := make(map[string]*string, len(values))
	for id, value := range values {
		if _, err := s.pathFor(id); err != nil {
			return err
		}
		if strings.TrimSpace(value) == "" {
			return errors.New("备份包含空凭据")
		}
		old, err := s.Get(ctx, id)
		if errors.Is(err, ErrNotFound) {
			previous[id] = nil
			continue
		}
		if err != nil {
			return err
		}
		previous[id] = &old
	}
	ids := make([]string, 0, len(values))
	for id := range values {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	written := make([]string, 0, len(values))
	for _, id := range ids {
		value := values[id]
		// Set may succeed even if the index update fails, so include this ID in rollback.
		written = append(written, id)
		if err := s.Put(ctx, id, value); err != nil {
			var rollback error
			for index := len(written) - 1; index >= 0; index-- {
				done := written[index]
				if previous[done] == nil {
					rollback = errors.Join(rollback, s.Delete(ctx, done))
				} else {
					rollback = errors.Join(rollback, s.Put(ctx, done, *previous[done]))
				}
			}
			return errors.Join(err, rollback)
		}
	}
	return nil
}
