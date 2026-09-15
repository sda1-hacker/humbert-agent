package skills

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/google/uuid"
)

// InstallFromDirectory 验证并复制一个用户选择的本地 Skill Package。
//
// 安装采用“Skills Root 内临时目录 + rename”提交：只有整个包复制、校验以及 Humbert 来源
// 元数据落盘都成功后才会把安装视为完成。来源记录位于 Skills Root 的内部 JSON，不写入第三方
// Package，因此不会改变 Package Identity。源目录被视为不可信：symlink、设备文件、路径穿越
// 和超限文件都会被拒绝。
func (m *Manager) InstallFromDirectory(ctx context.Context, sourceDirectory string) (Info, error) {
	if err := m.validate(); err != nil {
		return Info{}, err
	}
	if ctx == nil {
		return Info{}, errors.New("context.Context 不能为空")
	}
	if err := ctx.Err(); err != nil {
		return Info{}, fmt.Errorf("安装 Skill 被取消: %w", err)
	}

	sourceDirectory = strings.TrimSpace(sourceDirectory)
	if sourceDirectory == "" {
		return Info{}, errors.New("Skill 源目录不能为空")
	}

	// 外部源目录名不要求与 frontmatter.name 一致；最终安装目录会统一使用经过验证的 name。
	sourcePackage, err := inspectPackage(ctx, sourceDirectory, m.config, false)
	if err != nil {
		return Info{}, fmt.Errorf("验证待安装 Skill 失败: %w", err)
	}
	source, err := localSourceInfo(sourceDirectory, sourcePackage.Info.Identity, time.Now())
	if err != nil {
		return Info{}, err
	}

	installed, err := m.installValidatedPackage(ctx, sourcePackage, source)
	if err != nil {
		return Info{}, err
	}

	m.logger.Info(
		ctx,
		"本地 Skill 已安装",
		"operation", "skill.install",
		"skill_name", installed.Name,
		"file_count", installed.FileCount,
		"size_bytes", installed.SizeBytes,
	)
	return installed, nil
}

