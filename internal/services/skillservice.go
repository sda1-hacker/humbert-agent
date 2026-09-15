package services

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/sda1-hacker/humbert-agent/internal/agents"
	coreapp "github.com/sda1-hacker/humbert-agent/internal/app"
	"github.com/sda1-hacker/humbert-agent/internal/skills"

	"github.com/wailsapp/wails/v3/pkg/application"
)

const (
	skillServiceReadTimeout    = 15 * time.Second
	skillServiceInstallTimeout = 2 * time.Minute
)

// SkillAgentDTO 是 Skills 控制面需要的最小 Agent 身份投影。
//
// 它既用于 State.Agents 驱动“先选 Agent，再开关 Skill”，也用于 SkillDTO.UsedByAgents 展示
// 当前启用情况。真正关系仍只存在 Agent Profile.enabled_skills；删除时后端会重新扫描。
type SkillAgentDTO struct {
	ID string `json:"id"`

	Name string `json:"name"`

	// EnabledSkills 是该 Agent Profile 当前保存的 canonical Skill 名称副本。
	// Settings → Skills 用它驱动“先选 Agent，再开关 Skill”的唯一控制面；它不是第二份关系存储。
	EnabledSkills []string `json:"enabledSkills"`
}

// SkillFileDTO 是 Skill 详情页使用的只读包内文件元数据。
//
// Path 始终是已经过 skills.Manager 校验的 `/` 分隔相对路径；Text=false 的文件只展示在树中，
// 不允许通过 ReadSkillFile 把二进制内容送入 WebView。
type SkillFileDTO struct {
	Path string `json:"path"`

	SizeBytes int64 `json:"sizeBytes"`

	Text bool `json:"text"`
}

// SkillSourceDTO 是详情页可展示的来源投影。
//
// DisplayURL 会去掉 query/fragment，避免 Direct ZIP 的短期签名参数进入 WebView；真正的完整
// OriginalURL 只保存在 0600 的 Humbert 来源文件中，并由 Go Update/Reinstall 内部使用。
type SkillSourceDTO struct {
	Known bool `json:"known"`

	Kind string `json:"kind,omitempty"`

	Provider string `json:"provider,omitempty"`

	DisplayURL string `json:"displayUrl,omitempty"`

	LocalDirectory string `json:"localDirectory,omitempty"`

	SkillPath string `json:"skillPath,omitempty"`

	Repository string `json:"repository,omitempty"`

	Ref string `json:"ref,omitempty"`

	InstalledAt string `json:"installedAt,omitempty"`

	UpdatedAt string `json:"updatedAt,omitempty"`

	RecordedIdentity string `json:"recordedIdentity,omitempty"`

	Drifted bool `json:"drifted"`
}

// SkillUpdateCheckDTO 是用户显式点击“检查更新”后的结果。
type SkillUpdateCheckDTO struct {
	Name string `json:"name"`

	CurrentIdentity string `json:"currentIdentity"`

	CandidateIdentity string `json:"candidateIdentity"`

	UpdateAvailable bool `json:"updateAvailable"`

	SourceDrifted bool `json:"sourceDrifted"`

	CheckedAt string `json:"checkedAt"`
}

// SkillUpdateResultDTO 是 Update/Reinstall 的提交结果。
type SkillUpdateResultDTO struct {
	Name string `json:"name"`

	Identity string `json:"identity"`

	PreviousIdentity string `json:"previousIdentity"`

	Changed bool `json:"changed"`

	Source SkillSourceDTO `json:"source"`
}

// SkillDiagnosticDTO 是前端展示的兼容诊断。
type SkillDiagnosticDTO struct {
	Code    string `json:"code"`
	Level   string `json:"level"`
	Message string `json:"message"`
}

// SkillScriptRuntimeDTO 描述 scripts/ 文件的解释器探测状态。
type SkillScriptRuntimeDTO struct {
	Path      string `json:"path"`
	Language  string `json:"language,omitempty"`
	Command   string `json:"command,omitempty"`
	Available bool   `json:"available"`
	Supported bool   `json:"supported"`
	Message   string `json:"message,omitempty"`
}

