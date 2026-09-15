package sandbox

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

const sandboxFingerprintVersion = 1

type fingerprintPathRule struct {
	Root   string      `json:"root"`
	Access AccessLevel `json:"access"`
	Source RuleSource  `json:"source"`
}

type fingerprintPayload struct {
	Version int `json:"version"`

	Profile     Profile     `json:"profile"`
	NetworkMode NetworkMode `json:"network_mode"`
	NativeMode  NativeMode  `json:"native_mode"`

	WorkspaceRoot string                `json:"workspace_root"`
	PathRules     []fingerprintPathRule `json:"path_rules,omitempty"`

	Platform    string `json:"platform"`
	Backend     string `json:"backend"`
	Filesystem  bool   `json:"filesystem"`
	ProcessTree bool   `json:"process_tree"`
	Network     bool   `json:"network"`
}

// Fingerprint 返回当前 EffectivePolicy 的稳定安全身份。
//
// Permission 的可复用 Allow Rule 必须绑定这个值。Workspace、PathRules、网络策略、
// Native Sandbox 模式或平台隔离能力变化后，旧授权自然失效并重新询问，避免“用户在旧
// 安全边界下批准的能力”静默扩展到新的环境。
func (p EffectivePolicy) Fingerprint() (string, error) {
	if err := p.Validate(); err != nil {
		return "", fmt.Errorf("计算 Sandbox Fingerprint 失败: %w", err)
	}

	rules := make([]fingerprintPathRule, 0, len(p.PathRules))
	for _, rule := range p.PathRules {
		rules = append(rules, fingerprintPathRule{
			Root:   normalizeFingerprintPath(rule.Root),
			Access: rule.Access,
			Source: rule.Source,
		})
	}
	sort.SliceStable(rules, func(i, j int) bool {
		if rules[i].Root != rules[j].Root {
			return rules[i].Root < rules[j].Root
		}
		if rules[i].Access != rules[j].Access {
			return rules[i].Access < rules[j].Access
		}
		return rules[i].Source < rules[j].Source
	})

	payload := fingerprintPayload{
		Version:       sandboxFingerprintVersion,
		Profile:       p.Profile,
		NetworkMode:   p.NetworkMode,
		NativeMode:    p.NativeMode,
		WorkspaceRoot: normalizeFingerprintPath(p.WorkspaceRoot),
		PathRules:     rules,
		Platform:      strings.ToLower(strings.TrimSpace(p.Capability.Platform)),
		Backend:       strings.ToLower(strings.TrimSpace(p.Capability.Backend)),
		Filesystem:    p.Capability.Filesystem,
		ProcessTree:   p.Capability.ProcessTree,
		Network:       p.Capability.Network,
	}

	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("编码 Sandbox Fingerprint 失败: %w", err)
	}
	digest := sha256.Sum256(encoded)
	return "sbx1:" + hex.EncodeToString(digest[:]), nil
}

func normalizeFingerprintPath(value string) string {
	value = filepath.Clean(strings.TrimSpace(value))
	if runtime.GOOS == "windows" {
		value = strings.ToLower(value)
	}
	return value
}
