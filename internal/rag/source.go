package rag

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"

	"github.com/cloudwego/eino/components/document"
	"github.com/sda1-hacker/humbert-agent/internal/documenttext"
)

// LocalSourceStore 将原件按 SHA-256 和扩展名保存，共享相同内容，保留 PDF/Office 等原格式。
// 只有显式导入才写文件；数据库保存相对标识，移动整个数据目录后仍可访问原件。
type LocalSourceStore struct {
	root     *os.Root
	maxBytes int64
}

// NewLocalSourceStore 的空目录使用系统用户配置目录，兼容 macOS、Windows 和 Linux。
// OpenRoot 限制后续文件操作的范围；调用方负责 Close。
func NewLocalSourceStore(directory string, maxBytes int64) (*LocalSourceStore, error) {
	if maxBytes < 0 || maxBytes == math.MaxInt64 {
		return nil, errors.New("rag: 原件大小限制无效")
	}
	if maxBytes == 0 {
		maxBytes = documenttext.DefaultOptions().MaxInputBytes
	}
	if strings.TrimSpace(directory) == "" {
		base, err := os.UserConfigDir()
		if err != nil {
			return nil, err
		}
		directory = filepath.Join(base, "humbert-agent", "rag", "sources")
	}
	directory, err := filepath.Abs(directory)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(directory, 0700); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, err
	}
	return &LocalSourceStore{root: root, maxBytes: maxBytes}, nil
}

// Save 流式复制受限快照，完整写入并同步后才发布；解析失败时原件仍可用于重试。
// 原件由知识库存储独立持有，不随聊天会话清理；共享文件不在删除单个文档时直接移除。
func (s *LocalSourceStore) Save(ctx context.Context, src document.Source) (document.Source, SourceFile, error) {
	var empty document.Source
	path, _, err := validateSource(src)
	if err != nil {
		return empty, SourceFile{}, err
	}
	file, err := os.Open(path)
	if err != nil {
		return empty, SourceFile{}, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return empty, SourceFile{}, err
	}
	if !info.Mode().IsRegular() || info.Size() > s.maxBytes {
		return empty, SourceFile{}, fmt.Errorf("%w: 原件不是普通文件或超过 %d 字节", ErrIngestLimit, s.maxBytes)
	}
	temp := ".upload-" + rand.Text()
	output, err := s.root.OpenFile(temp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return empty, SourceFile{}, err
	}
	defer s.root.Remove(temp)
	defer output.Close()
	hash := sha256.New()
	n, err := io.Copy(io.MultiWriter(output, hash), io.LimitReader(sourceReader{ctx, file}, s.maxBytes+1))
	if err != nil {
		return empty, SourceFile{}, err
	}
	if n == 0 || n > s.maxBytes {
		return empty, SourceFile{}, fmt.Errorf("%w: 原件为空或超过 %d 字节", ErrIngestLimit, s.maxBytes)
	}
	if err := output.Sync(); err != nil {
		return empty, SourceFile{}, err
	}
	if err := output.Close(); err != nil {
		return empty, SourceFile{}, err
	}
	if err := ctx.Err(); err != nil {
		return empty, SourceFile{}, err
	}
	ext := strings.ToLower(filepath.Ext(path))
	stored := SourceFile{URI: path, Name: filepath.Base(path), Type: strings.TrimPrefix(ext, "."), Size: n, Hash: hex.EncodeToString(hash.Sum(nil))}
	stored.Path = stored.Hash + ext
	// 同内容并发导入时只发布一个原件；检查已有文件，拒绝复用损坏或被替换的内容。
	if err := s.root.Link(temp, stored.Path); err != nil {
		if !errors.Is(err, os.ErrExist) {
			return empty, SourceFile{}, err
		}
		if err := s.verify(ctx, stored); err != nil {
			return empty, SourceFile{}, err
		}
	}
	return document.Source{URI: filepath.Join(s.root.Name(), stored.Path)}, stored, nil
}

// Open 按数据库中的相对标识读取原件，拒绝任意路径和越界符号链接。
func (s *LocalSourceStore) Open(path string) (*os.File, error) {
	if !validSourcePath(path) {
		return nil, errors.New("rag: 无效的原件存储标识")
	}
	return s.root.Open(path)
}

// Resolve 供失败后人工重试使用，不依赖用户上传时的临时文件仍然存在。
func (s *LocalSourceStore) Resolve(ctx context.Context, source SourceFile) (document.Source, error) {
	if source.Size <= 0 || source.Size > s.maxBytes || source.Path != source.Hash+"."+source.Type || !validSourcePath(source.Path) {
		return document.Source{}, errors.New("rag: 托管原件引用无效，请重新上传文件")
	}
	if err := s.verify(ctx, source); err != nil {
		return document.Source{}, err
	}
	return document.Source{URI: filepath.Join(s.root.Name(), source.Path)}, nil
}

func (s *LocalSourceStore) Close() error { return s.root.Close() }

// Directory 返回实际原件目录，便于桌面下载和示例输出定位文件。
func (s *LocalSourceStore) Directory() string { return s.root.Name() }

func (s *LocalSourceStore) verify(ctx context.Context, source SourceFile) error {
	file, err := s.Open(source.Path)
	if err != nil {
		return err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() != source.Size {
		return errors.New("rag: 已保存的原件大小不一致，请检查文件存储")
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, io.LimitReader(sourceReader{ctx, file}, s.maxBytes+1)); err != nil {
		return err
	}
	if hex.EncodeToString(hash.Sum(nil)) != source.Hash {
		return errors.New("rag: 已保存的原件摘要不一致，请检查文件存储")
	}
	return nil
}

func validSourcePath(path string) bool {
	if path != filepath.Base(path) || len(path) <= 65 || path[64] != '.' {
		return false
	}
	_, err := hex.DecodeString(path[:64])
	return err == nil && path == strings.ToLower(path) && !strings.ContainsAny(path, `/\\`)
}

// sourceReader 在复制循环中传递取消，避免停止任务后继续读取大文件。
type sourceReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r sourceReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}