// SkillDiscoveryCandidateDTO 是一次 Repository Discovery 中可选择安装的 Skill。
type SkillDiscoveryCandidateDTO struct {
	Path           string                  `json:"path"`
	Name           string                  `json:"name"`
	Description    string                  `json:"description"`
	SpecStatus     string                  `json:"specStatus"`
	SpecMessage    string                  `json:"specMessage,omitempty"`
	RuntimeStatus  string                  `json:"runtimeStatus"`
	RuntimeMessage string                  `json:"runtimeMessage,omitempty"`
	Valid          bool                    `json:"valid"`
	Error          string                  `json:"error,omitempty"`
	Installed      bool                    `json:"installed"`
	FileCount      int                     `json:"fileCount"`
	SizeBytes      int64                   `json:"sizeBytes"`
	HasScripts     bool                    `json:"hasScripts"`
	HasReferences  bool                    `json:"hasReferences"`
	HasAssets      bool                    `json:"hasAssets"`
	Diagnostics    []SkillDiagnosticDTO    `json:"diagnostics,omitempty"`
	ScriptRuntimes []SkillScriptRuntimeDTO `json:"scriptRuntimes,omitempty"`
}

// SkillDiscoveryDTO 是本地目录或远程仓库的 Skill 发现结果。
type SkillDiscoveryDTO struct {
	SourceKind    string                       `json:"sourceKind"`
	Provider      string                       `json:"provider,omitempty"`
	DisplaySource string                       `json:"displaySource,omitempty"`
	Candidates    []SkillDiscoveryCandidateDTO `json:"candidates"`
}

// SkillDetailDTO 是用户点击一个 Skill 后按需加载的详情。
//
// State 不再长期把 SKILL.md/脚本正文放进 WebView；详情页只加载文件树，具体文本文件仍在用户
// 点击时通过 ReadSkillFile 单独读取。
type SkillDetailDTO struct {
	Name string `json:"name"`

	Alias string `json:"alias,omitempty"`

	Description string `json:"description"`

	SpecStatus string `json:"specStatus"`

	SpecMessage string `json:"specMessage,omitempty"`

	License string `json:"license,omitempty"`

	Compatibility string `json:"compatibility,omitempty"`

	Metadata map[string]string `json:"metadata,omitempty"`

	AllowedTools string `json:"allowedTools,omitempty"`

	RuntimeStatus string `json:"runtimeStatus"`

	RuntimeMessage string `json:"runtimeMessage,omitempty"`

	Diagnostics []SkillDiagnosticDTO `json:"diagnostics,omitempty"`

	ScriptRuntimes []SkillScriptRuntimeDTO `json:"scriptRuntimes,omitempty"`

	HasAssets bool `json:"hasAssets"`

	RootDir string `json:"rootDir"`

	Identity string `json:"identity"`

	FileCount int `json:"fileCount"`

	SizeBytes int64 `json:"sizeBytes"`

	UpdatedAt string `json:"updatedAt"`

	Source SkillSourceDTO `json:"source"`

	Files []SkillFileDTO `json:"files"`
}

// SkillFileContentDTO 是详情页一次文本预览的返回值。
type SkillFileContentDTO struct {
	Path string `json:"path"`

	SizeBytes int64 `json:"sizeBytes"`

	Content string `json:"content"`
}

// SkillDTO 是设置页与 Agent Skill Selector 使用的安全 Skill 元数据。
//
// SKILL.md 正文不会通过 Desktop Service 全量暴露给 WebView。Agent Runtime 直接从受控
// skills.Manager Snapshot 读取内容，设置页只需要名称、描述、包大小与健康状态。
type SkillDTO struct {
	Name string `json:"name"`

	// Alias 是 Humbert 用户自己的本地展示名称。它不参与 Skill 身份、Agent 引用或 Runtime。
	Alias string `json:"alias,omitempty"`

	Description string `json:"description"`

	SpecStatus string `json:"specStatus"`

	SpecMessage string `json:"specMessage,omitempty"`

	License string `json:"license,omitempty"`

	Compatibility string `json:"compatibility,omitempty"`

	Metadata map[string]string `json:"metadata,omitempty"`

	AllowedTools string `json:"allowedTools,omitempty"`

	RuntimeStatus string `json:"runtimeStatus"`

	RuntimeMessage string `json:"runtimeMessage,omitempty"`

	Diagnostics []SkillDiagnosticDTO `json:"diagnostics,omitempty"`

	ScriptRuntimes []SkillScriptRuntimeDTO `json:"scriptRuntimes,omitempty"`

	DirectoryName string `json:"directoryName"`

	RootDir string `json:"rootDir"`

	Identity string `json:"identity"`

	Valid bool `json:"valid"`

	Error string `json:"error,omitempty"`

	FileCount int `json:"fileCount"`

	SizeBytes int64 `json:"sizeBytes"`

	HasReferences bool `json:"hasReferences"`

	HasScripts bool `json:"hasScripts"`

	HasAssets bool `json:"hasAssets"`

	UpdatedAt string `json:"updatedAt"`

	// Source 是 Humbert 记录的安装来源投影。Invalid Skill 也会携带它，
	// 这样详情页可以直接提供“从来源修复”，而不要求 Package 当前可解析。
	Source SkillSourceDTO `json:"source"`

	UsedByAgents []SkillAgentDTO `json:"usedByAgents"`
}

