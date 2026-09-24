package transcript

import "testing"

func TestToolResultRejectedOnlyMatchesHumbertDenials(t *testing.T) {
	for _, value := range []string{
		`工具 "write_file" 已被 Humbert Permission Policy 拒绝，未执行任何操作。`,
		`用户拒绝了工具 "edit_file" 的本次调用，未执行任何操作。`,
	} {
		if !ToolResultRejected(value) {
			t.Fatalf("denial not recognized: %q", value)
		}
	}
	if ToolResultRejected("网页中提到了用户拒绝了工具，但文件读取已经成功") {
		t.Fatal("ordinary tool content was classified as a denial")
	}
}
