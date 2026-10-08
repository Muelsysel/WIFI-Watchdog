//go:build windows

package main

import (
	"fmt"
	"runtime"
	"sync"
	"syscall"
	"unsafe"
)

// Fluent Lite: pure Win32/GDI, no WebView, no runtime assets, no timers, no
// third-party UI dependencies. All window operations run on the dedicated UI
// OS thread. Brushes, fonts and pens are created once for the process lifetime.
const (
	modernPageOverview = iota
	modernPageChecks
	modernPageRecovery
	modernPageVPN
)

const (
	ID_NAV_OVERVIEW = 2101 + iota
	ID_NAV_CHECKS
	ID_NAV_RECOVERY
	ID_NAV_VPN
)

const (
	wmPaint          = 0x000F
	wmEraseBkgnd     = 0x0014
	wmCtlColorEdit   = 0x0133
	wmCtlColorButton = 0x0135
	wmCtlColorStatic = 0x0138
	wmDrawItem       = 0x002B
	bsOwnerDraw      = 0x0000000B
	transparentBk    = 1
	dtCenter         = 0x00000001
	dtVCenter        = 0x00000004
	dtSingleLine     = 0x00000020
	odsDisabled      = 0x0004
	odsSelected      = 0x0001
	odsFocus         = 0x0010
	psSolid          = 0
)

type uiRect struct{ Left, Top, Right, Bottom int32 }
type uiPaintStruct struct {
	HDC       uintptr
	Erase     int32
	Paint     uiRect
	Restore   int32
	IncUpdate int32
	Reserved  [32]byte
}
type uiDrawItem struct {
	CtlType, CtlID, ItemID, ItemAction, ItemState uint32
	HwndItem, HDC                                 uintptr
	Rect                                          uiRect
	ItemData                                      uintptr
}
type processMemoryCountersEx struct {
	Cb               uint32
	Faults           uint32
	PeakWorkingSet   uintptr
	WorkingSet       uintptr
	PeakPagedPool    uintptr
	PagedPool        uintptr
	PeakNonPagedPool uintptr
	NonPagedPool     uintptr
	Pagefile         uintptr
	PeakPagefile     uintptr
	PrivateBytes     uintptr
}
type fluentAssets struct {
	dpi                                                                                 float64
	background, sidebar, white, navActive, accent, accentSoft uintptr
	borderPen, navPen, clearPen, accentPen, sidebarPen                                 uintptr
	font, heading, title                                                                uintptr
}

var fluent struct {
	sync.Once
	assets fluentAssets
}

var (
	procGetDpiForSystem      = user32.NewProc("GetDpiForSystem")
	procBeginPaint           = user32.NewProc("BeginPaint")
	procEndPaint             = user32.NewProc("EndPaint")
	procGetClientRect        = user32.NewProc("GetClientRect")
	procFillRect             = user32.NewProc("FillRect")
	procInvalidateRect       = user32.NewProc("InvalidateRect")
	procDrawFocusRect        = user32.NewProc("DrawFocusRect")
	procDrawTextW            = user32.NewProc("DrawTextW")
	procSetTextColor         = gdi32.NewProc("SetTextColor")
	procSetBkColor           = gdi32.NewProc("SetBkColor")
	procSetBkMode            = gdi32.NewProc("SetBkMode")
	procSelectObject         = gdi32.NewProc("SelectObject")
	procCreateSolidBrush     = gdi32.NewProc("CreateSolidBrush")
	procCreatePen            = gdi32.NewProc("CreatePen")
	procRoundRect            = gdi32.NewProc("RoundRect")
	procCreateFontW          = gdi32.NewProc("CreateFontW")
	procGetCurrentProcess    = kernel32.NewProc("GetCurrentProcess")
	procReadProcessMemory = kernel32.NewProc("ReadProcessMemory")
	psapi                    = syscall.NewLazyDLL("psapi.dll")
	procGetProcessMemoryInfo = psapi.NewProc("GetProcessMemoryInfo")
)

