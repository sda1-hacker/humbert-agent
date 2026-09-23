package builtin

import (
	"context"
	"testing"

	humberttools "github.com/sda1-hacker/humbert-agent/internal/tools"
	"github.com/sda1-hacker/humbert-agent/internal/transcript"
)

type indexedHistoryStub struct {
	entries   []transcript.Entry
	fullLoads int
}

func (s *indexedHistoryStub) LoadTranscript(context.Context, string) (transcript.Document, error) {
	s.fullLoads++
	return transcript.Document{ActiveBranch: s.entries}, nil
}
func (s *indexedHistoryStub) VisitActiveBranchReverse(_ context.Context, _ string, visit func(transcript.Entry) bool) error {
	for i := len(s.entries) - 1; i >= 0; i-- {
		if !visit(s.entries[i]) {
			break
		}
	}
	return nil
}
func (s *indexedHistoryStub) ReadActiveBranchRange(_ context.Context, _, id string, before, after int) ([]transcript.Entry, error) {
	for i, e := range s.entries {
		if e.ID == id {
			start, end := i-before, i+after+1
			if start < 0 {
				start = 0
			}
			if end > len(s.entries) {
				end = len(s.entries)
			}
			return s.entries[start:end], nil
		}
	}
	return nil, nil
}
func TestHistorySearchAndReadUseIndexedRepository(t *testing.T) {
	repo := &indexedHistoryStub{entries: []transcript.Entry{
		{Type: transcript.EntryMessage, ID: "old", Message: &transcript.AgentMessage{Role: transcript.RoleUser, Content: []transcript.ContentBlock{{Type: transcript.ContentText, Text: "needle first"}}}},
		{Type: transcript.EntryMessage, ID: "assistant", Message: &transcript.AgentMessage{Role: transcript.RoleAssistant, Content: []transcript.ContentBlock{{Type: transcript.ContentThinking, Thinking: "needle private"}, {Type: transcript.ContentText, Text: "visible"}}}},
		{Type: transcript.EntryMessage, ID: "new", Message: &transcript.AgentMessage{Role: transcript.RoleUser, Content: []transcript.ContentBlock{{Type: transcript.ContentText, Text: "needle last"}}}},
	}}
	factory := &SessionHistoryFactory{repository: repo}
	scope := humberttools.Scope{SessionID: "session"}
	matches, err := factory.search(context.Background(), scope, &SessionHistoryInput{Query: "needle", MaxResults: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 || matches[0].EntryID != "new" {
		t.Fatalf("matches=%#v", matches)
	}
	entries, err := factory.read(context.Background(), scope, &SessionHistoryInput{EntryID: "old", Before: 1, After: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || entries[0].Content != "needle first" {
		t.Fatalf("entries=%#v", entries)
	}
	if repo.fullLoads != 0 {
		t.Fatal("history tool loaded full transcript")
	}
}
