package websearch

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"golang.org/x/net/html"
)

const (
	searchBodyLimit = 4 * 1024 * 1024
	anySearchURL    = "https://api.anysearch.com/v1/search"
)

// Backend 是可独立替换的检索来源，SDK 只需适配这个小接口。
//
// Backend 只负责请求 Provider 并转换为统一 Result。质量判断、域名过滤、去重和降级
// 由 Service 处理，Eino Tool Schema 与宿主权限由独立适配层处理。
// Name 在实例生命周期内必须稳定；实现应支持并发调用并遵守传入 Context 的取消。
// 复用传入的 HTTP Client 可以保持组件超时；自行使用 SDK 时也要配置相应超时。
type Backend interface {
	Name() string
	Search(ctx context.Context, client *http.Client, query string, count int) ([]Result, error)
}

// anySearchBackend 调用匿名搜索端点；失败时由上层回退到其它可用来源。
type anySearchBackend struct{}

func (anySearchBackend) Name() string { return webSearchProviderAnySearchFree }

func (b anySearchBackend) Search(ctx context.Context, client *http.Client, query string, count int) ([]searchResult, error) {
	requestCount := count
	if requestCount < 1 {
		requestCount = 1
	}
	// AnySearch 官方 /v1/search 当前约束 max_results 为 1~20。即使 Humbert
	// 未来把全局搜索上限调高，也不能把非法数量透传给 Provider。
	if requestCount > 20 {
		requestCount = 20
	}
	requestBody := map[string]any{"query": query, "max_results": requestCount, "language": searchLanguage(query)}
	if containsCJK(query) {
		// 中文实时信息优先请求中国区结果。AnySearch 会继续负责底层数据源路由、
		// 合并和排序，Humbert 不需要知道具体搜索引擎。
		requestBody["zone"] = "cn"
	} else {
		requestBody["zone"] = "intl"
	}
	encodedBody, err := json.Marshal(requestBody)
	if err != nil {
		return nil, fmt.Errorf("编码 AnySearch 请求失败: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, anySearchURL, bytes.NewReader(encodedBody))
	if err != nil {
		return nil, fmt.Errorf("创建 AnySearch 请求失败: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("请求 AnySearch 失败: %w", err)
	}
	defer response.Body.Close()
	body, err := readSearchResponseBody(response.Body)
	if err != nil {
		return nil, fmt.Errorf("读取 AnySearch 响应失败: %w", err)
	}
	// AnySearch 匿名额度耗尽时，402 响应的 data 可能包含自动注册的账号或 API Key。
	// Humbert 绝不能把这类上游敏感字段拼进 Tool Error、日志或模型上下文。
	// 因此 AnySearch 的非 2xx 错误只解析顶层 message，并按 HTTP Status 输出
	// 有界、非敏感的诊断，不回显原始 Response Body。
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, anySearchHTTPError(response.StatusCode, body)
	}
	var decoded anySearchEnvelope
	if err := json.Unmarshal(body, &decoded); err != nil {
		return nil, fmt.Errorf("解析 AnySearch JSON 失败: %w", err)
	}
	if decoded.Code != 0 {
		message := strings.TrimSpace(decoded.Message)
		if message == "" {
			message = fmt.Sprintf("code %d", decoded.Code)
		}
		return nil, fmt.Errorf("AnySearch API 返回错误: %s", message)
	}
	items := decoded.Data.Results
	if len(items) == 0 {
		items = decoded.Results
	}
	results := make([]searchResult, 0, min(len(items), requestCount))
	for _, item := range items {
		snippet := strings.TrimSpace(item.Content)
		if snippet == "" {
			snippet = strings.TrimSpace(item.Description)
		}
		if snippet == "" {
			snippet = strings.TrimSpace(item.Snippet)
		}
		results = append(results, searchResult{Title: item.Title, URL: item.URL, Snippet: snippet, PublishedAt: item.PublishedAt})
		if len(results) >= requestCount {
			break
		}
	}
	return results, nil
}

func anySearchHTTPError(status int, body []byte) error {
	message := ""
	var decoded struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(body, &decoded); err == nil {
		message = strings.TrimSpace(decoded.Message)
	}
	switch status {
	case http.StatusUnauthorized,
		http.StatusForbidden:
		if message == "" {
			message = "鉴权失败"
		}
		return fmt.Errorf("AnySearch HTTP %d: %s", status, message)
	case http.StatusPaymentRequired:
		return fmt.Errorf("AnySearch HTTP %d: 搜索额度已用尽", status)
	case http.StatusTooManyRequests:
		return fmt.Errorf("AnySearch HTTP %d: 请求频率受限", status)
	default:
		if message == "" {
			message = http.StatusText(status)
		}
		if message == "" {
			message = "请求失败"
		}
		return fmt.Errorf("AnySearch HTTP %d: %s", status, message)
	}
}

type anySearchEnvelope struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    struct {
		Results []anySearchItem `json:"results"`
	} `json:"data"`
	Results []anySearchItem `json:"results"`
}

type anySearchItem struct {
	Title       string `json:"title"`
	URL         string `json:"url"`
	Content     string `json:"content"`
	Description string `json:"description"`
	Snippet     string `json:"snippet"`
	PublishedAt string `json:"published_at"`
}

func searchLanguage(query string) string {
	if containsCJK(query) {
		return "zh-CN"
	}
	return "en"
}

// tavilyBackend 使用 Tavily JSON Search API。
type duckDuckGoBackend struct{}

func (duckDuckGoBackend) Name() string {
	return webSearchProviderDuckDuckGo
}

func (duckDuckGoBackend) Search(ctx context.Context, client *http.Client, query string, count int) ([]searchResult, error) {
	target := "https://html.duckduckgo.com/html/?q=" + url.QueryEscape(query)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, fmt.Errorf("创建 DuckDuckGo 请求失败: %w", err)
	}
	request.Header.Set("User-Agent", "Mozilla/5.0 (compatible; HumbertAgent/0.1)")
	request.Header.Set("Accept", "text/html")
	request.Header.Set("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.8")
	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("请求 DuckDuckGo 失败: %w", err)
	}
	defer response.Body.Close()
	body, err := readSearchResponseBody(response.Body)
	if err != nil {
		return nil, fmt.Errorf("读取 DuckDuckGo 响应失败: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("DuckDuckGo HTTP %d", response.StatusCode)
	}
	results, err := parseDuckDuckGoHTML(body)
	if err != nil {
		return nil, err
	}
	if len(results) > count {
		results = results[:count]
	}
	return results, nil
}