// SkillStateDTO 是 Skills 设置页一次加载需要的完整状态。
type SkillStateDTO struct {
	RootDir string `json:"rootDir"`

	// SourceError 只表示来源元数据不可用，不应让整个 Catalog 失效。Skill 安装、启停和删除
	// 仍然可以继续；更新/修复来源需要用户先处理该警告。
	SourceError string `json:"sourceError,omitempty"`

	SourceResolvers []string `json:"sourceResolvers"`

	// Agents 是 Skills 设置页顶部可选择的 Agent 列表，并携带 enabled_skills 只读投影。
	// 开关操作最终仍然只修改 Agent.config.json，不创建第二份 Skill 关系存储。
	Agents []SkillAgentDTO `json:"agents"`

	Skills []SkillDTO `json:"skills"`
}

// SkillService 是本地 Skill Package 的 Wails Desktop Adapter。
//
// 路径选择器属于桌面适配层；真正的目录安全校验、复制、哈希和 Runtime Snapshot 都由
// skills.Manager 完成。文件详情也只通过 Manager 的受控只读接口按需读取。
type SkillService struct {
	core *coreapp.Application
}

// NewSkillService 创建 Skill Desktop Service。
func NewSkillService(core *coreapp.Application) *SkillService {
	return &SkillService{core: core}
}

// ServiceName 返回 Wails Service Name。
func (s *SkillService) ServiceName() string {
	return "SkillService"
}

// State 返回当前已安装 Skill 与受控安装目录。
func (s *SkillService) State() (SkillStateDTO, error) {
	if err := s.validate(); err != nil {
		return SkillStateDTO{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), skillServiceReadTimeout)
	defer cancel()

	values, err := s.core.Skills().List(ctx)
	if err != nil {
		return SkillStateDTO{}, fmt.Errorf("读取 Skill 列表失败: %w", err)
	}
	agentValues, err := s.core.Agents().List(ctx)
	if err != nil {
		return SkillStateDTO{}, fmt.Errorf("读取 Agent Skill 配置失败: %w", err)
	}
	usage := buildSkillUsageMap(agentValues)
	aliases, err := s.core.Skills().Aliases(ctx)
	if err != nil {
		return SkillStateDTO{}, fmt.Errorf("读取 Skill 展示 Alias 失败: %w", err)
	}
	sources, sourceErr := s.core.Skills().Sources(ctx)
	sourceError := ""
	if sourceErr != nil {
		// 来源元数据属于更新/修复控制面，不应该因为它损坏就让整个 Skills Catalog 空白。
		// 后端 Update/Reinstall 仍会严格拒绝使用损坏来源；这里仅降级为 Catalog 可读。
		sources = make(map[string]skills.SourceInfo)
		sourceError = fmt.Sprintf("读取 Skill 安装来源失败: %v", sourceErr)
	}

	result := make([]SkillDTO, 0, len(values))
	for _, value := range values {
		dto := projectSkillDTO(value)
		dto.Alias = aliases[value.Name]
		if source, known := sources[value.Name]; known {
			dto.Source = projectSkillSource(source, true, value.Identity)
		} else {
			dto.Source = projectSkillSource(skills.SourceInfo{}, false, value.Identity)
		}
		dto.UsedByAgents = append([]SkillAgentDTO(nil), usage[value.Name]...)
		result = append(result, dto)
	}
	return SkillStateDTO{
		RootDir:         s.core.Skills().RootDir(),
		SourceError:     sourceError,
		SourceResolvers: s.core.Skills().RemoteSourceResolvers(),
		Agents:          projectSkillAgents(agentValues),
		Skills:          result,
	}, nil
}

