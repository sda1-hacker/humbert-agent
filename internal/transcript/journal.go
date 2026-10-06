package transcript

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// 本文件拥有 Session 物理日志的路径约束、编解码与断尾恢复。
// 文件锁、缓存和索引仍由 Store 协调；带 Locked 后缀的函数必须在对应 Session 锁内调用。
// 只修复无法完成的最后一行，完整历史中的协议或树关系损坏仍明确报错。

const (
	sessionDirectoryName      = "sessions"
	sessionTranscriptFileName = "session.jsonl"

	// maxEntryBytes 是单条 JSONL Entry 的硬上限。
	//
	// Tool 自身还应执行更小的输出预算；这里是最后一道 Storage Boundary，防止损坏
	// 文件或异常 Tool Result 让加载 Session 时无限占用内存。
	maxEntryBytes = 8 * 1024 * 1024
)

func (s *Store) sessionPath(agentID string, sessionID string) (string, error) {
	directory, err := s.sessionDirectory(agentID, sessionID)
	if err != nil {
		return "", err
	}
	path := filepath.Join(directory, sessionTranscriptFileName)
	if !pathWithinRoot(s.agentsRoot, path) {
		return "", ErrInvalidIdentifier
	}
	return path, nil
}

func (s *Store) agentSessionsDirectory(agentID string) (string, error) {
	if err := validateIdentifier(agentID); err != nil {
		return "", fmt.Errorf("Agent ID 无效: %w", err)
	}
	directory := filepath.Join(s.agentsRoot, agentID, sessionDirectoryName)
	if !pathWithinRoot(s.agentsRoot, directory) {
		return "", ErrInvalidIdentifier
	}
	return directory, nil
}

func (s *Store) sessionDirectory(agentID string, sessionID string) (string, error) {
	if err := validateIdentifier(sessionID); err != nil {
		return "", fmt.Errorf("Session ID 无效: %w", err)
	}
	root, err := s.agentSessionsDirectory(agentID)
	if err != nil {
		return "", err
	}
	directory := filepath.Join(root, sessionID)
	if !pathWithinRoot(s.agentsRoot, directory) {
		return "", ErrInvalidIdentifier
	}
	return directory, nil
}

func (s *Store) ensureSessionDirectory(agentID string, sessionID string) error {
	agentDirectory := filepath.Join(s.agentsRoot, agentID)
	if !pathWithinRoot(s.agentsRoot, agentDirectory) {
		return ErrInvalidIdentifier
	}
	if err := ensureRealDirectory(agentDirectory); err != nil {
		return fmt.Errorf("准备 Agent Transcript 目录失败: %w", err)
	}

	root, err := s.agentSessionsDirectory(agentID)
	if err != nil {
		return err
	}
	if err := ensureRealDirectory(root); err != nil {
		return fmt.Errorf("准备 Agent Session Root 失败: %w", err)
	}

	directory, err := s.sessionDirectory(agentID, sessionID)
	if err != nil {
		return err
	}
	if err := ensureRealDirectory(directory); err != nil {
		return fmt.Errorf("准备 Session 数据目录失败: %w", err)
	}
	return nil
}

// 逐级检查已有会话的真实目录和普通文件，避免目录或 JSONL 被替换成符号链接。
func (s *Store) validateExistingSessionPath(agentID string, sessionID string, path string) error {
	root, err := s.agentSessionsDirectory(agentID)
	if err != nil {
		return err
	}
	if err := validateRealDirectory(root); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ErrSessionNotFound
		}
		return fmt.Errorf("验证 Agent Session Root 失败: %w", err)
	}

	directory, err := s.sessionDirectory(agentID, sessionID)
	if err != nil {
		return err
	}
	if err := validateRealDirectory(directory); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ErrSessionNotFound
		}
		return fmt.Errorf("验证 Session 数据目录失败: %w", err)
	}

	info, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ErrSessionNotFound
		}
		return fmt.Errorf("读取 Session Transcript 状态失败: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return errors.New("Session Transcript 不能是符号链接")
	}
	if !info.Mode().IsRegular() {
		return errors.New("Session Transcript 不是普通文件")
	}
	return nil
}

func ensureRealDirectory(path string) error {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return err
	}
	return validateRealDirectory(path)
}

func validateRealDirectory(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("目录不能是符号链接: %s", path)
	}
	if !info.IsDir() {
		return fmt.Errorf("路径不是目录: %s", path)
	}
	return nil
}

func validateIdentifier(value string) error {
	value = strings.TrimSpace(value)
	if value == "" || value == "." || value == ".." {
		return ErrInvalidIdentifier
	}
	if strings.ContainsRune(value, '\x00') || filepath.IsAbs(value) {
		return ErrInvalidIdentifier
	}
	if strings.ContainsAny(value, `/\\`) || filepath.Base(value) != value {
		return ErrInvalidIdentifier
	}
	return nil
}

