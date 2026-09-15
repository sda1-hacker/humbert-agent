package skills

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
)

// UpdateCheck 是一次显式来源检查的结果。
//
// UpdateAvailable 只比较 Package Identity，不依赖第三方 Registry 的版本号。这样没有 semver、
// tag 或 manifest 的普通 Git 仓库 Skill 也能可靠判断内容是否变化。
type UpdateCheck struct {
	Name string

	CurrentIdentity string

	CandidateIdentity string

	UpdateAvailable bool

	SourceDrifted bool

	CheckedAt time.Time

	Source SourceInfo
}

// UpdateResult 是 Update/Reinstall 原子替换结果。
type UpdateResult struct {
	Info Info

	PreviousIdentity string

	Changed bool

	Source SourceInfo
}

// installedState 是更新提交前捕获的当前安装状态。
//
// Valid=false 表示目标目录存在，但当前 Package 已损坏/不再通过严格校验。Reinstall/显式更换来源
// 允许以这种状态作为修复目标；Update/Check Update 则仍要求当前 Package 有效，避免把“修复”与
// “普通更新”混成一个隐式操作。
type installedState struct {
	Valid bool

	Package Package
}

func (s installedState) identity() string {
	if !s.Valid {
		return ""
	}
	return s.Package.Info.Identity
}

// CheckUpdate 从已记录来源重新准备一个候选 Package，并与当前安装 Identity 比较。
//
// 该方法只下载/读取/验证，不修改已安装 Package、Alias 或 Agent Profile。远程来源仍经过与
// Install 相同的 Resolver + SSRF + ZIP 安全链路；本地来源重新读取用户最初选择的目录。
// 当前 Package 已损坏时不会把 Check Update 当作修复入口，详情页应使用 Reinstall。
func (m *Manager) CheckUpdate(ctx context.Context, name string) (UpdateCheck, error) {
	current, source, operationCtx, cancel, err := m.prepareUpdateContext(ctx, name)
	if err != nil {
		return UpdateCheck{}, err
	}
	defer cancel()

	candidate, _, cleanup, err := m.preparePackageFromSource(operationCtx, source)
	if err != nil {
		return UpdateCheck{}, fmt.Errorf("检查 Skill %q 更新失败: %w", current.Info.Name, err)
	}
	defer cleanup()
	if candidate.Info.Name != current.Info.Name {
		return UpdateCheck{}, fmt.Errorf(
			"%w: 来源中的 Skill name 从 %q 变为 %q，拒绝自动替换",
			ErrInvalidSkill,
			current.Info.Name,
			candidate.Info.Name,
		)
	}

	return UpdateCheck{
		Name:              current.Info.Name,
		CurrentIdentity:   current.Info.Identity,
		CandidateIdentity: candidate.Info.Identity,
		UpdateAvailable:   candidate.Info.Identity != current.Info.Identity,
		SourceDrifted:     source.Identity != "" && source.Identity != current.Info.Identity,
		CheckedAt:         time.Now().UTC(),
		Source:            source.Clone(),
	}, nil
}

// Update 从已记录来源更新一个当前有效的 Skill；只有候选 Identity 变化时才替换 Package。
//
// 已损坏的 Package 必须通过 Reinstall 显式修复。这样控制面能清楚区分“正常升级”和“修复”。
func (m *Manager) Update(ctx context.Context, name string) (UpdateResult, error) {
	current, source, operationCtx, cancel, err := m.prepareUpdateContext(ctx, name)
	if err != nil {
		return UpdateResult{}, err
	}
	defer cancel()

	candidate, refreshedSource, cleanup, err := m.preparePackageFromSource(operationCtx, source)
	if err != nil {
		return UpdateResult{}, fmt.Errorf("准备 Skill %q 更新失败: %w", current.Info.Name, err)
	}
	defer cleanup()

	// 自动 Update 沿用第一次记录的来源建立时间；UpdatedAt 会在原子提交时刷新。
	refreshedSource.InstalledAt = source.InstalledAt
	return m.replaceValidatedPackage(
		operationCtx,
		current.Info.Name,
		installedState{Valid: true, Package: current},
		candidate,
		refreshedSource,
		false,
	)
}

