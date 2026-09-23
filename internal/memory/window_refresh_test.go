package memory

import (
	"github.com/sda1-hacker/humbert-agent/internal/transcript"
	"testing"
)

func TestReconstructMemoryBranchUsesIndexedWindowOnlyWhenCursorCovered(t *testing.T) {
	lineage := []transcript.Entry{{ID: "old", Type: transcript.EntryMessage, Message: &transcript.AgentMessage{Role: transcript.RoleUser}}, {ID: "kept", Type: transcript.EntryMessage, Message: &transcript.AgentMessage{Role: transcript.RoleUser}}, {ID: "new", Type: transcript.EntryMessage, Message: &transcript.AgentMessage{Role: transcript.RoleAssistant}}}
	decoded := []transcript.Entry{{ID: "kept", Type: transcript.EntryMessage, Message: &transcript.AgentMessage{Role: transcript.RoleUser, Content: []transcript.ContentBlock{{Type: transcript.ContentText, Text: "kept full"}}}}, {ID: "new", Type: transcript.EntryMessage, Message: &transcript.AgentMessage{Role: transcript.RoleAssistant, Content: []transcript.ContentBlock{{Type: transcript.ContentText, Text: "new full"}}}}}
	document := transcript.Document{Lineage: lineage, ActiveBranch: decoded, LeafID: "new"}
	cursor := Cursor{CoveredLeafID: "old", LineageHash: lineageHash(lineage[:1])}
	reconstructed, ok := reconstructMemoryBranch(document, cursor, true)
	if !ok || len(reconstructed.ActiveBranch) != 3 || reconstructed.ActiveBranch[2].Message.Content[0].Text != "new full" {
		t.Fatalf("valid cursor did not use window: %#v", reconstructed)
	}
	stale := Cursor{CoveredLeafID: "old", LineageHash: "wrong"}
	if _, ok := reconstructMemoryBranch(document, stale, true); ok {
		t.Fatal("stale cursor accepted")
	}
}
