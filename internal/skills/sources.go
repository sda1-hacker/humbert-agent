package skills

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sda1-hacker/humbert-agent/internal/atomicfile"
)

const (
	// skillSourcesFileName 保存 Humbert 自己维护的安装来源元数据。
	//
	// 文件位于 Skills Root，而不写入第三方 Skill Package，因此来源记录不会改变 Package
	// Identity，也不会被 Skill Update 覆盖。权限使用 0600，因为 Direct ZIP URL 可能包含
	// 短期签名 query；Desktop UI 只会拿到去除 userinfo/query/fragment 后的展示地址。
	skillSourcesFileName = ".humbert-skill-sources.json"

	skillSourcesSchemaVersion = 1

	SkillSourceKindLocal  = "local"
	SkillSourceKindRemote = "remote"
)

// SourceInfo 是一个已安装 Skill 的 Humbert 来源记录。
//
// Identity 表示最后一次由 Humbert 从该来源成功安装/更新时的 Package Identity。当前磁盘
// Package 如果与它不同，控制面可以提示“本地内容已变化”，但 Runtime 永远以当前磁盘包扫描
// 结果为准。来源元数据不参与 Agent enabled_skills，也不进入 Runtime Snapshot。
type SourceInfo struct {
	Kind string `json:"kind"`

	Provider string `json:"provider,omitempty"`

	OriginalURL string `json:"original_url,omitempty"`

	LocalDirectory string `json:"local_directory,omitempty"`

	SkillPath string `json:"skill_path,omitempty"`

	SkillName string `json:"skill_name,omitempty"`

	Metadata map[string]string `json:"metadata,omitempty"`

	InstalledAt time.Time `json:"installed_at"`

	UpdatedAt time.Time `json:"updated_at"`

	Identity string `json:"identity"`
}

// Clone 返回不共享 Metadata map 的副本。
func (s SourceInfo) Clone() SourceInfo {
	cloned := s
	if len(s.Metadata) > 0 {
		cloned.Metadata = make(map[string]string, len(s.Metadata))
		for key, value := range s.Metadata {
			cloned.Metadata[key] = value
		}
	}
	return cloned
}

type skillSourcesDocument struct {
	SchemaVersion int `json:"schema_version"`

	Skills map[string]SourceInfo `json:"skills"`
}

// Source 返回一个 Skill 的安装来源。
//
// bool=false 表示该 Skill 是旧版本 Humbert 安装、用户手工复制，或来源记录已被明确清除。
// 这种情况下 Skill 仍然可以正常使用，只是无法自动 Update/Reinstall，用户可以在详情页重新
// 选择 URL/本地目录建立来源。
func (m *Manager) Source(ctx context.Context, name string) (SourceInfo, bool, error) {
	if err := m.validate(); err != nil {
		return SourceInfo{}, false, err
	}
	if ctx == nil {
		return SourceInfo{}, false, errors.New("context.Context 不能为空")
	}
	if err := ctx.Err(); err != nil {
		return SourceInfo{}, false, fmt.Errorf("读取 Skill 来源被取消: %w", err)
	}
	name, err := normalizeSkillName(name)
	if err != nil {
		return SourceInfo{}, false, err
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	document, err := m.readSourcesLocked(ctx)
	if err != nil {
		return SourceInfo{}, false, err
	}
	value, exists := document.Skills[name]
	if !exists {
		return SourceInfo{}, false, nil
	}
	return value.Clone(), true, nil
}

// Sources 返回当前全部 Skill 来源记录的安全副本。
//
// 这个批量接口主要给控制面 Catalog 使用，避免为列表中的每个 Skill 重复读取同一份来源文档。
// 返回 map 的 key 仍然是 canonical Skill name；调用方修改返回值不会影响 Manager 内部状态。
func (m *Manager) Sources(ctx context.Context) (map[string]SourceInfo, error) {
	if err := m.validate(); err != nil {
		return nil, err
	}
	if ctx == nil {
		return nil, errors.New("context.Context 不能为空")
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("读取 Skill 来源列表被取消: %w", err)
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	document, err := m.readSourcesLocked(ctx)
	if err != nil {
		return nil, err
	}
	result := make(map[string]SourceInfo, len(document.Skills))
	for name, source := range document.Skills {
		result[name] = source.Clone()
	}
	return result, nil
}

func (m *Manager) sourcesPath() string {
	return filepath.Join(m.rootDir, skillSourcesFileName)
}

func (m *Manager) readSourcesLocked(ctx context.Context) (skillSourcesDocument, error) {
	document := skillSourcesDocument{
		SchemaVersion: skillSourcesSchemaVersion,
		Skills:        make(map[string]SourceInfo),
	}
	if err := atomicfile.ReadJSON(ctx, m.sourcesPath(), &document); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return document, nil
		}
		return skillSourcesDocument{}, fmt.Errorf("读取 Skill 来源配置失败: %w", err)
	}
	if document.SchemaVersion != skillSourcesSchemaVersion {
		return skillSourcesDocument{}, fmt.Errorf(
			"读取 Skill 来源配置失败: 不支持 schema_version=%d",
			document.SchemaVersion,
		)
	}
	if document.Skills == nil {
		document.Skills = make(map[string]SourceInfo)
	}

	for name, source := range document.Skills {
		normalizedName, err := normalizeSkillName(name)
		if err != nil || normalizedName != name {
			return skillSourcesDocument{}, fmt.Errorf("读取 Skill 来源配置失败: 非法 Skill 名称 %q", name)
		}
		normalized, err := normalizeStoredSourceInfo(source)
		if err != nil {
			return skillSourcesDocument{}, fmt.Errorf("读取 Skill %q 来源失败: %w", name, err)
		}
		document.Skills[name] = normalized
	}
	return document, nil
}

