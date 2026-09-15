package skills

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	remoteSkillArchiveExpansionFactor = int64(8)
	remoteSkillArchiveFileFactor      = 16
	remoteSkillArchiveMaxExpanded     = int64(256 * 1024 * 1024)
	remoteSkillArchiveMaxFiles        = 8192
	remoteSkillUserAgent              = "Humbert-Skill-Installer/1"
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

func (m *Manager) publicSkillHTTPClient() *http.Client {
	return m.publicSkillHTTPClientWithHTTP2(true)
}

func (m *Manager) publicSkillHTTPClientWithHTTP2(forceHTTP2 bool) *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			Proxy:                 nil,
			DialContext:           (&publicSkillDialer{}).DialContext,
			ForceAttemptHTTP2:     forceHTTP2,
			MaxIdleConns:          16,
			MaxIdleConnsPerHost:   4,
			IdleConnTimeout:       30 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ResponseHeaderTimeout: 30 * time.Second,
		},
		CheckRedirect: func(request *http.Request, via []*http.Request) error {
			if len(via) > m.config.MaxRedirects {
				return fmt.Errorf("远程 Skill 重定向超过 %d 次上限", m.config.MaxRedirects)
			}
			if _, err := parsePublicSkillURL(request.URL.String()); err != nil {
				return fmt.Errorf("远程 Skill 重定向目标不安全: %w", err)
			}
			return nil
		},
	}
}

type gitSkillRepositorySpec struct {
	RemoteURL    string
	Ref          string
	SkillPath    string
	SkillName    string
	Wrapper      string
	DisplayLabel string
}

const gitSkillTreeOutputMaxBytes = 16 * 1024 * 1024

