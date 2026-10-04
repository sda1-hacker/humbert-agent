package builtin

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"sync"
	"time"
)

// browserProxy 在 socket 层复用 publicWebDialer，覆盖同一 Chrome 的页面、弹窗与 worker。
// CONNECT 不解密 HTTPS；HTTP/WS 的转发、头部处理和升级由标准库负责。
type browserProxy struct {
	address   string
	server    *http.Server
	transport *http.Transport
	dial      func(context.Context, string, string) (net.Conn, error)
	forward   *httputil.ReverseProxy
	cancel    context.CancelFunc
	mu        sync.Mutex
	closed    bool
	conns     map[net.Conn]struct{}
	slots     chan struct{}
}

func startBrowserProxy(dial func(context.Context, string, string) (net.Conn, error)) (*browserProxy, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	p := &browserProxy{address: listener.Addr().String(), dial: dial, cancel: cancel,
		conns: make(map[net.Conn]struct{}), slots: make(chan struct{}, 64)}
	p.transport = &http.Transport{Proxy: nil, DialContext: dial, MaxIdleConns: 16,
		MaxIdleConnsPerHost: 4, IdleConnTimeout: 30 * time.Second, ResponseHeaderTimeout: 30 * time.Second}
	p.forward = &httputil.ReverseProxy{
		Rewrite: func(request *httputil.ProxyRequest) {
			request.Out.Header.Del("Proxy-Authorization")
		},
		Transport: p.transport,
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, _ error) {
			http.Error(w, "browser proxy: destination unavailable or blocked", http.StatusBadGateway)
		},
	}
	p.server = &http.Server{Handler: p, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second,
		MaxHeaderBytes: 64 << 10, BaseContext: func(net.Listener) context.Context { return ctx }}
	go func() { _ = p.server.Serve(&browserProxyListener{Listener: listener, proxy: p}) }()
	return p, nil
}

func (p *browserProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	select {
	case p.slots <- struct{}{}:
		defer func() { <-p.slots }()
	default:
		http.Error(w, "browser proxy: too many requests", http.StatusServiceUnavailable)
		return
	}
	if r.Method == http.MethodConnect {
		p.connect(w, r)
		return
	}
	if r.URL.Scheme != "http" || r.URL.User != nil || r.URL.Host == "" {
		http.Error(w, "browser proxy: invalid HTTP target", http.StatusBadRequest)
		return
	}
	p.forward.ServeHTTP(w, r)
}

func (p *browserProxy) connect(w http.ResponseWriter, r *http.Request) {
	if _, _, err := net.SplitHostPort(r.Host); err != nil {
		http.Error(w, "browser proxy: invalid CONNECT target", http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	upstream, err := p.dial(ctx, "tcp", r.Host)
	cancel()
	if err != nil {
		http.Error(w, "browser proxy: destination unavailable or blocked", http.StatusBadGateway)
		return
	}
	defer upstream.Close()
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "browser proxy: tunnel unavailable", http.StatusInternalServerError)
		return
	}
	client, buffered, err := hijacker.Hijack()
	if err != nil {
		return
	}
	defer client.Close()
	if _, err = buffered.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
		return
	}
	if err = buffered.Flush(); err != nil {
		return
	}
	// Server.Close 不关闭已 hijack 的连接；请求取消也必须收敛两个方向。
	stop := context.AfterFunc(r.Context(), func() { _ = client.Close(); _ = upstream.Close() })
	defer stop()
	done := make(chan struct{})
	go func() { _, _ = io.Copy(upstream, buffered); _ = upstream.Close(); close(done) }()
	_, _ = io.Copy(client, upstream)
	_ = client.Close()
	<-done
}

// 包装 Close 同时覆盖标准 HTTP 和 hijack 后的 CONNECT/WS，避免遗留连接计数。
type browserProxyListener struct {
	net.Listener
	proxy *browserProxy
}

func (l *browserProxyListener) Accept() (net.Conn, error) {
	for {
		conn, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		wrapped := &browserProxyConn{Conn: conn, proxy: l.proxy}
		l.proxy.mu.Lock()
		allowed := !l.proxy.closed && len(l.proxy.conns) < 128
		if allowed {
			l.proxy.conns[wrapped] = struct{}{}
		}
		l.proxy.mu.Unlock()
		if allowed {
			return wrapped, nil
		}
		_ = conn.Close()
	}
}

type browserProxyConn struct {
	net.Conn
	proxy *browserProxy
}

func (c *browserProxyConn) Close() error {
	err := c.Conn.Close()
	c.proxy.mu.Lock()
	delete(c.proxy.conns, c)
	c.proxy.mu.Unlock()
	return err
}

func (p *browserProxy) Close() error {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil
	}
	p.closed = true
	connections := p.conns
	p.conns = make(map[net.Conn]struct{})
	p.mu.Unlock()
	p.cancel()
	err := p.server.Close()
	for conn := range connections {
		err = errors.Join(err, conn.Close())
	}
	p.transport.CloseIdleConnections()
	return err
}
