package chunker

import (
	"strings"
	"testing"
)

func TestFindHeadingBoundaries(t *testing.T) {
	text := `# 产品手册

## 安装

### Linux

正文。

## 配置

正文。
`

	boundaries := findHeadingBoundaries(text, 2)

	// primaryLevel = 2
	//
	// 应该包含:
	//
	//     H1 产品手册
	//     H2 安装
	//     H2 配置
	//
	// H3 Linux 不应该成为主 Section Boundary。
	if len(boundaries) != 3 {
		t.Fatalf("Boundary 数量错误: want=3 got=%d", len(boundaries))
	}

	if boundaries[0].headingLine != "# 产品手册" {
		t.Fatalf("第一个 Boundary 应为 H1: %q", boundaries[0].headingLine)
	}

	if boundaries[1].headingLine != "## 安装" {
		t.Fatalf("第二个 Boundary 应为安装: %q", boundaries[1].headingLine)
	}

	if boundaries[2].headingLine != "## 配置" {
		t.Fatalf("第三个 Boundary 应为配置: %q", boundaries[2].headingLine)
	}
}

func TestFindHeadingBoundariesIgnoresCodeFence(t *testing.T) {
	text := `# Real

这里是正文。

` + "```markdown" + `
## Fake
### Fake Deep
` + "```" + `

## Real Section
`

	boundaries := findHeadingBoundaries(text, 2)

	for _, boundary := range boundaries {
		if strings.Contains(boundary.headingLine, "Fake") {
			t.Fatalf("代码块中的 Heading 不应该成为 Boundary: %q", boundary.headingLine)
		}
	}
}

func TestFindHeadingBoundariesPreservesRuneOffset(t *testing.T) {
	text := "中文前言。\n\n## 安装\n正文。"

	boundaries := findHeadingBoundaries(text, 2)

	if len(boundaries) != 2 {
		t.Fatalf("预期两个 Boundary, got=%d", len(boundaries))
	}

	want := RuneLen("中文前言。\n\n")

	if boundaries[1].runeStart != want {
		t.Fatalf("Boundary 必须使用 rune offset: want=%d got=%d", want, boundaries[1].runeStart)
	}
}

func TestSplitByHeadingsBasic(t *testing.T) {
	body := strings.Repeat("这是一段用于填充分块长度的正文内容。", 15)

	text :=
		"# 产品手册\n" + body +
			"\n\n## 安装\n" + body +
			"\n\n## 配置\n" + body +
			"\n\n## 部署\n" + body

	cfg := DefaultConfig()
	cfg.ChunkSize = 300
	cfg.ChunkOverlap = 0

	chunks := splitByHeadingsImpl(text, cfg, nil)

	if len(chunks) < 3 {
		t.Fatalf("Heading Splitter 应得到多个章节 Chunk: got=%d", len(chunks))
	}

	foundInstall := false

	for _, chunk := range chunks {
		if strings.Contains(chunk.Content, "## 安装") {
			foundInstall = true

			if !strings.Contains(chunk.ContextHeader, "# 产品手册") {
				t.Fatalf("安装 Chunk 缺少 H1 ContextHeader: %q", chunk.ContextHeader)
			}

			if !strings.Contains(chunk.ContextHeader, "## 安装") {
				t.Fatalf("安装 Chunk 缺少 H2 ContextHeader: %q", chunk.ContextHeader)
			}
		}
	}

	if !foundInstall {
		t.Fatal("没有找到安装 Section Chunk")
	}
}

func TestSplitByHeadingsFallsBackWithoutStructure(t *testing.T) {
	text := "这只是一段普通正文，没有任何 Markdown Heading。"

	cfg := DefaultConfig()
	cfg.ChunkSize = 200
	cfg.ChunkOverlap = 0

	got := splitByHeadingsImpl(text, cfg, nil)
	want := SplitText(text, cfg)

	if len(got) != len(want) {
		t.Fatalf("无 Heading 文档应该回退 Legacy: want=%d got=%d", len(want), len(got))
	}
}

func TestSplitByHeadingsPreservesSourcePosition(t *testing.T) {
	text := `# 产品手册
这里是简介。

## 安装
这里是安装正文。

## 配置
这里是配置正文。

## 部署
这里是部署正文。
`

	cfg := DefaultConfig()
	cfg.ChunkSize = 200
	cfg.ChunkOverlap = 0

	chunks := splitByHeadingsImpl(text, cfg, nil)

	source := []rune(text)

	for i, chunk := range chunks {
		if chunk.Start < 0 || chunk.End < chunk.Start || chunk.End > len(source) {
			t.Fatalf("Chunk[%d] Source Range 非法: [%d,%d)", i, chunk.Start, chunk.End)
		}

		// Heading Section 没有 synthetic table header 时，
		// 必须满足完整 Source Mapping。
		got := string(source[chunk.Start:chunk.End])

		if got != chunk.Content {
			t.Fatalf(
				"Chunk[%d] Source Mapping 错误\nsource=%q\nchunk =%q",
				i,
				got,
				chunk.Content,
			)
		}
	}
}

