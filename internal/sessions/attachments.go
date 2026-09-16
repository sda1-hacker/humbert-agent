package sessions

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cloudwego/eino/schema"
	"github.com/google/uuid"

	"github.com/sda1-hacker/humbert-agent/internal/transcript"
)

const (
	maxAttachmentsPerMessage       = 8
	maxAttachmentBytes       int64 = 12 * 1024 * 1024
	maxAttachmentTotalBytes  int64 = 24 * 1024 * 1024
	attachmentURLPrefix            = "humbert-attachment://"
)

func (s *Service) appendUserInput(ctx context.Context, sessionID string, input UserInput) (Message, error) {
	text := strings.TrimSpace(input.Text)
	if len(text) > maxUserMessageBytes {
		return Message{}, fmt.Errorf("用户消息不能超过 %d 字节", maxUserMessageBytes)
	}
	if len(input.Attachments) > maxAttachmentsPerMessage {
		return Message{}, fmt.Errorf("单条消息最多允许 %d 个附件", maxAttachmentsPerMessage)
	}
	if text == "" && len(input.Attachments) == 0 {
		return Message{}, errors.New("用户消息或附件至少需要一个")
	}

	attachmentDir := ""
	if len(input.Attachments) > 0 {
		directory, err := s.store.SessionDirectory(ctx, sessionID)
		if err != nil {
			return Message{}, err
		}
		attachmentDir = filepath.Join(directory, "attachments")
		if err := ensureAttachmentDirectory(attachmentDir); err != nil {
			return Message{}, err
		}
	}

	created := make([]string, 0, len(input.Attachments))
	cleanup := func() {
		for _, path := range created {
			_ = os.Remove(path)
		}
	}
	parts := make([]schema.MessageInputPart, 0, len(input.Attachments)+1)
	if text != "" {
		parts = append(parts, schema.MessageInputPart{Type: schema.ChatMessagePartTypeText, Text: text})
	}
	var total int64
	for _, raw := range input.Attachments {
		name := strings.TrimSpace(raw.Name)
		if name == "" {
			cleanup()
			return Message{}, errors.New("附件名称不能为空")
		}
		if len([]rune(name)) > 255 {
			cleanup()
			return Message{}, errors.New("附件名称过长")
		}
		mime := strings.TrimSpace(raw.MIMEType)
		if mime == "" {
			mime = "application/octet-stream"
		}
		data, err := base64.StdEncoding.DecodeString(strings.TrimSpace(raw.Base64Data))
		if err != nil {
			cleanup()
			return Message{}, fmt.Errorf("附件 %s Base64 无效: %w", name, err)
		}
		size := int64(len(data))
		if size <= 0 {
			cleanup()
			return Message{}, fmt.Errorf("附件 %s 为空", name)
		}
		if size > maxAttachmentBytes {
			cleanup()
			return Message{}, fmt.Errorf("附件 %s 超过 12 MiB 限制", name)
		}
		total += size
		if total > maxAttachmentTotalBytes {
			cleanup()
			return Message{}, errors.New("单条消息附件总大小不能超过 24 MiB")
		}
		id := uuid.NewString()
		path := filepath.Join(attachmentDir, id)
		if err := writeAttachmentFile(path, data); err != nil {
			cleanup()
			return Message{}, fmt.Errorf("保存附件 %s 失败: %w", name, err)
		}
		created = append(created, path)
		url := attachmentURLPrefix + id
		extra := map[string]any{"name": name, "size_bytes": size, "attachment_id": id}
		if strings.HasPrefix(strings.ToLower(mime), "image/") {
			parts = append(parts, schema.MessageInputPart{Type: schema.ChatMessagePartTypeImageURL, Image: &schema.MessageInputImage{MessagePartCommon: schema.MessagePartCommon{URL: &url, MIMEType: mime}}, Extra: extra})
		} else {
			parts = append(parts, schema.MessageInputPart{Type: schema.ChatMessagePartTypeFileURL, File: &schema.MessageInputFile{MessagePartCommon: schema.MessagePartCommon{URL: &url, MIMEType: mime}, Name: name}, Extra: extra})
		}
	}
	message := &schema.Message{Role: schema.User, UserInputMultiContent: parts}
	// 纯文本继续使用简单 Content，保持 Provider 兼容与历史格式紧凑。
	if len(input.Attachments) == 0 {
		message = schema.UserMessage(text)
	}
	stored, err := s.append(ctx, sessionID, message, transcriptEncodeOptions())
	if err != nil {
		cleanup()
		return Message{}, err
	}
	s.renameDefaultSessionFromFirstInput(ctx, sessionID, stored, text, input.Attachments)
	return stored, nil
}

