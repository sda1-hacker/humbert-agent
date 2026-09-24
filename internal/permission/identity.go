package permission

import (
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
)

const CapabilityIdentityVersion = 1

// CapabilityKind 描述一次受控能力的稳定类别。
type CapabilityKind string

const (
	CapabilityBuiltin     CapabilityKind = "builtin"
	CapabilityCommand     CapabilityKind = "command"
	CapabilitySkillScript CapabilityKind = "skill_script"
	CapabilityMCP         CapabilityKind = "mcp"
)

// CapabilityIdentity 是 Permission v2 的可复用授权身份。
//
// Allow Rule 必须精确绑定完整 Identity。这样 Workspace/Sandbox、可执行文件、Skill 内容或
// MCP Server 安全身份发生变化时，旧 Allow 不会继续自动命中。
//
// Deny Rule 会在创建时通过 DenyScope() 收敛为更稳定的限制身份，例如 run_command 只绑定
// command 名称、MCP 只绑定 ServerID + Tool，从而保证环境变化不会意外放宽用户已经保存的拒绝。
type CapabilityIdentity struct {
	Version int            `json:"version,omitempty"`
	Kind    CapabilityKind `json:"kind,omitempty"`

	Tool string    `json:"tool,omitempty"`
	Risk RiskLevel `json:"risk,omitempty"`

	SandboxFingerprint string `json:"sandboxFingerprint,omitempty"`

	Command    string `json:"command,omitempty"`
	Executable string `json:"executable,omitempty"`
	// InvocationFingerprint 绑定完整 argv、实际工作目录及超时，不保存可能含密钥的原始参数。
	InvocationFingerprint string `json:"invocationFingerprint,omitempty"`

	SkillName     string `json:"skillName,omitempty"`
	SkillIdentity string `json:"skillIdentity,omitempty"`
	Script        string `json:"script,omitempty"`

	MCPServerID          string `json:"mcpServerId,omitempty"`
	MCPServerFingerprint string `json:"mcpServerFingerprint,omitempty"`
	MCPTool              string `json:"mcpTool,omitempty"`
}

func (i CapabilityIdentity) Empty() bool {
	return i.Version == 0 && i.Kind == "" && strings.TrimSpace(i.Tool) == "" &&
		strings.TrimSpace(i.SandboxFingerprint) == "" && strings.TrimSpace(i.Command) == "" &&
		strings.TrimSpace(i.Executable) == "" && strings.TrimSpace(i.SkillName) == "" &&
		strings.TrimSpace(i.InvocationFingerprint) == "" &&
		strings.TrimSpace(i.SkillIdentity) == "" && strings.TrimSpace(i.Script) == "" &&
		strings.TrimSpace(i.MCPServerID) == "" && strings.TrimSpace(i.MCPServerFingerprint) == "" &&
		strings.TrimSpace(i.MCPTool) == ""
}

// Normalize 返回用于比较和持久化的 canonical identity。
func (i CapabilityIdentity) Normalize() CapabilityIdentity {
	i.Tool = strings.TrimSpace(i.Tool)
	i.SandboxFingerprint = strings.TrimSpace(i.SandboxFingerprint)
	i.Command = strings.TrimSpace(i.Command)
	i.Executable = normalizeIdentityPath(i.Executable)
	i.InvocationFingerprint = strings.TrimSpace(i.InvocationFingerprint)
	i.SkillName = strings.TrimSpace(i.SkillName)
	i.SkillIdentity = strings.TrimSpace(i.SkillIdentity)
	i.Script = strings.TrimSpace(strings.ReplaceAll(i.Script, "\\", "/"))
	i.MCPServerID = strings.TrimSpace(i.MCPServerID)
	i.MCPServerFingerprint = strings.TrimSpace(i.MCPServerFingerprint)
	i.MCPTool = strings.TrimSpace(i.MCPTool)
	if runtime.GOOS == "windows" {
		i.Command = strings.ToLower(i.Command)
	}
	return i
}

