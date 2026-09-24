package builtin

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	einotool "github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/coder/websocket"
	"github.com/google/uuid"
	"github.com/sda1-hacker/humbert-agent/internal/sandbox"
	humberttools "github.com/sda1-hacker/humbert-agent/internal/tools"
)

const (
	browserToolName           = "browser"
	maxBrowserScreenshotBytes = 8 << 20
)

type BrowserInput struct {
	Action   string `json:"action" jsonschema:"description=One of open snapshot click type screenshot close. Use screenshot to capture the current web page, not run_command."`
	URL      string `json:"url,omitempty" jsonschema:"description=Public HTTP or HTTPS URL, required for open."`
	Selector string `json:"selector,omitempty" jsonschema:"description=CSS selector, required for click or type."`
	Text     string `json:"text,omitempty" jsonschema:"description=Text to enter when action is type."`
}

type BrowserOutput struct {
	URL            string           `json:"url,omitempty"`
	Title          string           `json:"title,omitempty"`
	Text           string           `json:"text,omitempty"`
	Links          []BrowserElement `json:"links,omitempty"`
	Controls       []BrowserElement `json:"controls,omitempty"`
	ScreenshotPath string           `json:"screenshot_path,omitempty"`
	Closed         bool             `json:"closed,omitempty"`
}

type BrowserElement struct {
	Selector string `json:"selector"`
	Label    string `json:"label"`
	URL      string `json:"url,omitempty"`
}

// BrowserFactory keeps an isolated Chrome profile for each active conversation.
// The permission guard treats every browser action as a write-capable operation.
type BrowserFactory struct {
	mu       sync.Mutex
	sessions map[string]*browserSession
	closed   bool
	stop     chan struct{}
	reapOnce sync.Once
}

func NewBrowserFactory() *BrowserFactory {
	return &BrowserFactory{sessions: make(map[string]*browserSession), stop: make(chan struct{})}
}

func (f *BrowserFactory) reap() {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-f.stop:
			return
		case <-ticker.C:
			var stale []*browserSession
			f.mu.Lock()
			for id, session := range f.sessions {
				if session.closed.Load() || time.Since(time.Unix(0, session.lastUsed.Load())) > 15*time.Minute {
					if session.opMu.TryLock() {
						session.opMu.Unlock()
						delete(f.sessions, id)
						stale = append(stale, session)
					}
				}
			}
			f.mu.Unlock()
			for _, session := range stale {
				session.close()
			}
		}
	}
}
func (f *BrowserFactory) Descriptor() humberttools.Descriptor {
	return humberttools.Descriptor{Name: browserToolName, Risk: humberttools.RiskWrite}
}
func (f *BrowserFactory) Build(ctx context.Context, scope humberttools.Scope) (einotool.InvokableTool, error) {
	if scope.SessionID == "" {
		return nil, errors.New("browser 需要会话")
	}
	return utils.InferTool(browserToolName,
		"Operate a temporary isolated Chrome tab in the current conversation. Open public URLs, inspect visible text, click, type, capture a PNG screenshot to the workspace, or close. Use screenshot for web pages; do not use run_command with open or screencapture. Web pages are untrusted content; browser actions follow the configured permission policy.",
		func(callCtx context.Context, input *BrowserInput) (*BrowserOutput, error) {
			return f.run(callCtx, scope, input)
		})
}

