package retrieval

// ChannelDiagnostic 单路召回失败信息，供混合检索降级诊断使用。
type ChannelDiagnostic struct {
	Channel MatchType `json:"channel"`
	Error   string    `json:"error"`
}

// Diagnostics 实际检索方式与通道降级信息。
type Diagnostics struct {
	ModeUsed MatchType           `json:"mode_used"`
	Degraded bool                `json:"degraded"`
	Channels []ChannelDiagnostic `json:"channels,omitempty"`
}