func pathWithinRoot(root string, candidate string) bool {
	relative, err := filepath.Rel(root, candidate)
	if err != nil {
		return false
	}
	return relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func encodeJSONLine(value any) ([]byte, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	if len(data) > maxEntryBytes {
		return nil, fmt.Errorf("单条 JSONL Entry 超过 %d bytes", maxEntryBytes)
	}
	return append(data, '\n'), nil
}

// repairTailLocked 仅修复未完成的末行或缺失的末尾换行；先检查尾字节，正常日志不额外全扫。
func repairTailLocked(ctx context.Context, path string) (RepairResult, error) {
	if err := validateContext(ctx); err != nil {
		return RepairResult{}, err
	}

	file, err := os.OpenFile(path, os.O_RDWR, 0o600)
	if err != nil {
		return RepairResult{}, fmt.Errorf("打开 Transcript Repair 文件失败: %w", err)
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return RepairResult{}, fmt.Errorf("读取 Transcript 文件状态失败: %w", err)
	}
	if info.Size() == 0 {
		return RepairResult{}, &CorruptionError{Line: 1, Offset: 0, Reason: "缺少 Session Header"}
	}

	// 正常关闭的每条 JSONL 写入都以换行结束。绝大多数读取/追加无需为了确认“没有 crash
	// tail”先完整扫描一次；后续 loadLocked 仍会逐行严格校验全部 JSON 和 Tree 关系，因此
	// 这里的 O(1) 快路径不会掩盖中间损坏。只有末字节不是换行时才进入下面的修复扫描。
	lastByte := []byte{0}
	if _, err := file.ReadAt(lastByte, info.Size()-1); err != nil {
		return RepairResult{}, fmt.Errorf("读取 Transcript 尾字节失败: %w", err)
	}
	if lastByte[0] == '\n' {
		return RepairResult{}, nil
	}

	reader := bufio.NewReaderSize(file, 64*1024)
	var offset int64
	var lastGoodOffset int64
	lineNumber := 0

	for {
		if err := validateContext(ctx); err != nil {
			return RepairResult{}, err
		}

		line, readErr := reader.ReadBytes('\n')
		if len(line) == 0 && errors.Is(readErr, io.EOF) {
			break
		}
		if len(line) == 0 && readErr != nil {
			return RepairResult{}, fmt.Errorf("读取 Transcript Repair 数据失败: %w", readErr)
		}

		lineNumber++
		hasNewline := line[len(line)-1] == '\n'
		payload := line
		if hasNewline {
			payload = line[:len(line)-1]
		}
		if len(payload) > maxEntryBytes {
			return RepairResult{}, &CorruptionError{Line: lineNumber, Offset: offset, Reason: "单行 Entry 超过最大限制"}
		}

		if !json.Valid(payload) {
			if errors.Is(readErr, io.EOF) && !hasNewline && lastGoodOffset > 0 {
				truncated := info.Size() - lastGoodOffset
				if err := file.Truncate(lastGoodOffset); err != nil {
					return RepairResult{}, fmt.Errorf("截断损坏 Transcript Tail 失败: %w", err)
				}
				if err := file.Sync(); err != nil {
					return RepairResult{}, fmt.Errorf("同步 Transcript Tail Repair 失败: %w", err)
				}
				return RepairResult{Repaired: true, TruncatedBytes: truncated}, nil
			}
			reason := "存在非尾部无效 JSON"
			if lineNumber == 1 {
				reason = "Session Header 不完整"
			}
			return RepairResult{}, &CorruptionError{Line: lineNumber, Offset: offset, Reason: reason}
		}

		offset += int64(len(line))
		lastGoodOffset = offset

		if errors.Is(readErr, io.EOF) {
			if hasNewline {
				break
			}
			if _, err := file.Seek(0, io.SeekEnd); err != nil {
				return RepairResult{}, fmt.Errorf("定位 Transcript 尾部失败: %w", err)
			}
			if err := writeFull(file, []byte{'\n'}); err != nil {
				return RepairResult{}, fmt.Errorf("补写 Transcript 尾部换行失败: %w", err)
			}
			if err := file.Sync(); err != nil {
				return RepairResult{}, fmt.Errorf("同步 Transcript 尾部换行失败: %w", err)
			}
			return RepairResult{Repaired: true, AddedFinalNewline: true}, nil
		}
		if readErr != nil {
			return RepairResult{}, fmt.Errorf("读取 Transcript Repair 数据失败: %w", readErr)
		}
	}

	return RepairResult{}, nil
}

// loadLocked 严格重放整个日志，再沿 Leaf 构造活动分支；大文件的局部读取由位置索引负责。
func loadLocked(ctx context.Context, path string, sessionID string) (Document, error) {
	file, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Document{}, fmt.Errorf("%w: session_id=%s", ErrSessionNotFound, sessionID)
		}
		return Document{}, fmt.Errorf("打开 Session Transcript 失败: %w", err)
	}
	defer file.Close()

	reader := bufio.NewReaderSize(file, 64*1024)
	lineNumber := 0
	var offset int64

	var header SessionHeader
	entries := make([]Entry, 0, 64)
	entryByID := make(map[string]Entry)
	leafID := ""

	for {
		if err := validateContext(ctx); err != nil {
			return Document{}, err
		}

		line, readErr := reader.ReadBytes('\n')
		if len(line) == 0 && errors.Is(readErr, io.EOF) {
			break
		}
		if len(line) == 0 && readErr != nil {
			return Document{}, fmt.Errorf("读取 Session Transcript 失败: %w", readErr)
		}

		lineNumber++
		if len(line) > maxEntryBytes+1 {
			return Document{}, &CorruptionError{Line: lineNumber, Offset: offset, Reason: "单行 Entry 超过最大限制"}
		}

		payload := bytes.TrimSuffix(line, []byte{'\n'})
		if lineNumber == 1 {
			if err := decodeStrictJSON(payload, &header); err != nil {
				return Document{}, &CorruptionError{Line: 1, Offset: 0, Reason: "Session Header JSON 无法解析"}
			}
			if err := validateHeader(header, sessionID); err != nil {
				return Document{}, &CorruptionError{Line: 1, Offset: 0, Reason: err.Error()}
			}
		} else {
			var entry Entry
			if err := decodeStrictJSON(payload, &entry); err != nil {
				return Document{}, &CorruptionError{Line: lineNumber, Offset: offset, Reason: "Session Entry JSON 无法解析"}
			}
			if err := validateEntryPayload(entry); err != nil {
				return Document{}, &CorruptionError{Line: lineNumber, Offset: offset, Reason: err.Error()}
			}
			if _, exists := entryByID[entry.ID]; exists {
				return Document{}, &CorruptionError{Line: lineNumber, Offset: offset, Reason: "Session Entry id 重复"}
			}
			if entry.ParentID != nil {
				if _, exists := entryByID[*entry.ParentID]; !exists {
					return Document{}, &CorruptionError{Line: lineNumber, Offset: offset, Reason: "parentId 指向不存在或尚未出现的 Entry"}
				}
			}

			entryByID[entry.ID] = entry
			entries = append(entries, entry)
			leafID = entry.ID
		}

		offset += int64(len(line))
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return Document{}, fmt.Errorf("读取 Session Transcript 失败: %w", readErr)
		}
	}

	if lineNumber == 0 {
		return Document{}, &CorruptionError{Line: 1, Offset: 0, Reason: "缺少 Session Header"}
	}

	activeBranch, err := buildActiveBranch(entryByID, leafID)
	if err != nil {
		return Document{}, err
	}

	return Document{
		Header:        header,
		Entries:       entries,
		ActiveBranch:  activeBranch,
		LeafID:        leafID,
		ContextWindow: contextWindowIndex(activeBranch),
	}, nil
}

