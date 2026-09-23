package transcript

import (
	"context"
	"fmt"
	"os"
	"strings"
)

// VisitActiveBranchReverse decodes historical entries lazily, newest first. Returning
// false stops disk IO immediately after enough matches have been found.
func (s *Store) VisitActiveBranchReverse(ctx context.Context, agentID, sessionID string, visit func(Entry) bool) error {
	path, err := s.sessionPath(agentID, sessionID)
	if err != nil {
		return err
	}
	unlock := s.locks.lock(path)
	defer unlock()
	if err := s.validateExistingSessionPath(agentID, sessionID, path); err != nil {
		return err
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if info.Size() <= defaultDocumentCacheBytes {
		document, _, _, err := s.documentForReadLocked(ctx, path, sessionID)
		if err != nil {
			return err
		}
		for i := len(document.ActiveBranch) - 1; i >= 0; i-- {
			if err := validateContext(ctx); err != nil {
				return err
			}
			if !visit(document.ActiveBranch[i]) {
				break
			}
		}
		return nil
	}
	idx, _, err := s.locationIndexLocked(ctx, path, sessionID)
	if err != nil {
		return err
	}
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	for i := len(idx.Branch) - 1; i >= 0; i-- {
		if err := validateContext(ctx); err != nil {
			return err
		}
		entry, err := readLocatedEntry(file, idx.Records[idx.Branch[i]])
		if err != nil {
			return err
		}
		if !visit(entry) {
			break
		}
	}
	return nil
}

// ReadActiveBranchRange reads only the requested entry and its nearby branch entries.
func (s *Store) ReadActiveBranchRange(ctx context.Context, agentID, sessionID, entryID string, before, after int) ([]Entry, error) {
	path, err := s.sessionPath(agentID, sessionID)
	if err != nil {
		return nil, err
	}
	unlock := s.locks.lock(path)
	defer unlock()
	if err := s.validateExistingSessionPath(agentID, sessionID, path); err != nil {
		return nil, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if info.Size() <= defaultDocumentCacheBytes {
		document, _, _, err := s.documentForReadLocked(ctx, path, sessionID)
		if err != nil {
			return nil, err
		}
		position := -1
		for i := range document.ActiveBranch {
			if document.ActiveBranch[i].ID == strings.TrimSpace(entryID) {
				position = i
				break
			}
		}
		if position < 0 {
			return nil, fmt.Errorf("entry_id 不在当前有效会话分支中: %s", entryID)
		}
		start, end := position-before, position+after+1
		if start < 0 {
			start = 0
		}
		if end > len(document.ActiveBranch) {
			end = len(document.ActiveBranch)
		}
		return cloneEntries(document.ActiveBranch[start:end]), nil
	}
	idx, _, err := s.locationIndexLocked(ctx, path, sessionID)
	if err != nil {
		return nil, err
	}
	position, ok := idx.Positions[strings.TrimSpace(entryID)]
	if !ok {
		return nil, fmt.Errorf("entry_id 不在当前有效会话分支中: %s", entryID)
	}
	start, end := position-before, position+after+1
	if start < 0 {
		start = 0
	}
	if end > len(idx.Branch) {
		end = len(idx.Branch)
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	entries := make([]Entry, 0, end-start)
	for _, recordIndex := range idx.Branch[start:end] {
		if err := validateContext(ctx); err != nil {
			return nil, err
		}
		entry, err := readLocatedEntry(file, idx.Records[recordIndex])
		if err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	return entries, nil
}