// installValidatedPackage 把已经在 Skills Root 外完成第一次验证的 Package 原子提交到 Root。
//
// sourcePackage 在复制后会再次按 Identity 复核，防止源目录在安装过程中发生变化。来源文档写入
// 失败时会删除刚提交的 target，因此调用方不会看到“安装成功但没有来源元数据”的半完成状态。
func (m *Manager) installValidatedPackage(
	ctx context.Context,
	sourcePackage Package,
	source SourceInfo,
) (Info, error) {
	name := sourcePackage.Info.Name
	if name == "" {
		return Info{}, fmt.Errorf("%w: 待安装 Skill 缺少 canonical name", ErrInvalidSkill)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// 在修改 Package 前先读取来源文档。如果用户手工破坏了内部来源文件，安装会明确失败，
	// 而不是先提交目录再发现无法记录可更新来源。
	sources, err := m.readSourcesLocked(ctx)
	if err != nil {
		return Info{}, err
	}

	target := filepath.Join(m.rootDir, name)
	if _, err := os.Lstat(target); err == nil {
		return Info{}, fmt.Errorf("%w: %s", ErrSkillExists, name)
	} else if !errors.Is(err, os.ErrNotExist) {
		return Info{}, fmt.Errorf("检查 Skill 安装目标失败: %w", err)
	}

	tempDir := filepath.Join(m.rootDir, ".humbert-install-"+uuid.NewString())
	if err := os.Mkdir(tempDir, 0o700); err != nil {
		return Info{}, fmt.Errorf("创建 Skill 安装临时目录失败: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = os.RemoveAll(tempDir)
		}
	}()

	if err := copyPackageTree(ctx, sourcePackage.Info.RootDir, tempDir); err != nil {
		return Info{}, fmt.Errorf("复制 Skill Package 失败: %w", err)
	}

	// 临时目录 basename 与 Skill name 不同，因此先用宽松模式复核内容身份；rename 后再做
	// 严格目录名检查。两次验证能够发现复制过程中的源文件变化或短写。
	copied, err := inspectPackage(ctx, tempDir, m.config, false)
	if err != nil {
		return Info{}, fmt.Errorf("复核已复制 Skill Package 失败: %w", err)
	}
	if copied.Info.Name != name || copied.Info.Identity != sourcePackage.Info.Identity {
		return Info{}, fmt.Errorf("%w: Skill 源目录在安装过程中发生变化", ErrSkillSnapshotStale)
	}

	if err := os.Rename(tempDir, target); err != nil {
		return Info{}, fmt.Errorf("提交 Skill 安装目录失败: %w", err)
	}
	committed = true

	installed, err := inspectPackage(ctx, target, m.config, true)
	if err != nil {
		_ = os.RemoveAll(target)
		return Info{}, fmt.Errorf("验证已安装 Skill 失败: %w", err)
	}

	source.Identity = installed.Info.Identity
	if source.InstalledAt.IsZero() {
		source.InstalledAt = time.Now().UTC()
	}
	if source.UpdatedAt.IsZero() {
		source.UpdatedAt = source.InstalledAt
	}
	source, err = normalizeStoredSourceInfo(source)
	if err != nil {
		_ = os.RemoveAll(target)
		return Info{}, fmt.Errorf("准备 Skill 来源记录失败: %w", err)
	}
	sources.Skills[name] = source
	if err := m.writeSourcesLocked(ctx, sources); err != nil {
		removeErr := os.RemoveAll(target)
		if removeErr != nil {
			return Info{}, errors.Join(
				fmt.Errorf("Skill 来源记录保存失败: %w", err),
				fmt.Errorf("回滚已安装 Skill 失败: %w", removeErr),
			)
		}
		return Info{}, fmt.Errorf("Skill 来源记录保存失败，安装已回滚: %w", err)
	}

	return installed.Info, nil
}

// Remove 删除一个已安装 Skill Package。
//
// Manager 只负责物理删除。是否仍被 Agent Profile 引用由上层 SkillService 在调用前检查；
// Runtime 已经创建的 Skill Snapshot 不受删除影响，SKILL.md 正文仍在内存，但如果之后读取
// 资源文件会得到 snapshot stale/not found，而不会越界读取其他路径。
func (m *Manager) Remove(ctx context.Context, name string) error {
	if err := m.validate(); err != nil {
		return err
	}
	if ctx == nil {
		return errors.New("context.Context 不能为空")
	}
	name, err := normalizeInstalledDirectoryName(name)
	if err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	target := filepath.Join(m.rootDir, name)
	info, err := os.Lstat(target)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("%w: %s", ErrSkillNotFound, name)
		}
		return fmt.Errorf("读取 Skill 目录失败: %w", err)
	}
	if info.Mode()&os.ModeSymlink == 0 && !info.IsDir() {
		return fmt.Errorf("%w: Skill 安装路径既不是目录也不是符号链接", ErrInvalidSkill)
	}

	// 无效 Skill 也必须能够从设置页清理。对于 Root 直属层级的 symlink，这里只 rename
	// 链接本身，再删除隔离后的链接；os.Rename/os.RemoveAll 都不会跟随 symlink 到目标路径。
	// 这样既允许用户修复“可见但删不掉”的坏包，也不会越过 Skills Root 删除外部内容。
	// 对真实目录仍保持原有“同 Root 隔离后再删除”的原子可见性。
	quarantine := filepath.Join(m.rootDir, ".humbert-delete-"+uuid.NewString())
	if err := os.Rename(target, quarantine); err != nil {
		return fmt.Errorf("隔离待删除 Skill 失败: %w", err)
	}
	if err := os.RemoveAll(quarantine); err != nil {
		// 删除失败时尽力恢复原路径；恢复失败会把两个错误一起返回。
		restoreErr := os.Rename(quarantine, target)
		if restoreErr != nil {
			return errors.Join(
				fmt.Errorf("删除 Skill %q 失败: %w", name, err),
				fmt.Errorf("恢复 Skill 目录失败: %w", restoreErr),
			)
		}
		return fmt.Errorf("删除 Skill %q 失败: %w", name, err)
	}

	// Alias 属于 Humbert 用户自己的展示覆盖，不属于 Package。Package 删除成功后做
	// best-effort 清理；即使展示元数据损坏，也不能把已经成功完成的物理删除伪装成失败。
	if aliasErr := m.removeAliasLocked(context.Background(), name); aliasErr != nil {
		m.logger.Warn(
			context.Background(),
			"Skill 已删除，但清理展示 Alias 失败",
			"operation", "skill.alias.cleanup",
			"skill_name", name,
			"error", aliasErr,
		)
	}
	if sourceErr := m.removeSourceLocked(context.Background(), name); sourceErr != nil {
		m.logger.Warn(
			context.Background(),
			"Skill 已删除，但清理来源记录失败",
			"operation", "skill.source.cleanup",
			"skill_name", name,
			"error", sourceErr,
		)
	}

	m.logger.Info(ctx, "本地 Skill 已删除", "operation", "skill.remove", "skill_name", name)
	return nil
}

