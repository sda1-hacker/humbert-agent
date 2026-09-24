package memory

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/sda1-hacker/humbert-agent/internal/atomicfile"
)

const memoryFileName = "memory.json"

// SessionDirectoryResolver 是 Memory Store 与 Session 物理布局之间的最小边界。
//
// Memory 不自行拼接 agent/session ID，也不接受前端传入任意文件路径。受控目录必须由
// sessions.Store 从 TranscriptStore 的安全路径解析逻辑推导，避免路径穿越或 symlink
// 逃逸。
type SessionDirectoryResolver interface {
	SessionDirectory(ctx context.Context, sessionID string) (string, error)
}

// Store 负责 memory.json 的原子读写。
//
// JSON 文件更新使用 atomicfile 的“同目录临时文件 + fsync + rename”。Store 的 per-session
// Mutex 只保护短生命周期的本地文件 IO，防止同一 Session 的 Load/Save 在进程内交错。跨越
// 模型调用的“读取 cursor -> 生成摘要 -> 保存”事务由 Manager 的可取消 Session 锁负责，避免
// 长时间持有不可取消的 Mutex；不同 Session 仍可并行。
type Store struct {
	directories SessionDirectoryResolver

	locksMu sync.Mutex
	locks   map[string]*sync.Mutex
}

// NewStore 创建 Session Memory Store。
func NewStore(directories SessionDirectoryResolver) (*Store, error) {
	if directories == nil {
		return nil, errors.New("Memory Store SessionDirectoryResolver 不能为空")
	}
	return &Store{
		directories: directories,
		locks:       make(map[string]*sync.Mutex),
	}, nil
}

// Load 读取一个 Session 的 memory.json。
//
// exists=false 表示尚未生成 Memory；v2 可读并在刷新时升级，损坏 JSON、未知版本、
// SessionID 不匹配等情况返回明确错误。Manager 决定是否降级或重建。
func (s *Store) Load(ctx context.Context, sessionID string) (document Document, exists bool, err error) {
	path, err := s.path(ctx, sessionID)
	if err != nil {
		return Document{}, false, err
	}

	lock := s.lockFor(sessionID)
	lock.Lock()
	defer lock.Unlock()

	return s.loadUnlocked(ctx, path, sessionID)
}

// Save 原子覆盖指定 Session 的 memory.json。
//
// memory.json 是派生状态，所以采用覆盖式 JSON 而不是 append-only。真正的对话事实始终在
// session.jsonl；即使应用在 Save 中途退出，atomicfile 也会保留旧完整版本或新完整版本。
func (s *Store) Save(ctx context.Context, sessionID string, document Document) error {
	path, err := s.path(ctx, sessionID)
	if err != nil {
		return err
	}

	lock := s.lockFor(sessionID)
	lock.Lock()
	defer lock.Unlock()

	return s.saveUnlocked(ctx, path, sessionID, document)
}

func (s *Store) loadUnlocked(ctx context.Context, path string, sessionID string) (Document, bool, error) {
	var document Document
	if err := atomicfile.ReadJSON(ctx, path, &document); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Document{}, false, nil
		}
		return Document{}, false, fmt.Errorf("读取 Session Memory 失败: %w", err)
	}
	// v2 的事实仍可继续供当前对话使用；Manager 在下次刷新时从 Transcript 重建
	// v3，以清除旧版按 ToolCall 推断出的不准确文件产物。
	if document.Version != CurrentVersion && document.Version != 2 {
		return Document{}, false, fmt.Errorf("不支持的 Session Memory 版本: %d", document.Version)
	}
	if document.SessionID != sessionID {
		return Document{}, false, fmt.Errorf("Session Memory ID 不匹配: expected=%s actual=%s", sessionID, document.SessionID)
	}
	if _, _, err := splitSummary(document.Summary); err != nil {
		return Document{}, false, fmt.Errorf("Session Memory Summary 格式无效: %w", err)
	}
	return document, true, nil
}

func (s *Store) saveUnlocked(ctx context.Context, path string, sessionID string, document Document) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("写入 Session Memory 被取消: %w", err)
	}
	if document.Version != CurrentVersion {
		return fmt.Errorf("Session Memory Version 必须为 %d", CurrentVersion)
	}
	if document.SessionID != sessionID {
		return errors.New("拒绝把其他 Session 的 Memory 写入当前目录")
	}
	if strings.TrimSpace(document.Cursor.CoveredLeafID) == "" || strings.TrimSpace(document.Cursor.LineageHash) == "" {
		return errors.New("Session Memory Cursor 不能为空")
	}
	if _, _, err := splitSummary(document.Summary); err != nil {
		return fmt.Errorf("拒绝写入格式无效的 Session Memory: %w", err)
	}
	if err := atomicfile.WriteJSON(ctx, path, 0o600, document); err != nil {
		return fmt.Errorf("原子写入 Session Memory 失败: %w", err)
	}
	return nil
}

func (s *Store) path(ctx context.Context, sessionID string) (string, error) {
	if ctx == nil {
		return "", errors.New("context.Context 不能为空")
	}
	if err := ctx.Err(); err != nil {
		return "", fmt.Errorf("解析 Session Memory 路径被取消: %w", err)
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return "", errors.New("Session ID 不能为空")
	}
	directory, err := s.directories.SessionDirectory(ctx, sessionID)
	if err != nil {
		return "", fmt.Errorf("解析 Session 受控目录失败: %w", err)
	}
	return filepath.Join(directory, memoryFileName), nil
}

func (s *Store) lockFor(sessionID string) *sync.Mutex {
	s.locksMu.Lock()
	defer s.locksMu.Unlock()
	if lock, exists := s.locks[sessionID]; exists {
		return lock
	}
	lock := &sync.Mutex{}
	s.locks[sessionID] = lock
	return lock
}
