package skills

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

// InstallFromURL 从公开 HTTPS Skill 来源下载、验证并安装 Package。
//
// Source Resolver 只负责把 Registry/仓库/归档页面规范化成 RemoteSkillSource；真正下载仍统一
// 经过 SSRF、Redirect、大小、ZIP 与 Package 校验。安装成功时会把 OriginalURL、Provider、
// skill_path 和 Resolver metadata 写入 Humbert 自己的来源文档，供后续 Check Update / Reinstall
// 使用；这些元数据不会写入第三方 Skill Package，也不会改变 Package Identity。
func (m *Manager) InstallFromURL(
	ctx context.Context,
	sourceURL string,
	skillPath string,
) (Info, error) {
	if err := m.validate(); err != nil {
		return Info{}, err
	}
	if ctx == nil {
		return Info{}, errors.New("context.Context 不能为空")
	}
	if err := ctx.Err(); err != nil {
		return Info{}, fmt.Errorf("安装远程 Skill 被取消: %w", err)
	}

	timeout := time.Duration(m.config.DownloadTimeoutMS) * time.Millisecond
	downloadCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	pkg, source, resolved, cleanup, err := m.prepareRemoteSkillPackage(downloadCtx, sourceURL, skillPath)
	if err != nil {
		return Info{}, err
	}
	defer cleanup()

	installed, err := m.installValidatedPackage(downloadCtx, pkg, source)
	if err != nil {
		return Info{}, fmt.Errorf("提交远程 Skill 安装失败: %w", err)
	}

	m.logger.Info(
		downloadCtx,
		"远程 Skill 已安装",
		"operation", "skill.remote.install",
		"skill_name", installed.Name,
		"source_provider", resolved.Provider,
		"source_host", resolved.DownloadURL.Hostname(),
		"file_count", installed.FileCount,
		"size_bytes", installed.SizeBytes,
	)
	return installed, nil
}

// prepareRemoteSkillPackage 把一个远程来源准备成“已验证但尚未提交”的 Package。
//
// 返回的 cleanup 必须由调用方执行。该函数不持有 Manager 锁，也不会修改 Skills Root 中现有
// Package，因此网络等待和 ZIP 解压不会阻塞 Catalog 读取。Install/Update 最终提交仍由各自的
// 原子目录切换流程完成。
func (m *Manager) prepareRemoteSkillPackage(
	ctx context.Context,
	sourceURL string,
	skillPath string,
) (Package, SourceInfo, RemoteSkillSource, func(), error) {
	resolved, extractDir, cleanup, err := m.prepareRemoteSkillArchive(ctx, sourceURL, skillPath)
	if err != nil {
		return Package{}, SourceInfo{}, RemoteSkillSource{}, func() {}, err
	}
	fail := func(err error) (Package, SourceInfo, RemoteSkillSource, func(), error) {
		cleanup()
		return Package{}, SourceInfo{}, RemoteSkillSource{}, func() {}, err
	}

	packageRoot, err := findRemoteSkillPackage(
		extractDir,
		resolved.SkillPath,
		resolved.SkillName,
		m.config.MaxDefinitionBytes,
	)
	if err != nil {
		return fail(err)
	}
	pkg, err := inspectPackage(ctx, packageRoot, m.config, false)
	if err != nil {
		return fail(fmt.Errorf("验证远程 Skill Package 失败: %w", err))
	}

	// 来源记录保存稳定的仓库内 SkillPath，而不是 zipball/gitlab archive 自动添加的顶层包装目录。
	if resolved.SkillPath == "" {
		rawRelative, relErr := filepath.Rel(extractDir, packageRoot)
		if relErr == nil {
			resolved.SkillPath = sourceRelativeSkillPath(resolved.Provider, filepath.ToSlash(rawRelative))
			if resolved.SkillPath == "." {
				resolved.SkillPath = ""
			}
		}
	}
	resolved.SkillName = pkg.Info.Name
	source, err := remoteSourceInfo(resolved, pkg.Info.Identity, time.Now())
	if err != nil {
		return fail(fmt.Errorf("准备远程 Skill 来源记录失败: %w", err))
	}
	return pkg, source, resolved.Clone(), cleanup, nil
}

