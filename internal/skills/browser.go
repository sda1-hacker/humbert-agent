package skills

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"unicode/utf8"
)

// ReadTextFile 为 Humbert 的 Skill 详情页读取一个已经安装、已经验证的包内文本文件。
//
// 这个方法只用于“查看 Skill 内容”，不参与 Runtime Snapshot，也不会执行 scripts/ 下的任何
// 文件。读取仍然复用 Skill Package 的 canonical name、相对路径规范、symlink 防护以及安装
// 扫描时记录的 SHA256；因此 UI 不能借这个接口越过 Skills Root，也不会在包被外部进程修改
// 后静默展示与当前 Catalog 身份不一致的内容。
func (m *Manager) ReadTextFile(
	ctx context.Context,
	name string,
	requested string,
) (string, FileInfo, error) {
	if err := m.validate(); err != nil {
		return "", FileInfo{}, err
	}
	if ctx == nil {
		return "", FileInfo{}, errors.New("context.Context 不能为空")
	}
	if err := ctx.Err(); err != nil {
		return "", FileInfo{}, fmt.Errorf("读取 Skill 文件被取消: %w", err)
	}

	name, err := normalizeSkillName(name)
	if err != nil {
		return "", FileInfo{}, err
	}
	relative, err := normalizeAssetPath(requested)
	if err != nil {
		return "", FileInfo{}, err
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	pkg, err := m.getLocked(ctx, name)
	if err != nil {
		return "", FileInfo{}, err
	}

	var expected *FileInfo
	for index := range pkg.Files {
		if pkg.Files[index].Path == relative {
			copyInfo := pkg.Files[index]
			expected = &copyInfo
			break
		}
	}
	if expected == nil {
		return "", FileInfo{}, fmt.Errorf("%w: %s/%s", ErrSkillAssetNotFound, name, relative)
	}
	if !expected.Text {
		return "", FileInfo{}, fmt.Errorf("%w: %s/%s", ErrSkillAssetBinary, name, relative)
	}

	limit := m.config.MaxAssetBytes
	if relative == SkillDefinitionFileName {
		limit = m.config.MaxDefinitionBytes
	}
	if expected.SizeBytes > limit {
		return "", FileInfo{}, fmt.Errorf("%w: Skill 文件超过预览大小上限", ErrInvalidSkill)
	}

	if err := validateAssetPathComponents(pkg.Info.RootDir, relative); err != nil {
		return "", FileInfo{}, err
	}
	fullPath := filepath.Join(pkg.Info.RootDir, filepath.FromSlash(relative))
	data, err := os.ReadFile(fullPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", FileInfo{}, fmt.Errorf("%w: %s/%s", ErrSkillSnapshotStale, name, relative)
		}
		return "", FileInfo{}, fmt.Errorf("读取 Skill 文件失败: %w", err)
	}
	if int64(len(data)) != expected.SizeBytes {
		return "", FileInfo{}, fmt.Errorf("%w: Skill 文件大小已变化", ErrSkillSnapshotStale)
	}
	digest := sha256.Sum256(data)
	if hex.EncodeToString(digest[:]) != expected.SHA256 {
		return "", FileInfo{}, fmt.Errorf("%w: Skill 文件内容已变化", ErrSkillSnapshotStale)
	}
	if !utf8.Valid(data) || bytes.IndexByte(data, 0) >= 0 {
		return "", FileInfo{}, fmt.Errorf("%w: %s/%s", ErrSkillAssetBinary, name, relative)
	}

	return string(data), *expected, nil
}