func parseDuckDuckGoHTML(body []byte) ([]searchResult, error) {
	document, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("解析 DuckDuckGo HTML 失败: %w", err)
	}
	var results []searchResult
	var snippets []string
	var walk func(*html.Node)
	walk =
		func(node *html.Node) {
			if node.Type == html.ElementNode && node.Data == "a" && hasClass(node, "result__a") {
				results = append(results, searchResult{Title: nodeText(node), URL: unwrapDDGURL(attr(node, "href"))})
			}
			if node.Type == html.ElementNode && hasClass(node, "result__snippet") {
				snippets = append(snippets, nodeText(node))
			}
			for child := node.FirstChild; child != nil; child = child.NextSibling {
				walk(child)
			}
		}
	walk(document)
	for index := range results {
		if index < len(snippets) {
			results[index].Snippet = snippets[index]
		}
	}
	return results, nil
}

func unwrapDDGURL(href string) string {
	raw := href
	if strings.HasPrefix(raw, "//") {
		raw = "https:" + raw
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return href
	}
	if target := parsed.Query().Get("uddg"); target != "" {
		return target
	}
	return raw
}

// bingBackend 使用 Bing HTML 搜索页，无需 API Key。
type bingBackend struct{}

func (bingBackend) Name() string {
	return webSearchProviderBing
}

