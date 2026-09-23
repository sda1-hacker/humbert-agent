package transcript

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// The sidecar contains only entry identities, roles and byte locations. Message bodies
// remain in session.jsonl, which is the sole source of truth. A size/mtime mismatch
// always rebuilds the index before it is used; a crash between the two fsyncs is safe.
const locationIndexName = "session.locations.jsonl"

const (
	maxLocationCacheEntries = 16
	maxLocationCacheBytes   = int64(128 * 1024 * 1024)
	// Records and active-branch metadata hold separate Entry values, maps and
	// slice capacity. This deliberately overestimates their typical heap cost.
	estimatedLocationRecordBytes = int64(900)
)

var errUnsafeLocationIndex = errors.New("Session 位置索引不是安全普通文件")

type locationHeader struct {
	Version           int           `json:"version"`
	Header            SessionHeader `json:"header"`
	HeaderBytes       int64         `json:"headerBytes"`
	TranscriptSize    int64         `json:"transcriptSize"`
	TranscriptModNano int64         `json:"transcriptModNano"`
}
type locationRecord struct {
	Offset            int64 `json:"offset"`
	Length            int64 `json:"length"`
	Entry             Entry `json:"entry"`
	TranscriptModNano int64 `json:"transcriptModNano"`
}
type locationIndex struct {
	Header           locationHeader
	Records          []locationRecord
	Branch           []int
	Metadata         []Entry
	Positions        map[string]int
	MessageIndexes   []int
	MessagePositions map[string]int
	Window           ContextWindowIndex
	Info             os.FileInfo
	SidecarBytes     int64
}
type locationCache struct {
	mu         sync.Mutex
	values     map[string]*locationIndex
	costs      map[string]int64
	totalBytes int64
	order      []string
}

func newLocationCache() *locationCache {
	return &locationCache{values: make(map[string]*locationIndex), costs: make(map[string]int64)}
}
func (c *locationCache) get(path string, info os.FileInfo) (*locationIndex, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	value := c.values[path]
	if value == nil || !sameTranscriptFile(value.Info, info) {
		c.remove(path)
		return nil, false
	}
	c.removeOrder(path)
	c.order = append(c.order, path)
	return value, true
}
func (c *locationCache) put(path string, value *locationIndex) {
	c.mu.Lock()
	c.remove(path)
	c.values[path] = value
	cost := int64(len(value.Records))*estimatedLocationRecordBytes + value.SidecarBytes
	c.costs[path] = cost
	c.totalBytes += cost
	c.order = append(c.order, path)
	for len(c.order) > maxLocationCacheEntries || (c.totalBytes > maxLocationCacheBytes && len(c.order) > 1) {
		c.remove(c.order[0])
	}
	c.mu.Unlock()
}
func (c *locationCache) invalidate(path string) {
	c.mu.Lock()
	c.remove(path)
	c.mu.Unlock()
}
func (c *locationCache) remove(path string) {
	delete(c.values, path)
	c.totalBytes -= c.costs[path]
	delete(c.costs, path)
	c.removeOrder(path)
}
func (c *locationCache) removeOrder(path string) {
	for i, item := range c.order {
		if item == path {
			c.order = append(c.order[:i], c.order[i+1:]...)
			return
		}
	}
}
func sidecarPath(path string) string { return filepath.Join(filepath.Dir(path), locationIndexName) }
func metadataEntry(entry Entry) Entry {
	if entry.Message != nil {
		entry.Message = &AgentMessage{Role: entry.Message.Role, ToolCallID: entry.Message.ToolCallID, ToolName: entry.Message.ToolName}
	}
	entry.Summary = ""
	entry.Data = nil
	return entry
}
func (idx *locationIndex) rebuildBranch() error {
	entries := make(map[string]Entry, len(idx.Records))
	positions := make(map[string]int, len(idx.Records))
	for i, record := range idx.Records {
		if record.Length <= 0 || record.Offset < idx.Header.HeaderBytes {
			return errors.New("位置索引 offset/length 非法")
		}
		if i == 0 && record.Offset != idx.Header.HeaderBytes {
			return errors.New("位置索引首条 offset 非法")
		}
		if i > 0 && record.Offset != idx.Records[i-1].Offset+idx.Records[i-1].Length {
			return errors.New("位置索引不连续")
		}
		if record.Entry.ID == "" {
			return errors.New("位置索引缺少 Entry ID")
		}
		if _, ok := entries[record.Entry.ID]; ok {
			return errors.New("位置索引 Entry ID 重复")
		}
		if record.Entry.ParentID != nil {
			if _, ok := entries[*record.Entry.ParentID]; !ok {
				return errors.New("位置索引 parentId 无效")
			}
		}
		entries[record.Entry.ID] = record.Entry
		positions[record.Entry.ID] = i
	}
	leaf := ""
	if len(idx.Records) > 0 {
		leaf = idx.Records[len(idx.Records)-1].Entry.ID
	}
	branch, err := buildActiveBranch(entries, leaf)
	if err != nil {
		return err
	}
	idx.Branch = make([]int, 0, len(branch))
	for _, entry := range branch {
		idx.Branch = append(idx.Branch, positions[entry.ID])
	}
	idx.Positions = make(map[string]int, len(branch))
	for i, entry := range branch {
		idx.Positions[entry.ID] = i
	}
	idx.Metadata = branch
	idx.MessageIndexes, idx.MessagePositions = indexMessages(branch)
	idx.Window = contextWindowIndex(branch)
	return nil
}

