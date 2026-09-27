package searchindex

import (
	"context"
	"io"
	"io/fs"
	"path/filepath"
	"strings"

	"github.com/sda1-hacker/humbert-agent/internal/documenttext"
	"github.com/sda1-hacker/humbert-agent/internal/workspace"
)

// RefreshDocuments 更新可重建的文档索引；读取仍受工作区根目录约束。
func RefreshDocuments(ctx context.Context, index *Index, workspaces *workspace.Manager, agentID string, resolved workspace.Workspace) error {
	root, err := workspaces.OpenRoot(ctx, resolved)
	if err != nil {
		return err
	}
	defer root.Close()
	seen := make(map[string]bool)
	files, docs := 0, 0
	truncated := false
	err = filepath.WalkDir(resolved.RootDir, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if path == resolved.RootDir {
			return nil
		}
		if entry.IsDir() {
			switch strings.ToLower(entry.Name()) {
			case ".git", ".idea", ".vscode", "node_modules", "vendor", "dist", "build", ".next", ".cache":
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		files++
		if files > 5000 || docs >= 1000 {
			truncated = true
			return fs.SkipAll
		}
		if documenttext.MIMEForName(entry.Name()) == "" {
			return nil
		}
		docs++
		rel, err := filepath.Rel(resolved.RootDir, path)
		if err != nil {
			return nil
		}
		seen[rel] = true
		stat, err := entry.Info()
		if err != nil || !stat.Mode().IsRegular() {
			return nil
		}
		current, err := index.DocumentCurrent(ctx, agentID, resolved.RootDir, rel, stat.Size(), stat.ModTime().UnixNano())
		if err != nil || current {
			return err
		}
		if stat.Size() <= 0 || stat.Size() > 12<<20 {
			// 文件仍存在但已不能提取时，必须用空投影替换旧正文。逐文件更新也能
			// 在扫描被数量上限截断、无法运行全量清理时立即消除旧搜索命中。
			return index.ReplaceDocument(ctx, agentID, resolved.RootDir, rel, stat.Size(), stat.ModTime().UnixNano(), "")
		}
		file, err := root.Open(rel)
		if err != nil {
			return nil
		}
		data, readErr := io.ReadAll(io.LimitReader(file, (12<<20)+1))
		file.Close()
		if readErr != nil {
			return nil
		}
		if len(data) > 12<<20 {
			// 文件可能在 stat 后继续增长，同样清除旧正文；下一次扫描按新元数据重试。
			return index.ReplaceDocument(ctx, agentID, resolved.RootDir, rel, stat.Size(), stat.ModTime().UnixNano(), "")
		}
		plain, _, extractErr := documenttext.Extract(ctx, entry.Name(), documenttext.MIMEForName(entry.Name()), data)
		if extractErr != nil {
			// 扫描件或不支持的文档保留空索引，文件变化后再尝试提取。
			plain = ""
		}
		return index.ReplaceDocument(ctx, agentID, resolved.RootDir, rel, stat.Size(), stat.ModTime().UnixNano(), plain)
	})
	if err != nil && err != fs.SkipAll {
		return err
	}
	if !truncated {
		if err := index.PruneDocuments(ctx, agentID, resolved.RootDir, seen); err != nil {
			return err
		}
	}
	return nil
}
