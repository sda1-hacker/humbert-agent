//go:build darwin && cgo

package computer

/*
#cgo CFLAGS: -x objective-c -fobjc-arc -fblocks -Wno-deprecated-declarations -mmacosx-version-min=13.0
#cgo LDFLAGS: -framework Cocoa -framework ApplicationServices -framework ScreenCaptureKit -framework ImageIO -mmacosx-version-min=13.0
#include <stdlib.h>
#include <stdbool.h>
char *hc_windows(void);
int hc_permissions(void);
int hc_focus(int pid, int x, int y, int w, int h);
bool hc_front(unsigned int window, int pid);
void *hc_capture(unsigned int window, bool screen, int w, int h, int *size);
bool hc_input_allowed(void);
bool hc_point_allowed(int x,int y,int pid);
bool hc_emergency(void);
*/
import "C"

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image/png"
	"strconv"
	"strings"
	"time"
	"unsafe"

	robotgo "github.com/go-vgo/robotgo"
)

type macDriver struct{ robotInput }

func NewDriver() Driver {
	return &macDriver{robotInput: robotInput{
		move: robotgo.Move, toggle: robotgo.Toggle, click: robotgo.Click, scroll: robotgo.Scroll,
		text: robotgo.TypeStr, keyTap: robotgo.KeyTap, keyToggle: robotgo.KeyToggle,
		allowed: func() bool { return bool(C.hc_input_allowed()) },
		point: func() bool {
			x, y := robotgo.Location()
			return bool(C.hc_point_allowed(C.int(x), C.int(y), 0))
		},
	}}
}

func (d *macDriver) Status() Status {
	bits := int(C.hc_permissions())
	status := Status{Backend: "macOS ScreenCaptureKit/RobotGo", Capture: bits&1 != 0, Control: bits&2 != 0}
	if !status.Capture || !status.Control {
		status.Reason = "请在 macOS 系统设置 → 隐私与安全性中，为 Humbert 开启屏幕录制和辅助功能，再重启应用；开发运行时需为实际宿主授予权限。"
	}
	return status
}

func (d *macDriver) Windows(ctx context.Context) ([]Window, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	raw := C.hc_windows()
	if raw == nil {
		return nil, errors.New("读取 macOS 窗口失败")
	}
	defer C.free(unsafe.Pointer(raw))
	var windows []Window
	err := json.Unmarshal([]byte(C.GoString(raw)), &windows)
	return windows, err
}

func (d *macDriver) Capture(ctx context.Context, id string) (Frame, error) {
	window, err := FindWindow(ctx, d, id)
	if err != nil {
		return Frame{}, err
	}
	if !d.Status().Capture {
		return Frame{}, errors.New(d.Status().Reason)
	}
	if strings.HasPrefix(id, "screen:") {
		number, err := strconv.ParseUint(strings.TrimPrefix(id, "screen:"), 10, 32)
		if err != nil {
			return Frame{}, err
		}
		return d.captureImage(ctx, window, uint32(number), true)
	}
	number, _, err := macWindowID(id)
	if err != nil {
		return Frame{}, err
	}
	return d.captureImage(ctx, window, number, false)
}
func (d *macDriver) captureImage(ctx context.Context, window Window, number uint32, screen bool) (Frame, error) {
	var size C.int
	data := C.hc_capture(C.uint(number), C.bool(screen), C.int(window.Width), C.int(window.Height), &size)
	if data == nil {
		return Frame{}, errors.New("macOS 窗口截图失败；窗口可能已关闭、受保护，或屏幕录制权限尚未生效")
	}
	defer C.free(data)
	if size <= 0 || size > 32<<20 {
		return Frame{}, errors.New("macOS 截图超过限制")
	}
	if err := ctx.Err(); err != nil {
		return Frame{}, err
	}
	im, err := png.Decode(bytes.NewReader(C.GoBytes(data, size)))
	return Frame{Window: window, Image: im}, err
}

func macWindowID(id string) (uint32, int, error) {
	parts := strings.Split(id, ":")
	if len(parts) != 3 {
		return 0, 0, errors.New("macOS 窗口 ID 无效")
	}
	number, err := strconv.ParseUint(parts[0], 10, 32)
	pid, pidErr := strconv.Atoi(parts[1])
	if err != nil || pidErr != nil || pid <= 0 {
		return 0, 0, errors.New("macOS 窗口 ID 无效")
	}
	return uint32(number), pid, nil
}

func (d *macDriver) Focus(ctx context.Context, window Window) error {
	if strings.HasPrefix(window.ID, "screen:") {
		return ctx.Err()
	}
	if !d.Status().Control {
		return errors.New(d.Status().Reason)
	}
	_, pid, err := macWindowID(window.ID)
	if err != nil {
		return err
	}
	if C.hc_focus(C.int(pid), C.int(window.X), C.int(window.Y), C.int(window.Width), C.int(window.Height)) == 0 {
		return errors.New("无法激活指定 macOS 窗口；请手动将该窗口置前后重新截图")
	}
	for range 40 {
		if d.Check(ctx, window) == nil {
			return nil
		}
		if err := Pause(ctx, 50*time.Millisecond); err != nil {
			return err
		}
	}
	return errors.New("目标窗口未成为前台窗口，已拒绝输入")
}

func (d *macDriver) Check(ctx context.Context, window Window) error {
	if !d.Status().Control {
		return errors.New(d.Status().Reason)
	}
	current, err := FindWindow(ctx, d, window.ID)
	if err != nil || !SameBounds(current, window) {
		return errors.New("目标窗口已关闭或位置发生变化，请重新截图")
	}
	if strings.HasPrefix(window.ID, "screen:") {
		return nil
	}
	number, pid, err := macWindowID(window.ID)
	if err != nil || !bool(C.hc_front(C.uint(number), C.int(pid))) {
		return errors.New("用户切换了前台窗口，已停止桌面输入")
	}
	return nil
}

func (d *macDriver) Move(ctx context.Context, window Window, x, y int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	pid := 0
	if !strings.HasPrefix(window.ID, "screen:") {
		_, pid, _ = macWindowID(window.ID)
	}
	if !bool(C.hc_point_allowed(C.int(window.X+x), C.int(window.Y+y), C.int(pid))) {
		return errors.New("目标位置被其他应用或 Humbert 审批界面遮挡")
	}
	// DragSmooth 内部会自行按下/松开且无法逐步取消，这里用库的单步 Drag。
	if d.dragging {
		return robotCall(ctx, func() error { return robotgo.Drag(window.X+x, window.Y+y, "left") })
	}
	return robotCall(ctx, func() error { return d.move(window.X+x, window.Y+y) })
}
func (d *macDriver) Emergency() bool { return bool(C.hc_emergency()) }
func (d *macDriver) Close() error    { return nil }
