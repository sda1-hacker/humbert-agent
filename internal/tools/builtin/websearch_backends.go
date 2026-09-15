package builtin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"unicode/utf8"

	"golang.org/x/net/html"
)

const (
	searchBodyLimit = 4 * 1024 * 1024

	searchErrorPreviewLimit = 2048

	anySearchURL = "https://api.anysearch.com/v1/search"
)

// searchBackend 是 web_search 内部的 Provider 抽象。
//
// Backend 只负责请求 Provider 并转换为统一 searchResult。Provider fallback、
// 质量判断、域名过滤、去重和 Tool Schema 都由 WebSearchFactory 负责。
type searchBackend interface {
	name() string

	search(
		ctx context.Context,
		client *http.Client,
		query string,
		count int,
	) (
		[]searchResult,
		error,
	)
}

// anySearchBackend 使用 AnySearch API。
//
// anonymous=true 时不发送 Authorization Header，对齐 OpenHanako 当前使用的
// anysearch_free Provider。这样 Humbert 即使没有额外搜索 API Key，也可以先使用
// 结构化实时搜索 API，而不是一开始就依赖容易被页面结构/反爬影响的 HTML 搜索页。
type anySearchBackend struct {
	apiKey string

	anonymous bool
}

func (
	b anySearchBackend,
) name() string {
	if b.anonymous ||
		strings.TrimSpace(
			b.apiKey,
		) == "" {
		return webSearchProviderAnySearchFree
	}

	return webSearchProviderAnySearch
}

func (
	b anySearchBackend,
) search(
	ctx context.Context,
	client *http.Client,
	query string,
	count int,
) (
	[]searchResult,
	error,
) {
	requestCount :=
		count

	if requestCount < 1 {
		requestCount = 1
	}

	// AnySearch 官方 /v1/search 当前约束 max_results 为 1~20。即使 Humbert
	// 未来把全局搜索上限调高，也不能把非法数量透传给 Provider。
	if requestCount > 20 {
		requestCount = 20
	}

	requestBody :=
		map[string]any{
			"query":       query,
			"max_results": requestCount,
			"language": searchLanguage(
				query,
			),
		}

	if containsCJK(
		query,
	) {
		// 中文实时信息优先请求中国区结果。AnySearch 会继续负责底层数据源路由、
		// 合并和排序，Humbert 不需要知道具体搜索引擎。
		requestBody["zone"] =
			"cn"
	} else {
		requestBody["zone"] =
			"intl"
	}

	encodedBody, err :=
		json.Marshal(
			requestBody,
		)
	if err != nil {
		return nil, fmt.Errorf(
			"编码 AnySearch 请求失败: %w",
			err,
		)
	}

	request, err :=
		http.NewRequestWithContext(
			ctx,
			http.MethodPost,
			anySearchURL,
			bytes.NewReader(
				encodedBody,
			),
		)
	if err != nil {
		return nil, fmt.Errorf(
			"创建 AnySearch 请求失败: %w",
			err,
		)
	}

	request.Header.Set(
		"Content-Type",
		"application/json",
	)

	request.Header.Set(
		"Accept",
		"application/json",
	)

	if key :=
		strings.TrimSpace(
			b.apiKey,
		); key != "" &&
		!b.anonymous {
		request.Header.Set(
			"Authorization",
			"Bearer "+key,
		)
	}

	response, err :=
		client.Do(
			request,
		)
	if err != nil {
		return nil, fmt.Errorf(
			"请求 AnySearch 失败: %w",
			err,
		)
	}

	defer response.Body.Close()

	body, err :=
		readSearchResponseBody(
			response.Body,
		)
	if err != nil {
		return nil, fmt.Errorf(
			"读取 AnySearch 响应失败: %w",
			err,
		)
	}

	// AnySearch 匿名额度耗尽时，402 响应的 data 可能包含自动注册的账号或 API Key。
	// Humbert 绝不能把这类上游敏感字段拼进 Tool Error、日志或模型上下文。
	// 因此 AnySearch 的非 2xx 错误只解析顶层 message，并按 HTTP Status 输出
	// 有界、非敏感的诊断，不回显原始 Response Body。
	if response.StatusCode < 200 ||
		response.StatusCode >= 300 {
		return nil,
			anySearchHTTPError(
				response.StatusCode,
				body,
			)
	}

	var decoded anySearchEnvelope

	if err :=
		json.Unmarshal(
			body,
			&decoded,
		); err != nil {
		return nil, fmt.Errorf(
			"解析 AnySearch JSON 失败: %w",
			err,
		)
	}

	if decoded.Code != 0 {
		message :=
			strings.TrimSpace(
				decoded.Message,
			)

		if message == "" {
			message =
				fmt.Sprintf(
					"code %d",
					decoded.Code,
				)
		}

		return nil, fmt.Errorf(
			"AnySearch API 返回错误: %s",
			message,
		)
	}

	items :=
		decoded.Data.Results

	if len(items) == 0 {
		items =
			decoded.Results
	}

	results :=
		make(
			[]searchResult,
			0,
			minInt(
				len(items),
				requestCount,
			),
		)

	for _, item := range items {
		snippet :=
			strings.TrimSpace(
				item.Content,
			)

		if snippet == "" {
			snippet =
				strings.TrimSpace(
					item.Description,
				)
		}

		if snippet == "" {
			snippet =
				strings.TrimSpace(
					item.Snippet,
				)
		}

		results =
			append(
				results,
				searchResult{
					Title:       item.Title,
					URL:         item.URL,
					Snippet:     snippet,
					PublishedAt: item.PublishedAt,
				},
			)

		if len(results) >=
			requestCount {
			break
		}
	}

	return results, nil
}