func (i CapabilityIdentity) Validate() error {
	i = i.Normalize()
	if i.Version != CapabilityIdentityVersion {
		return fmt.Errorf("Capability Identity Version %d 不受支持", i.Version)
	}
	if i.Tool == "" {
		return errors.New("Capability Identity Tool 不能为空")
	}
	switch i.Risk {
	case RiskRead, RiskWrite, RiskExec:
	default:
		return fmt.Errorf("Capability Identity Risk %q 不合法", i.Risk)
	}
	if i.SandboxFingerprint == "" {
		return errors.New("Capability Identity SandboxFingerprint 不能为空")
	}

	switch i.Kind {
	case CapabilityBuiltin:
		if i.Command != "" || i.Executable != "" || i.SkillName != "" || i.MCPServerID != "" {
			return errors.New("Builtin Capability Identity 包含不属于 Builtin 的字段")
		}
	case CapabilityCommand:
		if i.Command == "" || i.Executable == "" {
			return errors.New("Command Capability Identity 必须包含 Command 与 Executable")
		}
	case CapabilitySkillScript:
		if i.SkillName == "" || i.SkillIdentity == "" || i.Script == "" || i.Command == "" || i.Executable == "" {
			return errors.New("Skill Script Capability Identity 必须包含 SkillName/SkillIdentity/Script/Command/Executable")
		}
	case CapabilityMCP:
		if i.MCPServerID == "" || i.MCPServerFingerprint == "" || i.MCPTool == "" {
			return errors.New("MCP Capability Identity 必须包含 ServerID/Fingerprint/Tool")
		}
	default:
		return fmt.Errorf("Capability Identity Kind %q 不受支持", i.Kind)
	}
	return nil
}

// EqualExact 用于 Allow Rule：所有安全身份字段必须精确一致。
func (i CapabilityIdentity) EqualExact(other CapabilityIdentity) bool {
	i = i.Normalize()
	other = other.Normalize()
	return i == other
}

// DenyScope 返回适合作为长期拒绝的稳定身份。
//
// Deny 的目标是“继续收紧”而不是绑定某个暂时环境，因此主动去掉 Sandbox Fingerprint、
// executable path、Skill package identity、MCP fingerprint 等易变化字段。
func (i CapabilityIdentity) DenyScope() CapabilityIdentity {
	i = i.Normalize()
	result := CapabilityIdentity{
		Version: CapabilityIdentityVersion,
		Kind:    i.Kind,
		Tool:    i.Tool,
	}
	switch i.Kind {
	case CapabilityCommand:
		result.Command = i.Command
	case CapabilitySkillScript:
		result.SkillName = i.SkillName
		result.Script = i.Script
	case CapabilityMCP:
		result.MCPServerID = i.MCPServerID
		result.MCPTool = i.MCPTool
	}
	return result
}

// MatchesDeny 判断一个较宽的 Deny Identity 是否覆盖当前完整 Identity。
func (i CapabilityIdentity) MatchesDeny(current CapabilityIdentity) bool {
	i = i.Normalize()
	current = current.Normalize()
	if i.Tool == "" || i.Tool != current.Tool {
		return false
	}
	if i.Kind != "" && i.Kind != current.Kind {
		return false
	}
	if i.Command != "" && i.Command != current.Command {
		return false
	}
	if i.SkillName != "" && i.SkillName != current.SkillName {
		return false
	}
	if i.Script != "" && i.Script != current.Script {
		return false
	}
	if i.MCPServerID != "" && i.MCPServerID != current.MCPServerID {
		return false
	}
	if i.MCPTool != "" && i.MCPTool != current.MCPTool {
		return false
	}
	return true
}

func normalizeIdentityPath(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	value = filepath.Clean(value)
	if runtime.GOOS == "windows" {
		value = strings.ToLower(value)
	}
	return value
}
