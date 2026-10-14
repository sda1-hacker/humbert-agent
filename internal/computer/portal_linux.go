//go:build linux

package computer

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image/png"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/google/uuid"
)

const portalDestination = "org.freedesktop.portal.Desktop"
const portalDesktopPath = dbus.ObjectPath("/org/freedesktop/portal/desktop")
const remoteDesktop = "org.freedesktop.portal.RemoteDesktop"
const screenCast = "org.freedesktop.portal.ScreenCast"

type portalDriver struct {
	conn      *dbus.Conn
	session   dbus.ObjectPath
	stream    uint32
	window    Window
	closed    atomic.Bool
	closeOnce sync.Once
	done      chan struct{}
}
type portalStream struct {
	Node       uint32
	Properties map[string]dbus.Variant
}

func newPortalDriver() Driver { return &portalDriver{done: make(chan struct{})} }

func portalCaptureProgram() string {
	// 只启动固定的系统采集器，不从工作目录或模型参数解析可执行文件。
	for _, path := range []string{"/usr/bin/gst-launch-1.0", "/usr/local/bin/gst-launch-1.0"} {
		if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() && info.Mode()&0111 != 0 {
			return path
		}
	}
	return ""
}
func (d *portalDriver) Status() Status {
	ok := portalCaptureProgram() != "" && os.Getenv("DBUS_SESSION_BUS_ADDRESS") != ""
	status := Status{Backend: "Linux Wayland/XDG RemoteDesktop + PipeWire", Capture: ok, Control: ok, Reason: "首次截图由系统询问共享画面与键鼠控制；键盘授权属于桌面会话，可在系统共享指示器或聊天停止按钮中接管。"}
	if !ok {
		status.Reason = "Wayland 需要 xdg-desktop-portal 的 RemoteDesktop/ScreenCast 后端，以及 GStreamer 的 pipewiresrc、videoconvert、pngenc 和 gst-launch-1.0。"
	}
	return status
}
func (d *portalDriver) Windows(ctx context.Context) ([]Window, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if d.closed.Load() {
		return nil, errors.New("用户已停止桌面共享")
	}
	if d.window.ID != "" {
		return []Window{d.window}, nil
	}
	return []Window{{ID: "portal", App: "Wayland 用户授权的桌面", Title: "截图时由用户在系统对话框中选择"}}, nil
}

// Response 信号订阅必须先于方法调用，否则快速响应会被遗漏；取消时关闭系统请求。
func (d *portalDriver) request(ctx context.Context, method string, args ...any) (map[string]dbus.Variant, error) {
	token := "humbert_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	options := map[string]dbus.Variant{"handle_token": dbus.MakeVariant(token)}
	if len(args) > 0 {
		if value, ok := args[len(args)-1].(map[string]dbus.Variant); ok {
			options = value
			options["handle_token"] = dbus.MakeVariant(token)
			args = args[:len(args)-1]
		}
	}
	args = append(args, options)
	sender := strings.ReplaceAll(strings.TrimPrefix(d.conn.Names()[0], ":"), ".", "_")
	path := dbus.ObjectPath("/org/freedesktop/portal/desktop/request/" + sender + "/" + token)
	match := []dbus.MatchOption{dbus.WithMatchSender(portalDestination), dbus.WithMatchObjectPath(path), dbus.WithMatchInterface("org.freedesktop.portal.Request"), dbus.WithMatchMember("Response")}
	if err := d.conn.AddMatchSignal(match...); err != nil {
		return nil, err
	}
	defer d.conn.RemoveMatchSignal(match...)
	signals := make(chan *dbus.Signal, 8)
	d.conn.Signal(signals)
	defer d.conn.RemoveSignal(signals)
	var returned dbus.ObjectPath
	if err := d.conn.Object(portalDestination, portalDesktopPath).CallWithContext(ctx, method, 0, args...).Store(&returned); err != nil {
		return nil, fmt.Errorf("Wayland Portal %s: %w", method, err)
	}
	if returned != path {
		return nil, errors.New("Portal 未遵守请求标识协议，已拒绝继续")
	}
	select {
	case <-ctx.Done():
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		defer cancel()
		d.conn.Object(portalDestination, path).CallWithContext(cleanup, "org.freedesktop.portal.Request.Close", 0)
		return nil, ctx.Err()
	case signal := <-signals:
		var code uint32
		var result map[string]dbus.Variant
		if signal == nil || signal.Sender == "" || dbus.Store(signal.Body, &code, &result) != nil {
			return nil, errors.New("Portal 返回无效响应")
		}
		if code != 0 {
			return nil, errors.New("用户取消或系统拒绝了桌面共享/控制")
		}
		return result, nil
	}
}

