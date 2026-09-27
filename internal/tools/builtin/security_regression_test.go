package builtin

import (
	"context"
	"github.com/cloudwego/eino/adk/filesystem"
	"github.com/sda1-hacker/humbert-agent/internal/sandbox"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRecursiveSearchHonorsBlockedDescendant(t *testing.T) {
	b := testFilesystem(t)
	root := b.scope.Workspace.RootDir
	protected := filepath.Join(root, "blocked")
	if err := os.Mkdir(protected, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(protected, "secret.txt"), []byte("AUDIT_FAKE_SECRET"), 0600); err != nil {
		t.Fatal(err)
	}
	p := sandbox.WorkspaceOnlyPolicy(root)
	p.PathRules = append(p.PathRules, sandbox.PathRule{Root: protected, Access: sandbox.AccessBlocked, Source: sandbox.RuleSourceProtected})
	b.scope.Sandbox = p
	if _, err := b.Read(context.Background(), &filesystem.ReadRequest{FilePath: "blocked/secret.txt"}); err == nil {
		t.Fatal("fixture error: direct read must be denied")
	}
	matches, err := b.GrepRaw(context.Background(), &filesystem.GrepRequest{Path: ".", Pattern: "AUDIT_FAKE_SECRET"})
	if err != nil {
		t.Fatal(err)
	}
	visible := filepath.Join(root, "visible.txt")
	if err := os.WriteFile(visible, []byte("public"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{".", root} {
		entries, err := b.GlobInfo(context.Background(), &filesystem.GlobInfoRequest{Path: path, Pattern: "**"})
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 1 || entries[0].Path != "visible.txt" {
			t.Fatalf("blocked glob entry: %+v", entries)
		}
		listed, err := b.LsInfo(context.Background(), &filesystem.LsInfoRequest{Path: path})
		if err != nil {
			t.Fatal(err)
		}
		if len(listed) != 1 || listed[0].Path != "visible.txt" {
			t.Fatalf("blocked list entry: %+v", listed)
		}
	}
	if len(matches) > 0 {
		t.Fatalf("direct read denied, recursive grep returned protected content: %+v", matches)
	}
}

func TestBrowserHonorsNetworkNone(t *testing.T) {
	b := testFilesystem(t)
	b.scope.SessionID = "audit-session"
	b.scope.Sandbox = sandbox.WorkspaceOnlyPolicy(b.scope.Workspace.RootDir)
	b.scope.Sandbox.NetworkMode = sandbox.NetworkNone
	// 用不存在的可执行路径确保拒绝发生在启动 Chrome 之前，不启动浏览器或联网。
	t.Setenv("HUMBERT_BROWSER_CHROME_PATH", filepath.Join(t.TempDir(), "chrome-not-present"))
	factory := NewBrowserFactory(t.TempDir())
	defer factory.Close()
	tool, err := factory.Build(context.Background(), b.scope)
	if err != nil {
		t.Fatal(err)
	}
	// 模拟已存在的会话槽位，禁网也必须拒绝复用并清理该槽位。
	previous := &browserSession{}
	previous.closed.Store(true)
	factory.sessions[b.scope.AgentID] = previous
	for _, action := range []string{"open", "snapshot", "click", "type", "scroll", "press", "back", "forward", "refresh", "show", "screenshot"} {
		_, err = tool.InvokableRun(context.Background(), `{"action":"`+action+`","url":"https://8.8.8.8/"}`)
		if err == nil || !strings.Contains(err.Error(), "禁用网络") {
			t.Fatalf("action=%s: NetworkNone not enforced: %v", action, err)
		}
	}
	if len(factory.sessions) != 0 {
		t.Fatal("network-disabled call retained previous browser")
	}
	if _, err = tool.InvokableRun(context.Background(), `{"action":"close"}`); err != nil {
		t.Fatal(err)
	}
}
