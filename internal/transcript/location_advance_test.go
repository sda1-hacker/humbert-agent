package transcript

import (
	"reflect"
	"testing"
)

func TestLocationIndexIncrementalAdvanceMatchesRebuild(t *testing.T) {
	idx := &locationIndex{Header: locationHeader{Version: 1, HeaderBytes: 10}}
	if err := idx.rebuildBranch(); err != nil {
		t.Fatal(err)
	}
	appendEntry := func(entry Entry) {
		offset := idx.Header.HeaderBytes
		if len(idx.Records) > 0 {
			last := idx.Records[len(idx.Records)-1]
			offset = last.Offset + last.Length
		}
		if err := idx.appendRecord(locationRecord{Offset: offset, Length: 10, Entry: metadataEntry(entry)}, nil); err != nil {
			t.Fatal(err)
		}
	}
	first := Entry{Type: EntryMessage, ID: "m1", Message: &AgentMessage{Role: RoleUser}}
	appendEntry(first)
	parent1 := "m1"
	appendEntry(Entry{Type: EntryMessage, ID: "m2", ParentID: &parent1, Message: &AgentMessage{Role: RoleAssistant}})
	oldView := idx.branchMetadata()
	parent2 := "m2"
	appendEntry(Entry{Type: EntryCompaction, ID: "cp1", ParentID: &parent2, FirstKeptEntryID: "m2"})
	parent3 := "cp1"
	appendEntry(Entry{Type: EntryMessage, ID: "m3", ParentID: &parent3, Message: &AgentMessage{Role: RoleUser}})
	if len(oldView) != 2 || oldView[1].ID != "m2" {
		t.Fatal("borrowed old view changed after append")
	}
	rebuilt := &locationIndex{Header: idx.Header, Records: append([]locationRecord(nil), idx.Records...)}
	if err := rebuilt.rebuildBranch(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(idx.Metadata, rebuilt.Metadata) || !reflect.DeepEqual(idx.Branch, rebuilt.Branch) || !reflect.DeepEqual(idx.Positions, rebuilt.Positions) || !reflect.DeepEqual(idx.MessageIndexes, rebuilt.MessageIndexes) || !reflect.DeepEqual(idx.MessagePositions, rebuilt.MessagePositions) || idx.Window != rebuilt.Window {
		t.Fatalf("incremental state differs from rebuild: got=%#v want=%#v", idx.Window, rebuilt.Window)
	}
	if idx.Window.Generation != 1 || idx.Window.FirstKeptIndex != 1 || idx.Window.LatestCompactionIndex != 2 {
		t.Fatalf("compaction index=%#v", idx.Window)
	}
	forkParent := "m1"
	appendEntry(Entry{Type: EntryMessage, ID: "fork", ParentID: &forkParent, Message: &AgentMessage{Role: RoleUser}})
	if len(idx.Metadata) != 2 || idx.Metadata[1].ID != "fork" || idx.Window.Generation != 0 || len(idx.MessageIndexes) != 2 {
		t.Fatalf("fork did not rebuild active branch: %#v", idx.Metadata)
	}
}

func TestLocationCacheRespectsMemoryBudget(t *testing.T) {
	cache := newLocationCache()
	cache.put("first", &locationIndex{SidecarBytes: 70 * 1024 * 1024})
	cache.put("second", &locationIndex{SidecarBytes: 70 * 1024 * 1024})
	if len(cache.values) != 1 || cache.values["second"] == nil || cache.totalBytes > maxLocationCacheBytes {
		t.Fatalf("location cache exceeded memory budget: entries=%d bytes=%d", len(cache.values), cache.totalBytes)
	}
	cache.invalidate("second")
	if len(cache.values) != 0 || cache.totalBytes != 0 {
		t.Fatalf("invalidated cache still holds memory: entries=%d bytes=%d", len(cache.values), cache.totalBytes)
	}
}