func (d *portalDriver) start(ctx context.Context) error {
	if d.closed.Load() {
		return errors.New("用户已停止桌面共享")
	}
	if d.session != "" {
		return nil
	}
	if !d.Status().Capture {
		return errors.New(d.Status().Reason)
	}
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return fmt.Errorf("连接桌面 Portal 失败: %w", err)
	}
	d.conn = conn
	failed := true
	defer func() {
		if failed {
			d.Close()
		}
	}()
	result, err := d.request(ctx, remoteDesktop+".CreateSession", map[string]dbus.Variant{"session_handle_token": dbus.MakeVariant("humbert_" + strings.ReplaceAll(uuid.NewString(), "-", ""))})
	if err != nil {
		return err
	}
	value, ok := result["session_handle"]
	if !ok {
		return errors.New("Portal 缺少会话标识")
	}
	if err := dbus.Store([]any{value.Value()}, &d.session); err != nil {
		if text, ok := value.Value().(string); ok {
			d.session = dbus.ObjectPath(text)
		} else {
			return errors.New("Portal 会话标识无效")
		}
	}
	if !d.session.IsValid() {
		return errors.New("Portal 会话路径无效")
	}
	if _, err := d.request(ctx, remoteDesktop+".SelectDevices", d.session, map[string]dbus.Variant{"types": dbus.MakeVariant(uint32(3)), "persist_mode": dbus.MakeVariant(uint32(0))}); err != nil {
		return err
	}
	// Portal 的键盘输入是桌面级权限，因此明确让用户选择屏幕，不伪装成按应用隔离。
	if _, err := d.request(ctx, screenCast+".SelectSources", d.session, map[string]dbus.Variant{"types": dbus.MakeVariant(uint32(1)), "multiple": dbus.MakeVariant(false), "cursor_mode": dbus.MakeVariant(uint32(1))}); err != nil {
		return err
	}
	result, err = d.request(ctx, remoteDesktop+".Start", d.session, "", map[string]dbus.Variant{})
	if err != nil {
		return err
	}
	devices, ok := result["devices"].Value().(uint32)
	if !ok || devices&3 != 3 {
		return errors.New("系统未同时授予键盘和指针控制")
	}
	var streams []portalStream
	if err := dbus.Store([]any{result["streams"].Value()}, &streams); err != nil || len(streams) != 1 {
		return errors.New("Portal 必须返回一个用户选择的画面")
	}
	d.stream = streams[0].Node
	var size []int32
	for _, key := range []string{"logical_size", "size"} {
		if value, ok := streams[0].Properties[key]; ok {
			var pair struct{ Width, Height int32 }
			if dbus.Store([]any{value.Value()}, &pair) == nil {
				size = []int32{pair.Width, pair.Height}
				break
			}
		}
	}
	if len(size) != 2 || size[0] <= 0 || size[1] <= 0 || size[0] > 8192 || size[1] > 8192 {
		return errors.New("Portal 未提供可靠的逻辑画面尺寸，不能安全映射坐标")
	}
	d.window = Window{ID: "portal", App: "Wayland 用户授权的桌面", Title: "由系统共享指示器控制，键盘权限为桌面级", Width: int(size[0]), Height: int(size[1])}
	match := []dbus.MatchOption{dbus.WithMatchSender(portalDestination), dbus.WithMatchObjectPath(d.session), dbus.WithMatchInterface("org.freedesktop.portal.Session"), dbus.WithMatchMember("Closed")}
	if err := conn.AddMatchSignal(match...); err != nil {
		return err
	}
	signals := make(chan *dbus.Signal, 1)
	conn.Signal(signals)
	go func() {
		defer conn.RemoveSignal(signals)
		defer conn.RemoveMatchSignal(match...)
		select {
		case <-d.done:
		case <-signals:
			d.closed.Store(true)
		}
	}()
	failed = false
	return nil
}

// limitWriter 保证外部采集器不能向内存写入无限大图像。
type limitWriter struct {
	bytes.Buffer
	limit int
}

func (w *limitWriter) Write(data []byte) (int, error) {
	if len(data) > w.limit-w.Len() {
		return 0, errors.New("截图输出超过限制")
	}
	return w.Buffer.Write(data)
}

