//go:build windows

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"
)

// ============================================================
// Win32 constants / types
// ============================================================

const (
	WM_DESTROY       = 0x0002
	WM_CLOSE         = 0x0010
	WM_COMMAND       = 0x0111
	WM_SETFONT       = 0x0030
	WM_LBUTTONDBLCLK = 0x0203
	WM_RBUTTONUP     = 0x0205
	WM_CONTEXTMENU   = 0x007B
	WM_APP           = 0x8000

	WM_TRAYICON              = WM_APP + 1
	WM_STATUS_UPDATE         = WM_APP + 2
	WM_SETTINGS_REFRESH_DONE = WM_APP + 10
	WM_SETTINGS_STARTUP_DONE = WM_APP + 11
	WM_SETTINGS_SAVE_DONE    = WM_APP + 12
	WM_SETTINGS_ACTIVATE     = WM_APP + 13
	WM_SETTINGS_HEARTBEAT    = WM_APP + 14
	WM_SETTINGS_SHUTDOWN     = WM_APP + 15
	WM_SETTINGS_LIVE_STATUS  = WM_APP + 16

	NIM_ADD    = 0x00000000
	NIM_MODIFY = 0x00000001
	NIM_DELETE = 0x00000002

	NIF_MESSAGE = 0x00000001
	NIF_ICON    = 0x00000002
	NIF_TIP     = 0x00000004
	NIF_INFO    = 0x00000010

	NIIF_INFO    = 0x00000001
	NIIF_WARNING = 0x00000002
	NIIF_ERROR   = 0x00000003

	MF_STRING    = 0x00000000
	MF_GRAYED    = 0x00000001
	MF_SEPARATOR = 0x00000800

	TPM_RIGHTBUTTON = 0x0002
	TPM_RETURNCMD   = 0x0100

	WS_OVERLAPPED   = 0x00000000
	WS_CAPTION      = 0x00C00000
	WS_SYSMENU      = 0x00080000
	WS_VISIBLE      = 0x10000000
	WS_CHILD        = 0x40000000
	WS_TABSTOP      = 0x00010000
	WS_BORDER       = 0x00800000
	WS_CLIPCHILDREN = 0x02000000

	ES_NUMBER        = 0x2000
	BS_PUSHBUTTON    = 0x00000000
	BS_DEFPUSHBUTTON = 0x00000001
	BS_AUTOCHECKBOX  = 0x00000003

	SW_HIDE       = 0
	SW_SHOWNORMAL = 1
	SW_SHOW       = 5

	BM_GETCHECK = 0x00F0
	BM_SETCHECK = 0x00F1
	BST_CHECKED = 1

	IDC_ARROW = 32512

	IDI_APPLICATION = 32512
	IDI_ERROR       = 32513
	IDI_WARNING     = 32515
	IDI_INFORMATION = 32516
	IDI_SHIELD      = 32518

	COLOR_WINDOW     = 5
	DEFAULT_GUI_FONT = 17

	MB_OK          = 0x00000000
	MB_ICONERROR   = 0x00000010
	MB_ICONWARNING = 0x00000030
	MB_ICONINFO    = 0x00000040

	ERROR_ALREADY_EXISTS = 183
	CREATE_NO_WINDOW     = 0x08000000

	ID_MENU_CHECK    = 1001
	ID_MENU_REPAIR   = 1002
	ID_MENU_SETTINGS = 1003
	ID_MENU_LOG      = 1004
	ID_MENU_DATA     = 1005
	ID_MENU_EXIT     = 1006
	ID_MENU_CONNECT  = 1007
	ID_MENU_REMEMBER = 1008
	ID_MENU_DIAG     = 1009

	ID_EDIT_NORMAL_MINUTES    = 2001
	ID_EDIT_FAILURE_SECONDS   = 2002
	ID_EDIT_FAILURE_COUNT     = 2003
	ID_EDIT_REPAIR_MINUTES    = 2004
	ID_EDIT_DISABLE_SECONDS   = 2005
	ID_EDIT_STARTUP_SECONDS   = 2006
	ID_CHECK_STARTUP          = 2007
	ID_BUTTON_SAVE            = 2008
	ID_BUTTON_CANCEL          = 2009
	ID_EDIT_CONNECT_RETRY     = 2010
	ID_EDIT_CONNECT_DELAY     = 2011
	ID_EDIT_DHCP_WAIT         = 2012
	ID_CHECK_AUTO_RECONNECT   = 2013
	ID_CHECK_WLANSVC          = 2014
	ID_CHECK_VPN_AWARE        = 2015
	ID_EDIT_VPN_PORT          = 2016
	ID_BUTTON_REFRESH         = 2017
	ID_BUTTON_DEFAULTS        = 2018
	ID_BUTTON_DIAG            = 2019
	ID_EDIT_TIMEOUT_SECONDS   = 2020
	ID_EDIT_LOG_RETENTION     = 2021
	ID_EDIT_MIHOMO_CONTROLLER = 2022
)

type point struct {
	X int32
	Y int32
}

type msg struct {
	HWnd     uintptr
	Message  uint32
	WParam   uintptr
	LParam   uintptr
	Time     uint32
	Pt       point
	LPrivate uint32
}

type wndClassEx struct {
	CbSize        uint32
	Style         uint32
	LpfnWndProc   uintptr
	CbClsExtra    int32
	CbWndExtra    int32
	HInstance     uintptr
	HIcon         uintptr
	HCursor       uintptr
	HbrBackground uintptr
	LpszMenuName  *uint16
	LpszClassName *uint16
	HIconSm       uintptr
}

type guid struct {
	Data1 uint32
	Data2 uint16
	Data3 uint16
	Data4 [8]byte
}

type notifyIconData struct {
	CbSize           uint32
	HWnd             uintptr
	UID              uint32
	UFlags           uint32
	UCallbackMessage uint32
	HIcon            uintptr
	SzTip            [128]uint16
	DwState          uint32
	DwStateMask      uint32
	SzInfo           [256]uint16
	UTimeoutVersion  uint32
	SzInfoTitle      [64]uint16
	DwInfoFlags      uint32
	GuidItem         guid
	HBalloonIcon     uintptr
}

// ============================================================
// Win32 APIs
// ============================================================

var (
	user32   = syscall.NewLazyDLL("user32.dll")
	shell32  = syscall.NewLazyDLL("shell32.dll")
	kernel32 = syscall.NewLazyDLL("kernel32.dll")
	gdi32    = syscall.NewLazyDLL("gdi32.dll")

	procRegisterClassExW     = user32.NewProc("RegisterClassExW")
	procCreateWindowExW      = user32.NewProc("CreateWindowExW")
	procDefWindowProcW       = user32.NewProc("DefWindowProcW")
	procDestroyWindow        = user32.NewProc("DestroyWindow")
	procGetMessageW          = user32.NewProc("GetMessageW")
	procTranslateMessage     = user32.NewProc("TranslateMessage")
	procDispatchMessageW     = user32.NewProc("DispatchMessageW")
	procIsDialogMessageW     = user32.NewProc("IsDialogMessageW")
	procPostQuitMessage      = user32.NewProc("PostQuitMessage")
	procPostMessageW         = user32.NewProc("PostMessageW")
	procLoadIconW            = user32.NewProc("LoadIconW")
	procLoadCursorW          = user32.NewProc("LoadCursorW")
	procCreatePopupMenu      = user32.NewProc("CreatePopupMenu")
	procAppendMenuW          = user32.NewProc("AppendMenuW")
	procTrackPopupMenu       = user32.NewProc("TrackPopupMenu")
	procDestroyMenu          = user32.NewProc("DestroyMenu")
	procGetCursorPos         = user32.NewProc("GetCursorPos")
	procSetForegroundWindow  = user32.NewProc("SetForegroundWindow")
	procShowWindow           = user32.NewProc("ShowWindow")
	procMessageBoxW          = user32.NewProc("MessageBoxW")
	procSendMessageW         = user32.NewProc("SendMessageW")
	procGetWindowTextLengthW = user32.NewProc("GetWindowTextLengthW")
	procGetWindowTextW       = user32.NewProc("GetWindowTextW")
	procSetWindowTextW       = user32.NewProc("SetWindowTextW")
	procEnableWindow         = user32.NewProc("EnableWindow")
	procGetSystemMetrics     = user32.NewProc("GetSystemMetrics")
	procSetProcessDPIAware   = user32.NewProc("SetProcessDPIAware")

	procShellNotifyIconW = shell32.NewProc("Shell_NotifyIconW")
	procShellExecuteW    = shell32.NewProc("ShellExecuteW")
	procIsUserAnAdmin    = shell32.NewProc("IsUserAnAdmin")

	procGetModuleHandleW = kernel32.NewProc("GetModuleHandleW")
	procCreateMutexW     = kernel32.NewProc("CreateMutexW")
	procCloseHandle      = kernel32.NewProc("CloseHandle")
	procMoveFileExW      = kernel32.NewProc("MoveFileExW")

	procGetStockObject = gdi32.NewProc("GetStockObject")
)

func wstr(s string) *uint16 {
	p, _ := syscall.UTF16PtrFromString(s)
	return p
}

func setUTF16(dst []uint16, s string) {
	for i := range dst {
		dst[i] = 0
	}
	u := syscall.StringToUTF16(s)
	if len(u) > len(dst) {
		u = u[:len(dst)]
		u[len(u)-1] = 0
	}
	copy(dst, u)
}

func loword(v uintptr) uint16 { return uint16(v & 0xffff) }

// ============================================================
// Configuration / logging
// ============================================================

