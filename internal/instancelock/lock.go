// Package instancelock 在访问应用数据前取得进程间独占锁。
package instancelock

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

var ErrInUse = errors.New("Humbert 数据目录正由另一个实例使用，请先关闭该实例")

type Lock struct {
	file *os.File
	once sync.Once
	err  error
}

// Acquire 非阻塞地取得目录所有权。锁放在数据目录旁，恢复数据时即使替换整个
// 数据目录，锁仍然有效。锁文件不删除，防止等待者与新启动者锁住不同 inode。
func Acquire(root string) (*Lock, error) {
	if strings.TrimSpace(root) == "" {
		return nil, errors.New("数据目录不能为空")
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	// 解析已有目录的别名，确保经符号链接启动也竞争同一把锁。
	canonical, err := filepath.EvalSymlinks(absolute)
	if errors.Is(err, os.ErrNotExist) {
		// 离线恢复允许指定尚不存在的新目录；先创建父目录以容纳稳定的锁文件。
		if parentErr := os.MkdirAll(filepath.Dir(absolute), 0o700); parentErr != nil {
			return nil, parentErr
		}
		parent, parentErr := filepath.EvalSymlinks(filepath.Dir(absolute))
		if parentErr != nil {
			return nil, parentErr
		}
		canonical, err = filepath.Join(parent, filepath.Base(absolute)), nil
	}
	if err != nil {
		return nil, fmt.Errorf("解析数据目录失败: %w", err)
	}
	file, err := os.OpenFile(canonical+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("打开实例锁失败: %w", err)
	}
	if err := tryLock(file); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("锁定数据目录失败: %w", err)
	}
	return &Lock{file: file}, nil
}

// Close 释放句柄即释放系统锁；异常退出时也由操作系统回收，不需要清理过期 PID。
func (l *Lock) Close() error {
	l.once.Do(func() { l.err = l.file.Close() })
	return l.err
}
