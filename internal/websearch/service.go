// Package websearch 提供独立的网页检索组件；不依赖桌面、Agent、权限或会话实现。
package websearch

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

const (
	webSearchProviderAuto          = "auto"
	webSearchProviderAnySearchFree = "anysearch_free"
	webSearchProviderBing          = "bing"
	webSearchProviderDuckDuckGo    = "duckduckgo"

	maxWebSearchQueryBytes  = 8 * 1024
	maxWebSearchDomains     = 64
	maxWebSearchDomainBytes = 253
)

var searchWhitespacePattern = regexp.MustCompile(`\s+`)

// Input 是 web_search 的模型输入。
type Input struct {
	Query          string   `json:"query" jsonschema:"description=Search keywords. For time-sensitive information include the concrete subject; the runtime already provides the current date in the system prompt."`
	Count          int      `json:"count,omitempty" jsonschema:"description=Desired result count. Omit to use the configured default."`
	AllowedDomains []string `json:"allowed_domains,omitempty" jsonschema:"description=Only include these domains and their subdomains. Use this when the user requests a specific official site."`
	BlockedDomains []string `json:"blocked_domains,omitempty" jsonschema:"description=Exclude these domains and their subdomains."`
}

// Result 是一个规范化搜索结果。
type Result struct {
	Title       string `json:"title"`
	URL         string `json:"url"`
	Snippet     string `json:"snippet,omitempty"`
	PublishedAt string `json:"published_at,omitempty"`
}

// Attempt 描述 auto Provider Chain 中一次内部尝试。
//
// 一次模型 ToolCall 内部可能自动尝试多个 Provider，但 UI 仍然只显示一次
// web_search。Provider fallback 是 Tool 内部可靠性职责，
// 而不是让模型不断重复调用搜索工具。
type Attempt struct {
	Provider    string `json:"provider"`
	Status      string `json:"status"`
	ResultCount int    `json:"result_count,omitempty"`
	ErrorType   string `json:"error_type,omitempty"`
}

// Diagnostics 是供模型和开发调试使用的非敏感诊断信息。
type Diagnostics struct {
	Strategy       string    `json:"strategy"`
	SelectedStatus string    `json:"selected_status,omitempty"`
	Attempts       []Attempt `json:"attempts,omitempty"`
}

// Output 是 web_search 的结构化返回。
type Output struct {
	Query       string      `json:"query"`
	Provider    string      `json:"provider"`
	Results     []Result    `json:"results"`
	Diagnostics Diagnostics `json:"diagnostics"`
}

// Backend 与公开查询结果共用字段定义，避免重复转换相同的 DTO。
type searchResult = Result

// Service 负责查询、质量判断、域名过滤与降级，不持有 Agent 或 Session 状态。
type Service struct {
	backends       []Backend
	client         *http.Client
	provider       string
	defaultResults int
	maxResults     int
}

// Option 只在构造期间生效，运行中的请求不会重新读取后端清单。
type Option func(*Service)

// WithBackends 替换有序降级链。Backend 由调用方拥有，可以通过供应商 SDK 实现。
// 不传此选项时保留 AnySearch → Bing → DuckDuckGo 的既有行为。
func WithBackends(backends ...Backend) Option {
	frozen := append([]Backend(nil), backends...)
	return func(service *Service) { service.backends = append([]Backend(nil), frozen...) }
}

