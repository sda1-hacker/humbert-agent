package skills

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	"github.com/sda1-hacker/humbert-agent/internal/config"
)

// packageStage 只拥有尚未发布的副本。发布后的目录由安装/替换事务负责回滚。
type packageStage struct {
	directory string
	target    string
}

func (m *Manager) preparePackage(ctx context.Context, pkg Package) (*packageStage, error) {
	destination := filepath.Join(m.rootDir, ".humbert-install-"+uuid.NewString())
	if _, err := copyVerifiedPackage(ctx, pkg, destination, m.config); err != nil {
		return nil, err
	}
	return &packageStage{directory: destination}, nil
}

func copyVerifiedPackage(ctx context.Context, pkg Package, destination string, cfg config.SkillConfig) (_ Package, err error) {
	if err = ctx.Err(); err != nil {
		return Package{}, err
	}
	if err = os.Mkdir(destination, 0o700); err != nil {
		return Package{}, fmt.Errorf("创建 Skill 暂存目录失败: %w", err)
	}
	defer func() {
		if err != nil {
			_ = os.RemoveAll(destination)
		}
	}()
	if err = copyPackageTree(ctx, pkg.Info.RootDir, destination); err != nil {
		return Package{}, fmt.Errorf("复制 Skill %q 失败: %w", pkg.Info.Name, err)
	}
	copied, err := inspectPackage(ctx, destination, cfg, false)
	if err != nil {
		return Package{}, err
	}
	if copied.Info.Name != pkg.Info.Name || copied.Info.Identity != pkg.Info.Identity {
		return Package{}, fmt.Errorf("%w: Skill %q 来源在复制过程中发生变化", ErrSkillSnapshotStale, pkg.Info.Name)
	}
	return copied, nil
}

func (s *packageStage) cleanup() {
	if s.directory != "" {
		_ = os.RemoveAll(s.directory)
	}
}

func (s *packageStage) commit(ctx context.Context, target string, cfg config.SkillConfig) (Package, error) {
	if err := ctx.Err(); err != nil {
		return Package{}, err
	}
	if err := os.Rename(s.directory, target); err != nil {
		return Package{}, fmt.Errorf("提交 Skill 目录失败: %w", err)
	}
	s.directory, s.target = "", target
	return inspectPackage(ctx, target, cfg, true)
}

func installedSource(source SourceInfo, identity string, now time.Time) (SourceInfo, error) {
	source.Identity = identity
	if source.InstalledAt.IsZero() {
		source.InstalledAt = now
	}
	if source.UpdatedAt.IsZero() {
		source.UpdatedAt = source.InstalledAt
	}
	return normalizeStoredSourceInfo(source)
}
