# 内置工具参数与结果索引

共提取 **44 个公开结构**。这是当前源码快照，不是单独维护的另一套工具协议。

以下直接提取 internal/tools/builtin 及 internal/websearch 中带 JSON 字段的公开参数/结果结构。JSON tag 才是模型输入/输出字段名，Go 字段名不能直接替代协议名称。具体默认值、审批、路径校验和副作用见第 05/06 章。

六项文件工具的请求 Schema 由 Eino filesystem middleware 提供；Skill 的基础参数由 Eino Skill middleware 提供，项目增加 file 参数。因此没有在此虚构它们的本地 Go Input 声明。MCP/扩展模块 Schema 来自各自目录或 Provider，不属于固定内置输入。

## apply_patch.go

[internal/tools/builtin/apply_patch.go](../../internal/tools/builtin/apply_patch.go)

```go
type PatchChange struct {
	Path    string `json:"path" jsonschema:"description=File to edit. Relative paths are resolved from the workspace."`
	OldText string `json:"old_text" jsonschema:"description=Exact existing text to replace. It must occur exactly once. Use empty old_text only with create=true."`
	NewText string `json:"new_text" jsonschema:"description=Replacement text or full content for create=true."`
	Create  bool   `json:"create,omitempty" jsonschema:"description=Create a new file. old_text must be empty and the target must not exist."`
}

type ApplyPatchInput struct {
	Changes []PatchChange `json:"changes" jsonschema:"description=One or more exact text replacements. All changes are validated before writes begin."`
}

type ApplyPatchOutput struct {
	Files   []string `json:"files"`
	Changes int      `json:"changes"`
}
```

## browser_tool.go

[internal/tools/builtin/browser_tool.go](../../internal/tools/builtin/browser_tool.go)

```go
type BrowserInput struct {
	Action   string `json:"action" jsonschema:"description=One of open snapshot click type scroll press back forward refresh show screenshot close."`
	URL      string `json:"url,omitempty" jsonschema:"description=Public HTTP or HTTPS URL, required for open."`
	Selector string `json:"selector,omitempty" jsonschema:"description=CSS selector, required for click or type."`
	Text     string `json:"text,omitempty" jsonschema:"description=Text to enter when action is type."`
	Key      string `json:"key,omitempty" jsonschema:"description=Key to press, such as Enter, Tab or Escape."`
	ScrollY  int    `json:"scroll_y,omitempty" jsonschema:"description=Vertical scroll pixels for scroll. Positive moves down."`
}

type BrowserOutput struct {
	URL                    string           `json:"url,omitempty"`
	Title                  string           `json:"title,omitempty"`
	Text                   string           `json:"text,omitempty"`
	Links                  []BrowserElement `json:"links,omitempty"`
	Controls               []BrowserElement `json:"controls,omitempty"`
	ScreenshotAttachmentID string           `json:"screenshot_attachment_id,omitempty"`
	ScreenshotName         string           `json:"screenshot_name,omitempty"`
	VisualObservation      string           `json:"visual_observation,omitempty"`
	NeedsHumanVerification bool             `json:"needs_human_verification,omitempty"`
	VerificationMessage    string           `json:"verification_message,omitempty"`
	Closed                 bool             `json:"closed,omitempty"`
}

type BrowserElement struct {
	Selector string `json:"selector"`
	Label    string `json:"label"`
	URL      string `json:"url,omitempty"`
}
```

## collaboration_tools.go

[internal/tools/builtin/collaboration_tools.go](../../internal/tools/builtin/collaboration_tools.go)

```go
type ListAgentsInput struct{}
type ListAgentsOutput struct {
	Agents []collaboration.AgentSummary `json:"agents"`
}

type RunAgentInput struct {
	ChildAgentID string `json:"child_agent_id" jsonschema:"description=The ID of the configured specialist Agent to call."`
	Task         string `json:"task" jsonschema:"description=A self-contained task including all context constraints and expected output the child needs."`
}
```

## context_artifact.go

[internal/tools/builtin/context_artifact.go](../../internal/tools/builtin/context_artifact.go)