func copyPackageTree(ctx context.Context, source string, destination string) error {
	return filepath.WalkDir(source, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}

		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		relativeSlash := filepath.ToSlash(relative)
		if relativeSlash == "." {
			return nil
		}
		if shouldIgnorePackagePath(relativeSlash, entry.IsDir()) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%w: Skill 包不能包含符号链接: %s", ErrInvalidSkill, relativeSlash)
		}

		target := filepath.Join(destination, relative)
		if entry.IsDir() {
			if err := os.Mkdir(target, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
				return err
			}
			return nil
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("%w: Skill 包只能包含普通文件: %s", ErrInvalidSkill, relativeSlash)
		}
		return copyRegularFile(ctx, path, target)
	})
}

func copyRegularFile(ctx context.Context, source string, destination string) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()

	output, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	closed := false
	defer func() {
		if !closed {
			_ = output.Close()
		}
	}()

	buffer := make([]byte, 64*1024)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		read, readErr := input.Read(buffer)
		if read > 0 {
			written := 0
			for written < read {
				n, writeErr := output.Write(buffer[written:read])
				if writeErr != nil {
					return writeErr
				}
				if n <= 0 {
					return io.ErrShortWrite
				}
				written += n
			}
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return readErr
		}
	}

	if err := output.Sync(); err != nil {
		return err
	}
	if err := output.Close(); err != nil {
		return err
	}
	closed = true
	if runtime.GOOS != "windows" {
		if err := os.Chmod(destination, 0o600); err != nil {
			return err
		}
	}
	return nil
}

type validatedInstallRequest struct {
	Package Package
	Source  SourceInfo
}

