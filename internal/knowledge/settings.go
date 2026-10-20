// Package knowledge 装配桌面知识库、后台导入和 Agent 能力；解析与检索继续复用 rag。
package knowledge

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/sda1-hacker/humbert-agent/internal/atomicfile"
	ragconfig "github.com/sda1-hacker/humbert-agent/internal/rag/config"
)

// 秘密单独保存到系统凭据库；配置与页面只保留引用或是否已填写。
var secretKeys = []string{"database.password", "embedding.api_key", "loader.image_recognition.api_key", "rerank_provider.api_key", "rerank_provider.headers"}

type Settings struct {
	Enabled    bool            `json:"enabled"`
	Revision   uint64          `json:"revision"`
	Values     map[string]any  `json:"values"`
	HasSecrets map[string]bool `json:"has_secrets"`
	Connected  bool            `json:"connected"`
	LogQueries bool            `json:"log_queries"`
}

type SaveSettingsRequest struct {
	Enabled  bool           `json:"enabled"`
	Revision uint64         `json:"revision"`
	Values   map[string]any `json:"values"`
	// 不传保持原值，传空字符串清除；密码从不回显。
	Secrets    map[string]string `json:"secrets"`
	LogQueries bool              `json:"log_queries"`
}

type Collection struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	AgentIDs    []string  `json:"agent_ids"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type diskState struct {
	Enabled     bool              `json:"enabled"`
	Revision    uint64            `json:"revision"`
	Values      map[string]any    `json:"values"`
	Credentials map[string]string `json:"credentials"`
	Collections []Collection      `json:"collections"`
	Jobs        []jobRecord       `json:"jobs"`
	LogQueries  bool              `json:"log_queries"`
}

type State struct {
	Settings    Settings     `json:"settings"`
	Collections []Collection `json:"collections"`
	Jobs        []Job        `json:"jobs"`
}

func (m *Manager) State() State {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.stateLocked()
}

// SaveSettings 与在途操作互斥：下一轮使用新配置，当前轮不能悄悄切到别的数据库或模型。
func (m *Manager) SaveSettings(ctx context.Context, req SaveSettingsRequest) (State, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed || m.active != 0 {
		return State{}, errors.New("请等待导入和检索结束后再修改 RAG 配置")
	}
	if req.Revision != m.state.Revision {
		return State{}, errors.New("配置已被更新，请刷新后重试")
	}
	if req.Values == nil {
		return State{}, errors.New("RAG 配置不能为空")
	}
	data, err := json.Marshal(req.Values)
	if err != nil || len(data) > 128<<10 {
		return State{}, errors.New("RAG 配置过大或格式无效")
	}
	values := clone(req.Values)
	for _, key := range secretKeys {
		if value := getValue(values, key); value != nil && value != "" {
			return State{}, fmt.Errorf("请使用独立密码输入框设置 %s", key)
		}
		deleteValue(values, key)
	}
	// 应用自行管理数据目录，页面不能把解析缓存、原件或图片目录指向任意位置。
	deleteValue(values, "source_directory")
	deleteValue(values, "loader.asset_directory")
	deleteValue(values, "loader.parse_options.cache_directory")
	values["collection_id"] = "desktop"
	values["ensure_schema"] = false
	cfg, err := ragconfig.FromValues(values)
	if err != nil {
		return State{}, err
	}
	if req.Enabled {
		if err := cfg.ValidatePostgres(); err != nil {
			return State{}, err
		}
	}
	next := clone(m.state)
	next.Values, next.Enabled, next.Revision = values, req.Enabled, next.Revision+1
	next.LogQueries = req.LogQueries
	created := []string{}
	for key, value := range req.Secrets {
		if !slices.Contains(secretKeys, key) {
			return State{}, fmt.Errorf("未知凭据字段 %s", key)
		}
		if len(value) > 16<<10 {
			return State{}, errors.New("凭据过长")
		}
		if key == "rerank_provider.headers" && value != "" {
			var headers map[string]string
			if err := json.Unmarshal([]byte(value), &headers); err != nil || headers == nil {
				return State{}, errors.New("精排额外请求头必须为 JSON 字符串对象")
			}
		}
	}
	for key, value := range req.Secrets {
		if strings.TrimSpace(value) == "" {
			delete(next.Credentials, key)
			continue
		}
		id := "knowledge-" + strings.ToLower(rand.Text())
		if err := m.host.Credentials.Put(ctx, id, value); err != nil {
			for _, id := range created {
				_ = m.host.Credentials.Delete(context.Background(), id)
			}
			return State{}, err
		}
		created = append(created, id)
		next.Credentials[key] = id
	}
	if err := m.persist(ctx, next); err != nil {
		for _, id := range created {
			_ = m.host.Credentials.Delete(context.Background(), id)
		}
		return State{}, err
	}
	for key, id := range m.state.Credentials {
		if next.Credentials[key] != id {
			_ = m.host.Credentials.Delete(context.Background(), id)
		}
	}
	if m.engine != nil {
		m.engine.Close()
		m.engine = nil
	}
	m.state = next
	return m.stateLocked(), nil
}

// 锁内只读投影，避免 State 重复加锁。返回副本，前端或测试不能修改内部配置。
func (m *Manager) stateLocked() State {
	copy := clone(m.state)
	has := make(map[string]bool)
	for _, key := range secretKeys {
		has[key] = copy.Credentials[key] != ""
	}
	jobs := make([]Job, 0, len(copy.Jobs))
	for _, j := range copy.Jobs {
		jobs = append(jobs, j.Job)
	}
	return State{Settings: Settings{copy.Enabled, copy.Revision, copy.Values, has, m.engine != nil, copy.LogQueries}, Collections: copy.Collections, Jobs: jobs}
}

// SaveCollection 的 Agent 绑定属于知识库模块，不向 Agent Profile 增加一份重复配置。
func (m *Manager) SaveCollection(ctx context.Context, c Collection) (State, error) {
	c.Name, c.Description = strings.TrimSpace(c.Name), strings.TrimSpace(c.Description)
	if c.Name == "" || len(c.Name) > 240 || len(c.Description) > 2000 || len(c.AgentIDs) > 200 {
		return State{}, errors.New("请填写有效的知识库名称、描述和 Agent 范围")
	}
	slices.Sort(c.AgentIDs)
	c.AgentIDs = slices.Compact(c.AgentIDs)
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return State{}, errors.New("知识库已关闭")
	}
	next := clone(m.state)
	if c.ID == "" {
		if len(next.Collections) >= 100 {
			return State{}, errors.New("知识库数量已达上限")
		}
		c.ID = "kb-" + strings.ToLower(rand.Text())
		next.Collections = append(next.Collections, c)
	}
	i := slices.IndexFunc(next.Collections, func(old Collection) bool { return old.ID == c.ID })
	if i < 0 {
		return State{}, errors.New("知识库不存在")
	}
	c.UpdatedAt = time.Now().UTC()
	next.Collections[i] = c
	next.Revision++
	if err := m.persist(ctx, next); err != nil {
		return State{}, err
	}
	m.state = next
	return m.stateLocked(), nil
}

func (m *Manager) persist(ctx context.Context, state diskState) error {
	return atomicfile.WriteJSON(ctx, filepath.Join(m.directory, "state.json"), 0600, state)
}

func (m *Manager) load(ctx context.Context) error {
	if err := os.MkdirAll(m.directory, 0700); err != nil {
		return err
	}
	m.state = diskState{Revision: 1, Values: ragconfig.Values(ragconfig.Default()), Credentials: map[string]string{}, Collections: []Collection{}, Jobs: []jobRecord{}}
	for _, key := range secretKeys {
		deleteValue(m.state.Values, key)
	}
	filename := filepath.Join(m.directory, "state.json")
	if info, err := os.Stat(filename); err == nil && info.Size() > 4<<20 {
		return errors.New("知识库配置文件过大")
	}
	err := atomicfile.ReadJSON(ctx, filename, &m.state)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if m.state.Values == nil {
		return errors.New("知识库配置缺少参数对象")
	}
	for _, key := range secretKeys {
		if value := getValue(m.state.Values, key); value != nil && value != "" {
			return fmt.Errorf("知识库 %s 必须通过设置页保存到凭据库", key)
		}
		deleteValue(m.state.Values, key)
	}
	if _, err := ragconfig.FromValues(m.state.Values); err != nil {
		return fmt.Errorf("读取知识库参数失败：%w", err)
	}
	if m.state.Credentials == nil {
		m.state.Credentials = map[string]string{}
	}
	// 退出时不自动重放会调用模型的任务；保留原件，供用户明确重试。
	changed := false
	for i := range m.state.Jobs {
		if pending(m.state.Jobs[i].Status) {
			m.state.Jobs[i].Status = "interrupted"
			m.state.Jobs[i].Error = "上次运行被中断，请重试"
			changed = true
		}
	}
	if changed {
		return m.persist(ctx, m.state)
	}
	return nil
}

func clone[T any](v T) T {
	data, _ := json.Marshal(v)
	var out T
	_ = json.Unmarshal(data, &out)
	return out
}
func getValue(values map[string]any, key string) any {
	parts := strings.Split(key, ".")
	var value any = values
	for _, part := range parts {
		m, ok := value.(map[string]any)
		if !ok {
			return nil
		}
		value = m[part]
	}
	return value
}
func setValue(values map[string]any, key string, value any) {
	parts := strings.Split(key, ".")
	for _, part := range parts[:len(parts)-1] {
		next, ok := values[part].(map[string]any)
		if !ok {
			next = map[string]any{}
			values[part] = next
		}
		values = next
	}
	values[parts[len(parts)-1]] = value
}
func deleteValue(values map[string]any, key string) {
	parts := strings.Split(key, ".")
	for _, part := range parts[:len(parts)-1] {
		next, ok := values[part].(map[string]any)
		if !ok {
			return
		}
		values = next
	}
	delete(values, parts[len(parts)-1])
}