func anySearchHTTPError(
	status int,
	body []byte,
) error {
	message := ""

	var decoded struct {
		Message string `json:"message"`
	}

	if err :=
		json.Unmarshal(
			body,
			&decoded,
		); err == nil {
		message =
			strings.TrimSpace(
				decoded.Message,
			)
	}

	switch status {
	case http.StatusUnauthorized,
		http.StatusForbidden:
		if message == "" {
			message =
				"鉴权失败"
		}

		return fmt.Errorf(
			"AnySearch HTTP %d: %s",
			status,
			message,
		)

	case http.StatusPaymentRequired:
		return fmt.Errorf(
			"AnySearch HTTP %d: 搜索额度已用尽",
			status,
		)

	case http.StatusTooManyRequests:
		return fmt.Errorf(
			"AnySearch HTTP %d: 请求频率受限",
			status,
		)

	default:
		if message == "" {
			message =
				http.StatusText(
					status,
				)
		}

		if message == "" {
			message =
				"请求失败"
		}

		return fmt.Errorf(
			"AnySearch HTTP %d: %s",
			status,
			message,
		)
	}
}

type anySearchEnvelope struct {
	Code int `json:"code"`

	Message string `json:"message"`

	Data struct {
		Results []anySearchItem `json:"results"`
	} `json:"data"`

	Results []anySearchItem `json:"results"`
}

type anySearchItem struct {
	Title string `json:"title"`

	URL string `json:"url"`

	Content string `json:"content"`

	Description string `json:"description"`

	Snippet string `json:"snippet"`

	PublishedAt string `json:"published_at"`
}

func searchLanguage(
	query string,
) string {
	if containsCJK(
		query,
	) {
		return "zh-CN"
	}

	return "en"
}

// tavilyBackend 使用 Tavily JSON Search API。
type tavilyBackend struct {
	apiKey string
}

func (
	b tavilyBackend,
) name() string {
	return webSearchProviderTavily
}

