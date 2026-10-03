package chunker

import (
	"strings"
	"testing"
)

// TestSplitParentChildEmptyText
//
// 空文档应该得到空 ParentChildResult。
func TestSplitParentChildEmptyText(t *testing.T) {
	result := SplitParentChild("", DefaultConfig(), DefaultConfig())

	if len(result.Parents) != 0 {
		t.Fatalf("空文档不应该产生 Parent: got=%d", len(result.Parents))
	}

	if len(result.Children) != 0 {
		t.Fatalf("空文档不应该产生 Child: got=%d", len(result.Children))
	}
}

// TestSplitParentChildCreatesParentsAndChildren
//
// 验证最基本的父子关系。
func TestSplitParentChildCreatesParentsAndChildren(t *testing.T) {
	text := strings.Repeat(
		"这是用于 Parent Child 分块测试的正文内容。这里继续增加一些内容。\n\n",
		100,
	)

	parentCfg := SplitterConfig{
		ChunkSize:    1000,
		ChunkOverlap: 100,
		Separators:   DefaultSeparators(),
		Strategy:     StrategyLegacy,
	}

	childCfg := SplitterConfig{
		ChunkSize:    250,
		ChunkOverlap: 50,
		Separators:   DefaultSeparators(),
		Strategy:     StrategyLegacy,
	}

	result := SplitParentChild(text, parentCfg, childCfg)

	if len(result.Parents) == 0 {
		t.Fatal("长文档应该产生 Parent")
	}

	if len(result.Children) <= len(result.Parents) {
		t.Fatalf(
			"Child 数量通常应该大于 Parent: parents=%d children=%d",
			len(result.Parents),
			len(result.Children),
		)
	}

	for i, child := range result.Children {
		if child.ParentIndex < 0 {
			continue
		}

		if child.ParentIndex >= len(result.Parents) {
			t.Fatalf(
				"Child[%d] ParentIndex 越界: index=%d parents=%d",
				i,
				child.ParentIndex,
				len(result.Parents),
			)
		}
	}
}

// TestSplitParentChildChildSequenceIsGlobal
//
// Child.Seq 必须在整个 Document 范围内连续，
//
// 不能：
//
//	Parent0:
//	    0 1 2
//
//	Parent1:
//	    0 1 2
//
// 而应该：
//
//	0 1 2 3 4 5 ...
func TestSplitParentChildChildSequenceIsGlobal(t *testing.T) {
	text := strings.Repeat(
		"第一段正文内容比较长，用于制造足够多的父子块。\n\n",
		120,
	)

	parentCfg := SplitterConfig{
		ChunkSize:    800,
		ChunkOverlap: 80,
		Separators:   DefaultSeparators(),
		Strategy:     StrategyLegacy,
	}

	childCfg := SplitterConfig{
		ChunkSize:    200,
		ChunkOverlap: 40,
		Separators:   DefaultSeparators(),
		Strategy:     StrategyLegacy,
	}

	result := SplitParentChild(text, parentCfg, childCfg)

	if len(result.Children) < 2 {
		t.Fatalf("测试需要多个 Child: got=%d", len(result.Children))
	}

	for i, child := range result.Children {
		if child.Seq != i {
			t.Fatalf(
				"Child Seq 应连续: child[%d].Seq=%d",
				i,
				child.Seq,
			)
		}
	}
}

