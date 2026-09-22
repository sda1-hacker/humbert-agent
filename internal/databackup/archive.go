package databackup

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const manifestName = "humbert-backup-manifest.json"
const pendingRestoreName = "pending-restore.json"
const pendingBackupName = "pending-backup.json"
const credentialExportName = "humbert-credential-export.json"
const maxArchiveBytes int64 = 20 << 30
const maxArchiveFiles = 100000

type fileRecord struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

type manifest struct {
	Format    int          `json:"format"`
	CreatedAt time.Time    `json:"created_at"`
	Files     []fileRecord `json:"files"`
}

// Create 把用户数据写成带逐文件哈希的 ZIP。调用方应在应用停止后运行，
// 以保证多个文件来自同一个逻辑时刻。日志、缓存和临时文件不属于用户数据。
func Create(ctx context.Context, root, destination string) error {
	root, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	destination, err = filepath.Abs(destination)
	if err != nil {
		return err
	}
	if within(root, destination) {
		return errors.New("备份文件不能位于数据目录内")
	}
	if err := realDirectory(root); err != nil {
		return err
	}
	if info, err := os.Lstat(destination); err == nil {
		if !info.Mode().IsRegular() {
			return errors.New("备份目标不是普通文件")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	paths := make([]string, 0)
	err = filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if path == root {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if rel == "logs" || rel == "cache" || rel == "tmp" {
				return filepath.SkipDir
			}
			return realDirectory(path)
		}
		if rel == pendingRestoreName || rel == pendingBackupName {
			return nil
		}
		if rel == backupErrorName {
			return nil
		}
		if rel == manifestName {
			return errors.New("数据目录包含备份清单保留文件名")
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("数据目录包含非普通文件: %s", rel)
		}
		paths = append(paths, rel)
		if len(paths) > maxArchiveFiles {
			return errors.New("备份文件数量超过上限")
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("扫描数据目录失败: %w", err)
	}
	sort.Strings(paths)
	parent := filepath.Dir(destination)
	if err := realDirectory(parent); err != nil {
		return err
	}
	temp, err := os.CreateTemp(parent, ".humbert-backup-*.zip")
	if err != nil {
		return err
	}
	defer os.Remove(temp.Name())
	defer temp.Close()
	if err := temp.Chmod(0o600); err != nil {
		return err
	}
	writer := zip.NewWriter(temp)
	doc := manifest{Format: 1, CreatedAt: time.Now().UTC()}
	var total int64
	for _, rel := range paths {
		if err := ctx.Err(); err != nil {
			return err
		}
		path := filepath.Join(root, rel)
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("备份期间文件类型改变: %s", rel)
		}
		total += info.Size()
		if total > maxArchiveBytes {
			return errors.New("备份大小超过上限")
		}
		input, err := os.Open(path)
		if err != nil {
			return err
		}
		entry, err := writer.Create(filepath.ToSlash(rel))
		if err != nil {
			input.Close()
			return err
		}
		hash := sha256.New()
		written, copyErr := io.Copy(entry, io.TeeReader(io.LimitReader(input, info.Size()+1), hash))
		closeErr := input.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
		if written != info.Size() {
			return fmt.Errorf("备份期间文件内容改变: %s", rel)
		}
		doc.Files = append(doc.Files, fileRecord{Path: filepath.ToSlash(rel), Size: written, SHA256: hex.EncodeToString(hash.Sum(nil))})
	}
	manifestEntry, err := writer.Create(manifestName)
	if err != nil {
		return err
	}
	if err := json.NewEncoder(manifestEntry).Encode(doc); err != nil {
		return err
	}
	if err := writer.Close(); err != nil {
		return err
	}
	if err := temp.Sync(); err != nil {
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Rename(temp.Name(), destination); err != nil {
		return err
	}
	return nil
}

// Verify 对清单、路径、大小和哈希进行完整校验，不向磁盘解压。
func Verify(ctx context.Context, archive string) error {
	return inspect(ctx, archive, "")
}

// Restore 只供应用完全退出后调用。原数据目录会保留为返回的 rollbackPath；
// 只有整个 ZIP 验证且暂存目录写完后才切换目录。
func Restore(ctx context.Context, archive, root string) (rollbackPath string, err error) {
	reader, err := zip.OpenReader(archive)
	if err != nil {
		return "", err
	}
	defer reader.Close()
	return restoreReader(ctx, &reader.Reader, root)
}

func restoreReader(ctx context.Context, reader *zip.Reader, root string) (rollbackPath string, err error) {
	root, err = filepath.Abs(root)
	if err != nil {
		return "", err
	}
	parent := filepath.Dir(root)
	if err := realDirectory(parent); err != nil {
		return "", err
	}
	stage, err := os.MkdirTemp(parent, ".humbert-restore-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(stage)
	if err := os.Chmod(stage, 0o700); err != nil {
		return "", err
	}
	if err := inspectReader(ctx, reader, stage); err != nil {
		return "", err
	}
	// 历史归档即使含有恢复计划，也不能让下次启动重复执行恢复。
	_ = os.Remove(filepath.Join(stage, pendingRestoreName))
	_ = os.Remove(filepath.Join(stage, pendingBackupName))
	_ = os.Remove(filepath.Join(stage, backupErrorName))
	_ = os.Remove(filepath.Join(stage, credentialExportName))
	if info, statErr := os.Lstat(root); statErr == nil {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return "", errors.New("现有数据目录不是安全目录")
		}
		rollbackPath = root + ".before-restore-" + time.Now().UTC().Format("20060102T150405.000000000")
		if err := os.Rename(root, rollbackPath); err != nil {
			return "", err
		}
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return "", statErr
	}
	if err := os.Rename(stage, root); err != nil {
		if rollbackPath != "" {
			_ = os.Rename(rollbackPath, root)
		}
		return "", err
	}
	if rollbackPath != "" {
		_ = os.Remove(filepath.Join(rollbackPath, pendingRestoreName))
		_ = os.Remove(filepath.Join(rollbackPath, pendingBackupName))
	}
	return rollbackPath, nil
}

func inspect(ctx context.Context, archive, stage string) error {
	reader, err := zip.OpenReader(archive)
	if err != nil {
		return err
	}
	defer reader.Close()
	return inspectReader(ctx, &reader.Reader, stage)
}

func inspectReader(ctx context.Context, reader *zip.Reader, stage string) error {
	if len(reader.File) > maxArchiveFiles+1 {
		return errors.New("备份文件数量超过上限")
	}
	files := make(map[string]*zip.File, len(reader.File))
	var doc manifest
	manifestSeen := false
	for _, file := range reader.File {
		if file.Name == manifestName {
			if manifestSeen {
				return errors.New("重复的备份清单")
			}
			manifestSeen = true
			if file.UncompressedSize64 > 10<<20 {
				return errors.New("备份清单过大")
			}
			input, err := file.Open()
			if err != nil {
				return err
			}
			data, err := io.ReadAll(io.LimitReader(input, 10<<20))
			input.Close()
			if err != nil {
				return err
			}
			if err := json.Unmarshal(data, &doc); err != nil {
				return err
			}
			continue
		}
		if !safeArchivePath(file.Name) || !file.Mode().IsRegular() {
			return fmt.Errorf("备份包含不安全文件: %s", file.Name)
		}
		if _, exists := files[file.Name]; exists {
			return fmt.Errorf("备份包含重复文件: %s", file.Name)
		}
		files[file.Name] = file
	}
	if !manifestSeen || doc.Format != 1 || len(doc.Files) != len(files) {
		return errors.New("备份清单格式或文件数无效")
	}
	seen := make(map[string]bool, len(doc.Files))
	var total int64
	for _, record := range doc.Files {
		if err := ctx.Err(); err != nil {
			return err
		}
		file := files[record.Path]
		if file == nil || seen[record.Path] || record.Size < 0 || int64(file.UncompressedSize64) != record.Size {
			return fmt.Errorf("备份文件清单无效: %s", record.Path)
		}
		seen[record.Path] = true
		total += record.Size
		if total > maxArchiveBytes {
			return errors.New("备份解压大小超过上限")
		}
		input, err := file.Open()
		if err != nil {
			return err
		}
		var output *os.File
		// Verify the encrypted credential entry without writing its plaintext
		// contents to the restore staging directory.
		if stage != "" && record.Path != credentialExportName {
			path := filepath.Join(stage, filepath.FromSlash(record.Path))
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				input.Close()
				return err
			}
			output, err = os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
			if err != nil {
				input.Close()
				return err
			}
		}
		hash := sha256.New()
		target := io.Writer(hash)
		if output != nil {
			target = io.MultiWriter(hash, output)
		}
		written, copyErr := io.Copy(target, io.LimitReader(input, record.Size+1))
		input.Close()
		if output != nil {
			if err := output.Sync(); copyErr == nil {
				copyErr = err
			}
			if err := output.Close(); copyErr == nil {
				copyErr = err
			}
		}
		if copyErr != nil {
			return copyErr
		}
		if written != record.Size || hex.EncodeToString(hash.Sum(nil)) != record.SHA256 {
			return fmt.Errorf("备份文件校验失败: %s", record.Path)
		}
	}
	return nil
}

func safeArchivePath(name string) bool {
	if name == "" || strings.Contains(name, "\\") || strings.HasPrefix(name, "/") {
		return false
	}
	clean := filepath.Clean(filepath.FromSlash(name))
	return clean != "." && clean != ".." && !strings.HasPrefix(clean, ".."+string(filepath.Separator)) && filepath.ToSlash(clean) == name
}

func within(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func realDirectory(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("不是安全目录: %s", path)
	}
	return nil
}