type Config struct {
	NormalCheckIntervalMinutes  int  `json:"normalCheckIntervalMinutes"`
	FailureCheckIntervalSeconds int  `json:"failureCheckIntervalSeconds"`
	FailureThreshold            int  `json:"failureThreshold"`
	RepairRetryIntervalMinutes  int  `json:"repairRetryIntervalMinutes"`
	WifiDisableWaitSeconds      int  `json:"wifiDisableWaitSeconds"`
	WifiStartupWaitSeconds      int  `json:"wifiStartupWaitSeconds"`
	ConnectionTimeoutSeconds    int  `json:"connectionTimeoutSeconds"`
	ConnectRetryCount           int  `json:"connectRetryCount"`
	ConnectRetryDelaySeconds    int  `json:"connectRetryDelaySeconds"`
	DHCPRenewWaitSeconds        int  `json:"dhcpRenewWaitSeconds"`
	AutoReconnectDisconnected   bool `json:"autoReconnectDisconnected"`
	EnableWlanServiceRestart    bool `json:"enableWlanServiceRestart"`
	EnableVPNAware              bool `json:"enableVpnAware"`
	VPNLocalPort                int  `json:"vpnLocalPort"`
	MihomoControllerPort        int  `json:"mihomoControllerPort"`
	LogRetentionDays            int  `json:"logRetentionDays"`
	StartWithWindows            bool `json:"startWithWindows"`
}

func defaultConfig() Config {
	return Config{
		NormalCheckIntervalMinutes:  10,
		FailureCheckIntervalSeconds: 10,
		FailureThreshold:            5,
		RepairRetryIntervalMinutes:  10,
		WifiDisableWaitSeconds:      3,
		WifiStartupWaitSeconds:      15,
		ConnectionTimeoutSeconds:    4,
		ConnectRetryCount:           3,
		ConnectRetryDelaySeconds:    6,
		DHCPRenewWaitSeconds:        8,
		AutoReconnectDisconnected:   false,
		EnableWlanServiceRestart:    false,
		EnableVPNAware:              true,
		VPNLocalPort:                0,    // auto-discover when the controller is available
		MihomoControllerPort:        9097, // TCP API may be disabled in Clash Verge Rev
		LogRetentionDays:            30,
		StartWithWindows:            false,
	}
}

func normalizeConfig(c Config) Config {
	c.NormalCheckIntervalMinutes = clamp(c.NormalCheckIntervalMinutes, 1, 1440)
	c.FailureCheckIntervalSeconds = clamp(c.FailureCheckIntervalSeconds, 1, 3600)
	c.FailureThreshold = clamp(c.FailureThreshold, 1, 100)
	c.RepairRetryIntervalMinutes = clamp(c.RepairRetryIntervalMinutes, 1, 1440)
	c.WifiDisableWaitSeconds = clamp(c.WifiDisableWaitSeconds, 1, 120)
	c.WifiStartupWaitSeconds = clamp(c.WifiStartupWaitSeconds, 1, 600)
	c.ConnectionTimeoutSeconds = clamp(c.ConnectionTimeoutSeconds, 1, 30)
	c.ConnectRetryCount = clamp(c.ConnectRetryCount, 1, 10)
	c.ConnectRetryDelaySeconds = clamp(c.ConnectRetryDelaySeconds, 1, 60)
	c.DHCPRenewWaitSeconds = clamp(c.DHCPRenewWaitSeconds, 1, 120)
	c.VPNLocalPort = clamp(c.VPNLocalPort, 0, 65535)
	c.MihomoControllerPort = clamp(c.MihomoControllerPort, 0, 65535)
	c.LogRetentionDays = clamp(c.LogRetentionDays, 1, 3650)
	return c
}

func clamp(v, minV, maxV int) int {
	if v < minV {
		return minV
	}
	if v > maxV {
		return maxV
	}
	return v
}

const (
	maxDailyLogBytes = 16 * 1024 * 1024
	keptLogTailBytes = 8 * 1024 * 1024
)

type Logger struct {
	dir            string
	mu             sync.Mutex
	retentionDays  int
	lastCleanupDay string
}

func newLogger(dir string, retentionDays int) *Logger {
	return &Logger{dir: dir, retentionDays: clamp(retentionDays, 1, 3650)}
}

func (l *Logger) setRetentionDays(days int) {
	l.mu.Lock()
	l.retentionDays = clamp(days, 1, 3650)
	// Force a new cleanup pass so reducing the retention applies immediately.
	l.lastCleanupDay = ""
	l.mu.Unlock()
}

func (l *Logger) currentPathLocked(now time.Time) string {
	return filepath.Join(l.dir, "watchdog-"+now.Format("2006-01-02")+".log")
}

func (l *Logger) currentPath() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	_ = os.MkdirAll(l.dir, 0755)
	return l.currentPathLocked(time.Now())
}

func (l *Logger) cleanupLocked(now time.Time) {
	dayKey := now.Format("2006-01-02")
	if l.lastCleanupDay == dayKey {
		return
	}
	retention := l.retentionDays
	if retention <= 0 {
		retention = 30
	}
	entries, err := os.ReadDir(l.dir)
	if err != nil {
		return
	}
	l.lastCleanupDay = dayKey
	today, _ := time.ParseInLocation("2006-01-02", dayKey, now.Location())
	cutoff := today.AddDate(0, 0, -(retention - 1))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasPrefix(name, "watchdog-") || !strings.HasSuffix(name, ".log") {
			continue
		}
		dateText := strings.TrimSuffix(strings.TrimPrefix(name, "watchdog-"), ".log")
		logDay, err := time.ParseInLocation("2006-01-02", dateText, now.Location())
		if err != nil {
			continue
		}
		if logDay.Before(cutoff) {
			_ = os.Remove(filepath.Join(l.dir, name))
		}
	}
}

// compactLocked preserves the recent tail if a single busy day exceeds the
// configured hard limit. We keep one file per calendar day and use an atomic
// replace rather than leaving unbounded rotated copies on disk.
func (l *Logger) compactLocked(path string) {
	st, err := os.Stat(path)
	if err != nil || st.Size() < maxDailyLogBytes {
		return
	}
	data, err := os.ReadFile(path)
	if err != nil || len(data) <= keptLogTailBytes {
		return
	}
	tail := data[len(data)-keptLogTailBytes:]
	if idx := bytes.IndexByte(tail, '\n'); idx >= 0 {
		tail = tail[idx+1:]
	}
	marker := []byte("--- early log entries compacted to bound daily disk usage ---\r\n")
	_ = atomicWriteFile(path, append(marker, tail...), 0644)
}

func (l *Logger) write(level, text string) {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()
	if err := os.MkdirAll(l.dir, 0755); err != nil {
		return
	}
	l.cleanupLocked(now)
	path := l.currentPathLocked(now)
	l.compactLocked(path)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "[%s] [%s] %s\r\n", now.Format("2006-01-02 15:04:05"), level, text)
}

func (l *Logger) info(s string) { l.write("INFO", s) }
func (l *Logger) warn(s string) { l.write("WARN", s) }
func (l *Logger) err(s string)  { l.write("ERROR", s) }

// ============================================================
// Application state
// ============================================================

type MonitorState int

const (
	StateStarting MonitorState = iota
	StateOnline
	StateWifiDisconnected
	StateChecking
	StateConfirming
	StateRepairing
	StateWaitingRetry
	StateVPNProtected
	StateOffline
	StateError
)

type App struct {
	hwnd uintptr
	nid  notifyIconData

	configPath string
	logDir     string
	dataDir    string

	cfgMu sync.RWMutex
	cfg   Config

	logger *Logger

	statusMu sync.RWMutex
	state    MonitorState
	status   string
	oldState MonitorState

	stopCh   chan struct{}
	wakeCh   chan struct{}
	onceStop sync.Once

	repairMu         sync.Mutex
	assessmentMu     sync.Mutex
	stateMu          sync.Mutex
	workers          sync.WaitGroup
	workerMu         sync.Mutex
	lastAutoRepairAt atomic.Int64

	settingsMu      sync.Mutex
	settingsHwnd    uintptr
	settingsOpening bool

	settingsHeartbeat atomic.Int64
	settingsLastDump  atomic.Int64

	shownStartupBalloon bool
	mutexHandle         uintptr
	statusPostPending   atomic.Bool
}

var app *App

func (a *App) getConfig() Config {
	a.cfgMu.RLock()
	defer a.cfgMu.RUnlock()
	return a.cfg
}

func (a *App) setConfig(c Config) {
	normalized := normalizeConfig(c)
	a.cfgMu.Lock()
	a.cfg = normalized
	a.cfgMu.Unlock()
	if a.logger != nil {
		a.goSafe("log-retention-update", func() {
			a.logger.setRetentionDays(normalized.LogRetentionDays)
		})
	}
}

func (a *App) saveConfig(c Config) error {
	c = normalizeConfig(c)
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return atomicWriteFile(a.configPath, b, 0644)
}

func loadConfig(path string) Config {
	c := defaultConfig()
	b, err := os.ReadFile(path)
	if err != nil {
		return c
	}
	if json.Unmarshal(b, &c) != nil {
		backup := path + ".invalid-" + time.Now().Format("20060102-150405") + ".json"
		_ = os.Rename(path, backup)
		return defaultConfig()
	}
	return normalizeConfig(c)
}

func (a *App) setStatus(state MonitorState, text string, logIt bool) {
	a.statusMu.Lock()
	a.oldState = a.state
	a.state = state
	a.status = text
	a.statusMu.Unlock()

	if logIt {
		switch state {
		case StateError, StateOffline:
			a.logger.err(text)
		case StateConfirming, StateRepairing, StateWaitingRetry, StateVPNProtected:
			a.logger.warn(text)
		default:
			a.logger.info(text)
		}
	}
	if a.statusPostPending.CompareAndSwap(false, true) {
		procPostMessageW.Call(a.hwnd, WM_STATUS_UPDATE, 0, 0)
	}
	// UI-thread-safe dashboard update; the tray never calls child HWND APIs.
	a.settingsMu.Lock()
	settings := a.settingsHwnd
	a.settingsMu.Unlock()
	if settings != 0 {
		procPostMessageW.Call(settings, WM_SETTINGS_LIVE_STATUS, 0, 0)
	}
}

func (a *App) currentStatus() (MonitorState, string) {
	a.statusMu.RLock()
	defer a.statusMu.RUnlock()
	return a.state, a.status
}

func (a *App) wake() {
	select {
	case a.wakeCh <- struct{}{}:
	default:
	}
}

func (a *App) stop() {
	a.workerMu.Lock()
	a.onceStop.Do(func() { close(a.stopCh) })
	a.workerMu.Unlock()
}

