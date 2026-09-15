package sandbox

import (
	"errors"
	"fmt"
)

// validateProcessIsolationPolicy 定义了 Humbert 何时被允许启动任意
// 本地子进程。PathGuard 仅能约束由 Humbert 拥有的文件处理工具；一旦
// 第三方运行时（如 Python、Node、stdio MCP 或 Skill 脚本）启动，受限的 Profile
// 绝不能静默降级为宿主用户不受限的文件系统权限。
//
// NativeOff 是唯一的显式“逃生”途径：即用户有意禁用了操作系统
// 进程隔离功能。即便如此，NetworkNone 策略仍保持“故障安全关闭”（fail-closed）状态，
// 因为无法阻止不受限的子进程打开网络套接字（socket）。
func validateProcessIsolationPolicy(policy EffectivePolicy) error {
	if policy.NativeMode == NativeOff {
		if policy.NetworkMode == NetworkNone {
			return errors.New("Sandbox 已禁止网络，但 Native Sandbox=off 无法约束任意第三方子进程网络")
		}
		return nil
	}

	restrictedFilesystem := policy.Profile != ProfileFullAccess
	if !policy.Capability.Available {
		if restrictedFilesystem || policy.NativeMode == NativeRequired || policy.NetworkMode == NetworkNone {
			return fmt.Errorf("当前平台原生 Sandbox 不可用，受限进程不会静默降级运行: %s", policy.Capability.Reason)
		}
		// FullAccess + Preferred 可以退回宿主用户权限，因为该 Profile 本身已经显式
		// 放弃文件系统边界；NativeUsed 会由具体 backend 返回 false 供审计。
		return nil
	}

	if restrictedFilesystem && !policy.Capability.Filesystem {
		return fmt.Errorf(
			"当前平台无法对本地子进程强制文件系统边界，%s/%s Profile 已拒绝启动，避免 Python/Node 绕过 PathGuard；如明确接受宿主用户权限，请在高级设置将 Native Sandbox 设为 off",
			policy.Capability.Platform,
			policy.Profile,
		)
	}

	if policy.NetworkMode == NetworkNone && !policy.Capability.Network {
		return fmt.Errorf("当前平台无法可靠禁止子进程网络访问: %s", policy.Capability.Reason)
	}
	return nil
}
