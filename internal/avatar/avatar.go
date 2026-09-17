package avatar

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

const MaxBytes = 2 * 1024 * 1024

var supportedMIMETypes = map[string]bool{
	"image/png":  true,
	"image/jpeg": true,
	"image/gif":  true,
	"image/webp": true,
}

// NormalizeDataURL validates an avatar and returns a canonical data URL.
// Empty input clears the avatar. SVG is intentionally excluded because avatars
// are rendered in privileged desktop WebViews and must not contain active markup.
func NormalizeDataURL(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	if !strings.HasPrefix(value, "data:") {
		return "", errors.New("头像必须是图片 Data URL")
	}
	comma := strings.IndexByte(value, ',')
	if comma < 0 {
		return "", errors.New("头像 Data URL 无效")
	}
	header := strings.ToLower(strings.TrimSpace(value[5:comma]))
	parts := strings.Split(header, ";")
	if len(parts) != 2 || parts[1] != "base64" || !supportedMIMETypes[parts[0]] {
		return "", errors.New("头像仅支持 PNG、JPEG、GIF 或 WebP")
	}
	data, err := base64.StdEncoding.DecodeString(strings.TrimSpace(value[comma+1:]))
	if err != nil {
		return "", fmt.Errorf("头像 Base64 无效: %w", err)
	}
	if len(data) == 0 {
		return "", errors.New("头像图片为空")
	}
	if len(data) > MaxBytes {
		return "", fmt.Errorf("头像不能超过 %d MiB", MaxBytes/(1024*1024))
	}
	detected := strings.ToLower(strings.Split(http.DetectContentType(data), ";")[0])
	if !supportedMIMETypes[detected] {
		return "", errors.New("头像内容不是受支持的图片")
	}
	return "data:" + detected + ";base64," + base64.StdEncoding.EncodeToString(data), nil
}
