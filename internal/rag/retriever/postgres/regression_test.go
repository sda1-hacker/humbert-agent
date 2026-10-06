package postgres

import (
	"github.com/sda1-hacker/humbert-agent/internal/rag/retrieval"
	"testing"
)

func TestRevisionMappingPreservesBigintPrecision(t *testing.T) {
	rev, err := retrieval.Revision(map[string]any{"rag_document_revision": "9007199254740993"})
	if err != nil || rev != 9007199254740993 {
		t.Fatalf("%d %v", rev, err)
	}
	if _, err := retrieval.Revision(map[string]any{"rag_document_revision": float64(2)}); err == nil {
		t.Fatal("lossy numeric revision accepted")
	}
}
