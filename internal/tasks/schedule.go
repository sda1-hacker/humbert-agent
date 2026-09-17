package tasks

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	defaultMaxDurationSeconds = 30 * 60
	defaultMaxModelCalls      = 20
	defaultMaxToolCalls       = 50
	defaultMaxAttempts        = 1
	defaultRetryDelaySeconds  = 30
)

func normalizeTaskInput(name, prompt string, status TaskStatus, schedule Schedule, limits Limits) (string, string, TaskStatus, Schedule, Limits, error) {
	name = strings.TrimSpace(name)
	prompt = strings.TrimSpace(prompt)
	if name == "" || len([]rune(name)) > 120 {
		return "", "", "", Schedule{}, Limits{}, errors.New("任务名称不能为空且最多 120 个字符")
	}
	if prompt == "" || len([]byte(prompt)) > 256*1024 {
		return "", "", "", Schedule{}, Limits{}, errors.New("任务提示词不能为空且最多 256 KiB")
	}
	if status == "" {
		status = TaskStatusActive
	}
	if status != TaskStatusActive && status != TaskStatusPaused {
		return "", "", "", Schedule{}, Limits{}, fmt.Errorf("无效任务状态: %s", status)
	}
	normalizedSchedule, err := normalizeSchedule(schedule)
	if err != nil {
		return "", "", "", Schedule{}, Limits{}, err
	}
	normalizedLimits, err := normalizeLimits(limits)
	if err != nil {
		return "", "", "", Schedule{}, Limits{}, err
	}
	return name, prompt, status, normalizedSchedule, normalizedLimits, nil
}

func normalizeLimits(value Limits) (Limits, error) {
	if value.MaxDurationSeconds == 0 {
		value.MaxDurationSeconds = defaultMaxDurationSeconds
	}
	if value.MaxModelCalls == 0 {
		value.MaxModelCalls = defaultMaxModelCalls
	}
	if value.MaxToolCalls == 0 {
		value.MaxToolCalls = defaultMaxToolCalls
	}
	if value.MaxAttempts == 0 {
		value.MaxAttempts = defaultMaxAttempts
	}
	if value.RetryDelaySeconds == 0 {
		value.RetryDelaySeconds = defaultRetryDelaySeconds
	}
	if value.MaxDurationSeconds < 30 || value.MaxDurationSeconds > 24*60*60 {
		return Limits{}, errors.New("任务最大运行时间必须位于 30-86400 秒")
	}
	if value.MaxModelCalls < 1 || value.MaxModelCalls > 100 {
		return Limits{}, errors.New("最大模型调用次数必须位于 1-100")
	}
	if value.MaxToolCalls < 1 || value.MaxToolCalls > 500 {
		return Limits{}, errors.New("最大工具调用次数必须位于 1-500")
	}
	if value.MaxAttempts < 1 || value.MaxAttempts > 5 {
		return Limits{}, errors.New("最大尝试次数必须位于 1-5")
	}
	if value.RetryDelaySeconds < 1 || value.RetryDelaySeconds > 6*60*60 {
		return Limits{}, errors.New("重试延迟必须位于 1-21600 秒")
	}
	return value, nil
}

func normalizeSchedule(value Schedule) (Schedule, error) {
	if value.Type == "" {
		value.Type = ScheduleManual
	}
	if value.MisfirePolicy == "" {
		value.MisfirePolicy = MisfireRunOnce
	}
	if value.OverlapPolicy == "" {
		value.OverlapPolicy = OverlapSkip
	}
	if value.MisfirePolicy != MisfireSkip && value.MisfirePolicy != MisfireRunOnce {
		return Schedule{}, fmt.Errorf("无效错过执行策略: %s", value.MisfirePolicy)
	}
	if value.OverlapPolicy != OverlapSkip && value.OverlapPolicy != OverlapQueueOne {
		return Schedule{}, fmt.Errorf("无效重叠策略: %s", value.OverlapPolicy)
	}
	if value.Type == ScheduleManual {
		return Schedule{Type: ScheduleManual, MisfirePolicy: value.MisfirePolicy, OverlapPolicy: value.OverlapPolicy}, nil
	}
	value.TimeZone = strings.TrimSpace(value.TimeZone)
	if value.TimeZone == "" {
		value.TimeZone = time.Local.String()
	}
	if _, err := time.LoadLocation(value.TimeZone); err != nil {
		return Schedule{}, fmt.Errorf("无效时区 %q: %w", value.TimeZone, err)
	}
	switch value.Type {
	case ScheduleOnce:
		if value.RunAt == nil || value.RunAt.IsZero() {
			return Schedule{}, errors.New("单次任务必须设置运行时间")
		}
		runAt := value.RunAt.UTC()
		value.RunAt = &runAt
	case ScheduleInterval:
		if value.IntervalMinutes < 1 || value.IntervalMinutes > 365*24*60 {
			return Schedule{}, errors.New("周期分钟数必须位于 1-525600")
		}
	case ScheduleDaily:
		if _, _, err := parseTimeOfDay(value.TimeOfDay); err != nil {
			return Schedule{}, err
		}
	case ScheduleWeekly:
		if _, _, err := parseTimeOfDay(value.TimeOfDay); err != nil {
			return Schedule{}, err
		}
		seen := make(map[int]struct{}, len(value.Weekdays))
		weekdays := make([]int, 0, len(value.Weekdays))
		for _, day := range value.Weekdays {
			if day < 0 || day > 6 {
				return Schedule{}, errors.New("星期必须位于 0-6，0 表示星期日")
			}
			if _, exists := seen[day]; exists {
				continue
			}
			seen[day] = struct{}{}
			weekdays = append(weekdays, day)
		}
		if len(weekdays) == 0 {
			return Schedule{}, errors.New("每周任务至少选择一天")
		}
		sort.Ints(weekdays)
		value.Weekdays = weekdays
	default:
		return Schedule{}, fmt.Errorf("无效日程类型: %s", value.Type)
	}
	return value, nil
}

