package builtin

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	einotool "github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"

	humberttools "github.com/sda1-hacker/humbert-agent/internal/tools"
)

const (
	currentTimeToolName = "get_current_time"
	updatePlanToolName  = "update_plan"
)

type CurrentTimeInput struct {
	TimeZone string `json:"time_zone,omitempty" jsonschema:"description=IANA time zone such as Asia/Tokyo. Omit to use the local time zone."`
}
type CurrentTimeOutput struct {
	TimeZone string `json:"time_zone"`
	RFC3339  string `json:"rfc3339"`
	Display  string `json:"display"`
}
type CurrentTimeFactory struct{}

func NewCurrentTimeFactory() *CurrentTimeFactory { return &CurrentTimeFactory{} }
func (f *CurrentTimeFactory) Descriptor() humberttools.Descriptor {
	return humberttools.Descriptor{Name: currentTimeToolName, Risk: humberttools.RiskRead}
}
func (f *CurrentTimeFactory) Build(ctx context.Context, scope humberttools.Scope) (einotool.InvokableTool, error) {
	return utils.InferTool(currentTimeToolName, "Get the current date and time, optionally in a specified IANA time zone.", func(callCtx context.Context, input *CurrentTimeInput) (*CurrentTimeOutput, error) {
		loc := time.Local
		if input != nil && strings.TrimSpace(input.TimeZone) != "" {
			parsed, err := time.LoadLocation(strings.TrimSpace(input.TimeZone))
			if err != nil {
				return nil, fmt.Errorf("无效时区: %w", err)
			}
			loc = parsed
		}
		now := time.Now().In(loc)
		return &CurrentTimeOutput{TimeZone: loc.String(), RFC3339: now.Format(time.RFC3339), Display: now.Format("2006-01-02 15:04:05 MST")}, nil
	})
}

type PlanItem struct {
	Content string `json:"content" jsonschema:"description=Short task description."`
	Status  string `json:"status" jsonschema:"description=Task status: pending, in_progress, or completed."`
}
type UpdatePlanInput struct {
	Items []PlanItem `json:"items" jsonschema:"description=The complete current plan. Replaces the previous plan for this tool instance."`
}
type UpdatePlanOutput struct {
	Items   []PlanItem `json:"items"`
	Summary string     `json:"summary"`
}
type UpdatePlanFactory struct{}

func NewUpdatePlanFactory() *UpdatePlanFactory { return &UpdatePlanFactory{} }
func (f *UpdatePlanFactory) Descriptor() humberttools.Descriptor {
	return humberttools.Descriptor{Name: updatePlanToolName, Risk: humberttools.RiskRead}
}
func (f *UpdatePlanFactory) Build(ctx context.Context, scope humberttools.Scope) (einotool.InvokableTool, error) {
	return utils.InferTool(updatePlanToolName, "Publish a concise structured plan for multi-step work. Send the complete plan each time; keep at most one item in_progress.", func(callCtx context.Context, input *UpdatePlanInput) (*UpdatePlanOutput, error) {
		if input == nil {
			return nil, errors.New("update_plan 输入不能为空")
		}
		if len(input.Items) > 30 {
			return nil, errors.New("update_plan 最多 30 项")
		}
		inProgress := 0
		done := 0
		items := make([]PlanItem, len(input.Items))
		for i, it := range input.Items {
			it.Content = strings.TrimSpace(it.Content)
			if it.Content == "" {
				return nil, fmt.Errorf("计划第 %d 项内容为空", i+1)
			}
			switch it.Status {
			case "pending":
			case "in_progress":
				inProgress++
			case "completed":
				done++
			default:
				return nil, fmt.Errorf("计划第 %d 项状态无效", i+1)
			}
			items[i] = it
		}
		if inProgress > 1 {
			return nil, errors.New("update_plan 最多只能有一个 in_progress 项")
		}
		return &UpdatePlanOutput{Items: items, Summary: fmt.Sprintf("%d/%d completed", done, len(items))}, nil
	})
}