func (
	b tavilyBackend,
) search(
	ctx context.Context,
	client *http.Client,
	query string,
	count int,
) (
	[]searchResult,
	error,
) {
	requestBody, err :=
		json.Marshal(
			map[string]any{
				"query":        query,
				"max_results":  count,
				"search_depth": "basic",
			},
		)
	if err != nil {
		return nil, fmt.Errorf(
			"编码 Tavily 请求失败: %w",
			err,
		)
	}

	request, err :=
		http.NewRequestWithContext(
			ctx,
			http.MethodPost,
			"https://api.tavily.com/search",
			bytes.NewReader(
				requestBody,
			),
		)
	if err != nil {
		return nil, fmt.Errorf(
			"创建 Tavily 请求失败: %w",
			err,
		)
	}

	request.Header.Set(
		"Authorization",
		"Bearer "+b.apiKey,
	)

	request.Header.Set(
		"Content-Type",
		"application/json",
	)

	request.Header.Set(
		"Accept",
		"application/json",
	)

	response, err :=
		client.Do(
			request,
		)
	if err != nil {
		return nil, fmt.Errorf(
			"请求 Tavily 失败: %w",
			err,
		)
	}

	defer response.Body.Close()

	body, err :=
		readSearchResponseBody(
			response.Body,
		)
	if err != nil {
		return nil, fmt.Errorf(
			"读取 Tavily 响应失败: %w",
			err,
		)
	}

	if response.StatusCode < 200 ||
		response.StatusCode >= 300 {
		return nil, fmt.Errorf(
			"Tavily HTTP %d: %s",
			response.StatusCode,
			searchErrorPreview(
				body,
			),
		)
	}

	var decoded struct {
		Results []struct {
			Title string `json:"title"`

			URL string `json:"url"`

			Content string `json:"content"`
		} `json:"results"`
	}

	if err :=
		json.Unmarshal(
			body,
			&decoded,
		); err != nil {
		return nil, fmt.Errorf(
			"解析 Tavily JSON 失败: %w",
			err,
		)
	}

	result :=
		make(
			[]searchResult,
			0,
			len(decoded.Results),
		)

	for _, item := range decoded.Results {
		result =
			append(
				result,
				searchResult{
					Title:   item.Title,
					URL:     item.URL,
					Snippet: item.Content,
				},
			)
	}

	return result, nil
}

// braveBackend 使用 Brave Search JSON API。
type braveBackend struct {
	apiKey string
}

func (
	b braveBackend,
) name() string {
	return webSearchProviderBrave
}

func (
	b braveBackend,
) search(
	ctx context.Context,
	client *http.Client,
	query string,
	count int,
) (
	[]searchResult,
	error,
) {
	target :=
		fmt.Sprintf(
			"https://api.search.brave.com/res/v1/web/search?q=%s&count=%d",
			url.QueryEscape(
				query,
			),
			count,
		)

	request, err :=
		http.NewRequestWithContext(
			ctx,
			http.MethodGet,
			target,
			nil,
		)
	if err != nil {
		return nil, fmt.Errorf(
			"创建 Brave Search 请求失败: %w",
			err,
		)
	}

	request.Header.Set(
		"Accept",
		"application/json",
	)

	request.Header.Set(
		"X-Subscription-Token",
		b.apiKey,
	)

	response, err :=
		client.Do(
			request,
		)
	if err != nil {
		return nil, fmt.Errorf(
			"请求 Brave Search 失败: %w",
			err,
		)
	}

	defer response.Body.Close()

	body, err :=
		readSearchResponseBody(
			response.Body,
		)
	if err != nil {
		return nil, fmt.Errorf(
			"读取 Brave Search 响应失败: %w",
			err,
		)
	}

	if response.StatusCode < 200 ||
		response.StatusCode >= 300 {
		return nil, fmt.Errorf(
			"Brave Search HTTP %d: %s",
			response.StatusCode,
			searchErrorPreview(
				body,
			),
		)
	}

	var decoded struct {
		Web struct {
			Results []struct {
				Title string `json:"title"`

				URL string `json:"url"`

				Description string `json:"description"`

				PageAge string `json:"page_age"`
			} `json:"results"`
		} `json:"web"`
	}

	if err :=
		json.Unmarshal(
			body,
			&decoded,
		); err != nil {
		return nil, fmt.Errorf(
			"解析 Brave Search JSON 失败: %w",
			err,
		)
	}

	result :=
		make(
			[]searchResult,
			0,
			len(decoded.Web.Results),
		)

	for _, item := range decoded.Web.Results {
		result =
			append(
				result,
				searchResult{
					Title: stripHTMLTags(
						item.Title,
					),
					URL: item.URL,
					Snippet: stripHTMLTags(
						item.Description,
					),
					PublishedAt: item.PageAge,
				},
			)
	}

	return result, nil
}

// serperBackend 使用 Serper Google Search API。
type serperBackend struct {
	apiKey string
}

func (
	b serperBackend,
) name() string {
	return webSearchProviderSerper
}

