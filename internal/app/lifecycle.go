package app

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

// 关闭分三阶段：先停止新工作，再等待已有运行，最后关闭它们使用的资源。
// 阶段内按构造顺序逆序清理；同一张清单同时用于 Bootstrap 失败回滚与正常退出。
const (
	stopWork = iota
	finishRuns
	closeResources
)

type cleanupEntry struct {
	phase int
	name  string
	close func(context.Context) error
}

type lifecycle struct {
	entries []cleanupEntry
	once    sync.Once
	err     error
}

// add 仅在单线程 Bootstrap 期间调用，资源构造成功后立即登记其所有权。
func (l *lifecycle) add(phase int, name string, close func(context.Context) error) {
	l.entries = append(l.entries, cleanupEntry{phase: phase, name: name, close: close})
}

// close 每个资源最多清理一次；一个失败不会阻止其他资源释放。
func (l *lifecycle) close(ctx context.Context) error {
	l.once.Do(func() {
		var failures []error
		for phase := stopWork; phase <= closeResources; phase++ {
			for i := len(l.entries) - 1; i >= 0; i-- {
				entry := l.entries[i]
				if entry.phase != phase {
					continue
				}
				if err := entry.close(ctx); err != nil {
					failures = append(failures, fmt.Errorf("关闭 %s 失败: %w", entry.name, err))
				}
			}
		}
		l.err = errors.Join(failures...)
	})
	return l.err
}
