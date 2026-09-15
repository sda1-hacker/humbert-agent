package approval

import (
	"bytes"
	"encoding/gob"
	"strings"
	"testing"

	"github.com/sda1-hacker/humbert-agent/internal/permission"
)

// TestEncodeInterruptKeepsRawArgumentsOutOfVisibleInfo 验证 Approval 最关键的数据边界：
// 前端可见的 InterruptInfo 不能包含原始 Tool Arguments；只有 Eino checkpoint 内部 state
// 才保存原参数，以便批准后执行与当时请求完全一致的调用。
func TestEncodeInterruptKeepsRawArgumentsOutOfVisibleInfo(t *testing.T) {
	t.Parallel()

	info := InterruptInfo{
		ApprovalID: "approval-1",
		RequestID:  "request-1",
		RunID:      "run-1",
		SessionID:  "session-1",
		AgentID:    "agent-1",
		ToolName:   "write_file",
		Risk:       permission.RiskWrite,
		Identity: permission.CapabilityIdentity{
			Version:            permission.CapabilityIdentityVersion,
			Kind:               permission.CapabilityBuiltin,
			Tool:               "write_file",
			Risk:               permission.RiskWrite,
			SandboxFingerprint: "sbx1:test",
		},
		Presentation: permission.Presentation{
			Title: "请求写入文件",
		},
	}
	secret := "do-not-expose-this-body"
	arguments := `{"path":"notes.txt","content":"` + secret + `"}`

	infoJSON, stateJSON, err := EncodeInterrupt(info, arguments)
	if err != nil {
		t.Fatalf("EncodeInterrupt() error = %v", err)
	}
	if strings.Contains(infoJSON, secret) {
		t.Fatal("用户可见 InterruptInfo 泄漏了原始 Tool Arguments")
	}
	if !strings.Contains(stateJSON, secret) {
		t.Fatal("内部 checkpoint state 必须保留原始参数以保证安全恢复")
	}

	state, err := DecodeInterruptState(stateJSON)
	if err != nil {
		t.Fatalf("DecodeInterruptState() error = %v", err)
	}
	if state.Arguments != arguments {
		t.Fatalf("checkpoint arguments = %q, want %q", state.Arguments, arguments)
	}
}

// TestCheckpointGobDynamicJSONContainers 验证 Eino ADK checkpoint 最容易踩到的动态 JSON
// 容器已经注册。Provider/Model 经常把额外元数据放进 interface{}；如果 map/slice 未注册，
// StatefulInterrupt 在真正调用 CheckPointStore.Set 之前就会被 gob 序列化错误打断。
func TestCheckpointGobDynamicJSONContainers(t *testing.T) {
	t.Parallel()

	type envelope struct {
		Value any
	}
	payload := envelope{Value: map[string]any{
		"provider": "test",
		"nested": []any{
			map[string]any{"ok": true},
			float64(1),
		},
	}}

	var buffer bytes.Buffer
	if err := gob.NewEncoder(&buffer).Encode(payload); err != nil {
		t.Fatalf("checkpoint gob encode dynamic JSON failed: %v", err)
	}

	var decoded envelope
	if err := gob.NewDecoder(&buffer).Decode(&decoded); err != nil {
		t.Fatalf("checkpoint gob decode dynamic JSON failed: %v", err)
	}
	if decoded.Value == nil {
		t.Fatal("decoded checkpoint dynamic JSON is nil")
	}
}
