package skills

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/sda1-hacker/humbert-agent/internal/config"
	"gopkg.in/yaml.v3"
)

const (
	maxSkillNameLength          = 64
	maxSkillDescriptionLength   = 2048 // legacy compatibility; Agent Skills 标准上限为 1024。
	maxSkillCompatibilityLength = 500
)

type frontMatter struct {
	Name string `yaml:"name"`

	Description string `yaml:"description"`

	License string `yaml:"license"`

	Compatibility string `yaml:"compatibility"`

	Metadata map[string]any `yaml:"metadata"`

	AllowedTools any `yaml:"allowed-tools"`

	// context/agent/model 是 Eino Skill Middleware 的扩展字段。它们属于 Runtime 能力，
	// 不属于安装有效性；安装阶段保留，激活阶段由 RuntimeStatus 决定是否支持。
	Context string `yaml:"context"`

	Agent string `yaml:"agent"`

	Model string `yaml:"model"`
}

// inspectPackage 对一个物理目录做完整 Skill Package 校验并生成不可变 Package 描述。
//
// requireDirectoryNameMatch=true 用于已安装目录：目录名必须等于 frontmatter.name，避免
// “目录叫 pdf，SKILL.md 却声明 xlsx”造成 Agent 配置、Runtime Snapshot 与删除操作身份混乱。
// 安装外部目录时可以暂时关闭该约束，最终复制到 ~/.humbert-agent/skills/<name> 后会再次
// 以严格模式验证。
func inspectPackage(
	ctx context.Context,
	directory string,
	cfg config.SkillConfig,
	requireDirectoryNameMatch bool,
) (Package, error) {
	if ctx == nil {
		return Package{}, errors.New("context.Context 不能为空")
	}
	if err := ctx.Err(); err != nil {
		return Package{}, fmt.Errorf("检查 Skill Package 被取消: %w", err)
	}

	directory = strings.TrimSpace(directory)
	if directory == "" {
		return Package{}, fmt.Errorf("%w: Skill 目录不能为空", ErrInvalidSkill)
	}
	absolute, err := filepath.Abs(directory)
	if err != nil {
		return Package{}, fmt.Errorf("解析 Skill 目录绝对路径失败: %w", err)
	}
	absolute = filepath.Clean(absolute)

	info, err := os.Lstat(absolute)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Package{}, fmt.Errorf("%w: %s", ErrSkillNotFound, absolute)
		}
		return Package{}, fmt.Errorf("读取 Skill 目录状态失败: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return Package{}, fmt.Errorf("%w: Skill 根目录不能是符号链接", ErrInvalidSkill)
	}
	if !info.IsDir() {
		return Package{}, fmt.Errorf("%w: Skill 根路径不是目录", ErrInvalidSkill)
	}

	files, totalBytes, updatedAt, err := scanPackageFiles(ctx, absolute, cfg)
	if err != nil {
		return Package{}, err
	}

	definitionPath := filepath.Join(absolute, SkillDefinitionFileName)
	definitionBytes, err := os.ReadFile(definitionPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Package{}, fmt.Errorf("%w: 缺少 %s", ErrInvalidSkill, SkillDefinitionFileName)
		}
		return Package{}, fmt.Errorf("读取 %s 失败: %w", SkillDefinitionFileName, err)
	}
	// scanPackageFiles 已经冻结过 SKILL.md 的大小与哈希。这里再次读取正文后必须复核，
	// 防止外部进程在“扫描元数据”和“解析 frontmatter”之间替换文件，导致 Snapshot Revision
	// 与真正交给模型的 Skill 指令不是同一版本。
	var definitionIdentity *FileInfo
	for index := range files {
		if files[index].Path == SkillDefinitionFileName {
			copyInfo := files[index]
			definitionIdentity = &copyInfo
			break
		}
	}
	if definitionIdentity == nil {
		return Package{}, fmt.Errorf("%w: 缺少 %s", ErrInvalidSkill, SkillDefinitionFileName)
	}
	definitionDigest := sha256.Sum256(definitionBytes)
	if int64(len(definitionBytes)) != definitionIdentity.SizeBytes ||
		hex.EncodeToString(definitionDigest[:]) != definitionIdentity.SHA256 {
		return Package{}, fmt.Errorf("%w: %s 在扫描过程中发生变化", ErrSkillSnapshotStale, SkillDefinitionFileName)
	}
	if int64(len(definitionBytes)) > cfg.MaxDefinitionBytes {
		return Package{}, fmt.Errorf(
			"%w: %s 大小 %d 超过上限 %d",
			ErrInvalidSkill,
			SkillDefinitionFileName,
			len(definitionBytes),
			cfg.MaxDefinitionBytes,
		)
	}
	if !utf8.Valid(definitionBytes) || bytes.IndexByte(definitionBytes, 0) >= 0 {
		return Package{}, fmt.Errorf("%w: %s 必须是 UTF-8 文本", ErrInvalidSkill, SkillDefinitionFileName)
	}

	meta, body, err := parseDefinition(definitionBytes)
	if err != nil {
		return Package{}, err
	}
	name, err := normalizeSkillName(meta.Name)
	if err != nil {
		return Package{}, err
	}

	directoryName := filepath.Base(absolute)
	if requireDirectoryNameMatch && directoryName != name {
		return Package{}, fmt.Errorf(
			"%w: 目录名 %q 必须与 SKILL.md name %q 一致",
			ErrInvalidSkill,
			directoryName,
			name,
		)
	}

	description := normalizeDescription(meta.Description)
	if description == "" {
		return Package{}, fmt.Errorf("%w: SKILL.md description 不能为空", ErrInvalidSkill)
	}
	if len(description) > maxSkillDescriptionLength {
		return Package{}, fmt.Errorf(
			"%w: Skill description 长度不能超过 %d 字节",
			ErrInvalidSkill,
			maxSkillDescriptionLength,
		)
	}

	compatibility := normalizeDescription(meta.Compatibility)
	if len(compatibility) > maxSkillCompatibilityLength {
		return Package{}, fmt.Errorf(
			"%w: Skill compatibility 长度不能超过 %d 字节",
			ErrInvalidSkill,
			maxSkillCompatibilityLength,
		)
	}

	contextMode := strings.ToLower(strings.TrimSpace(meta.Context))
	if contextMode == "inline" {
		contextMode = ""
	}
	allowedTools := normalizeAllowedTools(meta.AllowedTools)
	metadata := normalizeMetadata(meta.Metadata)
	specStatus, specMessage := evaluateSpecCompatibility(name, description)
	runtimeStatus, runtimeMessage, diagnostics, scriptRuntimes := evaluateRuntimeCompatibility(
		contextMode,
		strings.TrimSpace(meta.Agent),
		strings.TrimSpace(meta.Model),
		compatibility,
		allowedTools,
		absolute,
		files,
	)
	if specStatus == SpecStatusLegacy {
		diagnostics = append([]Diagnostic{{
			Code:    "legacy_spec",
			Level:   DiagnosticWarning,
			Message: "该 Package 使用 Humbert 早期兼容格式：" + specMessage,
		}}, diagnostics...)
	}

	body = strings.TrimSpace(body)
	if body == "" {
		return Package{}, fmt.Errorf("%w: Skill %q 指令正文不能为空", ErrInvalidSkill, name)
	}

	hasReferences := false
	hasScripts := false
	hasAssets := false
	for _, file := range files {
		if strings.HasPrefix(file.Path, "references/") {
			hasReferences = true
		}
		if strings.HasPrefix(file.Path, "scripts/") {
			hasScripts = true
		}
		if strings.HasPrefix(file.Path, "assets/") {
			hasAssets = true
		}
	}

	return Package{
		Info: Info{
			Name:           name,
			Description:    description,
			SpecStatus:     specStatus,
			SpecMessage:    specMessage,
			License:        strings.TrimSpace(meta.License),
			Compatibility:  compatibility,
			Metadata:       metadata,
			AllowedTools:   allowedTools,
			EinoContext:    contextMode,
			EinoAgent:      strings.TrimSpace(meta.Agent),
			EinoModel:      strings.TrimSpace(meta.Model),
			RuntimeStatus:  runtimeStatus,
			RuntimeMessage: runtimeMessage,
			Diagnostics:    diagnostics,
			ScriptRuntimes: scriptRuntimes,
			DirectoryName:  directoryName,
			RootDir:        absolute,
			Identity:       packageIdentity(files),
			Valid:          true,
			FileCount:      len(files),
			SizeBytes:      totalBytes,
			HasReferences:  hasReferences,
			HasScripts:     hasScripts,
			HasAssets:      hasAssets,
			UpdatedAt:      updatedAt,
		},
		Content: body,
		Files:   files,
	}, nil
}

