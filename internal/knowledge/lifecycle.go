package knowledge

import (
	"context"
	"errors"
	"slices"
	"sync"
	"time"
)

// engineBuild 合并同一次连接初始化；done 关闭后 err 不再变化。
// 连接和关闭在锁外执行，配置快照与最终安装仍由 Manager 锁保护。
type engineBuild struct {
	done       chan struct{}
	initialize bool
	err        error
}

func (m *Manager) acquire(ctx context.Context, revision uint64, initialize bool) (*engine, context.Context, func(), error) {
	ctx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(m.ctx, cancel)
	fail := func() { stop(); cancel() }
	for {
		m.mu.Lock()
		if err := m.checkAcquireLocked(ctx, revision); err != nil {
			m.mu.Unlock()
			fail()
			return nil, nil, nil, err
		}
		if pending := m.connecting; pending != nil {
			m.mu.Unlock()
			select {
			case <-ctx.Done():
				fail()
				return nil, nil, nil, ctx.Err()
			case <-pending.done:
				if pending.err != nil {
					fail()
					return nil, nil, nil, pending.err
				}
				// 普通连接不能代替显式迁移；并发的显式初始化只执行一次。
				initialize = initialize && !pending.initialize
				continue
			}
		}
		if initialize && m.active != 0 {
			m.mu.Unlock()
			fail()
			return nil, nil, nil, errors.New("请等待知识库任务结束后初始化")
		}
		stale := m.engine != nil && m.imageModels != nil && len(m.state.Connections) > 0 && m.engine.providerRevision != m.imageModels.Revision()
		if stale && m.active != 0 {
			m.mu.Unlock()
			fail()
			return nil, nil, nil, errors.New("供应商连接已更新，请等待当前知识库任务结束后重试")
		}
		if m.engine != nil && !initialize && !stale {
			e, release := m.retainEngineLocked(fail)
			m.mu.Unlock()
			return e, ctx, release, nil
		}

		pending := &engineBuild{done: make(chan struct{}), initialize: initialize}
		m.connecting = pending
		state := diskState{Revision: m.state.Revision, Values: clone(m.state.Values), Credentials: clone(m.state.Credentials), Connections: clone(m.state.Connections)}
		retired := m.engine
		m.engine = nil
		// Stop 与登记共用锁，不能在 Wait 开始后再添加连接或操作。
		m.wg.Add(1)
		m.mu.Unlock()

		if retired != nil {
			retired.Close()
		}
		connect, done := context.WithTimeout(ctx, 45*time.Second)
		e, err := m.buildEngine(connect, state, initialize)
		done()
		m.mu.Lock()
		if err == nil {
			err = m.checkAcquireLocked(ctx, state.Revision)
		}
		pending.err = err
		m.connecting = nil
		if err == nil {
			m.engine = e
		}
		var release func()
		if err == nil {
			_, release = m.retainEngineLocked(fail)
		}
		close(pending.done)
		m.mu.Unlock()
		if err != nil && e != nil {
			e.Close()
		}
		m.wg.Done()
		if err != nil {
			fail()
			return nil, nil, nil, err
		}
		return e, ctx, release, nil
	}
}

func (m *Manager) checkAcquireLocked(ctx context.Context, revision uint64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if m.closed || !m.state.Enabled {
		return errors.New("请先在设置中启用并配置 RAG")
	}
	if revision != 0 && revision != m.state.Revision {
		return errors.New("知识库配置或 Agent 绑定已变化，请重新发起本轮对话")
	}
	return nil
}

// retainEngineLocked 将同步查询和异步导入都纳入关闭等待；每次操作只释放一次。
func (m *Manager) retainEngineLocked(cleanup func()) (*engine, func()) {
	m.active++
	m.wg.Add(1)
	var once sync.Once
	return m.engine, func() {
		once.Do(func() {
			cleanup()
			m.mu.Lock()
			m.active--
			m.mu.Unlock()
			m.wg.Done()
		})
	}
}

// Initialize 是显式建库/迁移入口；不会调用 Embedding 或图片模型。
func (m *Manager) Initialize(ctx context.Context) error {
	e, ctx, release, err := m.acquire(ctx, 0, true)
	if err != nil {
		return err
	}
	defer release()
	m.mu.Lock()
	collections := slices.Clone(m.state.Collections)
	m.mu.Unlock()
	for _, c := range collections {
		if _, err := e.RecoverDocuments(ctx, c.ID); err != nil {
			return err
		}
	}
	m.host.Logger.Info(ctx, "rag.initialize", "database", e.config.Database.Name, "dimensions", e.config.Embedding.Dimensions)
	return nil
}

func (m *Manager) Stop(ctx context.Context) error {
	m.cancel()
	m.mu.Lock()
	m.closed = true
	m.mu.Unlock()
	done := make(chan struct{})
	go func() { m.wg.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (m *Manager) Close(ctx context.Context) error {
	if err := m.Stop(ctx); err != nil {
		return err
	}
	m.mu.Lock()
	e := m.engine
	m.engine = nil
	m.mu.Unlock()
	if e != nil {
		e.Close()
	}
	return nil
}