func (a *App) reportRecoveredPanic(scope string, recovered any) {
	stack := debug.Stack()
	msg := fmt.Sprintf("%s panic: %v", scope, recovered)
	if a.logger != nil {
		a.logger.err(msg + "；完整堆栈将写入独立的 crash 报告。")
	}
	dir := filepath.Join(a.dataDir, "diagnostics")
	_ = os.MkdirAll(dir, 0755)
	a.cleanupOldDiagnostics(time.Now())
	path := filepath.Join(dir, "crash-"+time.Now().Format("20060102-150405")+".txt")
	body := fmt.Sprintf("WiFi Watchdog crash report\r\nVersion: %s\r\nScope: %s\r\nTime: %s\r\nPanic: %v\r\n\r\n%s",
		appVersion, scope, time.Now().Format(time.RFC3339), recovered, stack)
	_ = atomicWriteFile(path, []byte(body), 0644)
	a.setStatus(StateError, "后台任务异常已被隔离，程序继续运行；崩溃报告："+path, true)
}

func (a *App) goSafe(scope string, fn func()) {
	// Synchronize Add with stop/Wait: WaitGroup.Add at zero concurrently with
	// Wait can panic, especially when the settings window closes during exit.
	a.workerMu.Lock()
	select {
	case <-a.stopCh:
		a.workerMu.Unlock()
		return
	default:
	}
	a.workers.Add(1)
	a.workerMu.Unlock()
	go func() {
		defer a.workers.Done()
		defer func() {
			if recovered := recover(); recovered != nil {
				a.reportRecoveredPanic(scope, recovered)
			}
		}()
		fn()
	}()
}

func (a *App) delayOrWake(d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-a.stopCh:
		return false
	case <-a.wakeCh:
		return true
	case <-t.C:
		return true
	}
}

// ============================================================
// Network / Wi-Fi operations
// ============================================================

type wifiInfo struct {
	Connected            bool
	InterfaceName        string // Windows friendly alias, e.g. Wi-Fi
	InterfaceGUID        string
	InterfaceDescription string
	ProfileName          string // WLAN profile name; may differ from SSID
	SSID                 string
	SignalQuality        int
}

func hiddenCommand(name string, args ...string) *exec.Cmd {
	cmd := exec.Command(name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: CREATE_NO_WINDOW}
	return cmd
}

func runUTF8Cmd(name string, args ...string) ([]byte, error) {
	if strings.EqualFold(name, "netsh.exe") || strings.EqualFold(name, "netsh") {
		// netsh 的本地化输出通常使用 OEM 代码页。通过 cmd 切到 UTF-8，
		// 同时强制超时，避免驱动/服务异常时把监控线程永久卡住。
		quoted := []string{"chcp 65001>nul", "&", "netsh"}
		quoted = append(quoted, args...)
		commandLine := strings.Join(quoted, " ")
		return runHiddenTimeout(8*time.Second, "cmd.exe", "/d", "/s", "/c", commandLine)
	}
	return runHiddenTimeout(8*time.Second, name, args...)
}

func detectWifiNetsh() wifiInfo {
	out, err := runUTF8Cmd("netsh.exe", "wlan", "show", "interfaces")
	if err != nil {
		return wifiInfo{}
	}
	text := strings.ReplaceAll(string(out), "\r\n", "\n")
	lines := strings.Split(text, "\n")

	// 分成空行隔开的块。真正的 WLAN 接口块通常包含很多“字段: 值”。
	type block struct {
		lines       []string
		colons      int
		hasSSID     bool
		ssid        string
		profile     string
		guid        string
		description string
		signal      int
	}
	var blocks []block
	var cur block
	flush := func() {
		if len(cur.lines) > 0 {
			blocks = append(blocks, cur)
			cur = block{}
		}
	}
	for _, raw := range lines {
		t := strings.TrimSpace(raw)
		if t == "" {
			flush()
			continue
		}
		cur.lines = append(cur.lines, t)
		p := strings.SplitN(t, ":", 2)
		if len(p) == 2 {
			cur.colons++
			key := strings.TrimSpace(p[0])
			val := strings.TrimSpace(p[1])
			lowerKey := strings.ToLower(key)
			if key == "SSID" && val != "" {
				cur.hasSSID = true
				cur.ssid = val
			}
			if (strings.Contains(lowerKey, "profile") || strings.Contains(key, "配置文件")) && val != "" {
				cur.profile = val
			}
			if strings.EqualFold(key, "GUID") && val != "" {
				cur.guid = val
			}
			if (strings.Contains(lowerKey, "description") || strings.Contains(key, "描述") || strings.Contains(key, "说明")) && val != "" {
				cur.description = val
			}
			if (strings.Contains(lowerKey, "signal") || strings.Contains(key, "信号")) && strings.HasSuffix(val, "%") {
				if n, e := strconv.Atoi(strings.TrimSpace(strings.TrimSuffix(val, "%"))); e == nil {
					cur.signal = n
				}
			}
		}
	}
	flush()

	bestIndex := -1
	for i, b := range blocks {
		if b.hasSSID && b.colons >= 4 {
			bestIndex = i
			break
		}
	}
	if bestIndex < 0 {
		for i, b := range blocks {
			if b.colons >= 4 {
				bestIndex = i
				break
			}
		}
	}
	if bestIndex < 0 {
		return wifiInfo{}
	}

	b := blocks[bestIndex]
	iface := ""
	for _, line := range b.lines {
		p := strings.SplitN(line, ":", 2)
		if len(p) != 2 {
			continue
		}
		val := strings.TrimSpace(p[1])
		if val != "" {
			// WLAN 接口块的第一个“字段: 值”就是接口名称（Name/名称）。
			iface = val
			break
		}
	}

	return wifiInfo{
		Connected:            b.hasSSID,
		InterfaceName:        iface,
		InterfaceGUID:        b.guid,
		InterfaceDescription: b.description,
		ProfileName:          b.profile,
		SSID:                 b.ssid,
		SignalQuality:        b.signal,
	}
}

// detectWifi prefers the locale-independent Native Wi-Fi API and only uses
// netsh as a compatibility fallback / source of the friendly interface alias.
func detectWifi() wifiInfo {
	native, err := detectWifiNative()
	netshInfo := detectWifiNetsh()
	if err == nil {
		if native.InterfaceName == "" {
			native.InterfaceName = netshInfo.InterfaceName
		}
		if native.InterfaceGUID == "" {
			native.InterfaceGUID = netshInfo.InterfaceGUID
		}
		if native.InterfaceDescription == "" {
			native.InterfaceDescription = netshInfo.InterfaceDescription
		}
		// On systems where Native Wi-Fi privacy restrictions hide SSID/profile,
		// keep whatever useful information netsh still exposes.
		if native.ProfileName == "" {
			native.ProfileName = netshInfo.ProfileName
		}
		if native.SSID == "" {
			native.SSID = netshInfo.SSID
		}
		if native.SignalQuality == 0 {
			native.SignalQuality = netshInfo.SignalQuality
		}
		if !native.Connected && netshInfo.Connected {
			native.Connected = true
		}
		return native
	}
	return netshInfo
}

func wifiIPv4ByAlias(alias string) (net.IP, bool) {
	if strings.TrimSpace(alias) == "" {
		return nil, false
	}
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, false
	}
	for _, iface := range ifaces {
		if !strings.EqualFold(strings.TrimSpace(iface.Name), strings.TrimSpace(alias)) {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			return nil, true
		}
		for _, addr := range addrs {
			var ip net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			if ip4 := ip.To4(); ip4 != nil && !ip4.IsLoopback() {
				return ip4, true
			}
		}
		return nil, true
	}
	return nil, false
}

