//go:build linux

package computer

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"image/color"
	"io"
	"math/bits"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	robotgo "github.com/go-vgo/robotgo/x11"
	"github.com/jezek/xgb"
	"github.com/jezek/xgb/xproto"
)

type xDriver struct {
	robotInput
	conn      *xgb.Conn
	socket    net.Conn
	screen    *xproto.ScreenInfo
	atoms     map[string]xproto.Atom
	closeOnce sync.Once
}

func NewDriver() Driver {
	// Wayland 会话不能退回 XWayland 偷控窗口；必须通过用户授权的 Portal。
	if os.Getenv("WAYLAND_DISPLAY") != "" || strings.EqualFold(os.Getenv("XDG_SESSION_TYPE"), "wayland") {
		return newPortalDriver()
	}
	return &xDriver{atoms: map[string]xproto.Atom{}, robotInput: robotInput{
		move: robotgo.Move, toggle: robotgo.Toggle, click: robotgo.Click, scroll: robotgo.Scroll,
		text: robotgo.TypeStr, keyTap: robotgo.KeyTap, keyToggle: robotgo.KeyToggle,
	}}
}

func (d *xDriver) Status() Status {
	ok := strings.HasPrefix(os.Getenv("DISPLAY"), ":")
	status := Status{Backend: "Linux X11/RobotGo", Capture: ok, Control: ok}
	if !ok {
		status.Reason = "需要本机 X11 图形会话（DISPLAY=:N）；不连接远程 X Server。"
	}
	return status
}

func (d *xDriver) connect(ctx context.Context) error {
	if d.conn != nil {
		return nil
	}
	if !d.Status().Capture {
		return errors.New(d.Status().Reason)
	}
	display := strings.Split(strings.TrimPrefix(os.Getenv("DISPLAY"), ":"), ".")[0]
	if _, err := strconv.ParseUint(display, 10, 16); err != nil {
		return errors.New("DISPLAY 无效")
	}
	socket, err := (&net.Dialer{Timeout: 2 * time.Second}).DialContext(ctx, "unix", "/tmp/.X11-unix/X"+display)
	if err != nil {
		return fmt.Errorf("连接 X11 失败: %w", err)
	}
	socket.SetDeadline(time.Now().Add(3 * time.Second))
	cookie, err := xAuthority(display)
	if err != nil {
		socket.Close()
		return err
	}
	conn, err := xgb.NewConnNetWithCookieHex(socket, cookie)
	if err != nil {
		socket.Close()
		return fmt.Errorf("X11 认证失败: %w", err)
	}
	d.conn, d.socket = conn, socket
	socket.SetDeadline(time.Time{})
	setup := xproto.Setup(conn)
	screen := 0
	if part := strings.Split(os.Getenv("DISPLAY"), "."); len(part) == 2 {
		screen, _ = strconv.Atoi(part[1])
	}
	if screen < 0 || screen >= len(setup.Roots) {
		d.Close()
		return errors.New("X11 屏幕不存在")
	}
	d.screen = &setup.Roots[screen]
	return nil
}

// 只读取当前会话的 MIT Cookie，限制文件大小，不把 Cookie 带入工具输出或日志。
func xAuthority(display string) (string, error) {
	path := os.Getenv("XAUTHORITY")
	if path == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		path = filepath.Join(home, ".Xauthority")
	}
	file, err := os.Open(path)
	if err != nil {
		return "", errors.New("无法读取当前 X11 会话认证文件")
	}
	defer file.Close()
	reader := io.LimitReader(file, 1<<20)
	host, _ := os.Hostname()
	for {
		var family uint16
		if binary.Read(reader, binary.BigEndian, &family) != nil {
			break
		}
		fields := make([][]byte, 4)
		for i := range fields {
			var size uint16
			if binary.Read(reader, binary.BigEndian, &size) != nil {
				return "", errors.New("X11 认证文件不完整")
			}
			fields[i] = make([]byte, size)
			if _, err := io.ReadFull(reader, fields[i]); err != nil {
				return "", errors.New("X11 认证文件不完整")
			}
		}
		if (family == 65535 || family == 256 && string(fields[0]) == host) && (string(fields[1]) == display || len(fields[1]) == 0) && string(fields[2]) == "MIT-MAGIC-COOKIE-1" && len(fields[3]) == 16 {
			return hex.EncodeToString(fields[3]), nil
		}
	}
	return "", errors.New("没有当前 DISPLAY 对应的 X11 认证信息")
}

