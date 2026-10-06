package skills

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

// Git 获取只负责在本次暂存目录中生成候选文件，不提交 Skills Root。
// 固定命令禁止 hooks/file/ext protocol；导出的归档继续经过统一 ZIP 和 Package 校验。
// 下载失败是否回退 Archive、暂存目录何时清理由 remote_installer 的编排入口决定。

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

// 安装获取是用户显式操作，保留宿主凭据辅助环境，并补入已探测 Git 的目录；不执行仓库脚本。
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

// 先按用户指定路径/名称收窄候选；未指定时导出候选集合，最终选择仍由共同包检查入口完成。
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
