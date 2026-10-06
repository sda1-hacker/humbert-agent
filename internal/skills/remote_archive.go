package skills

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

// HTTP 下载与 ZIP 展开构成一条受限外部输入链：公网解析/拨号、重定向、
// 下载大小、展开大小、文件数量与路径校验集中维护。Git 归档也复用相同解压入口。
// 本文件不选择安装目标，不覆盖已安装内容；包身份验证和提交仍由 Manager 完成。

const (
	remoteSkillArchiveExpansionFactor = int64(8)
	remoteSkillArchiveFileFactor      = 16
	remoteSkillArchiveMaxExpanded     = int64(256 * 1024 * 1024)
	remoteSkillArchiveMaxFiles        = 8192
	remoteSkillUserAgent              = "Humbert-Skill-Installer/1"
)

func (m *Manager) publicSkillHTTPClient() *http.Client {
	return m.publicSkillHTTPClientWithHTTP2(true)
}

func (m *Manager) publicSkillHTTPClientWithHTTP2(forceHTTP2 bool) *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			Proxy:                 nil,
			DialContext:           (&publicSkillDialer{}).DialContext,
			ForceAttemptHTTP2:     forceHTTP2,
			MaxIdleConns:          16,
			MaxIdleConnsPerHost:   4,
			IdleConnTimeout:       30 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ResponseHeaderTimeout: 30 * time.Second,
		},
		CheckRedirect: func(request *http.Request, via []*http.Request) error {
			if len(via) > m.config.MaxRedirects {
				return fmt.Errorf("远程 Skill 重定向超过 %d 次上限", m.config.MaxRedirects)
			}
			if _, err := parsePublicSkillURL(request.URL.String()); err != nil {
				return fmt.Errorf("远程 Skill 重定向目标不安全: %w", err)
			}
			return nil
		},
	}
}

func (m *Manager) downloadRemoteArchive(ctx context.Context, source *url.URL, destination string) error {
	client := m.publicSkillHTTPClient()
	defer client.CloseIdleConnections()

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, source.String(), nil)
	if err != nil {
		return fmt.Errorf("创建远程 Skill 请求失败: %w", err)
	}
	request.Header.Set("Accept", "application/zip, application/octet-stream;q=0.9")
	request.Header.Set("User-Agent", remoteSkillUserAgent)

	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("请求远程 Skill 失败: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("远程 Skill HTTP 状态异常: %d", response.StatusCode)
	}
	if response.ContentLength > m.config.MaxDownloadBytes {
		return fmt.Errorf(
			"远程 Skill ZIP 过大: Content-Length=%d，最大允许 %d bytes；如果来源是大型 Git 仓库，请优先使用 /tree/<ref>/<skill-path> 指向具体 Skill 子目录，或使用项目发布的轻量 Skill ZIP，而不是提高整个仓库下载上限",
			response.ContentLength,
			m.config.MaxDownloadBytes,
		)
	}

	output, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("创建远程 Skill ZIP 临时文件失败: %w", err)
	}
	closed := false
	defer func() {
		if !closed {
			_ = output.Close()
		}
	}()

	limited := io.LimitReader(response.Body, m.config.MaxDownloadBytes+1)
	written, err := io.Copy(output, limited)
	if err != nil {
		return fmt.Errorf("写入远程 Skill ZIP 失败: %w", err)
	}
	if written > m.config.MaxDownloadBytes {
		return fmt.Errorf(
			"远程 Skill ZIP 超过 %d bytes 下载上限；大型 Git 仓库请使用 /tree/<ref>/<skill-path> 或项目发布的轻量 Skill ZIP",
			m.config.MaxDownloadBytes,
		)
	}
	if err := output.Sync(); err != nil {
		return fmt.Errorf("同步远程 Skill ZIP 失败: %w", err)
	}
	if err := output.Close(); err != nil {
		return fmt.Errorf("关闭远程 Skill ZIP 失败: %w", err)
	}
	closed = true
	return nil
}