// Reinstall 从已记录来源强制重新安装 Skill，即使当前 Package 已损坏或来源内容 Identity 没变化
// 也会执行一次原子替换。这是控制面修复 Invalid Skill 的首选路径，同时保留 Alias 与 Agent
// enabled_skills。
func (m *Manager) Reinstall(ctx context.Context, name string) (UpdateResult, error) {
	if err := m.validate(); err != nil {
		return UpdateResult{}, err
	}
	if ctx == nil {
		return UpdateResult{}, errors.New("context.Context 不能为空")
	}
	name, err := normalizeSkillName(name)
	if err != nil {
		return UpdateResult{}, err
	}

	current, err := m.captureInstalledState(ctx, name, true)
	if err != nil {
		return UpdateResult{}, err
	}
	source, exists, err := m.Source(ctx, name)
	if err != nil {
		return UpdateResult{}, err
	}
	if !exists {
		return UpdateResult{}, fmt.Errorf("%w: %s", ErrSkillSourceUnknown, name)
	}

	operationCtx, cancel := m.operationContextForSource(ctx, source)
	defer cancel()

	candidate, refreshedSource, cleanup, err := m.preparePackageFromSource(operationCtx, source)
	if err != nil {
		return UpdateResult{}, fmt.Errorf("准备 Skill %q 重新安装失败: %w", name, err)
	}
	defer cleanup()

	refreshedSource.InstalledAt = source.InstalledAt
	return m.replaceValidatedPackage(
		operationCtx,
		name,
		current,
		candidate,
		refreshedSource,
		true,
	)
}

// ReinstallFromURL 从用户新提供的远程来源原子替换现有 Skill，并建立/更换来源记录。
//
// 这既是旧版本安装包迁移到 Source Metadata 的入口，也是 Invalid Skill 没有可复用来源时的
// 修复入口。候选包的 canonical name 必须与安装目录名完全一致，避免一个 URL 操作悄悄把
// Agent 引用指向另一种能力。
func (m *Manager) ReinstallFromURL(
	ctx context.Context,
	name string,
	sourceURL string,
	skillPath string,
) (UpdateResult, error) {
	if err := m.validate(); err != nil {
		return UpdateResult{}, err
	}
	if ctx == nil {
		return UpdateResult{}, errors.New("context.Context 不能为空")
	}
	name, err := normalizeSkillName(name)
	if err != nil {
		return UpdateResult{}, err
	}
	current, err := m.captureInstalledState(ctx, name, true)
	if err != nil {
		return UpdateResult{}, err
	}

	timeout := time.Duration(m.config.DownloadTimeoutMS) * time.Millisecond
	operationCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	candidate, source, _, cleanup, err := m.prepareRemoteSkillPackage(operationCtx, sourceURL, skillPath)
	if err != nil {
		return UpdateResult{}, err
	}
	defer cleanup()
	return m.replaceValidatedPackage(
		operationCtx,
		name,
		current,
		candidate,
		source,
		true,
	)
}

// ReinstallFromDirectory 从用户重新选择的本地目录原子替换现有 Skill，并建立/更换来源记录。
// 当前安装即使已经损坏也允许修复，但新来源必须是完整有效且 canonical name 相同的 Package。
func (m *Manager) ReinstallFromDirectory(
	ctx context.Context,
	name string,
	sourceDirectory string,
) (UpdateResult, error) {
	if err := m.validate(); err != nil {
		return UpdateResult{}, err
	}
	if ctx == nil {
		return UpdateResult{}, errors.New("context.Context 不能为空")
	}
	name, err := normalizeSkillName(name)
	if err != nil {
		return UpdateResult{}, err
	}
	current, err := m.captureInstalledState(ctx, name, true)
	if err != nil {
		return UpdateResult{}, err
	}

	sourceDirectory = strings.TrimSpace(sourceDirectory)
	if sourceDirectory == "" {
		return UpdateResult{}, errors.New("Skill 源目录不能为空")
	}
	candidate, err := inspectPackage(ctx, sourceDirectory, m.config, false)
	if err != nil {
		return UpdateResult{}, fmt.Errorf("验证待重新安装 Skill 失败: %w", err)
	}
	source, err := localSourceInfo(sourceDirectory, candidate.Info.Identity, time.Now())
	if err != nil {
		return UpdateResult{}, err
	}
	return m.replaceValidatedPackage(
		ctx,
		name,
		current,
		candidate,
		source,
		true,
	)
}

