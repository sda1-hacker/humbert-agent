package services

import (
	"context"
	"fmt"
	"time"

	coreapp "github.com/sda1-hacker/humbert-agent/internal/app"
	"github.com/sda1-hacker/humbert-agent/internal/preferences"
)

type UserProfileDTO struct {
	Name   string `json:"name"`
	Avatar string `json:"avatar"`
}

type PreferenceService struct {
	core *coreapp.Application
}

func NewPreferenceService(core *coreapp.Application) *PreferenceService {
	return &PreferenceService{core: core}
}

func (s *PreferenceService) ServiceName() string { return "PreferenceService" }

func (s *PreferenceService) GetUserProfile() (UserProfileDTO, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	value, err := s.core.Preferences().Get(ctx)
	if err != nil {
		return UserProfileDTO{}, fmt.Errorf("读取用户资料失败: %w", err)
	}
	return userProfileDTO(value), nil
}

func (s *PreferenceService) UpdateUserProfile(request UserProfileDTO) (UserProfileDTO, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	value, err := s.core.Preferences().Update(ctx, preferences.UserProfile{Name: request.Name, Avatar: request.Avatar})
	if err != nil {
		return UserProfileDTO{}, fmt.Errorf("更新用户资料失败: %w", err)
	}
	return userProfileDTO(value), nil
}

func userProfileDTO(value preferences.UserProfile) UserProfileDTO {
	return UserProfileDTO{Name: value.Name, Avatar: value.Avatar}
}

// ListPersonalMemories 返回用户可查看、修订和遗忘的跨会话事实。
func (s *PreferenceService) ListPersonalMemories() ([]preferences.PersonalMemory, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return s.core.Preferences().ListMemories(ctx)
}

func (s *PreferenceService) AddPersonalMemory(text string) (preferences.PersonalMemory, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return s.core.Preferences().AddMemory(ctx, text)
}

func (s *PreferenceService) UpdatePersonalMemory(id string, text string) (preferences.PersonalMemory, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return s.core.Preferences().UpdateMemory(ctx, id, text)
}

func (s *PreferenceService) DeletePersonalMemory(id string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return s.core.Preferences().DeleteMemory(ctx, id)
}