// SkillDetail 按需返回一个有效 Skill 的文件树与稳定元数据。
//
// 该方法不会返回任何文件正文；这样普通 Catalog 浏览不会把完整 Skill Package 常驻 WebView。
func (s *SkillService) SkillDetail(name string) (SkillDetailDTO, error) {
	if err := s.validate(); err != nil {
		return SkillDetailDTO{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), skillServiceReadTimeout)
	defer cancel()

	pkg, err := s.core.Skills().Get(ctx, name)
	if err != nil {
		return SkillDetailDTO{}, fmt.Errorf("读取 Skill 详情失败: %w", err)
	}
	aliases, err := s.core.Skills().Aliases(ctx)
	if err != nil {
		return SkillDetailDTO{}, fmt.Errorf("读取 Skill 展示 Alias 失败: %w", err)
	}
	source, sourceKnown, err := s.core.Skills().Source(ctx, pkg.Info.Name)
	if err != nil {
		return SkillDetailDTO{}, fmt.Errorf("读取 Skill 安装来源失败: %w", err)
	}

	updatedAt := ""
	if !pkg.Info.UpdatedAt.IsZero() {
		updatedAt = pkg.Info.UpdatedAt.UTC().Format(time.RFC3339Nano)
	}
	return SkillDetailDTO{
		Name:           pkg.Info.Name,
		Alias:          aliases[pkg.Info.Name],
		Description:    pkg.Info.Description,
		SpecStatus:     string(pkg.Info.SpecStatus),
		SpecMessage:    pkg.Info.SpecMessage,
		License:        pkg.Info.License,
		Compatibility:  pkg.Info.Compatibility,
		Metadata:       cloneSkillStringMap(pkg.Info.Metadata),
		AllowedTools:   pkg.Info.AllowedTools,
		RuntimeStatus:  string(pkg.Info.RuntimeStatus),
		RuntimeMessage: pkg.Info.RuntimeMessage,
		Diagnostics:    projectSkillDiagnostics(pkg.Info.Diagnostics),
		ScriptRuntimes: projectSkillScriptRuntimes(pkg.Info.ScriptRuntimes),
		HasAssets:      pkg.Info.HasAssets,
		RootDir:        pkg.Info.RootDir,
		Identity:       pkg.Info.Identity,
		FileCount:      pkg.Info.FileCount,
		SizeBytes:      pkg.Info.SizeBytes,
		UpdatedAt:      updatedAt,
		Source:         projectSkillSource(source, sourceKnown, pkg.Info.Identity),
		Files:          projectSkillFiles(pkg.Files),
	}, nil
}

// ReadSkillFile 为 Skill 详情页按需读取一个包内 UTF-8 文本文件。
//
// 相对路径、symlink、哈希、大小和二进制检查都在 skills.Manager 内完成。scripts/ 文件在这里
// 也只是文本预览，绝不会因为用户点击而执行。
func (s *SkillService) ReadSkillFile(name string, relativePath string) (SkillFileContentDTO, error) {
	if err := s.validate(); err != nil {
		return SkillFileContentDTO{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), skillServiceReadTimeout)
	defer cancel()

	content, info, err := s.core.Skills().ReadTextFile(ctx, name, relativePath)
	if err != nil {
		return SkillFileContentDTO{}, fmt.Errorf("读取 Skill 文件失败: %w", err)
	}
	return SkillFileContentDTO{
		Path:      info.Path,
		SizeBytes: info.SizeBytes,
		Content:   content,
	}, nil
}

// SelectSkillDirectory 打开原生目录选择器，选择一个包含 SKILL.md 的本地目录。
//
// 返回值仍被视为不可信输入，InstallSkill 会重新执行绝对路径、symlink、大小与包结构校验。
// 用户取消时返回空字符串。
func (s *SkillService) SelectSkillDirectory(currentPath string) (string, error) {
	if err := s.validate(); err != nil {
		return "", err
	}
	app := application.Get()
	if app == nil {
		return "", errors.New("Wails Application 尚未初始化")
	}

	dialog := app.Dialog.
		OpenFile().
		SetTitle("选择 Skill 或包含多个 Skills 的目录").
		CanChooseDirectories(true).
		CanChooseFiles(false).
		CanCreateDirectories(false)

	currentPath = strings.TrimSpace(currentPath)
	if currentPath != "" {
		if info, err := os.Stat(currentPath); err == nil && info.IsDir() {
			dialog.SetDirectory(currentPath)
		}
	}
	path, err := dialog.PromptForSingleSelection()
	if err != nil {
		return "", fmt.Errorf("打开 Skill 目录选择器失败: %w", err)
	}
	return strings.TrimSpace(path), nil
}

// InstallSkill 从用户明确选择的本地目录安装 Skill。
func (s *SkillService) InstallSkill(sourceDirectory string) (SkillDTO, error) {
	if err := s.validate(); err != nil {
		return SkillDTO{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), skillServiceInstallTimeout)
	defer cancel()

	value, err := s.core.Skills().InstallFromDirectory(ctx, sourceDirectory)
	if err != nil {
		return SkillDTO{}, fmt.Errorf("安装本地 Skill 失败: %w", err)
	}
	return projectSkillDTO(value), nil
}