func (d *xDriver) bounded(ctx context.Context) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := d.connect(ctx); err != nil {
		return nil, err
	}
	deadline := time.Now().Add(5 * time.Second)
	if value, ok := ctx.Deadline(); ok && value.Before(deadline) {
		deadline = value
	}
	d.socket.SetDeadline(deadline)
	stop := context.AfterFunc(ctx, func() { d.Close() })
	return func() { stop(); d.socket.SetDeadline(time.Time{}) }, nil
}
func (d *xDriver) atom(name string) (xproto.Atom, error) {
	if atom, ok := d.atoms[name]; ok {
		return atom, nil
	}
	reply, err := xproto.InternAtom(d.conn, false, uint16(len(name)), name).Reply()
	if err != nil {
		return 0, err
	}
	d.atoms[name] = reply.Atom
	return reply.Atom, nil
}
func (d *xDriver) property(window xproto.Window, name string) ([]byte, error) {
	atom, err := d.atom(name)
	if err != nil {
		return nil, err
	}
	reply, err := xproto.GetProperty(d.conn, false, window, atom, xproto.GetPropertyTypeAny, 0, 4096).Reply()
	if err != nil {
		return nil, err
	}
	return reply.Value, nil
}
func (d *xDriver) Windows(ctx context.Context) ([]Window, error) {
	end, err := d.bounded(ctx)
	if err != nil {
		return nil, err
	}
	defer end()
	data, err := d.property(d.screen.Root, "_NET_CLIENT_LIST_STACKING")
	if err != nil {
		return nil, err
	}
	if len(data) == 0 {
		data, err = d.property(d.screen.Root, "_NET_CLIENT_LIST")
	}
	if err != nil {
		return nil, err
	}
	result := []Window{{ID: fmt.Sprintf("screen:%d", d.screen.Root), App: "桌面（包含菜单与所有可见应用）", Title: "X11 桌面", Width: int(d.screen.WidthInPixels), Height: int(d.screen.HeightInPixels)}}
	for i := len(data) - 4; i >= 0 && len(result) < 80; i -= 4 {
		id := xproto.Window(xgb.Get32(data[i:]))
		attrs, err := xproto.GetWindowAttributes(d.conn, id).Reply()
		if err != nil || attrs.MapState != xproto.MapStateViewable {
			continue
		}
		x, y, width, height := robotgo.GetClient(int(id), true)
		if width < 40 || height < 40 || width > 8192 || height > 8192 {
			continue
		}
		pidData, _ := d.property(id, "_NET_WM_PID")
		pid := uint32(0)
		if len(pidData) >= 4 {
			pid = xgb.Get32(pidData)
		}
		if int(pid) == os.Getpid() {
			continue
		}
		// 没有可确认进程身份的窗口不授予可复用桌面操作权限。
		if pid == 0 {
			continue
		}
		stat, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
		if err != nil {
			continue
		}
		parts := strings.Fields(string(stat[strings.LastIndex(string(stat), ")")+1:]))
		if len(parts) < 20 {
			continue
		}
		executable, err := os.Readlink(fmt.Sprintf("/proc/%d/exe", pid))
		if err != nil {
			continue
		}
		title, _ := d.property(id, "_NET_WM_NAME")
		if len(title) == 0 {
			title, _ = d.property(id, "WM_NAME")
		}
		result = append(result, Window{ID: fmt.Sprintf("%d:%d:%s", id, pid, parts[19]), App: filepath.Base(executable), Title: strings.ToValidUTF8(strings.Trim(string(title), "\x00"), "�"), X: x, Y: y, Width: width, Height: height})
	}
	return result, ctx.Err()
}
func xWindow(id string) (xproto.Window, error) {
	parts := strings.Split(id, ":")
	if len(parts) != 3 {
		return 0, errors.New("X11 窗口 ID 无效")
	}
	number, err := strconv.ParseUint(parts[0], 10, 32)
	return xproto.Window(number), err
}
func (d *xDriver) Focus(ctx context.Context, window Window) error {
	if strings.HasPrefix(window.ID, "screen:") {
		return ctx.Err()
	}
	end, err := d.bounded(ctx)
	if err != nil {
		return err
	}
	defer end()
	id, err := xWindow(window.ID)
	if err != nil {
		return err
	}
	if err := robotCall(ctx, func() error { return robotgo.ActivePid(int(id), true) }); err != nil {
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
	return errors.New("X11 目标窗口无法置前，请手动置前后重新截图")
}
func (d *xDriver) Check(ctx context.Context, window Window) error {
	current, err := FindWindow(ctx, d, window.ID)
	if err != nil || !SameBounds(current, window) {
		return errors.New("目标窗口已关闭或位置变化，请重新截图")
	}
	if strings.HasPrefix(window.ID, "screen:") {
		return nil
	}
	data, err := d.property(d.screen.Root, "_NET_ACTIVE_WINDOW")
	id, idErr := xWindow(window.ID)
	if err != nil || idErr != nil || len(data) < 4 || xproto.Window(xgb.Get32(data)) != id {
		return errors.New("用户切换了前台窗口，已停止桌面输入")
	}
	return nil
}
func (d *xDriver) Capture(ctx context.Context, id string) (Frame, error) {
	window, err := FindWindow(ctx, d, id)
	if err != nil {
		return Frame{}, err
	}
	end, err := d.bounded(ctx)
	if err != nil {
		return Frame{}, err
	}
	defer end()
	if window.X < 0 || window.Y < 0 || window.X+window.Width > int(d.screen.WidthInPixels) || window.Y+window.Height > int(d.screen.HeightInPixels) {
		return Frame{}, errors.New("目标窗口超出屏幕，请完整移入屏幕后重试")
	}
	setup := xproto.Setup(d.conn)
	var format xproto.Format
	for _, value := range setup.PixmapFormats {
		if value.Depth == d.screen.RootDepth {
			format = value
			break
		}
	}
	var visual xproto.VisualInfo
	for _, depth := range d.screen.AllowedDepths {
		for _, value := range depth.Visuals {
			if value.VisualId == d.screen.RootVisual {
				visual = value
			}
		}
	}
	// RobotGo 按 BGRX 解码；常见布局直接交给库，其他深度/字节序保留兼容。
	if format.BitsPerPixel == 32 && format.ScanlinePad == 32 && setup.ImageByteOrder == xproto.ImageOrderLSBFirst && visual.RedMask == 0xff0000 && visual.GreenMask == 0xff00 && visual.BlueMask == 0xff {
		captureCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		stop := context.AfterFunc(captureCtx, robotgo.Close)
		defer stop()
		im, err := robotgo.CaptureImg(window.X, window.Y, window.Width, window.Height)
		return Frame{Window: window, Image: im}, errors.Join(err, captureCtx.Err())
	}
	bytesPerPixel := int(format.BitsPerPixel) / 8
	if bytesPerPixel < 2 || bytesPerPixel > 4 || format.ScanlinePad == 0 || visual.RedMask == 0 || visual.GreenMask == 0 || visual.BlueMask == 0 {
		return Frame{}, errors.New("X11 像素格式不支持")
	}
	reply, err := xproto.GetImage(d.conn, xproto.ImageFormatZPixmap, xproto.Drawable(d.screen.Root), int16(window.X), int16(window.Y), uint16(window.Width), uint16(window.Height), ^uint32(0)).Reply()
	if err != nil {
		return Frame{}, err
	}
	stride := ((window.Width*int(format.BitsPerPixel) + int(format.ScanlinePad) - 1) / int(format.ScanlinePad)) * int(format.ScanlinePad) / 8
	if len(reply.Data) < stride*window.Height {
		return Frame{}, errors.New("X11 截图数据不完整")
	}
	im := image.NewRGBA(image.Rect(0, 0, window.Width, window.Height))
	channel := func(value, mask uint32) uint8 {
		return uint8(((value & mask) >> bits.TrailingZeros32(mask)) * 255 / (mask >> bits.TrailingZeros32(mask)))
	}
	for y := 0; y < window.Height; y++ {
		for x := 0; x < window.Width; x++ {
			pixel := reply.Data[y*stride+x*bytesPerPixel:][:bytesPerPixel]
			var value uint32
			for i, b := range pixel {
				if setup.ImageByteOrder == xproto.ImageOrderLSBFirst {
					value |= uint32(b) << uint(i*8)
				} else {
					value = value<<8 | uint32(b)
				}
			}
			im.SetRGBA(x, y, color.RGBA{channel(value, visual.RedMask), channel(value, visual.GreenMask), channel(value, visual.BlueMask), 255})
		}
	}
	return Frame{Window: window, Image: im}, ctx.Err()
}
func (d *xDriver) Move(ctx context.Context, window Window, x, y int) error {
	// 检查该点最上层窗口，拒绝被其他应用遮挡的坐标。
	end, err := d.bounded(ctx)
	if err != nil {
		return err
	}
	defer end()
	if err := robotCall(ctx, func() error { return d.move(window.X+x, window.Y+y) }); err != nil {
		return err
	}
	reply, err := xproto.QueryPointer(d.conn, d.screen.Root).Reply()
	if err == nil {
		data, _ := d.property(reply.Child, "_NET_WM_PID")
		if len(data) >= 4 && int(xgb.Get32(data)) == os.Getpid() {
			return errors.New("禁止通过桌面工具操作 Humbert 自身")
		}
	}
	if strings.HasPrefix(window.ID, "screen:") {
		return err
	}
	id, idErr := xWindow(window.ID)
	if err != nil || idErr != nil {
		return errors.New("无法确认 X11 指针目标")
	}
	child := reply.Child
	for range 10 {
		if child == id {
			return nil
		}
		tree, err := xproto.QueryTree(d.conn, child).Reply()
		if err != nil || tree.Parent == d.screen.Root || tree.Parent == 0 {
			break
		}
		child = tree.Parent
	}
	// 窗口管理器的装饰框是 client 的父节点。
	tree, err := xproto.QueryTree(d.conn, id).Reply()
	if err == nil && tree.Parent == reply.Child {
		return nil
	}
	return errors.New("目标坐标被其他窗口遮挡，请重新截图")
}
func linuxKeysym(key string) uint32 {
	keys := map[string]uint32{"ctrl": 0xffe3, "mod": 0xffe3, "cmd": 0xffeb, "shift": 0xffe1, "alt": 0xffe9, "enter": 0xff0d, "tab": 0xff09, "escape": 0xff1b, "space": 32, "backspace": 0xff08, "delete": 0xffff, "home": 0xff50, "end": 0xff57, "pageup": 0xff55, "pagedown": 0xff56, "arrowleft": 0xff51, "arrowup": 0xff52, "arrowright": 0xff53, "arrowdown": 0xff54}
	if value := keys[key]; value != 0 {
		return value
	}
	if strings.HasPrefix(key, "f") {
		n, err := strconv.Atoi(key[1:])
		if err == nil && n >= 1 && n <= 20 {
			return uint32(0xffbd + n)
		}
	}
	if len(key) == 1 {
		return uint32(key[0])
	}
	return 0
}
func (d *xDriver) Emergency() bool { return false } // X11 用户可用聊天停止键接管；不额外全局抓键。
func (d *xDriver) Close() error {
	d.closeOnce.Do(func() {
		robotgo.Close()
		if d.conn != nil {
			d.conn.Close()
		}
	})
	return nil
}