// appendRecord advances the common linear branch in constant time. Branch and
// message indexes are rebuilt only if a future explicit fork appends to an
// older parent. All returned views remain stable because existing entries are
// never modified.
func (idx *locationIndex) appendRecord(record locationRecord, after os.FileInfo) error {
	meta := record.Entry
	linear := len(idx.Metadata) == 0 && meta.ParentID == nil
	if len(idx.Metadata) > 0 && meta.ParentID != nil && *meta.ParentID == idx.Metadata[len(idx.Metadata)-1].ID {
		linear = true
	}
	idx.Records = append(idx.Records, record)
	idx.Info = after
	if !linear {
		return idx.rebuildBranch()
	}
	branchIndex := len(idx.Metadata)
	idx.Branch = append(idx.Branch, len(idx.Records)-1)
	idx.Metadata = append(idx.Metadata, meta)
	if idx.Positions == nil {
		idx.Positions = make(map[string]int)
	}
	idx.Positions[meta.ID] = branchIndex
	if meta.Type == EntryMessage && meta.Message != nil {
		if idx.MessagePositions == nil {
			idx.MessagePositions = make(map[string]int)
		}
		idx.MessagePositions[meta.ID] = len(idx.MessageIndexes)
		idx.MessageIndexes = append(idx.MessageIndexes, branchIndex)
	}
	if meta.Type == EntryCompaction {
		idx.Window.Valid = true
		idx.Window.Generation++
		idx.Window.LatestCompactionIndex = branchIndex
		first, ok := idx.Positions[meta.FirstKeptEntryID]
		if !ok || first >= branchIndex {
			idx.Window.FirstKeptIndex = -1
		} else {
			idx.Window.FirstKeptIndex = first
		}
	}
	return nil
}
func (idx *locationIndex) branchMetadata() []Entry {
	return idx.Metadata[:len(idx.Metadata):len(idx.Metadata)]
}
func (idx *locationIndex) matches(info os.FileInfo) bool {
	if idx.Header.Version != 1 || idx.Header.HeaderBytes <= 0 || info == nil {
		return false
	}
	size, mod := idx.Header.TranscriptSize, idx.Header.TranscriptModNano
	if len(idx.Records) > 0 {
		last := idx.Records[len(idx.Records)-1]
		size = last.Offset + last.Length
		mod = last.TranscriptModNano
	}
	return size == info.Size() && mod == info.ModTime().UnixNano()
}
func readLocationIndex(path string, info os.FileInfo, sessionID string) (*locationIndex, error) {
	sidecar := sidecarPath(path)
	stat, err := os.Lstat(sidecar)
	if err != nil {
		return nil, err
	}
	if !stat.Mode().IsRegular() || stat.Mode()&os.ModeSymlink != 0 {
		return nil, errUnsafeLocationIndex
	}
	file, err := os.Open(sidecar)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	reader := bufio.NewReader(file)
	line, err := reader.ReadBytes('\n')
	if err != nil {
		return nil, err
	}
	idx := &locationIndex{}
	if err := json.Unmarshal(bytes.TrimSpace(line), &idx.Header); err != nil {
		return nil, err
	}
	if err := validateHeader(idx.Header.Header, sessionID); err != nil {
		return nil, err
	}
	for {
		line, err = reader.ReadBytes('\n')
		if len(line) == 0 && errors.Is(err, io.EOF) {
			break
		}
		if err != nil || len(line) == 0 || len(line) > maxEntryBytes+1 {
			return nil, errors.New("Session 位置索引残缺")
		}
		var record locationRecord
		if json.Unmarshal(bytes.TrimSpace(line), &record) != nil {
			return nil, errors.New("Session 位置索引 JSON 无效")
		}
		idx.Records = append(idx.Records, record)
	}
	if !idx.matches(info) {
		return nil, errors.New("Session 位置索引与 Transcript 不一致")
	}
	if err := idx.rebuildBranch(); err != nil {
		return nil, err
	}
	idx.Info = info
	idx.SidecarBytes = stat.Size()
	return idx, nil
}
func scanLocationIndex(ctx context.Context, path, sessionID string, info os.FileInfo) (*locationIndex, int64, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	defer file.Close()
	reader := bufio.NewReaderSize(file, 64*1024)
	idx := &locationIndex{Info: info}
	offset := int64(0)
	lineNo := 0
	ids := make(map[string]struct{})
	for {
		if err := validateContext(ctx); err != nil {
			return nil, offset, err
		}
		line, readErr := reader.ReadBytes('\n')
		if len(line) == 0 && errors.Is(readErr, io.EOF) {
			break
		}
		lineNo++
		if readErr != nil || len(line) == 0 || line[len(line)-1] != '\n' || len(line) > maxEntryBytes+1 {
			return nil, offset, &CorruptionError{Line: lineNo, Offset: offset, Reason: "位置索引扫描遇到损坏 JSONL"}
		}
		payload := line[:len(line)-1]
		if lineNo == 1 {
			var header SessionHeader
			if decodeStrictJSON(payload, &header) != nil || validateHeader(header, sessionID) != nil {
				return nil, offset, &CorruptionError{Line: 1, Offset: 0, Reason: "Session Header 无效"}
			}
			idx.Header = locationHeader{Version: 1, Header: header, HeaderBytes: int64(len(line)), TranscriptSize: int64(len(line)), TranscriptModNano: info.ModTime().UnixNano()}
		} else {
			var entry Entry
			if decodeStrictJSON(payload, &entry) != nil {
				return nil, offset, &CorruptionError{Line: lineNo, Offset: offset, Reason: "Session Entry JSON 无法解析"}
			}
			if err := validateEntryPayload(entry); err != nil {
				return nil, offset, &CorruptionError{Line: lineNo, Offset: offset, Reason: err.Error()}
			}
			if _, ok := ids[entry.ID]; ok {
				return nil, offset, &CorruptionError{Line: lineNo, Offset: offset, Reason: "Session Entry id 重复"}
			}
			if entry.ParentID != nil {
				if _, ok := ids[*entry.ParentID]; !ok {
					return nil, offset, &CorruptionError{Line: lineNo, Offset: offset, Reason: "parentId 无效"}
				}
			}
			ids[entry.ID] = struct{}{}
			idx.Records = append(idx.Records, locationRecord{Offset: offset, Length: int64(len(line)), Entry: metadataEntry(entry), TranscriptModNano: info.ModTime().UnixNano()})
		}
		offset += int64(len(line))
	}
	if lineNo == 0 {
		return nil, 0, &CorruptionError{Line: 1, Reason: "缺少 Session Header"}
	}
	if offset != info.Size() {
		return nil, offset, errors.New("Transcript 在索引扫描期间发生变化")
	}
	if err := idx.rebuildBranch(); err != nil {
		return nil, offset, err
	}
	return idx, offset, nil
}
func writeLocationIndex(path string, idx *locationIndex) error {
	target := sidecarPath(path)
	if stat, err := os.Lstat(target); err == nil {
		if !stat.Mode().IsRegular() || stat.Mode()&os.ModeSymlink != 0 {
			return errUnsafeLocationIndex
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(target), ".locations-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()
	if err := tmp.Chmod(0o600); err != nil {
		return err
	}
	writer := bufio.NewWriter(tmp)
	encoder := json.NewEncoder(writer)
	if err := encoder.Encode(idx.Header); err != nil {
		return err
	}
	for _, record := range idx.Records {
		if err := encoder.Encode(record); err != nil {
			return err
		}
	}
	if err := writer.Flush(); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), target)
}
func (s *Store) locationIndexLocked(ctx context.Context, path, sessionID string) (*locationIndex, ReadStats, error) {
	repair, err := repairTailLocked(ctx, path)
	if err != nil {
		return nil, ReadStats{}, err
	}
	if repair.Repaired {
		s.locations.invalidate(path)
		s.cache.invalidate(path)
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, ReadStats{}, err
	}
	if idx, ok := s.locations.get(path, info); ok {
		return idx, ReadStats{CacheHit: true}, nil
	}
	if idx, err := readLocationIndex(path, info, sessionID); err == nil {
		s.locations.put(path, idx)
		return idx, ReadStats{BytesRead: idx.SidecarBytes}, nil
	} else if errors.Is(err, errUnsafeLocationIndex) {
		return nil, ReadStats{}, err
	}
	idx, bytesRead, err := scanLocationIndex(ctx, path, sessionID, info)
	if err != nil {
		return nil, ReadStats{}, err
	}
	if err := writeLocationIndex(path, idx); err != nil {
		if errors.Is(err, errUnsafeLocationIndex) {
			return nil, ReadStats{}, err
		}
		// The sidecar is derived. A write failure must not hide otherwise valid
		// conversation history; the next read can rebuild it from JSONL.
	} else if stat, statErr := os.Stat(sidecarPath(path)); statErr == nil {
		idx.SidecarBytes = stat.Size()
	}
	s.locations.put(path, idx)
	return idx, ReadStats{BytesRead: bytesRead, IndexRebuilt: true}, nil
}
func readLocatedEntry(file *os.File, record locationRecord) (Entry, error) {
	if record.Length <= 0 || record.Length > maxEntryBytes+1 {
		return Entry{}, errors.New("位置索引 Entry 长度无效")
	}
	data := make([]byte, record.Length)
	if _, err := file.ReadAt(data, record.Offset); err != nil {
		return Entry{}, err
	}
	if data[len(data)-1] != '\n' {
		return Entry{}, errors.New("位置索引 Entry 边界无效")
	}
	var entry Entry
	if err := decodeStrictJSON(data[:len(data)-1], &entry); err != nil {
		return Entry{}, err
	}
	if entry.ID != record.Entry.ID {
		return Entry{}, errors.New("位置索引 Entry 身份不一致")
	}
	return entry, nil
}
func (s *Store) largeMetadataLocked(ctx context.Context, path, sessionID string) (Document, *locationIndex, error) {
	idx, _, err := s.locationIndexLocked(ctx, path, sessionID)
	if err != nil {
		return Document{}, nil, err
	}
	branch := idx.branchMetadata()
	leaf := ""
	if len(branch) > 0 {
		leaf = branch[len(branch)-1].ID
	}
	return Document{Header: idx.Header.Header, ActiveBranch: branch, LeafID: leaf, ContextWindow: idx.Window}, idx, nil
}
func (s *Store) largeContextSessionLocked(ctx context.Context, path, sessionID string) (Document, error) {
	idx, stats, err := s.locationIndexLocked(ctx, path, sessionID)
	if err != nil {
		return Document{}, err
	}
	lineage := idx.branchMetadata()
	start := 0
	if idx.Window.LatestCompactionIndex >= 0 {
		start = idx.Window.FirstKeptIndex
	}
	if start < 0 || start > len(idx.Branch) {
		return Document{}, errors.New("Compaction Window 位置索引无效")
	}
	file, err := os.Open(path)
	if err != nil {
		return Document{}, err
	}
	defer file.Close()
	branch := make([]Entry, 0, len(idx.Branch)-start)
	for _, recordIndex := range idx.Branch[start:] {
		if err := validateContext(ctx); err != nil {
			return Document{}, err
		}
		record := idx.Records[recordIndex]
		entry, err := readLocatedEntry(file, record)
		if err != nil {
			return Document{}, err
		}
		stats.BytesRead += record.Length
		branch = append(branch, entry)
	}
	window := idx.Window
	if window.LatestCompactionIndex >= 0 {
		window.LatestCompactionIndex -= start
		window.FirstKeptIndex = 0
	}
	leaf := ""
	if len(lineage) > 0 {
		leaf = lineage[len(lineage)-1].ID
	}
	return Document{Header: idx.Header.Header, ActiveBranch: branch, Lineage: lineage, LeafID: leaf, ContextWindow: window, ReadStats: stats}, nil
}
func (s *Store) largeMessagePageLocked(ctx context.Context, path, sessionID, beforeID string, limit int) (MessageEntryPage, error) {
	idx, _, err := s.locationIndexLocked(ctx, path, sessionID)
	if err != nil {
		return MessageEntryPage{}, err
	}
	branch := idx.branchMetadata()
	page, err := messageEntryPageFromIndex(branch, idx.MessageIndexes, idx.MessagePositions, strings.TrimSpace(beforeID), limit)
	if err != nil {
		return page, err
	}
	file, err := os.Open(path)
	if err != nil {
		return page, err
	}
	defer file.Close()
	for i := range page.Entries {
		p := idx.Positions[page.Entries[i].ID]
		page.Entries[i], err = readLocatedEntry(file, idx.Records[idx.Branch[p]])
		if err != nil {
			return MessageEntryPage{}, err
		}
	}
	return page, nil
}
func (s *Store) advanceLocationLocked(path string, before, after os.FileInfo, entry Entry, lineLength int64, preferred *locationIndex) {
	idx := preferred
	if idx == nil || !sameTranscriptFile(idx.Info, before) {
		var ok bool
		idx, ok = s.locations.get(path, before)
		if !ok {
			return
		}
	}
	record := locationRecord{Offset: before.Size(), Length: lineLength, Entry: metadataEntry(entry), TranscriptModNano: after.ModTime().UnixNano()}
	if idx.appendRecord(record, after) != nil {
		s.locations.invalidate(path)
		return
	}
	defer s.locations.put(path, idx)
	sidecar := sidecarPath(path)
	stat, err := os.Lstat(sidecar)
	if err != nil || !stat.Mode().IsRegular() || stat.Mode()&os.ModeSymlink != 0 {
		return
	}
	file, err := os.OpenFile(sidecar, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return
	}
	encoded, err := json.Marshal(record)
	if err == nil {
		encoded = append(encoded, '\n')
		err = writeFull(file, encoded)
	}
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil || closeErr != nil {
		return
	}
	idx.SidecarBytes += int64(len(encoded))
}