// InstallSkillFromURL 从公开 HTTPS Skill 来源安装 Skill。
//
// 该 Desktop API 与 Agent 的 install_skill Tool 复用同一个 skills.Manager 远程安装实现，因此
// 设置页不会拥有另一套网络/ZIP 安全规则。来源先经过可插拔 Resolver Registry；默认支持
// Direct ZIP、GitHub、GitLab.com、Gitee 与 skills.sh。skillPath 只在一个仓库/归档中存在
// 多个 SKILL.md 时需要；普通单 Skill ZIP 可以留空。
func (s *SkillService) InstallSkillFromURL(sourceURL string, skillPath string) (SkillDTO, error) {
	if err := s.validate(); err != nil {
		return SkillDTO{}, err
	}

	timeout := skillServiceInstallTimeout
	if cfg := s.core.Config(); cfg != nil {
		configured := time.Duration(cfg.Runtime.Skills.DownloadTimeoutMS) * time.Millisecond
		if configured > timeout {
			timeout = configured + 5*time.Second
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	value, err := s.core.Skills().InstallFromURL(ctx, sourceURL, skillPath)
	if err != nil {
		return SkillDTO{}, fmt.Errorf("安装远程 Skill 失败: %w", err)
	}
	return projectSkillDTO(value), nil
}

// DiscoverSkillSource 扫描公开 HTTPS 仓库/归档中的 Skill，不修改本地 Catalog。
func (s *SkillService) DiscoverSkillSource(sourceURL string, skillPath string) (SkillDiscoveryDTO, error) {
	if err := s.validate(); err != nil {
		return SkillDiscoveryDTO{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), s.skillMutationTimeout())
	defer cancel()
	result, err := s.core.Skills().DiscoverFromURL(ctx, sourceURL, skillPath)
	if err != nil {
		return SkillDiscoveryDTO{}, fmt.Errorf("扫描远程 Skill Source 失败: %w", err)
	}
	return projectSkillDiscovery(result), nil
}

// DiscoverLocalSkillSource 扫描用户选择的本地 Skill/仓库目录，不执行安装。
func (s *SkillService) DiscoverLocalSkillSource(sourceDirectory string) (SkillDiscoveryDTO, error) {
	if err := s.validate(); err != nil {
		return SkillDiscoveryDTO{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), skillServiceReadTimeout)
	defer cancel()
	result, err := s.core.Skills().DiscoverFromDirectory(ctx, sourceDirectory)
	if err != nil {
		return SkillDiscoveryDTO{}, fmt.Errorf("扫描本地 Skill Source 失败: %w", err)
	}
	return projectSkillDiscovery(result), nil
}

// InstallDiscoveredSkillsFromURL 从同一次远程 Source 选择中原子安装多个 Skill。
func (s *SkillService) InstallDiscoveredSkillsFromURL(sourceURL string, paths []string) ([]SkillDTO, error) {
	if err := s.validate(); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), s.skillMutationTimeout())
	defer cancel()
	values, err := s.core.Skills().InstallDiscoveredFromURL(ctx, sourceURL, paths)
	if err != nil {
		return nil, fmt.Errorf("批量安装远程 Skills 失败: %w", err)
	}
	result := make([]SkillDTO, 0, len(values))
	for _, value := range values {
		result = append(result, projectSkillDTO(value))
	}
	return result, nil
}

// InstallDiscoveredSkillsFromDirectory 从本地仓库选择中原子安装多个 Skill。
func (s *SkillService) InstallDiscoveredSkillsFromDirectory(sourceDirectory string, paths []string) ([]SkillDTO, error) {
	if err := s.validate(); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), skillServiceInstallTimeout)
	defer cancel()
	values, err := s.core.Skills().InstallDiscoveredFromDirectory(ctx, sourceDirectory, paths)
	if err != nil {
		return nil, fmt.Errorf("批量安装本地 Skills 失败: %w", err)
	}
	result := make([]SkillDTO, 0, len(values))
	for _, value := range values {
		result = append(result, projectSkillDTO(value))
	}
	return result, nil
}

// CheckSkillUpdate 从记录的来源重新读取候选 Package，但不修改当前安装。
func (s *SkillService) CheckSkillUpdate(name string) (SkillUpdateCheckDTO, error) {
	if err := s.validate(); err != nil {
		return SkillUpdateCheckDTO{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), s.skillMutationTimeout())
	defer cancel()

	result, err := s.core.Skills().CheckUpdate(ctx, name)
	if err != nil {
		return SkillUpdateCheckDTO{}, fmt.Errorf("检查 Skill 更新失败: %w", err)
	}
	return SkillUpdateCheckDTO{
		Name:              result.Name,
		CurrentIdentity:   result.CurrentIdentity,
		CandidateIdentity: result.CandidateIdentity,
		UpdateAvailable:   result.UpdateAvailable,
		SourceDrifted:     result.SourceDrifted,
		CheckedAt:         formatSkillTime(result.CheckedAt),
	}, nil
}