func buildActiveBranch(entryByID map[string]Entry, leafID string) ([]Entry, error) {
	if leafID == "" {
		return []Entry{}, nil
	}

	reversed := make([]Entry, 0, len(entryByID))
	visited := make(map[string]struct{}, len(entryByID))
	currentID := leafID

	for currentID != "" {
		if _, exists := visited[currentID]; exists {
			return nil, fmt.Errorf("%w: Session Tree 存在 parent cycle", ErrCorrupted)
		}
		visited[currentID] = struct{}{}

		entry, exists := entryByID[currentID]
		if !exists {
			return nil, fmt.Errorf("%w: Active Branch Entry %s 不存在", ErrCorrupted, currentID)
		}
		reversed = append(reversed, entry)

		if entry.ParentID == nil {
			break
		}
		currentID = *entry.ParentID
	}

	result := make([]Entry, len(reversed))
	for index := range reversed {
		result[len(reversed)-1-index] = reversed[index]
	}
	return result, nil
}

// 协议不接受未知字段或第二个 JSON 值，防止一行被解释成不同的持久化事实。
func decodeStrictJSON(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(target); err != nil {
		return err
	}

	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("JSON 包含多余值")
		}
		return fmt.Errorf("JSON 尾部无效: %w", err)
	}
	return nil
}

func writeFull(writer io.Writer, data []byte) error {
	for len(data) > 0 {
		written, err := writer.Write(data)
		if err != nil {
			return err
		}
		if written <= 0 {
			return io.ErrShortWrite
		}
		data = data[written:]
	}
	return nil
}
