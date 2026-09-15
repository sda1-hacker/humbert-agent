package skills

import (
	"errors"
	"fmt"
	"net/url"
	"path"
	"sort"
	"strings"
	"sync"
)

const directArchiveResolverPriority = -1000

// RemoteSkillSource 是来源解析完成后的统一远程 Skill 描述。
//
// Resolver 只负责把“展示页 / 仓库页 / 直接归档地址”规范化为一个最终下载地址和可选的
// Skill 定位提示，不负责发起网络请求。DownloadURL 后续仍必须经过 Humbert 的 SSRF、
// Redirect、大小限制、ZIP 与 Package 校验。
//
// OriginalURL 保留用户实际提供的地址，Metadata 保存来源特有的非敏感定位信息。安装成功后
// Manager 会把这份来源描述写入 Humbert 自己的 .humbert-skill-sources.json，供 Update /
// Reinstall 复用；它始终位于 Skill Package 外部，不会污染第三方内容或改变 Package Identity。
type RemoteSkillSource struct {
	DownloadURL *url.URL

	OriginalURL string

	SkillPath string

	SkillName string

	Provider string

	Metadata map[string]string
}

// Clone 返回不共享 URL/Metadata 可变对象的副本。
func (s RemoteSkillSource) Clone() RemoteSkillSource {
	cloned := s
	cloned.DownloadURL = cloneURL(s.DownloadURL)
	if len(s.Metadata) > 0 {
		cloned.Metadata = make(map[string]string, len(s.Metadata))
		for key, value := range s.Metadata {
			cloned.Metadata[key] = value
		}
	}
	return cloned
}

// RemoteSkillSourceResolver 是可插拔的远程 Skill 来源适配器。
//
// Resolver 必须是纯 URL 解析器：不得自行发起网络请求、读取本地文件或执行命令。这样所有
// Provider 无论来自 GitHub、GitLab、Gitee、Skill Registry 还是企业内部页面，最终下载都
// 会统一进入 Manager 的安全 Downloader。
//
// Priority 越大越优先。Direct HTTPS ZIP 使用 -1000 作为兜底，因此第三方 Resolver
// 通常应使用大于 -1000 的优先级。若同一 URL 同时匹配两个同优先级 Resolver，Registry 会
// 明确报冲突，而不是依赖注册顺序产生不确定行为。
type RemoteSkillSourceResolver interface {
	Name() string

	Priority() int

	Match(parsed *url.URL) bool

	Resolve(parsed *url.URL, explicitSkillPath string) (RemoteSkillSource, error)
}

type registeredRemoteSkillSourceResolver struct {
	resolver RemoteSkillSourceResolver
	name     string
	priority int
}

// RemoteSkillSourceRegistry 管理 Remote Skill Source Resolver。
//
// Registry 支持运行时注册自定义 Provider，同时对 Resolve 使用快照，因此设置页安装与 Agent
// install_skill 并发执行时不会持有 Registry 锁进入 Resolver 代码。
type RemoteSkillSourceRegistry struct {
	mu sync.RWMutex

	resolvers []registeredRemoteSkillSourceResolver
}

// NewRemoteSkillSourceRegistry 创建空 Registry。
func NewRemoteSkillSourceRegistry() *RemoteSkillSourceRegistry {
	return &RemoteSkillSourceRegistry{}
}

// NewDefaultRemoteSkillSourceRegistry 创建 Humbert 默认 Provider Registry。
//
// 默认支持：skills.sh、GitHub、GitLab.com、Gitee，以及任意公开 HTTPS ZIP URL。
// Installer 主流程不识别任何 Provider 细节；以后增加来源只需要注册新的 Resolver。
func NewDefaultRemoteSkillSourceRegistry() (*RemoteSkillSourceRegistry, error) {
	registry := NewRemoteSkillSourceRegistry()
	for _, resolver := range []RemoteSkillSourceResolver{
		NewSkillsShSourceResolver(),
		NewGitHubSourceResolver(),
		NewGitLabSourceResolver("gitlab.com"),
		NewGiteeSourceResolver(),
		NewDirectArchiveSourceResolver(),
	} {
		if err := registry.Register(resolver); err != nil {
			return nil, err
		}
	}
	return registry, nil
}

