package rerankhttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClientRealignsProviderRankingToInputOrder(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("Method 错误: %s", r.Method)
		}

		if r.Header.Get("Authorization") != "Bearer secret" {
			t.Fatalf("Authorization 错误: %q", r.Header.Get("Authorization"))
		}

		var request rerankRequest

		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}

		if request.Model != "bge-reranker" {
			t.Fatalf("Model 错误: %q", request.Model)
		}

		if request.Query != "Linux installation" {
			t.Fatalf("Query 错误: %q", request.Query)
		}

		if request.TopN != 3 {
			t.Fatalf("top_n 必须要求返回所有 Passage: %d", request.TopN)
		}

		w.Header().Set("Content-Type", "application/json")

		// Provider 按 relevance DESC 返回：
		//
		// passage[1] = 第一名
		// passage[2] = 第二名
		// passage[0] = 第三名
		//
		// Client 必须重新变回：
		//
		// scores[0]
		// scores[1]
		// scores[2]
		_ = json.NewEncoder(w).Encode(rerankResponse{
			Results: []rerankResult{
				{Index: 1, RelevanceScore: 0.95},
				{Index: 2, RelevanceScore: 0.70},
				{Index: 0, RelevanceScore: 0.20},
			},
		})
	}))
	defer server.Close()

	cfg := DefaultConfig()
	cfg.Endpoint = server.URL + "/v1/rerank"
	cfg.APIKey = "secret"
	cfg.Model = "bge-reranker"

	client, err := NewClient(cfg)
	if err != nil {
		t.Fatal(err)
	}

	scores, err := client.Score(
		context.Background(),
		"Linux installation",
		[]string{
			"Windows content",
			"Linux content",
			"Generic content",
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	if len(scores) != 3 {
		t.Fatalf("Score 数量错误: %d", len(scores))
	}

	if scores[0] != 0.20 {
		t.Fatalf("passage[0] Score 错误: %f", scores[0])
	}

	if scores[1] != 0.95 {
		t.Fatalf("passage[1] Score 错误: %f", scores[1])
	}

	if scores[2] != 0.70 {
		t.Fatalf("passage[2] Score 错误: %f", scores[2])
	}
}

func TestClientRejectsPartialResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(rerankResponse{
			Results: []rerankResult{
				{Index: 0, RelevanceScore: 0.9},
			},
		})
	}))
	defer server.Close()

	client, err := NewClient(Config{
		Endpoint: server.URL,
		Model:    "model",
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = client.Score(
		context.Background(),
		"query",
		[]string{"A", "B"},
	)

	if !errors.Is(err, ErrInvalidResponse) {
		t.Fatalf("Partial Response 应失败: %v", err)
	}
}

func TestClientRejectsDuplicateIndex(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(rerankResponse{
			Results: []rerankResult{
				{Index: 0, RelevanceScore: 0.9},
				{Index: 0, RelevanceScore: 0.8},
			},
		})
	}))
	defer server.Close()

	client, err := NewClient(Config{
		Endpoint: server.URL,
		Model:    "model",
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = client.Score(
		context.Background(),
		"query",
		[]string{"A", "B"},
	)

	if !errors.Is(err, ErrInvalidResponse) {
		t.Fatalf("重复 Index 应失败: %v", err)
	}
}

func TestClientRejectsInvalidScore(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(rerankResponse{
			Results: []rerankResult{
				{Index: 0, RelevanceScore: 1.5},
			},
		})
	}))
	defer server.Close()

	client, err := NewClient(Config{
		Endpoint: server.URL,
		Model:    "model",
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = client.Score(
		context.Background(),
		"query",
		[]string{"A"},
	)

	if !errors.Is(err, ErrInvalidScore) {
		t.Fatalf("非法 Score 应失败: %v", err)
	}
}

func TestClientReturnsHTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"model unavailable"}`, http.StatusServiceUnavailable)
	}))
	defer server.Close()

	client, err := NewClient(Config{
		Endpoint: server.URL,
		Model:    "model",
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = client.Score(
		context.Background(),
		"query",
		[]string{"A"},
	)

	if err == nil {
		t.Fatal("HTTP 503 应返回错误")
	}
}

func TestClientEmptyPassages(t *testing.T) {
	client, err := NewClient(Config{
		Endpoint: "http://127.0.0.1:12345/v1/rerank",
		Model:    "model",
	})
	if err != nil {
		t.Fatal(err)
	}

	scores, err := client.Score(
		context.Background(),
		"query",
		nil,
	)

	if err != nil {
		t.Fatal(err)
	}

	if len(scores) != 0 {
		t.Fatalf("空 Passage 应返回空 Score: %v", scores)
	}
}

func TestConfigValidation(t *testing.T) {
	cfg := DefaultConfig()

	if !errors.Is(cfg.Validate(), ErrMissingEndpoint) {
		t.Fatalf("应该缺少 Endpoint: %v", cfg.Validate())
	}

	cfg.Endpoint = "not-a-url"
	cfg.Model = "model"

	if !errors.Is(cfg.Validate(), ErrInvalidEndpoint) {
		t.Fatalf("非法 Endpoint 应失败: %v", cfg.Validate())
	}

	cfg.Endpoint = "http://localhost/v1/rerank"
	cfg.Model = ""

	if !errors.Is(cfg.Validate(), ErrMissingModel) {
		t.Fatalf("缺少 Model 应失败: %v", cfg.Validate())
	}
}
