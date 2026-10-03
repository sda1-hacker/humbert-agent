package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/sda1-hacker/humbert-agent/internal/component"
	agentruntime "github.com/sda1-hacker/humbert-agent/internal/runtime"
)

type BootstrapOption func(*bootstrapOptions)
type bootstrapOptions struct {
	modules []component.Installer
}

// WithModules 为 Bootstrap 追加模块，既有模块及桌面服务无需修改。
func WithModules(installers ...component.Installer) BootstrapOption {
	return func(options *bootstrapOptions) { options.modules = append(options.modules, installers...) }
}

type mountedModule struct {
	component.Module
	startAttempted bool
}
type moduleManager struct{ modules []*mountedModule }

func (m *moduleManager) install(ctx context.Context, host component.Host, resolver *agentruntime.Resolver, installers []component.Installer) error {
	ids := make(map[string]bool)
	for _, install := range installers {
		if install == nil {
			return errors.New("模块构造器不能为空")
		}
		module, err := install(ctx, host)
		if err != nil {
			return err
		}
		id := strings.TrimSpace(module.ID)
		invalid := id == "" || id != module.ID || ids[id] || (module.Start != nil && module.Stop == nil)
		if invalid {
			// 该模块尚未加入所有权清单，局部校验失败时立即归还它的资源。
			if module.Close != nil {
				cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
				cleanupErr := module.Close(cleanupCtx)
				cancel()
				if cleanupErr != nil {
					return errors.Join(fmt.Errorf("模块 %q 注册信息不合法", module.ID), cleanupErr)
				}
			}
			return fmt.Errorf("模块 %q 缺少稳定 ID、重复注册或缺少后台停止方法", module.ID)
		}
		ids[id] = true
		m.modules = append(m.modules, &mountedModule{Module: module})
		for _, provider := range module.Capabilities {
			if err := resolver.RegisterCapabilityProvider(provider); err != nil {
				return fmt.Errorf("注册模块 %q 失败: %w", id, err)
			}
		}
	}
	return nil
}

func (m *moduleManager) start(ctx context.Context) error {
	for _, module := range m.modules {
		if module.Start == nil {
			continue
		}
		// 即使 Start 在创建部分 Worker 后失败，停止阶段也必须调用 Stop。
		module.startAttempted = true
		if err := module.Start(ctx); err != nil {
			return fmt.Errorf("启动模块 %q 失败: %w", module.ID, err)
		}
	}
	return nil
}

func (m *moduleManager) stop(ctx context.Context) error {
	var failures []error
	for i := len(m.modules) - 1; i >= 0; i-- {
		module := m.modules[i]
		if module.startAttempted && module.Stop != nil {
			if err := module.Stop(ctx); err != nil {
				failures = append(failures, fmt.Errorf("停止模块 %q: %w", module.ID, err))
			}
		}
	}
	return errors.Join(failures...)
}

func (m *moduleManager) close(ctx context.Context) error {
	var failures []error
	for i := len(m.modules) - 1; i >= 0; i-- {
		module := m.modules[i]
		if module.Close != nil {
			if err := module.Close(ctx); err != nil {
				failures = append(failures, fmt.Errorf("关闭模块 %q: %w", module.ID, err))
			}
		}
	}
	return errors.Join(failures...)
}