// New 创建可独立使用的检索组件；网络策略与工具审批由宿主适配层负责。
func New(provider string, timeout time.Duration, defaultResults, maxResults int, options ...Option) (*Service, error) {
	if timeout <= 0 {
		return nil, errors.New("WebSearch Timeout 必须大于 0")
	}
	if defaultResults <= 0 {
		return nil, errors.New("WebSearch DefaultResults 必须大于 0")
	}
	if maxResults <= 0 || defaultResults > maxResults {
		return nil, errors.New("WebSearch MaxResults 无效")
	}
	service := &Service{client: &http.Client{Timeout: timeout}, provider: normalizeSearchProvider(provider), defaultResults: defaultResults, maxResults: maxResults,
		backends: []Backend{anySearchBackend{}, bingBackend{}, duckDuckGoBackend{}}}
	for _, option := range options {
		if option != nil {
			option(service)
		}
	}
	if len(service.backends) == 0 {
		return nil, errors.New("WebSearch 至少需要一个 Backend")
	}
	seen := make(map[string]bool, len(service.backends))
	for _, backend := range service.backends {
		if backend == nil {
			return nil, errors.New("WebSearch Backend 不能为空")
		}
		name := backend.Name()
		if name == "" || name == webSearchProviderAuto || name != normalizeSearchProvider(name) || seen[name] {
			return nil, fmt.Errorf("WebSearch Backend 名称无效或重复: %q", name)
		}
		seen[name] = true
	}
	if service.provider != webSearchProviderAuto {
		if _, err := service.backendFor(service.provider); err != nil {
			return nil, err
		}
	}
	return service, nil
}

func (f *Service) Search(ctx context.Context, input *Input) (*Output, error) {
	if ctx == nil {
		return nil, errors.New("web_search: context.Context 不能为空")
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("web_search 被取消: %w", err)
	}
	if input == nil {
		return nil, errors.New("web_search 输入不能为空")
	}
	query := searchWhitespacePattern.ReplaceAllString(strings.TrimSpace(input.Query), " ")
	if query == "" {
		return nil, errors.New("web_search query 不能为空")
	}
	if len(query) > maxWebSearchQueryBytes {
		return nil, fmt.Errorf("web_search query 超过 %d bytes 安全上限", maxWebSearchQueryBytes)
	}
	if err := validateSearchDomains(input.AllowedDomains); err != nil {
		return nil, fmt.Errorf("web_search allowed_domains 无效: %w", err)
	}
	if err := validateSearchDomains(input.BlockedDomains); err != nil {
		return nil, fmt.Errorf("web_search blocked_domains 无效: %w", err)
	}
	count := input.Count
	if count <= 0 {
		count = f.defaultResults
	}
	if count > f.maxResults {
		count = f.maxResults
	}
	fetchCount := count
	if len(input.AllowedDomains) > 0 || len(input.BlockedDomains) > 0 {
		fetchCount = count * 3
		if fetchCount > f.maxResults {
			fetchCount = f.maxResults
		}
	}
	if f.provider == webSearchProviderAuto {
		return f.runAuto(ctx, query, count, fetchCount, input.AllowedDomains, input.BlockedDomains)
	}
	backend, err := f.backendFor(f.provider)
	if err != nil {
		return nil, err
	}
	results, err := backend.Search(ctx, f.client, query, fetchCount)
	if canceled := ctx.Err(); canceled != nil {
		return nil, fmt.Errorf("web_search 被取消: %w", canceled)
	}
	if err != nil {
		return nil, fmt.Errorf("web_search %s 后端失败: %w", backend.Name(), err)
	}
	results = normalizeAndFilterSearchResults(results, input.AllowedDomains, input.BlockedDomains)
	if len(results) > count {
		results = results[:count]
	}
	return &Output{Query: query, Provider: backend.Name(), Results: results,
		Diagnostics: Diagnostics{Strategy: f.provider, Attempts: []Attempt{{Provider: backend.Name(), Status: searchAttemptStatus(results, query), ResultCount: len(results)}}}}, nil
}