// parseDefinition 解析 SKILL.md 的 YAML frontmatter 与 Markdown 正文。
//
// Frontmatter 必须由独占一行的 --- 包围。未知 YAML 字段允许存在，以兼容社区 Skill
// 规范中的 license/metadata/compatibility 等扩展；Humbert 只读取自己需要并能够安全执行
// 的字段。未知字段不会自动获得任何运行能力。
func parseDefinition(data []byte) (frontMatter, string, error) {
	text := strings.TrimPrefix(string(data), "\ufeff")
	lines := strings.SplitAfter(text, "\n")
	if len(lines) == 0 || strings.TrimSpace(strings.TrimSuffix(lines[0], "\n")) != "---" {
		return frontMatter{}, "", fmt.Errorf(
			"%w: %s 必须以 YAML frontmatter 开始",
			ErrInvalidSkill,
			SkillDefinitionFileName,
		)
	}

	end := -1
	for index := 1; index < len(lines); index++ {
		line := strings.TrimSpace(strings.TrimSuffix(lines[index], "\n"))
		if line == "---" {
			end = index
			break
		}
	}
	if end < 0 {
		return frontMatter{}, "", fmt.Errorf(
			"%w: %s 缺少 frontmatter 结束标记 ---",
			ErrInvalidSkill,
			SkillDefinitionFileName,
		)
	}

	var yamlBuilder strings.Builder
	for _, line := range lines[1:end] {
		yamlBuilder.WriteString(line)
	}

	var meta frontMatter
	if err := yaml.Unmarshal([]byte(yamlBuilder.String()), &meta); err != nil {
		return frontMatter{}, "", fmt.Errorf("%w: 解析 SKILL.md frontmatter 失败: %v", ErrInvalidSkill, err)
	}

	var bodyBuilder strings.Builder
	for _, line := range lines[end+1:] {
		bodyBuilder.WriteString(line)
	}
	return meta, bodyBuilder.String(), nil
}

