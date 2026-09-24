package runtime

import (
	"strings"
	"testing"
	"time"

	"github.com/sda1-hacker/humbert-agent/internal/contextengine"
	"github.com/sda1-hacker/humbert-agent/internal/tools"
	"github.com/sda1-hacker/humbert-agent/internal/workspace"
)

func TestRuntimePromptOnlyReferencesAvailableHelperTools(t *testing.T) {
	build := func(names ...string) string {
		descriptors := make([]tools.Descriptor, 0, len(names))
		for _, name := range names {
			descriptors = append(descriptors, tools.Descriptor{Name: name})
		}
		return buildRuntimeInstruction("Agent", "", workspace.Workspace{}, descriptors, time.Now())
	}
	withoutHelpers := build("schedule_task", "run_agent")
	if strings.Contains(withoutHelpers, "get_current_time") || strings.Contains(withoutHelpers, "list_agents") {
		t.Fatal("prompt instructs the model to call disabled helper tools")
	}
	withHelpers := build("schedule_task", "get_current_time", "run_agent", "list_agents")
	if !strings.Contains(withHelpers, "先用 get_current_time") || !strings.Contains(withHelpers, "list_agents 选择目标") {
		t.Fatal("prompt omitted available helper tools")
	}
	readOnly := build("read_file", "run_command")
	if strings.Contains(readOnly, "用 write_file") || strings.Contains(readOnly, "用 edit_file") {
		t.Fatal("prompt referenced disabled file tools")
	}
	if !strings.Contains(readOnly, "无法生成网页截图") {
		t.Fatal("prompt should explain missing browser screenshot capability")
	}
	withFiles := build("run_command", "list_files", "glob_files")
	if !strings.Contains(withFiles, "不要为 ls 或 pwd 调用 run_command") {
		t.Fatal("prompt should route directory listing to the file tool")
	}
	withBrowser := build("browser", "run_command")
	if !strings.Contains(withBrowser, "screenshot 动作") || strings.Contains(withBrowser, "当前没有 browser") {
		t.Fatal("prompt should route web screenshots to browser")
	}
}

func TestRuntimePromptTokenBudget(t *testing.T) {
	names := []string{"web_search", "web_fetch", "install_skill", "schedule_task", "get_current_time", "read_file", "write_file", "edit_file", "run_command", "run_agent", "list_agents", "apply_patch", "browser", "session_history", "extract_document"}
	descriptors := make([]tools.Descriptor, 0, len(names))
	for _, name := range names {
		descriptors = append(descriptors, tools.Descriptor{Name: name})
	}
	prompt := buildRuntimeInstruction("Humbert", "", workspace.Workspace{}, descriptors, time.Date(2026, 9, 24, 12, 0, 0, 0, time.Local))
	tokens := contextengine.NewApproxEstimator().EstimateText(prompt)
	t.Logf("runtime prompt: %d estimated tokens, %d bytes", tokens, len(prompt))
	if tokens > 750 {
		t.Fatalf("runtime policy grew beyond the fixed prompt budget: %d", tokens)
	}
	if strings.Contains(prompt, "<available_tools>") || strings.Contains(prompt, "<current_date>") {
		t.Fatal("runtime prompt repeats tool schemas or the date already present in current_time")
	}
}
