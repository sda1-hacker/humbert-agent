package proactive

import (
	"fmt"
	"strings"
	"time"
)

// DecisionEngine 把“发生了什么”与“应该做什么”解耦。当前实现是确定性的规则引擎；
// run_agent 动作则把复杂事件交给指定 Agent 继续判断和处理。
type DecisionEngine interface {
	Decide(event Event, settings Settings, now time.Time, latest *Record) Decision
}

type RuleDecisionEngine struct{}

func (RuleDecisionEngine) Decide(event Event, settings Settings, now time.Time, latest *Record) Decision {
	if !settings.Enabled {
		return Decision{Action: ActionIgnore, Reason: "主动助手已关闭"}
	}
	rule, exists := settings.Rules[event.Kind]
	if !exists || !rule.Enabled || rule.Action == ActionIgnore {
		return Decision{Action: ActionIgnore, Reason: "该事件规则未启用"}
	}
	if latest != nil && rule.CooldownSeconds > 0 && latest.UpdatedAt.Add(time.Duration(rule.CooldownSeconds)*time.Second).After(now) {
		return Decision{Action: ActionIgnore, Reason: "事件仍处于冷却时间"}
	}
	decision := Decision{Action: rule.Action, AgentID: strings.TrimSpace(rule.AgentID)}
	switch rule.Action {
	case ActionNotify:
		decision.Reason = "按事件规则通知用户"
	case ActionRunAgent:
		decision.Reason = "按事件规则交给 Agent 处理"
		decision.Prompt = strings.TrimSpace(rule.AgentPrompt)
	default:
		decision.Action = ActionIgnore
		decision.Reason = fmt.Sprintf("未知动作 %q，安全地忽略", rule.Action)
	}
	return decision
}

func InQuietHours(settings Settings, now time.Time) bool {
	quiet := settings.QuietHours
	if !quiet.Enabled {
		return false
	}
	location := time.Local
	if zone := strings.TrimSpace(quiet.TimeZone); zone != "" {
		if loaded, err := time.LoadLocation(zone); err == nil {
			location = loaded
		}
	}
	local := now.In(location)
	startHour, startMinute, err1 := parseClock(quiet.Start)
	endHour, endMinute, err2 := parseClock(quiet.End)
	if err1 != nil || err2 != nil {
		return false
	}
	minute := local.Hour()*60 + local.Minute()
	start := startHour*60 + startMinute
	end := endHour*60 + endMinute
	if start == end {
		return true
	}
	if start < end {
		return minute >= start && minute < end
	}
	return minute >= start || minute < end
}
