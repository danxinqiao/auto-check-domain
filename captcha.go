package main

import (
	"fmt"
	"log"
	"math"
	"math/rand"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/go-rod/rod/lib/proto"
)

// openBrowser 启动浏览器并导航到一口价页面，返回浏览器和页面实例。
// 调用方负责在不再使用时关闭浏览器。
func openBrowser() (*rod.Browser, *rod.Page, error) {
	log.Println("启动浏览器（headless 无头模式）...")

	u := launcher.New().
		Headless(true).
		Set("no-sandbox").
		Set("disable-blink-features", "AutomationControlled").
		MustLaunch()

	browser := rod.New().ControlURL(u).MustConnect()

	// 先创建空白页注入反检测脚本，再导航到目标页面
	page := browser.MustPage("")

	// 注入反检测脚本（在后续新文档加载前执行）
	_, _ = proto.PageAddScriptToEvaluateOnNewDocument{
		Source: `
			Object.defineProperty(navigator, 'webdriver', {get: () => undefined});
			Object.defineProperty(navigator, 'plugins', {get: () => [1, 2, 3, 4, 5]});
			Object.defineProperty(navigator, 'languages', {get: () => ['zh-CN', 'zh']});
		`,
	}.Call(page)

	log.Println("访问一口价页面 https://www.juyu.com/ykj/ ...")
	if err := page.Navigate("https://www.juyu.com/ykj/"); err != nil {
		browser.MustClose()
		return nil, nil, fmt.Errorf("导航失败: %w", err)
	}
	page.MustWaitLoad()

	title := page.MustInfo().Title
	log.Printf("一口价页面加载完成，标题: %s", title)

	return browser, page, nil
}

// solveCaptcha 点击搜索按钮触发验证并完成滑块滑动验证。
// 内部使用 Must* 系列方法，失败时会 panic（与既有验证流程一致）。
func solveCaptcha(page *rod.Page) {
	// 等待搜索按钮渲染完成（MustElement 自动等待元素出现）
	log.Println("等待搜索按钮渲染...")
	page.MustElement("button#cha")
	log.Println("搜索按钮已就绪")

	// 点击搜索按钮触发验证
	log.Println("点击搜索按钮触发验证...")
	page.MustElement("button#cha").MustClick()
	log.Println("已点击搜索按钮")

	// 等待滑块元素出现
	log.Println("等待滑块元素...")
	thumb := page.MustElement(".captcha-slider__thumb")
	log.Println("滑块元素已找到")

	// 获取滑块 thumb 的位置 bounding box
	shape := thumb.MustShape()
	box := shape.Box()

	// 动态获取滑轨实际宽度（thumb 的父元素即为滑轨）
	trackWidth := page.MustEval(`() => document.querySelector('.captcha-slider__thumb').parentElement.getBoundingClientRect().width`).Num()
	distance := trackWidth - box.Width
	log.Printf("滑动距离: %.2fpx（滑轨 %.2fpx - 滑块 %.2fpx）", distance, trackWidth, box.Width)

	// 滑块起始位置中心点
	startX := box.X + box.Width/2
	startY := box.Y + box.Height/2

	// 模拟人类拖动滑块
	mouse := page.Mouse

	mouse.MustMoveTo(startX, startY)
	randomSleep(200, 400)

	mouse.MustDown(proto.InputMouseButtonLeft)
	randomSleep(100, 200)

	// 分步滑动（模拟人类不匀速运动，先慢后快再慢）
	steps := 15 + rand.Intn(6) // 15~20 步
	for i := 1; i <= steps; i++ {
		progress := float64(i) / float64(steps)
		// 使用 sigmoid 型缓动曲线
		denom := 1.0 - progress + 0.01
		eased := 1.0 / (1.0 + math.Pow(progress/denom, -1.5))
		targetX := startX + distance*math.Min(eased, 1.0)
		// 加一点垂直抖动
		jitterY := (rand.Float64() - 0.5) * 3

		mouse.MustMoveTo(targetX, startY+jitterY)
		randomSleep(15, 40)
	}

	// 最终精确到位
	mouse.MustMoveTo(startX+distance, startY)
	randomSleep(80, 150)
	mouse.MustUp(proto.InputMouseButtonLeft)
	log.Println("滑块释放完成")

	// 等待验证完成（滑块元素消失即验证成功，最多等 10 秒）
	log.Println("等待验证完成...")
	page.Timeout(10 * time.Second).Wait(rod.Eval(`() => !document.querySelector('.captcha-slider__thumb')`))
	log.Println("验证完成")
}

// randomSleep 随机休眠指定范围（毫秒）
func randomSleep(minMs, maxMs int) {
	d := time.Duration(minMs+rand.Intn(maxMs-minMs+1)) * time.Millisecond
	time.Sleep(d)
}
