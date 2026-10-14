// Package computer 隔离各平台真实桌面 API；权限、会话和模型调用由工具层负责。
package computer

import (
	"context"
	"errors"
	"fmt"
	"image"
	"runtime"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// Input 坐标始终相对最近一张截图，不能把屏幕坐标或其他窗口的截图混进来。
type Input struct {
	Action        string `json:"action" jsonschema:"description=One of status windows screenshot click double_click right_click move drag type key scroll wait release."`
	WindowID      string `json:"window_id,omitempty" jsonschema:"description=Exact window ID returned by windows. Use portal for the user-selected Wayland screen. Required except status windows release."`
	ObservationID string `json:"observation_id,omitempty" jsonschema:"description=Latest observation ID for this window, required for every input action. Never reuse a previous action's observation."`
	Goal          string `json:"goal,omitempty" jsonschema:"description=What to locate or verify in the screenshot; passed to the visual observer, not executed as instructions."`
	X             *int   `json:"x,omitempty" jsonschema:"description=Pointer X in screenshot pixels; required for pointer actions, including scroll."`
	Y             *int   `json:"y,omitempty" jsonschema:"description=Pointer Y in screenshot pixels."`
	EndX          *int   `json:"end_x,omitempty" jsonschema:"description=Drag destination X in screenshot pixels."`
	EndY          *int   `json:"end_y,omitempty" jsonschema:"description=Drag destination Y in screenshot pixels."`
	Text          string `json:"text,omitempty" jsonschema:"description=Literal Unicode text for type. Does not press Enter or replace existing text."`
	Key           string `json:"key,omitempty" jsonschema:"description=Key or shortcut, e.g. Enter, Tab, Escape, Ctrl+A, Cmd+S. Mod means Cmd on macOS and Ctrl elsewhere."`
	ScrollX       int    `json:"scroll_x,omitempty" jsonschema:"description=Approximate horizontal scroll amount; positive goes right. Some OS settings scroll in lines."`
	ScrollY       int    `json:"scroll_y,omitempty" jsonschema:"description=Approximate vertical scroll amount; positive goes down. Some OS settings scroll in lines."`
	WaitMS        int    `json:"wait_ms,omitempty" jsonschema:"description=Settle time 0-5000 ms; default 350, or 1000 for wait."`
}

// Window.ID 包含进程身份，避免窗口句柄被其他应用复用后继承旧批准。
type Window struct {
	ID     string `json:"id"`
	App    string `json:"app"`
	Title  string `json:"title,omitempty"`
	X      int    `json:"x"`
	Y      int    `json:"y"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
}

type Status struct {
	Backend string `json:"backend"`
	Capture bool   `json:"capture"`
	Control bool   `json:"control"`
	Reason  string `json:"reason,omitempty"`
}

type Target struct {
	Label string `json:"label"`
	X     int    `json:"x"`
	Y     int    `json:"y"`
}

// Observation 是纯观察结果；坐标必须来自该帧，模糊目标不作为可点击目标返回。
type Observation struct {
	Summary                string   `json:"summary"`
	Targets                []Target `json:"targets"`
	NeedsHumanVerification bool     `json:"needs_human_verification"`
}

func (o Observation) Validate(width, height int) error {
	if strings.TrimSpace(o.Summary) == "" || len([]rune(o.Summary)) > 4000 || len(o.Targets) > 30 {
		return errors.New("视觉观察格式或长度无效")
	}
	for _, target := range o.Targets {
		if target.Label == "" || len([]rune(target.Label)) > 120 || target.X < 0 || target.Y < 0 || target.X >= width || target.Y >= height {
			return errors.New("视觉定位坐标超出截图或目标标签无效")
		}
	}
	return nil
}

// Frame 保留原始像素和系统逻辑尺寸。Retina/DPI 缩放只在动作前转换一次。
type Frame struct {
	Window Window
	Image  image.Image
}

// Driver 只执行一条已验证的动作，不启动 Shell、不读写剪贴板、不替模型决策。
type Driver interface {
	Status() Status
	Windows(context.Context) ([]Window, error)
	Capture(context.Context, string) (Frame, error)
	Focus(context.Context, Window) error
	Check(context.Context, Window) error
	Move(context.Context, Window, int, int) error
	Button(context.Context, string, bool, int) error
	Scroll(context.Context, int, int) error
	Text(context.Context, string) error
	Key(context.Context, string, bool) error
	Emergency() bool
	Close() error
}

func FindWindow(ctx context.Context, driver Driver, id string) (Window, error) {
	windows, err := driver.Windows(ctx)
	if err != nil {
		return Window{}, err
	}
	for _, window := range windows {
		if window.ID == id {
			if window.Width <= 0 || window.Height <= 0 || window.Width > 8192 || window.Height > 8192 || window.Width*window.Height > 16<<20 {
				return Window{}, errors.New("目标画面超过桌面像素限制")
			}
			return window, nil
		}
	}
	return Window{}, errors.New("目标窗口不存在或不可见，请重新列出窗口")
}

func SameBounds(a, b Window) bool {
	return a.ID == b.ID && a.App == b.App && a.X == b.X && a.Y == b.Y && a.Width == b.Width && a.Height == b.Height
}

func (in Input) Validate() error {
	if len(in.WindowID) > 128 || len(in.ObservationID) > 128 || !utf8.ValidString(in.Text) || !utf8.ValidString(in.Goal) || strings.ContainsRune(in.Text, 0) {
		return errors.New("桌面参数无效")
	}
	if len([]rune(in.Text)) > 4000 || len([]rune(in.Goal)) > 500 || in.WaitMS < 0 || in.WaitMS > 5000 || in.ScrollX < -3000 || in.ScrollX > 3000 || in.ScrollY < -3000 || in.ScrollY > 3000 {
		return errors.New("桌面输入超过限制（文本 4000 字符、目标 500 字符、等待 5 秒、滚动 3000 像素）")
	}
	if (in.X == nil) != (in.Y == nil) || (in.EndX == nil) != (in.EndY == nil) {
		return errors.New("坐标必须成对提供")
	}
	if in.Action != "drag" && (in.EndX != nil || in.EndY != nil) {
		return errors.New("只有拖拽接受结束坐标")
	}
	if in.X != nil && (in.Action == "type" || in.Action == "key" || in.Action == "wait") {
		return errors.New("文本/按键/等待不接受指针坐标；先 click 指定控件，再 type 或 key")
	}
	switch in.Action {
	case "status", "windows", "release":
		return nil
	case "screenshot", "wait":
	case "click", "double_click", "right_click", "move", "drag", "scroll":
		if in.X == nil || in.Y == nil || (in.Action == "drag" && (in.EndX == nil || in.EndY == nil)) {
			return errors.New("指针操作缺少截图坐标")
		}
	case "type":
		if in.Text == "" {
			return errors.New("输入文本不能为空")
		}
	case "key":
		if _, err := ParseKeys(in.Key); err != nil {
			return err
		}
	default:
		return fmt.Errorf("不支持的桌面操作 %q", in.Action)
	}
	if in.WindowID == "" || (in.Action != "screenshot" && in.ObservationID == "") {
		return errors.New("必须指定 window_id，并为动作提供最近的 observation_id")
	}
	return nil
}

// ParseKeys 只接受明确键名；拒绝空组合和重复键，保留 Ctrl+Alt+Escape 给用户紧急停止。
func ParseKeys(value string) ([]string, error) {
	parts := strings.Split(value, "+")
	if len(parts) > 5 {
		return nil, errors.New("快捷键最多包含 5 个键")
	}
	mod := "ctrl"
	if runtime.GOOS == "darwin" {
		mod = "cmd"
	}
	aliases := map[string]string{
		"control": "ctrl", "command": "cmd", "meta": "cmd", "super": "cmd", "win": "cmd",
		"option": "alt", "return": "enter", "esc": "escape", "mod": mod,
	}
	seen := map[string]bool{}
	for i, part := range parts {
		key := strings.ToLower(strings.TrimSpace(part))
		if alias := aliases[key]; alias != "" {
			key = alias
		}
		valid := len(key) == 1 && ((key[0] >= 'a' && key[0] <= 'z') || (key[0] >= '0' && key[0] <= '9'))
		if strings.HasPrefix(key, "f") {
			number, err := strconv.Atoi(key[1:])
			valid = valid || err == nil && number >= 1 && number <= 20
		}
		switch key {
		case "ctrl", "cmd", "mod", "alt", "shift", "enter", "tab", "escape", "backspace", "delete", "space", "arrowup", "arrowdown", "arrowleft", "arrowright", "home", "end", "pageup", "pagedown":
			valid = true
		}
		if !valid || seen[key] {
			return nil, fmt.Errorf("无效或重复的按键 %q", part)
		}
		parts[i], seen[key] = key, true
	}
	if seen["ctrl"] && seen["alt"] && seen["escape"] {
		return nil, errors.New("Ctrl+Alt+Escape 是用户紧急停止键，工具不能模拟")
	}
	return parts, nil
}

func Pause(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// Perform 保证取消/失败时释放本次按下的键和鼠标，避免拖拽或快捷键残留。
// 每个有副作用的步骤前重查前台窗口；窗口切换后不把剩余输入发给其他应用。
func Perform(ctx context.Context, driver Driver, window Window, in Input) (resultErr error) {
	check := func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if driver.Emergency() {
			return errors.New("用户已按紧急停止键")
		}
		return driver.Check(ctx, window)
	}
	if err := check(); err != nil {
		return err
	}
	if in.X != nil {
		if err := driver.Move(ctx, window, *in.X, *in.Y); err != nil {
			return err
		}
	}
	release := func(action func(context.Context) error) error {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		defer cancel()
		return action(cleanupCtx)
	}
	switch in.Action {
	case "click", "double_click", "right_click", "drag":
		if in.Action == "double_click" {
			if clicker, ok := driver.(interface {
				DoubleClick(context.Context, string) error
			}); ok {
				if err := check(); err != nil {
					return err
				}
				return clicker.DoubleClick(ctx, "left")
			}
		}
		button, count := "left", 1
		if in.Action == "right_click" {
			button = "right"
		}
		if in.Action == "double_click" {
			count = 2
		}
		for n := 0; n < count; n++ {
			if err := check(); err != nil {
				return err
			}
			if err := driver.Button(ctx, button, true, n+1); err != nil {
				// 库可能在按下后才报告取消/失败，仍须松开，不能依赖错误表示“未执行”。
				return errors.Join(err, release(func(cleanupCtx context.Context) error { return driver.Button(cleanupCtx, button, false, n+1) }))
			}
			// 即使下一步取消，松开动作也不继承已取消的 Context。
			err := func() (buttonErr error) {
				defer func() {
					buttonErr = errors.Join(buttonErr, release(func(cleanupCtx context.Context) error { return driver.Button(cleanupCtx, button, false, n+1) }))
				}()
				if in.Action != "drag" {
					return Pause(ctx, 40*time.Millisecond)
				}
				for step := 1; step <= 12; step++ {
					if err := check(); err != nil {
						return err
					}
					x := *in.X + (*in.EndX-*in.X)*step/12
					y := *in.Y + (*in.EndY-*in.Y)*step/12
					if err := driver.Move(ctx, window, x, y); err != nil {
						return err
					}
					if err := Pause(ctx, 20*time.Millisecond); err != nil {
						return err
					}
				}
				return nil
			}()
			if err != nil {
				return err
			}
			if n+1 < count {
				if err := Pause(ctx, 70*time.Millisecond); err != nil {
					return err
				}
			}
		}
	case "type":
		// 分批输入保留取消机会；不借助用户剪贴板，避免覆盖隐私内容。
		runes := []rune(in.Text)
		for start := 0; start < len(runes); start += 32 {
			if err := check(); err != nil {
				return err
			}
			if err := driver.Text(ctx, string(runes[start:min(start+32, len(runes))])); err != nil {
				return err
			}
		}
	case "key":
		keys, err := ParseKeys(in.Key)
		if err != nil {
			return err
		}
		if keyboard, ok := driver.(interface {
			KeyChord(context.Context, []string) error
		}); ok {
			if err := check(); err != nil {
				return err
			}
			return keyboard.KeyChord(ctx, keys)
		}
		pressed := []string{}
		defer func() {
			for i := len(pressed) - 1; i >= 0; i-- {
				resultErr = errors.Join(resultErr, release(func(cleanupCtx context.Context) error { return driver.Key(cleanupCtx, pressed[i], false) }))
			}
		}()
		for _, key := range keys {
			if err := check(); err != nil {
				return err
			}
			if err := driver.Key(ctx, key, true); err != nil {
				return err
			}
			pressed = append(pressed, key)
		}
	case "scroll":
		if err := check(); err != nil {
			return err
		}
		return driver.Scroll(ctx, in.ScrollX, in.ScrollY)
	}
	return nil
}
