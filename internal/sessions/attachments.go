package sessions

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/cloudwego/eino/schema"
	"github.com/google/uuid"

	"github.com/sda1-hacker/humbert-agent/internal/documenttext"
	"github.com/sda1-hacker/humbert-agent/internal/multimodal"
	"github.com/sda1-hacker/humbert-agent/internal/transcript"
)

const (
	maxAttachmentsPerMessage        = 8
	maxAttachmentBytes        int64 = 12 * 1024 * 1024
	maxAttachmentTotalBytes   int64 = 24 * 1024 * 1024
	maxTextAttachmentBytes    int64 = 512 * 1024
	maxRuntimeAttachmentBytes int64 = 32 * 1024 * 1024
	attachmentURLPrefix             = "humbert-attachment://"
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
		mimeType := normalizeAttachmentMIME(raw.MIMEType)
		if mimeType == "" {
			mimeType = "application/octet-stream"
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
		if mimeType == "application/octet-stream" {
			if detected := normalizeAttachmentMIME(http.DetectContentType(data)); strings.HasPrefix(detected, "image/") {
				mimeType = detected
			}
		}

		extractedText := ""
		if strings.HasPrefix(mimeType, "image/") {
			if err := validateImageAttachment(name, mimeType, data); err != nil {
				cleanup()
				return Message{}, err
			}
		} else if documenttext.MIMEForName(name) != "" {
			var validateErr error
			mimeType, validateErr = documenttext.Validate(ctx, name, mimeType, data)
			if validateErr != nil {
				cleanup()
				return Message{}, validateErr
			}
		} else {
			var extractErr error
			extractedText, mimeType, extractErr = extractTextAttachment(ctx, name, mimeType, data)
			if extractErr != nil {
				cleanup()
				return Message{}, extractErr
			}
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
		if strings.HasPrefix(mimeType, "image/") {
			parts = append(parts, schema.MessageInputPart{Type: schema.ChatMessagePartTypeImageURL, Image: &schema.MessageInputImage{MessagePartCommon: schema.MessagePartCommon{URL: &url, MIMEType: mimeType}}, Extra: extra})
		} else {
			extra["extracted_text"] = extractedText
			if documenttext.MIMEForName(name) != "" {
				extra["document_on_demand"] = true
			}
			parts = append(parts, schema.MessageInputPart{Type: schema.ChatMessagePartTypeFileURL, File: &schema.MessageInputFile{MessagePartCommon: schema.MessagePartCommon{URL: &url, MIMEType: mimeType}, Name: name}, Extra: extra})
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
		mimeType := normalizeAttachmentMIME(raw.MIMEType)
		if mimeType == "" {
			mimeType = "application/octet-stream"
		}
		provided, err := base64.StdEncoding.DecodeString(strings.TrimSpace(raw.Base64Data))
		if err != nil {
			return false, nil
		}
		if mimeType == "application/octet-stream" {
			if detected := normalizeAttachmentMIME(http.DetectContentType(provided)); strings.HasPrefix(detected, "image/") {
				mimeType = detected
			}
		}
		if !strings.HasPrefix(mimeType, "image/") {
			if documenttext.MIMEForName(name) != "" {
				var validateErr error
				mimeType, validateErr = documenttext.Validate(ctx, name, mimeType, provided)
				if validateErr != nil {
					return false, nil
				}
			} else {
				_, normalizedMIME, extractErr := extractTextAttachment(ctx, name, mimeType, provided)
				if extractErr != nil {
					return false, nil
				}
				mimeType = normalizedMIME
			}
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
		if id == "" || storedName != name || storedMIME != mimeType {
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

// HydrateMessages 在真正调用 Provider 前恢复 Session 附件。近期图片转成 Base64 多模态
// 输入；较早图片转成元数据占位，避免每个后续 Turn 都重复读取和上传同一二进制；文本文件
// 转成普通 text part，避免把当前 Eino Adapter 不支持的 file_url 发给 Provider。
// ContextEngine/Transcript 始终保留 sidecar 引用和确定性提取文本。
func (s *Service) HydrateMessages(ctx context.Context, sessionID string, messages []*schema.Message) ([]*schema.Message, error) {
	result := make([]*schema.Message, 0, len(messages))
	var hydratedBytes int64
	imageReplayMask := multimodal.ImageReplayMask(messages)
	fileReplayMask := multimodal.FileReplayMask(messages)
	for index, message := range messages {
		hydrated, err := s.hydrateUserAttachmentsWithBudget(
			ctx,
			sessionID,
			message,
			&hydratedBytes,
			imageReplayMask[index],
			fileReplayMask[index],
		)
		if err != nil {
			return nil, fmt.Errorf("恢复第 %d 条 Runtime Message 附件失败: %w", index+1, err)
		}
		result = append(result, hydrated)
	}
	return result, nil
}

// hydrateUserAttachments 构造单条 Provider Message；附件二进制只在请求内存中存在。
func (s *Service) hydrateUserAttachments(ctx context.Context, sessionID string, message *schema.Message) (*schema.Message, error) {
	var hydratedBytes int64
	return s.hydrateUserAttachmentsWithBudget(ctx, sessionID, message, &hydratedBytes, true, true)
}

func (s *Service) hydrateUserAttachmentsWithBudget(
	ctx context.Context,
	sessionID string,
	message *schema.Message,
	hydratedBytes *int64,
	replayImages bool,
	replayFiles bool,
) (*schema.Message, error) {
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
			if !replayImages {
				next = schema.MessageInputPart{
					Type: schema.ChatMessagePartTypeText,
					Text: multimodal.HistoricalImagePlaceholder(part),
				}
				break
			}
			common, err := s.hydrateCommon(ctx, sessionID, part.Image.MessagePartCommon, hydratedBytes)
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
			if !replayFiles {
				next = schema.MessageInputPart{Type: schema.ChatMessagePartTypeText, Text: multimodal.HistoricalFilePlaceholder(part)}
				break
			}
			extractedText := stringExtra(part.Extra, "extracted_text")
			if extractedText == "" {
				if documenttext.MIMEForName(part.File.Name) == "" {
					return nil, fmt.Errorf("文件附件 %q 缺少可发送给模型的提取文本", part.File.Name)
				}
				next = schema.MessageInputPart{Type: schema.ChatMessagePartTypeText, Text: multimodal.HistoricalFilePlaceholder(part)}
				break
			}
			next = schema.MessageInputPart{
				Type: schema.ChatMessagePartTypeText,
				Text: formatExtractedFileForModel(part.File.Name, part.File.MIMEType, extractedText),
			}
		}
		copyMessage.UserInputMultiContent = append(copyMessage.UserInputMultiContent, next)
	}
	return &copyMessage, nil
}

func (s *Service) hydrateCommon(
	ctx context.Context,
	sessionID string,
	common schema.MessagePartCommon,
	hydratedBytes *int64,
) (schema.MessagePartCommon, error) {
	if common.URL == nil || !strings.HasPrefix(*common.URL, attachmentURLPrefix) {
		return common, nil
	}
	id := strings.TrimPrefix(*common.URL, attachmentURLPrefix)
	data, err := s.readAttachmentBytes(ctx, sessionID, id)
	if err != nil {
		return schema.MessagePartCommon{}, err
	}
	if hydratedBytes != nil {
		*hydratedBytes += int64(len(data))
		if *hydratedBytes > maxRuntimeAttachmentBytes {
			return schema.MessagePartCommon{}, fmt.Errorf(
				"本次模型上下文中的图片附件超过 %d MiB，请开启新会话或先压缩旧上下文",
				maxRuntimeAttachmentBytes/(1024*1024),
			)
		}
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
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("打开附件失败: %w", err)
	}
	defer file.Close()
	openedInfo, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("读取附件状态失败: %w", err)
	}
	if !openedInfo.Mode().IsRegular() || !os.SameFile(info, openedInfo) {
		return nil, errors.New("附件在安全校验期间发生变化")
	}
	data, err := io.ReadAll(io.LimitReader(file, maxAttachmentBytes+1))
	if err != nil {
		return nil, fmt.Errorf("读取附件失败: %w", err)
	}
	if int64(len(data)) > maxAttachmentBytes {
		return nil, errors.New("附件超过安全大小限制")
	}
	return data, nil
}

func normalizeAttachmentMIME(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if index := strings.IndexByte(value, ';'); index >= 0 {
		value = strings.TrimSpace(value[:index])
	}
	if value == "image/jpg" {
		return "image/jpeg"
	}
	return value
}

func validateImageAttachment(name string, claimedMIME string, data []byte) error {
	detected := normalizeAttachmentMIME(http.DetectContentType(data))
	if !strings.HasPrefix(detected, "image/") {
		return fmt.Errorf("附件 %s 声明为图片，但内容不是受支持的图片格式", name)
	}
	// 要求声明与内容一致，避免把任意二进制伪装成 data URI 交给 Provider。
	if detected != claimedMIME {
		return fmt.Errorf("附件 %s 的图片类型不匹配: declared=%s detected=%s", name, claimedMIME, detected)
	}
	return nil
}

func extractTextAttachment(ctx context.Context, name string, mimeType string, data []byte) (string, string, error) {
	if err := ctx.Err(); err != nil {
		return "", "", err
	}
	if int64(len(data)) > maxTextAttachmentBytes {
		return "", "", fmt.Errorf("文本附件 %s 超过 512 KiB 限制", name)
	}
	extension := strings.ToLower(filepath.Ext(name))
	if !textAttachmentTypeSupported(mimeType, extension) {
		return "", "", fmt.Errorf("附件 %s 的格式 %s 暂不支持；当前文件附件仅支持 UTF-8 文本、源码和 JSON/YAML/XML 等文本格式", name, mimeType)
	}
	if !utf8.Valid(data) || bytes.IndexByte(data, 0) >= 0 {
		return "", "", fmt.Errorf("附件 %s 不是有效 UTF-8 文本", name)
	}
	text := strings.TrimPrefix(string(data), "\ufeff")
	if strings.TrimSpace(text) == "" {
		return "", "", fmt.Errorf("附件 %s 没有可读取的文本内容", name)
	}
	if mimeType == "application/octet-stream" || mimeType == "" {
		mimeType = "text/plain"
	}
	return text, mimeType, nil
}

func textAttachmentTypeSupported(mimeType string, extension string) bool {
	if strings.HasPrefix(mimeType, "text/") {
		return true
	}
	switch mimeType {
	case "application/json", "application/ld+json", "application/xml", "application/javascript",
		"application/x-javascript", "application/yaml", "application/x-yaml", "application/toml",
		"application/sql", "application/graphql":
		return true
	}
	if mimeType != "" && mimeType != "application/octet-stream" {
		return false
	}
	switch extension {
	case ".txt", ".md", ".markdown", ".json", ".jsonl", ".yaml", ".yml", ".xml", ".csv", ".tsv",
		".go", ".js", ".jsx", ".ts", ".tsx", ".vue", ".py", ".rb", ".rs", ".java", ".kt",
		".c", ".h", ".cc", ".cpp", ".cs", ".swift", ".sh", ".zsh", ".fish", ".ps1", ".sql",
		".html", ".css", ".scss", ".less", ".toml", ".ini", ".conf", ".env", ".graphql":
		return true
	default:
		return false
	}
}

func formatExtractedFileForModel(name string, mimeType string, content string) string {
	return fmt.Sprintf("[Untrusted attachment text; file: %q; MIME: %q]\n%s", strings.TrimSpace(name), strings.TrimSpace(mimeType), content)
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
