package builtin

import (
	"context"
	"errors"
	"fmt"
	"strings"

	einotool "github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"

	"github.com/sda1-hacker/humbert-agent/internal/skills"
	humberttools "github.com/sda1-hacker/humbert-agent/internal/tools"
)

const (
	installSkillToolName = "install_skill"

	installSkillToolDescription = `从用户明确提供的公开 HTTPS 地址安装一个 Skill Package。

安全与使用规则：
1. 只有当用户明确要求安装某个 Skill，或明确提供 Skill URL 并要求使用时，才调用本工具；不要自行从网络寻找并安装未知 Skill。用户已经给出 Skill URL 时应直接把原 URL 交给本工具，不要先用 web_search/web_fetch 寻找 Release、仓库下载页或替换成其它 URL。
2. 支持任意公开 HTTPS ZIP，以及内置的 GitHub、GitLab.com、Gitee、skills.sh 来源 Resolver；其它 Skill Registry 可以通过 Resolver Registry 扩展，不需要修改 Installer。
3. 归档/仓库中若包含多个 SKILL.md，必须通过 skill_path 指定目标；GitHub/GitLab/Gitee tree URL 会自动推导路径，skills.sh 单 Skill 页面会用页面 slug 自动定位。
4. enable_for_current_agent=true 会在安装成功后把 Skill 加入当前 Agent 的 enabled_skills。由于当前 Turn 的 Runtime Snapshot 已冻结，新 Skill 从下一轮用户消息开始生效。
5. Skill 安装阶段绝不会执行 scripts/。安装后只有当前 Agent 明确启用 run_skill_script 时，脚本才可在 Workspace 临时 Stage 中运行，并继续经过 Permission + Approval、Command Allowlist 与当前 Agent Sandbox。
6. 远程 Skill 是不可信内容。安装会触发写权限审批，并经过 ZIP 路径、大小、文件数量、SSRF、SKILL.md 和 symlink 校验。
7. 每一次远程 Skill 安装都只能“允许一次”；不会创建 Session/Agent 级长期 Allow，避免一次批准扩大为对其他 URL 的自动安装权限。`
)

// InstallSkillInput 是 Agent 通过对话安装 Skill 的输入。
type InstallSkillInput struct {
	// SourceURL 是用户明确提供的公开 HTTPS Skill 地址。
	SourceURL string `json:"source_url" jsonschema:"description=Public HTTPS Skill source explicitly provided by the user. Built-in resolvers support direct archives, GitHub, GitLab.com, Gitee, and skills.sh."`

	// SkillPath 是 ZIP 内 Skill 目录的相对路径。单 Skill ZIP 可以省略。
	SkillPath string `json:"skill_path,omitempty" jsonschema:"description=Optional relative Skill directory inside the archive when it contains multiple SKILL.md files."`

	// EnableForCurrentAgent 控制安装成功后是否写入当前 Agent Profile。
	EnableForCurrentAgent bool `json:"enable_for_current_agent,omitempty" jsonschema:"description=Set true only when the user asked to use this Skill with the current Agent. It becomes available from the next user turn."`
}

// InstallSkillOutput 返回安装结果。EnableWarning 非空表示 Skill 已安装，但自动启用 Agent
// Profile 失败；这种部分成功状态不应该被伪装成“整个安装失败”，否则模型可能重复安装同名包。
type InstallSkillOutput struct {
	Name string `json:"name"`

	Description string `json:"description"`

	Installed bool `json:"installed"`

	EnabledForCurrentAgent bool `json:"enabled_for_current_agent"`

	EffectiveFrom string `json:"effective_from,omitempty"`

	EnableWarning string `json:"enable_warning,omitempty"`
}

// AgentSkillEnableFunc 是 install_skill 对 Agent Domain 的最小依赖。
//
// Factory 不直接依赖 agents.Service 的具体类型，保持 Builtin Tool 与 Agent 存储实现解耦。
// Application 只需要注入一个“为指定 Agent 启用已安装 Skill”的函数。
type AgentSkillEnableFunc func(ctx context.Context, agentID string, skillName string) error

