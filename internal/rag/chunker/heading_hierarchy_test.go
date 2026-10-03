package chunker

import "testing"

func TestHeadingHierarchyLinearNesting(t *testing.T) {
	h := NewHeadingHierarchy()

	h.Observe("# 产品手册")
	h.Observe("## 安装")
	h.Observe("### Linux")

	got := h.Breadcrumb()
	want := "产品手册 > 安装 > Linux"

	if got != want {
		t.Fatalf("Breadcrumb 错误: want=%q got=%q", want, got)
	}
}

func TestHeadingHierarchySiblingClearsDeeperLevels(t *testing.T) {
	h := NewHeadingHierarchy()

	h.Observe("# 产品手册")
	h.Observe("## 安装")
	h.Observe("### Linux")

	h.Observe("## 配置")

	got := h.Breadcrumb()
	want := "产品手册 > 配置"

	if got != want {
		t.Fatalf("新的 H2 应清除旧 H3: want=%q got=%q", want, got)
	}
}

func TestHeadingHierarchyNewH1ClearsEverythingBelow(t *testing.T) {
	h := NewHeadingHierarchy()

	h.Observe("# 第一章")
	h.Observe("## 安装")
	h.Observe("### Linux")

	h.Observe("# 第二章")

	if got := h.Breadcrumb(); got != "第二章" {
		t.Fatalf("新的 H1 应清除更深层级: got=%q", got)
	}

	if h.Depth() != 1 {
		t.Fatalf("Depth 应为1: got=%d", h.Depth())
	}
}

func TestHeadingHierarchyIgnoresNormalText(t *testing.T) {
	h := NewHeadingHierarchy()
	h.Observe("# 标题")

	level, title := h.Observe("这是一段普通正文。")

	if level != 0 || title != "" {
		t.Fatalf("普通正文不应该被识别为 Heading: level=%d title=%q", level, title)
	}

	if got := h.Breadcrumb(); got != "标题" {
		t.Fatalf("普通正文不应该修改 Heading 状态: got=%q", got)
	}
}

func TestHeadingHierarchyWithHashes(t *testing.T) {
	h := NewHeadingHierarchy()

	h.Observe("# 产品手册")
	h.Observe("## 安装")
	h.Observe("### Linux")

	got := h.BreadcrumbWithHashes()
	want := "# 产品手册\n## 安装\n### Linux"

	if got != want {
		t.Fatalf("BreadcrumbWithHashes 错误\nwant=%q\ngot =%q", want, got)
	}
}

func TestHeadingHierarchySkipLevel(t *testing.T) {
	h := NewHeadingHierarchy()

	h.Observe("# 产品手册")
	h.Observe("### Linux")

	got := h.BreadcrumbWithHashes()
	want := "# 产品手册\n### Linux"

	if got != want {
		t.Fatalf("跳级 Heading 应保留真实层级\nwant=%q\ngot =%q", want, got)
	}
}

func TestHeadingHierarchyReset(t *testing.T) {
	h := NewHeadingHierarchy()

	h.Observe("# A")
	h.Observe("## B")

	h.Reset()

	if h.Depth() != 0 || h.Breadcrumb() != "" || h.BreadcrumbWithHashes() != "" {
		t.Fatal("Reset 后 HeadingHierarchy 应为空")
	}
}
