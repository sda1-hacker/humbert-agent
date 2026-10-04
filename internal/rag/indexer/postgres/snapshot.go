package postgres

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"

	"github.com/sda1-hacker/humbert-agent/internal/rag/application"
)

// A same-attempt retry is idempotent only when all published evidence and
// embedding inputs match. Markdown alone misses title/header/chunk changes.
func processSnapshot(batch application.IngestionBatch, texts []string) (json.RawMessage, error) {
	input, err := json.Marshal(struct {
		Title             string
		Metadata          map[string]any
		Parents, Children []application.ChunkRecord
		SearchContent     []string
	}{batch.Title, batch.Metadata, batch.Parents, batch.Children, texts})
	if err != nil {
		return nil, fmt.Errorf("encode ingestion snapshot: %w", err)
	}
	config := make(map[string]json.RawMessage)
	if len(batch.ProcessConfig) > 0 {
		if err = json.Unmarshal(batch.ProcessConfig, &config); err != nil {
			return nil, err
		}
	}
	if config == nil {
		return nil, fmt.Errorf("%w: process config must be an object", ErrInvalidIngestionBatch)
	}
	config["index_input_hash"], _ = json.Marshal(fmt.Sprintf("%x", sha256.Sum256(input)))
	return json.Marshal(config)
}