func (m *Manager) prepareUpdateContext(
	ctx context.Context,
	name string,
) (Package, SourceInfo, context.Context, context.CancelFunc, error) {
	if err := m.validate(); err != nil {
		return Package{}, SourceInfo{}, nil, func() {}, err
	}
	if ctx == nil {
		return Package{}, SourceInfo{}, nil, func() {}, errors.New("context.Context 不能为空")
	}
	name, err := normalizeSkillName(name)
	if err != nil {
		return Package{}, SourceInfo{}, nil, func() {}, err
	}
	current, err := m.Get(ctx, name)
	if err != nil {
		return Package{}, SourceInfo{}, nil, func() {}, err
	}
	source, exists, err := m.Source(ctx, name)
	if err != nil {
		return Package{}, SourceInfo{}, nil, func() {}, err
	}
	if !exists {
		return Package{}, SourceInfo{}, nil, func() {}, fmt.Errorf("%w: %s", ErrSkillSourceUnknown, name)
	}

	operationCtx, cancel := m.operationContextForSource(ctx, source)
	return current, source, operationCtx, cancel, nil
}

func (m *Manager) operationContextForSource(
	ctx context.Context,
	source SourceInfo,
) (context.Context, context.CancelFunc) {
	if source.Kind == SkillSourceKindRemote {
		timeout := time.Duration(m.config.DownloadTimeoutMS) * time.Millisecond
		return context.WithTimeout(ctx, timeout)
	}
	return context.WithCancel(ctx)
}

// captureInstalledState 捕获一次更新/修复开始时的当前安装状态，用于提交阶段检测并发变化。
//
// allowInvalid=true 只允许“目标路径存在但 Package 无效”的状态，不会把完全不存在的 Skill
// 当作可修复对象，也不会接受普通文件。Root 直属 symlink 可以作为 Invalid 目标被原子隔离替换，
// 过程中不会跟随链接到 Root 外部。
func (m *Manager) captureInstalledState(
	ctx context.Context,
	name string,
	allowInvalid bool,
) (installedState, error) {
	if ctx == nil {
		return installedState{}, errors.New("context.Context 不能为空")
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.installedStateLocked(ctx, name, allowInvalid)
}

func (m *Manager) installedStateLocked(
	ctx context.Context,
	name string,
	allowInvalid bool,
) (installedState, error) {
	if err := ctx.Err(); err != nil {
		return installedState{}, fmt.Errorf("读取 Skill %q 被取消: %w", name, err)
	}

	target := filepath.Join(m.rootDir, name)
	pkg, inspectErr := inspectPackage(ctx, target, m.config, true)
	if inspectErr == nil {
		return installedState{Valid: true, Package: pkg}, nil
	}
	if errors.Is(inspectErr, ErrSkillNotFound) || errors.Is(inspectErr, os.ErrNotExist) {
		return installedState{}, fmt.Errorf("%w: %s", ErrSkillNotFound, name)
	}
	if !allowInvalid {
		return installedState{}, fmt.Errorf("读取 Skill %q 失败: %w", name, inspectErr)
	}

	info, err := os.Lstat(target)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return installedState{}, fmt.Errorf("%w: %s", ErrSkillNotFound, name)
		}
		return installedState{}, fmt.Errorf("读取 Skill %q 安装路径失败: %w", name, err)
	}
	if info.Mode()&os.ModeSymlink == 0 && !info.IsDir() {
		return installedState{}, fmt.Errorf(
			"%w: Skill %q 安装路径既不是目录也不是符号链接",
			ErrInvalidSkill,
			name,
		)
	}
	return installedState{Valid: false}, nil
}

func installedStateMatches(expected installedState, current installedState) bool {
	if expected.Valid != current.Valid {
		return false
	}
	if !expected.Valid {
		return true
	}
	return expected.Package.Info.Identity == current.Package.Info.Identity
}