// Register 注册一个新的来源 Resolver。
func (r *RemoteSkillSourceRegistry) Register(resolver RemoteSkillSourceResolver) error {
	if r == nil {
		return errors.New("Remote Skill Source Registry 不能为空")
	}
	if resolver == nil {
		return errors.New("Remote Skill Source Resolver 不能为空")
	}
	name := strings.TrimSpace(resolver.Name())
	if name == "" {
		return errors.New("Remote Skill Source Resolver 名称不能为空")
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	for _, existing := range r.resolvers {
		if strings.EqualFold(existing.name, name) {
			return fmt.Errorf("Remote Skill Source Resolver %q 已注册", name)
		}
	}

	r.resolvers = append(r.resolvers, registeredRemoteSkillSourceResolver{
		resolver: resolver,
		name:     name,
		priority: resolver.Priority(),
	})
	sort.SliceStable(r.resolvers, func(i, j int) bool {
		if r.resolvers[i].priority != r.resolvers[j].priority {
			return r.resolvers[i].priority > r.resolvers[j].priority
		}
		return r.resolvers[i].name < r.resolvers[j].name
	})
	return nil
}

// Names 返回按解析优先级排序的 Resolver 名称副本。
func (r *RemoteSkillSourceRegistry) Names() []string {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]string, 0, len(r.resolvers))
	for _, entry := range r.resolvers {
		result = append(result, entry.name)
	}
	return result
}

// Resolve 把用户/模型提供的来源地址规范化为统一下载描述。
func (r *RemoteSkillSourceRegistry) Resolve(rawURL string, explicitSkillPath string) (RemoteSkillSource, error) {
	if r == nil {
		return RemoteSkillSource{}, errors.New("Remote Skill Source Registry 不能为空")
	}
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return RemoteSkillSource{}, errors.New("远程 Skill URL 不能为空")
	}

	parsed, err := parsePublicSkillURL(rawURL)
	if err != nil {
		return RemoteSkillSource{}, fmt.Errorf("远程 Skill URL 无效: %w", err)
	}
	skillPath, err := normalizeRemoteSkillPath(explicitSkillPath)
	if err != nil {
		return RemoteSkillSource{}, err
	}

	r.mu.RLock()
	entries := append([]registeredRemoteSkillSourceResolver(nil), r.resolvers...)
	r.mu.RUnlock()
	if len(entries) == 0 {
		return RemoteSkillSource{}, errors.New("没有可用的远程 Skill Source Resolver")
	}

	var (
		matched         []registeredRemoteSkillSourceResolver
		matchedPriority int
		foundMatch      bool
	)
	for _, entry := range entries {
		if !entry.resolver.Match(parsed) {
			continue
		}
		if !foundMatch {
			foundMatch = true
			matchedPriority = entry.priority
		}
		if entry.priority != matchedPriority {
			break
		}
		matched = append(matched, entry)
	}
	if len(matched) == 0 {
		return RemoteSkillSource{}, errors.New("没有 Resolver 能识别该远程 Skill 地址")
	}
	if len(matched) > 1 {
		names := make([]string, 0, len(matched))
		for _, entry := range matched {
			names = append(names, entry.name)
		}
		return RemoteSkillSource{}, fmt.Errorf(
			"远程 Skill Source Resolver 冲突: 优先级 %d 同时匹配 %s",
			matchedPriority,
			strings.Join(names, "、"),
		)
	}

	entry := matched[0]
	source, err := entry.resolver.Resolve(parsed, skillPath)
	if err != nil {
		return RemoteSkillSource{}, fmt.Errorf("%s Resolver 解析失败: %w", entry.name, err)
	}
	source, err = normalizeResolvedRemoteSkillSource(source)
	if err != nil {
		return RemoteSkillSource{}, fmt.Errorf("%s Resolver 返回无效结果: %w", entry.name, err)
	}

	source.Provider = strings.TrimSpace(source.Provider)
	if source.Provider == "" {
		source.Provider = entry.name
	}
	if strings.TrimSpace(source.OriginalURL) == "" {
		source.OriginalURL = parsed.String()
	}
	return source.Clone(), nil
}

