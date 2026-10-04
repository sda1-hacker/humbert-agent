package builtin

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestBrowserProxyRejectsPrivateDestinations(t *testing.T) {
	p, err := startBrowserProxy((&publicWebDialer{}).DialContext)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	proxyURL, _ := url.Parse("http://" + p.address)
	transport := &http.Transport{Proxy: http.ProxyURL(proxyURL)}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 2 * time.Second}
	for _, target := range []string{"http://127.0.0.1:9/", "http://[::1]:9/", "http://169.254.169.254/latest/meta-data/"} {
		response, err := client.Get(target)
		if err != nil {
			t.Fatal(err)
		}
		_ = response.Body.Close()
		if response.StatusCode != http.StatusBadGateway {
			t.Fatalf("target %s returned %d", target, response.StatusCode)
		}
	}
	if response, err := client.Get("https://127.0.0.1:9/"); err == nil {
		_ = response.Body.Close()
		t.Fatal("CONNECT accepted a private target")
	}
}

func TestBrowserProxyForwardsHTTPAndClosesTunnels(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Proxy-Authorization") != "" {
			t.Error("proxy credential forwarded")
		}
		_, _ = io.WriteString(w, "echo:"+r.URL.Path)
	}))
	defer upstream.Close()
	target := strings.TrimPrefix(upstream.URL, "http://")
	var dialer net.Dialer
	p, err := startBrowserProxy(func(ctx context.Context, network, address string) (net.Conn, error) {
		// 人工公共域名只在测试里映射到本机服务，生产调用始终使用 publicWebDialer。
		if address != "example.test:80" && address != "example.test:443" {
			return nil, fmt.Errorf("unexpected target %s", address)
		}
		return dialer.DialContext(ctx, network, target)
	})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	proxyURL, _ := url.Parse("http://" + p.address)
	transport := &http.Transport{Proxy: http.ProxyURL(proxyURL)}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 2 * time.Second}
	request, _ := http.NewRequest(http.MethodGet, "http://example.test/ordinary", nil)
	request.Header.Set("Proxy-Authorization", "test-only")
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	content, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil || string(content) != "echo:/ordinary" {
		t.Fatalf("response=%q %v", content, err)
	}
	tunnel, err := net.DialTimeout("tcp", p.address, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer tunnel.Close()
	_ = tunnel.SetDeadline(time.Now().Add(2 * time.Second))
	_, _ = io.WriteString(tunnel, "CONNECT example.test:443 HTTP/1.1\r\nHost: example.test:443\r\n\r\n")
	reader := bufio.NewReader(tunnel)
	connected, err := http.ReadResponse(reader, &http.Request{Method: http.MethodConnect})
	if err != nil || connected.StatusCode != http.StatusOK {
		t.Fatalf("CONNECT=%v %v", connected, err)
	}
	_, _ = io.WriteString(tunnel, "GET /tunnel HTTP/1.1\r\nHost: example.test\r\n\r\n")
	response, err = http.ReadResponse(reader, &http.Request{Method: http.MethodGet})
	if err != nil {
		t.Fatal(err)
	}
	content, err = io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil || string(content) != "echo:/tunnel" {
		t.Fatalf("tunnel response=%q %v", content, err)
	}
	_ = p.Close()
	if _, err := reader.ReadByte(); err == nil {
		t.Fatal("tunnel survived proxy close")
	}
}
