package application

import (
	"context"
	"testing"

	"github.com/cloudwego/eino/components/document"
	"github.com/cloudwego/eino/schema"
)

type versionedStore struct {
	fakeStore
	reserved bool
}

func (s *versionedStore) ReserveDocument(context.Context, string, string) (int64, error) {
	s.reserved = true
	return 7, nil
}

type orderedLoader struct {
	store  *versionedStore
	t      *testing.T
	source *schema.Document
}

func (l *orderedLoader) Load(context.Context, document.Source, ...document.LoaderOption) ([]*schema.Document, error) {
	if !l.store.reserved {
		l.t.Fatal("parsing started before attempt reservation")
	}
	return []*schema.Document{l.source}, nil
}

func TestVersionedIngestReservesBeforeParsingAndKeepsStableIdentity(t *testing.T) {
	store := &versionedStore{}
	original := &schema.Document{ID: "temporary-path-hash", Content: "# Original\n\nText"}
	s := NewService(&orderedLoader{store: store, t: t, source: original}, store, nil)
	r, err := s.Ingest(context.Background(), IngestRequest{CollectionID: "kb", DocumentID: "stable-business-id"})
	if err != nil {
		t.Fatal(err)
	}
	if r.Documents[0].DocumentID != "stable-business-id" || r.Documents[0].Attempt != 7 || store.batches[0].Attempt != 7 || store.batches[0].ContentHash == "" || len(store.batches[0].ProcessConfig) == 0 {
		t.Fatalf("missing identity/version snapshot: %+v", r)
	}
	if original.ID != "temporary-path-hash" {
		t.Fatal("loader output was mutated")
	}
}

func TestVersionedIngestRequiresBusinessIDBeforeParsing(t *testing.T) {
	store := &versionedStore{}
	s := NewService(&orderedLoader{store: store, t: t}, store, nil)
	if _, err := s.Ingest(context.Background(), IngestRequest{CollectionID: "kb"}); err == nil {
		t.Fatal("path identity accepted for versioned ingestion")
	}
}