// renameDefaultSessionFromFirstInput 为首次输入生成短标题。
//
// 标题属于辅助控制面；自动命名失败不能让一条已经落盘的用户消息表现为发送失败。
func (s *Service) renameDefaultSessionFromFirstInput(
	ctx context.Context,
	sessionID string,
	stored Message,
	text string,
	attachments []AttachmentInput,
) {
	// 第一条 Tree Entry 没有 parent。直接使用 Append 返回的事实，既避免为自动标题再次
	// 全量读取 Transcript，也不会在两个并发输入先后落盘后因观察到两条消息而都放弃命名。
	if stored.ParentID != nil {
		return
	}
	session, err := s.store.GetSession(ctx, sessionID)
	if err != nil || session.Title != defaultSessionTitle {
		return
	}

	title := strings.Join(strings.Fields(text), " ")
	if title == "" && len(attachments) > 0 {
		title = strings.TrimSpace(attachments[0].Name)
	}
	if title == "" {
		return
	}
	const maxAutomaticTitleRunes = 42
	runes := []rune(title)
	if len(runes) > maxAutomaticTitleRunes {
		title = string(runes[:maxAutomaticTitleRunes-1]) + "…"
	}
	if err := s.store.RenameSession(ctx, sessionID, title); err != nil {
		s.logger.Warn(
			ctx,
			"Session 自动命名失败",
			"operation", "session.title.auto",
			"session_id", sessionID,
			"error", err,
		)
	}
}

func (s *Service) userInputMatchesStoredMessage(ctx context.Context, sessionID string, input UserInput, message *schema.Message) (bool, error) {
	if message == nil || message.Role != schema.User {
		return false, nil
	}
	text := strings.TrimSpace(input.Text)
	if len(input.Attachments) == 0 {
		return len(message.UserInputMultiContent) == 0 && strings.TrimSpace(message.Content) == text, nil
	}
	if len(message.UserInputMultiContent) == 0 {
		return false, nil
	}
	storedText := ""
	storedAttachments := make([]schema.MessageInputPart, 0, len(message.UserInputMultiContent))
	for _, part := range message.UserInputMultiContent {
		switch part.Type {
		case schema.ChatMessagePartTypeText:
			storedText += part.Text
		case schema.ChatMessagePartTypeImageURL, schema.ChatMessagePartTypeFileURL:
			storedAttachments = append(storedAttachments, part)
		}
	}
	if strings.TrimSpace(storedText) != text || len(storedAttachments) != len(input.Attachments) {
		return false, nil
	}
	for index, raw := range input.Attachments {
		part := storedAttachments[index]
		name := strings.TrimSpace(raw.Name)
		mime := strings.TrimSpace(raw.MIMEType)
		if mime == "" {
			mime = "application/octet-stream"
		}
		id := stringExtra(part.Extra, "attachment_id")
		storedName := stringExtra(part.Extra, "name")
		storedMIME := ""
		switch part.Type {
		case schema.ChatMessagePartTypeImageURL:
			if part.Image == nil {
				return false, nil
			}
			storedMIME = part.Image.MIMEType
		case schema.ChatMessagePartTypeFileURL:
			if part.File == nil {
				return false, nil
			}
			storedMIME = part.File.MIMEType
			if storedName == "" {
				storedName = part.File.Name
			}
		}
		if id == "" || storedName != name || storedMIME != mime {
			return false, nil
		}
		provided, err := base64.StdEncoding.DecodeString(strings.TrimSpace(raw.Base64Data))
		if err != nil {
			return false, nil
		}
		stored, err := s.readAttachmentBytes(ctx, sessionID, id)
		if err != nil {
			return false, err
		}
		if !bytes.Equal(stored, provided) {
			return false, nil
		}
	}
	return true, nil
}

// HydrateMessages 在真正调用 Provider 前把 Session 附件引用恢复为 Eino multimodal 内容。
// ContextEngine/Transcript 始终保留轻量 sidecar 引用，避免 Base64 进入 JSONL、摘要与压缩。
func (s *Service) HydrateMessages(ctx context.Context, sessionID string, messages []*schema.Message) ([]*schema.Message, error) {
	result := make([]*schema.Message, 0, len(messages))
	for index, message := range messages {
		hydrated, err := s.hydrateUserAttachments(ctx, sessionID, message)
		if err != nil {
			return nil, fmt.Errorf("恢复第 %d 条 Runtime Message 附件失败: %w", index+1, err)
		}
		result = append(result, hydrated)
	}
	return result, nil
}

