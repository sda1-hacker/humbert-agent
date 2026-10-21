package fileops

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/sda1-hacker/humbert-agent/internal/documenttext"
	"github.com/sda1-hacker/humbert-agent/internal/sandbox"
)

// ReadDocument 读取已授权文档原件；解析和会话附件仍由调用方负责。
func ReadDocument(ctx context.Context, policy sandbox.EffectivePolicy, path string) (name, display, mime string, data []byte, err error) {
	target, err := openSandboxTarget(ctx, policy, path, sandbox.OpRead)
	if err != nil {
		return "", "", "", nil, fmt.Errorf("文档路径被 Sandbox 拒绝: %w", err)
	}
	defer target.Close()
	name, display = target.relative, target.display
	mime = documenttext.MIMEForName(name)
	if mime == "" {
		return name, display, mime, nil, errors.New("仅支持 PDF、DOCX、XLSX 和 PPTX 文档")
	}
	file, err := target.root.Open(target.relative)
	if err != nil {
		return name, display, mime, nil, fmt.Errorf("打开文档失败: %w", err)
	}
	defer file.Close()
	info, statErr := file.Stat()
	if statErr != nil || !info.Mode().IsRegular() || info.Size() > 12<<20 {
		return name, display, mime, nil, errors.New("文档必须是 12 MiB 以内的普通文件")
	}
	data, err = io.ReadAll(io.LimitReader(file, (12<<20)+1))
	if err != nil {
		return name, display, mime, nil, fmt.Errorf("读取文档失败: %w", err)
	}
	return name, display, mime, data, nil
}
