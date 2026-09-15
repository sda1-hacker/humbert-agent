package skills

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
)

const (
	registryPageResolverPriority = 300
	repositoryResolverPriority   = 200
)

// NewSkillsShSourceResolver 返回 skills.sh 单 Skill 页面 Resolver。
func NewSkillsShSourceResolver() RemoteSkillSourceResolver {
	return skillsShSourceResolver{}
}

// NewGitHubSourceResolver 返回 GitHub.com 仓库 Resolver。
func NewGitHubSourceResolver() RemoteSkillSourceResolver {
	return githubSourceResolver{}
}

// NewGitLabSourceResolver 创建 GitLab 仓库 Resolver。
//
// hosts 为空时默认匹配 gitlab.com；也可以注册企业自建的公开 GitLab Host。Resolver 仅按 URL
// 规则构造该 Host 自身的 /api/v4 repository archive 地址，不探测服务类型。
func NewGitLabSourceResolver(hosts ...string) RemoteSkillSourceResolver {
	if len(hosts) == 0 {
		hosts = []string{"gitlab.com"}
	}
	normalized := make(map[string]struct{}, len(hosts))
	for _, host := range hosts {
		host = strings.ToLower(strings.TrimSpace(host))
		if host != "" {
			normalized[host] = struct{}{}
		}
	}
	return gitLabSourceResolver{hosts: normalized}
}

// NewGiteeSourceResolver 返回 Gitee 仓库 Resolver。
func NewGiteeSourceResolver() RemoteSkillSourceResolver {
	return giteeSourceResolver{}
}

// NewDirectArchiveSourceResolver 返回公开 HTTPS ZIP 兜底 Resolver。
func NewDirectArchiveSourceResolver() RemoteSkillSourceResolver {
	return directArchiveSourceResolver{}
}

type skillsShSourceResolver struct{}

func (skillsShSourceResolver) Name() string { return "skills.sh" }

func (skillsShSourceResolver) Priority() int { return registryPageResolverPriority }

