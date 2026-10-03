package models

import (
	"context"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/sda1-hacker/humbert-agent/internal/logging"
)

// 首响应超时不应中断正在读取的流；取消时也不能开始构造 SDK。
func TestStreamingClientPolicyAndCancelledConstruction(t *testing.T) {
	client, err := newStreamingHTTPClient(1500 * time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	transport := client.Transport.(*http.Transport)
	if client.Timeout != 0 || transport.ResponseHeaderTimeout != 1500*time.Millisecond {
		t.Fatalf("streaming policy changed: %+v", client)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := NewFactory(nil).Create(ctx, ResolvedModel{}); err == nil {
		t.Fatal("cancelled construction accepted")
	}
}

// 使用真实 Eino SDK 构造验证缓存；配置变更后必须释放旧实例，保留新的版本。
func TestFactoryKeepsRegistryCacheAndInvalidation(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	store, err := NewStore(ctx, filepath.Join(root, "providers.json"), filepath.Join(root, "models.json"))
	if err != nil {
		t.Fatal(err)
	}
	registry := NewRegistry(store, nil, logging.NewBootstrap())
	provider, err := registry.CreateProvider(ctx, CreateProviderInput{Name: "Local", Type: ProviderTypeOllama})
	if err != nil {
		t.Fatal(err)
	}
	input := CreateModelInput{ProviderID: provider.ID, ModelName: "local", TimeoutMS: 1000, ContextWindow: 8192, MaxOutputTokens: 512, Enabled: true}
	model, err := registry.CreateModel(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	first, err := registry.ResolveSnapshot(ctx, model.ID)
	if err != nil {
		t.Fatal(err)
	}
	second, err := registry.ResolveSnapshot(ctx, model.ID)
	if err != nil || first.Instance != second.Instance {
		t.Fatalf("cache bypassed: err=%v", err)
	}
	input.DisplayName = "Updated"
	if _, err := registry.UpdateModel(ctx, model.ID, UpdateModelInput(input)); err != nil {
		t.Fatal(err)
	}
	third, err := registry.ResolveSnapshot(ctx, model.ID)
	if err != nil || third.Revision == first.Revision || third.Instance == first.Instance {
		t.Fatalf("configuration invalidation lost: err=%v", err)
	}
}