// resolveSystemGitExecutable 在桌面应用环境中可靠定位系统 Git。
//
// macOS 从 Finder/Wails 启动时 PATH 往往不同于用户 Terminal，Homebrew Git 常见于
// /opt/homebrew/bin 或 /usr/local/bin。Repository Skill 的主 transport 不能仅依赖
// exec.LookPath("git")，否则会误判“未安装 Git”并退回整仓 ZIP，重新触发大型仓库
// MaxDownloadBytes 限制。
func resolveSystemGitExecutable() (string, error) {
	candidates := make([]string, 0, 8)
	appendCandidate := func(value string) {
		value = filepath.Clean(strings.TrimSpace(value))
		if value == "" || value == "." {
			return
		}
		for _, existing := range candidates {
			if filepath.Clean(existing) == value {
				return
			}
		}
		candidates = append(candidates, value)
	}

	// 桌面应用的 PATH 可能只命中 macOS 的 /usr/bin/git shim，而用户实际可用的是
	// Homebrew Git。因此 macOS 先检查常见真实安装目录，再考虑 PATH 命中的 executable。
	if runtime.GOOS == "darwin" {
		for _, candidate := range gitExecutableCandidates(runtime.GOOS, os.Getenv("ProgramFiles"), os.Getenv("ProgramFiles(x86)")) {
			appendCandidate(candidate)
		}
	}
	if resolved, err := exec.LookPath("git"); err == nil {
		if absolute, absErr := filepath.Abs(resolved); absErr == nil {
			appendCandidate(absolute)
		} else {
			appendCandidate(resolved)
		}
	}
	if runtime.GOOS != "darwin" {
		for _, candidate := range gitExecutableCandidates(runtime.GOOS, os.Getenv("ProgramFiles"), os.Getenv("ProgramFiles(x86)")) {
			appendCandidate(candidate)
		}
	}

	for _, candidate := range candidates {
		if err := probeGitExecutable(candidate); err == nil {
			return candidate, nil
		}
	}

	// /usr/bin/git 在部分 macOS 安装上只是 xcrun shim；如果用户通过 Xcode/CLT
	// 安装了 Git，但 PATH 又被桌面环境裁剪，xcrun 仍可以给出真实位置。
	if runtime.GOOS == "darwin" {
		xcrunCandidates := []string{"/usr/bin/xcrun"}
		if xcrun, err := exec.LookPath("xcrun"); err == nil {
			xcrunCandidates = append([]string{xcrun}, xcrunCandidates...)
		}
		for _, xcrun := range xcrunCandidates {
			if _, err := os.Stat(xcrun); err != nil {
				continue
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			cmd := exec.CommandContext(ctx, xcrun, "--find", "git")
			cmd.Env = os.Environ()
			output, runErr := cmd.Output()
			cancel()
			if runErr != nil {
				continue
			}
			resolved := filepath.Clean(strings.TrimSpace(string(output)))
			if resolved != "" && probeGitExecutable(resolved) == nil {
				return resolved, nil
			}
		}
	}

	return "", errors.New("系统未找到可用 Git；Humbert 已检查桌面应用 PATH、Homebrew/Xcode 常见安装目录并执行 git --version 验证")
}

func probeGitExecutable(candidate string) error {
	info, err := os.Stat(candidate)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return errors.New("Git 路径指向目录")
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o111 == 0 {
		return errors.New("Git 文件不可执行")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, candidate, "--version")
	cmd.Env = skillGitEnvironment(candidate)
	if output, err := cmd.CombinedOutput(); err != nil {
		message := strings.TrimSpace(string(output))
		if message == "" {
			return err
		}
		return fmt.Errorf("%w: %s", err, message)
	}
	return nil
}

func skillGitEnvironment(gitPath string) []string {
	environment := append([]string(nil), os.Environ()...)
	gitDir := filepath.Dir(strings.TrimSpace(gitPath))
	if gitDir == "" || gitDir == "." {
		return environment
	}
	currentPath := os.Getenv("PATH")
	parts := filepath.SplitList(currentPath)
	for _, existing := range parts {
		if filepath.Clean(existing) == filepath.Clean(gitDir) {
			return environment
		}
	}
	value := gitDir
	if strings.TrimSpace(currentPath) != "" {
		value += string(os.PathListSeparator) + currentPath
	}
	return append(environment, "PATH="+value)
}

func gitExecutableCandidates(goos, programFiles, programFilesX86 string) []string {
	switch goos {
	case "darwin":
		return []string{
			"/opt/homebrew/bin/git",
			"/usr/local/bin/git",
			"/usr/bin/git",
		}
	case "linux":
		return []string{
			"/usr/local/bin/git",
			"/usr/bin/git",
			"/bin/git",
		}
	case "windows":
		values := make([]string, 0, 4)
		for _, root := range []string{programFiles, programFilesX86} {
			root = strings.TrimSpace(root)
			if root == "" {
				continue
			}
			values = append(values,
				filepath.Join(root, "Git", "cmd", "git.exe"),
				filepath.Join(root, "Git", "bin", "git.exe"),
			)
		}
		return values
	default:
		return nil
	}
}

func isGitSkillRepositorySource(source RemoteSkillSource) bool {
	_, ok := gitSkillRepositorySpecForSource(source)
	return ok
}

func gitSkillRepositorySpecForSource(source RemoteSkillSource) (gitSkillRepositorySpec, bool) {
	provider := strings.ToLower(strings.TrimSpace(source.Provider))
	metadata := source.Metadata
	if metadata == nil {
		metadata = map[string]string{}
	}
	skillPath, err := normalizeRemoteSkillPath(source.SkillPath)
	if err != nil {
		return gitSkillRepositorySpec{}, false
	}
	ref := strings.TrimSpace(metadata["ref"])
	name := strings.TrimSpace(source.SkillName)

	switch provider {
	case "github", "skills.sh":
		owner := strings.TrimSpace(metadata["repository_owner"])
		repository := strings.TrimSpace(metadata["repository_name"])
		if owner == "" || repository == "" {
			return gitSkillRepositorySpec{}, false
		}
		return gitSkillRepositorySpec{
			RemoteURL:    "https://github.com/" + owner + "/" + repository + ".git",
			Ref:          ref,
			SkillPath:    skillPath,
			SkillName:    name,
			Wrapper:      "github-" + sanitizeRemoteWrapperSegment(owner) + "-" + sanitizeRemoteWrapperSegment(repository),
			DisplayLabel: owner + "/" + repository,
		}, true
	case "gitlab":
		host := strings.TrimSpace(metadata["repository_host"])
		repositoryPath := strings.Trim(strings.TrimSpace(metadata["repository_path"]), "/")
		if host == "" || repositoryPath == "" {
			return gitSkillRepositorySpec{}, false
		}
		return gitSkillRepositorySpec{
			RemoteURL:    "https://" + host + "/" + repositoryPath + ".git",
			Ref:          ref,
			SkillPath:    skillPath,
			SkillName:    name,
			Wrapper:      "gitlab-" + sanitizeRemoteWrapperSegment(host) + "-" + sanitizeRemoteWrapperSegment(strings.ReplaceAll(repositoryPath, "/", "-")),
			DisplayLabel: host + "/" + repositoryPath,
		}, true
	case "gitee":
		owner := strings.TrimSpace(metadata["repository_owner"])
		repository := strings.TrimSpace(metadata["repository_name"])
		if owner == "" || repository == "" {
			return gitSkillRepositorySpec{}, false
		}
		return gitSkillRepositorySpec{
			RemoteURL:    "https://gitee.com/" + owner + "/" + repository + ".git",
			Ref:          ref,
			SkillPath:    skillPath,
			SkillName:    name,
			Wrapper:      "gitee-" + sanitizeRemoteWrapperSegment(owner) + "-" + sanitizeRemoteWrapperSegment(repository),
			DisplayLabel: owner + "/" + repository,
		}, true
	default:
		return gitSkillRepositorySpec{}, false
	}
}

// materializeGitSkillRepository 使用系统 Git 获取 Repository Skill。
//
// Git 是 Repository 来源的首选 transport：它不依赖 GitHub/GitLab REST API 配额，天然支持
// branch/tag/commit 与用户现有 Credential Helper。Humbert 只执行固定的 init/fetch/ls-tree/
// archive 命令，不 checkout、不运行 repository hooks，也不执行仓库内容。最终文件仍统一经过
// ZIP entry、symlink、Package Size、File Count 与 SKILL.md 校验。
//
// handled=false 表示当前机器没有 Git，调用方应回退到原有 HTTPS Archive 下载；Git 已存在但
// Repository 操作失败时 handled=true 并返回具体错误，调用方可以选择再尝试 Archive fallback。
func (m *Manager) materializeGitSkillRepository(
	ctx context.Context,
	source RemoteSkillSource,
	destination string,
) (handled bool, err error) {
	spec, ok := gitSkillRepositorySpecForSource(source)
	if !ok {
		return false, nil
	}
	gitPath, err := resolveSystemGitExecutable()
	if err != nil {
		// Repository 来源明确需要 Git-first。把“桌面环境找不到 Git”保留下来，
		// 这样 HTTPS Archive fallback 若因为整仓过大失败时，最终错误会同时指出
		// 真正原因，而不是误导用户继续修改 /tree URL 或放大下载上限。
		return true, err
	}

	workRoot := filepath.Dir(destination)
	repoDir := filepath.Join(workRoot, "git-repository")
	archivePath := filepath.Join(workRoot, "git-skill.zip")
	hooksDir := filepath.Join(workRoot, "git-empty-hooks")
	if err := os.RemoveAll(repoDir); err != nil {
		return true, err
	}
	_ = os.Remove(archivePath)
	if err := os.MkdirAll(repoDir, 0o700); err != nil {
		return true, err
	}
	if err := os.MkdirAll(hooksDir, 0o700); err != nil {
		return true, err
	}

	runGit := func(args ...string) ([]byte, error) {
		base := []string{
			"-c", "core.hooksPath=" + hooksDir,
			"-c", "protocol.file.allow=never",
			"-c", "protocol.ext.allow=never",
			"-c", "submodule.recurse=false",
		}
		command := exec.CommandContext(ctx, gitPath, append(base, args...)...)
		command.Env = append(skillGitEnvironment(gitPath), "GIT_TERMINAL_PROMPT=0", "GCM_INTERACTIVE=Never")
		output, commandErr := command.CombinedOutput()
		if len(output) > gitSkillTreeOutputMaxBytes {
			return nil, fmt.Errorf("Git 输出超过 %d bytes 安全上限", gitSkillTreeOutputMaxBytes)
		}
		if commandErr != nil {
			message := strings.TrimSpace(string(output))
			if len(message) > 2048 {
				message = message[len(message)-2048:]
			}
			if message == "" {
				return nil, commandErr
			}
			return nil, fmt.Errorf("%w: %s", commandErr, message)
		}
		return output, nil
	}

	if _, err := runGit("-C", repoDir, "init", "--quiet"); err != nil {
		return true, fmt.Errorf("初始化 Skill Git Repository 失败: %w", err)
	}
	if _, err := runGit("-C", repoDir, "remote", "add", "origin", spec.RemoteURL); err != nil {
		return true, fmt.Errorf("配置 Skill Git Remote 失败: %w", err)
	}
	ref := strings.TrimSpace(spec.Ref)
	if ref == "" {
		ref = "HEAD"
	}
	if _, err := runGit("-C", repoDir, "fetch", "--quiet", "--depth=1", "--filter=blob:none", "--no-tags", "origin", ref); err != nil {
		return true, fmt.Errorf("Git 拉取 %s@%s 失败: %w", spec.DisplayLabel, ref, err)
	}

	treeOutput, err := runGit("-C", repoDir, "ls-tree", "-r", "-z", "--name-only", "FETCH_HEAD")
	if err != nil {
		return true, fmt.Errorf("读取 Git Skill Tree 失败: %w", err)
	}
	candidates := gitSkillCandidateDirectories(treeOutput)
	if len(candidates) == 0 {
		return true, fmt.Errorf("%w: Repository %s 中未发现 %s", ErrSkillNotFound, spec.DisplayLabel, SkillDefinitionFileName)
	}
	archivePaths, err := selectGitSkillArchivePaths(candidates, spec.SkillPath, spec.SkillName)
	if err != nil {
		return true, err
	}

	archiveArgs := []string{"-C", repoDir, "archive", "--format=zip", "--prefix=" + spec.Wrapper + "/", "--output=" + archivePath, "FETCH_HEAD"}
	if len(archivePaths) > 0 {
		archiveArgs = append(archiveArgs, "--")
		archiveArgs = append(archiveArgs, archivePaths...)
	}
	if _, err := runGit(archiveArgs...); err != nil {
		return true, fmt.Errorf("导出 Git Skill Package 失败: %w", err)
	}
	if err := m.extractRemoteArchive(ctx, archivePath, destination); err != nil {
		return true, fmt.Errorf("解压 Git Skill Package 失败: %w", err)
	}

	m.logger.Info(
		ctx,
		"远程 Skill 已通过 Git 获取",
		"operation", "skill.remote.git.fetch",
		"provider", source.Provider,
		"repository", spec.DisplayLabel,
		"ref", ref,
		"skill_path", spec.SkillPath,
	)
	return true, nil
}

func gitSkillCandidateDirectories(treeOutput []byte) []string {
	seen := make(map[string]struct{})
	for _, raw := range strings.Split(string(treeOutput), "\x00") {
		value := strings.Trim(strings.ReplaceAll(raw, "\\", "/"), "/")
		if value == "" {
			continue
		}
		if value == SkillDefinitionFileName {
			seen["."] = struct{}{}
			continue
		}
		if !strings.HasSuffix(value, "/"+SkillDefinitionFileName) {
			continue
		}
		directory := strings.TrimSuffix(value, "/"+SkillDefinitionFileName)
		if directory != "" {
			seen[directory] = struct{}{}
		}
	}
	result := make([]string, 0, len(seen))
	for value := range seen {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func selectGitSkillArchivePaths(candidates []string, requestedPath string, requestedName string) ([]string, error) {
	requestedPath, err := normalizeRemoteSkillPath(requestedPath)
	if err != nil {
		return nil, err
	}
	requestedName = strings.TrimSpace(requestedName)
	selected := make([]string, 0, len(candidates))
	if requestedPath != "" {
		for _, candidate := range candidates {
			if strings.Trim(candidate, "/") == strings.Trim(requestedPath, "/") {
				selected = append(selected, candidate)
			}
		}
		if len(selected) == 0 {
			return nil, fmt.Errorf("%w: Repository 中没有找到 skill_path=%q", ErrSkillNotFound, requestedPath)
		}
		return minimalGitSkillArchivePaths(selected), nil
	}

	if requestedName != "" {
		for _, candidate := range candidates {
			if candidate != "." && path.Base(candidate) == requestedName {
				selected = append(selected, candidate)
			}
		}
		if len(selected) > 0 {
			return minimalGitSkillArchivePaths(selected), nil
		}
	}
	return minimalGitSkillArchivePaths(candidates), nil
}

func minimalGitSkillArchivePaths(values []string) []string {
	cleaned := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, raw := range values {
		value := strings.Trim(strings.ReplaceAll(raw, "\\", "/"), "/")
		if value == "" || value == "." {
			return nil // Root SKILL.md 表示整个 Repository 本身就是一个 Skill Package。
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		cleaned = append(cleaned, value)
	}
	sort.Slice(cleaned, func(i, j int) bool {
		leftDepth := strings.Count(cleaned[i], "/")
		rightDepth := strings.Count(cleaned[j], "/")
		if leftDepth != rightDepth {
			return leftDepth < rightDepth
		}
		return cleaned[i] < cleaned[j]
	})
	result := make([]string, 0, len(cleaned))
	for _, candidate := range cleaned {
		covered := false
		for _, parent := range result {
			if strings.HasPrefix(candidate, parent+"/") {
				covered = true
				break
			}
		}
		if !covered {
			result = append(result, candidate)
		}
	}
	return result
}

func sanitizeRemoteWrapperSegment(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "repo"
	}
	var builder strings.Builder
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			builder.WriteRune(r)
		default:
			builder.WriteByte('-')
		}
	}
	result := strings.Trim(builder.String(), "-.")
	if result == "" {
		return "repo"
	}
	return result
}

func (m *Manager) downloadRemoteArchive(ctx context.Context, source *url.URL, destination string) error {
	client := m.publicSkillHTTPClient()
	defer client.CloseIdleConnections()

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, source.String(), nil)
	if err != nil {
		return fmt.Errorf("创建远程 Skill 请求失败: %w", err)
	}
	request.Header.Set("Accept", "application/zip, application/octet-stream;q=0.9")
	request.Header.Set("User-Agent", remoteSkillUserAgent)

	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("请求远程 Skill 失败: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("远程 Skill HTTP 状态异常: %d", response.StatusCode)
	}
	if response.ContentLength > m.config.MaxDownloadBytes {
		return fmt.Errorf(
			"远程 Skill ZIP 过大: Content-Length=%d，最大允许 %d bytes；如果来源是大型 Git 仓库，请优先使用 /tree/<ref>/<skill-path> 指向具体 Skill 子目录，或使用项目发布的轻量 Skill ZIP，而不是提高整个仓库下载上限",
			response.ContentLength,
			m.config.MaxDownloadBytes,
		)
	}

	output, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("创建远程 Skill ZIP 临时文件失败: %w", err)
	}
	closed := false
	defer func() {
		if !closed {
			_ = output.Close()
		}
	}()

	limited := io.LimitReader(response.Body, m.config.MaxDownloadBytes+1)
	written, err := io.Copy(output, limited)
	if err != nil {
		return fmt.Errorf("写入远程 Skill ZIP 失败: %w", err)
	}
	if written > m.config.MaxDownloadBytes {
		return fmt.Errorf(
			"远程 Skill ZIP 超过 %d bytes 下载上限；大型 Git 仓库请使用 /tree/<ref>/<skill-path> 或项目发布的轻量 Skill ZIP",
			m.config.MaxDownloadBytes,
		)
	}
	if err := output.Sync(); err != nil {
		return fmt.Errorf("同步远程 Skill ZIP 失败: %w", err)
	}
	if err := output.Close(); err != nil {
		return fmt.Errorf("关闭远程 Skill ZIP 失败: %w", err)
	}
	closed = true
	return nil
}

