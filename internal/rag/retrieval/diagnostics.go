package retrieval

type ChannelDiagnostic struct {
	Channel MatchType `json:"channel"`
	Error   string    `json:"error"`
}

type Diagnostics struct {
	ModeUsed MatchType           `json:"mode_used"`
	Degraded bool                `json:"degraded"`
	Channels []ChannelDiagnostic `json:"channels,omitempty"`
}
