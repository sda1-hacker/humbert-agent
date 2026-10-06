package postgres

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"

	"github.com/sda1-hacker/humbert-agent/internal/rag"
)

// 固定当前版本的实际输入，拒绝同一版本用不同分块或检索文本重试。
func processSnapshot(batch rag.IngestionBatch, texts []string, hashKey string) (json.RawMessage, error) {
	input, err := json.Marshal(struct {
		Title             string
		Metadata          map[string]any
		Parents, Children []rag.ChunkRecord
		SearchContent     []string
	}{batch.Title, batch.Metadata, batch.Parents, batch.Children, texts})
	if err != nil {
		return nil, fmt.Errorf("encode ingestion snapshot: %w", err)
	}
	config := make(map[string]json.RawMessage)
	if len(batch.ProcessConfig) > 0 {
		if err := json.Unmarshal(batch.ProcessConfig, &config); err != nil {
			return nil, err
		}
	}
	if config == nil {
		return nil, fmt.Errorf("%w: process config must be an object", rag.ErrInvalidIngestionBatch)
	}
	config[hashKey], _ = json.Marshal(fmt.Sprintf("%x", sha256.Sum256(input)))
	return json.Marshal(config)
}