func (m *Manager) preparePackageFromSource(
	ctx context.Context,
	source SourceInfo,
) (Package, SourceInfo, func(), error) {
	switch source.Kind {
	case SkillSourceKindLocal:
		pkg, err := inspectPackage(ctx, source.LocalDirectory, m.config, false)
		if err != nil {
			return Package{}, SourceInfo{}, func() {}, fmt.Errorf("读取本地 Skill 来源失败: %w", err)
		}
		refreshed, err := localSourceInfo(source.LocalDirectory, pkg.Info.Identity, time.Now())
		if err != nil {
			return Package{}, SourceInfo{}, func() {}, err
		}
		return pkg, refreshed, func() {}, nil
	case SkillSourceKindRemote:
		pkg, refreshed, _, cleanup, err := m.prepareRemoteSkillPackage(
			ctx,
			source.OriginalURL,
			source.SkillPath,
		)
		if err != nil {
			return Package{}, SourceInfo{}, func() {}, err
		}
		// Update/Reinstall 必须继续使用第一次记录的 Provider 语义。以后注册新的高优先级
		// Resolver 时，同一个 URL 可能被另一个 Provider 匹配；自动操作不能因为 Registry
		// 变化就悄悄切换来源。用户若确实要更换 Provider，应通过 ReinstallFromURL 显式确认。
		if source.Provider != "" && !strings.EqualFold(source.Provider, refreshed.Provider) {
			cleanup()
			return Package{}, SourceInfo{}, func() {}, fmt.Errorf(
				"%w: 记录的 Provider=%q，但当前 URL 解析为 %q；请显式更换来源",
				ErrInvalidSkill,
				source.Provider,
				refreshed.Provider,
			)
		}
		return pkg, refreshed, cleanup, nil
	default:
		return Package{}, SourceInfo{}, func() {}, fmt.Errorf("%w: 未知来源 kind=%q", ErrInvalidSkill, source.Kind)
	}
}

