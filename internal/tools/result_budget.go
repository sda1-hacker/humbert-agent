package tools

import "sync"

// ResultBudget accounts for direct tool result content across one turn. A tool
// result that cannot fit is archived; its small reference remains recoverable.
type ResultBudget struct {
	mu          sync.Mutex
	limit, used int
}

func NewResultBudget(limit int) *ResultBudget {
	if limit < 0 {
		limit = 0
	}
	return &ResultBudget{limit: limit}
}
func (b *ResultBudget) reserveFull(chars int) bool {
	if b == nil {
		return true
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if chars < 0 || b.used+chars > b.limit {
		return false
	}
	b.used += chars
	return true
}
func (b *ResultBudget) reservePreview(want int) int {
	if b == nil {
		return want
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	remaining := b.limit - b.used
	if remaining < 0 {
		remaining = 0
	}
	if want > remaining {
		want = remaining
	}
	if want < 0 {
		want = 0
	}
	b.used += want
	return want
}
func (b *ResultBudget) Usage() (int, int) {
	if b == nil {
		return 0, 0
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.used, b.limit
}

// SeedUsed reserves space already occupied by retained tool results before
// this turn's tools execute. ResolveTurn calls it once after final compaction.
func (b *ResultBudget) SeedUsed(chars int) {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if chars < 0 {
		chars = 0
	}
	if chars > b.limit {
		chars = b.limit
	}
	b.used = chars
}
