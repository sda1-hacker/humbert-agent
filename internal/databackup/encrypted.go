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

	"filippo.io/age"
)

// CreateEncrypted 直接把归档流写入 age 加密器；磁盘上不会出现明文 ZIP。
// 应在应用未运行时调用，以保证跨文件快照一致。
func CreateEncrypted(ctx context.Context, root, destination, passphrase string, credentials map[string]string) error {
	if len([]rune(passphrase)) < 12 {
		return errors.New("备份口令至少需要 12 个字符")
	}
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
	if err := realDirectory(filepath.Dir(destination)); err != nil {
		return err
	}
	if info, err := os.Lstat(destination); err == nil && !info.Mode().IsRegular() {
		return errors.New("备份目标不是普通文件")
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	recipient, err := age.NewScryptRecipient(passphrase)
	if err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(destination), ".humbert-encrypted-*.age")
	if err != nil {
		return err
	}
	defer os.Remove(temp.Name())
	defer temp.Close()
	if err := temp.Chmod(0o600); err != nil {
		return err
	}
	encrypted, err := age.Encrypt(temp, recipient)
	if err != nil {
		return err
	}
	if err := writeEncryptedArchive(ctx, root, encrypted, credentials); err != nil {
		_ = encrypted.Close()
		return err
	}
	if err := encrypted.Close(); err != nil {
		return err
	}
	if err := temp.Sync(); err != nil {
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(temp.Name(), destination)
}

func writeEncryptedArchive(ctx context.Context, root string, output io.Writer, credentials map[string]string) error {
	paths := make([]string, 0)
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
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
			if rel == "logs" || rel == "cache" || rel == "tmp" || rel == "secrets" {
				return filepath.SkipDir
			}
			return realDirectory(path)
		}
		if rel == pendingRestoreName || rel == pendingBackupName || rel == backupErrorName {
			return nil
		}
		if rel == manifestName || rel == credentialExportName {
			return errors.New("数据目录包含备份保留文件名")
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("数据目录包含非普通文件: %s", rel)
		}
		paths = append(paths, rel)
		if len(paths) > maxArchiveFiles-1 {
			return errors.New("备份文件数量超过上限")
		}
		return nil
	})
	if err != nil {
		return err
	}
	sort.Strings(paths)
	writer := zip.NewWriter(output)
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
		if info.Size() > maxArchiveBytes-total {
			return errors.New("备份大小超过上限")
		}
		total += info.Size()
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
	secretBytes, err := json.Marshal(credentials)
	if err != nil {
		return err
	}
	if len(secretBytes) > 10<<20 {
		return errors.New("凭据导出过大")
	}
	entry, err := writer.Create(credentialExportName)
	if err != nil {
		return err
	}
	if _, err := entry.Write(secretBytes); err != nil {
		return err
	}
	hash := sha256.Sum256(secretBytes)
	doc.Files = append(doc.Files, fileRecord{Path: credentialExportName, Size: int64(len(secretBytes)), SHA256: hex.EncodeToString(hash[:])})
	manifestEntry, err := writer.Create(manifestName)
	if err != nil {
		return err
	}
	if err := json.NewEncoder(manifestEntry).Encode(doc); err != nil {
		return err
	}
	return writer.Close()
}

func openEncryptedArchive(path, passphrase string) (*os.File, *zip.Reader, error) {
	if passphrase == "" {
		return nil, nil, errors.New("备份口令不能为空")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > maxArchiveBytes+maxArchiveBytes/10 {
		file.Close()
		return nil, nil, errors.New("加密备份不是有效普通文件或大小超过上限")
	}
	identity, err := age.NewScryptIdentity(passphrase)
	if err != nil {
		file.Close()
		return nil, nil, err
	}
	readerAt, size, err := age.DecryptReaderAt(file, info.Size(), identity)
	if err != nil {
		file.Close()
		return nil, nil, fmt.Errorf("解密备份失败，检查口令: %w", err)
	}
	reader, err := zip.NewReader(readerAt, size)
	if err != nil {
		file.Close()
		return nil, nil, err
	}
	return file, reader, nil
}

func VerifyEncrypted(ctx context.Context, path, passphrase string) error {
	file, reader, err := openEncryptedArchive(path, passphrase)
	if err != nil {
		return err
	}
	defer file.Close()
	return inspectReader(ctx, reader, "")
}

// RestoreEncrypted 验证后切换目录，并返回只应导入系统凭据库的秘密数据。
func RestoreEncrypted(ctx context.Context, path, root, passphrase string) (string, map[string]string, error) {
	file, reader, err := openEncryptedArchive(path, passphrase)
	if err != nil {
		return "", nil, err
	}
	defer file.Close()
	if err := inspectReader(ctx, reader, ""); err != nil {
		return "", nil, err
	}
	var credentials map[string]string
	found := false
	for _, entry := range reader.File {
		if entry.Name != credentialExportName {
			continue
		}
		found = true
		input, err := entry.Open()
		if err != nil {
			return "", nil, err
		}
		data, err := io.ReadAll(io.LimitReader(input, (10<<20)+1))
		input.Close()
		if err != nil || len(data) > 10<<20 {
			return "", nil, errors.New("凭据导出无效")
		}
		if err := json.Unmarshal(data, &credentials); err != nil {
			return "", nil, err
		}
	}
	if !found {
		return "", nil, errors.New("加密备份缺少凭据清单")
	}
	if credentials == nil {
		credentials = map[string]string{}
	}
	rollback, err := restoreReader(ctx, reader, root)
	if err != nil {
		return "", nil, err
	}
	return rollback, credentials, nil
}

// RestoreEncryptedAndImport 保证凭据导入失败时尝试回退数据目录。
func RestoreEncryptedAndImport(ctx context.Context, path, root, passphrase string, importer func(context.Context, string, map[string]string) error) (string, error) {
	if importer == nil {
		return "", errors.New("凭据导入器不能为空")
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	rollback, credentials, err := RestoreEncrypted(ctx, path, root, passphrase)
	if err != nil {
		return "", err
	}
	if err := importer(ctx, root, credentials); err != nil {
		failed := root + ".failed-restore-" + time.Now().UTC().Format("20060102T150405.000000000")
		moveErr := os.Rename(root, failed)
		if moveErr == nil && rollback != "" {
			moveErr = os.Rename(rollback, root)
		}
		if moveErr != nil {
			return "", errors.Join(err, fmt.Errorf("恢复原数据目录失败: %w", moveErr))
		}
		return "", fmt.Errorf("导入系统凭据失败，原数据已回退: %w", err)
	}
	return rollback, nil
}

func IsEncrypted(path string) bool { return strings.EqualFold(filepath.Ext(path), ".age") }
