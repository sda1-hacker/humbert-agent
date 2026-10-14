//go:build windows

package computer

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
	"unsafe"

	robotgo "github.com/go-vgo/robotgo/win"
	"golang.org/x/sys/windows"
)

var (
	user32     = windows.NewLazySystemDLL("user32.dll")
	windowScan struct {
		sync.Mutex
		window  func(uintptr) uintptr
		monitor func(uintptr, uintptr) uintptr
	}
	// NewCallback 的槽位不会回收；窗口刷新只更换访问函数，不重复注册回调。
	windowCallback  = windows.NewCallback(func(hwnd, _ uintptr) uintptr { return windowScan.window(hwnd) })
	monitorCallback = windows.NewCallback(func(monitor, dc, rect, data uintptr) uintptr { return windowScan.monitor(monitor, rect) })
)

type winDriver struct{ robotInput }
type winRect struct{ Left, Top, Right, Bottom int32 }

func NewDriver() Driver {
	return &winDriver{robotInput: robotInput{
		move: robotgo.Move, toggle: robotgo.Toggle, click: robotgo.Click, scroll: robotgo.Scroll,
		text: robotgo.TypeStr, keyTap: robotgo.KeyTap, keyToggle: robotgo.KeyToggle,
		allowed: winInputAllowed,
	}}
}

func (d *winDriver) Status() Status {
	return Status{Backend: "Windows Win32/RobotGo", Capture: true, Control: true, Reason: "仅支持当前用户的普通交互桌面；UAC 安全桌面、锁屏和更高权限应用不能控制。"}
}

// DPI 感知只作用于当前调用线程，避免改变 Wails 主窗口的缩放设置。
func winDPI() func() {
	runtime.LockOSThread()
	proc := user32.NewProc("SetThreadDpiAwarenessContext")
	var previous uintptr
	if proc.Find() == nil {
		previous, _, _ = proc.Call(^uintptr(3)) // PER_MONITOR_AWARE_V2 (-4)
	}
	return func() {
		if previous != 0 {
			proc.Call(previous)
		}
		runtime.UnlockOSThread()
	}
}

func (d *winDriver) Windows(ctx context.Context) ([]Window, error) {
	windowScan.Lock()
	defer windowScan.Unlock()
	defer func() { windowScan.window, windowScan.monitor = nil, nil }()
	defer winDPI()()
	result := []Window{}
	windowScan.monitor = func(monitor, rect uintptr) uintptr {
		var bounds winRect
		if rect != 0 {
			bounds = *(*winRect)(unsafe.Pointer(rect))
			result = append(result, Window{ID: fmt.Sprintf("screen:%x", monitor), App: "桌面（包含菜单与所有可见应用）", Title: fmt.Sprintf("屏幕 %d", len(result)+1), X: int(bounds.Left), Y: int(bounds.Top), Width: int(bounds.Right - bounds.Left), Height: int(bounds.Bottom - bounds.Top)})
		}
		return 1
	}
	user32.NewProc("EnumDisplayMonitors").Call(0, 0, monitorCallback, 0)
	windowScan.window = func(hwnd uintptr) uintptr {
		if ctx.Err() != nil || len(result) >= 80 {
			return 0
		}
		visible, _, _ := user32.NewProc("IsWindowVisible").Call(hwnd)
		iconic, _, _ := user32.NewProc("IsIconic").Call(hwnd)
		x, y, width, height := robotgo.GetBounds(int(hwnd), true)
		if visible == 0 || iconic != 0 || width < 40 || height < 40 || width > 8192 || height > 8192 {
			return 1
		}
		var pid uint32
		user32.NewProc("GetWindowThreadProcessId").Call(hwnd, uintptr(unsafe.Pointer(&pid)))
		if pid == windows.GetCurrentProcessId() {
			return 1
		}
		title := make([]uint16, 512)
		user32.NewProc("GetWindowTextW").Call(hwnd, uintptr(unsafe.Pointer(&title[0])), uintptr(len(title)))
		process, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
		if err != nil {
			return 1
		}
		defer windows.CloseHandle(process)
		var created, exited, kernel, cpu windows.Filetime
		if windows.GetProcessTimes(process, &created, &exited, &kernel, &cpu) != nil {
			return 1
		}
		path := make([]uint16, 1024)
		size := uint32(len(path))
		if windows.QueryFullProcessImageName(process, 0, &path[0], &size) != nil {
			return 1
		}
		result = append(result, Window{
			ID:  fmt.Sprintf("%x:%d:%d", hwnd, pid, uint64(created.HighDateTime)<<32|uint64(created.LowDateTime)),
			App: filepath.Base(windows.UTF16ToString(path[:size])), Title: windows.UTF16ToString(title),
			X: x, Y: y, Width: width, Height: height,
		})
		return 1
	}
	user32.NewProc("EnumWindows").Call(windowCallback, 0)
	return result, ctx.Err()
}