func (m *Manager) writeSourcesLocked(ctx context.Context, document skillSourcesDocument) error {
	document.SchemaVersion = skillSourcesSchemaVersion
	if document.Skills == nil {
		document.Skills = make(map[string]SourceInfo)
	}
	if err := atomicfile.WriteJSON(ctx, m.sourcesPath(), 0o600, document); err != nil {
		return fmt.Errorf("保存 Skill 来源配置失败: %w", err)
	}
	return nil
}

func normalizeStoredSourceInfo(source SourceInfo) (SourceInfo, error) {
	source.Kind = strings.ToLower(strings.TrimSpace(source.Kind))
	source.Provider = strings.TrimSpace(source.Provider)
	source.OriginalURL = strings.TrimSpace(source.OriginalURL)
	source.LocalDirectory = strings.TrimSpace(source.LocalDirectory)
	source.Identity = strings.TrimSpace(source.Identity)
	if source.Identity == "" {
		return SourceInfo{}, errors.New("来源记录缺少 Package Identity")
	}

	var err error
	source.SkillPath, err = normalizeRemoteSkillPath(source.SkillPath)
	if err != nil {
		return SourceInfo{}, fmt.Errorf("skill_path 无效: %w", err)
	}
	if strings.TrimSpace(source.SkillName) != "" {
		source.SkillName, err = normalizeSkillName(source.SkillName)
		if err != nil {
			return SourceInfo{}, fmt.Errorf("skill_name 无效: %w", err)
		}
	}

	switch source.Kind {
	case SkillSourceKindLocal:
		if source.LocalDirectory == "" {
			return SourceInfo{}, errors.New("本地来源缺少 local_directory")
		}
		absolute, err := filepath.Abs(source.LocalDirectory)
		if err != nil {
			return SourceInfo{}, fmt.Errorf("解析本地来源路径失败: %w", err)
		}
		source.LocalDirectory = filepath.Clean(absolute)
		source.OriginalURL = ""
		if source.Provider == "" {
			source.Provider = "local"
		}
	case SkillSourceKindRemote:
		if source.OriginalURL == "" {
			return SourceInfo{}, errors.New("远程来源缺少 original_url")
		}
		if _, err := parsePublicSkillURL(source.OriginalURL); err != nil {
			return SourceInfo{}, fmt.Errorf("远程 original_url 无效: %w", err)
		}
		source.LocalDirectory = ""
		if source.Provider == "" {
			source.Provider = "remote"
		}
	default:
		return SourceInfo{}, fmt.Errorf("未知来源 kind=%q", source.Kind)
	}

	if source.InstalledAt.IsZero() {
		source.InstalledAt = source.UpdatedAt
	}
	if source.UpdatedAt.IsZero() {
		source.UpdatedAt = source.InstalledAt
	}
	if source.InstalledAt.IsZero() || source.UpdatedAt.IsZero() {
		return SourceInfo{}, errors.New("来源记录缺少 installed_at/updated_at")
	}

	if len(source.Metadata) > 0 {
		cleaned := make(map[string]string, len(source.Metadata))
		for rawKey, rawValue := range source.Metadata {
			key := strings.TrimSpace(rawKey)
			value := strings.TrimSpace(rawValue)
			if key == "" || value == "" {
				continue
			}
			if len(key) > 128 || len(value) > 2048 {
				return SourceInfo{}, errors.New("来源 metadata 超过允许长度")
			}
			cleaned[key] = value
		}
		source.Metadata = cleaned
	}
	return source, nil
}

func localSourceInfo(sourceDirectory string, identity string, now time.Time) (SourceInfo, error) {
	absolute, err := filepath.Abs(strings.TrimSpace(sourceDirectory))
	if err != nil {
		return SourceInfo{}, fmt.Errorf("解析 Skill 本地来源路径失败: %w", err)
	}
	return normalizeStoredSourceInfo(SourceInfo{
		Kind:           SkillSourceKindLocal,
		Provider:       "local",
		LocalDirectory: filepath.Clean(absolute),
		InstalledAt:    now.UTC(),
		UpdatedAt:      now.UTC(),
		Identity:       identity,
	})
}

func remoteSourceInfo(source RemoteSkillSource, identity string, now time.Time) (SourceInfo, error) {
	metadata := make(map[string]string, len(source.Metadata))
	for key, value := range source.Metadata {
		metadata[key] = value
	}
	return normalizeStoredSourceInfo(SourceInfo{
		Kind:        SkillSourceKindRemote,
		Provider:    source.Provider,
		OriginalURL: source.OriginalURL,
		SkillPath:   source.SkillPath,
		SkillName:   source.SkillName,
		Metadata:    metadata,
		InstalledAt: now.UTC(),
		UpdatedAt:   now.UTC(),
		Identity:    identity,
	})
}

// removeSourceLocked 在 Package 已删除后清理来源记录。调用方必须持有 m.mu 写锁。
func (m *Manager) removeSourceLocked(ctx context.Context, name string) error {
	document, err := m.readSourcesLocked(ctx)
	if err != nil {
		return err
	}
	if _, exists := document.Skills[name]; !exists {
		return nil
	}
	delete(document.Skills, name)
	return m.writeSourcesLocked(ctx, document)
}
