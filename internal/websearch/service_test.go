package websearch

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"testing"
	"time"
)

type testBackend struct {
	id  string
	run func(context.Context) ([]Result, error)
}

func (b testBackend) Name() string { return b.id }
func (b testBackend) Search(ctx context.Context, _ *http.Client, _ string, _ int) ([]Result, error) {
	return b.run(ctx)
}

// SDK 后端可以独立插入；过滤、去重和降级仍由同一个组件负责。
func TestInjectedBackendChainPreservesFallbackAndFiltering(t *testing.T) {
	var calls []string
	backend := func(id string, results []Result, err error) Backend {
		return testBackend{id: id, run: func(context.Context) ([]Result, error) { calls = append(calls, id); return results, err }}
	}
	chain := []Backend{backend("failed", nil, errors.New("unavailable")), backend("empty", nil, nil), backend("sdk", []Result{
		{Title: "Guide", URL: "https://docs.example.org/guide", Snippet: "modular agent guide"},
		{Title: "Duplicate", URL: "https://docs.example.org/guide"},
		{Title: "Other", URL: "https://other.example.net/guide"},
	}, nil)}
	service, err := New("auto", time.Second, 3, 10, WithBackends(chain...))
	if err != nil {
		t.Fatal(err)
	}
	// 修改传入 slice 不得影响已经装配的降级链。
	chain[0] = backend("changed", nil, nil)
	output, err := service.Search(context.Background(), &Input{Query: "modular agent", AllowedDomains: []string{"example.org"}})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(calls, []string{"failed", "empty", "sdk"}) || output.Provider != "sdk" || len(output.Results) != 1 {
		t.Fatalf("fallback/filter changed: calls=%v output=%+v", calls, output)
	}
	if len(output.Diagnostics.Attempts) != 3 || output.Diagnostics.Attempts[0].Status != "error" || output.Diagnostics.Attempts[1].Status != "empty" {
		t.Fatalf("missing fallback diagnostics: %+v", output.Diagnostics)
	}
}

func TestCancellationDoesNotContinueFallback(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	called := false
	service, err := New("auto", time.Second, 3, 10, WithBackends(
		testBackend{id: "first", run: func(context.Context) ([]Result, error) { cancel(); return nil, context.Canceled }},
		testBackend{id: "second", run: func(context.Context) ([]Result, error) { called = true; return nil, nil }},
	))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Search(ctx, &Input{Query: "agent"}); !errors.Is(err, context.Canceled) || called {
		t.Fatalf("cancel ignored: %v, called=%v", err, called)
	}
}

// 单后端和固定 Provider 也不能把取消包装成普通错误或 all_failed 返回。
func TestCancellationAtLastBackend(t *testing.T) {
	for _, provider := range []string{"auto", "sdk"} {
		t.Run(provider, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			backend := testBackend{id: "sdk", run: func(context.Context) ([]Result, error) { cancel(); return nil, context.Canceled }}
			service, err := New(provider, time.Second, 1, 3, WithBackends(backend))
			if err != nil {
				t.Fatal(err)
			}
			if output, err := service.Search(ctx, &Input{Query: "agent"}); output != nil || !errors.Is(err, context.Canceled) {
				t.Fatalf("cancellation became a normal search result: output=%+v err=%v", output, err)
			}
		})
	}
}

func TestBackendConfigurationFailsBeforeNetworkCalls(t *testing.T) {
	backend := testBackend{id: "sdk", run: func(context.Context) ([]Result, error) { t.Fatal("unexpected network call"); return nil, nil }}
	for _, option := range []Option{WithBackends(), WithBackends(nil), WithBackends(backend, backend), WithBackends(testBackend{id: "auto"})} {
		if _, err := New("auto", time.Second, 1, 3, option); err == nil {
			t.Fatal("invalid backend configuration accepted")
		}
	}
	if _, err := New("missing", time.Second, 1, 3, WithBackends(backend)); err == nil {
		t.Fatal("missing selected backend accepted")
	}
}
