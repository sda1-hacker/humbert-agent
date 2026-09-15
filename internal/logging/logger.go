package logging

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/sda1-hacker/humbert-agent/internal/config"
)

var (
	bearerPattern = regexp.MustCompile(
		`(?i)(authorization\s*[:=]\s*bearer\s+)[^\s,;]+`,
	)

	secretKVPattern = regexp.MustCompile(
		`(?i)((?:api[_-]?key|access[_-]?token|refresh[_-]?token|token|secret|password|cookie)\s*[:=]\s*)[^\s,;]+`,
	)

	apiKeyPattern = regexp.MustCompile(
		`\bsk-[A-Za-z0-9_-]{12,}\b`,
	)
)

// Logger 是 Humbert 业务代码统一使用的结构化日志组件。
//
// 底层采用标准库 slog，既避免业务代码绑定特定第三方 logger，
// 又能够直接作为 Wails v3 的 system logger。
//
// Logger 同时承担基础敏感信息脱敏责任。所有 string/error 类型日志字段
// 都会在真正进入 slog Handler 前经过 RedactText。
type Logger struct {
	logger *slog.Logger
	file   *os.File
	level  slog.Level

	closeOnce sync.Once
}

// NewBootstrap 创建应用配置尚未加载完成之前使用的启动日志器。
//
// Bootstrap Logger 只写 stderr，不持有文件资源。
// 它仍然属于统一 logging package，不允许 main.go 使用 fmt.Println
// 或 log.Println 作为替代。
func NewBootstrap() *Logger {
	level := slog.LevelInfo

	handler := slog.NewTextHandler(
		os.Stderr,
		&slog.HandlerOptions{
			Level: level,
		},
	)

	return &Logger{
		logger: slog.New(handler),
		level:  level,
	}
}

// New 创建正式应用 Logger。
//
// 日志同时写入 stderr 和 ~/.humbert-agent/logs/humbert.log。
// 日志路径不允许用户配置，从而保证所有 Humbert 状态集中存储。
func New(
	cfg config.LoggingConfig,
	logFile string,
) (*Logger, error) {
	level, err := parseLevel(cfg.Level)
	if err != nil {
		return nil, err
	}

	if strings.TrimSpace(logFile) == "" {
		return nil, fmt.Errorf(
			"日志文件路径不能为空",
		)
	}

	if err := os.MkdirAll(
		filepath.Dir(logFile),
		0700,
	); err != nil {
		return nil, fmt.Errorf(
			"创建日志目录失败: %w",
			err,
		)
	}

	file, err := os.OpenFile(
		logFile,
		os.O_CREATE|
			os.O_APPEND|
			os.O_WRONLY,
		0600,
	)
	if err != nil {
		return nil, fmt.Errorf(
			"打开日志文件失败: %w",
			err,
		)
	}

	if err := file.Chmod(0600); err != nil {
		_ = file.Close()

		return nil, fmt.Errorf(
			"设置日志文件权限失败: %w",
			err,
		)
	}

	writer := io.MultiWriter(
		os.Stderr,
		file,
	)

	options := &slog.HandlerOptions{
		Level: level,
	}

	var handler slog.Handler

	switch strings.ToLower(
		strings.TrimSpace(cfg.Format),
	) {
	case "json":
		handler = slog.NewJSONHandler(
			writer,
			options,
		)

	case "text":
		handler = slog.NewTextHandler(
			writer,
			options,
		)

	default:
		_ = file.Close()

		return nil, fmt.Errorf(
			"不支持的日志格式: %q",
			cfg.Format,
		)
	}

	return &Logger{
		logger: slog.New(handler),
		file:   file,
		level:  level,
	}, nil
}

func parseLevel(value string) (slog.Level, error) {
	switch strings.ToLower(
		strings.TrimSpace(value),
	) {
	case "debug":
		return slog.LevelDebug, nil

	case "info":
		return slog.LevelInfo, nil

	case "warn":
		return slog.LevelWarn, nil

	case "error":
		return slog.LevelError, nil

	default:
		return 0, fmt.Errorf(
			"不支持的日志级别: %q",
			value,
		)
	}
}