func (a *App) monitorLoop() {
	a.logger.info("WiFi Watchdog v" + appVersion + " 监控服务启动。")
	a.setStatus(StateStarting, "监控服务已启动。", true)

	for {
		select {
		case <-a.stopCh:
			return
		default:
		}

		c := a.getConfig()
		assessment := a.assessNetwork()
		info := assessment.WiFi

		if assessment.Online {
			a.recordInternetOK()
			if info.Connected {
				a.rememberConnectedInfo(info)
			}
			a.setStatus(StateOnline, fmt.Sprintf("系统网络正常；%d 分钟后再次检测。", c.NormalCheckIntervalMinutes), true)
			if !a.delayOrWake(time.Duration(c.NormalCheckIntervalMinutes) * time.Minute) {
				return
			}
			continue
		}

		// If Wi-Fi association itself is gone, optional auto-reconnect is allowed
		// even in VPN mode because this is strong evidence of a Wi-Fi fault.
		if !info.Connected && c.AutoReconnectDisconnected {
			t := a.resolveTarget(info)
			if t.ProfileName != "" || t.SSID != "" {
				a.setStatus(StateRepairing, "Wi-Fi 已断开，尝试主动连接上一次成功的 Profile...", true)
				a.repairMu.Lock()
				ok := a.softReconnect(t)
				a.repairMu.Unlock()
				if ok && a.hasInternet() {
					connected := detectWifi()
					a.rememberConnectedInfo(connected)
					a.setStatus(StateOnline, "Wi-Fi 已自动重新连接，系统互联网恢复。", true)
					if !a.delayOrWake(time.Duration(c.NormalCheckIntervalMinutes) * time.Minute) {
						return
					}
					continue
				}
			}
		}

		if !info.Connected && !c.AutoReconnectDisconnected {
			a.setStatus(StateWifiDisconnected, fmt.Sprintf("Wi-Fi 当前未连接；自动重连未启用，%d 分钟后再检查。", c.NormalCheckIntervalMinutes), false)
			if !a.delayOrWake(time.Duration(c.NormalCheckIntervalMinutes) * time.Minute) {
				return
			}
			continue
		}

		// Capture the recovery target BEFORE any adapter operation.
		target := a.resolveTarget(info)
		failures := 1
		a.setStatus(StateConfirming, fmt.Sprintf("系统互联网检测失败 %d/%d，开始快速复检。", failures, c.FailureThreshold), true)
		latest := assessment
		recovered := false

		for failures < c.FailureThreshold {
			c = a.getConfig()
			if !a.delayOrWake(time.Duration(c.FailureCheckIntervalSeconds) * time.Second) {
				return
			}
			latest = a.assessNetwork()
			if latest.Online {
				if latest.WiFi.Connected {
					a.rememberConnectedInfo(latest.WiFi)
				}
				recovered = true
				a.setStatus(StateOnline, "系统网络已自行恢复，取消自动恢复。", true)
				break
			}
			if latest.WiFi.Connected {
				target = mergeTarget(targetFromInfo(latest.WiFi), target)
			}
			failures++
			a.setStatus(StateConfirming, fmt.Sprintf("系统互联网检测失败 %d/%d。", failures, c.FailureThreshold), true)
		}

		if recovered {
			c = a.getConfig()
			if !a.delayOrWake(time.Duration(c.NormalCheckIntervalMinutes) * time.Minute) {
				return
			}
			continue
		}

		// VPN/TUN protection: if the physical Wi-Fi has no strong fault evidence,
		// never reset it merely because the VPN/system egress is broken.
		if latest.UnderlayProtected {
			a.setStatus(StateWaitingRetry,
				fmt.Sprintf("物理 Wi-Fi 直连正常，更像代理/DNS/系统路由异常；%d 分钟后重检。", c.RepairRetryIntervalMinutes), true)
			if !a.delayOrWake(time.Duration(c.RepairRetryIntervalMinutes) * time.Minute) {
				return
			}
			continue
		}
		if latest.VPNProtected {
			a.setStatus(StateVPNProtected,
				fmt.Sprintf("检测到 VPN/TUN；Wi-Fi 底层未见强故障，跳过 Wi-Fi 恢复。%d 分钟后重检。", c.RepairRetryIntervalMinutes), true)
			a.logger.warn("VPN/TUN 保护原因：" + latest.Reason + "；Underlay=" + latest.Underlay.Reason)
			if !a.delayOrWake(time.Duration(c.RepairRetryIntervalMinutes) * time.Minute) {
				return
			}
			continue
		}
		if latest.CaptiveProtected {
			a.setStatus(StateWaitingRetry,
				fmt.Sprintf("疑似校园网认证门户/受限网络，避免重启 Wi-Fi；%d 分钟后重检。", c.RepairRetryIntervalMinutes), true)
			if !a.delayOrWake(time.Duration(c.RepairRetryIntervalMinutes) * time.Minute) {
				return
			}
			continue
		}
		if !latest.ShouldRepairWiFi {
			a.setStatus(StateWaitingRetry, fmt.Sprintf("网络异常但证据不足以安全重启 Wi-Fi；%d 分钟后重检。", c.RepairRetryIntervalMinutes), true)
			if !a.delayOrWake(time.Duration(c.RepairRetryIntervalMinutes) * time.Minute) {
				return
			}
			continue
		}

		a.setStatus(StateOffline, fmt.Sprintf("连续失败 %d 次，且确认允许修复 Wi-Fi：%s", c.FailureThreshold, latest.Reason), true)

		for {
			select {
			case <-a.stopCh:
				return
			default:
			}

			c = a.getConfig()
			cooldown := time.Duration(c.RepairRetryIntervalMinutes) * time.Minute
			if remaining := a.remainingAutoRepairCooldown(cooldown); remaining > 0 {
				mins := int((remaining + time.Minute - 1) / time.Minute)
				a.setStatus(StateWaitingRetry, fmt.Sprintf("上一次自动恢复距今过短；还需等待约 %d 分钟，避免重启程序绕过恢复冷却。", mins), true)
				if !a.delayOrWake(remaining) {
					return
				}
				latest = a.assessNetwork()
				if latest.Online {
					a.recordInternetOK()
					a.setStatus(StateOnline, "恢复冷却期间网络已自行恢复。", true)
					break
				}
				if latest.UnderlayProtected || latest.VPNProtected || latest.CaptiveProtected || !latest.ShouldRepairWiFi {
					a.setStatus(StateVPNProtected, "冷却结束后重新评估：当前不适合自动操作 Wi-Fi。", true)
					break
				}
			}

			a.recordAutoRepairAttempt()
			a.setStatus(StateRepairing, "正在执行多级 Wi-Fi / DHCP / DNS 恢复...", true)
			if a.robustRepair(target) {
				connected := detectWifi()
				a.rememberConnectedInfo(connected)
				a.setStatus(StateOnline, "网络恢复成功，退出恢复模式。", true)
				break
			}

			c = a.getConfig()
			a.setStatus(StateWaitingRetry, fmt.Sprintf("本轮恢复未成功；%d 分钟后再判断，期间不会重复重启 Wi-Fi。", c.RepairRetryIntervalMinutes), true)
			if !a.delayOrWake(time.Duration(c.RepairRetryIntervalMinutes) * time.Minute) {
				return
			}

			latest = a.assessNetwork()
			if latest.Online {
				if latest.WiFi.Connected {
					a.rememberConnectedInfo(latest.WiFi)
				}
				a.setStatus(StateOnline, "等待期间系统网络已自行恢复，无需再次操作 Wi-Fi。", true)
				break
			}
			if latest.UnderlayProtected || latest.VPNProtected || latest.CaptiveProtected || !latest.ShouldRepairWiFi {
				a.setStatus(StateVPNProtected, "当前更像 VPN/TUN/认证层问题，停止连续操作 Wi-Fi，返回保护监控。", true)
				break
			}
			a.setStatus(StateOffline, "系统网络仍未恢复，且 Wi-Fi 仍有明确故障证据，准备下一轮多级恢复。", true)
		}

		c = a.getConfig()
		if !a.delayOrWake(time.Duration(c.NormalCheckIntervalMinutes) * time.Minute) {
			return
		}
	}
}

func (a *App) manualCheck() {
	a.setStatus(StateChecking, "正在完整检测四个 HTTP 目标 / VPN / Wi-Fi 底层...", false)
	n := a.assessNetworkMode(true)
	if n.Online {
		a.setStatus(StateOnline, "立即检测：系统互联网正常。", true)
		return
	}
	if n.VPNProtected {
		a.setStatus(StateVPNProtected, "立即检测：系统互联网异常，但检测到 VPN/TUN 且 Wi-Fi 底层无强故障；不会自动重启 Wi-Fi。", true)
		return
	}
	if n.CaptiveProtected {
		a.setStatus(StateWaitingRetry, "立即检测：疑似认证门户/受限网络；不会自动重启 Wi-Fi。", true)
		return
	}
	if n.ShouldRepairWiFi {
		a.setStatus(StateOffline, "立即检测：系统互联网异常，且允许进行 Wi-Fi 恢复。原因："+n.Reason, true)
		return
	}
	a.setStatus(StateOffline, "立即检测：网络异常，但暂不建议自动操作 Wi-Fi。", true)
}
func (a *App) manualRepair() {
	a.setStatus(StateRepairing, "正在执行手动多级恢复...", true)
	target := a.resolveTarget(detectWifi())
	if !a.robustRepair(target) {
		a.setStatus(StateOffline, "手动多级恢复未成功，请查看日志或生成诊断报告。", true)
		a.wake()
		return
	}
	connected := detectWifi()
	a.rememberConnectedInfo(connected)
	a.setStatus(StateOnline, "手动多级恢复成功，网络已恢复。", true)
	a.wake()
}

// ============================================================
// Startup scheduled task
// ============================================================

const startupTaskName = "WiFiWatchdog"

func startupTaskEnabled() (bool, error) {
	out, err := runHiddenTimeout(5*time.Second, "schtasks.exe", "/Query", "/TN", startupTaskName)
	if err == nil {
		return true, nil
	}
	text := strings.ToLower(string(out))
	if strings.Contains(text, "cannot find") || strings.Contains(text, "cannot find the file") ||
		strings.Contains(text, "找不到") || strings.Contains(text, "不存在") {
		return false, nil
	}
	if strings.Contains(err.Error(), "timeout") {
		return false, err
	}
	// schtasks /Query returns non-zero when the task does not exist. On some
	// localized Windows builds the message is not stable, so treat ordinary
	// non-timeout query failure as "not enabled" and surface it in the UI note.
	return false, nil
}