// UpdateSkill 从已记录来源更新 Skill；内容没有变化时不会做无意义的目录替换。
func (s *SkillService) UpdateSkill(name string) (SkillUpdateResultDTO, error) {
	if err := s.validate(); err != nil {
		return SkillUpdateResultDTO{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), s.skillMutationTimeout())
	defer cancel()

	result, err := s.core.Skills().Update(ctx, name)
	if err != nil {
		return SkillUpdateResultDTO{}, fmt.Errorf("更新 Skill 失败: %w", err)
	}
	return projectSkillUpdateResult(result), nil
}

// ReinstallSkill 从已记录来源强制重新安装 Skill。
func (s *SkillService) ReinstallSkill(name string) (SkillUpdateResultDTO, error) {
	if err := s.validate(); err != nil {
		return SkillUpdateResultDTO{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), s.skillMutationTimeout())
	defer cancel()

	result, err := s.core.Skills().Reinstall(ctx, name)
	if err != nil {
		return SkillUpdateResultDTO{}, fmt.Errorf("重新安装 Skill 失败: %w", err)
	}
	return projectSkillUpdateResult(result), nil
}

// ReinstallSkillFromURL 为旧版本/手工安装的 Skill 建立远程来源，或显式更换来源。
func (s *SkillService) ReinstallSkillFromURL(
	name string,
	sourceURL string,
	skillPath string,
) (SkillUpdateResultDTO, error) {
	if err := s.validate(); err != nil {
		return SkillUpdateResultDTO{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), s.skillMutationTimeout())
	defer cancel()

	result, err := s.core.Skills().ReinstallFromURL(ctx, name, sourceURL, skillPath)
	if err != nil {
		return SkillUpdateResultDTO{}, fmt.Errorf("从 URL 重新安装 Skill 失败: %w", err)
	}
	return projectSkillUpdateResult(result), nil
}

// ReinstallSkillFromDirectory 为旧版本/手工安装的 Skill 建立本地来源，或显式更换来源。
func (s *SkillService) ReinstallSkillFromDirectory(
	name string,
	sourceDirectory string,
) (SkillUpdateResultDTO, error) {
	if err := s.validate(); err != nil {
		return SkillUpdateResultDTO{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), s.skillMutationTimeout())
	defer cancel()

	result, err := s.core.Skills().ReinstallFromDirectory(ctx, name, sourceDirectory)
	if err != nil {
		return SkillUpdateResultDTO{}, fmt.Errorf("从本地目录重新安装 Skill 失败: %w", err)
	}
	return projectSkillUpdateResult(result), nil
}

// SetSkillAlias 设置或清除一个 Skill 的本地展示别名。
//
// Alias 不写入第三方 SKILL.md，也不会进入 Agent enabled_skills 或 Runtime Snapshot。
func (s *SkillService) SetSkillAlias(name string, alias string) error {
	if err := s.validate(); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), skillServiceReadTimeout)
	defer cancel()

	if err := s.core.Skills().SetAlias(ctx, name, alias); err != nil {
		return fmt.Errorf("保存 Skill Alias 失败: %w", err)
	}
	return nil
}

// EnableSkillForAgent 让指定 Agent 从下一 Turn 开始启用 Skill。
//
// 关系的唯一事实来源仍然是 Agent.config.json 的 enabled_skills；SkillService 只提供从
// Settings → Skills 方向操作同一份数据的 Desktop Adapter。
func (s *SkillService) EnableSkillForAgent(skillName string, agentID string) error {
	if err := s.validate(); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), skillServiceReadTimeout)
	defer cancel()

	if _, err := s.core.Agents().EnableSkillForAgent(ctx, agentID, skillName); err != nil {
		return fmt.Errorf("启用 Agent Skill 失败: %w", err)
	}
	return nil
}

// DisableSkillForAgent 从指定 Agent Profile 移除 Skill 引用。
//
// Disable 允许清理已经 missing/invalid 的 stale 引用；当前 Turn 的冻结 Snapshot 不受影响。
func (s *SkillService) DisableSkillForAgent(skillName string, agentID string) error {
	if err := s.validate(); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), skillServiceReadTimeout)
	defer cancel()

	if _, err := s.core.Agents().DisableSkillForAgent(ctx, agentID, skillName); err != nil {
		return fmt.Errorf("禁用 Agent Skill 失败: %w", err)
	}
	return nil
}