// TestSplitParentChildConvertsChildOffsetsToDocumentCoordinates
//
// 这是 Parent-Child 最重要的不变量之一。
//
// Child Split 是针对：
//
//	parent.Content
//
// 执行的，最初 Start/End 是 Parent 局部坐标。
//
// 最终返回给外部时必须恢复成：
//
//	整篇文档坐标。
func TestSplitParentChildConvertsChildOffsetsToDocumentCoordinates(t *testing.T) {
	text := strings.Repeat(
		"这是中文正文，用来验证 rune offset 是否正确。这里继续补充一些文本。\n\n",
		100,
	)

	parentCfg := SplitterConfig{
		ChunkSize:    700,
		ChunkOverlap: 0,
		Separators:   DefaultSeparators(),
		Strategy:     StrategyLegacy,
	}

	childCfg := SplitterConfig{
		ChunkSize:    180,
		ChunkOverlap: 0,
		Separators:   DefaultSeparators(),
		Strategy:     StrategyLegacy,
	}

	result := SplitParentChild(text, parentCfg, childCfg)
	source := []rune(text)

	if len(result.Children) == 0 {
		t.Fatal("应该产生 Child")
	}

	for i, child := range result.Children {
		if child.Start < 0 || child.End < child.Start || child.End > len(source) {
			t.Fatalf(
				"Child[%d] 全局 Offset 非法: [%d,%d)",
				i,
				child.Start,
				child.End,
			)
		}

		// 当前测试没有 Table Synthetic Header，
		// 所以 Child 应继续满足严格 source mapping。
		got := string(source[child.Start:child.End])

		if got != child.Content {
			t.Fatalf(
				"Child[%d] 没有映射回整篇文档坐标\nsource=%q\nchild =%q",
				i,
				got,
				child.Content,
			)
		}
	}
}

// TestSplitParentChildDropsRedundantParent
//
// 如果：
//
//	Parent
//
// 再切 Child 后：
//
//	只有一个 Child
//
// 并且：
//
//	Child.Content == Parent.Content
//
// 就没有必要再单独保存 Parent。
func TestSplitParentChildDropsRedundantParent(t *testing.T) {
	text := "这是一篇非常短的文档。"

	parentCfg := SplitterConfig{
		ChunkSize:    4096,
		ChunkOverlap: 80,
		Separators:   DefaultSeparators(),
		Strategy:     StrategyLegacy,
	}

	childCfg := SplitterConfig{
		ChunkSize:    384,
		ChunkOverlap: 76,
		Separators:   DefaultSeparators(),
		Strategy:     StrategyLegacy,
	}

	result := SplitParentChild(text, parentCfg, childCfg)

	if len(result.Parents) != 0 {
		t.Fatalf(
			"Parent 与唯一 Child 完全相同时不应该保存 Parent: got=%d",
			len(result.Parents),
		)
	}

	if len(result.Children) != 1 {
		t.Fatalf("短文档应该产生一个 Child: got=%d", len(result.Children))
	}

	if result.Children[0].ParentIndex != -1 {
		t.Fatalf(
			"冗余 Parent 被删除后 ParentIndex 应为 -1: got=%d",
			result.Children[0].ParentIndex,
		)
	}

	if result.Children[0].Content != text {
		t.Fatalf(
			"Child Content 错误\nwant=%q\ngot =%q",
			text,
			result.Children[0].Content,
		)
	}
}

// TestSplitParentChildKeepsParentWhenMultipleChildren
//
// Parent 被切成多个 Child 时，Parent 必须保存。
func TestSplitParentChildKeepsParentWhenMultipleChildren(t *testing.T) {
	text := strings.Repeat(
		"这是一段较长的正文内容，用来确保一个 Parent 能够产生多个 Child。 ",
		40,
	)

	parentCfg := SplitterConfig{
		ChunkSize:    5000,
		ChunkOverlap: 80,
		Separators:   []string{"。", " "},
		Strategy:     StrategyLegacy,
	}

	childCfg := SplitterConfig{
		ChunkSize:    200,
		ChunkOverlap: 40,
		Separators:   []string{"。", " "},
		Strategy:     StrategyLegacy,
	}

	result := SplitParentChild(text, parentCfg, childCfg)

	if len(result.Parents) != 1 {
		t.Fatalf(
			"一个 Parent 产生多个 Child 后应保留 Parent: got=%d",
			len(result.Parents),
		)
	}

	if len(result.Children) <= 1 {
		t.Fatalf(
			"测试需要一个 Parent 产生多个 Child: got=%d",
			len(result.Children),
		)
	}

	for i, child := range result.Children {
		if child.ParentIndex != 0 {
			t.Fatalf(
				"Child[%d] 应关联 Parent 0: got=%d",
				i,
				child.ParentIndex,
			)
		}
	}
}