func normalizeResolvedRemoteSkillSource(source RemoteSkillSource) (RemoteSkillSource, error) {
	if source.DownloadURL == nil {
		return RemoteSkillSource{}, errors.New("缺少 DownloadURL")
	}
	validated, err := parsePublicSkillURL(source.DownloadURL.String())
	if err != nil {
		return RemoteSkillSource{}, fmt.Errorf("DownloadURL 不安全: %w", err)
	}
	source.DownloadURL = validated

	source.SkillPath, err = normalizeRemoteSkillPath(source.SkillPath)
	if err != nil {
		return RemoteSkillSource{}, err
	}
	if strings.TrimSpace(source.SkillName) != "" {
		source.SkillName, err = normalizeSkillName(source.SkillName)
		if err != nil {
			return RemoteSkillSource{}, fmt.Errorf("SkillName 无效: %w", err)
		}
	}
	return source, nil
}

func cloneURL(value *url.URL) *url.URL {
	if value == nil {
		return nil
	}
	copyValue := *value
	return &copyValue
}

func splitURLPath(value string) []string {
	parts := strings.Split(strings.Trim(value, "/"), "/")
	result := make([]string, 0, len(parts))
	for _, item := range parts {
		item = strings.TrimSpace(item)
		if item != "" {
			result = append(result, item)
		}
	}
	return result
}

func normalizeRepositoryPathSegment(value string, provider string, label string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || value == "." || value == ".." {
		return "", fmt.Errorf("%s Skill 地址缺少合法 %s", provider, label)
	}
	for _, char := range value {
		valid := (char >= 'a' && char <= 'z') ||
			(char >= 'A' && char <= 'Z') ||
			(char >= '0' && char <= '9') ||
			char == '-' || char == '_' || char == '.'
		if !valid {
			return "", fmt.Errorf("%s %s %q 包含非法字符 %q", provider, label, value, char)
		}
	}
	return value, nil
}

func normalizeRemoteSkillPath(value string) (string, error) {
	value = strings.TrimSpace(strings.ReplaceAll(value, "\\", "/"))
	if value == "" || value == "." {
		return "", nil
	}
	if strings.HasPrefix(value, "/") {
		return "", errors.New("skill_path 必须是 ZIP 内的相对目录")
	}
	cleaned := path.Clean(value)
	if cleaned == "." {
		return "", nil
	}
	if cleaned == ".." || strings.HasPrefix(cleaned, "../") || strings.Contains(cleaned, ":") {
		return "", errors.New("skill_path 不能越过 ZIP 根目录")
	}
	return cleaned, nil
}

func parsePublicSkillURL(raw string) (*url.URL, error) {
	parsed, err := url.Parse(raw)
	if err != nil {
		return nil, err
	}
	if !strings.EqualFold(parsed.Scheme, "https") {
		return nil, errors.New("远程 Skill 只允许 HTTPS URL")
	}
	if parsed.Hostname() == "" {
		return nil, errors.New("URL 缺少 Host")
	}
	if parsed.User != nil {
		return nil, errors.New("URL 不能包含 userinfo 凭据")
	}
	if parsed.Port() != "" && parsed.Port() != "443" {
		return nil, errors.New("远程 Skill 只允许标准 HTTPS 443 端口")
	}
	return parsed, nil
}