func (skillsShSourceResolver) Match(parsed *url.URL) bool {
	if parsed == nil {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	return host == "skills.sh" || host == "www.skills.sh"
}

func (skillsShSourceResolver) Resolve(
	parsed *url.URL,
	explicitSkillPath string,
) (RemoteSkillSource, error) {
	if parsed == nil {
		return RemoteSkillSource{}, errors.New("URL 不能为空")
	}
	segments := splitURLPath(parsed.Path)
	if len(segments) > 0 && strings.EqualFold(segments[0], "s") {
		segments = segments[1:]
	}
	if len(segments) > 0 && strings.EqualFold(segments[0], "p") {
		return RemoteSkillSource{}, errors.New("skills.sh Pack 暂不支持；请安装单个 Skill 详情页")
	}
	if len(segments) != 3 {
		return RemoteSkillSource{}, errors.New(
			"skills.sh 地址必须是单个 GitHub Skill 详情页，例如 https://skills.sh/owner/repo/skill",
		)
	}

	owner, err := normalizeRepositoryPathSegment(segments[0], "GitHub", "owner")
	if err != nil {
		return RemoteSkillSource{}, err
	}
	repository, err := normalizeRepositoryPathSegment(segments[1], "GitHub", "repository")
	if err != nil {
		return RemoteSkillSource{}, err
	}
	skillName, err := normalizeSkillName(segments[2])
	if err != nil {
		return RemoteSkillSource{}, fmt.Errorf("skills.sh Skill slug 无效: %w", err)
	}

	return RemoteSkillSource{
		DownloadURL: &url.URL{
			Scheme: "https",
			Host:   "api.github.com",
			Path:   "/repos/" + owner + "/" + repository + "/zipball",
		},
		OriginalURL: parsed.String(),
		SkillPath:   explicitSkillPath,
		SkillName:   skillName,
		Provider:    "skills.sh",
		Metadata: map[string]string{
			"repository_provider": "github",
			"repository_owner":    owner,
			"repository_name":     repository,
			"skill_slug":          skillName,
		},
	}, nil
}

type githubSourceResolver struct{}

func (githubSourceResolver) Name() string { return "github" }

func (githubSourceResolver) Priority() int { return repositoryResolverPriority }

func (githubSourceResolver) Match(parsed *url.URL) bool {
	if parsed == nil || !strings.EqualFold(parsed.Hostname(), "github.com") {
		return false
	}
	segments := splitURLPath(parsed.Path)
	if len(segments) < 2 {
		return true
	}
	// GitHub 已经明确给出的 ZIP/Archive URL 应进入 Direct ZIP Resolver。
	return !looksLikeArchivePath(parsed.Path)
}

func (githubSourceResolver) Resolve(
	parsed *url.URL,
	explicitSkillPath string,
) (RemoteSkillSource, error) {
	segments := splitURLPath(parsed.Path)
	if len(segments) < 2 {
		return RemoteSkillSource{}, errors.New("GitHub 地址至少需要 owner/repository")
	}

	owner, err := normalizeRepositoryPathSegment(segments[0], "GitHub", "owner")
	if err != nil {
		return RemoteSkillSource{}, err
	}
	repositorySegment := strings.TrimSuffix(segments[1], ".git")
	repository, err := normalizeRepositoryPathSegment(repositorySegment, "GitHub", "repository")
	if err != nil {
		return RemoteSkillSource{}, err
	}

	ref := ""
	pathFromURL := ""
	switch {
	case len(segments) == 2:
		// zipball 不指定 ref 时由 GitHub 使用仓库默认分支。
	case len(segments) >= 4 && segments[2] == "tree":
		ref = strings.TrimSpace(segments[3])
		if ref == "" || ref == "." || ref == ".." {
			return RemoteSkillSource{}, errors.New("GitHub tree 地址缺少合法 ref")
		}
		if len(segments) > 4 {
			pathFromURL = strings.Join(segments[4:], "/")
		}
	default:
		return RemoteSkillSource{}, errors.New(
			"GitHub Skill 地址只支持仓库根地址、/tree/<ref>/<path> 或明确 ZIP URL",
		)
	}

	skillPath := explicitSkillPath
	if skillPath == "" && pathFromURL != "" {
		skillPath, err = normalizeRemoteSkillPath(pathFromURL)
		if err != nil {
			return RemoteSkillSource{}, err
		}
	}

	downloadURL := &url.URL{
		Scheme: "https",
		Host:   "api.github.com",
		Path:   "/repos/" + owner + "/" + repository + "/zipball",
	}
	// Git 是 GitHub Repository 的主 transport。只有机器没有 Git 或 Git 获取失败时才会
	// 使用这里的 Archive fallback。对于已经明确 ref 的 /tree/<ref>/... 地址，直接使用
	// codeload，不再消耗 GitHub REST API quota；仓库根地址由于不知道默认分支，仍保留
	// zipball fallback。
	if ref != "" {
		downloadURL = &url.URL{
			Scheme: "https",
			Host:   "codeload.github.com",
			Path:   "/" + owner + "/" + repository + "/zip/" + ref,
		}
	}
	return RemoteSkillSource{
		DownloadURL: downloadURL,
		OriginalURL: parsed.String(),
		SkillPath:   skillPath,
		Provider:    "github",
		Metadata: map[string]string{
			"repository_owner": owner,
			"repository_name":  repository,
			"ref":              ref,
		},
	}, nil
}

type gitLabSourceResolver struct {
	hosts map[string]struct{}
}

func (r gitLabSourceResolver) Name() string {
	if len(r.hosts) == 1 {
		for host := range r.hosts {
			if host != "gitlab.com" {
				return "gitlab:" + host
			}
		}
	}
	return "gitlab"
}

func (gitLabSourceResolver) Priority() int { return repositoryResolverPriority }

func (r gitLabSourceResolver) Match(parsed *url.URL) bool {
	if parsed == nil {
		return false
	}
	if _, ok := r.hosts[strings.ToLower(parsed.Hostname())]; !ok {
		return false
	}
	if looksLikeArchivePath(parsed.Path) {
		return false
	}
	segments := splitURLPath(parsed.Path)
	if len(segments) >= 2 {
		for i := 0; i+1 < len(segments); i++ {
			if segments[i] == "-" && segments[i+1] == "archive" {
				return false
			}
		}
	}
	return true
}

func (r gitLabSourceResolver) Resolve(
	parsed *url.URL,
	explicitSkillPath string,
) (RemoteSkillSource, error) {
	segments := splitURLPath(parsed.Path)
	if len(segments) < 2 {
		return RemoteSkillSource{}, errors.New("GitLab 地址至少需要 namespace/project")
	}

	marker := -1
	for i := 0; i < len(segments); i++ {
		if segments[i] == "-" {
			marker = i
			break
		}
	}
	projectSegments := segments
	operationSegments := []string(nil)
	if marker >= 0 {
		projectSegments = segments[:marker]
		operationSegments = segments[marker+1:]
	}
	if len(projectSegments) < 2 {
		return RemoteSkillSource{}, errors.New("GitLab 地址缺少合法 namespace/project")
	}
	for i, segment := range projectSegments {
		label := "namespace"
		if i == len(projectSegments)-1 {
			label = "project"
		}
		value := strings.TrimSuffix(segment, ".git")
		if _, err := normalizeRepositoryPathSegment(value, "GitLab", label); err != nil {
			return RemoteSkillSource{}, err
		}
		projectSegments[i] = value
	}

	ref := ""
	pathFromURL := ""
	switch {
	case len(operationSegments) == 0:
		// 默认分支。
	case len(operationSegments) >= 2 && operationSegments[0] == "tree":
		ref = strings.TrimSpace(operationSegments[1])
		if ref == "" || ref == "." || ref == ".." {
			return RemoteSkillSource{}, errors.New("GitLab tree 地址缺少合法 ref")
		}
		if len(operationSegments) > 2 {
			pathFromURL = strings.Join(operationSegments[2:], "/")
		}
	default:
		return RemoteSkillSource{}, errors.New(
			"GitLab Skill 地址只支持项目根地址、/-/tree/<ref>/<path> 或明确 ZIP URL",
		)
	}

	skillPath := explicitSkillPath
	var err error
	if skillPath == "" && pathFromURL != "" {
		skillPath, err = normalizeRemoteSkillPath(pathFromURL)
		if err != nil {
			return RemoteSkillSource{}, err
		}
	}

	projectPath := strings.Join(projectSegments, "/")
	plainPath := "/api/v4/projects/" + projectPath + "/repository/archive.zip"
	rawPath := "/api/v4/projects/" + url.PathEscape(projectPath) + "/repository/archive.zip"
	downloadURL := &url.URL{
		Scheme:  "https",
		Host:    parsed.Host,
		Path:    plainPath,
		RawPath: rawPath,
	}
	if ref != "" {
		query := downloadURL.Query()
		query.Set("sha", ref)
		downloadURL.RawQuery = query.Encode()
	}

	return RemoteSkillSource{
		DownloadURL: downloadURL,
		OriginalURL: parsed.String(),
		SkillPath:   skillPath,
		Provider:    "gitlab",
		Metadata: map[string]string{
			"repository_host": parsed.Hostname(),
			"repository_path": projectPath,
			"ref":             ref,
		},
	}, nil
}

type giteeSourceResolver struct{}

func (giteeSourceResolver) Name() string { return "gitee" }

func (giteeSourceResolver) Priority() int { return repositoryResolverPriority }

func (giteeSourceResolver) Match(parsed *url.URL) bool {
	if parsed == nil || !strings.EqualFold(parsed.Hostname(), "gitee.com") {
		return false
	}
	if looksLikeArchivePath(parsed.Path) {
		return false
	}
	segments := splitURLPath(parsed.Path)
	if len(segments) >= 4 && segments[2] == "repository" && segments[3] == "archive" {
		return false
	}
	return true
}

func (giteeSourceResolver) Resolve(
	parsed *url.URL,
	explicitSkillPath string,
) (RemoteSkillSource, error) {
	segments := splitURLPath(parsed.Path)
	if len(segments) < 2 {
		return RemoteSkillSource{}, errors.New("Gitee 地址至少需要 owner/repository")
	}
	owner, err := normalizeRepositoryPathSegment(segments[0], "Gitee", "owner")
	if err != nil {
		return RemoteSkillSource{}, err
	}
	repositorySegment := strings.TrimSuffix(segments[1], ".git")
	repository, err := normalizeRepositoryPathSegment(repositorySegment, "Gitee", "repository")
	if err != nil {
		return RemoteSkillSource{}, err
	}

	ref := ""
	pathFromURL := ""
	switch {
	case len(segments) == 2:
		// zipball ref 为空时使用仓库默认分支。
	case len(segments) >= 4 && segments[2] == "tree":
		ref = strings.TrimSpace(segments[3])
		if ref == "" || ref == "." || ref == ".." {
			return RemoteSkillSource{}, errors.New("Gitee tree 地址缺少合法 ref")
		}
		if len(segments) > 4 {
			pathFromURL = strings.Join(segments[4:], "/")
		}
	default:
		return RemoteSkillSource{}, errors.New(
			"Gitee Skill 地址只支持仓库根地址、/tree/<ref>/<path> 或明确 ZIP URL",
		)
	}

	skillPath := explicitSkillPath
	if skillPath == "" && pathFromURL != "" {
		skillPath, err = normalizeRemoteSkillPath(pathFromURL)
		if err != nil {
			return RemoteSkillSource{}, err
		}
	}

	downloadURL := &url.URL{
		Scheme: "https",
		Host:   "gitee.com",
		Path:   "/api/v5/repos/" + owner + "/" + repository + "/zipball",
	}
	if ref != "" {
		query := downloadURL.Query()
		query.Set("ref", ref)
		downloadURL.RawQuery = query.Encode()
	}
	return RemoteSkillSource{
		DownloadURL: downloadURL,
		OriginalURL: parsed.String(),
		SkillPath:   skillPath,
		Provider:    "gitee",
		Metadata: map[string]string{
			"repository_owner": owner,
			"repository_name":  repository,
			"ref":              ref,
		},
	}, nil
}

type directArchiveSourceResolver struct{}

func (directArchiveSourceResolver) Name() string { return "https_zip" }

func (directArchiveSourceResolver) Priority() int { return directArchiveResolverPriority }

func (directArchiveSourceResolver) Match(parsed *url.URL) bool { return parsed != nil }

func (directArchiveSourceResolver) Resolve(
	parsed *url.URL,
	explicitSkillPath string,
) (RemoteSkillSource, error) {
	if parsed == nil {
		return RemoteSkillSource{}, errors.New("URL 不能为空")
	}
	return RemoteSkillSource{
		DownloadURL: cloneURL(parsed),
		OriginalURL: parsed.String(),
		SkillPath:   explicitSkillPath,
		Provider:    "https_zip",
		Metadata: map[string]string{
			"source_host": parsed.Hostname(),
		},
	}, nil
}

func looksLikeArchivePath(value string) bool {
	lower := strings.ToLower(strings.TrimSpace(value))
	for _, suffix := range []string{".zip", ".tar", ".tar.gz", ".tgz", ".tar.bz2", ".tbz2"} {
		if strings.HasSuffix(lower, suffix) {
			return true
		}
	}
	return false
}
