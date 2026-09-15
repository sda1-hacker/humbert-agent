package memory

import (
	"testing"

	"github.com/sda1-hacker/humbert-agent/internal/transcript"
)

func TestCursorMatchesOnlyCurrentLineage(t *testing.T) {
	t.Parallel()

	branch := []transcript.Entry{{ID: "a"}, {ID: "b"}, {ID: "c"}}
	cursor := Cursor{CoveredLeafID: "b", LineageHash: lineageHash(branch[:2])}
	index, ok := cursorMatches(cursor, branch)
	if !ok || index != 1 {
		t.Fatalf("cursorMatches() = (%d,%v), want (1,true)", index, ok)
	}

	fork := []transcript.Entry{{ID: "a"}, {ID: "x"}, {ID: "c"}}
	if _, ok := cursorMatches(cursor, fork); ok {
		t.Fatal("cursor from abandoned branch must not match a fork")
	}
}