// prepareRemoteSkillArchive 只负责来源解析、下载与安全解压，不选择具体 Skill。
// Repository Discovery 与单 Skill 安装共用它，避免两套网络/SSRF/ZIP 安全规则漂移。
func (m *Manager) prepareRemoteSkillArchive(
	ctx context.Context,
	sourceURL string,
	skillPath string,
) (RemoteSkillSource, string, func(), error) {
	resolved, err := m.sourceRegistry.Resolve(sourceURL, skillPath)
	if err != nil {
		return RemoteSkillSource{}, "", func() {}, err
	}

	workDir := filepath.Join(m.rootDir, ".humbert-remote-"+uuid.NewString())
	if err := os.Mkdir(workDir, 0o700); err != nil {
		return RemoteSkillSource{}, "", func() {}, fmt.Errorf("创建远程 Skill 临时目录失败: %w", err)
	}
	cleanup := func() {
		if removeErr := os.RemoveAll(workDir); removeErr != nil {
			m.logger.Warn(
				context.Background(),
				"清理远程 Skill 临时目录失败",
				"operation", "skill.remote.cleanup",
				"error", removeErr,
			)
		}
	}
	fail := func(err error) (RemoteSkillSource, string, func(), error) {
		cleanup()
		return RemoteSkillSource{}, "", func() {}, err
	}

	extractDir := filepath.Join(workDir, "extract")
	if err := os.Mkdir(extractDir, 0o700); err != nil {
		return fail(fmt.Errorf("创建远程 Skill 解压目录失败: %w", err))
	}

	// Repository URL（GitHub / GitLab / Gitee / skills.sh）优先使用系统 Git。
	// 这样安装/发现不依赖 GitHub REST API 配额，也不会为了一个子目录下载整个大型仓库。
	// Git 不可用时才回退到 Provider Resolver 给出的 HTTPS Archive；Archive 仍受
	// MaxDownloadBytes 限制，因此不会因为缺 Git 就自动放宽远程下载安全边界。
	var repositoryGitErr error
	repositorySource := isGitSkillRepositorySource(resolved)
	gitAvailable := false
	if repositorySource {
		handled, gitErr := m.materializeGitSkillRepository(ctx, resolved, extractDir)
		gitAvailable = handled
		if handled && gitErr == nil {
			return resolved.Clone(), extractDir, cleanup, nil
		}
		if handled && gitErr != nil {
			repositoryGitErr = gitErr
			m.logger.Warn(
				ctx,
				"Git Skill 获取失败，将尝试 HTTPS Archive fallback",
				"operation", "skill.remote.git.fallback",
				"provider", resolved.Provider,
				"error", gitErr,
			)
		}
	}

	archivePath := filepath.Join(workDir, "package.zip")
	if err := m.downloadRemoteArchive(ctx, resolved.DownloadURL, archivePath); err != nil {
		switch {
		case repositoryGitErr != nil:
			return fail(fmt.Errorf("Git 获取 Repository Skill 失败: %v；HTTPS Archive fallback 也失败: %w", repositoryGitErr, err))
		case repositorySource && !gitAvailable:
			return fail(fmt.Errorf("系统未找到 Git，已尝试 HTTPS Archive fallback 但失败: %w；大型或私有 Skill Repository 建议安装 Git，以便 Humbert 使用浅层稀疏获取而不依赖 Provider API 配额", err))
		default:
			return fail(fmt.Errorf("下载远程 Skill 失败: %w", err))
		}
	}
	if err := m.extractRemoteArchive(ctx, archivePath, extractDir); err != nil {
		return fail(fmt.Errorf("解压远程 Skill 失败: %w", err))
	}
	return resolved.Clone(), extractDir, cleanup, nil
}

func findRemoteSkillPackage(
	root string,
	requestedPath string,
	requestedName string,
	maxDefinitionBytes int64,
) (string, error) {
	candidates := make([]string, 0)
	err := filepath.WalkDir(root, func(current string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("%w: 解压目录出现符号链接", ErrInvalidSkill)
		}
		if entry.IsDir() || entry.Name() != SkillDefinitionFileName {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("%w: %s 不是普通文件", ErrInvalidSkill, SkillDefinitionFileName)
		}
		relative, err := filepath.Rel(root, filepath.Dir(current))
		if err != nil {
			return err
		}
		candidates = append(candidates, filepath.ToSlash(relative))
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("扫描远程 Skill ZIP 失败: %w", err)
	}
	if len(candidates) == 0 {
		return "", fmt.Errorf("%w: ZIP 中未找到 %s", ErrInvalidSkill, SkillDefinitionFileName)
	}
	sort.Strings(candidates)

	if requestedPath == "" && requestedName != "" {
		selected, err := selectRemoteSkillByName(
			root,
			candidates,
			requestedName,
			maxDefinitionBytes,
		)
		if err != nil {
			return "", err
		}
		return selected, nil
	}

	if requestedPath == "" {
		if len(candidates) != 1 {
			return "", fmt.Errorf(
				"%w: ZIP 中发现 %d 个 Skill（%s）；请提供 skill_path 指定要安装的目录",
				ErrInvalidSkill,
				len(candidates),
				strings.Join(limitStrings(candidates, 8), "、"),
			)
		}
		return filepath.Join(root, filepath.FromSlash(candidates[0])), nil
	}

	matches := make([]string, 0, 1)
	for _, candidate := range candidates {
		if candidate == requestedPath || stripArchiveWrapper(candidate) == requestedPath {
			matches = append(matches, candidate)
		}
	}
	if len(matches) == 0 {
		return "", fmt.Errorf(
			"%w: ZIP 中没有 skill_path=%q 对应的 %s",
			ErrSkillNotFound,
			requestedPath,
			SkillDefinitionFileName,
		)
	}
	if len(matches) > 1 {
		return "", fmt.Errorf("%w: skill_path=%q 匹配到多个 Skill", ErrInvalidSkill, requestedPath)
	}
	return filepath.Join(root, filepath.FromSlash(matches[0])), nil
}