func (m *Manager) replaceValidatedPackage(
	ctx context.Context,
	name string,
	expected installedState,
	candidate Package,
	source SourceInfo,
	force bool,
) (UpdateResult, error) {
	if candidate.Info.Name != name {
		return UpdateResult{}, fmt.Errorf(
			"%w: 来源中的 Skill name=%q，与当前 %q 不一致",
			ErrInvalidSkill,
			candidate.Info.Name,
			name,
		)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	current, err := m.installedStateLocked(ctx, name, true)
	if err != nil {
		return UpdateResult{}, err
	}
	if !installedStateMatches(expected, current) {
		return UpdateResult{}, fmt.Errorf(
			"%w: Skill %q 在更新准备期间已发生变化，请重新检查",
			ErrSkillSnapshotStale,
			name,
		)
	}

	previousIdentity := current.identity()
	sources, err := m.readSourcesLocked(ctx)
	if err != nil {
		return UpdateResult{}, err
	}
	if !force && current.Valid && candidate.Info.Identity == previousIdentity {
		currentSource := source
		if stored, ok := sources.Skills[name]; ok {
			currentSource = stored
		}
		return UpdateResult{
			Info:             current.Package.Info,
			PreviousIdentity: previousIdentity,
			Changed:          false,
			Source:           currentSource.Clone(),
		}, nil
	}

	stage := filepath.Join(m.rootDir, ".humbert-update-"+uuid.NewString())
	if err := os.Mkdir(stage, 0o700); err != nil {
		return UpdateResult{}, fmt.Errorf("创建 Skill 更新临时目录失败: %w", err)
	}
	stageExists := true
	defer func() {
		if stageExists {
			_ = os.RemoveAll(stage)
		}
	}()

	if err := copyPackageTree(ctx, candidate.Info.RootDir, stage); err != nil {
		return UpdateResult{}, fmt.Errorf("复制 Skill 更新 Package 失败: %w", err)
	}
	staged, err := inspectPackage(ctx, stage, m.config, false)
	if err != nil {
		return UpdateResult{}, fmt.Errorf("复核 Skill 更新 Package 失败: %w", err)
	}
	if staged.Info.Name != name || staged.Info.Identity != candidate.Info.Identity {
		return UpdateResult{}, fmt.Errorf("%w: Skill 更新来源在复制过程中发生变化", ErrSkillSnapshotStale)
	}

	target := filepath.Join(m.rootDir, name)
	backup := filepath.Join(m.rootDir, ".humbert-backup-"+uuid.NewString())
	if err := os.Rename(target, backup); err != nil {
		return UpdateResult{}, fmt.Errorf("备份当前 Skill Package 失败: %w", err)
	}
	backupExists := true
	rollback := func(cause error) error {
		var rollbackErrors []error
		if err := os.RemoveAll(target); err != nil && !errors.Is(err, os.ErrNotExist) {
			rollbackErrors = append(rollbackErrors, fmt.Errorf("清理新 Skill Package 失败: %w", err))
		}
		if backupExists {
			if err := os.Rename(backup, target); err != nil {
				rollbackErrors = append(rollbackErrors, fmt.Errorf("恢复旧 Skill Package 失败: %w", err))
			} else {
				backupExists = false
			}
		}
		if len(rollbackErrors) > 0 {
			return errors.Join(append([]error{cause}, rollbackErrors...)...)
		}
		return cause
	}

	if err := os.Rename(stage, target); err != nil {
		return UpdateResult{}, rollback(fmt.Errorf("提交新 Skill Package 失败: %w", err))
	}
	stageExists = false

	installed, err := inspectPackage(ctx, target, m.config, true)
	if err != nil {
		return UpdateResult{}, rollback(fmt.Errorf("验证更新后的 Skill Package 失败: %w", err))
	}

	now := time.Now().UTC()
	if source.InstalledAt.IsZero() {
		source.InstalledAt = now
	}
	source.UpdatedAt = now
	source.Identity = installed.Info.Identity
	source, err = normalizeStoredSourceInfo(source)
	if err != nil {
		return UpdateResult{}, rollback(fmt.Errorf("准备更新后的 Skill 来源记录失败: %w", err))
	}

	previousSources := cloneSkillSourcesDocument(sources)
	sources.Skills[name] = source
	if err := m.writeSourcesLocked(ctx, sources); err != nil {
		rollbackErr := rollback(fmt.Errorf("保存更新后的 Skill 来源记录失败: %w", err))
		// AtomicFile 理论上要么保留旧文件、要么提交新文件；若错误发生在 rename 之后，
		// 这里仍尽力恢复旧来源文档，使 Package 与 provenance 保持一致。
		if restoreErr := m.writeSourcesLocked(context.Background(), previousSources); restoreErr != nil {
			rollbackErr = errors.Join(rollbackErr, fmt.Errorf("恢复旧 Skill 来源记录失败: %w", restoreErr))
		}
		return UpdateResult{}, rollbackErr
	}

	if backupExists {
		if err := os.RemoveAll(backup); err != nil {
			m.logger.Warn(
				context.Background(),
				"Skill 已更新，但清理旧 Package 备份失败",
				"operation", "skill.update.cleanup",
				"skill_name", name,
				"error", err,
			)
		} else {
			backupExists = false
		}
	}

	m.logger.Info(
		ctx,
		"Skill Package 已原子更新",
		"operation", "skill.update",
		"skill_name", name,
		"source_provider", source.Provider,
		"previous_valid", current.Valid,
		"previous_identity", previousIdentity,
		"identity", installed.Info.Identity,
		"changed", !current.Valid || installed.Info.Identity != previousIdentity,
		"forced", force,
	)
	return UpdateResult{
		Info:             installed.Info,
		PreviousIdentity: previousIdentity,
		Changed:          !current.Valid || installed.Info.Identity != previousIdentity,
		Source:           source.Clone(),
	}, nil
}

func cloneSkillSourcesDocument(document skillSourcesDocument) skillSourcesDocument {
	cloned := skillSourcesDocument{
		SchemaVersion: document.SchemaVersion,
		Skills:        make(map[string]SourceInfo, len(document.Skills)),
	}
	for name, source := range document.Skills {
		cloned.Skills[name] = source.Clone()
	}
	return cloned
}
