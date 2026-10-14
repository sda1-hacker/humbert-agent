//go:build (darwin && !cgo) || (!darwin && !windows && !linux)

package computer

import (
	"context"
	"errors"
)

// 非桌面构建显式标记不可用，不影响无 CGO 的离线命令编译。
type unavailableDriver struct{}

func NewDriver() Driver { return &unavailableDriver{} }
func (d *unavailableDriver) Status() Status {
	return Status{Backend: "unavailable", Reason: "当前构建不支持桌面控制；macOS 桌面构建必须启用 CGO。"}
}
func (d *unavailableDriver) err() error                                      { return errors.New(d.Status().Reason) }
func (d *unavailableDriver) Windows(context.Context) ([]Window, error)       { return nil, d.err() }
func (d *unavailableDriver) Capture(context.Context, string) (Frame, error)  { return Frame{}, d.err() }
func (d *unavailableDriver) Focus(context.Context, Window) error             { return d.err() }
func (d *unavailableDriver) Check(context.Context, Window) error             { return d.err() }
func (d *unavailableDriver) Move(context.Context, Window, int, int) error    { return d.err() }
func (d *unavailableDriver) Button(context.Context, string, bool, int) error { return d.err() }
func (d *unavailableDriver) Scroll(context.Context, int, int) error          { return d.err() }
func (d *unavailableDriver) Text(context.Context, string) error              { return d.err() }
func (d *unavailableDriver) Key(context.Context, string, bool) error         { return d.err() }
func (d *unavailableDriver) Emergency() bool                                 { return false }
func (d *unavailableDriver) Close() error                                    { return nil }
