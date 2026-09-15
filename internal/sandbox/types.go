package sandbox

import (
	"errors"
	"fmt"
	"runtime"
	"strings"
	"time"
)

// Profile 描述 Agent 的文件系统隔离强度。
type Profile string

const (
	ProfileWorkspaceOnly Profile = "workspace_only"
	ProfileStandard      Profile = "standard"
	ProfileFullAccess    Profile = "full_access"
)

// NetworkMode 描述 Agent 是否允许本地 Tool/Connector 发起网络连接。
type NetworkMode string

const (
	NetworkNone   NetworkMode = "none"
	NetworkPublic NetworkMode = "public"
	NetworkAll    NetworkMode = "all"
)

// NativeMode 描述本地子进程是否要求 OS 原生 Sandbox。
type NativeMode string

const (
	NativePreferred NativeMode = "preferred"
	NativeRequired  NativeMode = "required"
	NativeOff       NativeMode = "off"
)

// AgentPolicy 是持久化在 Agent Profile 中的 Sandbox 覆盖配置。
// Standard Profile 已允许读取用户 Home 下的普通文件，因此 Agent 不再维护额外只读目录。
// AdditionalWritePaths 仅用于用户显式授予 Workspace 外的创建/修改能力；Runtime 内部统一编译成 PathRules。
type AgentPolicy struct {
	Profile Profile `json:"profile,omitempty"`

	AdditionalWritePaths []string `json:"additional_write_paths,omitempty"`

	NetworkMode NetworkMode `json:"network_mode,omitempty"`
	NativeMode  NativeMode  `json:"native_mode,omitempty"`
}

// Config 是应用级 Sandbox 默认配置。
type Config struct {
	DefaultProfile     Profile
	DefaultNetworkMode NetworkMode
	DefaultNativeMode  NativeMode

	CommandGracePeriod time.Duration
}

// Capability 表示当前平台原生隔离能力探测结果。
type Capability struct {
	Platform  string `json:"platform"`
	Backend   string `json:"backend"`
	Available bool   `json:"available"`
	Reason    string `json:"reason,omitempty"`

	Filesystem  bool `json:"filesystem"`
	ProcessTree bool `json:"processTree"`
	Network     bool `json:"network"`
}

// EffectivePolicy 是一次 Runtime Turn 冻结后的安全边界。
// PathRules 是文件系统权限的唯一事实来源；OS Sandbox 需要的 RO/RW roots 必须通过
// NativeFilesystem() 临时编译，禁止再保存第二份目录权限状态。
type EffectivePolicy struct {
	Profile     Profile
	NetworkMode NetworkMode
	NativeMode  NativeMode

	WorkspaceRoot string
	PathRules     []PathRule

	Capability Capability
}

func NormalizeProfile(value Profile, fallback Profile) Profile {
	value = Profile(strings.ToLower(strings.TrimSpace(string(value))))
	if value == "" {
		value = fallback
	}
	return value
}

func NormalizeNetworkMode(value NetworkMode, fallback NetworkMode) NetworkMode {
	value = NetworkMode(strings.ToLower(strings.TrimSpace(string(value))))
	if value == "" {
		value = fallback
	}
	return value
}

func NormalizeNativeMode(value NativeMode, fallback NativeMode) NativeMode {
	value = NativeMode(strings.ToLower(strings.TrimSpace(string(value))))
	if value == "" {
		value = fallback
	}
	return value
}

func (p AgentPolicy) Validate() error {
	profile := NormalizeProfile(p.Profile, ProfileWorkspaceOnly)
	switch profile {
	case ProfileWorkspaceOnly, ProfileStandard, ProfileFullAccess:
	default:
		return fmt.Errorf("不支持的 Sandbox Profile: %q", profile)
	}

	network := NormalizeNetworkMode(p.NetworkMode, NetworkPublic)
	switch network {
	case NetworkNone, NetworkPublic, NetworkAll:
	default:
		return fmt.Errorf("不支持的 Sandbox NetworkMode: %q", network)
	}

	if p.NativeMode != "" {
		native := NormalizeNativeMode(p.NativeMode, NativePreferred)
		switch native {
		case NativePreferred, NativeRequired, NativeOff:
		default:
			return fmt.Errorf("不支持的 Sandbox NativeMode: %q", native)
		}
	}
	return nil
}

func (c Config) Validate() error {
	if c.CommandGracePeriod <= 0 {
		return errors.New("Sandbox CommandGracePeriod 必须大于 0")
	}
	if err := (AgentPolicy{Profile: c.DefaultProfile, NetworkMode: c.DefaultNetworkMode, NativeMode: c.DefaultNativeMode}).Validate(); err != nil {
		return err
	}
	return nil
}

func (p EffectivePolicy) Validate() error {
	if strings.TrimSpace(p.WorkspaceRoot) == "" {
		return errors.New("Sandbox WorkspaceRoot 不能为空")
	}
	if err := (AgentPolicy{Profile: p.Profile, NetworkMode: p.NetworkMode, NativeMode: p.NativeMode}).Validate(); err != nil {
		return err
	}
	if p.Profile == ProfileFullAccess {
		return nil
	}
	if len(p.PathRules) == 0 {
		return errors.New("Sandbox PathRules 不能为空")
	}
	workspaceFull := false
	for _, rule := range p.PathRules {
		if err := rule.Validate(); err != nil {
			return err
		}
		if pathEqual(rule.Root, p.WorkspaceRoot) && rule.Access == AccessFull {
			workspaceFull = true
		}
	}
	if !workspaceFull {
		return errors.New("Sandbox WorkspaceRoot 必须存在 FULL PathRule")
	}
	return nil
}

func CurrentPlatform() string { return runtime.GOOS }

// AllowsNetwork 返回当前 Agent 是否允许网络型能力存在。
func (p EffectivePolicy) AllowsNetwork() bool { return p.NetworkMode != NetworkNone }

// WorkspaceOnlyPolicy 构建一个安全的 Workspace-only Policy，主要用于不依赖 Manager 的
// 纯 Tool 测试/局部作用域。真实 Runtime 仍应始终使用 Manager.Resolve()。
func WorkspaceOnlyPolicy(root string) EffectivePolicy {
	return EffectivePolicy{
		Profile:       ProfileWorkspaceOnly,
		NetworkMode:   NetworkPublic,
		NativeMode:    NativeOff,
		WorkspaceRoot: root,
		PathRules: []PathRule{{
			Root:   root,
			Access: AccessFull,
			Source: RuleSourceWorkspace,
		}},
	}
}