// hydrateUserAttachments 把 JSONL 中的 sidecar URL 恢复为 Eino Base64Data，仅存在于模型请求内存中。
func (s *Service) hydrateUserAttachments(ctx context.Context, sessionID string, message *schema.Message) (*schema.Message, error) {
	if message == nil || message.Role != schema.User || len(message.UserInputMultiContent) == 0 {
		return message, nil
	}
	copyMessage := *message
	copyMessage.UserInputMultiContent = make([]schema.MessageInputPart, 0, len(message.UserInputMultiContent))
	for _, part := range message.UserInputMultiContent {
		next := part
		switch part.Type {
		case schema.ChatMessagePartTypeImageURL:
			if part.Image == nil {
				return nil, errors.New("历史图片附件结构无效")
			}
			common, err := s.hydrateCommon(ctx, sessionID, part.Image.MessagePartCommon)
			if err != nil {
				return nil, err
			}
			image := *part.Image
			image.MessagePartCommon = common
			next.Image = &image
		case schema.ChatMessagePartTypeFileURL:
			if part.File == nil {
				return nil, errors.New("历史文件附件结构无效")
			}
			common, err := s.hydrateCommon(ctx, sessionID, part.File.MessagePartCommon)
			if err != nil {
				return nil, err
			}
			file := *part.File
			file.MessagePartCommon = common
			next.File = &file
		}
		copyMessage.UserInputMultiContent = append(copyMessage.UserInputMultiContent, next)
	}
	return &copyMessage, nil
}

func (s *Service) hydrateCommon(ctx context.Context, sessionID string, common schema.MessagePartCommon) (schema.MessagePartCommon, error) {
	if common.URL == nil || !strings.HasPrefix(*common.URL, attachmentURLPrefix) {
		return common, nil
	}
	id := strings.TrimPrefix(*common.URL, attachmentURLPrefix)
	data, err := s.readAttachmentBytes(ctx, sessionID, id)
	if err != nil {
		return schema.MessagePartCommon{}, err
	}
	encoded := base64.StdEncoding.EncodeToString(data)
	common.URL = nil
	common.Base64Data = &encoded
	return common, nil
}

func (s *Service) readAttachmentBytes(ctx context.Context, sessionID, id string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	id = strings.TrimSpace(id)
	if id == "" || strings.ContainsAny(id, `/\\`) {
		return nil, errors.New("Attachment ID 非法")
	}
	directory, err := s.store.SessionDirectory(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	attachmentDir := filepath.Join(directory, "attachments")
	if err := validateAttachmentDirectory(attachmentDir); err != nil {
		return nil, err
	}
	path := filepath.Join(attachmentDir, id)
	info, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("附件不存在: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, errors.New("附件不是普通文件")
	}
	if info.Size() > maxAttachmentBytes {
		return nil, errors.New("附件超过安全大小限制")
	}
	return os.ReadFile(path)
}

func ensureAttachmentDirectory(path string) error {
	info, err := os.Lstat(path)
	if err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return errors.New("附件目录不是安全目录")
		}
		return nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("检查附件目录失败: %w", err)
	}
	if err := os.Mkdir(path, 0o700); err != nil {
		if errors.Is(err, os.ErrExist) {
			return validateAttachmentDirectory(path)
		}
		return fmt.Errorf("创建附件目录失败: %w", err)
	}
	return nil
}

func validateAttachmentDirectory(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("附件目录不可用: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return errors.New("附件目录不是安全目录")
	}
	return nil
}

func writeAttachmentFile(path string, data []byte) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return err
	}
	return nil
}

// ReadAttachment 供 Desktop 历史消息按需预览图片/下载文件内容。
func (s *Service) ReadAttachment(ctx context.Context, sessionID, id string) ([]byte, error) {
	return s.readAttachmentBytes(ctx, sessionID, id)
}

// transcriptEncodeOptions 避免 attachments.go 为零值选项额外引入持久化逻辑。
func transcriptEncodeOptions() transcript.EncodeOptions { return transcript.EncodeOptions{} }

func stringExtra(values map[string]any, key string) string {
	if values == nil {
		return ""
	}
	value, _ := values[key].(string)
	return strings.TrimSpace(value)
}
