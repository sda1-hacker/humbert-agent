package workspace

// Mode 描述一个 Agent Workspace 的来源。
type Mode string

const (
	// ModeManaged 表示 Humbert 管理默认 Workspace。
	//
	// 实际路径：
	//
	//	~/.humbert-agent/workspaces/<agent-id>
	//
	// Agent Profile 中 configured_path 保持为空。
	ModeManaged Mode = "managed"

	// ModeCustom 表示用户明确选择一个外部目录作为 Workspace。
	//
	// 例如：
	//
	//	/Users/alice/workspaces/humbert
	//
	// Humbert 不会在目录外增加 files/ 等额外层级。
	ModeCustom Mode = "custom"
)

// Workspace 是一次 Runtime Snapshot 中冻结的 Workspace 描述。
//
// ConfiguredPath：
//
//   - managed 模式为空；
//   - custom 模式保存用户配置路径。
//
// RootDir:
//
//   - 当前 Turn 真正使用的物理根目录；
//   - 已经转换为绝对路径；
//   - custom 模式会解析最外层 symlink。
//
// Runtime 创建 Snapshot 后，即使用户修改 Agent Workspace，
// 当前 Turn 仍然继续使用这个 Workspace。
type Workspace struct {
	AgentID string

	Mode Mode

	ConfiguredPath string

	RootDir string
}
