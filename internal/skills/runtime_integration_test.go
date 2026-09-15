package skills

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudwego/eino/adk"
	einotool "github.com/cloudwego/eino/components/tool"
	"github.com/sda1-hacker/humbert-agent/internal/logging"
)

// 验证真实 Eino Middleware 注入的 skill 工具，而不只测试包解析函数。
func TestInstalledSkillLoadsThroughEinoMiddleware(t *testing.T) {
	ctx := context.Background()
	source := t.TempDir()
	writeSkillTestFile(t, source, "SKILL.md", "---\nname: integration-skill\ndescription: Integration skill.\n---\nFollow the integration instructions.")
	writeSkillTestFile(t, source, "references/example.md", "reference version one")
	m, err := NewManager(ctx, t.TempDir(), testSkillConfig(), logging.NewBootstrap())
	if err != nil {
		t.Fatal(err)
	}
	info, err := m.InstallFromDirectory(ctx, source)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.InstallFromDirectory(ctx, source); err == nil {
		t.Fatal("duplicate install accepted")
	}
	snapshot, err := m.ResolveRuntimeSnapshot(ctx, []string{info.Name})
	if err != nil {
		t.Fatal(err)
	}
	handler := snapshot.Middleware().(interface {
		BeforeAgent(context.Context, *adk.ChatModelAgentContext) (context.Context, *adk.ChatModelAgentContext, error)
	})
	_, run, err := handler.BeforeAgent(ctx, &adk.ChatModelAgentContext{})
	if err != nil || len(run.Tools) != 1 {
		t.Fatalf("middleware: %v", err)
	}
	tool, ok := run.Tools[0].(einotool.InvokableTool)
	if !ok {
		t.Fatal("skill is not invokable")
	}
	for _, test := range []struct{ args, want string }{
		{`{"skill":"integration-skill"}`, "Follow the integration instructions"},
		{`{"skill":"integration-skill","file":"references/example.md"}`, "reference version one"},
	} {
		result, err := tool.InvokableRun(ctx, test.args)
		if err != nil || !strings.Contains(result, test.want) {
			t.Fatalf("call %s: %s, %v", test.args, result, err)
		}
	}
	if _, err := tool.InvokableRun(ctx, `{"skill":"integration-skill","file":"../../outside"}`); err == nil {
		t.Fatal("path traversal accepted")
	}
	if _, err := tool.InvokableRun(ctx, `{"skill":"not-enabled"}`); err == nil {
		t.Fatal("unselected skill accepted")
	}
	if err := os.WriteFile(filepath.Join(info.RootDir, "references/example.md"), []byte("reference version two"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := tool.InvokableRun(ctx, `{"skill":"integration-skill","file":"references/example.md"}`); err == nil {
		t.Fatal("changed asset accepted by old snapshot")
	}
}
