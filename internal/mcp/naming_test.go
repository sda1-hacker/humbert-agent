package mcp

import (
	"strings"
	"testing"
)

func TestNameExposedToolKeepsReadableSnakeCase(t *testing.T) {
	name, err := NameExposedTool("github", "search_issues")
	if err != nil {
		t.Fatalf("NameExposedTool 失败: %v", err)
	}
	if name != "mcp_github_search_issues" {
		t.Fatalf("name = %q", name)
	}
}

func TestNameExposedToolAvoidsNormalizedCollision(t *testing.T) {
	a, err := NameExposedTool("github", "search-issues")
	if err != nil {
		t.Fatal(err)
	}
	b, err := NameExposedTool("github", "search_issues")
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Fatalf("normalized names collided: %q", a)
	}
	if !strings.HasPrefix(a, "mcp_github_search_issues_") {
		t.Fatalf("unexpected normalized name: %q", a)
	}
}

func TestNameExposedToolCapsLength(t *testing.T) {
	name, err := NameExposedTool("github", strings.Repeat("very_long_tool_name_", 8))
	if err != nil {
		t.Fatal(err)
	}
	if len(name) > 64 {
		t.Fatalf("name too long: %d %q", len(name), name)
	}
}
