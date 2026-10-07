//go:build windows

package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unicode/utf16"
	"unsafe"
)

var appVersion = "1.4.1"

type RecoveryTarget struct {
	ProfileName          string    `json:"profileName"`
	SSID                 string    `json:"ssid"`
	InterfaceGUID        string    `json:"interfaceGuid"`
	InterfaceName        string    `json:"interfaceName"`
	InterfaceDescription string    `json:"interfaceDescription"`
	LastSeen             time.Time `json:"lastSeen"`
}

type PersistentState struct {
	Version          int            `json:"version"`
	LastTarget       RecoveryTarget `json:"lastTarget"`
	LastAutoRepairAt time.Time      `json:"lastAutoRepairAt,omitempty"`
	LastInternetOKAt time.Time      `json:"lastInternetOkAt,omitempty"`
}

func atomicWriteFile(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	ok := false
	defer func() {
		_ = tmp.Close()
		if !ok {
			_ = os.Remove(tmpName)
		}
	}()
	if err := tmp.Chmod(perm); err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	const moveFileReplaceExisting = 0x1
	const moveFileWriteThrough = 0x8
	r, _, callErr := procMoveFileExW.Call(
		uintptr(unsafe.Pointer(wstr(tmpName))),
		uintptr(unsafe.Pointer(wstr(path))),
		moveFileReplaceExisting|moveFileWriteThrough,
	)
	if r == 0 {
		return fmt.Errorf("MoveFileExW replace failed: %v", callErr)
	}
	ok = true
	return nil
}

func targetFromInfo(i wifiInfo) RecoveryTarget {
	return RecoveryTarget{
		ProfileName:          i.ProfileName,
		SSID:                 i.SSID,
		InterfaceGUID:        i.InterfaceGUID,
		InterfaceName:        i.InterfaceName,
		InterfaceDescription: i.InterfaceDescription,
		LastSeen:             time.Now(),
	}
}

func mergeTarget(primary, fallback RecoveryTarget) RecoveryTarget {
	out := primary
	if out.ProfileName == "" {
		out.ProfileName = fallback.ProfileName
	}
	if out.SSID == "" {
		out.SSID = fallback.SSID
	}
	if out.InterfaceGUID == "" {
		out.InterfaceGUID = fallback.InterfaceGUID
	}
	if out.InterfaceName == "" {
		out.InterfaceName = fallback.InterfaceName
	}
	if out.InterfaceDescription == "" {
		out.InterfaceDescription = fallback.InterfaceDescription
	}
	if out.LastSeen.IsZero() {
		out.LastSeen = fallback.LastSeen
	}
	return out
}

func (a *App) statePath() string { return filepath.Join(a.dataDir, "state.json") }

func (a *App) loadPersistentState() PersistentState {
	a.stateMu.Lock()
	defer a.stateMu.Unlock()
	return a.loadPersistentStateLocked()
}

func (a *App) loadPersistentStateLocked() PersistentState {
	b, err := os.ReadFile(a.statePath())
	if err != nil {
		return PersistentState{Version: 2}
	}
	var st PersistentState
	if json.Unmarshal(b, &st) != nil {
		backup := a.statePath() + ".invalid-" + time.Now().Format("20060102-150405") + ".json"
		_ = os.Rename(a.statePath(), backup)
		return PersistentState{Version: 2}
	}
	if st.Version < 2 {
		st.Version = 2
	}
	return st
}

func (a *App) savePersistentStateLocked(st PersistentState) error {
	st.Version = 2
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	return atomicWriteFile(a.statePath(), b, 0644)
}

func (a *App) updatePersistentState(fn func(*PersistentState)) error {
	a.stateMu.Lock()
	defer a.stateMu.Unlock()
	st := a.loadPersistentStateLocked()
	fn(&st)
	return a.savePersistentStateLocked(st)
}

func (a *App) loadRememberedTarget() RecoveryTarget {
	return a.loadPersistentState().LastTarget
}

func (a *App) rememberTarget(t RecoveryTarget) {
	if t.ProfileName == "" && t.SSID == "" {
		return
	}
	t.LastSeen = time.Now()
	if err := a.updatePersistentState(func(st *PersistentState) { st.LastTarget = t }); err != nil {
		a.logger.warn("保存恢复目标失败：" + err.Error())
		return
	}
	a.logger.info(fmt.Sprintf("已记住恢复目标：Profile=%q, SSID=%q, 接口=%q。", t.ProfileName, t.SSID, t.InterfaceName))
}