func nextOccurrence(schedule Schedule, after time.Time) (*time.Time, error) {
	if schedule.Type == ScheduleManual {
		return nil, nil
	}
	location, err := time.LoadLocation(schedule.TimeZone)
	if err != nil {
		return nil, err
	}
	after = after.In(location)
	var next time.Time
	switch schedule.Type {
	case ScheduleOnce:
		if schedule.RunAt == nil || !schedule.RunAt.After(after) {
			return nil, nil
		}
		next = schedule.RunAt.In(location)
	case ScheduleInterval:
		next = after.Add(time.Duration(schedule.IntervalMinutes) * time.Minute)
	case ScheduleDaily:
		hour, minute, err := parseTimeOfDay(schedule.TimeOfDay)
		if err != nil {
			return nil, err
		}
		for offset := 0; offset <= 370; offset++ {
			candidate := time.Date(after.Year(), after.Month(), after.Day()+offset, hour, minute, 0, 0, location)
			// 春季 DST 跳时可能使 02:30 这类 wall-clock 不存在。Go 会把它
			// 归一化到相邻时刻；主动任务选择跳过该天，而不是悄悄改变用户钟点。
			if !sameWallClock(candidate, hour, minute) {
				continue
			}
			if candidate.After(after) {
				next = candidate
				break
			}
		}
		if next.IsZero() {
			return nil, errors.New("无法计算下一次每日运行时间")
		}
	case ScheduleWeekly:
		hour, minute, err := parseTimeOfDay(schedule.TimeOfDay)
		if err != nil {
			return nil, err
		}
		for offset := 0; offset <= 14; offset++ {
			candidate := time.Date(after.Year(), after.Month(), after.Day()+offset, hour, minute, 0, 0, location)
			if sameWallClock(candidate, hour, minute) && candidate.After(after) && containsWeekday(schedule.Weekdays, int(candidate.Weekday())) {
				next = candidate
				break
			}
		}
		if next.IsZero() {
			return nil, errors.New("无法计算下一次每周运行时间")
		}
	default:
		return nil, fmt.Errorf("无效日程类型: %s", schedule.Type)
	}
	next = next.UTC()
	return &next, nil
}

func sameWallClock(value time.Time, hour, minute int) bool {
	return value.Hour() == hour && value.Minute() == minute
}

// advanceOccurrence 从已经声明的 scheduledFor 向前推进，并直接跳过所有不晚于 now 的
// 旧槽位，保证休眠恢复只产生一次补跑而不会形成历史洪峰。
func advanceOccurrence(schedule Schedule, scheduledFor, now time.Time) (*time.Time, error) {
	if schedule.Type == ScheduleOnce || schedule.Type == ScheduleManual {
		return nil, nil
	}
	if schedule.Type == ScheduleInterval {
		interval := time.Duration(schedule.IntervalMinutes) * time.Minute
		next := scheduledFor.Add(interval)
		if !next.After(now) {
			missed := now.Sub(next)/interval + 1
			next = next.Add(missed * interval)
		}
		next = next.UTC()
		return &next, nil
	}
	return nextOccurrence(schedule, now)
}

func parseTimeOfDay(value string) (int, int, error) {
	parts := strings.Split(strings.TrimSpace(value), ":")
	if len(parts) != 2 {
		return 0, 0, errors.New("运行时间必须使用 HH:MM 格式")
	}
	hour, err := strconv.Atoi(parts[0])
	if err != nil || hour < 0 || hour > 23 {
		return 0, 0, errors.New("运行小时必须位于 00-23")
	}
	minute, err := strconv.Atoi(parts[1])
	if err != nil || minute < 0 || minute > 59 {
		return 0, 0, errors.New("运行分钟必须位于 00-59")
	}
	return hour, minute, nil
}

func containsWeekday(values []int, value int) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}