// TestMergeBreadcrumbs
//
// 单独固定 Parent / Child ContextHeader 合并行为。
func TestMergeBreadcrumbs(t *testing.T) {
	tests := []struct {
		name   string
		parent string
		child  string
		want   string
	}{
		{
			name:   "parent为空",
			parent: "",
			child:  "## 安装\n### Linux",
			want:   "## 安装\n### Linux",
		},
		{
			name:   "child为空",
			parent: "# 产品手册\n## 安装",
			child:  "",
			want:   "# 产品手册\n## 安装",
		},
		{
			name:   "接缝重复",
			parent: "# 产品手册\n## 安装",
			child:  "## 安装\n### Linux",
			want:   "# 产品手册\n## 安装\n### Linux",
		},
		{
			name:   "没有重复",
			parent: "# 产品手册",
			child:  "## 安装\n### Linux",
			want:   "# 产品手册\n## 安装\n### Linux",
		},
		{
			name:   "child只有重复行",
			parent: "# 产品手册\n## 安装",
			child:  "## 安装",
			want:   "# 产品手册\n## 安装",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mergeBreadcrumbs(tt.parent, tt.child)

			if got != tt.want {
				t.Fatalf(
					"mergeBreadcrumbs 错误\nparent=%q\nchild =%q\nwant  =%q\ngot   =%q",
					tt.parent,
					tt.child,
					tt.want,
					got,
				)
			}
		})
	}
}

// TestSplitParentChildMergesHeadingBreadcrumbs
//
// 这是 Parent-Child 里非常关键的结构测试。
//
// Parent 拥有较粗粒度 Heading，
// Child 再次对 Parent.Content 做 Heading-aware Split，
// 应该获得更深的 Breadcrumb。
func TestSplitParentChildMergesHeadingBreadcrumbs(t *testing.T) {
	body := strings.Repeat(
		"这是用于扩充分块长度的正文内容。这里继续增加一些说明文字。 ",
		15,
	)

	text :=
		"# 产品手册\n\n" +
			"## 安装\n\n" +
			"### Linux\n\n" +
			body + "\n\n" +
			"### Windows\n\n" +
			body + "\n\n" +
			"## 配置\n\n" +
			"### Database\n\n" +
			body + "\n\n" +
			"### Cache\n\n" +
			body

	parentCfg := SplitterConfig{
		ChunkSize:    1800,
		ChunkOverlap: 80,
		Separators:   DefaultSeparators(),
		Strategy:     StrategyHeading,
	}

	childCfg := SplitterConfig{
		ChunkSize:    350,
		ChunkOverlap: 50,
		Separators:   DefaultSeparators(),
		Strategy:     StrategyHeading,
	}

	result := SplitParentChild(text, parentCfg, childCfg)

	if len(result.Children) == 0 {
		t.Fatal("应该产生 Child")
	}

	foundLinux := false

	for _, child := range result.Children {
		if strings.Contains(child.Content, "Linux") ||
			strings.Contains(child.ContextHeader, "### Linux") {

			if !strings.Contains(child.ContextHeader, "## 安装") {
				t.Fatalf(
					"Linux Child 应继承 Parent 安装 Breadcrumb:\n%s",
					child.ContextHeader,
				)
			}

			if !strings.Contains(child.ContextHeader, "### Linux") {
				t.Fatalf(
					"Linux Child 应拥有 Child 层 H3 Breadcrumb:\n%s",
					child.ContextHeader,
				)
			}

			// 接缝不应该出现：
			//
			//     ## 安装
			//     ## 安装
			if strings.Count(child.ContextHeader, "## 安装") > 1 {
				t.Fatalf(
					"Parent/Child Breadcrumb 接缝发生重复:\n%s",
					child.ContextHeader,
				)
			}

			foundLinux = true
			break
		}
	}

	if !foundLinux {
		t.Fatal("没有找到 Linux Child")
	}
}