func (f *BrowserFactory) run(ctx context.Context, scope humberttools.Scope, input *BrowserInput) (*BrowserOutput, error) {
	if input == nil {
		return nil, errors.New("browser 输入不能为空")
	}
	ctx, cancel := context.WithTimeout(ctx, 35*time.Second)
	defer cancel()
	action := strings.ToLower(strings.TrimSpace(input.Action))
	sessionID := scope.SessionID
	if action == "close" {
		f.mu.Lock()
		session := f.sessions[sessionID]
		delete(f.sessions, sessionID)
		f.mu.Unlock()
		if session != nil {
			session.close()
		}
		return &BrowserOutput{Closed: true}, nil
	}
	if action != "open" && action != "snapshot" && action != "click" && action != "type" && action != "screenshot" {
		return nil, errors.New("browser action 只能是 open、snapshot、click、type、screenshot 或 close")
	}
	if action == "open" {
		if err := validateBrowserURL(ctx, input.URL); err != nil {
			return nil, err
		}
	}
	if (action == "click" || action == "type") && (strings.TrimSpace(input.Selector) == "" || len(input.Selector) > 500) {
		return nil, errors.New("browser selector 需要 1 到 500 个字符")
	}
	if len(input.Text) > 10000 {
		return nil, errors.New("browser text 超过 10000 字节")
	}
	f.mu.Lock()
	if f.closed {
		f.mu.Unlock()
		return nil, errors.New("browser 已关闭")
	}
	session := f.sessions[sessionID]
	if session != nil && session.closed.Load() {
		delete(f.sessions, sessionID)
		session = nil
	}
	if session == nil {
		if action != "open" {
			f.mu.Unlock()
			return nil, errors.New("请先用 browser open 打开网页")
		}
		if len(f.sessions) >= 3 {
			f.mu.Unlock()
			return nil, errors.New("最多同时打开三个会话浏览器，请先关闭不用的浏览器")
		}
		var err error
		session, err = startBrowser(ctx)
		if err != nil {
			f.mu.Unlock()
			return nil, err
		}
		f.sessions[sessionID] = session
		f.reapOnce.Do(func() { go f.reap() })
	}
	f.mu.Unlock()
	session.opMu.Lock()
	defer session.opMu.Unlock()
	session.lastUsed.Store(time.Now().UnixNano())
	if action == "open" {
		previousValue, _ := session.evaluate(ctx, `location.href`)
		var previousURL string
		_ = json.Unmarshal(previousValue, &previousURL)
		if _, err := session.call(ctx, "Page.navigate", map[string]any{"url": input.URL}); err != nil {
			return nil, err
		}
		if err := session.waitReady(ctx, previousURL); err != nil {
			return nil, err
		}
	}
	if action == "click" || action == "type" {
		selector, _ := json.Marshal(input.Selector)
		var script string
		if action == "click" {
			script = fmt.Sprintf(`(() => { const e=document.querySelector(%s); if(!e) throw Error('找不到元素'); e.click(); return true })()`, selector)
		} else {
			value, _ := json.Marshal(input.Text)
			script = fmt.Sprintf(`(() => { const e=document.querySelector(%s); if(!e) throw Error('找不到元素'); e.focus(); const setter=Object.getOwnPropertyDescriptor(e.tagName==='TEXTAREA'?HTMLTextAreaElement.prototype:HTMLInputElement.prototype,'value')?.set; if(setter) setter.call(e,%s); else e.value=%s; e.dispatchEvent(new Event('input',{bubbles:true})); e.dispatchEvent(new Event('change',{bubbles:true})); return true })()`, selector, value, value)
		}
		if _, err := session.evaluate(ctx, script); err != nil {
			return nil, err
		}
	}
	if action == "screenshot" {
		page, err := session.pageIdentity(ctx)
		if err != nil {
			return nil, err
		}
		if err := validateBrowserURL(ctx, page.URL); err != nil {
			return nil, fmt.Errorf("网页已离开允许访问的公网地址: %w", err)
		}
		png, err := session.captureScreenshot(ctx)
		if err != nil {
			return nil, err
		}
		path, err := saveBrowserScreenshot(ctx, scope, png)
		if err != nil {
			return nil, err
		}
		return &BrowserOutput{URL: page.URL, Title: page.Title, ScreenshotPath: path}, nil
	}
	output, err := session.snapshot(ctx)
	if err != nil {
		return nil, err
	}
	if err := validateBrowserURL(ctx, output.URL); err != nil {
		f.mu.Lock()
		if f.sessions[sessionID] == session {
			delete(f.sessions, sessionID)
		}
		f.mu.Unlock()
		session.close()
		return nil, fmt.Errorf("网页已离开允许访问的公网地址: %w", err)
	}
	session.lastUsed.Store(time.Now().UnixNano())
	return output, nil
}

func (f *BrowserFactory) Close() error {
	f.mu.Lock()
	if f.closed {
		f.mu.Unlock()
		return nil
	}
	f.closed = true
	close(f.stop)
	sessions := f.sessions
	f.sessions = make(map[string]*browserSession)
	f.mu.Unlock()
	for _, session := range sessions {
		session.close()
	}
	return nil
}