func uiRGB(r, g, b uint32) uintptr { return uintptr(r | (g << 8) | (b << 16)) }
func uiInit() {
	fluent.Do(func() {
		u := &fluent.assets
		u.dpi = 1.0
		if r, _, _ := procGetDpiForSystem.Call(); r >= 96 && r <= 384 {
			u.dpi = float64(r) / 96.0
		}
		// Fit even on Win11 laptops with 150%-200% system scaling.
		screenW, _, _ := procGetSystemMetrics.Call(0)
		screenH, _, _ := procGetSystemMetrics.Call(1)
		if screenW > 0 && screenH > 0 {
			limitW := float64(screenW-40) / 990
			limitH := float64(screenH-72) / 790
			if limitW > 0 && u.dpi > limitW {
				u.dpi = limitW
			}
			if limitH > 0 && u.dpi > limitH {
				u.dpi = limitH
			}
			if u.dpi < 0.75 {
				u.dpi = 0.75
			}
		}
		brush := func(r, g, b uint32) uintptr { h, _, _ := procCreateSolidBrush.Call(uiRGB(r, g, b)); return h }
		u.background = brush(245, 247, 252)
		u.sidebar = brush(16, 28, 51)
		u.white = brush(255, 255, 255)
		u.navActive = brush(35, 57, 93)
		u.accent = brush(35, 105, 235)
		u.accentSoft = brush(230, 239, 255)
		u.borderPen, _, _ = procCreatePen.Call(psSolid, 1, uiRGB(226, 232, 242))
		u.navPen, _, _ = procCreatePen.Call(psSolid, 1, uiRGB(35, 57, 93))
		u.clearPen, _, _ = procCreatePen.Call(psSolid, 1, uiRGB(245, 247, 252))
		u.accentPen, _, _ = procCreatePen.Call(psSolid, 1, uiRGB(35, 105, 235))
		u.sidebarPen, _, _ = procCreatePen.Call(psSolid, 1, uiRGB(16, 28, 51))
		u.font = uiCreateFont(15, 400)
		u.heading = uiCreateFont(19, 600)
		u.title = uiCreateFont(26, 700)
	})
}
func uiS(n int32) int32 {
	if fluent.assets.dpi == 0 {
		uiInit()
	}
	return int32(float64(n)*fluent.assets.dpi + 0.5)
}
func uiCreateFont(size, weight int32) uintptr {
	h := -uiS(size)
	r, _, _ := procCreateFontW.Call(uintptr(int64(h)), 0, 0, 0, uintptr(weight),
		0, 0, 0, 1, 0, 0, 5, 0, uintptr(unsafe.Pointer(wstr("Segoe UI"))))
	return r
}
func uiDefaultFont() uintptr { uiInit(); return fluent.assets.font }
func uiTrack(sc *settingsControls, page int, hwnd uintptr) uintptr {
	if hwnd != 0 {
		sc.pages[page] = append(sc.pages[page], hwnd)
	}
	return hwnd
}
func uiStatic(sc *settingsControls, page int, text string, x, y, w, h int32, muted bool) uintptr {
	hwnd := uiTrack(sc, page, createChild(sc.hwnd, "STATIC", text, 0, x, y, w, h, 0))
	if muted {
		sc.muted[hwnd] = true
	}
	return hwnd
}
func uiButton(sc *settingsControls, page, id int, text string, x, y, w, h int32) uintptr {
	return uiTrack(sc, page, createChild(sc.hwnd, "BUTTON", text, bsOwnerDraw|WS_TABSTOP, x, y, w, h, id))
}
func uiField(sc *settingsControls, page, id int, label, unit string, value int, x, y int32) {
	uiStatic(sc, page, label, x, y, 278, 24, false)
	e := uiTrack(sc, page, createChild(sc.hwnd, "EDIT", fmt.Sprint(value), WS_BORDER|WS_TABSTOP|ES_NUMBER, x, y+29, 150, 35, id))
	sc.edits[id] = e
	uiStatic(sc, page, unit, x+164, y+34, 100, 26, true)
}
func uiCheck(sc *settingsControls, page, id int, label string, checked bool, y int32) uintptr {
	h := uiTrack(sc, page, createChild(sc.hwnd, "BUTTON", label, BS_AUTOCHECKBOX|WS_TABSTOP, 258, y, 648, 32, id))
	setCheck(h, checked)
	return h
}