func (
	b serperBackend,
) search(
	ctx context.Context,
	client *http.Client,
	query string,
	count int,
) (
	[]searchResult,
	error,
) {
	requestBody, err :=
		json.Marshal(
			map[string]any{
				"q":   query,
				"num": count,
			},
		)
	if err != nil {
		return nil, fmt.Errorf(
			"编码 Serper 请求失败: %w",
			err,
		)
	}

	request, err :=
		http.NewRequestWithContext(
			ctx,
			http.MethodPost,
			"https://google.serper.dev/search",
			bytes.NewReader(
				requestBody,
			),
		)
	if err != nil {
		return nil, fmt.Errorf(
			"创建 Serper 请求失败: %w",
			err,
		)
	}

	request.Header.Set(
		"Content-Type",
		"application/json",
	)

	request.Header.Set(
		"Accept",
		"application/json",
	)

	request.Header.Set(
		"X-API-KEY",
		b.apiKey,
	)

	response, err :=
		client.Do(
			request,
		)
	if err != nil {
		return nil, fmt.Errorf(
			"请求 Serper 失败: %w",
			err,
		)
	}

	defer response.Body.Close()

	body, err :=
		readSearchResponseBody(
			response.Body,
		)
	if err != nil {
		return nil, fmt.Errorf(
			"读取 Serper 响应失败: %w",
			err,
		)
	}

	if response.StatusCode < 200 ||
		response.StatusCode >= 300 {
		return nil, fmt.Errorf(
			"Serper HTTP %d: %s",
			response.StatusCode,
			searchErrorPreview(
				body,
			),
		)
	}

	var decoded struct {
		Organic []struct {
			Title string `json:"title"`

			Link string `json:"link"`

			Snippet string `json:"snippet"`

			Date string `json:"date"`
		} `json:"organic"`
	}

	if err :=
		json.Unmarshal(
			body,
			&decoded,
		); err != nil {
		return nil, fmt.Errorf(
			"解析 Serper JSON 失败: %w",
			err,
		)
	}

	result :=
		make(
			[]searchResult,
			0,
			len(decoded.Organic),
		)

	for _, item := range decoded.Organic {
		result =
			append(
				result,
				searchResult{
					Title:       item.Title,
					URL:         item.Link,
					Snippet:     item.Snippet,
					PublishedAt: item.Date,
				},
			)
	}

	return result, nil
}

// duckDuckGoBackend 是无需 API Key 的 HTML Backend。
type duckDuckGoBackend struct{}

func (
	duckDuckGoBackend,
) name() string {
	return webSearchProviderDuckDuckGo
}

func (
	duckDuckGoBackend,
) search(
	ctx context.Context,
	client *http.Client,
	query string,
	count int,
) (
	[]searchResult,
	error,
) {
	target :=
		"https://html.duckduckgo.com/html/?q=" +
			url.QueryEscape(
				query,
			)

	request, err :=
		http.NewRequestWithContext(
			ctx,
			http.MethodGet,
			target,
			nil,
		)
	if err != nil {
		return nil, fmt.Errorf(
			"创建 DuckDuckGo 请求失败: %w",
			err,
		)
	}

	request.Header.Set(
		"User-Agent",
		"Mozilla/5.0 (compatible; HumbertAgent/0.1)",
	)

	request.Header.Set(
		"Accept",
		"text/html",
	)

	request.Header.Set(
		"Accept-Language",
		"zh-CN,zh;q=0.9,en;q=0.8",
	)

	response, err :=
		client.Do(
			request,
		)
	if err != nil {
		return nil, fmt.Errorf(
			"请求 DuckDuckGo 失败: %w",
			err,
		)
	}

	defer response.Body.Close()

	body, err :=
		readSearchResponseBody(
			response.Body,
		)
	if err != nil {
		return nil, fmt.Errorf(
			"读取 DuckDuckGo 响应失败: %w",
			err,
		)
	}

	if response.StatusCode < 200 ||
		response.StatusCode >= 300 {
		return nil, fmt.Errorf(
			"DuckDuckGo HTTP %d",
			response.StatusCode,
		)
	}

	results, err :=
		parseDuckDuckGoHTML(
			body,
		)
	if err != nil {
		return nil, err
	}

	if len(results) >
		count {
		results =
			results[:count]
	}

	return results, nil
}

