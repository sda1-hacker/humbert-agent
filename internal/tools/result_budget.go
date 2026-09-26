package tools

import "sync"

const (
	defaultRecoveryReserveDivisor = 4
	defaultRecoveryReserveMin     = 2048
)

// ResultBudget controls how much newly generated ToolResult content may be
// injected into the model context during one turn.
//
// The budget is intentionally split into two logical pools:
//   - general budget: normal tool results and archived-result previews;
//   - recovery budget: context_resource reads used to recover already archived
//     or previously omitted content.
//
// Normal tools are never allowed to consume the recovery reserve. A recovery
// read may use the reserve and any still-unused general capacity. This keeps a
// useful context_resource read possible after large tool outputs have already
// forced results into the artifact store.
//
// Historical ToolResults are NOT charged to this object. Historical context is
// already bounded by ContextEngine projection/compaction; charging it again here
// would make a fresh turn begin with part (or all) of its result budget spent.
type ResultBudget struct {
	mu sync.Mutex

	limit           int
	recoveryReserve int
	generalUsed     int
	recoveryUsed    int
}

// NewResultBudget creates a per-turn result budget with a recovery reserve.
//
// The default reserve is 25% of the total budget, with a 2K minimum for normal
// production-sized windows. It is capped at half of the total budget so small
// synthetic/test budgets still retain usable general capacity.
func NewResultBudget(limit int) *ResultBudget {
	return NewResultBudgetWithRecovery(limit, defaultRecoveryReserve(limit))
}

// NewResultBudgetWithRecovery is primarily useful for deterministic tests and
// callers that need an explicit split. recoveryReserve is clamped to [0, limit].
func NewResultBudgetWithRecovery(limit int, recoveryReserve int) *ResultBudget {
	if limit < 0 {
		limit = 0
	}
	if recoveryReserve < 0 {
		recoveryReserve = 0
	}
	if recoveryReserve > limit {
		recoveryReserve = limit
	}
	return &ResultBudget{
		limit:           limit,
		recoveryReserve: recoveryReserve,
	}
}

func defaultRecoveryReserve(limit int) int {
	if limit <= 0 {
		return 0
	}
	reserve := limit / defaultRecoveryReserveDivisor
	if reserve < defaultRecoveryReserveMin {
		reserve = defaultRecoveryReserveMin
	}
	maxReserve := limit / 2
	if reserve > maxReserve {
		reserve = maxReserve
	}
	if reserve > limit {
		reserve = limit
	}
	if reserve < 0 {
		return 0
	}
	return reserve
}

// reserveFull reserves space for a normal ToolResult. Normal results may use
// only the general pool and can never consume the recovery reserve.
func (b *ResultBudget) reserveFull(chars int) bool {
	if b == nil {
		return true
	}
	if chars < 0 {
		return false
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	if chars > b.generalRemainingLocked() {
		return false
	}
	b.generalUsed += chars
	return true
}

// reservePreview reserves as much normal-result preview space as is currently
// available. The recovery reserve is never consumed by previews.
func (b *ResultBudget) reservePreview(want int) int {
	if b == nil {
		if want < 0 {
			return 0
		}
		return want
	}
	if want < 0 {
		want = 0
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	remaining := b.generalRemainingLocked()
	if want > remaining {
		want = remaining
	}
	b.generalUsed += want
	return want
}

// reserveRecovery reserves space for a context_resource result. Recovery reads
// may use both the dedicated reserve and any general capacity that remains
// unused, but they can never exceed the total per-turn result budget.
func (b *ResultBudget) reserveRecovery(chars int) bool {
	if b == nil {
		return true
	}
	if chars < 0 {
		return false
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	if chars > b.recoveryRemainingLocked() {
		return false
	}
	b.recoveryUsed += chars
	return true
}

// Usage returns total newly injected result characters and the total budget.
func (b *ResultBudget) Usage() (int, int) {
	if b == nil {
		return 0, 0
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.generalUsed + b.recoveryUsed, b.limit
}

// GeneralUsage returns normal-result usage and the normal-result ceiling. The
// ceiling excludes the protected recovery reserve.
func (b *ResultBudget) GeneralUsage() (int, int) {
	if b == nil {
		return 0, 0
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.generalUsed, b.generalLimitLocked()
}

// RecoveryUsage returns characters consumed by context_resource and the
// configured protected reserve. Recovery may additionally borrow unused general
// capacity, so recoveryUsed can legitimately exceed recoveryReserve.
func (b *ResultBudget) RecoveryUsage() (int, int) {
	if b == nil {
		return 0, 0
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.recoveryUsed, b.recoveryReserve
}

// GeneralRemaining reports how many more normal-result characters can be
// admitted without consuming recovery capacity.
func (b *ResultBudget) GeneralRemaining() int {
	if b == nil {
		return 0
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.generalRemainingLocked()
}

// RecoveryRemaining reports how many more context_resource result characters
// can still be admitted. This includes the dedicated reserve plus any general
// capacity that has not already been consumed.
func (b *ResultBudget) RecoveryRemaining() int {
	if b == nil {
		return 0
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.recoveryRemainingLocked()
}

func (b *ResultBudget) generalLimitLocked() int {
	limit := b.limit - b.recoveryReserve
	if limit < 0 {
		return 0
	}
	return limit
}

func (b *ResultBudget) generalRemainingLocked() int {
	byGeneralPool := b.generalLimitLocked() - b.generalUsed
	if byGeneralPool < 0 {
		byGeneralPool = 0
	}

	byTotalPool := b.limit - b.generalUsed - b.recoveryUsed
	if byTotalPool < 0 {
		byTotalPool = 0
	}

	if byGeneralPool < byTotalPool {
		return byGeneralPool
	}
	return byTotalPool
}

func (b *ResultBudget) recoveryRemainingLocked() int {
	remaining := b.limit - b.generalUsed - b.recoveryUsed
	if remaining < 0 {
		return 0
	}
	return remaining
}

// SeedUsed is kept for source compatibility with older Resolver code.
//
// Deprecated: historical ToolResults must not consume the current turn's
// ResultBudget. ContextEngine already limits/compacts retained history before
// the turn starts, so this method intentionally does nothing.
func (b *ResultBudget) SeedUsed(_ int) {}