// runAuto 在一次 ToolCall 内完成 Provider fallback。
func (f *Service) runAuto(ctx context.Context, query string, count int, fetchCount int, allowed []string, blocked []string) (*Output, error) {
	attempts := make([]Attempt, 0, 8)
	var firstLowQuality []searchResult
	firstLowQualityProvider := ""
	for _, backend := range f.backends {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("web_search auto 被取消: %w", err)
		}
		results, err := backend.Search(ctx, f.client, query, fetchCount)
		// 最后一个后端返回时也要检查取消，避免被包装成 all_failed 或低质量结果。
		if canceled := ctx.Err(); canceled != nil {
			return nil, fmt.Errorf("web_search auto 被取消: %w", canceled)
		}
		if err != nil {
			attempts = append(attempts, Attempt{Provider: backend.Name(), Status: "error", ErrorType: classifySearchError(err)})
			continue
		}
		results = normalizeAndFilterSearchResults(results, allowed, blocked)
		status := searchAttemptStatus(results, query)
		attempts = append(attempts, Attempt{Provider: backend.Name(), Status: status, ResultCount: len(results)})
		switch status {
		case "empty":
			continue
		case "low_quality":
			if firstLowQuality == nil {
				firstLowQuality = append([]searchResult(nil), results...)
				firstLowQualityProvider = backend.Name()
			}
			continue
		default:
			if len(results) > count {
				results = results[:count]
			}
			return &Output{Query: query, Provider: backend.Name(), Results: results,
				Diagnostics: Diagnostics{Strategy: webSearchProviderAuto, SelectedStatus: "ok", Attempts: attempts}}, nil
		}
	}
	if firstLowQuality != nil {
		if len(firstLowQuality) > count {
			firstLowQuality = firstLowQuality[:count]
		}
		return &Output{Query: query, Provider: firstLowQualityProvider, Results: firstLowQuality,
			Diagnostics: Diagnostics{Strategy: webSearchProviderAuto, SelectedStatus: "low_quality", Attempts: attempts}}, nil
	}
	return &Output{Query: query, Provider: webSearchProviderAuto, Results: []Result{}, Diagnostics: Diagnostics{Strategy: webSearchProviderAuto, SelectedStatus: "all_failed", Attempts: attempts}}, nil
}

// backendFor 查找构造时冻结的后端集合，新增实现无需修改查询流程。
func (f *Service) backendFor(provider string) (Backend, error) {
	for _, backend := range f.backends {
		if backend.Name() == provider {
			return backend, nil
		}
	}
	return nil, fmt.Errorf("WebSearch Provider 不支持: %q", provider)
}

func normalizeSearchProvider(provider string) string {
	provider = strings.ToLower(strings.TrimSpace(provider))
	if provider == "" {
		return webSearchProviderAuto
	}
	return provider
}

func searchAttemptStatus(results []searchResult, query string) string {
	if len(results) == 0 {
		return "empty"
	}
	if isLikelyLowQualityResults(query, results) {
		return "low_quality"
	}
	return "ok"
}

func classifySearchError(err error) string {
	if err == nil {
		return ""
	}
	text := strings.ToLower(err.Error())
	switch {
	case strings.Contains(text, "429"),
		strings.Contains(text, "402"),
		strings.Contains(text, "rate limit"),
		strings.Contains(text, "quota"):
		return "rate_limited"
	case strings.Contains(text, "401"),
		strings.Contains(text, "403"),
		strings.Contains(text, "unauthorized"),
		strings.Contains(text, "forbidden"),
		strings.Contains(text, "api key"):
		return "auth"
	case strings.Contains(text, "captcha"),
		strings.Contains(text, "blocked"):
		return "blocked"
	default:
		return "error"
	}
}

// isLikelyLowQualityResults 当前搜索链的质量门槛。
//
// 中文搜索引擎偶尔会把一个多词查询错误拆成字典/百科解释。若前三条结果大多是
// “汉典/词典/基本解释”类页面，且几乎没有覆盖查询中的中文词组，就把本 Provider
// 判为 low_quality 并继续 fallback，而不是把错误结果交给模型继续编故事。
func isLikelyLowQualityResults(query string, results []searchResult) bool {
	if !containsCJK(query) || len(results) == 0 {
		return false
	}
	terms := cjkQualityTerms(query)
	if len(terms) < 2 {
		return false
	}
	topCount := 3
	if len(results) < topCount {
		topCount = len(results)
	}
	top := results[:topCount]
	var combined strings.Builder
	dictionaryCount := 0
	for _, result := range top {
		text := normalizeQualityText(result.Title + " " + result.URL + " " + result.Snippet)
		combined.WriteString(text)
		combined.WriteByte(' ')
		if looksLikeDictionaryResult(text) {
			dictionaryCount++
		}
	}
	normalizedTop := combined.String()
	matched := 0
	for _, term := range terms {
		if strings.Contains(normalizedTop, term) {
			matched++
		}
	}
	return dictionaryCount >= min(2, topCount) && matched <= 1
}

