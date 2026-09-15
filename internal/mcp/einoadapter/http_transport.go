package einoadapter

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"

	"golang.org/x/net/http/httpguts"

	humbertmcp "github.com/sda1-hacker/humbert-agent/internal/mcp"
)

const mcpHTTPMaxRedirects = 10

var blockedRemotePrefixes = mustParsePrefixes(
	"100.64.0.0/10",   // carrier-grade NAT
	"192.0.0.0/24",    // IETF protocol assignments
	"192.0.2.0/24",    // documentation
	"198.18.0.0/15",   // benchmark
	"198.51.100.0/24", // documentation
	"203.0.113.0/24",  // documentation
	"2001:db8::/32",   // IPv6 documentation
)

type ipResolver interface {
	LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error)
}

// credentialHeaderRoundTripper 在每一个 Streamable HTTP 请求发送前注入已经从
// CredentialStore 解析出的 Header。它只持有当前 Session 的内存副本，不把秘密写回
// Server 配置，也不会修改调用方传入的 Request。
type credentialHeaderRoundTripper struct {
	base          http.RoundTripper
	headers       http.Header
	allowedOrigin string
}

func (t credentialHeaderRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	if request == nil {
		return nil, errors.New("MCP HTTP request 不能为空")
	}
	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}
	if t.allowedOrigin != "" && normalizedOrigin(request.URL) != t.allowedOrigin {
		return nil, fmt.Errorf("拒绝向 MCP Endpoint 之外的 Origin 发送请求: %s", normalizedOrigin(request.URL))
	}

	clone := request.Clone(request.Context())
	clone.Header = request.Header.Clone()
	for name, values := range t.headers {
		clone.Header.Del(name)
		for _, value := range values {
			clone.Header.Add(name, value)
		}
	}
	return base.RoundTrip(clone)
}

// secureMCPDialer 把 HTTP Transport 的连接目标固定在用户配置的 Endpoint Host，并在每次
// 建连时自行解析 DNS 后直接拨号到已校验 IP。这样可以阻止 hostname 经 DNS 重绑定到
// loopback/private/link-local/metadata 等地址。localhost/显式 loopback 仅在用户本来就
// 配置了本机 Endpoint 时允许。
type secureMCPDialer struct {
	resolver      ipResolver
	dialer        net.Dialer
	endpointHost  string
	allowLoopback bool
	allowPrivate  bool
}

func (d *secureMCPDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	if ctx == nil {
		return nil, errors.New("MCP HTTP dial context 不能为空")
	}
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, fmt.Errorf("解析 MCP HTTP dial address 失败: %w", err)
	}
	if canonicalHost(host) != canonicalHost(d.endpointHost) {
		return nil, fmt.Errorf("拒绝连接 MCP Endpoint Host 之外的地址: %s", host)
	}

	ips, err := d.lookup(ctx, host)
	if err != nil {
		return nil, err
	}
	var rejected []string
	var dialErrors []string
	for _, ip := range ips {
		if network == "tcp4" && ip.To4() == nil {
			continue
		}
		if network == "tcp6" && ip.To4() != nil {
			continue
		}
		if err := validateMCPDialIP(ip, d.allowLoopback, d.allowPrivate); err != nil {
			rejected = append(rejected, ip.String())
			continue
		}
		conn, err := d.dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		if err == nil {
			return conn, nil
		}
		dialErrors = append(dialErrors, ip.String()+": "+err.Error())
	}
	if len(dialErrors) > 0 {
		return nil, fmt.Errorf("连接 MCP Endpoint %q 失败: %s", host, strings.Join(dialErrors, "; "))
	}
	if len(rejected) == 0 {
		return nil, fmt.Errorf("MCP Endpoint %q 没有可用 IP", host)
	}
	return nil, fmt.Errorf("MCP Endpoint %q 解析结果均被网络安全策略拒绝: %s", host, strings.Join(rejected, ", "))
}

func (d *secureMCPDialer) lookup(ctx context.Context, host string) ([]net.IP, error) {
	if ip := net.ParseIP(strings.TrimSpace(host)); ip != nil {
		return []net.IP{ip}, nil
	}
	resolver := d.resolver
	if resolver == nil {
		resolver = net.DefaultResolver
	}
	addresses, err := resolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, fmt.Errorf("解析 MCP Endpoint Host %q 失败: %w", host, err)
	}
	result := make([]net.IP, 0, len(addresses))
	for _, address := range addresses {
		if address.IP != nil {
			result = append(result, address.IP)
		}
	}
	return result, nil
}

func newStreamableHTTPClient(endpoint string, headers http.Header) (*http.Client, error) {
	// Control Plane 默认沿用原有安全策略：仅当 Endpoint 本身明确写 localhost/loopback
	// 时允许 loopback，私有网络仍拒绝。Agent Runtime 会根据 Sandbox NetworkMode
	// 调用 newStreamableHTTPClientWithAccess 进一步收紧或显式放开 LAN。
	return newStreamableHTTPClientWithAccess(endpoint, headers, true, false)
}

