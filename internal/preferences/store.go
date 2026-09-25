package preferences

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/sda1-hacker/humbert-agent/internal/atomicfile"
	"github.com/sda1-hacker/humbert-agent/internal/avatar"
)

const schemaVersion = 1

type UserProfile struct {
	Name     string `json:"name"`
	Avatar   string `json:"avatar,omitempty"`
	Language string `json:"language,omitempty"`
}

const DefaultLanguage = "zh-CN"

func SupportedLanguage(value string) bool {
	switch value {
	case "zh-CN", "en-US", "ja-JP", "ko-KR":
		return true
	default:
		return false
	}
}

type document struct {
	SchemaVersion int         `json:"schema_version"`
	User          UserProfile `json:"user"`
}

type Store struct {
	path string
	mu   sync.RWMutex
}

func NewStore(ctx context.Context, path string) (*Store, error) {
	if ctx == nil {
		return nil, errors.New("context.Context 不能为空")
	}
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, errors.New("Preferences 文件路径不能为空")
	}
	store := &Store{path: path}
	if _, err := store.Get(ctx); err != nil {
		return nil, fmt.Errorf("初始化 Preferences Store 失败: %w", err)
	}
	return store, nil
}

func (s *Store) Get(ctx context.Context) (UserProfile, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	value, err := s.readLocked(ctx)
	if err != nil {
		return UserProfile{}, err
	}
	return value.User, nil
}

func (s *Store) Update(ctx context.Context, profile UserProfile) (UserProfile, error) {
	name := strings.TrimSpace(profile.Name)
	if name == "" {
		return UserProfile{}, errors.New("用户名称不能为空")
	}
	if len([]rune(name)) > 100 {
		return UserProfile{}, errors.New("用户名称不能超过 100 个字符")
	}
	normalizedAvatar, err := avatar.NormalizeDataURL(profile.Avatar)
	if err != nil {
		return UserProfile{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	previous, err := s.readLocked(ctx)
	if err != nil {
		return UserProfile{}, err
	}
	// 旧版调用只提交名称和头像时，语言设置不能被清空。
	if profile.Language == "" {
		profile.Language = previous.User.Language
	}
	if !SupportedLanguage(profile.Language) {
		return UserProfile{}, errors.New("不支持的界面语言")
	}
	profile = UserProfile{Name: name, Avatar: normalizedAvatar, Language: profile.Language}
	if err := atomicfile.WriteJSON(ctx, s.path, 0o600, document{SchemaVersion: schemaVersion, User: profile}); err != nil {
		return UserProfile{}, fmt.Errorf("保存用户资料失败: %w", err)
	}
	return profile, nil
}

func (s *Store) SetLanguage(ctx context.Context, language string) (UserProfile, error) {
	if !SupportedLanguage(language) {
		return UserProfile{}, errors.New("不支持的界面语言")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	value, err := s.readLocked(ctx)
	if err != nil {
		return UserProfile{}, err
	}
	value.User.Language = language
	if err := atomicfile.WriteJSON(ctx, s.path, 0o600, value); err != nil {
		return UserProfile{}, fmt.Errorf("保存界面语言失败: %w", err)
	}
	return value.User, nil
}

func (s *Store) readLocked(ctx context.Context) (document, error) {
	var value document
	if err := atomicfile.ReadJSON(ctx, s.path, &value); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			value = document{SchemaVersion: schemaVersion, User: UserProfile{Name: "你", Language: DefaultLanguage}}
			if writeErr := atomicfile.WriteJSON(ctx, s.path, 0o600, value); writeErr != nil {
				return document{}, writeErr
			}
			return value, nil
		}
		return document{}, fmt.Errorf("读取用户资料失败: %w", err)
	}
	if value.SchemaVersion != schemaVersion {
		return document{}, fmt.Errorf("不支持的 preferences.json schema_version: %d", value.SchemaVersion)
	}
	if strings.TrimSpace(value.User.Name) == "" {
		value.User.Name = "你"
	}
	if value.User.Language == "" {
		value.User.Language = DefaultLanguage
	}
	if !SupportedLanguage(value.User.Language) {
		return document{}, fmt.Errorf("不支持的界面语言: %s", value.User.Language)
	}
	normalizedAvatar, err := avatar.NormalizeDataURL(value.User.Avatar)
	if err != nil {
		return document{}, fmt.Errorf("用户头像无效: %w", err)
	}
	value.User.Avatar = normalizedAvatar
	return value, nil
}
