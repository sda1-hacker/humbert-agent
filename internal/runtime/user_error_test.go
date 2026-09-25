package runtime

import (
	"errors"
	"strings"
	"testing"
)

func TestRuntimeUserVisibleErrorRecognizesProviderFailures(t *testing.T) {
	tests := []struct {
		name   string
		source string
		want   string
	}{
		{
			name:   "free quota takes precedence over 403",
			source: `Eino Agent 执行失败: status code: 403, message: Free quota exhausted. To continue, please add funds or disable the "use free tier only" mode.`,
			want:   "免费额度已用尽",
		},
		{name: "balance", source: "status code: 402, insufficient balance", want: "余额或调用额度不足"},
		{name: "rate limit", source: "status code: 429, rate limit exceeded", want: "请求过于频繁"},
		{name: "access denied", source: "status code: 403, forbidden", want: "拒绝了本次请求"},
		{name: "generic", source: "provider returned a private error payload", want: "生成过程中发生错误"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			actual := runtimeUserVisibleError(errors.New(test.source))
			if !strings.Contains(actual, test.want) {
				t.Fatalf("got %q, want text %q", actual, test.want)
			}
			if strings.Contains(actual, test.source) {
				t.Fatal("provider error was exposed directly")
			}
		})
	}
}
