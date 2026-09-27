package builtin

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
)

func defaultSearchPath(path string) string {
	if strings.TrimSpace(path) == "" {
		return "."
	}
	return path
}

func displayRootChild(target *sandboxTarget, rel string) string {
	if child, err := filepath.Rel(target.relative, rel); err == nil {
		rel = child
	}
	if target.display == "." {
		return filepath.ToSlash(rel)
	}
	if filepath.IsAbs(target.display) {
		return filepath.Join(target.display, rel)
	}
	return filepath.ToSlash(filepath.Join(target.display, rel))
}

func walkRoot(ctx context.Context, target *sandboxTarget, visit func(rel string, entry fs.DirEntry) error) error {
	start := target.relative
	// 超过上限返回明确错误，调用方可缩小 path；不能把截断结果伪装成完整搜索。
	const maxVisited = 20000
	visited := 0
	var walk func(string) error
	walk = func(rel string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if allowed, err := target.canReadChild(rel); err != nil || !allowed {
			return err
		}
		f, err := target.root.Open(rel)
		if err != nil {
			return nil
		}
		info, err := f.Stat()
		if err != nil {
			f.Close()
			return nil
		}
		if !info.IsDir() {
			f.Close()
			entry := fs.FileInfoToDirEntry(info)
			return visit(rel, entry)
		}
		entries, err := f.ReadDir(maxVisited - visited + 1)
		f.Close()
		if err != nil && err != io.EOF {
			return err
		}
		visited += len(entries)
		if visited > maxVisited {
			return errors.New("扫描超过 20000 个目录项，请缩小 path 范围")
		}
		sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
		for _, entry := range entries {
			child := entry.Name()
			if rel != "." && rel != "" {
				child = filepath.Join(rel, entry.Name())
			}
			if entry.Name() == ".git" && entry.IsDir() {
				continue
			}
			if entry.Type()&fs.ModeSymlink != 0 {
				continue
			}
			if allowed, err := target.canReadChild(child); err != nil {
				return err
			} else if !allowed {
				continue
			}
			if err := visit(child, entry); err != nil {
				return err
			}
			if entry.IsDir() {
				if err := walk(child); err != nil {
					return err
				}
			}
		}
		return nil
	}
	return walk(start)
}
