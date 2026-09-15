package builtin

import (
	"errors"
	"fmt"
)

// FileLimits 描述 Builtin File Tool 的资源限制。
//
// 这个结构本身不读取配置。
//
// 下一步 Application Wiring 时会由：
//
//	Viper Config
//	    ↓
//	config.ToolConfig
//	    ↓
//	FileLimits
//
// 注入。
//
// 这样 Tool Package 不依赖 Viper，也不会自行读取环境变量或配置文件。
type FileLimits struct {
	// MaxReadableFileBytes 是 read_file 允许读取的最大文件大小。
	//
	// 超过后直接拒绝，不尝试把大型日志或数据库文件塞进 LLM Context。
	MaxReadableFileBytes int64

	// MaxReadOutputBytes 是一次 read_file 最多返回给模型的文本字节数。
	MaxReadOutputBytes int

	// DefaultReadLines 是没有指定 line_count 时的默认行数。
	DefaultReadLines int

	// MaxReadLines 是一次 read_file 最多返回的行数。
	MaxReadLines int

	// MaxListEntries 是一次 list_files 最多返回的目录项数量。
	MaxListEntries int
}

// Validate 校验 File Tool Limits。
func (c FileLimits) Validate() error {
	if c.MaxReadableFileBytes <= 0 {
		return errors.New(
			"MaxReadableFileBytes 必须大于 0",
		)
	}

	if c.MaxReadOutputBytes <= 0 {
		return errors.New(
			"MaxReadOutputBytes 必须大于 0",
		)
	}

	if c.DefaultReadLines <= 0 {
		return errors.New(
			"DefaultReadLines 必须大于 0",
		)
	}

	if c.MaxReadLines <= 0 {
		return errors.New(
			"MaxReadLines 必须大于 0",
		)
	}

	if c.DefaultReadLines >
		c.MaxReadLines {

		return fmt.Errorf(
			"DefaultReadLines(%d) 不能大于 MaxReadLines(%d)",
			c.DefaultReadLines,
			c.MaxReadLines,
		)
	}

	if c.MaxListEntries <= 0 {
		return errors.New(
			"MaxListEntries 必须大于 0",
		)
	}

	return nil
}
