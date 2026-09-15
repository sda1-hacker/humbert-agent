package eventbus

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
)

var ErrClosed = errors.New(
	"事件总线已关闭",
)

// Handler 表示一个同步事件处理函数。
//
// EventBus 自身绝不创建 goroutine。
// 这样不会把生命周期不可控的后台任务隐藏在基础设施层中。
//
// 如果业务需要异步消费事件，应由对应 Service 创建受 Context 控制的
// Worker，而不是由 EventBus 擅自启动后台 goroutine。
type Handler func(
	ctx context.Context,
	payload any,
)

// Bus 是进程内同步事件总线。
//
// v0.1 主要用于：
//   - Runtime → Wails；
//   - Model Registry → UI；
//   - Application lifecycle。
//
// 后续 Skills、MCP 等 Registry 同样可以复用。
//
// EventBus 不是可靠消息队列，应用退出后事件不会持久化。
type Bus struct {
	mu sync.RWMutex

	closed bool

	nextID atomic.Uint64

	handlers map[string]map[uint64]Handler
}

// New 创建新的 EventBus。
func New() *Bus {
	return &Bus{
		handlers: make(
			map[string]map[uint64]Handler,
		),
	}
}

// Subscribe 订阅指定 Topic。
//
// cancel 可以安全重复调用。
func (b *Bus) Subscribe(
	topic string,
	handler Handler,
) (func(), error) {
	if topic == "" {
		return nil, errors.New(
			"事件主题不能为空",
		)
	}

	if handler == nil {
		return nil, errors.New(
			"事件 Handler 不能为空",
		)
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	if b.closed {
		return nil, ErrClosed
	}

	id := b.nextID.Add(1)

	if _, exists := b.handlers[topic]; !exists {
		b.handlers[topic] =
			make(map[uint64]Handler)
	}

	b.handlers[topic][id] =
		handler

	var once sync.Once

	cancel := func() {
		once.Do(func() {
			b.mu.Lock()
			defer b.mu.Unlock()

			handlers, exists :=
				b.handlers[topic]
			if !exists {
				return
			}

			delete(
				handlers,
				id,
			)

			if len(handlers) == 0 {
				delete(
					b.handlers,
					topic,
				)
			}
		})
	}

	return cancel, nil
}

// Publish 同步发布事件。
//
// 发布时先复制 Handler，之后释放读锁，再真正执行回调。
// 因此 Handler 在执行时可以安全取消自己的订阅。
//
// Handler panic 会被转换成 error，而不是直接让整个应用崩溃。
func (b *Bus) Publish(
	ctx context.Context,
	topic string,
	payload any,
) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf(
			"发布事件被取消: %w",
			err,
		)
	}

	b.mu.RLock()

	if b.closed {
		b.mu.RUnlock()

		return ErrClosed
	}

	topicHandlers :=
		b.handlers[topic]

	handlers := make(
		[]Handler,
		0,
		len(topicHandlers),
	)

	for _, handler := range topicHandlers {
		handlers = append(
			handlers,
			handler,
		)
	}

	b.mu.RUnlock()

	var publishErrors []error

	for index, handler := range handlers {

		if err := ctx.Err(); err != nil {
			return fmt.Errorf(
				"发布事件被取消: %w",
				err,
			)
		}

		if err := callHandler(
			ctx,
			handler,
			payload,
		); err != nil {
			publishErrors = append(
				publishErrors,
				fmt.Errorf(
					"事件 Handler #%d 执行失败: %w",
					index,
					err,
				),
			)
		}
	}

	return errors.Join(
		publishErrors...,
	)
}

func callHandler(
	ctx context.Context,
	handler Handler,
	payload any,
) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf(
				"事件 Handler panic: %v",
				recovered,
			)
		}
	}()

	handler(
		ctx,
		payload,
	)

	return nil
}

// Close 关闭 EventBus 并清理全部订阅。
func (b *Bus) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.closed {
		return
	}

	b.closed = true

	b.handlers =
		make(
			map[string]map[uint64]Handler,
		)
}