func buildFluentSettings(sc *settingsControls, c Config, a *App) {
	sc.pages = make(map[int][]uintptr, 5)
	sc.muted = make(map[uintptr]bool, 32)
	sc.page = modernPageOverview
	for i, title := range []string{"概览", "检测策略", "Wi-Fi 恢复", "VPN / 系统"} {
		uiButton(sc, -1, ID_NAV_OVERVIEW+i, title, 17, int32(150+58*i), 178, 46)
	}
	// Overview -- 3 cards with deliberately sparse content.
	state, msg := a.currentStatus()
	sc.summarySystem = uiStatic(sc, modernPageOverview, "监控状态："+msg, 260, 187, 640, 42, false)
	_ = state
	sc.summaryWiFi = uiStatic(sc, modernPageOverview, "网络信息待刷新；正常监控仍在后台运行。", 260, 323, 638, 42, false)
	sc.summaryVPN = uiStatic(sc, modernPageOverview, "VPN/TUN 详情只在手动刷新或系统故障时读取。", 260, 459, 638, 43, false)
	sc.refreshButton = uiButton(sc, modernPageOverview, ID_BUTTON_REFRESH, "刷新网络状态", 260, 558, 166, 42)
	uiButton(sc, modernPageOverview, ID_BUTTON_DIAG, "导出诊断", 438, 558, 136, 42)
	sc.memoryLine = uiStatic(sc, modernPageOverview, "", 594, 566, 316, 30, true)

	// Monitoring: regular polling/thresholds and background resources.
	uiField(sc, modernPageChecks, ID_EDIT_NORMAL_MINUTES, "正常检测间隔", "分钟", c.NormalCheckIntervalMinutes, 263, 241)
	uiField(sc, modernPageChecks, ID_EDIT_FAILURE_SECONDS, "异常快速复检", "秒", c.FailureCheckIntervalSeconds, 596, 241)
	uiField(sc, modernPageChecks, ID_EDIT_FAILURE_COUNT, "连续失败阈值", "次", c.FailureThreshold, 263, 341)
	uiField(sc, modernPageChecks, ID_EDIT_REPAIR_MINUTES, "恢复最小间隔", "分钟", c.RepairRetryIntervalMinutes, 596, 341)
	uiField(sc, modernPageChecks, ID_EDIT_TIMEOUT_SECONDS, "单次连接超时", "秒", c.ConnectionTimeoutSeconds, 263, 441)
	uiField(sc, modernPageChecks, ID_EDIT_LOG_RETENTION, "日志保留天数", "天", c.LogRetentionDays, 596, 441)
	sc.startup = uiCheck(sc, modernPageChecks, ID_CHECK_STARTUP, "开机自动启动（Windows 任务计划程序）", c.StartWithWindows, 541)

	// Wireless repair: scoped, controlled fallback operations.
	uiField(sc, modernPageRecovery, ID_EDIT_DISABLE_SECONDS, "网卡关闭等待", "秒", c.WifiDisableWaitSeconds, 263, 232)
	uiField(sc, modernPageRecovery, ID_EDIT_STARTUP_SECONDS, "网卡启动等待", "秒", c.WifiStartupWaitSeconds, 596, 232)
	uiField(sc, modernPageRecovery, ID_EDIT_CONNECT_RETRY, "Profile 重试次数", "次", c.ConnectRetryCount, 263, 331)
	uiField(sc, modernPageRecovery, ID_EDIT_CONNECT_DELAY, "两次连接等待", "秒", c.ConnectRetryDelaySeconds, 596, 331)
	uiField(sc, modernPageRecovery, ID_EDIT_DHCP_WAIT, "DHCP 续租等待", "秒", c.DHCPRenewWaitSeconds, 263, 430)
	sc.autoReconnect = uiCheck(sc, modernPageRecovery, ID_CHECK_AUTO_RECONNECT, "断线时主动连接已保存的 Wi-Fi Profile", c.AutoReconnectDisconnected, 527)
	sc.wlanSvc = uiCheck(sc, modernPageRecovery, ID_CHECK_WLANSVC, "最后兜底：允许重启 Windows WlanSvc（高级）", c.EnableWlanServiceRestart, 563)

	// VPN settings: readonly API and local mixed-proxy verification.
	uiField(sc, modernPageVPN, ID_EDIT_VPN_PORT, "混合代理端口", "0 = 自动", c.VPNLocalPort, 263, 257)
	uiField(sc, modernPageVPN, ID_EDIT_MIHOMO_CONTROLLER, "Mihomo 控制端口", "0 = 关闭", c.MihomoControllerPort, 596, 257)
	sc.vpnAware = uiCheck(sc, modernPageVPN, ID_CHECK_VPN_AWARE, "开启 VPN/TUN 保护；避免将代理故障误判为 Wi-Fi 断网", c.EnableVPNAware, 373)
	uiStatic(sc, modernPageVPN, "控制接口仅执行本地只读诊断；没有授权不会修改代理模式或节点。", 263, 432, 644, 34, true)
	uiStatic(sc, modernPageVPN, "控制接口无法访问时仍可使用混合代理端口进行 HTTPS 连通性检测。", 263, 469, 644, 34, true)
	uiStatic(sc, modernPageVPN, "需要鉴权时通过进程环境变量 WIFI_WATCHDOG_MIHOMO_SECRET 提供。", 263, 506, 644, 42, true)

	// Persistent footer and page-independent actions.
	sc.statusLine = uiStatic(sc, -1, "设置已就绪。所有耗时操作均在后台完成。", 261, 649, 615, 25, true)
	uiButton(sc, -1, ID_BUTTON_DEFAULTS, "恢复默认", 261, 694, 126, 40)
	sc.cancelButton = uiButton(sc, -1, ID_BUTTON_CANCEL, "关闭", 683, 694, 99, 40)
	sc.saveButton = uiButton(sc, -1, ID_BUTTON_SAVE, "保存更改", 795, 694, 138, 40)
	uiShowPage(sc, modernPageOverview)
	uiUpdateMemory(sc)
}

