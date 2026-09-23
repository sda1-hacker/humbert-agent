package builtin

import (
	"context"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

func TestBrowserRejectsLocalAddress(t *testing.T) {
	if err := validateBrowserURL(context.Background(), "http://127.0.0.1:8080/private"); err == nil {
		t.Fatal("browser accepted loopback URL")
	}
	if err := validateBrowserURL(context.Background(), "file:///etc/passwd"); err == nil {
		t.Fatal("browser accepted file URL")
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
}
