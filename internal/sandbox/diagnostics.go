package sandbox

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Diagnose 在临时目录中验证 PathGuard 与原生进程的文件隔离，退出时清理所有探针。
// 敏感路径由应用装配层传入，避免领域层依赖配置文件或猜测自定义 Home 的布局。
// 此入口有实际文件和进程 IO，仅在用户显式自检时调用；超时由调用方的 Context 控制。
func (m *Manager) Diagnose(ctx context.Context, protectedDirectory, protectedFile string) (Diagnostics, error) {
	if m == nil || ctx == nil {
		return Diagnostics{}, errors.New("Sandbox 自检需要 Manager 和非空 Context")
	}
	if err := ctx.Err(); err != nil {
		return Diagnostics{}, err
	}
	root, err := os.MkdirTemp("", "humbert-sandbox-diagnostics-*")
	if err != nil {
		return Diagnostics{}, fmt.Errorf("创建 Sandbox 自检目录失败: %w", err)
	}
	defer os.RemoveAll(root)

	workspaceRoot := filepath.Join(root, "workspace")
	outsideRoot := filepath.Join(root, "outside")
	if err := os.MkdirAll(workspaceRoot, 0o700); err != nil {
		return Diagnostics{}, fmt.Errorf("创建自检 Workspace 失败: %w", err)
	}
	if err := os.MkdirAll(outsideRoot, 0o700); err != nil {
		return Diagnostics{}, fmt.Errorf("创建自检外部目录失败: %w", err)
	}

	policy, err := m.Resolve(ctx, workspaceRoot, AgentPolicy{
		// 使用 Standard 才能验证“普通 Home 可读，但敏感文件仍被硬保护”的真实产品语义。
		Profile: ProfileStandard, NetworkMode: NetworkPublic, NativeMode: NativeRequired,
	})
	if err != nil {
		return Diagnostics{}, fmt.Errorf("构建自检 Sandbox Policy 失败: %w", err)
	}

	checks := make([]DiagnosticCheck, 0, 7)
	appendCheck := func(key, label, status, detail string) {
		checks = append(checks, DiagnosticCheck{Key: key, Label: label, Status: status, Detail: detail})
	}

	insidePath := filepath.Join(workspaceRoot, "inside.txt")
	if decision, checkErr := policy.CheckPath(insidePath, OpCreate); checkErr == nil && decision.Allowed {
		appendCheck("pathguard_workspace", "工作目录写入", "pass", "工作目录内的创建操作已正确允许。")
	} else {
		appendCheck("pathguard_workspace", "工作目录写入", "fail", errorDetail(checkErr, "工作目录内的创建操作被错误拒绝。"))
	}

	outsidePath := filepath.Join(outsideRoot, "outside.txt")
	if _, checkErr := policy.CheckPath(outsidePath, OpCreate); checkErr != nil {
		appendCheck("pathguard_outside", "工作目录外写入阻止", "pass", "工作目录外的创建操作已正确阻止。")
	} else {
		appendCheck("pathguard_outside", "工作目录外写入阻止", "fail", "工作目录外的创建操作被错误允许。")
	}

	protectedProbe := filepath.Join(protectedDirectory, "diagnostic-probe")
	if _, checkErr := policy.CheckPath(protectedProbe, OpRead); checkErr != nil {
		appendCheck("pathguard_protected", "敏感目录保护", "pass", "Humbert 的敏感数据目录已正确阻止访问。")
	} else {
		appendCheck("pathguard_protected", "敏感目录保护", "fail", "Humbert 的敏感数据目录被错误允许读取。")
	}

	protectedFileProbe := protectedFile
	if decision, checkErr := policy.CheckPath(protectedFileProbe, OpRead); checkErr != nil && decision.Source == RuleSourceProtectedFile {
		appendCheck("pathguard_protected_file", "敏感文件保护", "pass", "Humbert 配置文件位于普通 Home 可读范围内，但已被单文件硬保护规则正确阻止。")
	} else if checkErr != nil {
		appendCheck("pathguard_protected_file", "敏感文件保护", "warning", "敏感文件读取被阻止，但未命中预期的单文件规则："+checkErr.Error())
	} else {
		appendCheck("pathguard_protected_file", "敏感文件保护", "fail", "Humbert 配置文件被错误允许读取。")
	}

	linkPath := filepath.Join(workspaceRoot, "escape-link")
	if linkErr := os.Symlink(outsideRoot, linkPath); linkErr != nil {
		appendCheck("pathguard_symlink", "符号链接逃逸保护", "warning", "当前平台无法创建用于自检的符号链接："+linkErr.Error())
	} else if _, checkErr := policy.CheckPath(filepath.Join(linkPath, "escape.txt"), OpCreate); checkErr != nil {
		appendCheck("pathguard_symlink", "符号链接逃逸保护", "pass", "指向工作目录外的符号链接已正确阻止。")
	} else {
		appendCheck("pathguard_symlink", "符号链接逃逸保护", "fail", "符号链接错误地允许写出工作目录。")
	}

	capability := m.Capability()
	if !capability.Available {
		appendCheck("native_filesystem", "本地程序文件隔离", "warning", "当前系统的本地程序隔离不可用："+capability.Reason)
	} else if !capability.Filesystem {
		detail := "当前平台不能可靠限制本地程序的文件系统访问。Humbert 已启用 fail-closed：标准/严格保护下不会静默启动未受文件隔离的 Python、Node、Skill 或 stdio MCP。"
		if strings.TrimSpace(capability.Reason) != "" {
			detail += " " + capability.Reason
		}
		appendCheck("native_filesystem", "本地程序文件隔离", "warning", detail)
	} else {
		touchPath, lookupErr := exec.LookPath("touch")
		if lookupErr != nil {
			appendCheck("native_filesystem", "本地程序文件隔离", "warning", "找不到系统 touch 命令，无法执行文件写入自检。")
		} else {
			touchPath, _ = filepath.Abs(touchPath)
			insideNative := filepath.Join(workspaceRoot, "native-inside.txt")
			var insideStderr bytes.Buffer
			insideResult, runErr := m.Runner().Run(ctx, policy, ProcessSpec{
				Executable: touchPath, Args: []string{insideNative}, Dir: workspaceRoot, Env: []string{},
				Stdout: io.Discard, Stderr: &insideStderr,
			})
			if runErr != nil || insideResult.ExitCode != 0 {
				detail := errorDetail(runErr, "受保护的本地程序无法在工作目录内创建测试文件。")
				if runErr == nil && strings.TrimSpace(insideResult.TerminationDetail) != "" {
					detail += " 进程终态：" + insideResult.TerminationReason + "（" + insideResult.TerminationDetail + "）"
				}
				if text := strings.TrimSpace(insideStderr.String()); text != "" {
					detail += " " + text
				}
				appendCheck("native_filesystem", "本地程序文件隔离", "fail", detail)
			} else {
				outsideNative := filepath.Join(outsideRoot, "native-outside.txt")
				var outsideStderr bytes.Buffer
				outsideResult, outsideErr := m.Runner().Run(ctx, policy, ProcessSpec{
					Executable: touchPath, Args: []string{outsideNative}, Dir: workspaceRoot, Env: []string{},
					Stdout: io.Discard, Stderr: &outsideStderr,
				})
				_, statErr := os.Stat(outsideNative)
				blocked := outsideErr == nil && outsideResult.ExitCode != 0 && errors.Is(statErr, os.ErrNotExist)
				if blocked {
					appendCheck("native_filesystem", "本地程序文件隔离", "pass", "本地程序可以写入工作目录，并且无法写入工作目录外。")
				} else {
					detail := "本地程序写入工作目录外的操作没有被可靠阻止。"
					if outsideErr != nil {
						detail = outsideErr.Error()
					} else if strings.TrimSpace(outsideResult.TerminationDetail) != "" {
						detail += " 进程终态：" + outsideResult.TerminationReason + "（" + outsideResult.TerminationDetail + "）"
					}
					if text := strings.TrimSpace(outsideStderr.String()); text != "" {
						detail += " " + text
					}
					appendCheck("native_filesystem", "本地程序文件隔离", "fail", detail)
				}
			}
		}
	}

	summary := "pass"
	for _, check := range checks {
		if check.Status == "fail" {
			summary = "fail"
			break
		}
		if check.Status == "warning" && summary == "pass" {
			summary = "warning"
		}
	}
	return Diagnostics{Summary: summary, Checks: checks}, nil
}

func errorDetail(err error, fallback string) string {
	if err != nil {
		return err.Error()
	}
	return fallback
}