func uiShowPage(sc *settingsControls, page int) {
	if sc == nil || page < modernPageOverview || page > modernPageVPN {
		return
	}
	sc.page = page
	for p, controls := range sc.pages {
		show := p == -1 || p == page
		mode := uintptr(SW_HIDE)
		if show {
			mode = SW_SHOW
		}
		for _, h := range controls {
			procShowWindow.Call(h, mode)
		}
	}
	procInvalidateRect.Call(sc.hwnd, 0, 0)
	for _, h := range sc.pages[-1] {
		procInvalidateRect.Call(h, 0, 1)
	}
	if page == modernPageOverview {
		uiUpdateMemory(sc)
	}
}

func uiUpdateMemory(sc *settingsControls) {
	if sc == nil || sc.memoryLine == 0 {
		return
	}
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	pmc := processMemoryCountersEx{Cb: uint32(unsafe.Sizeof(processMemoryCountersEx{}))}
	p, _, _ := procGetCurrentProcess.Call()
	working := "未知"
	if ok, _, _ := procGetProcessMemoryInfo.Call(p, uintptr(unsafe.Pointer(&pmc)), uintptr(pmc.Cb)); ok != 0 {
		working = fmt.Sprintf("%.1f MiB", float64(pmc.WorkingSet)/(1024*1024))
	}
	setControlText(sc.memoryLine, fmt.Sprintf("工作集 %s · Go 堆 %.1f MiB", working, float64(m.HeapAlloc)/(1024*1024)))
}

