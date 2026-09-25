package main

import (
	"fmt"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
)

func main() {
	// 显示浏览器窗口
	url := launcher.New().
		Headless(false).
		MustLaunch()

	browser := rod.New().
		ControlURL(url).
		MustConnect()

	defer browser.MustClose()

	page := browser.
		MustPage("https://www.baidu.com").
		MustWaitLoad()

	page = page.Timeout(15 * time.Second)

	page.MustElement("#kw").
		MustInput("Go 语言")

	page.MustElement("#su").
		MustClick()

	page.MustElement("#content_left")

	page.MustWaitStable()

	page.MustScreenshotFullPage("baidu-go.png")

	fmt.Println("截图完成: baidu-go.png")

	// 方便观察结果
	time.Sleep(3 * time.Second)
}