// Slog 返回底层 slog.Logger。
//
// 主要提供给 Wails 等基础设施使用。
// 普通业务代码仍应使用 Logger.Debug/Info/Warn/Error。
func (l *Logger) Slog() *slog.Logger {
	return l.logger
}

// Level 返回当前日志级别，可直接提供给 Wails LogLevel。
func (l *Logger) Level() slog.Level {
	return l.level
}

// Debug 输出 Debug 级别结构化日志。
func (l *Logger) Debug(
	ctx context.Context,
	message string,
	attrs ...any,
) {
	l.logger.DebugContext(
		ctx,
		message,
		sanitizeAttrs(attrs)...,
	)
}

// Info 输出 Info 级别结构化日志。
func (l *Logger) Info(
	ctx context.Context,
	message string,
	attrs ...any,
) {
	l.logger.InfoContext(
		ctx,
		message,
		sanitizeAttrs(attrs)...,
	)
}

// Warn 输出 Warn 级别结构化日志。
func (l *Logger) Warn(
	ctx context.Context,
	message string,
	attrs ...any,
) {
	l.logger.WarnContext(
		ctx,
		message,
		sanitizeAttrs(attrs)...,
	)
}

// Error 输出 Error 级别结构化日志。
func (l *Logger) Error(
	ctx context.Context,
	message string,
	attrs ...any,
) {
	l.logger.ErrorContext(
		ctx,
		message,
		sanitizeAttrs(attrs)...,
	)
}

// Duration 生成统一 duration_ms 字段。
func Duration(start time.Time) slog.Attr {
	return slog.Int64(
		"duration_ms",
		time.Since(start).Milliseconds(),
	)
}

// SafeErrorText 将 error 转换成允许进入日志和前端错误消息的文本。
func SafeErrorText(
	err error,
	maxLength int,
) string {
	if err == nil {
		return ""
	}

	return RedactText(
		err.Error(),
		maxLength,
	)
}

// RedactText 对常见 Token、API Key、Password、Cookie 等信息进行脱敏。
//
// 这只是最后一道保护。
// 根本原则仍然是敏感值本身不应该被传入日志。
func RedactText(
	value string,
	maxLength int,
) string {
	value = bearerPattern.ReplaceAllString(
		value,
		"${1}[REDACTED]",
	)

	value = secretKVPattern.ReplaceAllString(
		value,
		"${1}[REDACTED]",
	)

	value = apiKeyPattern.ReplaceAllString(
		value,
		"[REDACTED]",
	)

	if maxLength > 0 &&
		len(value) > maxLength {
		value =
			value[:maxLength] +
				"...[TRUNCATED]"
	}

	return value
}

// sanitizeAttrs 尽可能在日志组件内部自动保护 string/error 属性。
//
// attrs 使用 slog 常见的 key,value 形式。
// 对其他结构化类型保持原值，业务层仍不得把包含秘密的复杂对象整体传入日志。
func sanitizeAttrs(
	attrs []any,
) []any {
	result := make(
		[]any,
		len(attrs),
	)

	for index, value := range attrs {
		switch typed := value.(type) {
		case string:
			result[index] =
				RedactText(
					typed,
					4096,
				)

		case error:
			result[index] =
				SafeErrorText(
					typed,
					4096,
				)

		case slog.Attr:
			result[index] =
				sanitizeAttr(typed)

		default:
			result[index] = value
		}
	}

	return result
}

func sanitizeAttr(
	attr slog.Attr,
) slog.Attr {
	if attr.Value.Kind() == slog.KindString {
		return slog.String(
			attr.Key,
			RedactText(
				attr.Value.String(),
				4096,
			),
		)
	}

	return attr
}

// Close 刷新并关闭 Logger 自己持有的日志文件。
//
// 方法可以安全重复调用。
func (l *Logger) Close() error {
	var closeErr error

	l.closeOnce.Do(func() {
		if l.file == nil {
			return
		}

		if err := l.file.Sync(); err != nil {
			closeErr = fmt.Errorf(
				"同步日志文件失败: %w",
				err,
			)
		}

		if err := l.file.Close(); err != nil &&
			closeErr == nil {
			closeErr = fmt.Errorf(
				"关闭日志文件失败: %w",
				err,
			)
		}
	})

	return closeErr
}