```go
type ContextResourceInput struct {
	ResourceType string `json:"resource_type" jsonschema:"description=资源类型：artifact 表示被上下文保护层存档的超大工具结果；attachment 表示较早文本附件的提取正文。"`
	ResourceID   string `json:"resource_id" jsonschema:"description=资源编号。artifact 使用超大工具结果中的 resource_id；attachment 使用历史附件占位中的 attachment ID。"`
	Offset       int    `json:"offset,omitempty" jsonschema:"description=从第几个 Unicode 字符开始读取，默认 0。"`
	Limit        int    `json:"limit,omitempty" jsonschema:"description=最多读取多少字符，默认 8000，最大 16000。"`
}

type ContextResourceOutput struct {
	Status       string `json:"status,omitempty"`
	Message      string `json:"message,omitempty"`
	ResourceType string `json:"resource_type,omitempty"`
	ResourceID   string `json:"resource_id,omitempty"`
	ToolName     string `json:"tool_name,omitempty"`
	Name         string `json:"name,omitempty"`
	MIMEType     string `json:"mime_type,omitempty"`
	Offset       int    `json:"offset,omitempty"`
	End          int    `json:"end,omitempty"`
	TotalChars   int    `json:"total_chars,omitempty"`
	More         bool   `json:"more,omitempty"`
	Content      string `json:"content,omitempty"`
}
```

## context_history.go

[internal/tools/builtin/context_history.go](../../internal/tools/builtin/context_history.go)

```go
type SessionHistoryInput struct {
	Action string `json:"action" jsonschema:"description=操作类型：search 用关键词定位旧历史；read 用 entry_id 读取原始历史。"`

	Query      string `json:"query,omitempty" jsonschema:"description=action=search 时要查找的关键词或短语。"`
	MaxResults int    `json:"max_results,omitempty" jsonschema:"description=action=search 时最多返回多少条匹配，默认 8，最大 20。"`

	EntryID string `json:"entry_id,omitempty" jsonschema:"description=action=read 时要读取的 entry_id，可来自 search 结果或上下文检查点来源。"`
	Before  int    `json:"before,omitempty" jsonschema:"description=action=read 时同时读取目标之前多少条当前分支记录，默认 1，最大 5。"`
	After   int    `json:"after,omitempty" jsonschema:"description=action=read 时同时读取目标之后多少条当前分支记录，默认 2，最大 8。"`
	Offset  int    `json:"offset,omitempty" jsonschema:"description=action=read 时目标 entry 从第几个 Unicode 字符开始读取，默认 0。"`
	Limit   int    `json:"limit,omitempty" jsonschema:"description=action=read 时目标 entry 最多读取多少字符，默认 12000，最大 20000。"`
}

type SessionHistoryMatch struct {
	EntryID   string `json:"entry_id"`
	Role      string `json:"role"`
	Timestamp string `json:"timestamp"`
	Snippet   string `json:"snippet"`
}

type SessionHistoryEntry struct {
	EntryID    string `json:"entry_id"`
	Type       string `json:"type"`
	Role       string `json:"role,omitempty"`
	Timestamp  string `json:"timestamp"`
	Offset     int    `json:"offset,omitempty"`
	End        int    `json:"end,omitempty"`
	TotalChars int    `json:"total_chars,omitempty"`
	More       bool   `json:"more,omitempty"`
	Content    string `json:"content"`
}

type SessionHistoryOutput struct {
	Action  string                `json:"action"`
	Matches []SessionHistoryMatch `json:"matches,omitempty"`
	Entries []SessionHistoryEntry `json:"entries,omitempty"`
}
```

## extract_document.go

[internal/tools/builtin/extract_document.go](../../internal/tools/builtin/extract_document.go)

```go
type ExtractDocumentInput struct {
	Path         string `json:"path,omitempty" jsonschema:"description=PDF/DOCX/XLSX/PPTX file path in an allowed workspace or Sandbox root. Use either path or attachment_id."`
	AttachmentID string `json:"attachment_id,omitempty" jsonschema:"description=Document attachment ID shown in the conversation. Use either attachment_id or path."`
	Offset       int    `json:"offset,omitempty" jsonschema:"description=Unicode character offset into extracted Markdown; starts at 0."`
	Limit        int    `json:"limit,omitempty" jsonschema:"description=Maximum Unicode characters to return; default 12000, maximum 20000."`
}

type ExtractDocumentOutput struct {
	Path         string `json:"path,omitempty"`
	AttachmentID string `json:"attachment_id,omitempty"`
	Name         string `json:"name"`
	MIMEType     string `json:"mime_type"`
	Format       string `json:"format"`
	Offset       int    `json:"offset"`
	End          int    `json:"end"`
	TotalChars   int    `json:"total_chars"`
	More         bool   `json:"more"`
	Content      string `json:"content"`
}
```

