package memory

import "time"

const (
	// CurrentVersion 是 Session Memory sidecar 当前格式版本。
	// memory.json 是可重建派生状态，因此当前开发阶段不维护复杂迁移链；遇到未知版本时
	// Manager 会把它视为不可用状态，并在下一次刷新时基于 Transcript 重新生成。
	CurrentVersion = 2

	importantFactsHeading = "### 重要事实"
	timelineHeading       = "### 事情经过"
)

// Cursor 标记 memory.json 已经覆盖到当前 Session Tree 的哪一个节点。
//
// CoveredLeafID 不能单独判断摘要是否仍属于当前 ActiveBranch：Retry/Fork 后可能存在
// 相同数量但不同 lineage 的节点。因此同时持久化 LineageHash；增量更新前必须验证二者
// 都与当前 ActiveBranch 的前缀一致，否则转为完整重建。
type Cursor struct {
	CoveredLeafID string `json:"coveredLeafId"`

	LineageHash string `json:"lineageHash"`
}

// Artifacts 保存从 ToolCall 参数中确定性提取出的工作产物信息。
//
// 这些字段不依赖 LLM 是否在摘要中提及某个文件，因此可用于后续 UI、跨 Session Memory
// 编译和调试。这里只记录路径等低敏感元数据，不记录文件正文、Credential 或完整 Tool
// Result。
type Artifacts struct {
	ReadFiles []string `json:"readFiles,omitempty"`

	ModifiedFiles []string `json:"modifiedFiles,omitempty"`
}

// SourceRange 记录 Session Memory 所覆盖的原始会话范围。Memory 是派生参考数据，
// 真正事实仍以这些 Entry 对应的 session.jsonl 为准。
type SourceRange struct {
	FirstEntryID string `json:"firstEntryId,omitempty"`
	LastEntryID  string `json:"lastEntryId,omitempty"`
	EntryCount   int    `json:"entryCount,omitempty"`
}

// Document 是单 Session memory.json 的完整派生状态。
//
// Summary 必须严格使用“重要事实/事情经过”两个标题。主模型只注入“重要事实”部分，
// Timeline 保留在 sidecar 中用于后续增量合并与未来跨 Session Memory 编译，避免同一事件
// 同时出现在 Compaction Checkpoint、Recent Messages 和 Memory Timeline 中造成重复上下文。
type Document struct {
	Version int `json:"version"`

	SessionID string `json:"sessionId"`

	Cursor Cursor `json:"cursor"`

	Summary string `json:"summary"`

	Artifacts Artifacts `json:"artifacts"`

	Sources SourceRange `json:"sources"`

	UpdatedAt time.Time `json:"updatedAt"`
}

// RefreshResult 描述一次 Session Memory 刷新结果。
//
// Updated=false 表示尚未达到自动刷新阈值且没有 force；这不是错误。Rebuilt=true 表示旧
// Cursor 已不属于当前 ActiveBranch（例如 Retry/Fork），本次没有增量合并旧摘要，而是基于
// 当前分支重新生成。
type RefreshResult struct {
	Updated bool `json:"updated"`

	Rebuilt bool `json:"rebuilt"`

	CoveredLeafID string `json:"coveredLeafId,omitempty"`

	PendingUserTurns int `json:"pendingUserTurns"`

	PendingTokens int `json:"pendingTokens"`
}
