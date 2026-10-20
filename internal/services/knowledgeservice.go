package services

import (
	"context"
	"errors"
	"sync"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/sda1-hacker/humbert-agent/internal/knowledge"
)

// KnowledgeService 是窄的桌面桥接层；文件路径只从原生选择器取得，不开放任意路径读写 API。
type KnowledgeService struct {
	deps        KnowledgeDependencies
	mu          sync.Mutex
	unsubscribe func()
}

func NewKnowledgeService(deps KnowledgeDependencies) *KnowledgeService {
	return &KnowledgeService{deps: deps}
}
func (*KnowledgeService) ServiceName() string      { return "KnowledgeService" }
func (s *KnowledgeService) State() knowledge.State { return s.deps.Knowledge.State() }
func (s *KnowledgeService) SaveSettings(ctx context.Context, req knowledge.SaveSettingsRequest) (knowledge.State, error) {
	return s.deps.Knowledge.SaveSettings(ctx, req)
}
func (s *KnowledgeService) Initialize(ctx context.Context) error {
	return s.deps.Knowledge.Initialize(ctx)
}
func (s *KnowledgeService) SaveCollection(ctx context.Context, c knowledge.Collection) (knowledge.State, error) {
	for _, id := range c.AgentIDs {
		if _, err := s.deps.Agents.Get(ctx, id); err != nil {
			return knowledge.State{}, errors.New("关联的 Agent 不存在，请刷新后选择")
		}
	}
	return s.deps.Knowledge.SaveCollection(ctx, c)
}
func (s *KnowledgeService) Documents(ctx context.Context, collection string, offset int) (knowledge.DocumentPage, error) {
	return s.deps.Knowledge.Documents(ctx, collection, offset)
}
func (s *KnowledgeService) Upload(ctx context.Context, collection string) ([]knowledge.Job, error) {
	app := application.Get()
	if app == nil {
		return nil, errors.New("桌面文件选择器不可用")
	}
	files, err := app.Dialog.OpenFile().SetTitle("上传知识库文档").CanChooseFiles(true).CanChooseDirectories(false).
		AddFilter("文档与图片", "*.pdf;*.docx;*.pptx;*.xlsx;*.md;*.txt;*.csv;*.html;*.htm;*.png;*.jpg;*.jpeg;*.webp;*.gif;*.bmp;*.tif;*.tiff").PromptForMultipleSelection()
	if err != nil {
		return nil, err
	}
	return s.deps.Knowledge.Upload(ctx, collection, files)
}
func (s *KnowledgeService) Cancel(ctx context.Context, collection, document string) error {
	return s.deps.Knowledge.Cancel(ctx, collection, document)
}
func (s *KnowledgeService) Retry(ctx context.Context, collection, document string) ([]knowledge.Job, error) {
	return s.deps.Knowledge.Retry(ctx, collection, document)
}
func (s *KnowledgeService) Reindex(ctx context.Context, collection, document string) ([]knowledge.Job, error) {
	return s.deps.Knowledge.Reindex(ctx, collection, document)
}
func (s *KnowledgeService) Delete(ctx context.Context, collection, document string) error {
	return s.deps.Knowledge.Delete(ctx, collection, document)
}
func (s *KnowledgeService) PreviewDocument(ctx context.Context, collection, document string, offset int) ([]knowledge.Source, error) {
	return s.deps.Knowledge.PreviewDocument(ctx, collection, document, offset)
}
func (s *KnowledgeService) Search(ctx context.Context, collection, query, mode string) (knowledge.SearchOutput, error) {
	return s.deps.Knowledge.PreviewSearch(ctx, collection, query, mode)
}
func (s *KnowledgeService) ReadSource(ctx context.Context, agentID, id string) (knowledge.Source, error) {
	return s.deps.Knowledge.ReadSource(ctx, agentID, id)
}
func (s *KnowledgeService) PreviewSource(ctx context.Context, id string) (knowledge.Source, error) {
	return s.deps.Knowledge.PreviewSource(ctx, id)
}
func (s *KnowledgeService) Download(ctx context.Context, collection, document string) error {
	name, err := s.deps.Knowledge.DocumentName(ctx, collection, document)
	if err != nil {
		return err
	}
	app := application.Get()
	if app == nil {
		return errors.New("桌面保存对话框不可用")
	}
	filename, err := app.Dialog.SaveFile().SetMessage("保存知识库原始文件").SetFilename(name).PromptForSingleSelection()
	if err != nil || filename == "" {
		return err
	}
	return s.deps.Knowledge.DownloadOriginal(ctx, collection, document, filename)
}
func (s *KnowledgeService) ServiceStartup(_ context.Context, _ application.ServiceOptions) error {
	unsubSources, err := s.deps.Events.Subscribe(knowledge.TopicSources, func(_ context.Context, payload any) {
		if app := application.Get(); app != nil {
			app.Event.Emit(knowledge.TopicSources, payload)
		}
	})
	if err != nil {
		return err
	}
	unsubscribe, err := s.deps.Events.Subscribe(knowledge.TopicChanged, func(_ context.Context, payload any) {
		if app := application.Get(); app != nil {
			app.Event.Emit(knowledge.TopicChanged, payload)
		}
	})
	if err != nil {
		unsubSources()
		return err
	}
	s.mu.Lock()
	s.unsubscribe = func() { unsubscribe(); unsubSources() }
	s.mu.Unlock()
	return nil
}
func (s *KnowledgeService) ServiceShutdown() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.unsubscribe != nil {
		s.unsubscribe()
		s.unsubscribe = nil
	}
	return nil
}