func uiDrawText(hdc uintptr, content string, rect uiRect, color uintptr, font uintptr, align uint32) {
	procSetBkMode.Call(hdc, transparentBk)
	procSetTextColor.Call(hdc, color)
	old, _, _ := procSelectObject.Call(hdc, font)
	p := wstr(content)
	procDrawTextW.Call(hdc, uintptr(unsafe.Pointer(p)), uintptr(^uint32(0)), uintptr(unsafe.Pointer(&rect)), uintptr(dtVCenter|dtSingleLine|align))
	procSelectObject.Call(hdc, old)
}
func uiScaledRect(l, t, r, b int32) uiRect {
	return uiRect{uiS(l), uiS(t), uiS(r), uiS(b)}
}
func uiFill(hdc uintptr, rect uiRect, brush uintptr) {
	procFillRect.Call(hdc, uintptr(unsafe.Pointer(&rect)), brush)
}
func uiRound(hdc uintptr, rect uiRect, brush, pen uintptr, radius int32) {
	oldBrush, _, _ := procSelectObject.Call(hdc, brush)
	oldPen, _, _ := procSelectObject.Call(hdc, pen)
	procRoundRect.Call(hdc, uintptr(int64(rect.Left)), uintptr(int64(rect.Top)), uintptr(int64(rect.Right)), uintptr(int64(rect.Bottom)), uintptr(uiS(radius)), uintptr(uiS(radius)))
	procSelectObject.Call(hdc, oldBrush)
	procSelectObject.Call(hdc, oldPen)
}
func uiPaintSettings(hwnd uintptr) {
	uiInit()
	u := &fluent.assets
	var ps uiPaintStruct
	hdc, _, _ := procBeginPaint.Call(hwnd, uintptr(unsafe.Pointer(&ps)))
	if hdc == 0 {
		return
	}
	defer procEndPaint.Call(hwnd, uintptr(unsafe.Pointer(&ps)))
	var client uiRect
	procGetClientRect.Call(hwnd, uintptr(unsafe.Pointer(&client)))
	uiFill(hdc, client, u.background)
	uiFill(hdc, uiScaledRect(0, 0, 215, 780), u.sidebar)

	uiDrawText(hdc, "WiFi", uiScaledRect(27, 27, 202, 65), uiRGB(255, 255, 255), u.title, 0)
	uiDrawText(hdc, "WATCHDOG", uiScaledRect(28, 67, 204, 88), uiRGB(123, 157, 200), u.font, 0)
	uiRound(hdc, uiScaledRect(26, 105, 184, 107), u.accent, u.navPen, 2)
	uiDrawText(hdc, "轻量 · 安全 · 自动恢复", uiScaledRect(25, 453, 207, 481), uiRGB(156, 175, 204), u.font, 0)
	uiDrawText(hdc, "Fluent Lite / Win32", uiScaledRect(25, 705, 207, 731), uiRGB(111, 137, 173), u.font, 0)

	var page int
	if value, ok := settingsMap.Load(hwnd); ok {
		page = value.(*settingsControls).page
	}
	titles := []string{"运行概览", "检测策略", "Wi-Fi 恢复", "VPN / 系统"}
	subtitles := []string{
		"网络健康与资源概览",
		"配置检查频率与故障确认阈值",
		"按级别修复网络，避免干扰已连接的无线链路",
		"Mihomo / TUN 状态及故障隔离",
	}
	uiDrawText(hdc, titles[page], uiScaledRect(238, 30, 927, 69), uiRGB(21, 34, 60), u.title, 0)
	uiDrawText(hdc, subtitles[page], uiScaledRect(239, 70, 930, 100), uiRGB(105, 123, 149), u.font, 0)
	if page == modernPageOverview {
		type stat struct {
			top   int32
			label string
			color uintptr
		}
		stats := []stat{
			{126, "系统互联网", uiRGB(24, 105, 222)},
			{262, "物理 Wi-Fi", uiRGB(22, 156, 117)},
			{398, "VPN / TUN", uiRGB(110, 79, 201)},
		}
		for _, s := range stats {
			uiRound(hdc, uiScaledRect(238, s.top, 932, s.top+119), u.white, u.borderPen, 18)
			uiRound(hdc, uiScaledRect(259, s.top+18, 265, s.top+50), u.accent, u.clearPen, 6)
			uiDrawText(hdc, s.label, uiScaledRect(280, s.top+16, 875, s.top+50), s.color, u.heading, 0)
		}
	} else {
		uiRound(hdc, uiScaledRect(238, 126, 932, 617), u.white, u.borderPen, 18)
		details := []string{"", "检测间隔与日志", "无线网卡及 DHCP 恢复", "Clash Verge Rev / Mihomo"}
		uiDrawText(hdc, details[page], uiScaledRect(262, 146, 910, 180), uiRGB(27, 44, 74), u.heading, 0)
		uiDrawText(hdc, "数值修改后点击右下角保存；不会中断当前监控。", uiScaledRect(263, 180, 910, 211), uiRGB(111, 127, 151), u.font, 0)
	}
	uiRound(hdc, uiScaledRect(238, 634, 932, 681), u.white, u.borderPen, 14)
}