## file_ops.go

[internal/tools/builtin/file_ops.go](../../internal/tools/builtin/file_ops.go)

```go
type CopyFileInput struct {
	Source       string `json:"source,omitempty" jsonschema:"description=Source file path. Use either source or attachment_id."`
	AttachmentID string `json:"attachment_id,omitempty" jsonschema:"description=Current-session attachment ID to copy into the destination. Use instead of source."`
	Destination  string `json:"destination" jsonschema:"description=Destination file. Relative paths are resolved from the workspace."`
	Overwrite    bool   `json:"overwrite,omitempty" jsonschema:"description=Allow replacing an existing destination file."`
}

type MoveFileInput struct {
	Source      string `json:"source" jsonschema:"description=Source file. Relative paths are resolved from the workspace."`
	Destination string `json:"destination" jsonschema:"description=Destination file. Relative paths are resolved from the workspace."`
	Overwrite   bool   `json:"overwrite,omitempty" jsonschema:"description=Allow replacing an existing destination file."`
}

type FileOperationOutput struct {
	Source       string `json:"source,omitempty"`
	AttachmentID string `json:"attachment_id,omitempty"`
	Destination  string `json:"destination,omitempty"`
	Path         string `json:"path,omitempty"`
	Bytes        int64  `json:"bytes,omitempty"`
}

type DeleteFileInput struct {
	Path string `json:"path" jsonschema:"description=Regular file to delete. Relative paths are resolved from the workspace."`
}
```

## git_tools.go

[internal/tools/builtin/git_tools.go](../../internal/tools/builtin/git_tools.go)

```go
type GitStatusInput struct {
	Path string `json:"path,omitempty" jsonschema:"description=Repository directory. Relative paths resolve from the workspace."`
}

type GitDiffInput struct {
	Path   string `json:"path,omitempty" jsonschema:"description=Repository directory. Relative paths resolve from the workspace."`
	Staged bool   `json:"staged,omitempty" jsonschema:"description=Show staged changes instead of unstaged changes."`
	File   string `json:"file,omitempty" jsonschema:"description=Optional repository-relative file path to limit the diff."`
}

type GitLogInput struct {
	Path  string `json:"path,omitempty" jsonschema:"description=Repository directory. Relative paths resolve from the workspace."`
	Limit int    `json:"limit,omitempty" jsonschema:"description=Number of commits. Defaults to 20 and is capped at 100."`
}

type GitReadOutput struct {
	Output        string `json:"output"`
	ExitCode      int    `json:"exit_code"`
	Truncated     bool   `json:"truncated"`
	NativeSandbox bool   `json:"native_sandbox"`
}
```

## install_skill.go

[internal/tools/builtin/install_skill.go](../../internal/tools/builtin/install_skill.go)

```go
type InstallSkillInput struct {
	// SourceURL 是用户明确提供的公开 HTTPS Skill 地址。
	SourceURL string `json:"source_url" jsonschema:"description=Public HTTPS Skill source explicitly provided by the user. Built-in resolvers support direct archives, GitHub, GitLab.com, Gitee, and skills.sh."`

	// SkillPath 是 ZIP 内 Skill 目录的相对路径。单 Skill ZIP 可以省略。
	SkillPath string `json:"skill_path,omitempty" jsonschema:"description=Optional relative Skill directory inside the archive when it contains multiple SKILL.md files."`

	// EnableForCurrentAgent 控制安装成功后是否写入当前 Agent Profile。
	EnableForCurrentAgent bool `json:"enable_for_current_agent,omitempty" jsonschema:"description=Set true only when the user asked to use this Skill with the current Agent. It becomes available from the next user turn."`
}

type InstallSkillOutput struct {
	Name string `json:"name"`

	Description string `json:"description"`

	Installed bool `json:"installed"`

	EnabledForCurrentAgent bool `json:"enabled_for_current_agent"`

	EffectiveFrom string `json:"effective_from,omitempty"`

	EnableWarning string `json:"enable_warning,omitempty"`
}
```