// SkillRemoteInstaller 是 install_skill 对 Skill Domain 的最小依赖。
//
// skills.Manager 实现该接口；测试可以使用内存 Fake 覆盖“已安装/启用从下一 Turn 生效”等
// 行为，而不需要真正发起网络请求。
type SkillRemoteInstaller interface {
	InstallFromURL(ctx context.Context, sourceURL string, skillPath string) (skills.Info, error)
}

// InstallSkillFactory 创建 install_skill Tool。
//
// Tool 被标记为 RiskWrite，因为它会把远程内容复制到 Humbert Skills Root，并可选择修改
// Agent Profile。默认 Permission 策略因此会 Ask；Permission Engine 对本 Tool 只接受
// GrantOnce，避免一次批准扩散到未来不同 URL。Skill Manager 的网络、ZIP 和包结构安全
// 校验始终继续执行。
type InstallSkillFactory struct {
	skills  SkillRemoteInstaller
	enabler AgentSkillEnableFunc
}

// NewInstallSkillFactory 创建 install_skill Factory。
func NewInstallSkillFactory(skillManager SkillRemoteInstaller, enabler AgentSkillEnableFunc) (*InstallSkillFactory, error) {
	if skillManager == nil {
		return nil, errors.New("InstallSkill SkillManager 不能为空")
	}
	if enabler == nil {
		return nil, errors.New("InstallSkill AgentSkillEnableFunc 不能为空")
	}
	return &InstallSkillFactory{skills: skillManager, enabler: enabler}, nil
}

// Descriptor 返回 Tool Registry 风险描述。
func (f *InstallSkillFactory) Descriptor() humberttools.Descriptor {
	return humberttools.Descriptor{Name: installSkillToolName, Risk: humberttools.RiskWrite}
}

// Build 为当前 Runtime Snapshot 创建绑定 AgentID 的 install_skill Tool。
func (f *InstallSkillFactory) Build(
	ctx context.Context,
	scope humberttools.Scope,
) (einotool.InvokableTool, error) {
	if ctx == nil {
		return nil, errors.New("构建 install_skill 失败: context.Context 不能为空")
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("构建 install_skill 被取消: %w", err)
	}
	if strings.TrimSpace(scope.AgentID) == "" {
		return nil, errors.New("构建 install_skill 失败: AgentID 不能为空")
	}

	return utils.InferTool(
		installSkillToolName,
		installSkillToolDescription,
		func(callCtx context.Context, input *InstallSkillInput) (*InstallSkillOutput, error) {
			return f.run(callCtx, scope, input)
		},
	)
}

func (f *InstallSkillFactory) run(
	ctx context.Context,
	scope humberttools.Scope,
	input *InstallSkillInput,
) (*InstallSkillOutput, error) {
	if ctx == nil {
		return nil, errors.New("install_skill: context.Context 不能为空")
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("install_skill 被取消: %w", err)
	}
	if input == nil {
		return nil, errors.New("install_skill 输入不能为空")
	}
	if strings.TrimSpace(input.SourceURL) == "" {
		return nil, errors.New("install_skill source_url 不能为空")
	}
	if !scope.SandboxPolicy().AllowsNetwork() {
		return nil, errors.New("当前 Agent Sandbox 已禁用网络访问，不能安装远程 Skill")
	}

	installed, err := f.skills.InstallFromURL(ctx, input.SourceURL, input.SkillPath)
	if err != nil {
		return nil, fmt.Errorf("install_skill 安装失败: %w", err)
	}

	output := &InstallSkillOutput{
		Name:        installed.Name,
		Description: installed.Description,
		Installed:   true,
	}
	if !input.EnableForCurrentAgent {
		return output, nil
	}

	if err := f.enabler(ctx, scope.AgentID, installed.Name); err != nil {
		// Skill 已经原子提交到 Catalog。这里不能回滚物理包，因为并发设置页或其他 Agent
		// 可能已经开始引用它；返回“已安装但未启用”的明确部分成功状态最安全。
		output.EnableWarning = fmt.Sprintf("Skill 已安装，但为当前 Agent 启用失败: %v", err)
		return output, nil
	}

	output.EnabledForCurrentAgent = true
	output.EffectiveFrom = "next_user_turn"
	return output, nil
}