func setStartupTask(enabled bool) error {
	if enabled {
		exePath, err := os.Executable()
		if err != nil {
			return err
		}
		taskCmd := `"` + exePath + `"`
		out, err := runHiddenTimeout(12*time.Second, "schtasks.exe",
			"/Create", "/TN", startupTaskName,
			"/SC", "ONLOGON",
			"/TR", taskCmd,
			"/RL", "HIGHEST",
			"/F")
		if err != nil {
			return fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out)))
		}
		return nil
	}

	out, err := runHiddenTimeout(12*time.Second, "schtasks.exe", "/Delete", "/TN", startupTaskName, "/F")
	if err != nil {
		text := strings.ToLower(string(out))
		if strings.Contains(text, "cannot find") || strings.Contains(text, "找不到") || strings.Contains(text, "不存在") {
			return nil
		}
		return fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// ============================================================
// Tray / UI
// ============================================================

func iconResource(id uintptr) uintptr {
	h, _, _ := procLoadIconW.Call(0, id)
	return h
}

func stateIcon(s MonitorState) uintptr {
	switch s {
	case StateOnline:
		return iconResource(IDI_INFORMATION)
	case StateConfirming, StateRepairing, StateWaitingRetry, StateVPNProtected:
		return iconResource(IDI_WARNING)
	case StateOffline, StateError:
		return iconResource(IDI_ERROR)
	case StateWifiDisconnected:
		return iconResource(IDI_APPLICATION)
	default:
		return iconResource(IDI_SHIELD)
	}
}

func (a *App) addTrayIcon() bool {
	a.nid = notifyIconData{}
	a.nid.CbSize = uint32(unsafe.Sizeof(a.nid))
	a.nid.HWnd = a.hwnd
	a.nid.UID = 1
	a.nid.UFlags = NIF_MESSAGE | NIF_ICON | NIF_TIP
	a.nid.UCallbackMessage = WM_TRAYICON
	a.nid.HIcon = stateIcon(StateStarting)
	setUTF16(a.nid.SzTip[:], "WiFi Watchdog - 正在启动")

	r, _, _ := procShellNotifyIconW.Call(NIM_ADD, uintptr(unsafe.Pointer(&a.nid)))
	return r != 0
}

func (a *App) updateTray() {
	// Clear first: a concurrent status change can then queue a fresh update.
	a.statusPostPending.Store(false)
	state, status := a.currentStatus()
	a.nid.UFlags = NIF_ICON | NIF_TIP
	a.nid.HIcon = stateIcon(state)
	tip := "WiFi Watchdog - " + status
	ru := []rune(tip)
	if len(ru) > 120 {
		tip = string(ru[:120])
	}
	setUTF16(a.nid.SzTip[:], tip)
	procShellNotifyIconW.Call(NIM_MODIFY, uintptr(unsafe.Pointer(&a.nid)))

	if !a.shownStartupBalloon && state == StateOnline {
		a.shownStartupBalloon = true
		c := a.getConfig()
		a.showBalloon("WiFi Watchdog v"+appVersion+" 已运行", fmt.Sprintf("网络监控已启动。正常每 %d 分钟检测一次。", c.NormalCheckIntervalMinutes), NIIF_INFO)
	}

	a.statusMu.RLock()
	oldState := a.oldState
	a.statusMu.RUnlock()
	if state == StateRepairing && oldState != StateRepairing {
		a.showBalloon("正在恢复 Wi-Fi", status, NIIF_WARNING)
	} else if state == StateOnline && (strings.Contains(status, "恢复") || strings.Contains(status, "自行恢复")) {
		a.showBalloon("网络已恢复", status, NIIF_INFO)
	}
}

func (a *App) showBalloon(title, text string, flags uint32) {
	n := a.nid
	n.UFlags = NIF_INFO
	setUTF16(n.SzInfoTitle[:], title)
	setUTF16(n.SzInfo[:], text)
	n.DwInfoFlags = flags
	procShellNotifyIconW.Call(NIM_MODIFY, uintptr(unsafe.Pointer(&n)))
}

func (a *App) removeTrayIcon() {
	procShellNotifyIconW.Call(NIM_DELETE, uintptr(unsafe.Pointer(&a.nid)))
}

func (a *App) showTrayMenu() {
	menu, _, _ := procCreatePopupMenu.Call()
	if menu == 0 {
		return
	}
	defer procDestroyMenu.Call(menu)

	_, status := a.currentStatus()
	label := "状态：" + status
	if len([]rune(label)) > 60 {
		label = string([]rune(label)[:60]) + "..."
	}
	procAppendMenuW.Call(menu, MF_STRING|MF_GRAYED, 0, uintptr(unsafe.Pointer(wstr(label))))
	procAppendMenuW.Call(menu, MF_SEPARATOR, 0, 0)
	procAppendMenuW.Call(menu, MF_STRING, ID_MENU_CHECK, uintptr(unsafe.Pointer(wstr("立即检测网络"))))
	procAppendMenuW.Call(menu, MF_STRING, ID_MENU_CONNECT, uintptr(unsafe.Pointer(wstr("主动连接已记住的 Wi-Fi"))))
	procAppendMenuW.Call(menu, MF_STRING, ID_MENU_REPAIR, uintptr(unsafe.Pointer(wstr("立即执行完整多级恢复"))))
	procAppendMenuW.Call(menu, MF_STRING, ID_MENU_REMEMBER, uintptr(unsafe.Pointer(wstr("记住当前 Wi-Fi 为恢复目标"))))
	procAppendMenuW.Call(menu, MF_SEPARATOR, 0, 0)
	procAppendMenuW.Call(menu, MF_STRING, ID_MENU_SETTINGS, uintptr(unsafe.Pointer(wstr("设置..."))))
	procAppendMenuW.Call(menu, MF_STRING, ID_MENU_LOG, uintptr(unsafe.Pointer(wstr("打开日志"))))
	procAppendMenuW.Call(menu, MF_STRING, ID_MENU_DIAG, uintptr(unsafe.Pointer(wstr("生成诊断报告"))))
	procAppendMenuW.Call(menu, MF_STRING, ID_MENU_DATA, uintptr(unsafe.Pointer(wstr("打开数据目录"))))
	procAppendMenuW.Call(menu, MF_SEPARATOR, 0, 0)
	procAppendMenuW.Call(menu, MF_STRING, ID_MENU_EXIT, uintptr(unsafe.Pointer(wstr("退出"))))

	var p point
	procGetCursorPos.Call(uintptr(unsafe.Pointer(&p)))
	procSetForegroundWindow.Call(a.hwnd)
	cmd, _, _ := procTrackPopupMenu.Call(menu, TPM_RIGHTBUTTON|TPM_RETURNCMD, uintptr(p.X), uintptr(p.Y), 0, a.hwnd, 0)
	if cmd != 0 {
		a.handleMenuCommand(int(cmd))
	}
}

func (a *App) handleMenuCommand(id int) {
	switch id {
	case ID_MENU_CHECK:
		a.goSafe("manual-check", a.manualCheck)
	case ID_MENU_CONNECT:
		a.goSafe("manual-connect", a.manualConnectTarget)
	case ID_MENU_REPAIR:
		a.goSafe("manual-repair", a.manualRepair)
	case ID_MENU_REMEMBER:
		a.goSafe("remember-target", a.manualRememberTarget)
	case ID_MENU_SETTINGS:
		a.showSettings()
	case ID_MENU_LOG:
		path := a.logger.currentPath()
		if f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644); err == nil {
			_ = f.Close()
		}
		_ = exec.Command("notepad.exe", path).Start()
	case ID_MENU_DIAG:
		a.goSafe("diagnostics", func() {
			path := a.generateDiagnosticReport()
			_ = exec.Command("notepad.exe", path).Start()
		})
	case ID_MENU_DATA:
		_ = exec.Command("explorer.exe", a.dataDir).Start()
	case ID_MENU_EXIT:
		procDestroyWindow.Call(a.hwnd)
	}
}

func messageBox(owner uintptr, title, text string, flags uintptr) {
	procMessageBoxW.Call(owner,
		uintptr(unsafe.Pointer(wstr(text))),
		uintptr(unsafe.Pointer(wstr(title))),
		flags)
}

// ============================================================
// Settings window
// ============================================================

type settingsControls struct {
	hwnd          uintptr
	edits         map[int]uintptr
	startup       uintptr
	autoReconnect uintptr
	wlanSvc       uintptr
	vpnAware      uintptr
	saveButton    uintptr
	cancelButton  uintptr
	refreshButton uintptr
	statusLine    uintptr
	summarySystem uintptr
	summaryWiFi   uintptr
	summaryVPN    uintptr
	memoryLine    uintptr
	pages         map[int][]uintptr
	muted         map[uintptr]bool
	page          int

	mu              sync.Mutex
	startupKnown    bool
	startupEnabled  bool
	startupErr      string
	startupTouched  bool
	assessment      NetworkAssessment
	assessmentReady bool
	refreshing      bool
	saveInProgress  bool
	saveErr         string
	pendingConfig   Config
}

var settingsMap sync.Map // hwnd -> *settingsControls

func createChild(parent uintptr, className, text string, style uint32, x, y, w, h int32, id int) uintptr {
	hwnd, _, _ := procCreateWindowExW.Call(
		0,
		uintptr(unsafe.Pointer(wstr(className))),
		uintptr(unsafe.Pointer(wstr(text))),
		uintptr(WS_CHILD|WS_VISIBLE|style),
		uintptr(uiS(x)), uintptr(uiS(y)), uintptr(uiS(w)), uintptr(uiS(h)),
		parent,
		uintptr(id),
		0,
		0,
	)
	if hwnd != 0 {
		font := uiDefaultFont()
		if font != 0 {
			procSendMessageW.Call(hwnd, WM_SETFONT, font, 0)
		}
	}
	return hwnd
}

func setControlText(hwnd uintptr, text string) {
	if hwnd != 0 {
		procSetWindowTextW.Call(hwnd, uintptr(unsafe.Pointer(wstr(text))))
	}
}

func setCheck(hwnd uintptr, checked bool) {
	v := uintptr(0)
	if checked {
		v = BST_CHECKED
	}
	procSendMessageW.Call(hwnd, BM_SETCHECK, v, 0)
}

func isChecked(hwnd uintptr) bool {
	checked, _, _ := procSendMessageW.Call(hwnd, BM_GETCHECK, 0, 0)
	return checked == BST_CHECKED
}

func (a *App) showSettings() {
	a.settingsMu.Lock()
	if a.settingsHwnd != 0 {
		hwnd := a.settingsHwnd
		a.settingsMu.Unlock()
		procPostMessageW.Call(hwnd, WM_SETTINGS_ACTIVATE, 0, 0)
		return
	}
	if a.settingsOpening {
		a.settingsMu.Unlock()
		return
	}
	a.settingsOpening = true
	a.settingsMu.Unlock()

	a.goSafe("settings-ui", func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		defer func() {
			a.settingsMu.Lock()
			a.settingsOpening = false
			if a.settingsHwnd != 0 {
				// WM_DESTROY normally clears this. This fallback prevents a stale
				// handle if creation/message-loop setup exits abnormally.
				a.settingsHwnd = 0
			}
			a.settingsMu.Unlock()
			if recovered := recover(); recovered != nil {
				a.reportRecoveredPanic("settings-ui-thread", recovered)
			}
		}()
		a.runSettingsThread()
	})
}

