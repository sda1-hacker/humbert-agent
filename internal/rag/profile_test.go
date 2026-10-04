package rag

import (
	"testing"
	"time"
)

func TestEmbeddingProfileIgnoresCredentialsAndRequestSettings(t *testing.T) {
	c := DefaultConfig()
	c.Embedding.Model = "model-a"
	before := c.EmbeddingProfile().ID()
	c.Embedding.APIKey = "rotated-key"
	c.Embedding.Timeout = time.Minute
	c.Embedding.Dimensions = 0
	if c.EmbeddingProfile().ID() != before {
		t.Fatal("key/timeout/request dimension changed semantic profile")
	}
	c.Embedding.ModelRevision = "new-weights"
	if c.EmbeddingProfile().ID() == before {
		t.Fatal("model revision not included in profile")
	}
	c.Embedding.ModelRevision = ""
	c.Indexer.SearchBuilder.TitleKeys = []string{"custom-title"}
	if c.EmbeddingProfile().ID() == before {
		t.Fatal("input representation not included in profile")
	}
}