func (a *App) recordInternetOK() {
	_ = a.updatePersistentState(func(st *PersistentState) { st.LastInternetOKAt = time.Now() })
}

func (a *App) recordAutoRepairAttempt() {
	_ = a.updatePersistentState(func(st *PersistentState) { st.LastAutoRepairAt = time.Now() })
}

func (a *App) remainingAutoRepairCooldown(interval time.Duration) time.Duration {
	st := a.loadPersistentState()
	if st.LastAutoRepairAt.IsZero() {
		return 0
	}
	remaining := interval - time.Since(st.LastAutoRepairAt)
	if remaining < 0 {
		return 0
	}
	return remaining
}

func (a *App) rememberConnectedInfo(i wifiInfo) {
	if !i.Connected {
		return
	}
	t := a.resolveTarget(i)
	a.rememberTarget(t)
}

func sameNetworkIdentity(current wifiInfo, remembered RecoveryTarget) bool {
	if current.ProfileName != "" && remembered.ProfileName != "" {
		return current.ProfileName == remembered.ProfileName
	}
	if current.SSID != "" && remembered.SSID != "" {
		return current.SSID == remembered.SSID
	}
	if current.InterfaceGUID != "" && remembered.InterfaceGUID != "" {
		return strings.EqualFold(strings.Trim(current.InterfaceGUID, "{}"), strings.Trim(remembered.InterfaceGUID, "{}"))
	}
	return false
}

func (a *App) resolveTarget(current wifiInfo) RecoveryTarget {
	remembered := a.loadRememberedTarget()
	base := targetFromInfo(current)
	if !current.Connected {
		// If disconnected, keep current interface identity but use the last known
		// successful Profile/SSID. This is the key fallback when auto-connect fails.
		return mergeTarget(base, remembered)
	}

	// When connected to a different network, never borrow the old Profile just
	// because a privacy restriction hid the current Profile name. Only merge the
	// remembered target when there is evidence it is the same network/interface.
	if sameNetworkIdentity(current, remembered) {
		return mergeTarget(base, remembered)
	}
	return base
}

func targetMatches(i wifiInfo, t RecoveryTarget) bool {
	if !i.Connected {
		return false
	}
	if t.ProfileName != "" && i.ProfileName != "" {
		return i.ProfileName == t.ProfileName
	}
	if t.SSID != "" && i.SSID != "" {
		return i.SSID == t.SSID
	}
	return i.Connected
}

func (a *App) waitForAssociation(t RecoveryTarget, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		select {
		case <-a.stopCh:
			return false
		default:
		}
		info := detectWifi()
		if targetMatches(info, t) {
			a.logger.info(fmt.Sprintf("Wi-Fi 已关联：Profile=%q, SSID=%q, 信号=%d%%。", info.ProfileName, info.SSID, info.SignalQuality))
			a.rememberConnectedInfo(info)
			return true
		}
		time.Sleep(time.Second)
	}
	return false
}

func runHiddenTimeout(timeout time.Duration, name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: CREATE_NO_WINDOW}
	out, err := cmd.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		return out, fmt.Errorf("command timeout after %s", timeout)
	}
	return out, err
}

func psQuote(s string) string { return strings.ReplaceAll(s, "'", "''") }

func powershellEncoded(script string, timeout time.Duration) ([]byte, error) {
	encodedRunes := utf16.Encode([]rune(script))
	b := make([]byte, len(encodedRunes)*2)
	for i, v := range encodedRunes {
		b[i*2] = byte(v)
		b[i*2+1] = byte(v >> 8)
	}
	enc := base64.StdEncoding.EncodeToString(b)
	return runHiddenTimeout(timeout, "powershell.exe", "-NoLogo", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-EncodedCommand", enc)
}

func (a *App) nativeConnectTarget(t RecoveryTarget) error {
	if t.ProfileName == "" {
		return fmt.Errorf("Native Wi-Fi 需要已保存的 Profile 名称")
	}
	if err := nativeConnectProfile(t.InterfaceGUID, t.ProfileName); err != nil {
		return err
	}
	a.logger.info("Native Wi-Fi API 已发起 Profile 主动连接：" + t.ProfileName)
	return nil
}

func (a *App) netshConnectTarget(t RecoveryTarget) error {
	profile := t.ProfileName
	if profile == "" {
		profile = t.SSID
	}
	if profile == "" {
		return fmt.Errorf("没有可用的 Profile/SSID")
	}

	args := []string{"wlan", "connect", "name=" + profile}
	if t.InterfaceName != "" {
		argsWithInterface := append(append([]string{}, args...), "interface="+t.InterfaceName)
		out, err := runHiddenTimeout(20*time.Second, "netsh.exe", argsWithInterface...)
		if err == nil {
			a.logger.info("netsh 已按指定接口发起 Wi-Fi 主动连接：" + profile)
			return nil
		}
		a.logger.warn("netsh 指定接口连接失败，将让 Windows 自动选择 WLAN 接口：" + err.Error() + " " + strings.TrimSpace(string(out)))
	}

	out, err := runHiddenTimeout(20*time.Second, "netsh.exe", args...)
	if err != nil {
		return fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out)))
	}
	a.logger.info("netsh 已发起 Wi-Fi 主动连接：" + profile)
	return nil
}

