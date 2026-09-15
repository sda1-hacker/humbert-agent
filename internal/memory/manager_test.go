package memory

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sda1-hacker/humbert-agent/internal/config"
	"github.com/sda1-hacker/humbert-agent/internal/logging"
	"github.com/sda1-hacker/humbert-agent/internal/transcript"
)

type memoryTestDirectoryResolver struct {
	root string
}

func (r memoryTestDirectoryResolver) SessionDirectory(_ context.Context, sessionID string) (string, error) {
	path := filepath.Join(r.root, sessionID)
	if err := os.MkdirAll(path, 0o700); err != nil {
		return "", err
	}
	return path, nil
}

type memoryTestTranscriptRepository struct{}

func (memoryTestTranscriptRepository) LoadTranscript(context.Context, string) (transcript.Document, error) {
	return transcript.Document{}, nil
}

type memoryTestEstimator struct{}

func (memoryTestEstimator) EstimateText(value string) int { return len(value) }

func TestContextFactsRejectsMemoryFromAbandonedBranch(t *testing.T) {
	t.Parallel()

	store, err := NewStore(memoryTestDirectoryResolver{root: t.TempDir()})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	manager, err := NewManager(
		config.ContextConfig{},
		store,
		memoryTestTranscriptRepository{},
		memoryTestEstimator{},
		logging.NewBootstrap(),
	)
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}

	originalBranch := []transcript.Entry{{ID: "a"}, {ID: "b"}}
	document := Document{
		Version:   CurrentVersion,
		SessionID: "session-1",
		Cursor: Cursor{
			CoveredLeafID: "b",
			LineageHash:   lineageHash(originalBranch),
		},
		Summary:   "### 重要事实\n- 只属于旧分支\n\n### 事情经过\n- 旧事件",
		UpdatedAt: time.Now().UTC(),
	}
	if err := store.Save(context.Background(), "session-1", document); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	fork := []transcript.Entry{{ID: "a"}, {ID: "x"}}
	facts, err := manager.ContextFacts(context.Background(), "session-1", fork)
	if err != nil {
		t.Fatalf("ContextFacts() error = %v", err)
	}
	if facts != "" {
		t.Fatalf("stale branch facts leaked into context: %q", facts)
	}

	facts, err = manager.ContextFacts(context.Background(), "session-1", originalBranch)
	if err != nil {
		t.Fatalf("ContextFacts(valid) error = %v", err)
	}
	if facts != "- 只属于旧分支" {
		t.Fatalf("ContextFacts(valid) = %q", facts)
	}
}