func containsCJK(value string) bool {
	for _, r := range value {
		if r >= '\u3400' && r <= '\u9fff' {
			return true
		}
	}
	return false
}

func cjkQualityTerms(query string) []string {
	fields := strings.Fields(query)
	terms := make([]string, 0, len(fields))
	seen := make(map[string]struct{}, len(fields))
	for _, field := range fields {
		term := normalizeQualityText(field)
		if len([]rune(term)) < 2 || !containsCJK(term) {
			continue
		}
		if _, exists := seen[term]; exists {
			continue
		}
		seen[term] = struct{}{}
		terms = append(terms, term)
	}
	return terms
}

func normalizeQualityText(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	replacer :=
		strings.NewReplacer(
			" ",
			"",
			"\t",
			"",
			"\n",
			"",
			"\r",
			"",
			"，",
			"",
			"。",
			"",
			"、",
			"",
			"“",
			"",
			"”",
			"",
			"‘",
			"",
			"’",
			"",
			"：",
			"",
			"；",
			"",
			"？",
			"",
			"！",
			"",
			"（",
			"",
			"）",
			"",
			"(",
			"",
			")",
			"",
			"|",
			"",
			"_",
			"",
			"-",
			"",
			"—",
			"",
			":",
			"",
			";",
			"",
			",",
			"",
			".",
			"",
			"!",
			"",
			"?",
			"",
			"/",
			"",
			"\\",
			"",
		)
	return replacer.Replace(value)
}

func looksLikeDictionaryResult(normalizedText string) bool {
	markers := []string{"汉语文字", "汉典", "字典", "词典", "基本解释", "百度百科", "hanyu", "zdic", "zidian", "cidian"}
	for _, marker := range markers {
		if strings.Contains(normalizedText, marker) {
			return true
		}
	}
	return false
}

func normalizeAndFilterSearchResults(results []searchResult, allowed []string, blocked []string) []searchResult {
	output := make([]searchResult, 0, len(results))
	seen := make(map[string]struct{}, len(results))
	for _, result := range results {
		result.Title = strings.TrimSpace(result.Title)
		result.URL = strings.TrimSpace(result.URL)
		result.Snippet = strings.TrimSpace(result.Snippet)
		result.PublishedAt = strings.TrimSpace(result.PublishedAt)
		if result.Title == "" || result.URL == "" {
			continue
		}
		parsed, err := url.Parse(result.URL)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" {
			continue
		}
		parsed.Fragment = ""
		result.URL = parsed.String()
		host := strings.ToLower(parsed.Hostname())
		if len(allowed) > 0 && !matchesAnyDomain(host, allowed) {
			continue
		}
		if matchesAnyDomain(host, blocked) {
			continue
		}
		dedupeKey := strings.ToLower(result.URL)
		if _, exists := seen[dedupeKey]; exists {
			continue
		}
		seen[dedupeKey] = struct{}{}
		output = append(output, result)
	}
	return output
}

func matchesAnyDomain(host string, domains []string) bool {
	if host == "" {
		return false
	}
	for _, domain := range domains {
		domain = strings.ToLower(strings.TrimSpace(domain))
		if domain == "" {
			continue
		}
		if host == domain || strings.HasSuffix(host, "."+domain) {
			return true
		}
	}
	return false
}

func validateSearchDomains(domains []string) error {
	if len(domains) > maxWebSearchDomains {
		return fmt.Errorf("域名数量 %d 超过最大值 %d", len(domains), maxWebSearchDomains)
	}
	for index, domain := range domains {
		domain = strings.ToLower(strings.TrimSpace(domain))
		if domain == "" {
			continue
		}
		if len(domain) > maxWebSearchDomainBytes {
			return fmt.Errorf("第 %d 个域名超过 %d bytes", index+1, maxWebSearchDomainBytes)
		}
		if strings.ContainsAny(domain, `/\?#@:`) {
			return fmt.Errorf("第 %d 个值必须是纯域名，不能包含 URL 路径、端口或协议", index+1)
		}
	}
	return nil
}
