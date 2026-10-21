package tools

// DisplayMetadata 是能力设置的展示元数据，不参与权限判断或模型工具 Schema。
// 新工具可在 Descriptor.Display 中提供元数据；未提供时使用内置目录或安全的通用展示。
type DisplayMetadata struct {
	Category       string
	Label          string
	DefaultEnabled bool
}

var builtinDisplay = map[string]DisplayMetadata{
	"list_files":    {Category: "files", Label: "列出文件", DefaultEnabled: true},
	"read_file":     {Category: "files", Label: "读取文件", DefaultEnabled: true},
	"write_file":    {Category: "files", Label: "写入文件", DefaultEnabled: true},
	"edit_file":     {Category: "files", Label: "编辑文件", DefaultEnabled: true},
	"glob_files":    {Category: "files", Label: "按名称查找文件", DefaultEnabled: true},
	"grep_files":    {Category: "files", Label: "搜索文件内容", DefaultEnabled: true},
	"run_command":   {Category: "execution", Label: "执行本地命令", DefaultEnabled: false},
	"computer":      {Category: "execution", Label: "操作桌面应用", DefaultEnabled: true},
	"git":           {Category: "git", Label: "查看 Git", DefaultEnabled: false},
	"web_search":    {Category: "web", Label: "网页搜索", DefaultEnabled: true},
	"web_fetch":     {Category: "web", Label: "读取网页", DefaultEnabled: true},
	"browser":       {Category: "web", Label: "浏览网页与截图", DefaultEnabled: true},
	"install_skill": {Category: "skills", Label: "安装 Skill", DefaultEnabled: true},
	"list_agents":   {Category: "collaboration", Label: "查看可用 Agent", DefaultEnabled: true},
	"run_agent":     {Category: "collaboration", Label: "调用专业 Agent", DefaultEnabled: true},
	"schedule_task": {Category: "agent", Label: "安排提醒或任务", DefaultEnabled: true},
}

// DisplayMetadata 返回值副本，调用方无法修改目录定义。
func (d Descriptor) DisplayMetadata() DisplayMetadata {
	if d.Display != nil {
		return *d.Display
	}
	if value, ok := builtinDisplay[d.Name]; ok {
		return value
	}
	return DisplayMetadata{Category: "other", Label: d.Name}
}