func validateBrowserURL(ctx context.Context, raw string) error {
	parsed, err := parsePublicFetchURL(raw)
	if err != nil {
		return err
	}
	if len(raw) > 4096 {
		return errors.New("browser URL 超过 4096 字节")
	}
	_, err = resolvePublicIPs(ctx, parsed.Hostname())
	return err
}

type browserSession struct {
	cmd      *exec.Cmd
	profile  string
	conn     *websocket.Conn
	opMu     sync.Mutex
	writeMu  sync.Mutex
	mu       sync.Mutex
	pending  map[int64]chan cdpMessage
	nextID   atomic.Int64
	lastUsed atomic.Int64
	closed   atomic.Bool
}

type cdpMessage struct {
	ID     int64           `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func chromePath() string {
	if configured := strings.TrimSpace(os.Getenv("HUMBERT_BROWSER_CHROME_PATH")); configured != "" {
		return configured
	}
	switch runtime.GOOS {
	case "darwin":
		return "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"
	case "windows":
		return `C:\Program Files\Google\Chrome\Application\chrome.exe`
	default:
		return "google-chrome"
	}
}

func startBrowser(ctx context.Context) (*browserSession, error) {
	profile, err := os.MkdirTemp("", "humbert-browser-*")
	if err != nil {
		return nil, err
	}
	path := chromePath()
	cmd := exec.Command(path, "--headless=new", "--no-first-run", "--no-default-browser-check", "--disable-extensions", "--disable-sync", "--disable-background-networking", "--remote-debugging-address=127.0.0.1", "--remote-debugging-port=0", "--user-data-dir="+profile, "about:blank")
	cmd.Stdout, cmd.Stderr = nil, nil
	if err := cmd.Start(); err != nil {
		os.RemoveAll(profile)
		return nil, fmt.Errorf("启动 Chrome 失败: %w", err)
	}
	cleanup := func() { _ = cmd.Process.Kill(); _ = cmd.Wait(); _ = os.RemoveAll(profile) }
	var port string
	startupDeadline := time.Now().Add(5 * time.Second)
	for {
		data, readErr := os.ReadFile(filepath.Join(profile, "DevToolsActivePort"))
		if readErr == nil {
			port = strings.TrimSpace(strings.SplitN(string(data), "\n", 2)[0])
			break
		}
		if err := ctx.Err(); err != nil {
			cleanup()
			return nil, fmt.Errorf("等待 Chrome 调试端口失败: %w", err)
		}
		if time.Now().After(startupDeadline) {
			cleanup()
			return nil, errors.New("Chrome 未能在 5 秒内启动调试端口")
		}
		time.Sleep(50 * time.Millisecond)
	}
	if _, err := strconv.Atoi(port); err != nil {
		cleanup()
		return nil, errors.New("Chrome 返回无效调试端口")
	}
	client := &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{Proxy: nil}}
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://127.0.0.1:"+port+"/json/list", nil)
	response, err := client.Do(request)
	if err != nil {
		cleanup()
		return nil, err
	}
	var targets []struct {
		Type                 string `json:"type"`
		WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
	}
	err = json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&targets)
	response.Body.Close()
	if err != nil {
		cleanup()
		return nil, err
	}
	var endpoint string
	for _, target := range targets {
		if target.Type == "page" {
			endpoint = target.WebSocketDebuggerURL
			break
		}
	}
	if endpoint == "" {
		cleanup()
		return nil, errors.New("Chrome 没有可用网页标签")
	}
	conn, _, err := websocket.Dial(ctx, endpoint, &websocket.DialOptions{HTTPClient: client})
	if err != nil {
		cleanup()
		return nil, err
	}
	// 8 MiB PNG 的 Base64 CDP 消息可接近 11 MiB。
	conn.SetReadLimit(12 << 20)
	session := &browserSession{cmd: cmd, profile: profile, conn: conn, pending: make(map[int64]chan cdpMessage)}
	session.lastUsed.Store(time.Now().UnixNano())
	go session.readLoop()
	if _, err := session.call(ctx, "Page.enable", nil); err != nil {
		session.close()
		return nil, err
	}
	if _, err := session.call(ctx, "Runtime.enable", nil); err != nil {
		session.close()
		return nil, err
	}
	// 固定较清晰的桌面视口；截图保持 PNG 原始像素，不经过文字提取或缩放。
	if _, err := session.call(ctx, "Emulation.setDeviceMetricsOverride", map[string]any{"width": 1280, "height": 800, "deviceScaleFactor": 2, "mobile": false}); err != nil {
		session.close()
		return nil, err
	}
	if _, err := session.call(ctx, "Fetch.enable", map[string]any{"patterns": []map[string]string{{"urlPattern": "*"}}}); err != nil {
		session.close()
		return nil, err
	}
	return session, nil
}

func (s *browserSession) readLoop() {
	for {
		_, data, err := s.conn.Read(context.Background())
		if err != nil {
			s.close()
			return
		}
		var message cdpMessage
		if json.Unmarshal(data, &message) != nil {
			continue
		}
		if message.Method == "Fetch.requestPaused" {
			var paused struct {
				RequestID string `json:"requestId"`
				Request   struct {
					URL string `json:"url"`
				} `json:"request"`
			}
			if json.Unmarshal(message.Params, &paused) == nil {
				go func() {
					ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					defer cancel()
					method := "Fetch.continueRequest"
					params := map[string]any{"requestId": paused.RequestID}
					if err := validateBrowserURL(ctx, paused.Request.URL); err != nil {
						method = "Fetch.failRequest"
						params["errorReason"] = "BlockedByClient"
					}
					_, _ = s.call(ctx, method, params)
				}()
			}
			continue
		}
		if message.ID != 0 {
			s.mu.Lock()
			ch := s.pending[message.ID]
			delete(s.pending, message.ID)
			s.mu.Unlock()
			if ch != nil {
				ch <- message
			}
		}
	}
}

func (s *browserSession) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	if s.closed.Load() {
		return nil, errors.New("浏览器连接已关闭")
	}
	id := s.nextID.Add(1)
	ch := make(chan cdpMessage, 1)
	s.mu.Lock()
	s.pending[id] = ch
	s.mu.Unlock()
	defer func() { s.mu.Lock(); delete(s.pending, id); s.mu.Unlock() }()
	request, err := json.Marshal(map[string]any{"id": id, "method": method, "params": params})
	if err != nil {
		return nil, err
	}
	s.writeMu.Lock()
	err = s.conn.Write(ctx, websocket.MessageText, request)
	s.writeMu.Unlock()
	if err != nil {
		return nil, err
	}
	select {
	case response := <-ch:
		if response.Error != nil {
			return nil, errors.New(response.Error.Message)
		}
		return response.Result, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (s *browserSession) evaluate(ctx context.Context, script string) (json.RawMessage, error) {
	result, err := s.call(ctx, "Runtime.evaluate", map[string]any{"expression": script, "returnByValue": true, "awaitPromise": true})
	if err != nil {
		return nil, err
	}
	var value struct {
		Result struct {
			Value json.RawMessage `json:"value"`
		} `json:"result"`
		ExceptionDetails *struct {
			Text string `json:"text"`
		} `json:"exceptionDetails"`
	}
	if err := json.Unmarshal(result, &value); err != nil {
		return nil, err
	}
	if value.ExceptionDetails != nil {
		return nil, errors.New(value.ExceptionDetails.Text)
	}
	return value.Result.Value, nil
}

func (s *browserSession) waitReady(ctx context.Context, previousURL string) error {
	started := time.Now()
	for {
		value, err := s.evaluate(ctx, `({url:location.href,ready:document.readyState==='complete'})`)
		if err == nil {
			var state struct {
				URL   string `json:"url"`
				Ready bool   `json:"ready"`
			}
			if json.Unmarshal(value, &state) == nil && state.Ready && (state.URL != previousURL || time.Since(started) > 300*time.Millisecond) {
				return nil
			}
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func (s *browserSession) snapshot(ctx context.Context) (*BrowserOutput, error) {
	const script = `(() => { const selector=(el)=>{if(el.id)return '#'+CSS.escape(el.id);const parts=[];for(let e=el;e&&e!==document.documentElement;e=e.parentElement){let p=e.tagName.toLowerCase();let n=1;for(let x=e.previousElementSibling;x;x=x.previousElementSibling)if(x.tagName===e.tagName)n++;p+=':nth-of-type('+n+')';parts.unshift(p)}return 'html>'+parts.join('>')};const item=(e)=>({selector:selector(e),label:(e.innerText||e.getAttribute('aria-label')||e.getAttribute('placeholder')||'').trim().slice(0,100),url:e.href||''});return {url:location.href,title:document.title,text:(document.body?.innerText||'').slice(0,20000),links:Array.from(document.querySelectorAll('a[href]')).slice(0,60).map(item),controls:Array.from(document.querySelectorAll('button,input,textarea,select')).slice(0,60).map(item)}})()`
	value, err := s.evaluate(ctx, script)
	if err != nil {
		return nil, err
	}
	var output BrowserOutput
	if err := json.Unmarshal(value, &output); err != nil {
		return nil, err
	}
	return &output, nil
}

func (s *browserSession) pageIdentity(ctx context.Context) (*BrowserOutput, error) {
	value, err := s.evaluate(ctx, `({url:location.href,title:document.title})`)
	if err != nil {
		return nil, err
	}
	var page BrowserOutput
	if err := json.Unmarshal(value, &page); err != nil {
		return nil, err
	}
	return &page, nil
}

func (s *browserSession) captureScreenshot(ctx context.Context) ([]byte, error) {
	result, err := s.call(ctx, "Page.captureScreenshot", map[string]any{"format": "png", "fromSurface": true, "captureBeyondViewport": false})
	if err != nil {
		return nil, fmt.Errorf("网页截图失败: %w", err)
	}
	var payload struct {
		Data string `json:"data"`
	}
	if err := json.Unmarshal(result, &payload); err != nil {
		return nil, fmt.Errorf("解析网页截图失败: %w", err)
	}
	if base64.StdEncoding.DecodedLen(len(payload.Data)) > maxBrowserScreenshotBytes {
		return nil, errors.New("网页截图超过 8 MiB，请缩小页面后重试")
	}
	png, err := base64.StdEncoding.DecodeString(payload.Data)
	if err != nil || len(png) == 0 || len(png) > maxBrowserScreenshotBytes || !bytes.HasPrefix(png, []byte("\x89PNG\r\n\x1a\n")) {
		return nil, errors.New("Chrome 返回的网页截图不是有效 PNG")
	}
	return png, nil
}

// 截图只写入当前 Agent 的工作区；通过同一 PathGuard 和 os.Root 限制写入范围。
func saveBrowserScreenshot(ctx context.Context, scope humberttools.Scope, png []byte) (string, error) {
	path := filepath.Join("screenshots", time.Now().UTC().Format("20060102-150405")+"-"+uuid.NewString()+".png")
	target, err := openSandboxTarget(ctx, scope, path, sandbox.OpModify)
	if err != nil {
		return "", fmt.Errorf("截图保存路径被 Sandbox 拒绝: %w", err)
	}
	defer target.Close()
	if err := ensureRootParentDirectory(target.root, target.relative); err != nil {
		return "", fmt.Errorf("创建截图目录失败: %w", err)
	}
	file, err := target.root.OpenFile(target.relative, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", fmt.Errorf("创建截图文件失败: %w", err)
	}
	if err := writeAllWithContext(ctx, file, png); err != nil {
		file.Close()
		_ = target.root.Remove(target.relative)
		return "", fmt.Errorf("保存截图失败: %w", err)
	}
	if err := file.Sync(); err != nil {
		file.Close()
		_ = target.root.Remove(target.relative)
		return "", fmt.Errorf("同步截图文件失败: %w", err)
	}
	if err := file.Close(); err != nil {
		_ = target.root.Remove(target.relative)
		return "", fmt.Errorf("关闭截图文件失败: %w", err)
	}
	return target.display, nil
}

func (s *browserSession) close() {
	if !s.closed.CompareAndSwap(false, true) {
		return
	}
	_ = s.conn.Close(websocket.StatusNormalClosure, "closing")
	if s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
		_ = s.cmd.Wait()
	}
	_ = os.RemoveAll(s.profile)
}
