package skills

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

const agentSkillsDescriptionMaxLength = 1024

func evaluateSpecCompatibility(name string, description string) (SpecStatus, string) {
	issues := make([]string, 0, 2)
	if err := validateStandardSkillName(name); err != nil {
		issues = append(issues, err.Error())
	}
	if len(description) > agentSkillsDescriptionMaxLength {
		issues = append(issues, fmt.Sprintf("description 超过 Agent Skills 标准的 %d 字符上限", agentSkillsDescriptionMaxLength))
	}
	if len(issues) == 0 {
		return SpecStatusStandard, ""
	}
	return SpecStatusLegacy, strings.Join(issues, "；")
}

func validateStandardSkillName(name string) error {
	if name == "" {
		return fmt.Errorf("name 不能为空")
	}
	if len(name) > maxSkillNameLength {
		return fmt.Errorf("name 长度不能超过 %d", maxSkillNameLength)
	}
	if strings.HasPrefix(name, "-") || strings.HasSuffix(name, "-") {
		return fmt.Errorf("name 不能以连字符开头或结尾")
	}
	if strings.Contains(name, "--") {
		return fmt.Errorf("name 不能包含连续连字符")
	}
	for _, char := range name {
		if (char >= 'a' && char <= 'z') || (char >= '0' && char <= '9') || char == '-' {
			continue
		}
		return fmt.Errorf("name 只能包含小写字母、数字和连字符")
	}
	return nil
}

func evaluateRuntimeCompatibility(
	contextMode string,
	agent string,
	model string,
	compatibility string,
	allowedTools string,
	root string,
	files []FileInfo,
) (RuntimeStatus, string, []Diagnostic, []ScriptRuntime) {
	diagnostics := make([]Diagnostic, 0, 8)
	status := RuntimeStatusReady

	markUnsupported := func(code string, message string) {
		status = RuntimeStatusUnsupported
		diagnostics = append(diagnostics, Diagnostic{Code: code, Level: DiagnosticError, Message: message})
	}
	markNeedsSetup := func(code string, message string) {
		if status != RuntimeStatusUnsupported {
			status = RuntimeStatusNeedsSetup
		}
		diagnostics = append(diagnostics, Diagnostic{Code: code, Level: DiagnosticWarning, Message: message})
	}

	if contextMode != "" {
		markUnsupported(
			"eino_context",
			fmt.Sprintf("当前 Humbert Runtime 尚未启用 Eino Skill context=%s；Package 可以安装，但暂不能为 Agent 启用", contextMode),
		)
	}
	if strings.TrimSpace(agent) != "" {
		markUnsupported(
			"eino_agent",
			fmt.Sprintf("当前 Humbert Runtime 尚未接入 Eino Skill agent=%s", strings.TrimSpace(agent)),
		)
	}
	if strings.TrimSpace(model) != "" {
		markUnsupported(
			"eino_model",
			fmt.Sprintf("当前 Humbert Runtime 不允许 Skill 覆盖 Agent 默认模型（model=%s）", strings.TrimSpace(model)),
		)
	}

	if compatibility = strings.TrimSpace(compatibility); compatibility != "" {
		diagnostics = append(diagnostics, Diagnostic{
			Code:    "compatibility_note",
			Level:   DiagnosticInfo,
			Message: "Skill 声明的环境要求：" + compatibility,
		})
	}
	if allowedTools = strings.TrimSpace(allowedTools); allowedTools != "" {
		diagnostics = append(diagnostics, Diagnostic{
			Code:  "allowed_tools",
			Level: DiagnosticInfo,
			Message: "Skill 声明 allowed-tools=" + allowedTools +
				"。Humbert 只把它作为兼容提示，不会用它绕过 Agent Tool Selection、Permission 或 Approval。",
		})
	}

	scripts := inspectScriptRuntimes(root, files)
	for _, script := range scripts {
		switch {
		case !script.Supported:
			markNeedsSetup(
				"script_runtime_unsupported",
				fmt.Sprintf("脚本 %s 的运行时尚未被 Humbert 识别；仍可读取脚本内容，但不能通过 run_skill_script 执行", script.Path),
			)
		case !script.Available:
			markNeedsSetup(
				"script_runtime_missing",
				fmt.Sprintf("脚本 %s 需要 %s，但当前 Humbert 进程 PATH 中未找到该程序", script.Path, script.Command),
			)
		}
	}

	message := ""
	if status != RuntimeStatusReady {
		parts := make([]string, 0, len(diagnostics))
		for _, item := range diagnostics {
			if item.Level == DiagnosticError || item.Level == DiagnosticWarning {
				parts = append(parts, item.Message)
			}
		}
		message = strings.Join(parts, "；")
	}
	return status, message, diagnostics, scripts
}