// installValidatedPackages 原子提交一组已经验证的 Skill Package。
//
// 所有目标、临时副本、Identity 与来源记录都会先完成校验，再统一切换到 Skills Root。
// 任一步失败都会回滚本批次已经提交的目录，避免多选安装留下“前几个成功、后几个失败”的
// 半完成状态。
func (m *Manager) installValidatedPackages(
	ctx context.Context,
	requests []validatedInstallRequest,
) ([]Info, error) {
	if len(requests) == 0 {
		return nil, errors.New("没有选择要安装的 Skill")
	}
	if ctx == nil {
		return nil, errors.New("context.Context 不能为空")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	sources, err := m.readSourcesLocked(ctx)
	if err != nil {
		return nil, err
	}

	names := make(map[string]struct{}, len(requests))
	for _, request := range requests {
		name := strings.TrimSpace(request.Package.Info.Name)
		if name == "" {
			return nil, fmt.Errorf("%w: 待安装 Skill 缺少 canonical name", ErrInvalidSkill)
		}
		if _, duplicate := names[name]; duplicate {
			return nil, fmt.Errorf("%w: 本次安装包含重复 Skill %q", ErrInvalidSkill, name)
		}
		names[name] = struct{}{}
		target := filepath.Join(m.rootDir, name)
		if _, statErr := os.Lstat(target); statErr == nil {
			return nil, fmt.Errorf("%w: %s", ErrSkillExists, name)
		} else if !errors.Is(statErr, os.ErrNotExist) {
			return nil, fmt.Errorf("检查 Skill %q 安装目标失败: %w", name, statErr)
		}
	}

	type stagedInstall struct {
		request validatedInstallRequest
		tempDir string
		target  string
	}
	staged := make([]stagedInstall, 0, len(requests))
	cleanupTemps := func() {
		for _, item := range staged {
			if item.tempDir != "" {
				_ = os.RemoveAll(item.tempDir)
			}
		}
	}
	defer cleanupTemps()

	for _, request := range requests {
		name := request.Package.Info.Name
		tempDir := filepath.Join(m.rootDir, ".humbert-install-"+uuid.NewString())
		if err := os.Mkdir(tempDir, 0o700); err != nil {
			return nil, fmt.Errorf("创建 Skill %q 安装临时目录失败: %w", name, err)
		}
		item := stagedInstall{
			request: request,
			tempDir: tempDir,
			target:  filepath.Join(m.rootDir, name),
		}
		staged = append(staged, item)

		if err := copyPackageTree(ctx, request.Package.Info.RootDir, tempDir); err != nil {
			return nil, fmt.Errorf("复制 Skill %q 失败: %w", name, err)
		}
		copied, err := inspectPackage(ctx, tempDir, m.config, false)
		if err != nil {
			return nil, fmt.Errorf("复核 Skill %q 临时副本失败: %w", name, err)
		}
		if copied.Info.Name != name || copied.Info.Identity != request.Package.Info.Identity {
			return nil, fmt.Errorf("%w: Skill %q 来源在安装过程中发生变化", ErrSkillSnapshotStale, name)
		}
	}

	committed := make([]string, 0, len(staged))
	rollback := func(cause error) error {
		errorsList := []error{cause}
		for index := len(committed) - 1; index >= 0; index-- {
			if removeErr := os.RemoveAll(committed[index]); removeErr != nil {
				errorsList = append(errorsList, fmt.Errorf("回滚 %s 失败: %w", committed[index], removeErr))
			}
		}
		return errors.Join(errorsList...)
	}

	installed := make([]Info, 0, len(staged))
	now := time.Now().UTC()
	for index := range staged {
		item := &staged[index]
		if err := os.Rename(item.tempDir, item.target); err != nil {
			return nil, rollback(fmt.Errorf("提交 Skill %q 失败: %w", item.request.Package.Info.Name, err))
		}
		item.tempDir = ""
		committed = append(committed, item.target)

		pkg, err := inspectPackage(ctx, item.target, m.config, true)
		if err != nil {
			return nil, rollback(fmt.Errorf("验证已安装 Skill %q 失败: %w", item.request.Package.Info.Name, err))
		}
		source := item.request.Source
		source.Identity = pkg.Info.Identity
		if source.InstalledAt.IsZero() {
			source.InstalledAt = now
		}
		if source.UpdatedAt.IsZero() {
			source.UpdatedAt = source.InstalledAt
		}
		source, err = normalizeStoredSourceInfo(source)
		if err != nil {
			return nil, rollback(fmt.Errorf("准备 Skill %q 来源记录失败: %w", pkg.Info.Name, err))
		}
		sources.Skills[pkg.Info.Name] = source
		installed = append(installed, pkg.Info)
	}

	if err := m.writeSourcesLocked(ctx, sources); err != nil {
		return nil, rollback(fmt.Errorf("Skill 来源记录保存失败，批量安装已回滚: %w", err))
	}

	return installed, nil
}

// StagePackage 把一个已安装 Skill 的冻结版本复制到调用方提供的临时目录。
//
// 该方法用于 run_skill_script：真实安装目录永远不会直接作为脚本工作目录开放。expectedIdentity
// 来自当前 Turn 的 Tool Snapshot；如果用户在 Turn 运行期间更新了 Skill，Stage 会返回 stale，
// 而不是把新旧版本混在一次执行里。destination 必须不存在。
func (m *Manager) StagePackage(
	ctx context.Context,
	name string,
	expectedIdentity string,
	destination string,
) (Info, error) {
	if err := m.validate(); err != nil {
		return Info{}, err
	}
	if ctx == nil {
		return Info{}, errors.New("context.Context 不能为空")
	}
	name, err := normalizeSkillName(name)
	if err != nil {
		return Info{}, err
	}
	expectedIdentity = strings.TrimSpace(expectedIdentity)
	if expectedIdentity == "" {
		return Info{}, errors.New("Stage Skill 缺少 expected identity")
	}
	destination = strings.TrimSpace(destination)
	if destination == "" {
		return Info{}, errors.New("Stage Skill 目标目录不能为空")
	}
	if _, err := os.Lstat(destination); err == nil {
		return Info{}, fmt.Errorf("Stage Skill 目标已经存在: %s", destination)
	} else if !errors.Is(err, os.ErrNotExist) {
		return Info{}, fmt.Errorf("检查 Stage Skill 目标失败: %w", err)
	}

	m.mu.RLock()
	defer m.mu.RUnlock()
	pkg, err := m.getLocked(ctx, name)
	if err != nil {
		return Info{}, err
	}
	if pkg.Info.Identity != expectedIdentity {
		return Info{}, fmt.Errorf("%w: Skill %q Identity 已从 %s 变为 %s", ErrSkillSnapshotStale, name, expectedIdentity, pkg.Info.Identity)
	}
	if err := os.Mkdir(destination, 0o700); err != nil {
		return Info{}, fmt.Errorf("创建 Skill Stage 目录失败: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = os.RemoveAll(destination)
		}
	}()
	if err := copyPackageTree(ctx, pkg.Info.RootDir, destination); err != nil {
		return Info{}, fmt.Errorf("Stage Skill %q 失败: %w", name, err)
	}
	staged, err := inspectPackage(ctx, destination, m.config, false)
	if err != nil {
		return Info{}, fmt.Errorf("复核 Stage Skill %q 失败: %w", name, err)
	}
	if staged.Info.Name != name || staged.Info.Identity != expectedIdentity {
		return Info{}, fmt.Errorf("%w: Stage Skill %q 内容与当前 Turn Snapshot 不一致", ErrSkillSnapshotStale, name)
	}
	committed = true
	return staged.Info, nil
}
