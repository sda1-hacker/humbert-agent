package usecases

import (
	"context"
	"fmt"
	"strings"

	"github.com/sda1-hacker/humbert-agent/internal/agents"
)

// SkillUsers 和 SkillRemover 是此用例实际需要的两个接口。
// 引用关系仍归 Agent Profile，包删除仍归 Skills；用例不读取任何领域的数据目录。
type SkillUsers interface {
	AgentsUsingSkill(context.Context, string) ([]agents.AgentInfo, error)
}
type SkillRemover interface {
	Remove(context.Context, string) error
}

type SkillMaintenance struct {
	users  SkillUsers
	skills SkillRemover
}

func NewSkillMaintenance(users SkillUsers, skills SkillRemover) *SkillMaintenance {
	return &SkillMaintenance{users: users, skills: skills}
}

// Remove 保留“被 Agent 使用时拒绝删除”的规则，避免其他入口绕过桌面的引用检查。
func (s *SkillMaintenance) Remove(ctx context.Context, name string) error {
	users, err := s.users.AgentsUsingSkill(ctx, name)
	if err != nil {
		return fmt.Errorf("检查 Skill Agent 引用失败: %w", err)
	}
	if len(users) > 0 {
		names := make([]string, 0, len(users))
		for _, user := range users {
			names = append(names, user.Agent.Name)
		}
		return fmt.Errorf("Skill %q 仍被 %d 个 Agent 使用（%s），请先在“技能”页面切换对应 Agent 并关闭该 Skill", name, len(users), strings.Join(names, "、"))
	}
	if err := s.skills.Remove(ctx, name); err != nil {
		return fmt.Errorf("删除 Skill 失败: %w", err)
	}
	return nil
}