// extractRemoteArchive 在写文件之前先检查 ZIP 元数据，再逐个安全解压。
//
// ZIP 只是传输容器，最终 Skill Package 仍受更严格的 MaxPackageBytes/MaxFiles 限制。这里允许
// 仓库 ZIP 比最终 Skill 大一些，便于从包含多个 Skill 的仓库中选一个子目录，但仍使用 4 倍
// 上限约束整个解压工作区，防止 zip bomb。
func (m *Manager) extractRemoteArchive(ctx context.Context, archivePath string, destination string) error {
	reader, err := zip.OpenReader(archivePath)
	if err != nil {
		return fmt.Errorf("只支持标准 ZIP Skill Package: %w", err)
	}
	defer reader.Close()

	maxExpanded := m.config.MaxPackageBytes * remoteSkillArchiveExpansionFactor
	if maxExpanded > remoteSkillArchiveMaxExpanded {
		maxExpanded = remoteSkillArchiveMaxExpanded
	}
	maxFiles := m.config.MaxFiles * remoteSkillArchiveFileFactor
	if maxFiles < m.config.MaxFiles {
		maxFiles = m.config.MaxFiles
	}
	if maxFiles > remoteSkillArchiveMaxFiles {
		maxFiles = remoteSkillArchiveMaxFiles
	}

	var expanded int64
	files := 0
	for _, entry := range reader.File {
		if err := ctx.Err(); err != nil {
			return err
		}
		cleaned, isDir, err := validateArchiveEntry(entry)
		if err != nil {
			return err
		}
		if cleaned == "" || isDir {
			continue
		}
		files++
		if files > maxFiles {
			return fmt.Errorf("远程 Skill ZIP 文件数量超过 %d 个安全上限", maxFiles)
		}
		if entry.UncompressedSize64 > uint64(maxExpanded) ||
			expanded > maxExpanded-int64(entry.UncompressedSize64) {
			return fmt.Errorf("远程 Skill ZIP 解压后体积超过 %d bytes 安全上限", maxExpanded)
		}
		expanded += int64(entry.UncompressedSize64)
	}

	for _, entry := range reader.File {
		if err := ctx.Err(); err != nil {
			return err
		}
		cleaned, isDir, err := validateArchiveEntry(entry)
		if err != nil {
			return err
		}
		if cleaned == "" {
			continue
		}
		target := filepath.Join(destination, filepath.FromSlash(cleaned))
		if isDir {
			if err := os.MkdirAll(target, 0o700); err != nil {
				return fmt.Errorf("创建 Skill ZIP 目录失败: %w", err)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return fmt.Errorf("创建 Skill ZIP 父目录失败: %w", err)
		}
		if err := extractArchiveRegularFile(ctx, entry, target); err != nil {
			return err
		}
	}
	return nil
}

func validateArchiveEntry(entry *zip.File) (string, bool, error) {
	if entry == nil {
		return "", false, errors.New("远程 Skill ZIP 包含空 Entry")
	}
	name := strings.TrimSpace(strings.ReplaceAll(entry.Name, "\\", "/"))
	if name == "" {
		return "", false, nil
	}
	if strings.HasPrefix(name, "/") {
		return "", false, fmt.Errorf("%w: ZIP Entry 不能是绝对路径: %q", ErrInvalidSkill, name)
	}
	cleaned := path.Clean(name)
	if cleaned == "." {
		return "", entry.FileInfo().IsDir(), nil
	}
	if cleaned == ".." || strings.HasPrefix(cleaned, "../") || strings.Contains(cleaned, ":") {
		return "", false, fmt.Errorf("%w: ZIP Entry 越过包根目录: %q", ErrInvalidSkill, name)
	}
	mode := entry.Mode()
	if mode&os.ModeSymlink != 0 {
		return "", false, fmt.Errorf("%w: ZIP 不能包含符号链接: %q", ErrInvalidSkill, cleaned)
	}
	if entry.FileInfo().IsDir() {
		return cleaned, true, nil
	}
	if !mode.IsRegular() {
		return "", false, fmt.Errorf("%w: ZIP 只能包含普通文件和目录: %q", ErrInvalidSkill, cleaned)
	}
	return cleaned, false, nil
}

func extractArchiveRegularFile(ctx context.Context, entry *zip.File, destination string) error {
	input, err := entry.Open()
	if err != nil {
		return fmt.Errorf("打开 ZIP Entry 失败: %w", err)
	}
	defer input.Close()
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("创建 ZIP 解压文件失败: %w", err)
	}
	closed := false
	defer func() {
		if !closed {
			_ = output.Close()
		}
	}()

	buffer := make([]byte, 64*1024)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		read, readErr := input.Read(buffer)
		if read > 0 {
			written := 0
			for written < read {
				n, writeErr := output.Write(buffer[written:read])
				if writeErr != nil {
					return fmt.Errorf("写入 ZIP 解压文件失败: %w", writeErr)
				}
				if n <= 0 {
					return fmt.Errorf("写入 ZIP 解压文件失败: %w", io.ErrShortWrite)
				}
				written += n
			}
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return fmt.Errorf("读取 ZIP Entry 失败: %w", readErr)
		}
	}
	if err := output.Sync(); err != nil {
		return fmt.Errorf("同步 ZIP 解压文件失败: %w", err)
	}
	if err := output.Close(); err != nil {
		return fmt.Errorf("关闭 ZIP 解压文件失败: %w", err)
	}
	closed = true
	return nil
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

