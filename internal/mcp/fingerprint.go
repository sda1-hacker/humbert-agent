package mcp

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
)

// ServerFingerprint 对会改变 MCP Server 安全身份的连接字段生成稳定摘要。
//
// Display Name 不参与；Key/Transport/Endpoint/Command/Args/WorkingDirectory 参与。后续
// Permission 条件会使用该摘要使“修改 Server 指向”自动失效旧长期授权。
func ServerFingerprint(server Server) string {
	payload := struct {
		Key       string       `json:"key"`
		Transport Transport    `json:"transport"`
		Stdio     *StdioConfig `json:"stdio,omitempty"`
		HTTP      *HTTPConfig  `json:"http,omitempty"`
	}{
		Key:       strings.TrimSpace(server.Key),
		Transport: server.Transport,
		Stdio:     server.Stdio,
		HTTP:      server.HTTP,
	}
	encoded, _ := json.Marshal(payload)
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}
