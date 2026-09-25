package builtin

import (
	"bytes"
	"context"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sda1-hacker/humbert-agent/internal/sandbox"
	humberttools "github.com/sda1-hacker/humbert-agent/internal/tools"
	"github.com/sda1-hacker/humbert-agent/internal/workspace"
)

type screenshotAttachmentStub struct {
	sessionID string
	data      []byte
}

func (s *screenshotAttachmentStub) SaveToolImage(_ context.Context, sessionID, _, _ string, data []byte) (string, error) {
	s.sessionID = sessionID
	s.data = append([]byte(nil), data...)
	return "attachment-1", nil
}

func TestBrowserScreenshotDefaultsToConversationAttachment(t *testing.T) {
	root, err := sandbox.CanonicalRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	writer := &screenshotAttachmentStub{}
	factory := NewBrowserFactory(t.TempDir())
	factory.SetAttachmentWriter(writer)
	scope := humberttools.Scope{SessionID: "session-1", Workspace: workspace.Workspace{RootDir: root}}
	png := []byte("\x89PNG\r\n\x1a\noriginal")
	result, err := factory.persistScreenshot(context.Background(), scope, png)
	if err != nil || result.ScreenshotAttachmentID != "attachment-1" || writer.sessionID != scope.SessionID || !bytes.Equal(writer.data, png) {
		t.Fatalf("default screenshot should only be attached: result=%+v err=%v", result, err)
	}
}

func TestBrowserRejectsLocalAddress(t *testing.T) {
	if err := validateBrowserURL(context.Background(), "http://127.0.0.1:8080/private"); err == nil {
		t.Fatal("browser accepted loopback URL")
	}
	if err := validateBrowserURL(context.Background(), "file:///etc/passwd"); err == nil {
		t.Fatal("browser accepted file URL")
	}
}

func TestBrowserProfileReusedPerAgent(t *testing.T) {
	root := filepath.Join(t.TempDir(), "browser-profiles")
	first, err := persistentBrowserProfile(root, "agent-a")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(first, "cookie-marker"), []byte("kept"), 0o600); err != nil {
		t.Fatal(err)
	}
	again, err := persistentBrowserProfile(root, "agent-a")
	if err != nil || again != first {
		t.Fatalf("profile not reused: %q, %v", again, err)
	}
	other, err := persistentBrowserProfile(root, "agent-b")
	if err != nil || other == first {
		t.Fatalf("profiles not isolated: %q, %v", other, err)
	}
	if data, err := os.ReadFile(filepath.Join(again, "cookie-marker")); err != nil || string(data) != "kept" {
		t.Fatalf("profile data not retained: %q, %v", data, err)
	}
}

func TestBrowserVerificationIsForHuman(t *testing.T) {
	page := &BrowserOutput{URL: "https://wappass.baidu.com/static/captcha/tuxing_v2.html?x=1", Title: "百度安全验证"}
	markBrowserVerification(page)
	if !page.NeedsHumanVerification || !strings.Contains(page.VerificationMessage, "手动完成") {
		t.Fatalf("verification not surfaced: %+v", page)
	}
	ordinary := &BrowserOutput{URL: "https://www.baidu.com/s?wd=go", Title: "百度一下"}
	markBrowserVerification(ordinary)
	if ordinary.NeedsHumanVerification {
		t.Fatalf("ordinary page marked as verification: %+v", ordinary)
	}
}

func TestBrowserChromeSmoke(t *testing.T) {
	if os.Getenv("HUMBERT_TEST_BROWSER") != "1" {
		t.Skip("set HUMBERT_TEST_BROWSER=1 to run installed Chrome smoke test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	session, err := startBrowser(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer session.close()
	page, err := session.snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if page.URL != "about:blank" {
		t.Fatalf("unexpected page: %+v", page)
	}
	dataURL := "data:text/html," + url.PathEscape(`<html><body><input id="entry"><button id="send" onclick="document.getElementById('result').textContent=document.getElementById('entry').value">Go</button><p id="result"></p></body></html>`)
	if _, err := session.call(ctx, "Page.navigate", map[string]any{"url": dataURL}); err != nil {
		t.Fatal(err)
	}
	if err := session.waitReady(ctx, "about:blank"); err != nil {
		t.Fatal(err)
	}
	if _, err := session.evaluate(ctx, `document.querySelector('#entry').value='hello'; document.querySelector('#send').click()`); err != nil {
		t.Fatal(err)
	}
	page, err = session.snapshot(ctx)
	if err != nil || !strings.Contains(page.Text, "hello") {
		t.Fatalf("interaction=%+v err=%v", page, err)
	}
	png, err := session.captureScreenshot(ctx)
	if err != nil || !bytes.HasPrefix(png, []byte("\x89PNG\r\n\x1a\n")) {
		t.Fatalf("invalid screenshot: %v", err)
	}
}

func TestPersistentBrowserChromeSmoke(t *testing.T) {
	if os.Getenv("HUMBERT_TEST_BROWSER") != "1" {
		t.Skip("set HUMBERT_TEST_BROWSER=1 to run installed Chrome smoke test")
	}
	profile, err := persistentBrowserProfile(filepath.Join(t.TempDir(), "browser-profiles"), "agent-a")
	if err != nil {
		t.Fatal(err)
	}
	for attempt := 0; attempt < 2; attempt++ {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		session, startErr := startBrowserWithProfile(ctx, false, profile, false)
		if startErr != nil {
			cancel()
			t.Fatal(startErr)
		}
		page, snapshotErr := session.snapshot(ctx)
		session.closeGracefully()
		cancel()
		if snapshotErr != nil || page.URL != "about:blank" {
			t.Fatalf("attempt %d snapshot=%+v err=%v", attempt, page, snapshotErr)
		}
		if _, statErr := os.Stat(profile); statErr != nil {
			t.Fatalf("persistent profile disappeared: %v", statErr)
		}
	}
}

func TestVisibleBrowserChromeSmoke(t *testing.T) {
	if os.Getenv("HUMBERT_TEST_BROWSER_VISIBLE") != "1" {
		t.Skip("set HUMBERT_TEST_BROWSER_VISIBLE=1 to run visible Chrome smoke test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	session, err := startBrowserWithMode(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	defer session.close()
	page, err := session.snapshot(ctx)
	if err != nil || page.URL == "" {
		t.Fatalf("visible browser snapshot=%+v err=%v", page, err)
	}
	if _, err := session.call(ctx, "Page.navigate", map[string]any{"url": "data:text/html,<title>Humbert Browser Smoke</title><p>Visible page</p>"}); err != nil {
		t.Fatal(err)
	}
	if err := session.waitReady(ctx, page.URL); err != nil {
		t.Fatal(err)
	}
	page, err = session.snapshot(ctx)
	if err != nil || !strings.Contains(page.Text, "Visible page") {
		t.Fatalf("visible browser page=%+v err=%v", page, err)
	}
	if png, err := session.captureScreenshot(ctx); err != nil || !bytes.HasPrefix(png, []byte("\x89PNG\r\n\x1a\n")) {
		t.Fatalf("visible browser screenshot invalid: %v", err)
	}
}