func inspectScriptRuntimes(root string, files []FileInfo) []ScriptRuntime {
	result := make([]ScriptRuntime, 0)
	for _, file := range files {
		if !strings.HasPrefix(file.Path, "scripts/") || file.Path == "scripts/" {
			continue
		}
		language, candidates := scriptRuntimeCandidates(file.Path)
		if len(candidates) == 0 && file.Text {
			if command := scriptShebangCommand(root, file.Path); command != "" {
				language, candidates = runtimeFromShebang(command)
			}
		}

		// scripts/ 目录里允许存在 package.json、lockfile、模板、数据文件和其他运行时资源。
		// 只有“扩展名明确表示脚本”或“文本文件带 shebang”的文件才进入执行兼容性诊断。
		// 未识别的普通资源文件应被忽略，而不是把整个 Skill 标成“需要配置”。
		if len(candidates) == 0 && language == "" {
			continue
		}

		item := ScriptRuntime{Path: file.Path, Language: language}
		if len(candidates) == 0 {
			item.Message = "该脚本类型暂未配置可安全直连执行的运行时"
			result = append(result, item)
			continue
		}
		item.Supported = true
		item.Command = candidates[0]
		for _, command := range candidates {
			if _, err := exec.LookPath(command); err == nil {
				item.Command = command
				item.Available = true
				break
			}
		}
		if !item.Available {
			item.Message = fmt.Sprintf("未找到 %s", strings.Join(candidates, " / "))
		}
		result = append(result, item)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Path < result[j].Path })
	return result
}

func scriptRuntimeCandidates(relative string) (string, []string) {
	ext := strings.ToLower(path.Ext(relative))
	switch ext {
	case ".py":
		return "python", []string{"python3", "python"}
	case ".sh", ".bash":
		return "shell", []string{"bash", "sh"}
	case ".js", ".mjs", ".cjs":
		return "javascript", []string{"node"}
	case ".rb":
		return "ruby", []string{"ruby"}
	case ".php":
		return "php", []string{"php"}
	case ".pl":
		return "perl", []string{"perl"}
	case ".ps1":
		if runtime.GOOS == "windows" {
			return "powershell", []string{"pwsh", "powershell"}
		}
		return "powershell", []string{"pwsh"}
	case ".ts", ".mts", ".cts":
		// TypeScript 的执行器差异较大（tsx/bun/deno/ts-node），不能根据扩展名擅自选择。
		return "typescript", nil
	default:
		return "", nil
	}
}

func scriptShebangCommand(root string, relative string) string {
	if strings.TrimSpace(root) == "" {
		return ""
	}
	full := filepath.Join(root, filepath.FromSlash(relative))
	file, err := os.Open(full)
	if err != nil {
		return ""
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	if !scanner.Scan() {
		return ""
	}
	line := strings.TrimSpace(scanner.Text())
	if !strings.HasPrefix(line, "#!") {
		return ""
	}
	fields := strings.Fields(strings.TrimSpace(strings.TrimPrefix(line, "#!")))
	if len(fields) == 0 {
		return ""
	}
	command := path.Base(strings.ReplaceAll(fields[0], "\\", "/"))
	if command == "env" && len(fields) > 1 {
		command = fields[1]
	}
	return strings.TrimSpace(command)
}

func runtimeFromShebang(command string) (string, []string) {
	command = strings.ToLower(strings.TrimSpace(command))
	switch command {
	case "python", "python3":
		return "python", []string{command}
	case "bash", "sh", "zsh":
		return "shell", []string{command}
	case "node":
		return "javascript", []string{"node"}
	case "ruby":
		return "ruby", []string{"ruby"}
	case "php":
		return "php", []string{"php"}
	case "perl":
		return "perl", []string{"perl"}
	case "pwsh", "powershell":
		return "powershell", []string{command}
	default:
		return "", nil
	}
}
