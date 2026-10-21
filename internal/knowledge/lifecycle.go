package knowledge

import (
	"context"
	"errors"
	"slices"
	"sync"
	"time"
)

// acquire 把配置、外部连接和操作生命周期锁定在一起，禁止运行中换库或释放连接。
func (m *Manager) acquire(ctx context.Context, revision uint64, initialize bool) (*engine, context.Context, func(), error) {
	ctx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(m.ctx, cancel)
	fail := func() { stop(); cancel() }
	m.mu.Lock()
	if err := ctx.Err(); err != nil {
		m.mu.Unlock()
		fail()
		return nil, nil, nil, err
	}
	if m.closed || !m.state.Enabled {
		m.mu.Unlock()
		fail()
		return nil, nil, nil, errors.New("请先在设置中启用并配置 RAG")
	}
	if revision != 0 && revision != m.state.Revision {
		m.mu.Unlock()
		fail()
		return nil, nil, nil, errors.New("知识库配置或 Agent 绑定已变化，请重新发起本轮对话")
	}
	if initialize && m.active != 0 {
		m.mu.Unlock()
		fail()
		return nil, nil, nil, errors.New("请等待知识库任务结束后初始化")
	}
	// 共享供应商改动只影响后续操作；在途导入/检索继续使用它自己的冻结连接。
	if m.engine != nil && m.imageModels != nil && len(m.state.Connections) > 0 && m.engine.providerRevision != m.imageModels.Revision() {
		if m.active != 0 {
			m.mu.Unlock()
			fail()
			return nil, nil, nil, errors.New("供应商连接已更新，请等待当前知识库任务结束后重试")
		}
		m.engine.Close()
		m.engine = nil
	}
	if initialize && m.engine != nil {
		m.engine.Close()
		m.engine = nil
	}
	if m.engine == nil {
		connect, done := context.WithTimeout(ctx, 45*time.Second)
		var err error
		m.engine, err = m.buildEngine(connect, initialize)
		done()
		if err != nil {
			m.mu.Unlock()
			fail()
			return nil, nil, nil, err
		}
	}
	e := m.engine
	m.active++
	m.mu.Unlock()
	var once sync.Once
	release := func() { once.Do(func() { fail(); m.mu.Lock(); m.active--; m.mu.Unlock() }) }
	return e, ctx, release, nil
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
	defer m.mu.Unlock()
	if m.engine != nil {
		m.engine.Close()
		m.engine = nil
	}
	return nil
}