// extractRemoteArchive 在写文件之前先检查 ZIP 元数据，再逐个安全解压。
//
// ZIP 只是传输容器，最终 Skill Package 仍受更严格的 MaxPackageBytes/MaxFiles 限制。这里允许
// 仓库 ZIP 比最终 Skill 大一些，便于选择子目录：展开体积最多为包上限的 8 倍且不超过
// 256 MiB，文件数最多为包上限的 16 倍且不超过 8192；选中包仍重新接受自己的严格限制。
func (m *Manager) extractRemoteArchive(ctx context.Context, archivePath string, destination string) error {
	reader, err := zip.OpenReader(archivePath)
	if err != nil {
		return fmt.Errorf("只支持标准 ZIP Skill Package: %w", err)
	}
	defer reader.Close()

	maxExpanded := m.config.MaxPackageBytes * remoteSkillArchiveExpansionFactor
	if maxExpanded > remoteSkillArchiveMaxExpanded {
		maxExpanded = remoteSkillArchiveMaxExpanded
	}
	maxFiles := m.config.MaxFiles * remoteSkillArchiveFileFactor
	if maxFiles < m.config.MaxFiles {
		maxFiles = m.config.MaxFiles
	}
	if maxFiles > remoteSkillArchiveMaxFiles {
		maxFiles = remoteSkillArchiveMaxFiles
	}

	var expanded int64
	files := 0
	for _, entry := range reader.File {
		if err := ctx.Err(); err != nil {
			return err
		}
		cleaned, isDir, err := validateArchiveEntry(entry)
		if err != nil {
			return err
		}
		if cleaned == "" || isDir {
			continue
		}
		files++
		if files > maxFiles {
			return fmt.Errorf("远程 Skill ZIP 文件数量超过 %d 个安全上限", maxFiles)
		}
		if entry.UncompressedSize64 > uint64(maxExpanded) ||
			expanded > maxExpanded-int64(entry.UncompressedSize64) {
			return fmt.Errorf("远程 Skill ZIP 解压后体积超过 %d bytes 安全上限", maxExpanded)
		}
		expanded += int64(entry.UncompressedSize64)
	}

	for _, entry := range reader.File {
		if err := ctx.Err(); err != nil {
			return err
		}
		cleaned, isDir, err := validateArchiveEntry(entry)
		if err != nil {
			return err
		}
		if cleaned == "" {
			continue
		}
		target := filepath.Join(destination, filepath.FromSlash(cleaned))
		if isDir {
			if err := os.MkdirAll(target, 0o700); err != nil {
				return fmt.Errorf("创建 Skill ZIP 目录失败: %w", err)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return fmt.Errorf("创建 Skill ZIP 父目录失败: %w", err)
		}
		if err := extractArchiveRegularFile(ctx, entry, target); err != nil {
			return err
		}
	}
	return nil
}

// 写入前拒绝绝对路径、父目录逃逸和链接/特殊对象；返回规范化的包内相对路径。
func validateArchiveEntry(entry *zip.File) (string, bool, error) {
	if entry == nil {
		return "", false, errors.New("远程 Skill ZIP 包含空 Entry")
	}
	name := strings.TrimSpace(strings.ReplaceAll(entry.Name, "\\", "/"))
	if name == "" {
		return "", false, nil
	}
	if strings.HasPrefix(name, "/") {
		return "", false, fmt.Errorf("%w: ZIP Entry 不能是绝对路径: %q", ErrInvalidSkill, name)
	}
	cleaned := path.Clean(name)
	if cleaned == "." {
		return "", entry.FileInfo().IsDir(), nil
	}
	if cleaned == ".." || strings.HasPrefix(cleaned, "../") || strings.Contains(cleaned, ":") {
		return "", false, fmt.Errorf("%w: ZIP Entry 越过包根目录: %q", ErrInvalidSkill, name)
	}
	mode := entry.Mode()
	if mode&os.ModeSymlink != 0 {
		return "", false, fmt.Errorf("%w: ZIP 不能包含符号链接: %q", ErrInvalidSkill, cleaned)
	}
	if entry.FileInfo().IsDir() {
		return cleaned, true, nil
	}
	if !mode.IsRegular() {
		return "", false, fmt.Errorf("%w: ZIP 只能包含普通文件和目录: %q", ErrInvalidSkill, cleaned)
	}
	return cleaned, false, nil
}

// 每个文件独占创建，分块复制时检查 Context，并传播 ZIP 读取错误；完成后 Sync 再关闭。
func extractArchiveRegularFile(ctx context.Context, entry *zip.File, destination string) error {
	input, err := entry.Open()
	if err != nil {
		return fmt.Errorf("打开 ZIP Entry 失败: %w", err)
	}
	defer input.Close()
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("创建 ZIP 解压文件失败: %w", err)
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
					return fmt.Errorf("写入 ZIP 解压文件失败: %w", writeErr)
				}
				if n <= 0 {
					return fmt.Errorf("写入 ZIP 解压文件失败: %w", io.ErrShortWrite)
				}
				written += n
			}
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return fmt.Errorf("读取 ZIP Entry 失败: %w", readErr)
		}
	}
	if err := output.Sync(); err != nil {
		return fmt.Errorf("同步 ZIP 解压文件失败: %w", err)
	}
	if err := output.Close(); err != nil {
		return fmt.Errorf("关闭 ZIP 解压文件失败: %w", err)
	}
	closed = true
	return nil
}

// publicSkillDialer 是远程 Skill 下载的 SSRF 边界。
//
// Hostname 的所有 DNS 结果都会先校验；只要出现私网、Loopback、Link-Local、Multicast 或
// Unspecified 地址就整体拒绝。真正连接时直接拨已经校验过的 IP，避免 DNS rebinding 在
// “校验”和“连接”之间替换解析结果。
type publicSkillDialer struct{}

func (d *publicSkillDialer) DialContext(ctx context.Context, network string, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, fmt.Errorf("解析远程 Skill 网络地址失败: %w", err)
	}
	addresses, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return nil, fmt.Errorf("解析远程 Skill Host 失败: %w", err)
	}
	if len(addresses) == 0 {
		return nil, errors.New("远程 Skill Host 没有可用 IP")
	}
	for _, ip := range addresses {
		if !isPublicSkillIP(ip) {
			return nil, fmt.Errorf("远程 Skill Host 解析到非公网地址: %s", ip.String())
		}
	}

	dialer := &net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}
	var lastErr error
	for _, ip := range addresses {
		connection, dialErr := dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		if dialErr == nil {
			return connection, nil
		}
		lastErr = dialErr
	}
	return nil, fmt.Errorf("连接远程 Skill Host 失败: %w", lastErr)
}

func isPublicSkillIP(ip netip.Addr) bool {
	if !ip.IsValid() {
		return false
	}
	ip = ip.Unmap()
	return !ip.IsUnspecified() &&
		!ip.IsLoopback() &&
		!ip.IsPrivate() &&
		!ip.IsLinkLocalUnicast() &&
		!ip.IsLinkLocalMulticast() &&
		!ip.IsMulticast()
}