## run_command.go

[internal/tools/builtin/run_command.go](../../internal/tools/builtin/run_command.go)

```go
type RunCommandInput struct {
	// Command 支持程序名或路径；参数单独传入，不解析 Shell 命令字符串。
	Command string `json:"command" jsonschema:"description=Executable name or path, for example go or /opt/homebrew/bin/go. No shell syntax."`

	// Args 会逐项作为 argv 传递，不经过 Shell 解析。
	Args []string `json:"args,omitempty" jsonschema:"description=Argument vector passed directly to the executable. Do not combine multiple arguments into a shell command string."`

	// WorkingDirectory 支持沙盒允许的绝对目录或工作区相对目录，默认 "."。
	WorkingDirectory string `json:"working_directory,omitempty" jsonschema:"description=Absolute or workspace-relative working directory allowed by the active sandbox. Defaults to workspace root."`

	// TimeoutSeconds 允许模型缩短或适度延长单次命令时间，但不能突破配置硬上限。
	TimeoutSeconds int `json:"timeout_seconds,omitempty" jsonschema:"description=Optional timeout in seconds. The configured maximum is always enforced."`
}

type RunCommandOutput struct {
	Command string `json:"command"`

	Args []string `json:"args,omitempty"`

	WorkingDirectory string `json:"working_directory"`

	ExitCode int `json:"exit_code"`

	TimedOut bool `json:"timed_out"`

	// TerminationReason 明确告诉模型进程为什么结束，避免仅凭 exit_code=-1 猜测配置问题。
	TerminationReason string `json:"termination_reason"`

	Message string `json:"message,omitempty"`

	Output string `json:"output,omitempty"`

	OutputTruncated bool `json:"output_truncated"`

	DurationMS int64 `json:"duration_ms"`

	NativeSandbox bool `json:"native_sandbox"`
}
```

## run_skill_script.go

[internal/tools/builtin/run_skill_script.go](../../internal/tools/builtin/run_skill_script.go)

```go
type RunSkillScriptInput struct {
	Skill string `json:"skill" jsonschema:"description=Name of an enabled Skill in the current turn."`

	Script string `json:"script" jsonschema:"description=Script path relative to the Skill scripts directory. Both extract.py and scripts/extract.py are accepted."`

	Args []string `json:"args,omitempty" jsonschema:"description=Arguments passed to the Skill script after the script path."`

	WorkingDirectory string `json:"working_directory,omitempty" jsonschema:"description=Relative working directory inside the current Agent workspace. Defaults to workspace root."`

	TimeoutSeconds int `json:"timeout_seconds,omitempty" jsonschema:"description=Optional timeout in seconds. The configured command maximum is always enforced."`
}

type RunSkillScriptOutput struct {
	Skill             string   `json:"skill"`
	Script            string   `json:"script"`
	Runtime           string   `json:"runtime"`
	Command           string   `json:"command"`
	Args              []string `json:"args,omitempty"`
	WorkingDirectory  string   `json:"working_directory"`
	ExitCode          int      `json:"exit_code"`
	TimedOut          bool     `json:"timed_out"`
	TerminationReason string   `json:"termination_reason"`
	Message           string   `json:"message,omitempty"`
	Output            string   `json:"output,omitempty"`
	OutputTruncated   bool     `json:"output_truncated"`
	DurationMS        int64    `json:"duration_ms"`
	NativeSandbox     bool     `json:"native_sandbox"`
}
```

## schedule_task.go

[internal/tools/builtin/schedule_task.go](../../internal/tools/builtin/schedule_task.go)