func (a *App) softReconnect(t RecoveryTarget) bool {
	c := a.getConfig()
	// A clean disconnect helps when Windows believes the old association is still alive.
	if t.InterfaceGUID != "" {
		if err := nativeDisconnect(t.InterfaceGUID); err != nil {
			a.logger.warn("Native Wi-Fi 断开旧连接失败（可忽略）：" + err.Error())
		} else {
			a.logger.info("已主动断开旧 Wi-Fi 关联。")
			time.Sleep(2 * time.Second)
		}
	} else if t.InterfaceName != "" {
		_, _ = runHiddenTimeout(15*time.Second, "netsh.exe", "wlan", "disconnect", "interface="+t.InterfaceName)
		time.Sleep(2 * time.Second)
	}

	for attempt := 1; attempt <= c.ConnectRetryCount; attempt++ {
		a.logger.warn(fmt.Sprintf("主动连接尝试 %d/%d：Profile=%q, SSID=%q。", attempt, c.ConnectRetryCount, t.ProfileName, t.SSID))
		if t.InterfaceGUID != "" {
			_ = nativeScan(t.InterfaceGUID)
		}

		// First use the locale-independent Native Wi-Fi API. WlanConnect is
		// asynchronous: ERROR_SUCCESS only means the request was accepted, so
		// explicitly verify association before deciding whether netsh is needed.
		if err := a.nativeConnectTarget(t); err != nil {
			a.logger.warn("Native Wi-Fi 连接命令失败：" + err.Error())
		} else if a.waitForAssociation(t, time.Duration(maxInt(c.ConnectRetryDelaySeconds, 3))*time.Second) {
			return true
		} else {
			a.logger.warn("Native Wi-Fi 请求已接受，但等待后仍未关联目标网络，继续 netsh 兜底。")
		}

		// netsh is deliberately attempted even when WlanConnect returned success;
		// this covers driver/service stacks that accept the API request but fail
		// to complete the actual association.
		if err := a.netshConnectTarget(t); err != nil {
			a.logger.warn("netsh Wi-Fi 连接失败：" + err.Error())
		} else if a.waitForAssociation(t, time.Duration(maxInt(c.ConnectRetryDelaySeconds, 3))*time.Second) {
			return true
		}
	}
	return false
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func (a *App) flushDNS() {
	out, err := runHiddenTimeout(20*time.Second, "ipconfig.exe", "/flushdns")
	if err != nil {
		a.logger.warn("刷新 DNS 缓存失败：" + err.Error() + " " + strings.TrimSpace(string(out)))
		return
	}
	a.logger.info("已刷新 DNS 缓存。")
}

func (a *App) renewDHCP(t RecoveryTarget) {
	c := a.getConfig()
	alias := t.InterfaceName
	if alias == "" {
		alias = detectWifiNetsh().InterfaceName
	}
	if alias == "" {
		a.logger.warn("无法确定 Wi-Fi 友好名称，跳过定向 DHCP renew，避免影响其他网卡。")
		return
	}

	renew := func(name string) error {
		out, err := runHiddenTimeout(45*time.Second, "ipconfig.exe", "/renew", name)
		if err != nil {
			return fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out)))
		}
		return nil
	}
	if err := renew(alias); err != nil {
		a.logger.warn("DHCP renew 失败：" + err.Error())
		freshAlias := detectWifiNetsh().InterfaceName
		if freshAlias != "" && !strings.EqualFold(freshAlias, alias) {
			a.logger.info("检测到 Wi-Fi 接口名称已变化，使用新名称重试 DHCP renew：" + freshAlias)
			if err2 := renew(freshAlias); err2 != nil {
				a.logger.warn("使用新接口名称 DHCP renew 仍失败：" + err2.Error())
			} else {
				a.logger.info("已使用新接口名称完成 DHCP renew：" + freshAlias)
			}
		}
	} else {
		a.logger.info("已对 Wi-Fi 网卡执行 DHCP renew：" + alias)
	}
	select {
	case <-a.stopCh:
	case <-time.After(time.Duration(c.DHCPRenewWaitSeconds) * time.Second):
	}
}

