package approval

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
)

// CheckpointStore 是 Humbert 为 Eino Approval Interrupt 提供的进程内 checkpoint 存储。
//
// 第一阶段刻意不把 checkpoint 持久化到磁盘。原因是 snapshot 中的 Model/Tool 实例、
// Context cancellation 与 Session reservation 都是进程态；桌面应用重启后恢复一个旧的
// 高风险 Tool 调用既不完整也不安全。应用关闭会取消全部 ActiveRun，长期授权规则仍由
// permission.Store 独立持久化。
//
// Get/Set/Delete 都复制 []byte，避免 Eino 与 Store 共享底层 slice 造成竞态或意外修改。
type CheckpointStore struct {
	mu   sync.RWMutex
	data map[string][]byte
}

// NewCheckpointStore 创建空的线程安全 checkpoint store。
func NewCheckpointStore() *CheckpointStore {
	return &CheckpointStore{data: make(map[string][]byte)}
}

// Get 实现 adk.CheckPointStore。
func (s *CheckpointStore) Get(ctx context.Context, key string) ([]byte, bool, error) {
	if ctx == nil {
		return nil, false, errors.New("读取 Approval Checkpoint 失败: context.Context 不能为空")
	}
	if err := ctx.Err(); err != nil {
		return nil, false, fmt.Errorf("读取 Approval Checkpoint 被取消: %w", err)
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return nil, false, errors.New("读取 Approval Checkpoint 失败: key 不能为空")
	}

	s.mu.RLock()
	value, ok := s.data[key]
	if ok {
		value = append([]byte(nil), value...)
	}
	s.mu.RUnlock()
	return value, ok, nil
}

// Has 只检查 checkpoint 是否存在，不复制 payload。Runtime 在进入暂停态和恢复前用它
// 验证 Eino 已经完成 checkpoint 提交，避免出现“UI 可审批但实际上无法 Resume”的悬空状态。
func (s *CheckpointStore) Has(ctx context.Context, key string) (bool, error) {
	if ctx == nil {
		return false, errors.New("检查 Approval Checkpoint 失败: context.Context 不能为空")
	}
	if err := ctx.Err(); err != nil {
		return false, fmt.Errorf("检查 Approval Checkpoint 被取消: %w", err)
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return false, errors.New("检查 Approval Checkpoint 失败: key 不能为空")
	}
	s.mu.RLock()
	_, ok := s.data[key]
	s.mu.RUnlock()
	return ok, nil
}

// Set 实现 adk.CheckPointStore。
func (s *CheckpointStore) Set(ctx context.Context, key string, value []byte) error {
	if ctx == nil {
		return errors.New("写入 Approval Checkpoint 失败: context.Context 不能为空")
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("写入 Approval Checkpoint 被取消: %w", err)
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return errors.New("写入 Approval Checkpoint 失败: key 不能为空")
	}
	if len(value) == 0 {
		return errors.New("写入 Approval Checkpoint 失败: payload 不能为空")
	}

	copyValue := append([]byte(nil), value...)
	s.mu.Lock()
	s.data[key] = copyValue
	s.mu.Unlock()
	return nil
}

// Delete 实现 adk.CheckPointDeleter，并在 Turn 正常结束、失败、取消后主动释放 checkpoint。
func (s *CheckpointStore) Delete(ctx context.Context, key string) error {
	if ctx == nil {
		return errors.New("删除 Approval Checkpoint 失败: context.Context 不能为空")
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("删除 Approval Checkpoint 被取消: %w", err)
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return nil
	}
	s.mu.Lock()
	delete(s.data, key)
	s.mu.Unlock()
	return nil
}