// publicSkillDialer 是远程 Skill 下载的 SSRF 边界。
//
// Hostname 的所有 DNS 结果都会先校验；只要出现私网、Loopback、Link-Local、Multicast 或
// Unspecified 地址就整体拒绝。真正连接时直接拨已经校验过的 IP，避免 DNS rebinding 在
// “校验”和“连接”之间替换解析结果。
type publicSkillDialer struct{}

func (d *publicSkillDialer) DialContext(ctx context.Context, network string, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, fmt.Errorf("解析远程 Skill 网络地址失败: %w", err)
	}
	addresses, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return nil, fmt.Errorf("解析远程 Skill Host 失败: %w", err)
	}
	if len(addresses) == 0 {
		return nil, errors.New("远程 Skill Host 没有可用 IP")
	}
	for _, ip := range addresses {
		if !isPublicSkillIP(ip) {
			return nil, fmt.Errorf("远程 Skill Host 解析到非公网地址: %s", ip.String())
		}
	}

	dialer := &net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}
	var lastErr error
	for _, ip := range addresses {
		connection, dialErr := dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		if dialErr == nil {
			return connection, nil
		}
		lastErr = dialErr
	}
	return nil, fmt.Errorf("连接远程 Skill Host 失败: %w", lastErr)
}

func isPublicSkillIP(ip netip.Addr) bool {
	if !ip.IsValid() {
		return false
	}
	ip = ip.Unmap()
	return !ip.IsUnspecified() &&
		!ip.IsLoopback() &&
		!ip.IsPrivate() &&
		!ip.IsLinkLocalUnicast() &&
		!ip.IsLinkLocalMulticast() &&
		!ip.IsMulticast()
}
