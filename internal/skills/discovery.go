package skills

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type discoveredPackage struct {
	Path    string
	RawPath string
	Root    string
	Package Package
}

// DiscoverFromDirectory 扫描一个本地 Skill 或包含多个 Skill 的仓库目录。
// 扫描是只读操作，不会复制文件或修改 Skills Root。
func (m *Manager) DiscoverFromDirectory(ctx context.Context, sourceDirectory string) (DiscoveryResult, error) {
	if err := m.validate(); err != nil {
		return DiscoveryResult{}, err
	}
	if ctx == nil {
		return DiscoveryResult{}, errors.New("context.Context 不能为空")
	}
	root, err := canonicalDiscoveryDirectory(sourceDirectory)
	if err != nil {
		return DiscoveryResult{}, err
	}
	packages, err := m.discoverPackages(ctx, root, "local", "", "")
	if err != nil {
		return DiscoveryResult{}, err
	}
	return DiscoveryResult{
		SourceKind:    SkillSourceKindLocal,
		Provider:      "local",
		DisplaySource: root,
		Candidates:    m.projectDiscoveryCandidates(packages),
	}, nil
}

// DiscoverFromURL 下载一次远程仓库/归档并列出其中候选 Skill。
// skillPath 为空时自动发现全部 SKILL.md；非空时只返回该路径对应的候选。
func (m *Manager) DiscoverFromURL(ctx context.Context, sourceURL string, skillPath string) (DiscoveryResult, error) {
	if err := m.validate(); err != nil {
		return DiscoveryResult{}, err
	}
	if ctx == nil {
		return DiscoveryResult{}, errors.New("context.Context 不能为空")
	}
	resolved, extractDir, cleanup, err := m.prepareRemoteSkillArchive(ctx, sourceURL, skillPath)
	if err != nil {
		return DiscoveryResult{}, err
	}
	defer cleanup()

	packages, err := m.discoverPackages(ctx, extractDir, resolved.Provider, resolved.SkillPath, resolved.SkillName)
	if err != nil {
		return DiscoveryResult{}, err
	}
	return DiscoveryResult{
		SourceKind:    SkillSourceKindRemote,
		Provider:      resolved.Provider,
		DisplaySource: safeDisplayRemoteURL(resolved.OriginalURL),
		Candidates:    m.projectDiscoveryCandidates(packages),
	}, nil
}

// InstallDiscoveredFromDirectory 从一次本地仓库选择中原子安装多个 Skill。
func (m *Manager) InstallDiscoveredFromDirectory(
	ctx context.Context,
	sourceDirectory string,
	paths []string,
) ([]Info, error) {
	if err := m.validate(); err != nil {
		return nil, err
	}
	root, err := canonicalDiscoveryDirectory(sourceDirectory)
	if err != nil {
		return nil, err
	}
	packages, err := m.discoverPackages(ctx, root, "local", "", "")
	if err != nil {
		return nil, err
	}
	selected, err := selectDiscoveredPackages(packages, paths)
	if err != nil {
		return nil, err
	}
	requests := make([]validatedInstallRequest, 0, len(selected))
	for _, item := range selected {
		if !item.Package.Info.Valid {
			return nil, fmt.Errorf("%w: %s", ErrInvalidSkill, item.Package.Info.Error)
		}
		source, sourceErr := localSourceInfo(item.Root, item.Package.Info.Identity, time.Now())
		if sourceErr != nil {
			return nil, sourceErr
		}
		requests = append(requests, validatedInstallRequest{Package: item.Package, Source: source})
	}
	return m.installValidatedPackages(ctx, requests)
}

// InstallDiscoveredFromURL 从同一个已下载远程仓库中原子安装用户选择的多个 Skill。
func (m *Manager) InstallDiscoveredFromURL(
	ctx context.Context,
	sourceURL string,
	paths []string,
) ([]Info, error) {
	if err := m.validate(); err != nil {
		return nil, err
	}
	if ctx == nil {
		return nil, errors.New("context.Context 不能为空")
	}
	resolved, extractDir, cleanup, err := m.prepareRemoteSkillArchive(ctx, sourceURL, "")
	if err != nil {
		return nil, err
	}
	defer cleanup()

	packages, err := m.discoverPackages(ctx, extractDir, resolved.Provider, "", resolved.SkillName)
	if err != nil {
		return nil, err
	}
	selected, err := selectDiscoveredPackages(packages, paths)
	if err != nil {
		return nil, err
	}
	requests := make([]validatedInstallRequest, 0, len(selected))
	now := time.Now()
	for _, item := range selected {
		if !item.Package.Info.Valid {
			return nil, fmt.Errorf("%w: %s", ErrInvalidSkill, item.Package.Info.Error)
		}
		candidateSource := resolved.Clone()
		candidateSource.SkillPath = item.Path
		if candidateSource.SkillPath == "." {
			candidateSource.SkillPath = ""
		}
		candidateSource.SkillName = item.Package.Info.Name
		source, sourceErr := remoteSourceInfo(candidateSource, item.Package.Info.Identity, now)
		if sourceErr != nil {
			return nil, sourceErr
		}
		requests = append(requests, validatedInstallRequest{Package: item.Package, Source: source})
	}
	return m.installValidatedPackages(ctx, requests)
}

