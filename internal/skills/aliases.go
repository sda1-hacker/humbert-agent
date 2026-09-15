package skills

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/sda1-hacker/humbert-agent/internal/atomicfile"
)

const (
	// skillOverridesFileName 保存只属于当前 Humbert 用户的 Skill 展示覆盖。
	//
	// 它位于 Skills Root，而不写入第三方 Skill Package，因此修改 Alias 不会改变
	// Package Identity、Runtime Snapshot Revision，也不会污染后续 Upgrade 的源内容。
	skillOverridesFileName = ".humbert-skill-overrides.json"

	skillOverridesSchemaVersion = 1

	// maxSkillAliasRunes 限制本地展示别名长度。Alias 只用于 UI，不参与 Skill 身份。
	maxSkillAliasRunes = 80
)

type skillOverride struct {
	Alias string `json:"alias,omitempty"`
}

type skillOverridesDocument struct {
	SchemaVersion int `json:"schema_version"`

	Skills map[string]skillOverride `json:"skills"`
}

// Aliases 返回当前用户为 Skill 设置的本地展示别名副本。
//
// Alias 只属于控制面 UI：Runtime、Agent enabled_skills、Skill Tool、安装/升级与 Package
// Identity 始终使用 canonical name。调用方不能依赖 Alias 做任何身份判断。
func (m *Manager) Aliases(ctx context.Context) (map[string]string, error) {
	if err := m.validate(); err != nil {
		return nil, err
	}
	if ctx == nil {
		return nil, errors.New("context.Context 不能为空")
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("读取 Skill Alias 被取消: %w", err)
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	document, err := m.readOverridesLocked(ctx)
	if err != nil {
		return nil, err
	}
	result := make(map[string]string, len(document.Skills))
	for name, override := range document.Skills {
		alias := strings.TrimSpace(override.Alias)
		if alias == "" {
			continue
		}
		result[name] = alias
	}
	return result, nil
}

// SetAlias 设置或清除一个已安装 Skill 的本地展示别名。
//
// alias 为空字符串表示清除覆盖。该操作不会修改 SKILL.md，也不会改变 Package Identity。
// 只有已经安装且当前有效的 canonical Skill name 才能新增/修改 Alias；这样 Alias Store
// 不会被任意客户端当成无界键值存储。已经存在的 Alias 即使 Package 后来损坏，State 仍会
// 读取并展示，便于用户识别与修复。
func (m *Manager) SetAlias(ctx context.Context, name string, alias string) error {
	if err := m.validate(); err != nil {
		return err
	}
	if ctx == nil {
		return errors.New("context.Context 不能为空")
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("保存 Skill Alias 被取消: %w", err)
	}

	name, err := normalizeSkillName(name)
	if err != nil {
		return err
	}
	alias, err = normalizeSkillAlias(alias)
	if err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// 清除 Alias 仍要求 canonical name 合法，但不要求 Package 当前有效。这样当包被用户
	// 手工破坏后，设置页仍可以删除遗留的本地展示覆盖。
	if alias != "" {
		if _, err := m.getLocked(ctx, name); err != nil {
			return fmt.Errorf("设置 Skill %q Alias 失败: %w", name, err)
		}
	}

	document, err := m.readOverridesLocked(ctx)
	if err != nil {
		return err
	}
	if document.Skills == nil {
		document.Skills = make(map[string]skillOverride)
	}

	if alias == "" {
		delete(document.Skills, name)
	} else {
		document.Skills[name] = skillOverride{Alias: alias}
	}

	if err := m.writeOverridesLocked(ctx, document); err != nil {
		return err
	}

	m.logger.Info(
		ctx,
		"Skill 展示 Alias 已更新",
		"operation", "skill.alias.update",
		"skill_name", name,
		"alias_set", alias != "",
	)
	return nil
}

func (m *Manager) overridesPath() string {
	return filepath.Join(m.rootDir, skillOverridesFileName)
}

func (m *Manager) readOverridesLocked(ctx context.Context) (skillOverridesDocument, error) {
	document := skillOverridesDocument{
		SchemaVersion: skillOverridesSchemaVersion,
		Skills:        make(map[string]skillOverride),
	}
	path := m.overridesPath()
	if err := atomicfile.ReadJSON(ctx, path, &document); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return document, nil
		}
		return skillOverridesDocument{}, fmt.Errorf("读取 Skill 展示配置失败: %w", err)
	}
	if document.SchemaVersion != skillOverridesSchemaVersion {
		return skillOverridesDocument{}, fmt.Errorf(
			"读取 Skill 展示配置失败: 不支持 schema_version=%d",
			document.SchemaVersion,
		)
	}
	if document.Skills == nil {
		document.Skills = make(map[string]skillOverride)
	}

	// 文件虽然是 Humbert 自己写入的，仍把磁盘内容视为不可信。这样用户手工编辑或未来
	// Schema 迁移出错时，不会把非法别名带进 WebView。
	for name, override := range document.Skills {
		normalizedName, err := normalizeSkillName(name)
		if err != nil || normalizedName != name {
			return skillOverridesDocument{}, fmt.Errorf("读取 Skill 展示配置失败: 非法 Skill 名称 %q", name)
		}
		normalizedAlias, err := normalizeSkillAlias(override.Alias)
		if err != nil {
			return skillOverridesDocument{}, fmt.Errorf("读取 Skill %q Alias 失败: %w", name, err)
		}
		if normalizedAlias == "" {
			delete(document.Skills, name)
			continue
		}
		document.Skills[name] = skillOverride{Alias: normalizedAlias}
	}
	return document, nil
}

func (m *Manager) writeOverridesLocked(ctx context.Context, document skillOverridesDocument) error {
	document.SchemaVersion = skillOverridesSchemaVersion
	if document.Skills == nil {
		document.Skills = make(map[string]skillOverride)
	}
	if err := atomicfile.WriteJSON(ctx, m.overridesPath(), 0o600, document); err != nil {
		return fmt.Errorf("保存 Skill 展示配置失败: %w", err)
	}
	return nil
}

// removeAliasLocked 在 Skill Package 已经物理删除后清理对应 Alias。
//
// Alias 是非关键 UI 元数据，因此调用方可以选择 best-effort；即使这里失败也不能把已经成功
// 删除的 Package 伪装成“删除失败”。
func (m *Manager) removeAliasLocked(ctx context.Context, name string) error {
	document, err := m.readOverridesLocked(ctx)
	if err != nil {
		return err
	}
	if _, exists := document.Skills[name]; !exists {
		return nil
	}
	delete(document.Skills, name)
	return m.writeOverridesLocked(ctx, document)
}

func normalizeSkillAlias(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	if !utf8.ValidString(value) {
		return "", fmt.Errorf("%w: Skill Alias 必须是 UTF-8 文本", ErrInvalidSkill)
	}
	if utf8.RuneCountInString(value) > maxSkillAliasRunes {
		return "", fmt.Errorf("%w: Skill Alias 不能超过 %d 个字符", ErrInvalidSkill, maxSkillAliasRunes)
	}
	for _, r := range value {
		if r == '\n' || r == '\r' || unicode.IsControl(r) {
			return "", fmt.Errorf("%w: Skill Alias 不能包含换行或控制字符", ErrInvalidSkill)
		}
	}
	return value, nil
}
