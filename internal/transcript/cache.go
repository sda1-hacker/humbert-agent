package transcript

import (
	"container/list"
	"os"
	"sync"
)

const (
	defaultDocumentCacheEntries = 32
	defaultDocumentCacheBytes   = int64(64 * 1024 * 1024)
)

// documentCache 是 JSONL 的进程内、可丢弃投影。JSONL 始终是唯一权威数据；缓存命中
// 必须同时满足文件身份、大小和修改时间一致，任何不一致都会删除旧项并严格重建。
//
// LRU 同时限制 Session 数和对应 JSONL 字节数，避免打开大量历史 Session 后无界占用内存。
type documentCache struct {
	mu sync.Mutex

	values map[string]*documentCacheItem
	lru    *list.List

	totalBytes int64
	maxEntries int
	maxBytes   int64
}

type documentCacheItem struct {
	path string

	document         Document
	messageIndexes   []int
	messagePositions map[string]int
	fileInfo         os.FileInfo
	cost             int64

	element *list.Element
}

func newDocumentCache() *documentCache {
	return &documentCache{
		values:     make(map[string]*documentCacheItem),
		lru:        list.New(),
		maxEntries: defaultDocumentCacheEntries,
		maxBytes:   defaultDocumentCacheBytes,
	}
}

// readOnly 只供持有对应 Transcript 文件锁的 Store 内部路径使用。返回值不得修改；这样
// Append 可以读取 Leaf/ActiveBranch 而无需先复制整个 Document。
func (c *documentCache) readOnly(path string, current os.FileInfo) (Document, bool) {
	if c == nil || current == nil {
		return Document{}, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	item := c.values[path]
	if item == nil {
		return Document{}, false
	}
	if !sameTranscriptFile(item.fileInfo, current) {
		c.removeLocked(item)
		return Document{}, false
	}
	c.lru.MoveToFront(item.element)
	return item.document, true
}

// putOwned 接管一个不再由调用方修改或向外暴露的 Document。
func (c *documentCache) putOwned(path string, document Document, info os.FileInfo) {
	if c == nil || info == nil || info.Size() > c.maxBytes {
		if c != nil {
			c.invalidate(path)
		}
		return
	}
	document.Repair = RepairResult{}

	c.mu.Lock()
	defer c.mu.Unlock()
	if existing := c.values[path]; existing != nil {
		c.removeLocked(existing)
	}
	item := &documentCacheItem{
		path: path, document: document, fileInfo: info, cost: info.Size(),
	}
	item.messageIndexes, item.messagePositions = indexMessages(document.ActiveBranch)
	item.element = c.lru.PushFront(item)
	c.values[path] = item
	c.totalBytes += item.cost
	c.evictLocked()
}

// advance 在一次成功 fsync/close 后推进缓存。调用方必须持有对应 Transcript 文件锁，
// before 必须是写入前观察到的文件状态；不满足时宁可失效重建，也不猜测缓存状态。
func (c *documentCache) advance(path string, before os.FileInfo, after os.FileInfo, entry Entry) bool {
	if c == nil || before == nil || after == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	item := c.values[path]
	if item == nil || !sameTranscriptFile(item.fileInfo, before) {
		if item != nil {
			c.removeLocked(item)
		}
		return false
	}
	if after.Size() > c.maxBytes {
		c.removeLocked(item)
		return false
	}

	item.document.Entries = append(item.document.Entries, entry)
	item.document.ActiveBranch = append(item.document.ActiveBranch, entry)
	item.document.LeafID = entry.ID
	if entry.Type == EntryMessage && entry.Message != nil {
		position := len(item.messageIndexes)
		item.messageIndexes = append(item.messageIndexes, len(item.document.ActiveBranch)-1)
		item.messagePositions[entry.ID] = position
	}
	c.totalBytes += after.Size() - item.cost
	item.cost = after.Size()
	item.fileInfo = after
	c.lru.MoveToFront(item.element)
	c.evictLocked()
	return true
}

func (c *documentCache) messagePage(
	path string,
	current os.FileInfo,
	beforeEntryID string,
	limit int,
) (MessageEntryPage, bool, error) {
	if c == nil || current == nil {
		return MessageEntryPage{}, false, nil
	}
	c.mu.Lock()
	item := c.values[path]
	if item == nil {
		c.mu.Unlock()
		return MessageEntryPage{}, false, nil
	}
	if !sameTranscriptFile(item.fileInfo, current) {
		c.removeLocked(item)
		c.mu.Unlock()
		return MessageEntryPage{}, false, nil
	}
	c.lru.MoveToFront(item.element)
	branch := item.document.ActiveBranch
	messageIndexes := item.messageIndexes
	messagePositions := item.messagePositions
	c.mu.Unlock()

	page, err := messageEntryPageFromIndex(branch, messageIndexes, messagePositions, beforeEntryID, limit)
	return page, true, err
}

func (c *documentCache) invalidate(path string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if item := c.values[path]; item != nil {
		c.removeLocked(item)
	}
}

func (c *documentCache) evictLocked() {
	for len(c.values) > c.maxEntries || c.totalBytes > c.maxBytes {
		element := c.lru.Back()
		if element == nil {
			return
		}
		c.removeLocked(element.Value.(*documentCacheItem))
	}
}

func (c *documentCache) removeLocked(item *documentCacheItem) {
	delete(c.values, item.path)
	c.lru.Remove(item.element)
	c.totalBytes -= item.cost
}

func sameTranscriptFile(cached os.FileInfo, current os.FileInfo) bool {
	return cached != nil && current != nil &&
		cached.Size() == current.Size() &&
		cached.ModTime().Equal(current.ModTime()) &&
		os.SameFile(cached, current)
}

func indexMessages(activeBranch []Entry) ([]int, map[string]int) {
	indexes := make([]int, 0, len(activeBranch))
	positions := make(map[string]int)
	for branchIndex, entry := range activeBranch {
		if entry.Type != EntryMessage || entry.Message == nil {
			continue
		}
		positions[entry.ID] = len(indexes)
		indexes = append(indexes, branchIndex)
	}
	return indexes, positions
}

func cloneDocument(source Document) Document {
	entries := cloneEntries(source.Entries)
	entryByID := make(map[string]Entry, len(entries))
	for _, entry := range entries {
		entryByID[entry.ID] = entry
	}
	activeBranch := make([]Entry, len(source.ActiveBranch))
	for index, entry := range source.ActiveBranch {
		if cloned, exists := entryByID[entry.ID]; exists {
			// 与 loadLocked 的 Document 一致：Entries/ActiveBranch 的 Entry 值独立，
			// 但不可变的 Message/Details payload 共享，避免为当前分支重复一份大内容。
			activeBranch[index] = cloned
		} else {
			activeBranch[index] = cloneEntry(entry)
		}
	}
	return Document{
		Header:       source.Header,
		Entries:      entries,
		ActiveBranch: activeBranch,
		LeafID:       source.LeafID,
		Repair:       source.Repair,
	}
}

func cloneEntries(source []Entry) []Entry {
	if source == nil {
		return nil
	}
	result := make([]Entry, len(source))
	for index := range source {
		result[index] = cloneEntry(source[index])
	}
	return result
}

func cloneEntry(source Entry) Entry {
	result := source
	result.ParentID = cloneStringPointer(source.ParentID)
	result.FromID = cloneStringPointer(source.FromID)
	result.Label = cloneStringPointer(source.Label)
	result.Data = append([]byte(nil), source.Data...)
	if source.Message != nil {
		message := *source.Message
		message.Content = append([]ContentBlock(nil), source.Message.Content...)
		for index := range message.Content {
			message.Content[index].Arguments = append([]byte(nil), message.Content[index].Arguments...)
		}
		message.Details = append([]byte(nil), source.Message.Details...)
		if source.Message.Usage != nil {
			usage := *source.Message.Usage
			message.Usage = &usage
		}
		result.Message = &message
	}
	if source.Details != nil {
		details := *source.Details
		details.ReadFiles = append([]string(nil), source.Details.ReadFiles...)
		details.ModifiedFiles = append([]string(nil), source.Details.ModifiedFiles...)
		result.Details = &details
	}
	return result
}

func cloneStringPointer(value *string) *string {
	if value == nil {
		return nil
	}
	result := *value
	return &result
}
