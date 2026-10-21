// Package publicweb 为网页读取和浏览器代理共享公网地址边界。
package publicweb

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"
)

func ParseURL(raw string) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return nil, fmt.Errorf("解析 URL 失败: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("只允许 http/https URL，实际为 %q", parsed.Scheme)
	}
	if parsed.Hostname() == "" {
		return nil, errors.New("URL 缺少 Host")
	}
	if parsed.User != nil {
		return nil, errors.New("URL 不允许包含用户名或密码")
	}
	if port := parsed.Port(); port != "" {
		value, err := strconv.Atoi(port)
		if err != nil || value < 1 || value > 65535 {
			return nil, fmt.Errorf("URL 端口不合法: %q", port)
		}
	}
	parsed.Fragment = ""
	return parsed, nil
}

// Dialer 同时供 web_fetch 和 browser 代理使用；混合公网/私网 DNS 整体拒绝。
type Dialer struct{}

func (d *Dialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, fmt.Errorf("解析 web_fetch 目标地址失败: %w", err)
	}
	ips, err := ResolveIPs(ctx, host)
	if err != nil {
		return nil, err
	}
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	var lastErr error
	for _, ip := range ips {
		conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		if err == nil {
			return conn, nil
		}
		lastErr = err
	}
	return nil, fmt.Errorf("连接 web_fetch Host %q 失败: %w", host, lastErr)
}

func ResolveIPs(ctx context.Context, host string) ([]net.IP, error) {
	if parsed := net.ParseIP(host); parsed != nil {
		if isForbiddenIP(parsed) {
			return nil, fmt.Errorf("web_fetch 拒绝访问非公网地址 %s", parsed.String())
		}
		return []net.IP{parsed}, nil
	}
	addresses, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, fmt.Errorf("解析 web_fetch Host %q 失败: %w", host, err)
	}
	if len(addresses) == 0 {
		return nil, fmt.Errorf("web_fetch Host %q 没有 DNS 记录", host)
	}
	result := make([]net.IP, 0, len(addresses))
	for _, address := range addresses {
		if isForbiddenIP(address.IP) {
			return nil, fmt.Errorf("web_fetch Host %q 解析到非公网地址 %s", host, address.IP)
		}
		result = append(result, address.IP)
	}
	return result, nil
}

func isForbiddenIP(ip net.IP) bool {
	if ip == nil || ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() {
		return true
	}
	if v4 := ip.To4(); v4 != nil {
		// 0/8、CGNAT、benchmark 与保留地址不是公开网页目标。
		return v4[0] == 0 || v4[0] >= 240 || (v4[0] == 100 && v4[1] >= 64 && v4[1] <= 127) || (v4[0] == 198 && (v4[1] == 18 || v4[1] == 19))
	}
	return false
}