func parseDuckDuckGoHTML(
	body []byte,
) (
	[]searchResult,
	error,
) {
	document, err :=
		html.Parse(
			bytes.NewReader(
				body,
			),
		)
	if err != nil {
		return nil, fmt.Errorf(
			"解析 DuckDuckGo HTML 失败: %w",
			err,
		)
	}

	var results []searchResult

	var snippets []string

	var walk func(
		*html.Node,
	)

	walk =
		func(
			node *html.Node,
		) {
			if node.Type ==
				html.ElementNode &&
				node.Data == "a" &&
				hasClass(
					node,
					"result__a",
				) {
				results =
					append(
						results,
						searchResult{
							Title: nodeText(
								node,
							),
							URL: unwrapDDGURL(
								attr(
									node,
									"href",
								),
							),
						},
					)
			}

			if node.Type ==
				html.ElementNode &&
				hasClass(
					node,
					"result__snippet",
				) {
				snippets =
					append(
						snippets,
						nodeText(
							node,
						),
					)
			}

			for child :=
				node.FirstChild; child != nil; child =
				child.NextSibling {
				walk(
					child,
				)
			}
		}

	walk(
		document,
	)

	for index := range results {
		if index <
			len(snippets) {
			results[index].Snippet =
				snippets[index]
		}
	}

	return results, nil
}

func unwrapDDGURL(
	href string,
) string {
	raw :=
		href

	if strings.HasPrefix(
		raw,
		"//",
	) {
		raw =
			"https:" +
				raw
	}

	parsed, err :=
		url.Parse(
			raw,
		)
	if err != nil {
		return href
	}

	if target :=
		parsed.Query().
			Get(
				"uddg",
			); target != "" {
		return target
	}

	return raw
}

// bingBackend 使用 Bing HTML 搜索页，无需 API Key。
type bingBackend struct{}

func (
	bingBackend,
) name() string {
	return webSearchProviderBing
}

func (
	bingBackend,
) search(
	ctx context.Context,
	client *http.Client,
	query string,
	count int,
) (
	[]searchResult,
	error,
) {
	target :=
		fmt.Sprintf(
			"https://cn.bing.com/search?q=%s&count=%d&first=1",
			url.QueryEscape(
				query,
			),
			count,
		)

	request, err :=
		http.NewRequestWithContext(
			ctx,
			http.MethodGet,
			target,
			nil,
		)
	if err != nil {
		return nil, fmt.Errorf(
			"创建 Bing 请求失败: %w",
			err,
		)
	}

	request.Header.Set(
		"User-Agent",
		"Mozilla/5.0 (compatible; HumbertAgent/0.1)",
	)

	request.Header.Set(
		"Accept",
		"text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8",
	)

	request.Header.Set(
		"Accept-Language",
		"zh-CN,zh;q=0.9,en;q=0.8",
	)

	response, err :=
		client.Do(
			request,
		)
	if err != nil {
		return nil, fmt.Errorf(
			"请求 Bing 失败: %w",
			err,
		)
	}

	defer response.Body.Close()

	body, err :=
		readSearchResponseBody(
			response.Body,
		)
	if err != nil {
		return nil, fmt.Errorf(
			"读取 Bing 响应失败: %w",
			err,
		)
	}

	if response.StatusCode !=
		http.StatusOK {
		return nil, fmt.Errorf(
			"Bing HTTP %d",
			response.StatusCode,
		)
	}

	results, err :=
		parseBingHTML(
			body,
		)
	if err != nil {
		return nil, err
	}

	if len(results) >
		count {
		results =
			results[:count]
	}

	return results, nil
}