// selectRemoteSkillByName 用来源提供的稳定 Skill 名称提示，从多 Skill 仓库中定位包。
//
// 先使用目录 basename 做快速匹配；没有命中时再只读取各候选 SKILL.md 的 frontmatter。
// 无关候选即使 frontmatter 无效也不会阻塞目标 Skill 安装，最终选中的 Package 仍会在
// InstallFromDirectory 中执行完整大小、路径、symlink、正文和 identity 校验。
func selectRemoteSkillByName(
	root string,
	candidates []string,
	requestedName string,
	maxDefinitionBytes int64,
) (string, error) {
	requestedName, err := normalizeSkillName(requestedName)
	if err != nil {
		return "", err
	}

	basenameMatches := make([]string, 0, 1)
	for _, candidate := range candidates {
		if path.Base(candidate) == requestedName {
			basenameMatches = append(basenameMatches, candidate)
		}
	}
	if len(basenameMatches) == 1 {
		return filepath.Join(root, filepath.FromSlash(basenameMatches[0])), nil
	}
	if len(basenameMatches) > 1 {
		return "", fmt.Errorf(
			"%w: Skill 名称 %q 匹配到多个目录（%s）",
			ErrInvalidSkill,
			requestedName,
			strings.Join(limitStrings(basenameMatches, 8), "、"),
		)
	}

	frontMatterMatches := make([]string, 0, 1)
	for _, candidate := range candidates {
		definitionPath := filepath.Join(
			root,
			filepath.FromSlash(candidate),
			SkillDefinitionFileName,
		)
		name, readErr := readRemoteSkillDefinitionName(definitionPath, maxDefinitionBytes)
		if readErr != nil {
			continue
		}
		if name == requestedName {
			frontMatterMatches = append(frontMatterMatches, candidate)
		}
	}
	if len(frontMatterMatches) == 1 {
		return filepath.Join(root, filepath.FromSlash(frontMatterMatches[0])), nil
	}
	if len(frontMatterMatches) > 1 {
		return "", fmt.Errorf(
			"%w: Skill name=%q 匹配到多个 SKILL.md（%s）",
			ErrInvalidSkill,
			requestedName,
			strings.Join(limitStrings(frontMatterMatches, 8), "、"),
		)
	}

	return "", fmt.Errorf(
		"%w: ZIP 中未找到名为 %q 的 Skill；可改用 skill_path 明确指定目录",
		ErrSkillNotFound,
		requestedName,
	)
}

func readRemoteSkillDefinitionName(definitionPath string, maxDefinitionBytes int64) (string, error) {
	if maxDefinitionBytes <= 0 {
		return "", errors.New("SKILL.md 读取上限无效")
	}
	file, err := os.Open(definitionPath)
	if err != nil {
		return "", err
	}
	defer file.Close()

	limited := io.LimitReader(file, maxDefinitionBytes+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return "", err
	}
	if int64(len(data)) > maxDefinitionBytes {
		return "", fmt.Errorf("%s 超过 %d bytes", SkillDefinitionFileName, maxDefinitionBytes)
	}
	meta, _, err := parseDefinition(data)
	if err != nil {
		return "", err
	}
	return normalizeSkillName(meta.Name)
}

func stripArchiveWrapper(value string) string {
	value = strings.Trim(value, "/")
	index := strings.IndexByte(value, '/')
	if index < 0 {
		return ""
	}
	return value[index+1:]
}

func limitStrings(values []string, max int) []string {
	if len(values) <= max {
		return values
	}
	result := append([]string(nil), values[:max]...)
	result = append(result, "…")
	return result
}