func (a *App) runSettingsThread() {
	select {
	case <-a.stopCh:
		return
	default:
	}

	screenW, _, _ := procGetSystemMetrics.Call(0)
	screenH, _, _ := procGetSystemMetrics.Call(1)
	uiInit()
	width, height := uiS(990), uiS(790)
	x := int32(screenW)/2 - width/2
	y := int32(screenH)/2 - height/2

	hInstance, _, _ := procGetModuleHandleW.Call(0)
	hwnd, _, _ := procCreateWindowExW.Call(
		0,
		uintptr(unsafe.Pointer(wstr("WiFiWatchdog.Settings"))),
		uintptr(unsafe.Pointer(wstr("WiFi Watchdog 控制中心 v"+appVersion))),
		uintptr(WS_OVERLAPPED|WS_CAPTION|WS_SYSMENU|WS_CLIPCHILDREN),
		uintptr(x), uintptr(y), uintptr(width), uintptr(height),
		0, 0, hInstance, 0,
	)
	if hwnd == 0 {
		messageBox(a.hwnd, "WiFi Watchdog", "无法打开设置窗口。", MB_OK|MB_ICONERROR)
		return
	}

	c := a.getConfig()
	sc := &settingsControls{hwnd: hwnd, edits: map[int]uintptr{}}
	settingsMap.Store(hwnd, sc)

	buildFluentSettings(sc, c, a)

	a.settingsMu.Lock()
	a.settingsHwnd = hwnd
	a.settingsOpening = false
	a.settingsMu.Unlock()

	// Build the full control tree while hidden, then show it once. The settings
	// UI now owns a dedicated OS thread and message queue, isolated from the tray
	// and Explorer Shell calls on the main UI thread.
	setControlText(sc.statusLine, "所有设置在后台保存；点击概览页“刷新网络状态”更新详情。")
	procShowWindow.Call(hwnd, SW_SHOW)
	procSetForegroundWindow.Call(hwnd)

	a.settingsHeartbeat.Store(time.Now().UnixNano())
	a.goSafe("settings-hang-watchdog", func() { a.watchSettingsResponsiveness(hwnd) })

	select {
	case <-a.stopCh:
		procDestroyWindow.Call(hwnd)
		return
	default:
	}

	var m msg
	for {
		r, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(r) == -1 || r == 0 {
			break
		}
		if handled, _, _ := procIsDialogMessageW.Call(hwnd, uintptr(unsafe.Pointer(&m))); handled != 0 {
			continue
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		procDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
	}
}

func (a *App) watchSettingsResponsiveness(hwnd uintptr) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-a.stopCh:
			return
		case <-ticker.C:
			a.settingsMu.Lock()
			alive := a.settingsHwnd == hwnd
			a.settingsMu.Unlock()
			if !alive {
				return
			}

			now := time.Now()
			last := a.settingsHeartbeat.Load()
			procPostMessageW.Call(hwnd, WM_SETTINGS_HEARTBEAT, 0, 0)
			if last == 0 || now.Sub(time.Unix(0, last)) < 6*time.Second {
				continue
			}

			lastDump := a.settingsLastDump.Load()
			if lastDump != 0 && now.Sub(time.Unix(0, lastDump)) < 30*time.Second {
				continue
			}
			a.settingsLastDump.Store(now.UnixNano())
			a.writeSettingsHangReport(now, hwnd)
		}
	}
}

func (a *App) writeSettingsHangReport(now time.Time, hwnd uintptr) {
	dir := filepath.Join(a.dataDir, "diagnostics")
	_ = os.MkdirAll(dir, 0755)
	a.cleanupOldDiagnostics(now)
	path := filepath.Join(dir, "hang-"+now.Format("20060102-150405")+".txt")

	buf := make([]byte, 2*1024*1024)
	n := runtime.Stack(buf, true)
	state, status := a.currentStatus()
	body := fmt.Sprintf(
		"WiFi Watchdog UI hang report\r\nVersion: %s\r\nTime: %s\r\nSettings HWND: 0x%x\r\nMonitor state: %d\r\nMonitor status: %s\r\n\r\n===== ALL GO GOROUTINES =====\r\n%s",
		appVersion, now.Format(time.RFC3339), hwnd, state, status, string(buf[:n]),
	)
	_ = atomicWriteFile(path, []byte(body), 0644)
	if a.logger != nil {
		a.logger.err("检测到设置窗口超过 6 秒未处理心跳，已生成挂起报告：" + path)
	}
}

func getWindowText(hwnd uintptr) string {
	n, _, _ := procGetWindowTextLengthW.Call(hwnd)
	buf := make([]uint16, int(n)+1)
	procGetWindowTextW.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	return syscall.UTF16ToString(buf)
}

func (a *App) startSettingsRefresh(hwnd uintptr) {
	v, ok := settingsMap.Load(hwnd)
	if !ok {
		return
	}
	sc := v.(*settingsControls)
	sc.mu.Lock()
	if sc.refreshing {
		sc.mu.Unlock()
		return
	}
	sc.refreshing = true
	sc.mu.Unlock()
	procEnableWindow.Call(sc.refreshButton, 0)
	setControlText(sc.statusLine, "正在后台逐项检测四个 HTTP 目标与开机自启状态…")

	a.goSafe("settings-refresh", func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				if v, ok := settingsMap.Load(hwnd); ok {
					sc := v.(*settingsControls)
					sc.mu.Lock()
					sc.refreshing = false
					sc.startupErr = fmt.Sprintf("后台刷新异常：%v", recovered)
					sc.mu.Unlock()
					procPostMessageW.Call(hwnd, WM_SETTINGS_REFRESH_DONE, 0, 0)
				}
				a.reportRecoveredPanic("settings-refresh", recovered)
			}
		}()
		assessment := a.assessNetworkMode(true)
		startupEnabled, startupErr := startupTaskEnabled()
		v, ok := settingsMap.Load(hwnd)
		if !ok {
			return
		}
		sc := v.(*settingsControls)
		sc.mu.Lock()
		sc.assessment = assessment
		sc.assessmentReady = true
		sc.startupEnabled = startupEnabled
		sc.startupKnown = startupErr == nil
		if startupErr != nil {
			sc.startupErr = startupErr.Error()
		} else {
			sc.startupErr = ""
		}
		sc.refreshing = false
		sc.mu.Unlock()
		procPostMessageW.Call(hwnd, WM_SETTINGS_REFRESH_DONE, 0, 0)
	})
}

func (a *App) applySettingsRefresh(hwnd uintptr) {
	v, ok := settingsMap.Load(hwnd)
	if !ok {
		return
	}
	sc := v.(*settingsControls)
	sc.mu.Lock()
	n := sc.assessment
	ready := sc.assessmentReady
	startupKnown := sc.startupKnown
	startupEnabled := sc.startupEnabled
	startupErr := sc.startupErr
	startupTouched := sc.startupTouched
	sc.mu.Unlock()
	procEnableWindow.Call(sc.refreshButton, 1)
	if !ready {
		if startupErr != "" {
			setControlText(sc.statusLine, "状态刷新失败："+startupErr)
		} else {
			setControlText(sc.statusLine, "状态刷新未完成，请稍后重试。")
		}
		return
	}

	if n.Online {
		setControlText(sc.summarySystem, "系统互联网：正常（允许经过 VPN/TUN）")
	} else {
		setControlText(sc.summarySystem, "系统互联网：异常 · "+n.Reason)
	}
	if n.WiFi.Connected {
		name := n.WiFi.SSID
		if name == "" {
			name = n.WiFi.ProfileName
		}
		underlay := n.Underlay.Reason
		if underlay == "" {
			underlay = "系统在线，未执行侵入式底层检查"
		}
		setControlText(sc.summaryWiFi, fmt.Sprintf("Wi-Fi：已连接 %s · Profile=%s · 信号=%d%% · %s", name, n.WiFi.ProfileName, n.WiFi.SignalQuality, underlay))
	} else {
		setControlText(sc.summaryWiFi, "Wi-Fi：当前未关联无线网络")
	}
	if n.VPN.Detected {
		detail := strings.Join(n.VPN.Signals, "; ")
		if len([]rune(detail)) > 75 {
			detail = string([]rune(detail)[:75]) + "…"
		}
		if n.VPN.Mihomo.Available {
			if n.VPN.Mihomo.Authenticated {
				detail = fmt.Sprintf("Mihomo API 正常; mode=%s tun=%t; mixed=%d; HTTPS=%t", n.VPN.Mihomo.Mode, n.VPN.Mihomo.TunEnabled, n.VPN.EffectivePort, n.VPN.ProxyUpstreamOK)
			} else if n.VPN.Mihomo.Unauthorized {
				detail = "Mihomo 控制接口可达，但缺少正确的 Secret；Wi-Fi 保护仍有效"
			}
		}
		setControlText(sc.summaryVPN, "VPN/TUN："+detail)
	} else if n.Online && !n.DeepChecked {
		setControlText(sc.summaryVPN, "VPN/TUN：系统在线，未执行深度检测（无需影响 Wi-Fi 判定）")
	} else {
		if n.DeepChecked {
			setControlText(sc.summaryVPN, "VPN/TUN：未确认控制接口；9097 不监听不等于 Mihomo 已停止")
		} else {
			setControlText(sc.summaryVPN, "VPN/TUN：未执行深度探测")
		}
	}
	uiUpdateMemory(sc)
	if startupKnown && !startupTouched {
		setCheck(sc.startup, startupEnabled)
	}
	if startupErr != "" {
		setControlText(sc.statusLine, "完整检测已完成；开机自启查询失败："+startupErr)
	} else {
		setControlText(sc.statusLine, fmt.Sprintf("完整HTTP检测：有效%d/%d，逐项状态码、耗时和错误详见当日日志或诊断报告。", n.System.ValidHTTP, n.System.HTTPAttempted))
	}
}

