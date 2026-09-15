package atomicfile

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
)

// WriteJSON 使用“同目录临时文件 + fsync + rename”的方式原子替换 JSON 文件。
//
// 该函数只负责单个文件的可靠落盘，不负责业务级并发控制。调用方如果可能
// 同时修改同一份逻辑文档，必须在更高层使用互斥锁串行化“读取-修改-写回”流程。
//
// 设计原因：Provider、Model、Agent Profile 等配置文件都属于小型状态文档，
// 与高频追加的 Session JSONL 不同。配置更新时重写完整 JSON 能保持文件可读性，
// 而临时文件提交可以避免进程异常退出后留下半截 JSON。
//
// 安全边界：
//   - parentDir 必须由上层从受控 Humbert 数据根目录推导；
//   - 如果目标路径已经是符号链接，则拒绝覆盖，避免通过链接把配置写到数据根目录外；
//   - Unix 系统会把目标文件权限收紧到 perm。
func WriteJSON(
	ctx context.Context,
	path string,
	perm os.FileMode,
	value any,
) error {
	if ctx == nil {
		return errors.New("context.Context 不能为空")
	}

	if err := ctx.Err(); err != nil {
		return fmt.Errorf("写入 JSON 文件被取消: %w", err)
	}

	if path == "" {
		return errors.New("JSON 文件路径不能为空")
	}

	parentDir := filepath.Dir(path)
	if err := ensureRealDirectory(parentDir); err != nil {
		return fmt.Errorf("准备 JSON 文件父目录失败: %w", err)
	}

	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("拒绝覆盖符号链接 JSON 文件: %s", path)
		}
		if info.IsDir() {
			return fmt.Errorf("JSON 文件路径指向目录: %s", path)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("检查 JSON 文件状态失败: %w", err)
	}

	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("编码 JSON 失败: %w", err)
	}
	data = append(data, '\n')

	temp, err := os.CreateTemp(parentDir, ".humbert-json-*")
	if err != nil {
		return fmt.Errorf("创建 JSON 临时文件失败: %w", err)
	}

	tempName := temp.Name()
	committed := false
	defer func() {
		_ = temp.Close()
		if !committed {
			_ = os.Remove(tempName)
		}
	}()

	if runtime.GOOS != "windows" {
		if err := temp.Chmod(perm); err != nil {
			return fmt.Errorf("设置 JSON 临时文件权限失败: %w", err)
		}
	}

	if err := writeFull(temp, data); err != nil {
		return fmt.Errorf("写入 JSON 临时文件失败: %w", err)
	}

	if err := temp.Sync(); err != nil {
		return fmt.Errorf("同步 JSON 临时文件失败: %w", err)
	}

	if err := temp.Close(); err != nil {
		return fmt.Errorf("关闭 JSON 临时文件失败: %w", err)
	}

	if err := replaceFile(tempName, path); err != nil {
		return fmt.Errorf("提交 JSON 文件失败: %w", err)
	}

	if runtime.GOOS != "windows" {
		if err := os.Chmod(path, perm); err != nil {
			return fmt.Errorf("设置 JSON 文件权限失败: %w", err)
		}
	}

	committed = true
	return nil
}

// ReadJSON 读取并严格解析一个 JSON 文件。
//
// 该函数不会吞掉 os.ErrNotExist，调用方可以通过 errors.Is 判断是否需要执行
// 首次初始化逻辑。JSON 中出现未知字段会被拒绝，以便尽早发现用户手工编辑、
// 旧版本迁移或代码 Schema 演进造成的不一致，而不是静默忽略潜在配置错误。
func ReadJSON(
	ctx context.Context,
	path string,
	value any,
) error {
	if ctx == nil {
		return errors.New("context.Context 不能为空")
	}

	if err := ctx.Err(); err != nil {
		return fmt.Errorf("读取 JSON 文件被取消: %w", err)
	}

	if path == "" {
		return errors.New("JSON 文件路径不能为空")
	}

	if err := validateRealDirectory(filepath.Dir(path)); err != nil {
		return fmt.Errorf("验证 JSON 文件父目录失败: %w", err)
	}

	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("拒绝读取符号链接 JSON 文件: %s", path)
	}
	if info.IsDir() {
		return fmt.Errorf("JSON 文件路径指向目录: %s", path)
	}

	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()

	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(value); err != nil {
		return fmt.Errorf("解析 JSON 文件 %s 失败: %w", path, err)
	}

	var extra any
	if err := decoder.Decode(&extra); err == nil {
		return fmt.Errorf("JSON 文件 %s 包含多个顶层 JSON 值", path)
	} else if !errors.Is(err, io.EOF) {
		return fmt.Errorf("JSON 文件 %s 尾部包含非法内容: %w", path, err)
	}

	return nil
}

// ensureRealDirectory 创建或验证 JSON 文件的直接父目录。
//
// AtomicFile 的调用方通常已经从受控 Humbert Root 推导路径；这里仍然拒绝
// 直接父目录本身是符号链接，避免配置文件通过 config/ 或 agent-id/ 链接写出
// 预期目录。更高层的 Root 目录约束仍由 config/agents/transcript 各自负责。
func ensureRealDirectory(path string) error {
	info, err := os.Lstat(path)
	if err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return errors.New("JSON 文件父目录不能是符号链接")
		}
		if !info.IsDir() {
			return errors.New("JSON 文件父路径不是目录")
		}
		return nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return err
	}

	if err := os.MkdirAll(path, 0o700); err != nil {
		return err
	}
	return validateRealDirectory(path)
}

// validateRealDirectory 验证 JSON 文件直接父目录存在、为真实目录且不是 symlink。
func validateRealDirectory(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return errors.New("JSON 文件父目录不能是符号链接")
	}
	if !info.IsDir() {
		return errors.New("JSON 文件父路径不是目录")
	}
	return nil
}

func writeFull(file *os.File, data []byte) error {
	for len(data) > 0 {
		written, err := file.Write(data)
		if err != nil {
			return err
		}
		if written <= 0 {
			return errors.New("写入 JSON 文件时发生短写")
		}
		data = data[written:]
	}
	return nil
}

func replaceFile(source string, destination string) error {
	if err := os.Rename(source, destination); err == nil {
		return nil
	}

	// Windows 无法保证 Rename 可以直接覆盖已存在文件，因此使用备份切换。
	// 如果新文件提交失败，会尽力恢复旧文件。
	backup := destination + ".backup"
	_ = os.Remove(backup)

	oldExists := false
	if _, err := os.Stat(destination); err == nil {
		oldExists = true
		if err := os.Rename(destination, backup); err != nil {
			return fmt.Errorf("备份旧 JSON 文件失败: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("检查旧 JSON 文件失败: %w", err)
	}

	if err := os.Rename(source, destination); err != nil {
		if oldExists {
			_ = os.Rename(backup, destination)
		}
		return err
	}

	if oldExists {
		if err := os.Remove(backup); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("清理 JSON 备份文件失败: %w", err)
		}
	}

	return nil
}
