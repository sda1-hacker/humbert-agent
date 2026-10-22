package knowledge

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"

	"github.com/sda1-hacker/humbert-agent/internal/logging"
	"github.com/sda1-hacker/humbert-agent/internal/models"
	agentruntime "github.com/sda1-hacker/humbert-agent/internal/runtime"
)

const TopicChanged = "knowledge.changed"

type Manager struct {
	mu          sync.Mutex
	host        agentruntime.ModuleHost
	imageModels *models.Registry
	directory   string
	state       diskState
	engine      *engine
	connecting  *engineBuild
	active      int
	closed      bool
	ctx         context.Context
	cancel      context.CancelFunc
	wg          sync.WaitGroup
	imports     chan struct{}
	queries     chan struct{}
	runs        map[string]context.CancelFunc
}

func New(ctx context.Context, host agentruntime.ModuleHost, images *models.Registry) (*Manager, error) {
	if host.DataDir == "" || host.Logger == nil || host.Credentials == nil {
		return nil, errors.New("知识库缺少数据目录、日志或凭据存储")
	}
	stopping, cancel := context.WithCancel(context.Background())
	m := &Manager{host: host, imageModels: images, directory: filepath.Join(host.DataDir, "knowledge"), ctx: stopping, cancel: cancel,
		imports: make(chan struct{}, 2), queries: make(chan struct{}, 2), runs: map[string]context.CancelFunc{}}
	if err := m.load(ctx); err != nil {
		cancel()
		return nil, err
	}
	return m, nil
}

func (m *Manager) Module() agentruntime.Module {
	return agentruntime.Module{ID: "knowledge", Capabilities: []agentruntime.CapabilityProvider{m},
		Start: func(context.Context) error { return nil }, Stop: m.Stop, Close: m.Close}
}

func (m *Manager) ID() string { return "knowledge" }

// logFailure 只记录脱敏错误，不把数据库 DSN、文档正文或模型密钥写入日志。
func logFailure(err error) string {
	if err == nil {
		return ""
	}
	return logging.SafeErrorText(fmt.Errorf("%w", err), 1200)
}