// DeleteSkill 删除一个未被任何 Agent 引用的 Skill。
//
// 不自动修改 Agent Profile 是刻意设计：删除 Skill 与修改 Agent 能力是两个控制面操作。
// 如果仍被引用，用户必须先在“技能”工作区切换对应 Agent 并关闭 Skill，避免后台静默改变 Agent 行为。
func (s *SkillService) DeleteSkill(name string) error {
	if err := s.validate(); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), skillServiceReadTimeout)
	defer cancel()

	users, err := s.core.Agents().AgentsUsingSkill(ctx, name)
	if err != nil {
		return fmt.Errorf("检查 Skill Agent 引用失败: %w", err)
	}
	if len(users) > 0 {
		names := make([]string, 0, len(users))
		for _, user := range users {
			names = append(names, user.Agent.Name)
		}
		return fmt.Errorf(
			"Skill %q 仍被 %d 个 Agent 使用（%s），请先在“技能”页面切换对应 Agent 并关闭该 Skill",
			name,
			len(users),
			strings.Join(names, "、"),
		)
	}
	if err := s.core.Skills().Remove(ctx, name); err != nil {
		return fmt.Errorf("删除 Skill 失败: %w", err)
	}
	return nil
}

func projectSkillAgents(values []agents.AgentInfo) []SkillAgentDTO {
	result := make([]SkillAgentDTO, 0, len(values))
	for _, value := range values {
		id := strings.TrimSpace(value.Agent.ID)
		if id == "" {
			continue
		}
		result = append(result, SkillAgentDTO{
			ID:            id,
			Name:          strings.TrimSpace(value.Agent.Name),
			EnabledSkills: append([]string(nil), value.Agent.EnabledSkills...),
		})
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Name != result[j].Name {
			return result[i].Name < result[j].Name
		}
		return result[i].ID < result[j].ID
	})
	return result
}

func buildSkillUsageMap(values []agents.AgentInfo) map[string][]SkillAgentDTO {
	result := make(map[string][]SkillAgentDTO)
	for _, value := range values {
		agentID := strings.TrimSpace(value.Agent.ID)
		agentName := strings.TrimSpace(value.Agent.Name)
		if agentID == "" {
			continue
		}

		seen := make(map[string]struct{}, len(value.Agent.EnabledSkills))
		for _, rawSkillName := range value.Agent.EnabledSkills {
			skillName := strings.TrimSpace(rawSkillName)
			if skillName == "" {
				continue
			}
			if _, exists := seen[skillName]; exists {
				continue
			}
			seen[skillName] = struct{}{}
			result[skillName] = append(result[skillName], SkillAgentDTO{
				ID:   agentID,
				Name: agentName,
			})
		}
	}

	for skillName := range result {
		sort.Slice(result[skillName], func(i, j int) bool {
			left := result[skillName][i]
			right := result[skillName][j]
			if left.Name != right.Name {
				return left.Name < right.Name
			}
			return left.ID < right.ID
		})
	}
	return result
}

func projectSkillFiles(values []skills.FileInfo) []SkillFileDTO {
	result := make([]SkillFileDTO, 0, len(values))
	for _, value := range values {
		result = append(result, SkillFileDTO{
			Path:      value.Path,
			SizeBytes: value.SizeBytes,
			Text:      value.Text,
		})
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Path < result[j].Path
	})
	return result
}

func (s *SkillService) skillMutationTimeout() time.Duration {
	timeout := skillServiceInstallTimeout
	if s != nil && s.core != nil {
		if cfg := s.core.Config(); cfg != nil {
			configured := time.Duration(cfg.Runtime.Skills.DownloadTimeoutMS) * time.Millisecond
			if configured > timeout {
				timeout = configured + 5*time.Second
			}
		}
	}
	return timeout
}

func projectSkillSource(source skills.SourceInfo, known bool, currentIdentity string) SkillSourceDTO {
	if !known {
		return SkillSourceDTO{Known: false}
	}

	repository := ""
	if owner := strings.TrimSpace(source.Metadata["repository_owner"]); owner != "" {
		if name := strings.TrimSpace(source.Metadata["repository_name"]); name != "" {
			repository = owner + "/" + name
		}
	}
	if repository == "" {
		repository = strings.TrimSpace(source.Metadata["repository_path"])
	}
	if repository != "" {
		if host := strings.TrimSpace(source.Metadata["repository_host"]); host != "" && host != "gitlab.com" {
			repository = host + "/" + repository
		}
	}

	return SkillSourceDTO{
		Known:            true,
		Kind:             source.Kind,
		Provider:         source.Provider,
		DisplayURL:       sanitizeSkillSourceURL(source.OriginalURL),
		LocalDirectory:   source.LocalDirectory,
		SkillPath:        source.SkillPath,
		Repository:       repository,
		Ref:              strings.TrimSpace(source.Metadata["ref"]),
		InstalledAt:      formatSkillTime(source.InstalledAt),
		UpdatedAt:        formatSkillTime(source.UpdatedAt),
		RecordedIdentity: source.Identity,
		Drifted:          currentIdentity != "" && source.Identity != "" && source.Identity != currentIdentity,
	}
}

func sanitizeSkillSourceURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	parsed.User = nil
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String()
}

func formatSkillTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func projectSkillUpdateResult(value skills.UpdateResult) SkillUpdateResultDTO {
	return SkillUpdateResultDTO{
		Name:             value.Info.Name,
		Identity:         value.Info.Identity,
		PreviousIdentity: value.PreviousIdentity,
		Changed:          value.Changed,
		Source:           projectSkillSource(value.Source, true, value.Info.Identity),
	}
}

func (s *SkillService) validate() error {
	if s == nil || s.core == nil || s.core.Skills() == nil || s.core.Agents() == nil {
		return errors.New("SkillService 尚未正确初始化")
	}
	return nil
}

func projectSkillDTO(value skills.Info) SkillDTO {
	updatedAt := ""
	if !value.UpdatedAt.IsZero() {
		updatedAt = value.UpdatedAt.UTC().Format(time.RFC3339Nano)
	}
	return SkillDTO{
		Name:           value.Name,
		Description:    value.Description,
		SpecStatus:     string(value.SpecStatus),
		SpecMessage:    value.SpecMessage,
		License:        value.License,
		Compatibility:  value.Compatibility,
		Metadata:       cloneSkillStringMap(value.Metadata),
		AllowedTools:   value.AllowedTools,
		RuntimeStatus:  string(value.RuntimeStatus),
		RuntimeMessage: value.RuntimeMessage,
		Diagnostics:    projectSkillDiagnostics(value.Diagnostics),
		ScriptRuntimes: projectSkillScriptRuntimes(value.ScriptRuntimes),
		DirectoryName:  value.DirectoryName,
		RootDir:        value.RootDir,
		Identity:       value.Identity,
		Valid:          value.Valid,
		Error:          value.Error,
		FileCount:      value.FileCount,
		SizeBytes:      value.SizeBytes,
		HasReferences:  value.HasReferences,
		HasScripts:     value.HasScripts,
		HasAssets:      value.HasAssets,
		UpdatedAt:      updatedAt,
	}
}

func cloneSkillStringMap(value map[string]string) map[string]string {
	if len(value) == 0 {
		return nil
	}
	result := make(map[string]string, len(value))
	for key, item := range value {
		result[key] = item
	}
	return result
}

func projectSkillDiagnostics(values []skills.Diagnostic) []SkillDiagnosticDTO {
	if len(values) == 0 {
		return nil
	}
	result := make([]SkillDiagnosticDTO, 0, len(values))
	for _, value := range values {
		result = append(result, SkillDiagnosticDTO{
			Code:    value.Code,
			Level:   string(value.Level),
			Message: value.Message,
		})
	}
	return result
}

func projectSkillScriptRuntimes(values []skills.ScriptRuntime) []SkillScriptRuntimeDTO {
	if len(values) == 0 {
		return nil
	}
	result := make([]SkillScriptRuntimeDTO, 0, len(values))
	for _, value := range values {
		result = append(result, SkillScriptRuntimeDTO{
			Path:      value.Path,
			Language:  value.Language,
			Command:   value.Command,
			Available: value.Available,
			Supported: value.Supported,
			Message:   value.Message,
		})
	}
	return result
}

func projectSkillDiscovery(value skills.DiscoveryResult) SkillDiscoveryDTO {
	result := SkillDiscoveryDTO{
		SourceKind:    value.SourceKind,
		Provider:      value.Provider,
		DisplaySource: value.DisplaySource,
		Candidates:    make([]SkillDiscoveryCandidateDTO, 0, len(value.Candidates)),
	}
	for _, candidate := range value.Candidates {
		info := candidate.Info
		result.Candidates = append(result.Candidates, SkillDiscoveryCandidateDTO{
			Path:           candidate.Path,
			Name:           info.Name,
			Description:    info.Description,
			SpecStatus:     string(info.SpecStatus),
			SpecMessage:    info.SpecMessage,
			RuntimeStatus:  string(info.RuntimeStatus),
			RuntimeMessage: info.RuntimeMessage,
			Valid:          info.Valid,
			Error:          info.Error,
			Installed:      candidate.Installed,
			FileCount:      info.FileCount,
			SizeBytes:      info.SizeBytes,
			HasScripts:     info.HasScripts,
			HasReferences:  info.HasReferences,
			HasAssets:      info.HasAssets,
			Diagnostics:    projectSkillDiagnostics(info.Diagnostics),
			ScriptRuntimes: projectSkillScriptRuntimes(info.ScriptRuntimes),
		})
	}
	return result
}