func (m *Manager) discoverPackages(
	ctx context.Context,
	root string,
	provider string,
	requestedPath string,
	requestedName string,
) ([]discoveredPackage, error) {
	if ctx == nil {
		return nil, errors.New("context.Context 不能为空")
	}
	requestedPath, err := normalizeRemoteSkillPath(requestedPath)
	if err != nil {
		return nil, err
	}
	requestedName = strings.TrimSpace(requestedName)

	rawCandidates, err := collectSkillDirectories(ctx, root)
	if err != nil {
		return nil, err
	}
	if len(rawCandidates) == 0 {
		return nil, fmt.Errorf("%w: 未发现 %s", ErrSkillNotFound, SkillDefinitionFileName)
	}

	result := make([]discoveredPackage, 0, len(rawCandidates))
	for _, raw := range rawCandidates {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		displayPath := sourceRelativeSkillPath(provider, raw)
		if displayPath == "" {
			displayPath = "."
		}
		if requestedPath != "" && !discoveryPathMatches(requestedPath, raw, displayPath) {
			continue
		}
		packageRoot := filepath.Join(root, filepath.FromSlash(raw))
		pkg, inspectErr := inspectPackage(ctx, packageRoot, m.config, false)
		if inspectErr != nil {
			if requestedName != "" || requestedPath != "" {
				// 明确指定目标时不能因为目标自身坏掉而静默返回“未找到”。
				result = append(result, discoveredPackage{
					Path:    displayPath,
					RawPath: raw,
					Root:    packageRoot,
					Package: invalidDiscoveryPackage(packageRoot, displayPath, inspectErr),
				})
			}
			continue
		}
		if requestedName != "" && pkg.Info.Name != requestedName {
			continue
		}
		result = append(result, discoveredPackage{Path: displayPath, RawPath: raw, Root: packageRoot, Package: pkg})
	}
	// 未明确指定目标时，坏包也应展示给用户，而不是因为一个 repo 内的样例/旧目录阻断整个发现。
	// 这一步必须先于 len(result)==0 判断，这样“仓库里全部 Skill 都无效”时 UI 仍能看到每个
	// 候选的具体错误，而不是只得到一个笼统的“没有可安装 Skill”。
	if requestedPath == "" && requestedName == "" {
		seen := make(map[string]struct{}, len(result))
		for _, item := range result {
			seen[item.RawPath] = struct{}{}
		}
		for _, raw := range rawCandidates {
			if _, exists := seen[raw]; exists {
				continue
			}
			packageRoot := filepath.Join(root, filepath.FromSlash(raw))
			_, inspectErr := inspectPackage(ctx, packageRoot, m.config, false)
			if inspectErr == nil {
				continue
			}
			displayPath := sourceRelativeSkillPath(provider, raw)
			if displayPath == "" {
				displayPath = "."
			}
			result = append(result, discoveredPackage{
				Path:    displayPath,
				RawPath: raw,
				Root:    packageRoot,
				Package: invalidDiscoveryPackage(packageRoot, displayPath, inspectErr),
			})
		}
	}

	if len(result) == 0 {
		if requestedPath != "" {
			return nil, fmt.Errorf("%w: 没有找到 skill_path=%q", ErrSkillNotFound, requestedPath)
		}
		if requestedName != "" {
			return nil, fmt.Errorf("%w: 没有找到名为 %q 的 Skill", ErrSkillNotFound, requestedName)
		}
		return nil, fmt.Errorf("%w: 未发现 Skill Package", ErrSkillNotFound)
	}

	sort.Slice(result, func(i, j int) bool {
		leftValid := result[i].Package.Info.Valid
		rightValid := result[j].Package.Info.Valid
		if leftValid != rightValid {
			return leftValid
		}
		leftName := result[i].Package.Info.Name
		rightName := result[j].Package.Info.Name
		if leftName != rightName {
			return leftName < rightName
		}
		return result[i].Path < result[j].Path
	})
	return result, nil
}

