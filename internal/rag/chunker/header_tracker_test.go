package chunker

import (
	"strings"
	"testing"
)

func TestHeaderTrackerTracksMarkdownTable(
	t *testing.T,
) {

	tracker :=
		newHeaderTracker()

	header :=
		"| 城市 | 标准 |\n" +
			"| --- | --- |\n"

	tracker.update(
		header,
	)

	got :=
		tracker.getHeaders()

	if got != header {
		t.Fatalf(
			"Table Header 跟踪错误\nwant=%q\ngot =%q",
			header,
			got,
		)
	}
}

func TestHeaderTrackerEndsOnPlainText(
	t *testing.T,
) {

	tracker :=
		newHeaderTracker()

	tracker.update(
		"| 城市 | 标准 |\n" +
			"| --- | --- |\n",
	)

	if tracker.getHeaders() == "" {
		t.Fatal(
			"此时应该存在 Active Header",
		)
	}

	tracker.update(
		"这里已经离开表格了。",
	)

	if tracker.getHeaders() != "" {
		t.Fatalf(
			"普通正文出现后 Table Header 应该结束: %q",
			tracker.getHeaders(),
		)
	}
}

func TestHeaderTrackerRepairsEmptyHeader(
	t *testing.T,
) {

	tracker :=
		newHeaderTracker()

	// 模拟某些转换器输出的空 Header。
	tracker.update(
		"||\n" +
			"| --- | --- |\n",
	)

	// 第一条真实数据。
	tracker.update(
		"| 城市 | 标准 |\n",
	)

	got :=
		tracker.getHeaders()

	if !strings.Contains(
		got,
		"| 城市 | 标准 |",
	) {
		t.Fatalf(
			"空表头应该使用第一条真实数据补全: %q",
			got,
		)
	}

	if !strings.Contains(
		got,
		"| --- | --- |",
	) {
		t.Fatalf(
			"补全后应该继续保留 Separator: %q",
			got,
		)
	}
}

func TestHeaderTrackerEndsOnColumnMismatch(
	t *testing.T,
) {

	tracker :=
		newHeaderTracker()

	// 两列表。
	tracker.update(
		"| A | B |\n" +
			"| --- | --- |\n",
	)

	// 三列表。
	tracker.update(
		"| X | Y | Z |\n",
	)

	if tracker.getHeaders() != "" {
		t.Fatalf(
			"列数变化后旧 Header 应结束: %q",
			tracker.getHeaders(),
		)
	}

	if !tracker.headerEndedThisUnit {
		t.Fatal(
			"列数变化时应该标记 headerEndedThisUnit",
		)
	}
}