func (d *portalDriver) Capture(ctx context.Context, id string) (Frame, error) {
	if id != "portal" {
		return Frame{}, errors.New("Wayland 只能操作用户在系统中授权的 portal 画面")
	}
	if err := d.start(ctx); err != nil {
		return Frame{}, err
	}
	var fd dbus.UnixFD
	if err := d.conn.Object(portalDestination, portalDesktopPath).CallWithContext(ctx, screenCast+".OpenPipeWireRemote", 0, d.session, map[string]dbus.Variant{}).Store(&fd); err != nil {
		return Frame{}, err
	}
	remote := os.NewFile(uintptr(fd), "humbert-pipewire")
	defer remote.Close()
	// 固定参数直接启动 GStreamer，无 Shell 插值；远端 FD 仅能访问已授权的画面。
	cmd := exec.CommandContext(ctx, portalCaptureProgram(), "-q", "pipewiresrc", "fd=3", "path="+strconv.FormatUint(uint64(d.stream), 10), "num-buffers=1", "!", "videoconvert", "!", "videoscale", "!", fmt.Sprintf("video/x-raw,width=%d,height=%d", d.window.Width, d.window.Height), "!", "pngenc", "!", "fdsink", "fd=1")
	cmd.ExtraFiles = []*os.File{remote}
	cmd.WaitDelay = time.Second
	output := &limitWriter{limit: 32 << 20}
	cmd.Stdout = output
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		return Frame{}, fmt.Errorf("采集 Wayland 画面失败；请检查 PipeWire/GStreamer 插件或重新授权: %w", err)
	}
	config, err := png.DecodeConfig(bytes.NewReader(output.Bytes()))
	if err != nil || config.Width != d.window.Width || config.Height != d.window.Height {
		return Frame{}, errors.New("Wayland 截图尺寸不符合授权画面")
	}
	im, err := png.Decode(bytes.NewReader(output.Bytes()))
	return Frame{Window: d.window, Image: im}, err
}
func (d *portalDriver) Focus(ctx context.Context, window Window) error { return d.Check(ctx, window) }
func (d *portalDriver) Check(ctx context.Context, window Window) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if d.closed.Load() || d.session == "" || !SameBounds(window, d.window) {
		return errors.New("Wayland 授权画面已失效，请重新授权")
	}
	return nil
}
func (d *portalDriver) notify(ctx context.Context, method string, args ...any) error {
	if err := d.Check(ctx, d.window); err != nil {
		return err
	}
	values := []any{d.session, map[string]dbus.Variant{}}
	values = append(values, args...)
	return d.conn.Object(portalDestination, portalDesktopPath).CallWithContext(ctx, remoteDesktop+"."+method, 0, values...).Err
}
func (d *portalDriver) Move(ctx context.Context, _ Window, x, y int) error {
	return d.notify(ctx, "NotifyPointerMotionAbsolute", d.stream, float64(x), float64(y))
}
func (d *portalDriver) Button(ctx context.Context, button string, down bool, _ int) error {
	number, state := int32(0x110), uint32(0)
	if button == "right" {
		number = 0x111
	}
	if down {
		state = 1
	}
	return d.notify(ctx, "NotifyPointerButton", number, state)
}
func (d *portalDriver) Scroll(ctx context.Context, x, y int) error {
	return d.notify(ctx, "NotifyPointerAxis", float64(x), float64(y))
}
func (d *portalDriver) Text(ctx context.Context, text string) error {
	for _, char := range text {
		symbol := uint32(char)
		if symbol > 255 {
			symbol |= 0x01000000
		}
		if err := d.notify(ctx, "NotifyKeyboardKeysym", int32(symbol), uint32(1)); err != nil {
			return err
		}
		if err := d.notify(ctx, "NotifyKeyboardKeysym", int32(symbol), uint32(0)); err != nil {
			return err
		}
	}
	return nil
}
func (d *portalDriver) Key(ctx context.Context, key string, down bool) error {
	symbol := linuxKeysym(key)
	if symbol == 0 {
		return errors.New("Wayland 不支持该按键")
	}
	state := uint32(0)
	if down {
		state = 1
	}
	return d.notify(ctx, "NotifyKeyboardKeysym", int32(symbol), state)
}
func (d *portalDriver) Emergency() bool { return d.closed.Load() }
func (d *portalDriver) Close() error {
	var err error
	d.closeOnce.Do(func() {
		d.closed.Store(true)
		close(d.done)
		if d.conn != nil {
			if d.session != "" {
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				d.conn.Object(portalDestination, d.session).CallWithContext(ctx, "org.freedesktop.portal.Session.Close", 0)
				cancel()
			}
			err = d.conn.Close()
		}
	})
	return err
}
