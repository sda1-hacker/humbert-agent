package permission

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"

	"github.com/sda1-hacker/humbert-agent/internal/logging"
)

const maxApprovalFieldLength = 512

// BuildPresentation 将 raw Tool Arguments 转成可以展示在 Approval Card 中的最小必要信息。
//
// write_file/edit_file 的内容正文不会进入前端；run_command 的 argv 会经过统一敏感信息
// 脱敏；未知 Tool 默认只展示“参数已隐藏”。这样未来 MCP Tool 即使参数中出现 Token，
// 也不会因为尚未实现专用 Presenter 而直接泄露到 UI。已知 Builtin Tool 的参数必须是
// 合法 JSON：解析失败说明模型生成了不符合 Tool Schema 的调用，此时返回带上下文的错误，
// 绝不能以空字段审批后继续执行。
func BuildPresentation(request Request) (Presentation, error) {
	if request.Identity.Kind == CapabilityMCP {
		serverName := strings.TrimSpace(request.MCPServerName)
		if serverName == "" {
			serverName = "MCP Server"
		}
		rawToolName := strings.TrimSpace(request.MCPRawToolName)
		fields := []PresentationField{
			{Label: "MCP Server", Value: safeField(serverName)},
			{Label: "Tool", Value: safeField(rawToolName)},
			{Label: "模型名称", Value: safeField(request.ToolName)},
		}
		fields = append(fields, buildSafeArgumentPreview(request.Arguments)...)
		return Presentation{
			Title:       "请求调用 MCP Tool",
			Description: "该能力由外部 MCP Server 提供。参数预览已自动隐藏常见 Secret 并限制长度；长期授权会绑定当前 Server、安全沙盒和 Tool 风险身份。",
			Fields:      fields,
		}, nil
	}

	switch request.ToolName {
	case "browser":
		var input struct {
			Action   string `json:"action"`
			URL      string `json:"url"`
			Selector string `json:"selector"`
			Text     string `json:"text"`
		}
		if err := json.Unmarshal([]byte(request.Arguments), &input); err != nil {
			return Presentation{}, fmt.Errorf("解析 browser 审批参数失败: %w", err)
		}
		fields := []PresentationField{{Label: "操作", Value: safeField(input.Action)}}
		if input.URL != "" {
			parsed, err := url.Parse(input.URL)
			if err != nil {
				return Presentation{}, fmt.Errorf("解析 browser URL 失败: %w", err)
			}
			fields = append(fields, PresentationField{Label: "站点", Value: safeField(parsed.Scheme + "://" + parsed.Host)})
		}
		if input.Selector != "" {
			fields = append(fields, PresentationField{Label: "元素", Value: safeField(input.Selector)})
		}
		if input.Action == "type" {
			fields = append(fields, PresentationField{Label: "输入内容", Value: fmt.Sprintf("%d 字符（内容已隐藏）", len([]rune(input.Text)))})
		}
		return Presentation{Title: "请求操作浏览器", Description: "网页内容来自外部站点。批准后可在隔离的临时浏览器中执行本次操作；输入内容不会显示在审批卡片中。", Fields: fields}, nil
	case "schedule_task":
		var input struct {
			Name            string `json:"name"`
			Prompt          string `json:"prompt"`
			Execution       string `json:"execution"`
			ScheduleType    string `json:"schedule_type"`
			TimeZone        string `json:"time_zone"`
			RunAt           string `json:"run_at"`
			TimeOfDay       string `json:"time_of_day"`
			Weekdays        []int  `json:"weekdays"`
			IntervalMinutes int    `json:"interval_minutes"`
		}
		if err := json.Unmarshal([]byte(request.Arguments), &input); err != nil {
			return Presentation{}, fmt.Errorf("解析 schedule_task 审批参数失败: %w", err)
		}
		if strings.TrimSpace(input.Name) == "" || strings.TrimSpace(input.Prompt) == "" || len([]rune(strings.TrimSpace(input.Prompt))) > 500 {
			return Presentation{}, errors.New("任务名称或执行内容无效，执行内容最多 500 个字符")
		}
		when := strings.TrimSpace(input.ScheduleType)
		switch when {
		case "once":
			when = input.RunAt
		case "daily":
			when = "每天 " + input.TimeOfDay
		case "weekly":
			when = fmt.Sprintf("每周 %v 的 %s", input.Weekdays, input.TimeOfDay)
		case "interval":
			when = fmt.Sprintf("每 %d 分钟", input.IntervalMinutes)
		}
		execution := "由 Agent 执行"
		if input.Execution == "notification" {
			execution = "仅发送提醒"
		}
		zone := strings.TrimSpace(input.TimeZone)
		if zone == "" {
			zone = "本机时区"
		}
		return Presentation{
			Title:       "确认安排任务",
			Description: "确认后会创建并启用该计划。每次安排都需要单独确认；应用退出期间不会运行。",
			Fields: []PresentationField{
				{Label: "名称", Value: safeField(input.Name)},
				{Label: "执行方式", Value: execution},
				{Label: "时间", Value: safeField(when)},
				{Label: "时区", Value: safeField(zone)},
				{Label: "内容", Value: safeField(input.Prompt)},
			},
		}, nil
	case "write_file":
		var input struct {
			Path      string `json:"path"`
			Overwrite bool   `json:"overwrite"`
		}
		if err := json.Unmarshal([]byte(request.Arguments), &input); err != nil {
			return Presentation{}, fmt.Errorf("解析 write_file 审批参数失败: %w", err)
		}
		return Presentation{
			Title:       "请求写入文件",
			Description: "该操作会修改当前 Agent Workspace 中的文件。长期授权只在当前安全沙盒身份不变时复用。",
			Fields: []PresentationField{
				{Label: "文件", Value: safeField(input.Path)},
				{Label: "模式", Value: map[bool]string{true: "覆盖已有文件", false: "仅创建新文件"}[input.Overwrite]},
			},
		}, nil

	case "edit_file":
		var input struct {
			Path       string `json:"path"`
			OldText    string `json:"old_text"`
			NewText    string `json:"new_text"`
			ReplaceAll bool   `json:"replace_all"`
		}
		if err := json.Unmarshal([]byte(request.Arguments), &input); err != nil {
			return Presentation{}, fmt.Errorf("解析 edit_file 审批参数失败: %w", err)
		}
		return Presentation{
			Title:       "请求编辑文件",
			Description: "该操作会修改当前 Agent Workspace 中的已有文件。文本正文不会显示在审批卡片中。",
			Fields: []PresentationField{
				{Label: "文件", Value: safeField(input.Path)},
				{Label: "替换范围", Value: map[bool]string{true: "全部匹配", false: "首个匹配"}[input.ReplaceAll]},
				{Label: "原文本长度", Value: fmt.Sprintf("%d 字符", len([]rune(input.OldText)))},
				{Label: "新文本长度", Value: fmt.Sprintf("%d 字符", len([]rune(input.NewText)))},
			},
		}, nil

	case "delete_file":
		var input struct {
			Path string `json:"path"`
		}
		if err := json.Unmarshal([]byte(request.Arguments), &input); err != nil {
			return Presentation{}, fmt.Errorf("解析 delete_file 审批参数失败: %w", err)
		}
		return Presentation{
			Title:       "请求删除文件",
			Description: "该操作会删除当前 Agent Workspace 中的文件。",
			Fields:      []PresentationField{{Label: "文件", Value: safeField(input.Path)}},
		}, nil

	case "install_skill":
		var input struct {
			SourceURL string `json:"source_url"`
			SkillPath string `json:"skill_path"`
			Enable    bool   `json:"enable"`
		}
		if err := json.Unmarshal([]byte(request.Arguments), &input); err != nil {
			return Presentation{}, fmt.Errorf("解析 install_skill 审批参数失败: %w", err)
		}
		displayURL := input.SourceURL
		if parsed, err := url.Parse(input.SourceURL); err == nil && parsed != nil {
			if parsed.User != nil {
				parsed.User = url.User("***")
			}
			parsed.RawQuery = ""
			parsed.Fragment = ""
			displayURL = parsed.String()
		}
		skillPath := strings.TrimSpace(input.SkillPath)
		if skillPath == "" {
			skillPath = "（自动识别）"
		}
		enableText := "安装后不自动启用"
		if input.Enable {
			enableText = "安装后为当前 Agent 启用"
		}
		return Presentation{
			Title:       "请求安装远程 Skill",
			Description: "该操作会从公开 HTTPS 地址下载不可信内容并写入 Humbert Skills 目录；脚本不会在安装阶段执行。",
			Fields: []PresentationField{
				{Label: "来源", Value: safeField(displayURL)},
				{Label: "Skill 路径", Value: safeField(skillPath)},
				{Label: "启用方式", Value: enableText},
			},
		}, nil

	case "run_command":
		var input struct {
			Command          string   `json:"command"`
			Args             []string `json:"args"`
			WorkingDirectory string   `json:"working_directory"`
			TimeoutSeconds   int      `json:"timeout_seconds"`
		}
		if err := json.Unmarshal([]byte(request.Arguments), &input); err != nil {
			return Presentation{}, fmt.Errorf("解析 run_command 审批参数失败: %w", err)
		}

		command := request.Identity.Command
		args := make([]string, 0, len(input.Args))
		for _, value := range input.Args {
			args = append(args, safeField(value))
		}
		argumentText := strings.Join(args, " ")
		if argumentText == "" {
			argumentText = "（无参数）"
		}
		workingDirectory := strings.TrimSpace(input.WorkingDirectory)
		if workingDirectory == "" {
			workingDirectory = "."
		}

		return Presentation{
			Title:       "请求执行本地程序",
			Description: "该操作会在只读 Agent Workspace 中启动本地程序，不经过 Shell。程序及子进程只能写入临时目录；会话或长期允许只匹配相同程序、参数、工作目录和安全环境。",
			Fields: []PresentationField{
				{Label: "程序", Value: safeField(command)},
				{Label: "执行文件", Value: safeField(request.Identity.Executable)},
				{Label: "参数", Value: safeField(argumentText)},
				{Label: "工作目录", Value: safeField(workingDirectory)},
			},
		}, nil

	case "run_skill_script":
		var input struct {
			Skill  string `json:"skill"`
			Script string `json:"script"`
		}
		if err := json.Unmarshal([]byte(request.Arguments), &input); err != nil {
			return Presentation{}, fmt.Errorf("解析 run_skill_script 审批参数失败: %w", err)
		}
		script := strings.TrimSpace(request.Identity.Script)
		if script == "" {
			script = input.Script
		}
		return Presentation{
			Title:       "请求执行 Skill 脚本",
			Description: "该操作会执行当前 Turn 已冻结的 Skill 脚本。长期授权会绑定 Skill 内容身份、规范化脚本路径与当前安全沙盒。",
			Fields: []PresentationField{
				{Label: "Skill", Value: safeField(input.Skill)},
				{Label: "脚本", Value: safeField(script)},
			},
		}, nil

	default:
		return Presentation{
			Title:       "请求执行工具",
			Description: "该工具需要用户确认。为避免外部工具参数泄露敏感信息，当前版本默认隐藏原始参数。长期授权只在当前安全能力身份不变时复用。",
			Fields: []PresentationField{
				{Label: "工具", Value: safeField(request.ToolName)},
			},
		}, nil
	}
}