```go
type ScheduleTaskInput struct {
	Name            string `json:"name" jsonschema_description:"Short user-visible task name"`
	Prompt          string `json:"prompt" jsonschema_description:"Exact notification text, or instructions for the future agent run"`
	Execution       string `json:"execution" jsonschema_description:"notification or agent"`
	ScheduleType    string `json:"schedule_type" jsonschema_description:"once, daily, weekly, or interval"`
	TimeZone        string `json:"time_zone" jsonschema_description:"IANA timezone such as Asia/Shanghai"`
	RunAt           string `json:"run_at,omitempty" jsonschema_description:"For once only: future RFC3339 timestamp with explicit UTC offset"`
	TimeOfDay       string `json:"time_of_day,omitempty" jsonschema_description:"For daily or weekly: HH:MM in time_zone"`
	Weekdays        []int  `json:"weekdays,omitempty" jsonschema_description:"For weekly only: 0=Sunday through 6=Saturday"`
	IntervalMinutes int    `json:"interval_minutes,omitempty" jsonschema_description:"For interval only: number of minutes between runs"`
}

type ScheduleTaskOutput struct {
	TaskID    string `json:"task_id"`
	Name      string `json:"name"`
	NextRunAt string `json:"next_run_at,omitempty"`
	Status    string `json:"status"`
}
```

## system_tools.go

[internal/tools/builtin/system_tools.go](../../internal/tools/builtin/system_tools.go)

```go
type CurrentTimeInput struct {
	TimeZone string `json:"time_zone,omitempty" jsonschema:"description=IANA time zone such as Asia/Tokyo. Omit to use the local time zone."`
}

type CurrentTimeOutput struct {
	TimeZone string `json:"time_zone"`
	RFC3339  string `json:"rfc3339"`
	Display  string `json:"display"`
}

type PlanItem struct {
	Content string `json:"content" jsonschema:"description=Short task description."`
	Status  string `json:"status" jsonschema:"description=Task status: pending, in_progress, or completed."`
}

type UpdatePlanInput struct {
	Items []PlanItem `json:"items" jsonschema:"description=The complete current plan. Replaces the previous plan for this tool instance."`
}

type UpdatePlanOutput struct {
	Items    []PlanItem `json:"items"`
	Summary  string     `json:"summary"`
	Adjusted bool       `json:"adjusted,omitempty"`
	Notice   string     `json:"notice,omitempty"`
}
```

## webfetch_tool.go

[internal/tools/builtin/webfetch_tool.go](../../internal/tools/builtin/webfetch_tool.go)

```go
type WebFetchInput struct {
	URL      string `json:"url" jsonschema:"description=Absolute public http or https URL to read."`
	MaxChars int    `json:"max_chars,omitempty" jsonschema:"description=Maximum characters returned to the model. Omit to use the configured default."`
}

type WebFetchOutput struct {
	URL         string `json:"url"`
	FinalURL    string `json:"final_url"`
	StatusCode  int    `json:"status_code"`
	ContentType string `json:"content_type,omitempty"`
	Format      string `json:"format"`
	Truncated   bool   `json:"truncated"`
	Content     string `json:"content"`
}
```

## service.go

[internal/websearch/service.go](../../internal/websearch/service.go)

```go
type Input struct {
	Query          string   `json:"query" jsonschema:"description=Search keywords. For time-sensitive information include the concrete subject; the runtime already provides the current date in the system prompt."`
	Count          int      `json:"count,omitempty" jsonschema:"description=Desired result count. Omit to use the configured default."`
	AllowedDomains []string `json:"allowed_domains,omitempty" jsonschema:"description=Only include these domains and their subdomains. Use this when the user requests a specific official site."`
	BlockedDomains []string `json:"blocked_domains,omitempty" jsonschema:"description=Exclude these domains and their subdomains."`
}

type Result struct {
	Title       string `json:"title"`
	URL         string `json:"url"`
	Snippet     string `json:"snippet,omitempty"`
	PublishedAt string `json:"published_at,omitempty"`
}

type Attempt struct {
	Provider    string `json:"provider"`
	Status      string `json:"status"`
	ResultCount int    `json:"result_count,omitempty"`
	ErrorType   string `json:"error_type,omitempty"`
}

type Diagnostics struct {
	Strategy       string    `json:"strategy"`
	SelectedStatus string    `json:"selected_status,omitempty"`
	Attempts       []Attempt `json:"attempts,omitempty"`
}

type Output struct {
	Query       string      `json:"query"`
	Provider    string      `json:"provider"`
	Results     []Result    `json:"results"`
	Diagnostics Diagnostics `json:"diagnostics"`
}
```