func normalizeSkillName(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("%w: Skill name 不能为空", ErrInvalidSkill)
	}
	if len(value) > maxSkillNameLength {
		return "", fmt.Errorf("%w: Skill name 长度不能超过 %d", ErrInvalidSkill, maxSkillNameLength)
	}

	// Agent Skills 标准名称只使用小写字母、数字和连字符。Humbert 早期版本还允许
	// 下划线；这里保留对已经安装/引用的 legacy Skill 的兼容，避免一次整理让现有
	// Agent Profile 失效。新社区标准 Skill 是这个校验集合的严格子集。
	for index, char := range value {
		valid := (char >= 'a' && char <= 'z') ||
			(char >= '0' && char <= '9') ||
			char == '-' || char == '_'
		if !valid {
			return "", fmt.Errorf("%w: Skill name %q 包含非法字符 %q", ErrInvalidSkill, value, char)
		}
		if index == 0 && (char == '-' || char == '_') {
			return "", fmt.Errorf("%w: Skill name 不能以 %q 开头", ErrInvalidSkill, char)
		}
	}
	return value, nil
}

func normalizeDescription(value string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
}

func normalizeMetadata(value map[string]any) map[string]string {
	if len(value) == 0 {
		return nil
	}
	result := make(map[string]string, len(value))
	for rawKey, rawValue := range value {
		key := strings.TrimSpace(rawKey)
		if key == "" || rawValue == nil {
			continue
		}
		var text string
		switch typed := rawValue.(type) {
		case string:
			text = strings.TrimSpace(typed)
		case bool:
			text = fmt.Sprintf("%t", typed)
		case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, float32, float64:
			text = fmt.Sprint(typed)
		default:
			// Agent Skills metadata 的标准值是 string。复杂结构不是安装失败理由，
			// 但 Humbert 也不会把未知嵌套对象带入 Runtime。
			continue
		}
		if text != "" {
			result[key] = text
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

func normalizeAllowedTools(value any) string {
	var values []string
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return normalizeDescription(typed)
	case []any:
		for _, item := range typed {
			if text, ok := item.(string); ok {
				text = normalizeDescription(text)
				if text != "" {
					values = append(values, text)
				}
			}
		}
	case []string:
		for _, item := range typed {
			item = normalizeDescription(item)
			if item != "" {
				values = append(values, item)
			}
		}
	default:
		return normalizeDescription(fmt.Sprint(typed))
	}
	return strings.Join(values, " ")
}

func cloneStringMap(value map[string]string) map[string]string {
	if len(value) == 0 {
		return nil
	}
	result := make(map[string]string, len(value))
	for key, item := range value {
		result[key] = item
	}
	return result
}

func scanPackageFiles(
	ctx context.Context,
	root string,
	cfg config.SkillConfig,
) ([]FileInfo, int64, time.Time, error) {
	// time.Time 单独导入会让 parser.go 的依赖更清晰；这里使用零值开始累计最大 mtime。
	var updatedAt time.Time
	files := make([]FileInfo, 0, 16)
	var totalBytes int64

	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}

		relative, err := filepath.Rel(root, path)
		if err != nil {
			return fmt.Errorf("计算 Skill 包相对路径失败: %w", err)
		}
		relative = filepath.ToSlash(relative)
		if relative == "." {
			return nil
		}
		if shouldIgnorePackagePath(relative, entry.IsDir()) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		entryInfo, err := entry.Info()
		if err != nil {
			return fmt.Errorf("读取 Skill 文件状态失败: %w", err)
		}
		if entryInfo.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%w: Skill 包不能包含符号链接: %s", ErrInvalidSkill, relative)
		}
		if entry.IsDir() {
			return nil
		}
		if !entryInfo.Mode().IsRegular() {
			return fmt.Errorf("%w: Skill 包只能包含普通文件: %s", ErrInvalidSkill, relative)
		}
		// Runtime 的 file 参数使用 `/` 分隔的包内相对路径。安装阶段直接拒绝无法被这套
		// 规则无歧义表示的文件名，避免“成功安装但资源永远无法安全读取”的半可用包。
		normalizedRelative, normalizeErr := normalizeAssetPath(relative)
		if normalizeErr != nil || normalizedRelative != relative {
			if normalizeErr != nil {
				return normalizeErr
			}
			return fmt.Errorf("%w: Skill 文件路径 %q 无法安全规范化", ErrInvalidSkill, relative)
		}
		if len(files)+1 > cfg.MaxFiles {
			return fmt.Errorf("%w: Skill 包文件数量超过上限 %d", ErrInvalidSkill, cfg.MaxFiles)
		}

		limit := cfg.MaxAssetBytes
		if relative == SkillDefinitionFileName {
			limit = cfg.MaxDefinitionBytes
		}
		if entryInfo.Size() > limit {
			return fmt.Errorf(
				"%w: 文件 %s 大小 %d 超过上限 %d",
				ErrInvalidSkill,
				relative,
				entryInfo.Size(),
				limit,
			)
		}
		if totalBytes+entryInfo.Size() > cfg.MaxPackageBytes {
			return fmt.Errorf(
				"%w: Skill 包总大小超过上限 %d",
				ErrInvalidSkill,
				cfg.MaxPackageBytes,
			)
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("读取 Skill 文件 %s 失败: %w", relative, err)
		}
		if int64(len(data)) != entryInfo.Size() {
			return fmt.Errorf("%w: Skill 文件 %s 在扫描过程中发生变化", ErrSkillSnapshotStale, relative)
		}

		digest := sha256.Sum256(data)
		files = append(files, FileInfo{
			Path:      relative,
			SizeBytes: entryInfo.Size(),
			SHA256:    hex.EncodeToString(digest[:]),
			Text:      utf8.Valid(data) && bytes.IndexByte(data, 0) < 0,
		})
		totalBytes += entryInfo.Size()
		if entryInfo.ModTime().After(updatedAt) {
			updatedAt = entryInfo.ModTime().UTC()
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, 0, time.Time{}, fmt.Errorf("扫描 Skill Package 被取消: %w", err)
		}
		return nil, 0, time.Time{}, err
	}

	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	definitionFound := false
	for _, file := range files {
		if file.Path == SkillDefinitionFileName {
			definitionFound = true
			break
		}
	}
	if !definitionFound {
		return nil, 0, time.Time{}, fmt.Errorf("%w: 缺少 %s", ErrInvalidSkill, SkillDefinitionFileName)
	}
	return files, totalBytes, updatedAt, nil
}

func shouldIgnorePackagePath(relative string, directory bool) bool {
	parts := strings.Split(relative, "/")
	for _, part := range parts {
		switch part {
		case ".git", ".hg", ".svn", "__MACOSX":
			return true
		}
	}
	if !directory {
		base := parts[len(parts)-1]
		return base == ".DS_Store" || base == "Thumbs.db"
	}
	return false
}

func packageIdentity(files []FileInfo) string {
	hash := sha256.New()
	for _, file := range files {
		_, _ = hash.Write([]byte(file.Path))
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write([]byte(file.SHA256))
		_, _ = hash.Write([]byte{0})
	}
	return hex.EncodeToString(hash.Sum(nil))
}