func collectSkillDirectories(ctx context.Context, root string) ([]string, error) {
	root = filepath.Clean(root)
	candidates := make([]string, 0, 16)
	err := filepath.WalkDir(root, func(current string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		relative, err := filepath.Rel(root, current)
		if err != nil {
			return err
		}
		relativeSlash := filepath.ToSlash(relative)
		if relativeSlash != "." && shouldIgnorePackagePath(relativeSlash, entry.IsDir()) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() || entry.Name() != SkillDefinitionFileName {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		directoryRelative, err := filepath.Rel(root, filepath.Dir(current))
		if err != nil {
			return err
		}
		candidates = append(candidates, filepath.ToSlash(directoryRelative))
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("扫描 Skill Source 失败: %w", err)
	}
	sort.Strings(candidates)
	return candidates, nil
}

func (m *Manager) projectDiscoveryCandidates(values []discoveredPackage) []DiscoveryCandidate {
	installed := make(map[string]struct{})
	m.mu.RLock()
	entries, err := os.ReadDir(m.rootDir)
	if err == nil {
		for _, entry := range entries {
			if entry.IsDir() {
				installed[entry.Name()] = struct{}{}
			}
		}
	}
	m.mu.RUnlock()

	result := make([]DiscoveryCandidate, 0, len(values))
	for _, value := range values {
		_, exists := installed[value.Package.Info.Name]
		result = append(result, DiscoveryCandidate{Path: value.Path, Info: value.Package.Info, Installed: exists})
	}
	return result
}

func invalidDiscoveryPackage(root string, candidatePath string, err error) Package {
	name := path.Base(strings.Trim(candidatePath, "/"))
	if name == "." || name == "" {
		name = filepath.Base(root)
	}
	return Package{Info: Info{
		Name:          name,
		DirectoryName: filepath.Base(root),
		RootDir:       root,
		Valid:         false,
		Error:         truncateDiscoveryError(err),
	}}
}

func truncateDiscoveryError(err error) string {
	if err == nil {
		return "Skill Package 无效"
	}
	text := strings.TrimSpace(err.Error())
	if len(text) > 1024 {
		text = text[:1024] + "…"
	}
	return text
}

func selectDiscoveredPackages(values []discoveredPackage, paths []string) ([]discoveredPackage, error) {
	if len(paths) == 0 {
		return nil, errors.New("请至少选择一个 Skill")
	}
	byPath := make(map[string]discoveredPackage, len(values))
	for _, item := range values {
		byPath[item.Path] = item
	}
	selected := make([]discoveredPackage, 0, len(paths))
	seen := make(map[string]struct{}, len(paths))
	for _, raw := range paths {
		value := strings.TrimSpace(strings.ReplaceAll(raw, "\\", "/"))
		if value == "" {
			value = "."
		}
		if _, duplicate := seen[value]; duplicate {
			continue
		}
		seen[value] = struct{}{}
		item, exists := byPath[value]
		if !exists {
			return nil, fmt.Errorf("%w: Discovery 结果中不存在 %q", ErrSkillNotFound, value)
		}
		selected = append(selected, item)
	}
	return selected, nil
}

func canonicalDiscoveryDirectory(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", errors.New("Skill Source 目录不能为空")
	}
	absolute, err := filepath.Abs(value)
	if err != nil {
		return "", fmt.Errorf("解析 Skill Source 目录失败: %w", err)
	}
	absolute = filepath.Clean(absolute)
	info, err := os.Lstat(absolute)
	if err != nil {
		return "", fmt.Errorf("读取 Skill Source 目录失败: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return "", errors.New("Skill Source 必须是真实目录，不能是符号链接")
	}
	return absolute, nil
}

func sourceRelativeSkillPath(provider string, raw string) string {
	raw = strings.Trim(strings.ReplaceAll(raw, "\\", "/"), "/")
	if raw == "" || raw == "." {
		return "."
	}
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "github", "gitlab", "gitee", "skills.sh":
		stripped := stripArchiveWrapper(raw)
		if stripped == "" {
			return "."
		}
		return stripped
	default:
		return raw
	}
}

func discoveryPathMatches(requested string, raw string, display string) bool {
	requested = strings.Trim(strings.ReplaceAll(requested, "\\", "/"), "/")
	if requested == "" || requested == "." {
		return display == "." || raw == "."
	}
	return requested == strings.Trim(raw, "/") || requested == strings.Trim(display, "/")
}

func safeDisplayRemoteURL(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return ""
	}
	parsed.User = nil
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String()
}
