package services

import (
	"context"
	"errors"
	"net"
	"net/url"
	"strings"
	"testing"
)

func TestClassifyModelDiagnostic(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		category string
	}{
		{"timeout", context.DeadlineExceeded, "timeout"},
		{"auth", errors.New("status 401: invalid api key secret-value"), "credential"},
		{"missing credential", errors.New("读取 Provider Credential 失败: secret-value"), "credential"},
		{"endpoint", errors.New("status 404 model_not_found"), "endpoint"},
		{"invalid URL", errors.New("unsupported protocol scheme"), "endpoint"},
		{"quota", errors.New("status 429 rate limit"), "quota"},
		{"capability", errors.New("unsupported parameter: tools"), "capability"},
		{"network", &url.Error{Op: "Post", URL: "https://example.invalid", Err: &net.DNSError{Err: "no such host"}}, "network"},
		{"unknown", errors.New("secret-value internal failure"), "unknown"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			category, summary, action := classifyModelDiagnostic(test.err)
			if category != test.category {
				t.Fatalf("category = %q, want %q", category, test.category)
			}
			if summary == "" || action == "" {
				t.Fatal("missing actionable diagnostic")
			}
			if strings.Contains(summary+action, "secret-value") {
				t.Fatal("raw error was exposed")
			}
		})
	}
}