// TestSplitParentChildChildHonorsTokenLimit
//
// TokenLimit 只应该约束 Child，
// Parent 应继续保持较大的上下文窗口。
//
// 我们前面 DeriveParentChildConfigs 已经采用：
//
//	parent.TokenLimit = 0
//	child.TokenLimit  = base.TokenLimit
//
// 这里验证真实 SplitParentChild 路径中 Child 确实会调用 ensureDefaults。
func TestSplitParentChildChildHonorsTokenLimit(t *testing.T) {
	text := strings.Repeat(
		"这是用于测试 TokenLimit 的中文正文内容。这里继续补充更多字符。 ",
		100,
	)

	parentCfg := SplitterConfig{
		ChunkSize:    3000,
		ChunkOverlap: 80,
		Separators:   []string{"。", " "},
		Strategy:     StrategyLegacy,

		// Parent 不设置 TokenLimit。
	}

	childCfg := SplitterConfig{
		ChunkSize:    1000,
		ChunkOverlap: 80,
		Separators:   []string{"。", " "},
		Strategy:     StrategyLegacy,
		TokenLimit:   100,
		Languages:    []string{LangChinese},
	}

	result := SplitParentChild(text, parentCfg, childCfg)

	if len(result.Children) == 0 {
		t.Fatal("应该产生 Child")
	}

	// 中文：
	//
	//     100 * 1.7 * 0.9
	//     = 153
	//
	// ensureDefaults 后 Child target 大约是153。
	//
	// Validator/Protected Content 允许一定超额，
	// 所以这里不要求每一块 <=153，
	// 但普通文本不应该继续按原配置1000生成巨大 Child。
	for i, child := range result.Children {
		if RuneLen(child.Content) > 306 {
			t.Fatalf(
				"Child[%d] 明显没有受到 TokenLimit 约束: len=%d",
				i,
				RuneLen(child.Content),
			)
		}
	}
}

// TestSplitParentChildWithDiagnostics
//
// Diagnostics 当前描述 Parent Split 的 Strategy。
func TestSplitParentChildWithDiagnostics(t *testing.T) {
	body := strings.Repeat("这是正文内容。", 100)

	text :=
		"# 文档\n\n" +
			"## 第一部分\n" + body + "\n\n" +
			"## 第二部分\n" + body + "\n\n" +
			"## 第三部分\n" + body

	parentCfg := SplitterConfig{
		ChunkSize:    1000,
		ChunkOverlap: 80,
		Separators:   DefaultSeparators(),
		Strategy:     StrategyAuto,
	}

	childCfg := SplitterConfig{
		ChunkSize:    250,
		ChunkOverlap: 50,
		Separators:   DefaultSeparators(),
		Strategy:     StrategyAuto,
	}

	result, diag := SplitParentChildWithDiagnostics(text, parentCfg, childCfg)

	if len(result.Children) == 0 {
		t.Fatal("应该产生 Child")
	}

	if diag == nil {
		t.Fatal("Diagnostics 不应该为 nil")
	}

	if diag.Profile == nil {
		t.Fatal("Parent Auto Strategy 应产生 DocProfile")
	}

	if len(diag.TierChain) == 0 {
		t.Fatal("Diagnostics TierChain 不应该为空")
	}
}

// TestSplitParentChildParentIndexRefersToCompactedParents
//
// 这个测试固定一个很容易混淆的语义：
//
//	ParentIndex
//
// 指向的是:
//
//	result.Parents
//
// 而不是最初 parent split 的原始下标。
//
// 因为某些冗余 Parent 可能被删除。
func TestSplitParentChildParentIndexRefersToCompactedParents(t *testing.T) {
	longPart := strings.Repeat("这是一个比较长的章节内容。", 80)

	text :=
		"短前言。\n\n" +
			longPart + "\n\n" +
			longPart

	parentCfg := SplitterConfig{
		ChunkSize:    800,
		ChunkOverlap: 0,
		Separators:   []string{"\n\n", "。"},
		Strategy:     StrategyLegacy,
	}

	childCfg := SplitterConfig{
		ChunkSize:    180,
		ChunkOverlap: 0,
		Separators:   []string{"\n\n", "。"},
		Strategy:     StrategyLegacy,
	}

	result := SplitParentChild(text, parentCfg, childCfg)

	for i, child := range result.Children {
		if child.ParentIndex == -1 {
			continue
		}

		if child.ParentIndex < 0 || child.ParentIndex >= len(result.Parents) {
			t.Fatalf(
				"Child[%d] ParentIndex 必须指向压缩后的 result.Parents: index=%d parents=%d",
				i,
				child.ParentIndex,
				len(result.Parents),
			)
		}
	}
}