func newStreamableHTTPClientWithAccess(endpoint string, headers http.Header, allowLocal bool, allowPrivate bool) (*http.Client, error) {
	parsed, err := url.Parse(strings.TrimSpace(endpoint))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, errors.New("MCP HTTP endpoint 无效")
	}
	origin := normalizedOrigin(parsed)
	if origin == "" {
		return nil, errors.New("MCP HTTP endpoint Origin 无效")
	}

	base, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return nil, errors.New("默认 HTTP Transport 类型无效")
	}
	transport := base.Clone()
	// MCP Credential 不应被进程级 HTTP_PROXY/HTTPS_PROXY 静默转发给未知代理；Remote MCP
	// 若未来需要企业代理，应新增显式受控配置，而不是继承 Humbert 进程环境。
	transport.Proxy = nil
	transport.DialContext = (&secureMCPDialer{
		resolver:      net.DefaultResolver,
		dialer:        net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second},
		endpointHost:  parsed.Hostname(),
		allowLoopback: allowLocal && explicitLoopbackHost(parsed.Hostname()),
		allowPrivate:  allowPrivate,
	}).DialContext

	return &http.Client{
		Transport: credentialHeaderRoundTripper{
			base:          transport,
			headers:       headers.Clone(),
			allowedOrigin: origin,
		},
		CheckRedirect: func(request *http.Request, via []*http.Request) error {
			if request == nil || request.URL == nil {
				return errors.New("MCP HTTP redirect 缺少目标 URL")
			}
			if normalizedOrigin(request.URL) != origin {
				return fmt.Errorf("拒绝 MCP HTTP 跨 Origin 重定向: %s", normalizedOrigin(request.URL))
			}
			if len(via) >= mcpHTTPMaxRedirects {
				return errors.New("MCP HTTP 重定向次数过多")
			}
			return nil
		},
	}, nil
}

func validateMCPDialIP(ip net.IP, allowLoopback bool, allowPrivate bool) error {
	if ip == nil {
		return errors.New("IP 为空")
	}
	if ip.IsLoopback() {
		if allowLoopback {
			return nil
		}
		return errors.New("禁止 Remote MCP 访问 loopback 地址")
	}
	if ip.IsPrivate() {
		if allowPrivate {
			return nil
		}
		return errors.New("禁止 Remote MCP 访问私有网络地址")
	}
	if ip.IsUnspecified() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() {
		return errors.New("禁止 Remote MCP 访问非公网地址")
	}
	addr, ok := netip.AddrFromSlice(ip)
	if !ok {
		return errors.New("IP 地址无效")
	}
	addr = addr.Unmap()
	for _, prefix := range blockedRemotePrefixes {
		if prefix.Contains(addr) {
			return errors.New("禁止 Remote MCP 访问特殊用途网络地址")
		}
	}
	if !ip.IsGlobalUnicast() {
		return errors.New("禁止 Remote MCP 访问非全局单播地址")
	}
	return nil
}

func mustParsePrefixes(values ...string) []netip.Prefix {
	result := make([]netip.Prefix, 0, len(values))
	for _, value := range values {
		result = append(result, netip.MustParsePrefix(value))
	}
	return result
}

func explicitLoopbackHost(host string) bool {
	host = strings.TrimSpace(strings.TrimSuffix(host, "."))
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func canonicalHost(host string) string {
	return strings.ToLower(strings.TrimSpace(strings.TrimSuffix(host, ".")))
}

func normalizedOrigin(value *url.URL) string {
	if value == nil {
		return ""
	}
	scheme := strings.ToLower(strings.TrimSpace(value.Scheme))
	hostname := strings.ToLower(strings.TrimSpace(value.Hostname()))
	if scheme == "" || hostname == "" {
		return ""
	}
	port := value.Port()
	if (scheme == "https" && port == "443") || (scheme == "http" && port == "80") {
		port = ""
	}
	host := hostname
	if port != "" {
		host = net.JoinHostPort(hostname, port)
	} else if strings.Contains(hostname, ":") {
		host = "[" + hostname + "]"
	}
	return scheme + "://" + host
}

func resolveCredentialHeaders(
	ctx context.Context,
	reader humbertmcp.CredentialReader,
	server humbertmcp.Server,
) (http.Header, error) {
	if server.HTTP == nil {
		return nil, errors.New("Streamable HTTP MCP Server 缺少 HTTP 配置")
	}
	if reader == nil && (server.HTTP.BearerCredentialID != "" || len(server.HTTP.Headers) > 0) {
		return nil, errors.New("MCP HTTP Server 配置了 Credential，但 CredentialStore 不可用")
	}

	headers := make(http.Header)
	if credentialID := strings.TrimSpace(server.HTTP.BearerCredentialID); credentialID != "" {
		value, err := reader.Get(ctx, credentialID)
		if err != nil {
			return nil, fmt.Errorf("读取 MCP Bearer Credential 失败: %w", err)
		}
		value = strings.TrimSpace(value)
		if value == "" {
			return nil, errors.New("MCP Bearer Credential 为空")
		}
		if !httpguts.ValidHeaderFieldValue("Bearer " + value) {
			return nil, errors.New("MCP Bearer Credential 包含非法 HTTP Header 字符")
		}
		headers.Set("Authorization", "Bearer "+value)
	}

	for _, item := range server.HTTP.Headers {
		credentialID := strings.TrimSpace(item.CredentialID)
		value, err := reader.Get(ctx, credentialID)
		if err != nil {
			return nil, fmt.Errorf("读取 MCP HTTP Header %q Credential 失败: %w", item.Name, err)
		}
		value = strings.TrimSpace(value)
		if value == "" {
			return nil, fmt.Errorf("MCP HTTP Header %q Credential 为空", item.Name)
		}
		if !httpguts.ValidHeaderFieldValue(value) {
			return nil, fmt.Errorf("MCP HTTP Header %q Credential 包含非法 HTTP Header 字符", item.Name)
		}
		headers.Set(item.Name, value)
	}
	return headers, nil
}