func (a *App) collectSettings(hwnd uintptr) (Config, error) {
	v, ok := settingsMap.Load(hwnd)
	if !ok {
		return Config{}, fmt.Errorf("设置窗口已关闭")
	}
	sc := v.(*settingsControls)
	c := a.getConfig()
	parse := func(id int, minV, maxV int) (int, error) {
		text := strings.TrimSpace(getWindowText(sc.edits[id]))
		n, err := strconv.Atoi(text)
		if err != nil || n < minV || n > maxV {
			return 0, fmt.Errorf("请输入 %d 到 %d 之间的整数", minV, maxV)
		}
		return n, nil
	}
	values := []struct {
		id   int
		minV int
		maxV int
		set  func(int)
	}{
		{ID_EDIT_NORMAL_MINUTES, 1, 1440, func(v int) { c.NormalCheckIntervalMinutes = v }},
		{ID_EDIT_FAILURE_SECONDS, 1, 3600, func(v int) { c.FailureCheckIntervalSeconds = v }},
		{ID_EDIT_FAILURE_COUNT, 1, 100, func(v int) { c.FailureThreshold = v }},
		{ID_EDIT_REPAIR_MINUTES, 1, 1440, func(v int) { c.RepairRetryIntervalMinutes = v }},
		{ID_EDIT_DISABLE_SECONDS, 1, 120, func(v int) { c.WifiDisableWaitSeconds = v }},
		{ID_EDIT_STARTUP_SECONDS, 1, 600, func(v int) { c.WifiStartupWaitSeconds = v }},
		{ID_EDIT_CONNECT_RETRY, 1, 10, func(v int) { c.ConnectRetryCount = v }},
		{ID_EDIT_CONNECT_DELAY, 1, 60, func(v int) { c.ConnectRetryDelaySeconds = v }},
		{ID_EDIT_DHCP_WAIT, 1, 120, func(v int) { c.DHCPRenewWaitSeconds = v }},
		{ID_EDIT_VPN_PORT, 0, 65535, func(v int) { c.VPNLocalPort = v }},
		{ID_EDIT_MIHOMO_CONTROLLER, 0, 65535, func(v int) { c.MihomoControllerPort = v }},
		{ID_EDIT_TIMEOUT_SECONDS, 1, 30, func(v int) { c.ConnectionTimeoutSeconds = v }},
		{ID_EDIT_LOG_RETENTION, 1, 3650, func(v int) { c.LogRetentionDays = v }},
	}
	for _, item := range values {
		n, err := parse(item.id, item.minV, item.maxV)
		if err != nil {
			return Config{}, err
		}
		item.set(n)
	}
	c.StartWithWindows = isChecked(sc.startup)
	c.AutoReconnectDisconnected = isChecked(sc.autoReconnect)
	c.EnableWlanServiceRestart = isChecked(sc.wlanSvc)
	c.EnableVPNAware = isChecked(sc.vpnAware)
	return normalizeConfig(c), nil
}

func (a *App) saveSettingsAsync(hwnd uintptr) {
	c, err := a.collectSettings(hwnd)
	if err != nil {
		messageBox(hwnd, "WiFi Watchdog", "参数无效："+err.Error(), MB_OK|MB_ICONWARNING)
		return
	}
	v, ok := settingsMap.Load(hwnd)
	if !ok {
		return
	}
	sc := v.(*settingsControls)
	sc.mu.Lock()
	if sc.saveInProgress {
		sc.mu.Unlock()
		return
	}
	sc.saveInProgress = true
	sc.saveErr = ""
	sc.pendingConfig = c
	sc.mu.Unlock()
	procEnableWindow.Call(sc.saveButton, 0)
	procEnableWindow.Call(sc.cancelButton, 0)
	setControlText(sc.saveButton, "保存中…")
	setControlText(sc.statusLine, "正在后台保存设置；窗口仍可响应，不会阻塞 UI…")

	old := a.getConfig()
	a.goSafe("settings-save", func() {
		var saveErr error
		defer func() {
			if recovered := recover(); recovered != nil {
				saveErr = fmt.Errorf("后台保存异常：%v", recovered)
				a.reportRecoveredPanic("settings-save", recovered)
			}
			v, ok := settingsMap.Load(hwnd)
			if !ok {
				return
			}
			sc := v.(*settingsControls)
			sc.mu.Lock()
			if saveErr != nil {
				sc.saveErr = saveErr.Error()
			}
			sc.saveInProgress = false
			sc.pendingConfig = c
			sc.mu.Unlock()
			procPostMessageW.Call(hwnd, WM_SETTINGS_SAVE_DONE, 0, 0)
		}()
		startupChanged := c.StartWithWindows != old.StartWithWindows
		if startupChanged {
			saveErr = setStartupTask(c.StartWithWindows)
		}
		if saveErr == nil {
			saveErr = a.saveConfig(c)
			if saveErr != nil && startupChanged {
				_ = setStartupTask(old.StartWithWindows)
			}
		}
	})
}

func (a *App) finishSettingsSave(hwnd uintptr) {
	v, ok := settingsMap.Load(hwnd)
	if !ok {
		return
	}
	sc := v.(*settingsControls)
	sc.mu.Lock()
	errText := sc.saveErr
	c := sc.pendingConfig
	sc.mu.Unlock()
	if errText != "" {
		procEnableWindow.Call(sc.saveButton, 1)
		procEnableWindow.Call(sc.cancelButton, 1)
		setControlText(sc.saveButton, "保存")
		setControlText(sc.statusLine, "保存失败；已恢复按钮，可修改后重试。")
		messageBox(hwnd, "WiFi Watchdog", "保存设置失败：\r\n\r\n"+errText, MB_OK|MB_ICONERROR)
		return
	}
	a.setConfig(c)
	a.goSafe("settings-save-log", func() {
		a.logger.info("设置已保存，监控循环将立即应用新参数。")
	})
	a.wake()
	procEnableWindow.Call(sc.saveButton, 1)
	procEnableWindow.Call(sc.cancelButton, 1)
	setControlText(sc.saveButton, "保存")
	sc.mu.Lock()
	sc.startupKnown = true
	sc.startupEnabled = c.StartWithWindows
	sc.startupTouched = false
	sc.mu.Unlock()
	setControlText(sc.statusLine, "设置已保存并立即生效；窗口保持打开，可继续调整。")
}

func (a *App) restoreSettingsDefaults(hwnd uintptr) {
	v, ok := settingsMap.Load(hwnd)
	if !ok {
		return
	}
	sc := v.(*settingsControls)
	d := defaultConfig()
	values := map[int]int{
		ID_EDIT_NORMAL_MINUTES:    d.NormalCheckIntervalMinutes,
		ID_EDIT_FAILURE_SECONDS:   d.FailureCheckIntervalSeconds,
		ID_EDIT_FAILURE_COUNT:     d.FailureThreshold,
		ID_EDIT_REPAIR_MINUTES:    d.RepairRetryIntervalMinutes,
		ID_EDIT_DISABLE_SECONDS:   d.WifiDisableWaitSeconds,
		ID_EDIT_STARTUP_SECONDS:   d.WifiStartupWaitSeconds,
		ID_EDIT_CONNECT_RETRY:     d.ConnectRetryCount,
		ID_EDIT_CONNECT_DELAY:     d.ConnectRetryDelaySeconds,
		ID_EDIT_DHCP_WAIT:         d.DHCPRenewWaitSeconds,
		ID_EDIT_VPN_PORT:          d.VPNLocalPort,
		ID_EDIT_MIHOMO_CONTROLLER: d.MihomoControllerPort,
		ID_EDIT_TIMEOUT_SECONDS:   d.ConnectionTimeoutSeconds,
		ID_EDIT_LOG_RETENTION:     d.LogRetentionDays,
	}
	for id, value := range values {
		setControlText(sc.edits[id], strconv.Itoa(value))
	}
	setCheck(sc.vpnAware, d.EnableVPNAware)
	setCheck(sc.autoReconnect, d.AutoReconnectDisconnected)
	setCheck(sc.wlanSvc, d.EnableWlanServiceRestart)
	setCheck(sc.startup, d.StartWithWindows)
	sc.mu.Lock()
	sc.startupTouched = true
	sc.mu.Unlock()
	setControlText(sc.statusLine, "已载入默认参数；点击“保存”后才会生效。")
}

func settingsWndProc(hwnd uintptr, message uint32, wParam, lParam uintptr) uintptr {
	if handled, result := uiHandleSettingsMessage(hwnd, message, wParam, lParam); handled {
		return result
	}
	switch message {
	case WM_COMMAND:
		id := int(loword(wParam))
		switch id {
		case ID_NAV_OVERVIEW, ID_NAV_CHECKS, ID_NAV_RECOVERY, ID_NAV_VPN:
			if v, ok := settingsMap.Load(hwnd); ok {
				uiShowPage(v.(*settingsControls), id-ID_NAV_OVERVIEW)
			}
			return 0
		case ID_BUTTON_SAVE:
			if app != nil {
				app.saveSettingsAsync(hwnd)
			}
			return 0
		case ID_BUTTON_CANCEL:
			procDestroyWindow.Call(hwnd)
			return 0
		case ID_BUTTON_REFRESH:
			if app != nil {
				app.startSettingsRefresh(hwnd)
			}
			return 0
		case ID_BUTTON_DEFAULTS:
			if app != nil {
				app.restoreSettingsDefaults(hwnd)
			}
			return 0
		case ID_BUTTON_DIAG:
			if app != nil {
				app.goSafe("settings-diagnostics", func() {
					path := app.generateDiagnosticReport()
					_ = exec.Command("notepad.exe", path).Start()
				})
			}
			return 0
		case ID_CHECK_STARTUP:
			if v, ok := settingsMap.Load(hwnd); ok {
				sc := v.(*settingsControls)
				sc.mu.Lock()
				sc.startupTouched = true
				sc.mu.Unlock()
			}
		}
	case WM_SETTINGS_REFRESH_DONE:
		if app != nil {
			app.applySettingsRefresh(hwnd)
		}
		return 0
	case WM_SETTINGS_SAVE_DONE:
		if app != nil {
			app.finishSettingsSave(hwnd)
		}
		return 0
	case WM_SETTINGS_SHUTDOWN:
		// The main application is exiting: destroy this UI even when a settings
		// save is in progress. Workers finish independently during graceful exit.
		procDestroyWindow.Call(hwnd)
		return 0
	case WM_SETTINGS_ACTIVATE:
		if v, ok := settingsMap.Load(hwnd); ok {
			uiUpdateMemory(v.(*settingsControls))
		}
		procShowWindow.Call(hwnd, SW_SHOW)
		procSetForegroundWindow.Call(hwnd)
		return 0
	case WM_SETTINGS_LIVE_STATUS:
		if app != nil {
			if v, ok := settingsMap.Load(hwnd); ok {
				sc := v.(*settingsControls)
				state, text := app.currentStatus()
				if sc.page == modernPageOverview {
					setControlText(sc.summarySystem, fmt.Sprintf("后台监控 [%d]：%s", state, text))
				}
			}
		}
		return 0
	case WM_SETTINGS_HEARTBEAT:
		if app != nil {
			app.settingsHeartbeat.Store(time.Now().UnixNano())
		}
		return 0
	case WM_CLOSE:
		if v, ok := settingsMap.Load(hwnd); ok {
			sc := v.(*settingsControls)
			sc.mu.Lock()
			busy := sc.saveInProgress
			sc.mu.Unlock()
			if busy {
				// Never show a modal dialog in the Win11 settings message pump:
				// a short async save should not look like a frozen window.
				setControlText(sc.statusLine, "后台正在保存设置，请稍候再关闭窗口。")
				return 0
			}
		}
		procDestroyWindow.Call(hwnd)
		return 0
	case WM_DESTROY:
		settingsMap.Delete(hwnd)
		if app != nil {
			app.settingsMu.Lock()
			if app.settingsHwnd == hwnd {
				app.settingsHwnd = 0
			}
			app.settingsOpening = false
			app.settingsMu.Unlock()
		}
		// Settings owns a dedicated message queue/thread, so WM_QUIT here only
		// terminates that UI thread and cannot stop the tray/main window.
		procPostQuitMessage.Call(0)
		return 0
	}
	r, _, _ := procDefWindowProcW.Call(hwnd, uintptr(message), wParam, lParam)
	return r
}

