package tools

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/sda1-hacker/humbert-agent/internal/commandenv"
	"github.com/sda1-hacker/humbert-agent/internal/permission"
	"github.com/sda1-hacker/humbert-agent/internal/sandbox"
	"github.com/sda1-hacker/humbert-agent/internal/skills"
)

func buildCapabilityIdentity(descriptor Descriptor, scope Scope, arguments string) (permission.CapabilityIdentity, error) {
	policy := scope.SandboxPolicy()
	sandboxFingerprint, err := policy.Fingerprint()
	if err != nil {
		return permission.CapabilityIdentity{}, err
	}

	identity := permission.CapabilityIdentity{
		Version:            permission.CapabilityIdentityVersion,
		Kind:               permission.CapabilityBuiltin,
		Tool:               descriptor.Name,
		Risk:               descriptor.Risk,
		SandboxFingerprint: sandboxFingerprint,
	}

	if origin := descriptor.MCPOrigin; origin != nil {
		identity.Kind = permission.CapabilityMCP
		identity.MCPServerID = origin.ServerID
		identity.MCPServerFingerprint = origin.ServerFingerprint
		identity.MCPTool = origin.RawToolName
		identity = identity.Normalize()
		if err := identity.Validate(); err != nil {
			return permission.CapabilityIdentity{}, err
		}
		return identity, nil
	}

	switch descriptor.Name {
	case "run_command":
		var input struct {
			Command          string   `json:"command"`
			Args             []string `json:"args"`
			WorkingDirectory string   `json:"working_directory"`
			TimeoutSeconds   int      `json:"timeout_seconds"`
		}
		if err := json.Unmarshal([]byte(arguments), &input); err != nil {
			return permission.CapabilityIdentity{}, fmt.Errorf("解析 run_command Capability Identity 失败: %w", err)
		}
		workingDirectory := strings.TrimSpace(input.WorkingDirectory)
		if workingDirectory == "" {
			workingDirectory = "."
		}
		decision, err := policy.CheckPath(workingDirectory, sandbox.OpList)
		if err != nil {
			return permission.CapabilityIdentity{}, fmt.Errorf("解析 run_command 工作目录身份失败: %w", err)
		}
		executable, err := commandenv.Resolve(input.Command, decision.CanonicalPath)
		if err != nil {
			return permission.CapabilityIdentity{}, fmt.Errorf("解析 run_command 可执行文件身份失败: %w", err)
		}
		identity.Kind = permission.CapabilityCommand
		identity.Command = commandenv.Name(input.Command)
		identity.Executable = executable
		invocation, err := json.Marshal(struct {
			Args             []string `json:"args"`
			WorkingDirectory string   `json:"working_directory"`
			TimeoutSeconds   int      `json:"timeout_seconds"`
		}{append([]string{}, input.Args...), decision.CanonicalPath, input.TimeoutSeconds})
		if err != nil {
			return permission.CapabilityIdentity{}, fmt.Errorf("构建 run_command 参数身份失败: %w", err)
		}
		identity.InvocationFingerprint = fmt.Sprintf("cmd1:%x", sha256.Sum256(invocation))

	case "run_skill_script":
		var input struct {
			Skill  string `json:"skill"`
			Script string `json:"script"`
		}
		if err := json.Unmarshal([]byte(arguments), &input); err != nil {
			return permission.CapabilityIdentity{}, fmt.Errorf("解析 run_skill_script Capability Identity 失败: %w", err)
		}
		skillName := strings.TrimSpace(input.Skill)
		if skillName == "" || strings.TrimSpace(input.Script) == "" {
			return permission.CapabilityIdentity{}, errors.New("run_skill_script Capability Identity 缺少 Skill 或 Script")
		}
		script, err := skills.NormalizeScriptPath(input.Script)
		if err != nil {
			return permission.CapabilityIdentity{}, fmt.Errorf("Skill %q 脚本路径无效: %w", skillName, err)
		}
		skillIdentity := strings.TrimSpace(scope.SkillIdentities[skillName])
		if skillIdentity == "" {
			return permission.CapabilityIdentity{}, fmt.Errorf("Skill %q 不在当前 Turn 的冻结身份中", skillName)
		}
		command := commandenv.Name(scope.SkillScriptCommands[skillName][script])
		if command == "" {
			return permission.CapabilityIdentity{}, fmt.Errorf("Skill %q 脚本 %q 没有冻结的运行时命令身份", skillName, script)
		}
		if err := commandenv.Validate(command); err != nil {
			return permission.CapabilityIdentity{}, fmt.Errorf("Skill %q 脚本运行时命令无效: %w", skillName, err)
		}
		executable, err := commandenv.Resolve(command, policy.WorkspaceRoot)
		if err != nil {
			return permission.CapabilityIdentity{}, fmt.Errorf("解析 Skill %q 脚本解释器身份失败: %w", skillName, err)
		}
		identity.Kind = permission.CapabilitySkillScript
		identity.SkillName = skillName
		identity.SkillIdentity = skillIdentity
		identity.Script = script
		identity.Command = command
		identity.Executable = executable
	}

	identity = identity.Normalize()
	if err := identity.Validate(); err != nil {
		return permission.CapabilityIdentity{}, err
	}
	return identity, nil
}