func uiPaintButton(di *uiDrawItem, sc *settingsControls) bool {
	if di == nil {
		return false
	}
	u := &fluent.assets
	id := int(di.CtlID)
	page := id - ID_NAV_OVERVIEW
	isNav := page >= modernPageOverview && page <= modernPageVPN
	var brush, pen, color uintptr
	if isNav {
		brush = u.sidebar
		pen = u.sidebarPen
		color = uiRGB(191, 208, 230)
		if sc != nil && sc.page == page {
			brush = u.navActive
			pen = u.navPen
			color = uiRGB(255, 255, 255)
		}
	} else {
		brush = u.white
		pen = u.borderPen
		color = uiRGB(40, 60, 92)
		if id == ID_BUTTON_SAVE || id == ID_BUTTON_REFRESH {
			brush = u.accent
			pen = u.accentPen
			color = uiRGB(255, 255, 255)
		}
	}
	if di.ItemState&odsSelected != 0 && !isNav {
		if id==ID_BUTTON_SAVE || id==ID_BUTTON_REFRESH {
			brush=u.navActive
			pen=u.navPen
		} else {
			brush=u.accentSoft
			pen=u.borderPen
		}
	}
	// Draw into the control's client rect; it is already DPI-scaled.
	shape := uiRect{di.Rect.Left + uiS(2), di.Rect.Top + uiS(2), di.Rect.Right - uiS(2), di.Rect.Bottom - uiS(2)}
	uiRound(di.HDC, shape, brush, pen, 12)
	var textbuf [160]uint16
	procGetWindowTextW.Call(di.HwndItem, uintptr(unsafe.Pointer(&textbuf[0])), uintptr(len(textbuf)))
	title := syscall.UTF16ToString(textbuf[:])
	if di.ItemState&odsDisabled != 0 {
		color = uiRGB(147, 160, 179)
	}
	uiDrawText(di.HDC, title, di.Rect, color, u.font, dtCenter)
	if di.ItemState&odsFocus != 0 {
		focus:=uiRect{di.Rect.Left+uiS(8),di.Rect.Top+uiS(7),di.Rect.Right-uiS(8),di.Rect.Bottom-uiS(7)}
		procDrawFocusRect.Call(di.HDC,uintptr(unsafe.Pointer(&focus)))
	}
	return true
}

func uiHandleSettingsMessage(hwnd uintptr, message uint32, wParam, lParam uintptr) (bool, uintptr) {
	switch message {
	case wmEraseBkgnd:
		return true, 1
	case wmPaint:
		uiPaintSettings(hwnd)
		return true, 0
	case wmCtlColorStatic, wmCtlColorEdit, wmCtlColorButton:
		uiInit()
		if message == wmCtlColorButton {
			procSetBkMode.Call(wParam, transparentBk)
			procSetTextColor.Call(wParam, uiRGB(31, 49, 81))
			return true, fluent.assets.white
		}
		if message == wmCtlColorEdit {
			procSetTextColor.Call(wParam, uiRGB(30, 49, 80))
			procSetBkColor.Call(wParam, uiRGB(255, 255, 255))
			return true, fluent.assets.white
		}
		procSetBkMode.Call(wParam, transparentBk)
		colour := uiRGB(31, 49, 81)
		if v, ok := settingsMap.Load(hwnd); ok {
			if v.(*settingsControls).muted[lParam] {
				colour = uiRGB(105, 122, 146)
			}
		}
		procSetTextColor.Call(wParam, colour)
		return true, fluent.assets.white
	case wmDrawItem:
		if lParam == 0 {
			return true, 0
		}
		// Win32 owns the DRAWITEMSTRUCT. Copy it without an unchecked
		// uintptr-to-pointer cast, which go vet rejects.
		var di uiDrawItem
		var copied uintptr
		size := uintptr(unsafe.Sizeof(di))
		process, _, _ := procGetCurrentProcess.Call()
		ok, _, _ := procReadProcessMemory.Call(process, lParam, uintptr(unsafe.Pointer(&di)), size, uintptr(unsafe.Pointer(&copied)))
		if ok == 0 || copied != size {
			return true, 0
		}
		var sc *settingsControls
		if v, ok := settingsMap.Load(hwnd); ok {
			sc = v.(*settingsControls)
		}
		if uiPaintButton(&di, sc) {
			return true, 1
		}
	}
	return false, 0
}
