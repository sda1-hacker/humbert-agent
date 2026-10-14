package computer

import (
	"context"
	"errors"
)

// 各平台绑定 RobotGo 的同一组输入 API；适配层保留取消和输入目标检查。
// 不修改 RobotGo 的全局延迟，不读写用户剪贴板。
type robotInput struct {
	move      func(int, int, ...int) error
	toggle    func(...any) error
	click     func(...any) error
	scroll    func(int, int, ...int) error
	text      func(string, ...int) error
	keyTap    func(string, ...any) error
	keyToggle func(string, ...any) error
	allowed   func() bool
	point     func() bool
	dragging  bool
}

func robotCall(ctx context.Context, action func() error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return errors.Join(action(), ctx.Err())
}

func (r *robotInput) pointerAllowed() error {
	if r.point != nil && !r.point() {
		return errors.New("禁止向 Humbert 自身或被遮挡的窗口发送指针输入")
	}
	return nil
}

func (r *robotInput) inputAllowed() error {
	if r.allowed != nil && !r.allowed() {
		return errors.New("禁止向 Humbert 自身输入或修改审批、设置")
	}
	return nil
}

func (r *robotInput) Button(ctx context.Context, button string, down bool, _ int) error {
	if down {
		if err := r.pointerAllowed(); err != nil {
			return err
		}
	}
	state := "up"
	if down {
		state = "down"
	}
	err := robotCall(ctx, func() error { return r.toggle(button, state) })
	if err == nil && button == "left" {
		r.dragging = down
	}
	return err
}

// 双击交给库生成带正确 click state 的完整事件；失败时仍尝试松开鼠标。
func (r *robotInput) DoubleClick(ctx context.Context, button string) error {
	if err := r.pointerAllowed(); err != nil {
		return err
	}
	executed := false
	err := robotCall(ctx, func() error { executed = true; return r.click(button, true) })
	if err != nil && executed {
		err = errors.Join(err, r.toggle(button, "up"))
	}
	return err
}

func (r *robotInput) Scroll(ctx context.Context, x, y int) error {
	if err := r.pointerAllowed(); err != nil {
		return err
	}
	// 工具坐标使用近似像素，RobotGo 使用滚轮格数且正方向为上/左。
	units := func(delta int) int {
		if delta < 0 {
			return (delta - 79) / 80
		}
		return (delta + 79) / 80
	}
	return robotCall(ctx, func() error { return r.scroll(-units(x), -units(y), 0) })
}

func (r *robotInput) Text(ctx context.Context, text string) error {
	for _, char := range text {
		if err := r.inputAllowed(); err != nil {
			return err
		}
		if err := robotCall(ctx, func() error { return r.text(string(char)) }); err != nil {
			return err
		}
	}
	return nil
}

func robotKey(key string) string {
	switch key {
	case "arrowup":
		return "up"
	case "arrowdown":
		return "down"
	case "arrowleft":
		return "left"
	case "arrowright":
		return "right"
	case "escape":
		return "esc"
	}
	return key
}

func (r *robotInput) Key(ctx context.Context, key string, down bool) error {
	if down {
		if err := r.inputAllowed(); err != nil {
			return err
		}
	}
	state := "up"
	if down {
		state = "down"
	}
	return robotCall(ctx, func() error { return r.keyToggle(robotKey(key), state) })
}

// 一次提交快捷键，让库同时处理 modifier flags 与失败后的修饰键释放。
func (r *robotInput) KeyChord(ctx context.Context, keys []string) error {
	if err := r.inputAllowed(); err != nil {
		return err
	}
	primary := keys[len(keys)-1]
	modifiers := make([]string, 0, len(keys)-1)
	for _, key := range keys[:len(keys)-1] {
		switch key {
		case "ctrl", "alt", "shift", "cmd":
			modifiers = append(modifiers, key)
		default:
			return errors.New("快捷键应为修饰键加一个普通按键，例如 Cmd+S")
		}
	}
	executed := false
	err := robotCall(ctx, func() error { executed = true; return r.keyTap(robotKey(primary), modifiers) })
	if err != nil && executed {
		// 同步库调用可能只完成按下；清理不继承取消信号，按键与修饰键一起释放。
		err = errors.Join(err, r.keyToggle(robotKey(primary), "up", modifiers))
	}
	return err
}