func mainWndProc(hwnd uintptr, message uint32, wParam, lParam uintptr) uintptr {
	switch message {
	case WM_TRAYICON:
		event := uint32(lParam)
		if event == WM_RBUTTONUP || event == WM_CONTEXTMENU {
			if app != nil {
				app.showTrayMenu()
			}
			return 0
		}
		if event == WM_LBUTTONDBLCLK {
			if app != nil {
				app.showSettings()
			}
			return 0
		}
	case WM_STATUS_UPDATE:
		if app != nil {
			app.updateTray()
		}
		return 0
	case WM_DESTROY:
		if app != nil {
			app.settingsMu.Lock()
			settingsHwnd := app.settingsHwnd
			app.settingsMu.Unlock()
			if settingsHwnd != 0 {
				procPostMessageW.Call(settingsHwnd, WM_SETTINGS_SHUTDOWN, 0, 0)
			}
			app.stop()
			app.removeTrayIcon()
			app.logger.info("WiFi Watchdog 退出。")
		}
		procPostQuitMessage.Call(0)
		return 0
	}
	r, _, _ := procDefWindowProcW.Call(hwnd, uintptr(message), wParam, lParam)
	return r
}

func registerWindowClasses(hInstance uintptr) bool {
	cursor, _, _ := procLoadCursorW.Call(0, IDC_ARROW)

	mainName := wstr("WiFiWatchdog.Main")
	wc := wndClassEx{
		CbSize:        uint32(unsafe.Sizeof(wndClassEx{})),
		LpfnWndProc:   syscall.NewCallback(mainWndProc),
		HInstance:     hInstance,
		HIcon:         iconResource(IDI_SHIELD),
		HCursor:       cursor,
		HbrBackground: uintptr(COLOR_WINDOW + 1),
		LpszClassName: mainName,
		HIconSm:       iconResource(IDI_SHIELD),
	}
	if r, _, _ := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc))); r == 0 {
		return false
	}

	settingsName := wstr("WiFiWatchdog.Settings")
	wc2 := wndClassEx{
		CbSize:        uint32(unsafe.Sizeof(wndClassEx{})),
		LpfnWndProc:   syscall.NewCallback(settingsWndProc),
		HInstance:     hInstance,
		HIcon:         iconResource(IDI_SHIELD),
		HCursor:       cursor,
		HbrBackground: uintptr(COLOR_WINDOW + 1),
		LpszClassName: settingsName,
		HIconSm:       iconResource(IDI_SHIELD),
	}
	if r, _, _ := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc2))); r == 0 {
		return false
	}
	return true
}

// ============================================================
// Elevation / singleton / startup
// ============================================================

func isAdmin() bool {
	r, _, _ := procIsUserAnAdmin.Call()
	return r != 0
}

func relaunchElevated() bool {
	exePath, err := os.Executable()
	if err != nil {
		return false
	}
	r, _, _ := procShellExecuteW.Call(
		0,
		uintptr(unsafe.Pointer(wstr("runas"))),
		uintptr(unsafe.Pointer(wstr(exePath))),
		0,
		0,
		SW_SHOWNORMAL,
	)
	return r > 32
}

func createSingletonMutex() (uintptr, bool) {
	name := wstr("Global\\WiFiWatchdog.Singleton")
	h, _, err := procCreateMutexW.Call(0, 0, uintptr(unsafe.Pointer(name)))
	if h == 0 {
		return 0, false
	}
	errno, ok := err.(syscall.Errno)
	if ok && errno == ERROR_ALREADY_EXISTS {
		procCloseHandle.Call(h)
		return 0, false
	}
	return h, true
}

func ensureDataDir() (dataDir, configPath, logDir string, err error) {
	base := os.Getenv("LOCALAPPDATA")
	if base == "" {
		home, e := os.UserHomeDir()
		if e != nil {
			return "", "", "", e
		}
		base = filepath.Join(home, "AppData", "Local")
	}
	dataDir = filepath.Join(base, "WiFiWatchdog")
	if err = os.MkdirAll(dataDir, 0755); err != nil {
		return
	}
	configPath = filepath.Join(dataDir, "config.json")
	logDir = filepath.Join(dataDir, "logs")
	if err = os.MkdirAll(logDir, 0755); err != nil {
		return
	}
	return
}

func migrateLegacyLogs(dataDir, logDir string) {
	legacy := []string{"watchdog.log", "watchdog.log.1"}
	for _, name := range legacy {
		src := filepath.Join(dataDir, name)
		if _, err := os.Stat(src); err != nil {
			continue
		}
		dst := filepath.Join(logDir, "legacy-"+name)
		if _, err := os.Stat(dst); err == nil {
			dst = filepath.Join(logDir, "legacy-"+time.Now().Format("20060102-150405")+"-"+name)
		}
		_ = os.Rename(src, dst)
	}
}

func main() {
	// A lower steady-state Go heap reduces idle memory without embedding a
	// browser UI. The memory limit is a soft GC target, NOT an RSS limit.
	// Environment-provided GOGC/GOMEMLIMIT remain authoritative.
	if os.Getenv("GOGC") == "" {
		debug.SetGCPercent(70)
	}
	if os.Getenv("GOMEMLIMIT") == "" {
		debug.SetMemoryLimit(96 << 20)
	}

	// Win32 windows and message queues are OS-thread-affine. Keep all UI creation
	// and the message pump on one dedicated OS thread; goroutines must communicate
	// back through PostMessage instead of touching the message loop.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	// 让原生 Win32 设置窗口在高 DPI / 125%-200% 缩放下保持清晰并减少布局错位。
	procSetProcessDPIAware.Call()

	if !isAdmin() {
		if !relaunchElevated() {
			messageBox(0, "WiFi Watchdog", "WiFi Watchdog 需要管理员权限才能自动重启无线网卡。\r\n\r\n请在 UAC 提示中选择“是”。", MB_OK|MB_ICONWARNING)
		}
		return
	}

	mutexHandle, unique := createSingletonMutex()
	if !unique {
		messageBox(0, "WiFi Watchdog", "WiFi Watchdog 已经在运行。请查看任务栏右下角托盘。", MB_OK|MB_ICONINFO)
		return
	}
	defer procCloseHandle.Call(mutexHandle)

	dataDir, configPath, logDir, err := ensureDataDir()
	if err != nil {
		messageBox(0, "WiFi Watchdog", "无法创建数据目录：\r\n"+err.Error(), MB_OK|MB_ICONERROR)
		return
	}

	cfg := loadConfig(configPath)
	migrateLegacyLogs(dataDir, logDir)
	logger := newLogger(logDir, cfg.LogRetentionDays)
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		if b, e := json.MarshalIndent(cfg, "", "  "); e == nil {
			_ = os.WriteFile(configPath, b, 0644)
		}
	}

	hInstance, _, _ := procGetModuleHandleW.Call(0)
	if !registerWindowClasses(hInstance) {
		messageBox(0, "WiFi Watchdog", "注册 Windows 窗口类失败。", MB_OK|MB_ICONERROR)
		return
	}

	hwnd, _, _ := procCreateWindowExW.Call(
		0,
		uintptr(unsafe.Pointer(wstr("WiFiWatchdog.Main"))),
		uintptr(unsafe.Pointer(wstr("WiFi Watchdog"))),
		uintptr(WS_OVERLAPPED),
		0, 0, 0, 0,
		0, 0, hInstance, 0,
	)
	if hwnd == 0 {
		messageBox(0, "WiFi Watchdog", "创建后台窗口失败。", MB_OK|MB_ICONERROR)
		return
	}

	app = &App{
		hwnd:        hwnd,
		configPath:  configPath,
		logDir:      logDir,
		dataDir:     dataDir,
		cfg:         cfg,
		logger:      logger,
		state:       StateStarting,
		status:      "正在启动...",
		stopCh:      make(chan struct{}),
		wakeCh:      make(chan struct{}, 1),
		mutexHandle: mutexHandle,
	}

	if !app.addTrayIcon() {
		messageBox(0, "WiFi Watchdog", "创建系统托盘图标失败。", MB_OK|MB_ICONERROR)
		procDestroyWindow.Call(hwnd)
		return
	}

	logger.info("WiFi Watchdog 程序启动。")
	app.goSafe("monitor-loop", app.monitorLoop)

	var m msg
	for {
		r, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(r) == -1 || r == 0 {
			break
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		procDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
	}

	// Give in-flight recovery workers a short grace period to finish critical
	// cleanup (especially re-enabling an adapter/service) before process exit.
	done := make(chan struct{})
	go func() {
		app.workers.Wait()
		close(done)
	}()
	select {
	case <-done:
		logger.info("后台任务已安全结束。")
	case <-time.After(150 * time.Second):
		logger.warn("退出等待后台任务超过 150 秒，仍有任务在执行；请检查网卡启用状态。")
	}
}