func parseBingHTML(
	body []byte,
) (
	[]searchResult,
	error,
) {
	document, err :=
		html.Parse(
			bytes.NewReader(
				body,
			),
		)
	if err != nil {
		return nil, fmt.Errorf(
			"解析 Bing HTML 失败: %w",
			err,
		)
	}

	var results []searchResult

	var walk func(
		*html.Node,
	)

	walk =
		func(
			node *html.Node,
		) {
			if node.Type ==
				html.ElementNode &&
				node.Data == "li" &&
				hasClass(
					node,
					"b_algo",
				) {
				result :=
					searchResult{}

				var findTitle func(
					*html.Node,
				)

				findTitle =
					func(
						current *html.Node,
					) {
						if result.Title != "" {
							return
						}

						if current.Type ==
							html.ElementNode &&
							current.Data ==
								"h2" {
							for child :=
								current.FirstChild; child != nil; child =
								child.NextSibling {
								if child.Type ==
									html.ElementNode &&
									child.Data ==
										"a" {
									result.Title =
										nodeText(
											child,
										)

									result.URL =
										attr(
											child,
											"href",
										)

									return
								}
							}
						}

						for child :=
							current.FirstChild; child != nil; child =
							child.NextSibling {
							findTitle(
								child,
							)
						}
					}

				findTitle(
					node,
				)

				var findSnippet func(
					*html.Node,
				)

				findSnippet =
					func(
						current *html.Node,
					) {
						if result.Snippet != "" {
							return
						}

						if current.Type ==
							html.ElementNode &&
							hasClass(
								current,
								"b_caption",
							) {
							for child :=
								current.FirstChild; child != nil; child =
								child.NextSibling {
								if child.Type ==
									html.ElementNode &&
									child.Data ==
										"p" {
									result.Snippet =
										nodeText(
											child,
										)

									return
								}
							}
						}

						for child :=
							current.FirstChild; child != nil; child =
							child.NextSibling {
							findSnippet(
								child,
							)
						}
					}

				findSnippet(
					node,
				)

				if result.Title != "" &&
					result.URL != "" {
					results =
						append(
							results,
							result,
						)
				}

				return
			}

			for child :=
				node.FirstChild; child != nil; child =
				child.NextSibling {
				walk(
					child,
				)
			}
		}

	walk(
		document,
	)

	return results, nil
}

// readSearchResponseBody 严格限制 Provider Response 大小。
func readSearchResponseBody(
	reader io.Reader,
) (
	[]byte,
	error,
) {
	body, err :=
		io.ReadAll(
			io.LimitReader(
				reader,
				searchBodyLimit+1,
			),
		)
	if err != nil {
		return nil, err
	}

	if len(body) >
		searchBodyLimit {
		return nil, fmt.Errorf(
			"响应体超过 %d bytes 安全上限",
			searchBodyLimit,
		)
	}

	return body, nil
}

func searchErrorPreview(
	body []byte,
) string {
	text :=
		strings.TrimSpace(
			string(body),
		)

	if len(text) <=
		searchErrorPreviewLimit {
		return text
	}

	return safeSearchUTF8Prefix(
		text,
		searchErrorPreviewLimit,
	) + "…"
}

func attr(
	node *html.Node,
	name string,
) string {
	for _, attribute := range node.Attr {
		if attribute.Key ==
			name {
			return attribute.Val
		}
	}

	return ""
}

func hasClass(
	node *html.Node,
	className string,
) bool {
	for _, value := range strings.Fields(
		attr(
			node,
			"class",
		),
	) {
		if value ==
			className {
			return true
		}
	}

	return false
}

func nodeText(
	node *html.Node,
) string {
	var builder strings.Builder

	var walk func(
		*html.Node,
	)

	walk =
		func(
			current *html.Node,
		) {
			if current.Type ==
				html.TextNode {
				builder.WriteString(
					current.Data,
				)
			}

			for child :=
				current.FirstChild; child != nil; child =
				child.NextSibling {
				walk(
					child,
				)
			}
		}

	walk(
		node,
	)

	return strings.Join(
		strings.Fields(
			builder.String(),
		),
		" ",
	)
}

func stripHTMLTags(
	value string,
) string {
	if !strings.ContainsRune(
		value,
		'<',
	) {
		return value
	}

	document, err :=
		html.Parse(
			strings.NewReader(
				value,
			),
		)
	if err != nil {
		return value
	}

	return nodeText(
		document,
	)
}

// safeSearchUTF8Prefix 在字节预算内截取合法 UTF-8 前缀。
func safeSearchUTF8Prefix(
	value string,
	maxBytes int,
) string {
	if maxBytes <= 0 {
		return ""
	}

	if len(value) <=
		maxBytes {
		return value
	}

	end :=
		maxBytes

	for end > 0 &&
		!utf8.ValidString(
			value[:end],
		) {
		end--
	}

	return value[:end]
}