const (
	maxApprovalArgumentFields = 10
	maxApprovalArrayItems     = 8
	maxApprovalPreviewDepth   = 3
)

func buildSafeArgumentPreview(arguments string) []PresentationField {
	arguments = strings.TrimSpace(arguments)
	if arguments == "" {
		return nil
	}
	var decoded any
	if err := json.Unmarshal([]byte(arguments), &decoded); err != nil {
		return []PresentationField{{Label: "参数", Value: "无法安全解析，已隐藏"}}
	}
	object, ok := decoded.(map[string]any)
	if !ok {
		return []PresentationField{{Label: "参数", Value: safePreviewValue(decoded, "", 0)}}
	}
	keys := make([]string, 0, len(object))
	for key := range object {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	if len(keys) > maxApprovalArgumentFields {
		keys = keys[:maxApprovalArgumentFields]
	}
	fields := make([]PresentationField, 0, len(keys)+1)
	for _, key := range keys {
		fields = append(fields, PresentationField{
			Label: safeField(key),
			Value: safePreviewValue(object[key], key, 0),
		})
	}
	if len(object) > len(keys) {
		fields = append(fields, PresentationField{Label: "其他参数", Value: fmt.Sprintf("另有 %d 项已折叠", len(object)-len(keys))})
	}
	return fields
}

func safePreviewValue(value any, key string, depth int) string {
	if sensitiveArgumentKey(key) {
		return "••••••"
	}
	if depth >= maxApprovalPreviewDepth {
		return "…"
	}
	switch typed := value.(type) {
	case nil:
		return "null"
	case string:
		return safeField(typed)
	case bool:
		if typed {
			return "true"
		}
		return "false"
	case float64:
		return fmt.Sprintf("%v", typed)
	case []any:
		limit := len(typed)
		if limit > maxApprovalArrayItems {
			limit = maxApprovalArrayItems
		}
		parts := make([]string, 0, limit+1)
		for index := 0; index < limit; index++ {
			parts = append(parts, safePreviewValue(typed[index], "", depth+1))
		}
		if len(typed) > limit {
			parts = append(parts, fmt.Sprintf("… +%d", len(typed)-limit))
		}
		return safeField("[" + strings.Join(parts, ", ") + "]")
	case map[string]any:
		keys := make([]string, 0, len(typed))
		for childKey := range typed {
			keys = append(keys, childKey)
		}
		sort.Strings(keys)
		if len(keys) > maxApprovalArrayItems {
			keys = keys[:maxApprovalArrayItems]
		}
		parts := make([]string, 0, len(keys)+1)
		for _, childKey := range keys {
			parts = append(parts, childKey+": "+safePreviewValue(typed[childKey], childKey, depth+1))
		}
		if len(typed) > len(keys) {
			parts = append(parts, fmt.Sprintf("… +%d", len(typed)-len(keys)))
		}
		return safeField("{" + strings.Join(parts, ", ") + "}")
	default:
		encoded, err := json.Marshal(typed)
		if err != nil {
			return "（已隐藏）"
		}
		return safeField(string(encoded))
	}
}

func sensitiveArgumentKey(key string) bool {
	normalized := strings.ToLower(strings.TrimSpace(key))
	normalized = strings.NewReplacer("-", "", "_", "", ".", "", " ", "").Replace(normalized)
	switch normalized {
	case "token", "accesstoken", "refreshtoken", "idtoken", "password", "passwd", "secret",
		"clientsecret", "authorization", "proxyauthorization", "cookie", "setcookie", "apikey",
		"credential", "credentials", "privatekey", "secretkey":
		return true
	default:
		return strings.HasSuffix(normalized, "token") || strings.HasSuffix(normalized, "password") ||
			strings.HasSuffix(normalized, "secret") || strings.HasSuffix(normalized, "apikey")
	}
}

func safeField(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "（未提供）"
	}
	return logging.RedactText(value, maxApprovalFieldLength)
}