func TestSplitByHeadingsCoalescesTinySections(t *testing.T) {
	text := `# 安装日志

## Docker
使用 daocloud 部署。

## 浏览器缓存
浏览器使用了旧资源。

## 登录
数据库字段缺失。

## 解析
Embedding 表缺少列。
`

	cfg := DefaultConfig()
	cfg.ChunkSize = 500
	cfg.ChunkOverlap = 0

	chunks := splitByHeadingsImpl(text, cfg, nil)

	// 原始结构有 1 个 H1 + 4 个 H2，
	// 如果完全不 Coalesce，很容易得到 5 个很小 Chunk。
	//
	// 合并后应该明显少于这个数量。
	if len(chunks) >= 5 {
		t.Fatalf("Tiny Section 应被合并: got=%d", len(chunks))
	}

	for i, chunk := range chunks {
		if !strings.Contains(chunk.ContextHeader, "# 安装日志") {
			t.Fatalf("合并 Chunk[%d] 应保留共享 H1: %q", i, chunk.ContextHeader)
		}
	}

	// H2 Heading 本身仍然存在于 Content 中，
	// Coalesce 不能把原始结构文本丢掉。
	for _, heading := range []string{
		"## Docker",
		"## 浏览器缓存",
		"## 登录",
		"## 解析",
	} {
		found := false

		for _, chunk := range chunks {
			if strings.Contains(chunk.Content, heading) {
				found = true
				break
			}
		}

		if !found {
			t.Fatalf("合并后丢失 Heading: %q", heading)
		}
	}
}

func TestSplitByHeadingsDoesNotMergeDifferentTopLevelSections(t *testing.T) {
	text := `# Introduction
短内容。

# Usage
短内容。

# FAQ
短内容。
`

	cfg := DefaultConfig()
	cfg.ChunkSize = 500
	cfg.ChunkOverlap = 0

	chunks := splitByHeadingsImpl(text, cfg, nil)

	if len(chunks) != 3 {
		t.Fatalf("不同 H1 不应该被合并: want=3 got=%d", len(chunks))
	}

	expected := []string{"# Introduction", "# Usage", "# FAQ"}

	for i, heading := range expected {
		if !strings.Contains(chunks[i].Content, heading) {
			t.Fatalf("Chunk[%d] 应包含 %q", i, heading)
		}
	}
}

func TestCommonHeadingPrefix(t *testing.T) {
	tests := []struct {
		a    string
		b    string
		want string
	}{
		{
			a:    "# Doc\n## Install",
			b:    "# Doc\n## Config",
			want: "# Doc",
		},
		{
			a:    "# Doc\n## Install\n### Linux",
			b:    "# Doc\n## Install\n### Windows",
			want: "# Doc\n## Install",
		},
		{
			a:    "# Doc",
			b:    "# Doc",
			want: "# Doc",
		},
		{
			a:    "# A",
			b:    "# B",
			want: "",
		},
	}

	for _, tt := range tests {
		got := commonHeadingPrefix(tt.a, tt.b)

		if got != tt.want {
			t.Fatalf(
				"commonHeadingPrefix(%q, %q)\nwant=%q\ngot =%q",
				tt.a,
				tt.b,
				tt.want,
				got,
			)
		}
	}
}

func TestSplitByHeadingsDeepBreadcrumbInsideLargeSection(t *testing.T) {
	filler := strings.Repeat(
		"这里是一段用于扩充分块长度的条款正文内容。 ",
		30,
	)

	text :=
		"# 技术标准\n" +
			"## 前言\n" + filler + "\n\n" +
			"## 5 分类\n" + filler + "\n\n" +
			"### 5.9 九级\n" + filler + "\n\n" +
			"#### 5.9.2 特殊条款\n" + filler + "\n\n" +
			"这里包含用户真正搜索的 MARKER_ITEM_23。\n\n" +
			"## 附录 A\n" + filler + "\n\n" +
			"## 附录 B\n" + filler

	cfg := DefaultConfig()
	cfg.ChunkSize = 300
	cfg.ChunkOverlap = 0
	cfg.Separators = []string{"。", "\n"}

	chunks := splitByHeadingsImpl(text, cfg, nil)

	var markerChunk *Chunk

	for i := range chunks {
		if strings.Contains(chunks[i].Content, "MARKER_ITEM_23") {
			markerChunk = &chunks[i]
			break
		}
	}

	if markerChunk == nil {
		t.Fatal("没有找到包含 MARKER_ITEM_23 的 Chunk")
	}

	if !strings.Contains(markerChunk.ContextHeader, "## 5 分类") {
		t.Fatalf(
			"Marker Chunk 丢失主 Section Heading:\n%s",
			markerChunk.ContextHeader,
		)
	}

	if !strings.Contains(markerChunk.ContextHeader, "### 5.9 九级") {
		t.Fatalf(
			"Marker Chunk 丢失 H3 Heading:\n%s",
			markerChunk.ContextHeader,
		)
	}

	if !strings.Contains(markerChunk.ContextHeader, "#### 5.9.2 特殊条款") {
		t.Fatalf(
			"Marker Chunk 丢失最深 H4 Heading:\n%s",
			markerChunk.ContextHeader,
		)
	}
}

func TestSplitExplicitHeadingUsesHeadingSplitter(t *testing.T) {
	body := strings.Repeat("这是一段足够长的正文内容。", 20)

	text :=
		"# 文档\n" +
			"## 第一节\n" + body + "\n" +
			"## 第二节\n" + body + "\n" +
			"## 第三节\n" + body

	cfg := DefaultConfig()
	cfg.Strategy = StrategyHeading
	cfg.ChunkSize = 300
	cfg.ChunkOverlap = 20

	chunks, diag := SplitWithDiagnostics(text, cfg)

	if len(chunks) == 0 {
		t.Fatal("Heading Strategy 不应该返回空结果")
	}

	if diag.SelectedTier != TierHeading {
		t.Fatalf(
			"Heading 实现注册后应真正选择 TierHeading: got=%s rejected=%v",
			diag.SelectedTier,
			diag.Rejected,
		)
	}

	foundContext := false

	for _, chunk := range chunks {
		if chunk.ContextHeader != "" {
			foundContext = true
			break
		}
	}

	if !foundContext {
		t.Fatal("Heading Splitter 产生的 Chunk 应具有 ContextHeader")
	}
}