func winHandle(id string) (uintptr, error) {
	parts := strings.Split(id, ":")
	if len(parts) != 3 {
		return 0, errors.New("Windows 窗口 ID 无效")
	}
	hwnd, err := strconv.ParseUint(parts[0], 16, 64)
	return uintptr(hwnd), err
}

func (d *winDriver) Focus(ctx context.Context, window Window) error {
	if strings.HasPrefix(window.ID, "screen:") {
		return ctx.Err()
	}
	hwnd, err := winHandle(window.ID)
	if err != nil {
		return err
	}
	if err := robotCall(ctx, func() error { return robotgo.ActivePid(int(hwnd), true) }); err != nil {
		return err
	}
	for range 20 {
		if d.Check(ctx, window) == nil {
			return nil
		}
		if err := Pause(ctx, 50*time.Millisecond); err != nil {
			return err
		}
	}
	return errors.New("Windows 不允许激活目标窗口，请手动置前后重新截图")
}

func (d *winDriver) Check(ctx context.Context, window Window) error {
	current, err := FindWindow(ctx, d, window.ID)
	if err != nil || !SameBounds(current, window) {
		return errors.New("目标窗口已关闭或位置发生变化，请重新截图")
	}
	if strings.HasPrefix(window.ID, "screen:") {
		return nil
	}
	hwnd, err := winHandle(window.ID)
	front, _, _ := user32.NewProc("GetForegroundWindow").Call()
	if err != nil || front != hwnd {
		return errors.New("用户切换了前台窗口，已停止桌面输入")
	}
	return nil
}

func (d *winDriver) Capture(ctx context.Context, id string) (Frame, error) {
	defer winDPI()()
	window, err := FindWindow(ctx, d, id)
	if err != nil {
		return Frame{}, err
	}
	if err := d.Check(ctx, window); err != nil {
		return Frame{}, err
	}
	// 位图与 GDI 资源由 RobotGo 回收；保留物理像素的 DPI 感知与目标窗口检查。
	im, err := robotgo.CaptureImg(window.X, window.Y, window.Width, window.Height)
	return Frame{Window: window, Image: im}, errors.Join(err, ctx.Err())
}

func (d *winDriver) Move(ctx context.Context, window Window, x, y int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	defer winDPI()()
	px, py := window.X+x, window.Y+y
	point := uint64(uint32(px)) | uint64(uint32(py))<<32
	// 坐标被其他窗口遮挡时拒绝点击，不能把批准的窗口范围当成屏幕全局权限。
	var target uintptr
	if unsafe.Sizeof(uintptr(0)) == 4 {
		target, _, _ = user32.NewProc("WindowFromPoint").Call(uintptr(uint32(px)), uintptr(uint32(py)))
	} else {
		target, _, _ = user32.NewProc("WindowFromPoint").Call(uintptr(point))
	}
	root, _, _ := user32.NewProc("GetAncestor").Call(target, 2)
	var pid uint32
	user32.NewProc("GetWindowThreadProcessId").Call(root, uintptr(unsafe.Pointer(&pid)))
	if pid == windows.GetCurrentProcessId() {
		return errors.New("禁止通过桌面工具操作 Humbert 自身的审批或设置界面")
	}
	if strings.HasPrefix(window.ID, "screen:") {
		return robotCall(ctx, func() error { return d.move(px, py) })
	}
	hwnd, err := winHandle(window.ID)
	if err != nil || root != hwnd {
		return errors.New("目标坐标被其他窗口遮挡，请重新截图")
	}
	return robotCall(ctx, func() error { return d.move(px, py) })
}
func (d *winDriver) Emergency() bool {
	for _, key := range []uintptr{0x11, 0x12, 27} {
		state, _, _ := user32.NewProc("GetAsyncKeyState").Call(key)
		if state&0x8000 == 0 {
			return false
		}
	}
	return true
}
func (d *winDriver) Close() error { return nil }

func winInputAllowed() bool {
	hwnd, _, _ := user32.NewProc("GetForegroundWindow").Call()
	var pid uint32
	user32.NewProc("GetWindowThreadProcessId").Call(hwnd, uintptr(unsafe.Pointer(&pid)))
	return hwnd != 0 && pid != windows.GetCurrentProcessId()
}
