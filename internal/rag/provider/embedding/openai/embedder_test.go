package openai

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cloudwego/eino/components/embedding"
)

func TestNewRequiresModel(t *testing.T) {
	_, err := New(context.Background(), Config{})

	if !errors.Is(err, ErrMissingModel) {
		t.Fatalf("缺少 Model 应失败: %v", err)
	}
}

func TestOpenAICompatibleEmbedding(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/embeddings" {
			t.Fatalf("请求路径错误: %s", r.URL.Path)
		}

		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Fatalf("Authorization Header 错误: %q", r.Header.Get("Authorization"))
		}

		var request struct {
			Model      string   `json:"model"`
			Input      []string `json:"input"`
			Dimensions int      `json:"dimensions"`
		}

		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}

		// 验证 embedding.WithModel() 可以覆盖默认 Model。
		if request.Model != "override-model" {
			t.Fatalf("Model override 没生效: %q", request.Model)
		}

		if len(request.Input) != 2 {
			t.Fatalf("应该收到两个 Embedding Input: %v", request.Input)
		}

		if request.Dimensions != 3 {
			t.Fatalf("Dimensions 错误: %d", request.Dimensions)
		}

		w.Header().Set("Content-Type", "application/json")

		_ = json.NewEncoder(w).Encode(map[string]any{
			"object": "list",
			"model":  request.Model,
			"data": []any{
				map[string]any{
					"object":    "embedding",
					"index":     0,
					"embedding": []float64{0.1, 0.2, 0.3},
				},
				map[string]any{
					"object":    "embedding",
					"index":     1,
					"embedding": []float64{0.4, 0.5, 0.6},
				},
			},
			"usage": map[string]any{
				"prompt_tokens": 4,
				"total_tokens":  4,
			},
		})
	}))
	defer server.Close()

	cfg := DefaultConfig()
	cfg.APIKey = "test-key"
	cfg.BaseURL = server.URL + "/v1"
	cfg.Model = "default-model"
	cfg.Dimensions = 3

	embedder, err := New(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}

	vectors, err := embedder.EmbedStrings(
		context.Background(),
		[]string{"hello", "world"},
		embedding.WithModel("override-model"),
	)
	if err != nil {
		t.Fatal(err)
	}

	if len(vectors) != 2 {
		t.Fatalf("应该返回两个 Vector: %d", len(vectors))
	}

	if len(vectors[0]) != 3 || len(vectors[1]) != 3 {
		t.Fatalf("Embedding Dimension 错误: %#v", vectors)
	}
}

func TestConfigRejectsNegativeDimensions(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Model = "model"
	cfg.Dimensions = -1

	if !errors.Is(cfg.Validate(), ErrInvalidDimensions) {
		t.Fatalf("负 Dimensions 应失败: %v", cfg.Validate())
	}
}
