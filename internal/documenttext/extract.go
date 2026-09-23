package documenttext

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/tsawler/tabula"
	"github.com/tsawler/tabula/rag"
)

const maxInputBytes = 12 << 20
const maxOutputBytes = 512 << 10
const maxOfficeExpandedBytes = 64 << 20
const parseTimeout = 20 * time.Second
const workerEnv = "HUMBERT_DOCUMENT_WORKER_FILE"

// 同一二进制在受控环境中作为一次性文档解析进程启动。解析超时由父进程终止。
func init() {
	if path := os.Getenv(workerEnv); path != "" {
		options := rag.DefaultMarkdownOptions()
		if strings.EqualFold(filepath.Ext(path), ".pdf") {
			options.IncludePageNumbers = true
		}
		markdown, _, err := tabula.Open(path).ToMarkdownWithOptions(options)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		if _, err := io.WriteString(os.Stdout, markdown); err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
}

var supported = map[string]string{
	".pdf":  "application/pdf",
	".docx": "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
	".xlsx": "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
	".pptx": "application/vnd.openxmlformats-officedocument.presentationml.presentation",
}

// MIMEForName 只将明确的 PDF/Office 扩展名交给文档解析器。
func MIMEForName(name string) string { return supported[strings.ToLower(filepath.Ext(name))] }

// Validate 校验文档类型、大小和容器结构；上传时调用它而不提前解析正文。
func Validate(ctx context.Context, name, mimeType string, data []byte) (string, error) {
	ext := strings.ToLower(filepath.Ext(name))
	want := supported[ext]
	if want == "" {
		return "", errors.New("不支持的文档格式")
	}
	if mimeType != "" && mimeType != "application/octet-stream" && mimeType != want {
		return "", errors.New("文件扩展名与 MIME 类型不一致")
	}
	if len(data) == 0 || len(data) > maxInputBytes {
		return "", errors.New("文档大小超出 12 MiB 限制")
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if ext == ".pdf" {
		if !bytes.HasPrefix(data, []byte("%PDF-")) {
			return "", errors.New("PDF 文件签名无效")
		}
	} else if err := preflightOffice(data); err != nil {
		return "", err
	}
	return want, nil
}

// Extract 在一次性受限进程中将 PDF/Office 文档转换成 Markdown。
func Extract(ctx context.Context, name, mimeType string, data []byte) (string, string, error) {
	want, err := Validate(ctx, name, mimeType, data)
	if err != nil {
		return "", "", err
	}
	ext := strings.ToLower(filepath.Ext(name))
	file, err := os.CreateTemp("", "humbert-document-*"+ext)
	if err != nil {
		return "", "", err
	}
	defer os.Remove(file.Name())
	defer file.Close()
	if err := file.Chmod(0o600); err != nil {
		return "", "", err
	}
	if _, err := file.Write(data); err != nil {
		return "", "", err
	}
	if err := file.Close(); err != nil {
		return "", "", err
	}
	workerCtx, cancel := context.WithTimeout(ctx, parseTimeout)
	defer cancel()
	markdown, err := parseInWorker(workerCtx, file.Name())
	if err != nil {
		return "", "", fmt.Errorf("提取文档 Markdown 失败: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return "", "", err
	}
	markdown = strings.TrimSpace(markdown)
	if !utf8.ValidString(markdown) || markdown == "" {
		return "", "", errors.New("文档没有可读取的文本；扫描版 PDF 需要 OCR")
	}
	if len(markdown) > maxOutputBytes {
		return "", "", errors.New("文档 Markdown 超过 512 KiB，请拆分文档")
	}
	return markdown, want, nil
}

func parseInWorker(ctx context.Context, path string) (string, error) {
	executable, err := os.Executable()
	if err != nil {
		return "", err
	}
	command := exec.CommandContext(ctx, executable)
	command.Env = []string{workerEnv + "=" + path}
	command.WaitDelay = time.Second
	output := &boundedWriter{limit: maxOutputBytes + 1}
	errorsOutput := &boundedWriter{limit: 4096}
	command.Stdout, command.Stderr = output, errorsOutput
	if err := command.Run(); err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		if output.exceeded {
			return "", errors.New("文档 Markdown 超过 512 KiB，请拆分文档")
		}
		return "", fmt.Errorf("文档解析子进程失败: %s: %w", strings.TrimSpace(errorsOutput.String()), err)
	}
	if output.exceeded {
		return "", errors.New("文档 Markdown 超过 512 KiB，请拆分文档")
	}
	return output.String(), nil
}

type boundedWriter struct {
	buffer   bytes.Buffer
	limit    int
	exceeded bool
}

func (w *boundedWriter) Write(value []byte) (int, error) {
	original := len(value)
	remaining := w.limit - w.buffer.Len()
	if remaining <= 0 {
		w.exceeded = true
		return original, nil
	}
	if len(value) > remaining {
		w.exceeded = true
		value = value[:remaining]
	}
	_, _ = w.buffer.Write(value)
	return original, nil
}

func (w *boundedWriter) String() string { return w.buffer.String() }

func preflightOffice(data []byte) error {
	archive, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return fmt.Errorf("Office ZIP 结构无效: %w", err)
	}
	if len(archive.File) > 2048 {
		return errors.New("Office 文件内部条目过多")
	}
	var total uint64
	for _, file := range archive.File {
		if file.Mode()&os.ModeSymlink != 0 {
			return errors.New("Office 文件包含符号链接")
		}
		if strings.HasPrefix(file.Name, "/") || strings.Contains(file.Name, "\\") || strings.Contains(file.Name, "../") {
			return errors.New("Office 文件内部路径无效")
		}
		if file.UncompressedSize64 > maxOfficeExpandedBytes-total {
			return errors.New("Office 文件解压后超过 64 MiB 限制")
		}
		total += file.UncompressedSize64
	}
	return nil
}