func (bingBackend) Search(ctx context.Context, client *http.Client, query string, count int) ([]searchResult, error) {
	target := fmt.Sprintf("https://cn.bing.com/search?q=%s&count=%d&first=1", url.QueryEscape(query), count)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, fmt.Errorf("创建 Bing 请求失败: %w", err)
	}
	request.Header.Set("User-Agent", "Mozilla/5.0 (compatible; HumbertAgent/0.1)")
	request.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	request.Header.Set("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.8")
	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("请求 Bing 失败: %w", err)
	}
	defer response.Body.Close()
	body, err := readSearchResponseBody(response.Body)
	if err != nil {
		return nil, fmt.Errorf("读取 Bing 响应失败: %w", err)
	}
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Bing HTTP %d", response.StatusCode)
	}
	results, err := parseBingHTML(body)
	if err != nil {
		return nil, err
	}
	if len(results) > count {
		results = results[:count]
	}
	return results, nil
}

func parseBingHTML(body []byte) ([]searchResult, error) {
	document, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("解析 Bing HTML 失败: %w", err)
	}
	var results []searchResult
	var walk func(*html.Node)
	walk =
		func(node *html.Node) {
			if node.Type == html.ElementNode && node.Data == "li" && hasClass(node, "b_algo") {
				result := searchResult{}
				var findTitle func(*html.Node)
				findTitle =
					func(current *html.Node) {
						if result.Title != "" {
							return
						}
						if current.Type == html.ElementNode && current.Data == "h2" {
							for child := current.FirstChild; child != nil; child = child.NextSibling {
								if child.Type == html.ElementNode && child.Data == "a" {
									result.Title = nodeText(child)
									result.URL = attr(child, "href")
									return
								}
							}
						}
						for child := current.FirstChild; child != nil; child = child.NextSibling {
							findTitle(child)
						}
					}
				findTitle(node)
				var findSnippet func(*html.Node)
				findSnippet =
					func(current *html.Node) {
						if result.Snippet != "" {
							return
						}
						if current.Type == html.ElementNode && hasClass(current, "b_caption") {
							for child := current.FirstChild; child != nil; child = child.NextSibling {
								if child.Type == html.ElementNode && child.Data == "p" {
									result.Snippet = nodeText(child)
									return
								}
							}
						}
						for child := current.FirstChild; child != nil; child = child.NextSibling {
							findSnippet(child)
						}
					}
				findSnippet(node)
				if result.Title != "" && result.URL != "" {
					results = append(results, result)
				}
				return
			}
			for child := node.FirstChild; child != nil; child = child.NextSibling {
				walk(child)
			}
		}
	walk(document)
	return results, nil
}

// readSearchResponseBody 严格限制 Provider Response 大小。
func readSearchResponseBody(reader io.Reader) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(reader, searchBodyLimit+1))
	if err != nil {
		return nil, err
	}
	if len(body) > searchBodyLimit {
		return nil, fmt.Errorf("响应体超过 %d bytes 安全上限", searchBodyLimit)
	}
	return body, nil
}

func attr(node *html.Node, name string) string {
	for _, attribute := range node.Attr {
		if attribute.Key == name {
			return attribute.Val
		}
	}
	return ""
}

func hasClass(node *html.Node, className string) bool {
	for _, value := range strings.Fields(attr(node, "class")) {
		if value == className {
			return true
		}
	}
	return false
}

func nodeText(node *html.Node) string {
	var builder strings.Builder
	var walk func(*html.Node)
	walk =
		func(current *html.Node) {
			if current.Type == html.TextNode {
				builder.WriteString(current.Data)
			}
			for child := current.FirstChild; child != nil; child = child.NextSibling {
				walk(child)
			}
		}
	walk(node)
	return strings.Join(strings.Fields(builder.String()), " ")
}