func (a *App) repairIPLayer(t RecoveryTarget) bool {
	a.logger.warn("开始网络层修复：刷新 DNS + 更新 DHCP。")
	a.flushDNS()
	a.renewDHCP(t)
	return a.hasInternet()
}

func (a *App) setAdapterEnabledNetsh(alias string, enabled bool) error {
	if alias == "" {
		return fmt.Errorf("empty interface alias")
	}
	state := "disabled"
	if enabled {
		state = "enabled"
	}
	out, err := runHiddenTimeout(25*time.Second, "netsh.exe", "interface", "set", "interface", "name="+alias, "admin="+state)
	if err != nil {
		return fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (a *App) setAdapterEnabledPowerShell(t RecoveryTarget, enabled bool) error {
	action := "Disable-NetAdapter -Name $a.Name -Confirm:$false -ErrorAction Stop"
	if enabled {
		action = "Enable-NetAdapter -Name $a.Name -Confirm:$false -ErrorAction Stop"
	}

	var clauses []string
	if t.InterfaceGUID != "" {
		g := psQuote(strings.Trim(t.InterfaceGUID, "{}"))
		clauses = append(clauses, fmt.Sprintf("$_.InterfaceGuid -eq [guid]'%s'", g))
	}
	if t.InterfaceDescription != "" {
		clauses = append(clauses, fmt.Sprintf("$_.InterfaceDescription -eq '%s'", psQuote(t.InterfaceDescription)))
	}
	if t.InterfaceName != "" {
		clauses = append(clauses, fmt.Sprintf("$_.Name -eq '%s'", psQuote(t.InterfaceName)))
	}
	if len(clauses) == 0 {
		return fmt.Errorf("no adapter identity available")
	}
	selector := strings.Join(clauses, " -or ")
	script := "$ErrorActionPreference='Stop'; $a=Get-NetAdapter | Where-Object { " + selector + " } | Select-Object -First 1; if(-not $a){throw 'Wi-Fi adapter not found'}; " + action
	out, err := powershellEncoded(script, 30*time.Second)
	if err != nil {
		return fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (a *App) restartAdapter(t RecoveryTarget) bool {
	c := a.getConfig()
	alias := t.InterfaceName
	if alias == "" {
		alias = detectWifiNetsh().InterfaceName
	}

	a.logger.warn("兜底层：准备重启 Wi-Fi 网卡。")
	err := a.setAdapterEnabledNetsh(alias, false)
	if err != nil {
		a.logger.warn("netsh 关闭网卡失败，尝试 PowerShell 兜底：" + err.Error())
		if err = a.setAdapterEnabledPowerShell(t, false); err != nil {
			a.logger.err("关闭 Wi-Fi 网卡失败：" + err.Error())
			return false
		}
	}

	// Once disabled, re-enable is a critical cleanup action. An application exit
	// request shortens the wait but must not leave the adapter disabled.
	stopping := false
	select {
	case <-a.stopCh:
		stopping = true
	case <-time.After(time.Duration(c.WifiDisableWaitSeconds) * time.Second):
	}

	err = a.setAdapterEnabledNetsh(alias, true)
	if err != nil {
		a.logger.warn("netsh 开启网卡失败，尝试 PowerShell 兜底：" + err.Error())
		if err = a.setAdapterEnabledPowerShell(t, true); err != nil {
			a.logger.err("重新开启 Wi-Fi 网卡失败：" + err.Error())
			return false
		}
	}

	a.logger.info("Wi-Fi 网卡已重新启用，等待驱动初始化。")
	if stopping {
		a.logger.info("收到退出请求；网卡已重新启用，停止后续恢复步骤。")
		return false
	}
	select {
	case <-a.stopCh:
		return false
	case <-time.After(3 * time.Second):
	}
	return true
}

func (a *App) restartWlanService() bool {
	a.logger.warn("最后兜底：尝试重启 Windows WLAN AutoConfig (WlanSvc) 服务。")
	// Stop can return non-zero if the service is already stopped; always attempt
	// the start step so an exit request cannot intentionally leave WlanSvc down.
	_, _ = runHiddenTimeout(30*time.Second, "sc.exe", "stop", "WlanSvc")
	select {
	case <-a.stopCh:
		// Continue immediately to the critical start operation.
	case <-time.After(3 * time.Second):
	}
	out, err := runHiddenTimeout(30*time.Second, "sc.exe", "start", "WlanSvc")
	if err != nil {
		a.logger.err("启动 WlanSvc 失败：" + err.Error() + " " + strings.TrimSpace(string(out)))
		return false
	}
	select {
	case <-a.stopCh:
		return false
	case <-time.After(5 * time.Second):
	}
	return true
}

// robustRepair executes increasingly invasive recovery layers. The method is
// serialized so the monitor loop and manual tray actions cannot fight each other.
func (a *App) robustRepair(t RecoveryTarget) bool {
	a.repairMu.Lock()
	defer a.repairMu.Unlock()

	current := detectWifi()
	t = mergeTarget(t, a.resolveTarget(current))
	if t.ProfileName == "" && t.SSID == "" {
		a.logger.err("没有可用的历史 Wi-Fi Profile/SSID，无法主动恢复。请先正常连接一次目标 Wi-Fi。")
		return false
	}
	a.logger.warn(fmt.Sprintf("开始多级恢复：Profile=%q, SSID=%q, 接口=%q。", t.ProfileName, t.SSID, t.InterfaceName))

	if current.Connected && targetMatches(current, t) {
		a.logger.info("当前仍关联目标 Wi-Fi，先尝试轻量 DNS/DHCP 修复。")
		if a.repairIPLayer(t) {
			a.logger.info("DNS/DHCP 修复后网络恢复。")
			return true
		}
	}

	if a.hasInternet() {
		a.logger.info("进入主动断开前检测到互联网已自行恢复，取消侵入式恢复。")
		return true
	}
	a.logger.warn("恢复层 2：主动断开并重新连接保存的 WLAN Profile。")
	if a.softReconnect(t) {
		if a.hasInternet() {
			return true
		}
		if a.repairIPLayer(t) {
			return true
		}
	}

	if a.hasInternet() {
		a.logger.info("进入网卡重启前检测到互联网已自行恢复，取消网卡重启。")
		return true
	}
	a.logger.warn("恢复层 3：重启无线网卡，然后强制连接保存的 WLAN Profile。")
	if a.restartAdapter(t) {
		// The adapter alias can change after driver updates; refresh what we can.
		refreshed := detectWifiNetsh()
		if t.InterfaceName == "" && refreshed.InterfaceName != "" {
			t.InterfaceName = refreshed.InterfaceName
		}
		if a.softReconnect(t) {
			// Give 802.1X / DHCP a little extra time after a full adapter restart.
			c := a.getConfig()
			select {
			case <-a.stopCh:
				return false
			case <-time.After(time.Duration(c.WifiStartupWaitSeconds) * time.Second):
			}
			if a.hasInternet() {
				return true
			}
			if a.repairIPLayer(t) {
				return true
			}
		}
	}

	c := a.getConfig()
	if c.EnableWlanServiceRestart {
		a.logger.warn("恢复层 4：启用高级兜底，重启 WLAN AutoConfig 服务。")
		if a.restartWlanService() {
			if a.softReconnect(t) {
				if a.hasInternet() {
					return true
				}
				if a.repairIPLayer(t) {
					return true
				}
			}
		}
	} else {
		a.logger.info("高级兜底 WlanSvc 重启未启用（默认关闭，避免干扰企业/802.1X 环境）。")
	}

	a.logger.err("本轮所有恢复层均未恢复互联网。")
	return false
}

func (a *App) manualConnectTarget() {
	t := a.resolveTarget(detectWifi())
	if t.ProfileName == "" && t.SSID == "" {
		a.setStatus(StateError, "没有已记住的 Wi-Fi 目标；请先正常连接一次。", true)
		return
	}
	a.setStatus(StateRepairing, "正在主动连接已记住的 Wi-Fi Profile...", true)
	a.repairMu.Lock()
	ok := a.softReconnect(t)
	a.repairMu.Unlock()
	if ok {
		if a.hasInternet() {
			a.setStatus(StateOnline, "已主动连接目标 Wi-Fi，互联网正常。", true)
		} else {
			a.setStatus(StateOffline, "已连接目标 Wi-Fi，但互联网仍不可用。", true)
		}
	} else {
		a.setStatus(StateOffline, "主动连接目标 Wi-Fi 失败，请查看日志/诊断。", true)
	}
	a.wake()
}

func (a *App) manualRememberTarget() {
	a.setStatus(StateChecking, "正在后台读取当前 Wi-Fi 并保存恢复目标…", false)
	info := detectWifi()
	if !info.Connected {
		a.setStatus(StateError, "当前没有连接 Wi-Fi，无法记住恢复目标。", true)
		return
	}
	t := a.resolveTarget(info)
	a.rememberTarget(t)
	a.setStatus(StateOnline, fmt.Sprintf("已记住恢复目标：Profile=%q，SSID=%q。", t.ProfileName, t.SSID), true)
}

func appendCommandReport(b *strings.Builder, title string, timeout time.Duration, name string, args ...string) {
	b.WriteString("\r\n===== " + title + " =====\r\n")
	out, err := runHiddenTimeout(timeout, name, args...)
	if err != nil {
		b.WriteString("ERROR: " + err.Error() + "\r\n")
	}
	b.Write(out)
	if len(out) == 0 {
		b.WriteString("(no output)\r\n")
	}
}

func (a *App) diagnosticsDir() string {
	return filepath.Join(a.dataDir, "diagnostics")
}

func (a *App) cleanupOldDiagnostics(now time.Time) {
	dir := a.diagnosticsDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	retention := a.getConfig().LogRetentionDays
	if retention <= 0 {
		retention = 30
	}
	cutoff := now.AddDate(0, 0, -retention)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), "diagnostics-") || !strings.HasSuffix(entry.Name(), ".txt") {
			continue
		}
		info, err := entry.Info()
		if err == nil && info.ModTime().Before(cutoff) {
			_ = os.Remove(filepath.Join(dir, entry.Name()))
		}
	}
}

func (a *App) generateDiagnosticReport() string {
	now := time.Now()
	dir := a.diagnosticsDir()
	_ = os.MkdirAll(dir, 0755)
	a.cleanupOldDiagnostics(now)
	name := "diagnostics-" + now.Format("20060102-150405") + ".txt"
	path := filepath.Join(dir, name)
	var b strings.Builder
	b.WriteString("WiFi Watchdog Diagnostics\r\n")
	b.WriteString("Version: " + appVersion + "\r\n")
	b.WriteString("Time: " + now.Format(time.RFC3339) + "\r\n")
	b.WriteString("Privacy note: this report can contain SSID/Profile names, private IP addresses, routes, and adapter details. Review before sharing publicly.\r\n")
	b.WriteString(fmt.Sprintf("Admin: %v\r\n", isAdmin()))
	info := detectWifi()
	b.WriteString(fmt.Sprintf("Native/merged Wi-Fi: %+v\r\n", info))
	b.WriteString(fmt.Sprintf("Remembered target: %+v\r\n", a.loadRememberedTarget()))
	cfgBytes, _ := json.MarshalIndent(a.getConfig(), "", "  ")
	b.WriteString("Config:\r\n" + string(cfgBytes) + "\r\n")
	appendCommandReport(&b, "Windows version", 10*time.Second, "cmd.exe", "/d", "/c", "ver")
	appendCommandReport(&b, "WlanSvc status", 10*time.Second, "sc.exe", "query", "WlanSvc")
	appendCommandReport(&b, "netsh wlan show interfaces", 20*time.Second, "netsh.exe", "wlan", "show", "interfaces")
	appendCommandReport(&b, "netsh wlan show profiles", 20*time.Second, "netsh.exe", "wlan", "show", "profiles")
	appendCommandReport(&b, "netsh wlan show drivers", 20*time.Second, "netsh.exe", "wlan", "show", "drivers")
	appendCommandReport(&b, "ipconfig /all", 30*time.Second, "ipconfig.exe", "/all")
	appendCommandReport(&b, "route print -4", 30*time.Second, "route.exe", "print", "-4")
	appendCommandReport(&b, "arp -a", 15*time.Second, "arp.exe", "-a")
	appendCommandReport(&b, "WinHTTP proxy", 15*time.Second, "netsh.exe", "winhttp", "show", "proxy")
	b.WriteString("\r\n===== v1.4 VPN/TUN-aware assessment =====\r\n")
	assessment := a.assessNetwork()
	ab, _ := json.MarshalIndent(assessment, "", "  ")
	b.WriteString(string(ab) + "\r\n")
	_ = atomicWriteFile(path, []byte(b.String()), 0644)
	a.logger.info("已生成诊断报告：" + path)
	return path
}
