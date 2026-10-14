package computer

import (
	"sync/atomic"
	"time"
)

var inputRunning atomic.Bool
var inputFinished atomic.Int64

// BeginInput 让宿主拒绝由自动化鼠标/键盘触发的自审批和权限修改。
// Wayland 不提供任意窗口的进程身份，不能只依靠窗口命中测试保护自身界面。
func BeginInput() func() {
	inputRunning.Store(true)
	return func() { inputFinished.Store(time.Now().UnixNano()); inputRunning.Store(false) }
}

func InputActive() bool {
	// 原生输入与 WebView 的点击回调异步到达，留出短暂派发余量。
	return inputRunning.Load() || time.Since(time.Unix(0, inputFinished.Load())) < time.Second
}
