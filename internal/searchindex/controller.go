package searchindex

import (
	"context"
	"sync"
	"time"
)

// Controller 统一管理可丢弃索引的懒加载、后台刷新和退出等待。
// Wails 适配层只负责参数与 DTO，不再各自管理后台线程和数据库生命周期。
type Controller struct {
	path      string
	ctx       context.Context
	cancel    context.CancelFunc
	mu        sync.Mutex
	writer    sync.Mutex
	active    sync.WaitGroup
	index     *Index
	states    map[string]refreshState
	closeOnce sync.Once
	closeErr  error
}

type Status struct {
	Updating bool
	Error    string
}
type refreshState struct {
	Status
	checked time.Time
}

func NewController(path string) *Controller {
	ctx, cancel := context.WithCancel(context.Background())
	return &Controller{path: path, ctx: ctx, cancel: cancel, states: make(map[string]refreshState)}
}

// Acquire 返回已提交的数据视图，后台刷新不阻塞当前查询。
// release 必须在查询结束后执行；同一键在刷新期间以及十秒内不会重复启动任务。
func (c *Controller) Acquire(key string, refresh func(context.Context, *Index) error) (*Index, Status, func(), error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.ctx.Err(); err != nil {
		return nil, Status{}, nil, err
	}
	if c.index == nil {
		index, err := Open(c.path)
		if err != nil {
			return nil, Status{}, nil, err
		}
		c.index = index
	}
	state := c.states[key]
	if !state.Updating && time.Since(state.checked) > 10*time.Second {
		state.Status = Status{Updating: true}
		c.states[key] = state
		c.active.Add(1)
		go c.refresh(key, refresh)
	}
	c.active.Add(1)
	var once sync.Once
	return c.index, state.Status, func() { once.Do(c.active.Done) }, nil
}

func (c *Controller) refresh(key string, refresh func(context.Context, *Index) error) {
	defer c.active.Done()
	ctx, cancel := context.WithTimeout(c.ctx, 2*time.Minute)
	defer cancel()
	// 一个数据库只有一个索引写入者，查询可以继续使用已提交的事务。
	c.writer.Lock()
	err := ctx.Err()
	if err == nil {
		err = refresh(ctx, c.index)
	}
	c.writer.Unlock()
	state := refreshState{checked: time.Now()}
	if err != nil && c.ctx.Err() == nil {
		state.Error = err.Error()
	}
	c.mu.Lock()
	c.states[key] = state
	c.mu.Unlock()
}

func (c *Controller) Close() error {
	c.closeOnce.Do(func() {
		// 取消与 Acquire 的 Add 共用锁，保证 Wait 开始后没有新读者进入。
		c.mu.Lock()
		c.cancel()
		c.mu.Unlock()
		c.active.Wait()
		if c.index != nil {
			c.closeErr = c.index.Close()
		}
	})
	return c.closeErr
}
