//go:build windows

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestNormalizeConfigClampsValues(t *testing.T) {
	c := Config{
		NormalCheckIntervalMinutes:  -1,
		FailureCheckIntervalSeconds: 99999,
		FailureThreshold:            0,
		RepairRetryIntervalMinutes:  0,
		WifiDisableWaitSeconds:      0,
		WifiStartupWaitSeconds:      9999,
		ConnectionTimeoutSeconds:    0,
		ConnectRetryCount:           99,
		ConnectRetryDelaySeconds:    0,
		DHCPRenewWaitSeconds:        999,
		VPNLocalPort:                70000,
		LogRetentionDays:            0,
	}
	got := normalizeConfig(c)
	if got.NormalCheckIntervalMinutes != 1 {
		t.Fatalf("normal interval = %d", got.NormalCheckIntervalMinutes)
	}
	if got.FailureCheckIntervalSeconds != 3600 {
		t.Fatalf("failure interval = %d", got.FailureCheckIntervalSeconds)
	}
	if got.FailureThreshold != 1 {
		t.Fatalf("threshold = %d", got.FailureThreshold)
	}
	if got.RepairRetryIntervalMinutes != 1 {
		t.Fatalf("repair interval = %d", got.RepairRetryIntervalMinutes)
	}
	if got.VPNLocalPort != 65535 {
		t.Fatalf("vpn port = %d", got.VPNLocalPort)
	}
	if got.LogRetentionDays != 1 {
		t.Fatalf("log retention = %d", got.LogRetentionDays)
	}
}

func TestMergeTargetKeepsPrimaryIdentity(t *testing.T) {
	primary := RecoveryTarget{SSID: "campus", InterfaceName: "WLAN"}
	fallback := RecoveryTarget{ProfileName: "campus-8021x", SSID: "old", InterfaceGUID: "{ABC}"}
	got := mergeTarget(primary, fallback)
	if got.SSID != "campus" {
		t.Fatalf("SSID overwritten: %q", got.SSID)
	}
	if got.ProfileName != "campus-8021x" {
		t.Fatalf("profile not filled: %q", got.ProfileName)
	}
	if got.InterfaceGUID != "{ABC}" {
		t.Fatalf("guid not filled: %q", got.InterfaceGUID)
	}
}

func TestTargetMatchesUsesProfileBeforeSSID(t *testing.T) {
	info := wifiInfo{Connected: true, ProfileName: "profile-a", SSID: "same-ssid"}
	target := RecoveryTarget{ProfileName: "profile-b", SSID: "same-ssid"}
	if targetMatches(info, target) {
		t.Fatal("different profile names must not match merely because SSID matches")
	}
}

func TestPersistedRepairCooldownSurvivesReload(t *testing.T) {
	dir := t.TempDir()
	a := &App{
		dataDir: dir,
		logger:  newLogger(filepath.Join(dir, "logs-a"), 30),
	}
	a.recordAutoRepairAttempt()
	remaining := a.remainingAutoRepairCooldown(10 * time.Minute)
	if remaining <= 9*time.Minute || remaining > 10*time.Minute {
		t.Fatalf("unexpected remaining cooldown: %v", remaining)
	}

	// Simulate a new App instance after process restart reading the same state.json.
	b := &App{
		dataDir: dir,
		logger:  newLogger(filepath.Join(dir, "logs-b"), 30),
	}
	remaining2 := b.remainingAutoRepairCooldown(10 * time.Minute)
	if remaining2 <= 9*time.Minute || remaining2 > 10*time.Minute {
		t.Fatalf("cooldown was not persisted: %v", remaining2)
	}
}

func TestClassifyVPNProtectsHealthyWiFi(t *testing.T) {
	n := NetworkAssessment{
		System:   SystemProbeResult{Online: false},
		WiFi:     wifiInfo{Connected: true, InterfaceName: "WLAN"},
		VPN:      VPNStatus{Detected: true},
		Underlay: WiFiUnderlayStatus{StructuralHealthy: true, StrongFault: false},
	}
	classifyNetworkAssessment(&n)
	if !n.VPNProtected || n.ShouldRepairWiFi {
		t.Fatalf("expected VPN protection without Wi-Fi repair: %+v", n)
	}
}

func TestClassifyRepairsDisconnectedWiFiEvenWithVPN(t *testing.T) {
	n := NetworkAssessment{
		System: SystemProbeResult{Online: false},
		WiFi:   wifiInfo{Connected: false, InterfaceGUID: "{ABC}"},
		VPN:    VPNStatus{Detected: true},
	}
	classifyNetworkAssessment(&n)
	if !n.ShouldRepairWiFi || n.VPNProtected {
		t.Fatalf("expected disconnected Wi-Fi to be repairable: %+v", n)
	}
}

func TestClassifyCaptivePortalProtectsWiFi(t *testing.T) {
	n := NetworkAssessment{
		System:   SystemProbeResult{CaptiveSuspected: true},
		WiFi:     wifiInfo{Connected: true, InterfaceName: "WLAN"},
		Underlay: WiFiUnderlayStatus{StructuralHealthy: true},
	}
	classifyNetworkAssessment(&n)
	if !n.CaptiveProtected || n.ShouldRepairWiFi {
		t.Fatalf("expected captive portal protection: %+v", n)
	}
}

func TestDailyLoggerUsesDateAndPrunesOldFiles(t *testing.T) {
	dir := t.TempDir()
	logger := newLogger(dir, 2)

	old := filepath.Join(dir, "watchdog-2000-01-01.log")
	if err := os.WriteFile(old, []byte("old"), 0644); err != nil {
		t.Fatal(err)
	}
	logger.info("hello")

	if _, err := os.Stat(logger.currentPath()); err != nil {
		t.Fatalf("current daily log missing: %v", err)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatalf("old log should have been pruned, err=%v", err)
	}
}

func TestLoadConfigPreservesInvalidFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte("{not-json"), 0644); err != nil {
		t.Fatal(err)
	}
	got := loadConfig(path)
	if got.NormalCheckIntervalMinutes != defaultConfig().NormalCheckIntervalMinutes {
		t.Fatalf("did not fall back to defaults: %+v", got)
	}
	matches, err := filepath.Glob(path + ".invalid-*.json")
	if err != nil || len(matches) != 1 {
		t.Fatalf("invalid config backup missing: matches=%v err=%v", matches, err)
	}
}

func TestAtomicWriteFileReplacesExistingContent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	if err := os.WriteFile(path, []byte("old"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := atomicWriteFile(path, []byte("new"), 0644); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "new" {
		t.Fatalf("unexpected content: %q", string(got))
	}
}

func TestNetworkIdentityDoesNotUseAdapterAsNetworkProof(t *testing.T) {
	current := wifiInfo{Connected: true, SSID: "new-network", InterfaceGUID: "{ABC}"}
	remembered := RecoveryTarget{SSID: "old-network", ProfileName: "old-profile", InterfaceGUID: "{ABC}"}
	if sameNetworkIdentity(current, remembered) {
		t.Fatal("same adapter GUID must not prove same Wi-Fi network")
	}
	masked := wifiInfo{Connected: true, InterfaceGUID: "{ABC}"}
	if sameNetworkIdentity(masked, remembered) {
		t.Fatal("privacy-hidden SSID/Profile must not borrow a stale profile")
	}
}

func TestTargetMatchesFailsClosedWhenIdentityUnavailable(t *testing.T) {
	target := RecoveryTarget{SSID: "wanted", ProfileName: "wanted-profile"}
	if targetMatches(wifiInfo{Connected: true, InterfaceGUID: "{ABC}"}, target) {
		t.Fatal("unknown SSID/Profile cannot verify a target")
	}
	if targetMatches(wifiInfo{Connected: true, SSID: "other"}, target) {
		t.Fatal("connection to a different SSID cannot verify a target")
	}
	if !targetMatches(wifiInfo{Connected: true, ProfileName: "wanted-profile"}, target) {
		t.Fatal("matching saved WLAN profile should verify association")
	}
}

func TestResolveTargetDoesNotCrossWirelessAdapters(t *testing.T) {
	dir := t.TempDir()
	a := &App{dataDir: dir, logger: newLogger(filepath.Join(dir, "logs"), 30)}
	a.rememberTarget(RecoveryTarget{ProfileName: "old-profile", SSID: "old", InterfaceGUID: "{111}"})
	target := a.resolveTarget(wifiInfo{Connected: false, InterfaceGUID: "{222}"})
	if target.ProfileName != "" || target.SSID != "" {
		t.Fatalf("must not reconnect old profile on a different adapter: %+v", target)
	}
}

func TestProtectConfirmedWiFiUnderlayEvenWithoutVPN(t *testing.T) {
	n := NetworkAssessment{
		System:   SystemProbeResult{Online: false},
		WiFi:     wifiInfo{Connected: true, InterfaceName: "WLAN", SSID: "campus"},
		Underlay: WiFiUnderlayStatus{DirectProbeOK: true},
	}
	classifyNetworkAssessment(&n)
	if !n.UnderlayProtected || n.ShouldRepairWiFi {
		t.Fatalf("physical Wi-Fi direct reachability should protect adapter: %+v", n)
	}
}

func TestNoAdapterEvidenceDoesNotAuthorizeRepair(t *testing.T) {
	n := NetworkAssessment{System: SystemProbeResult{Online: false}}
	classifyNetworkAssessment(&n)
	if n.ShouldRepairWiFi {
		t.Fatalf("missing adapter is not evidence to restart it: %+v", n)
	}
}

func TestDailyLogIsCompactedToRecentTail(t *testing.T) {
	dir := t.TempDir()
	logger := newLogger(dir, 30)
	path := logger.currentPath()
	long := bytes.Repeat([]byte("line-abcdef\n"), maxDailyLogBytes/12+100)
	if err := os.WriteFile(path, long, 0644); err != nil {
		t.Fatal(err)
	}
	logger.info("new entry should survive compaction")
	stat, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if stat.Size() > maxDailyLogBytes {
		t.Fatalf("daily log not bounded: %d bytes", stat.Size())
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(b, []byte("new entry should survive compaction")) {
		t.Fatal("compaction lost newest entry")
	}
}

func TestDiagnosticCleanupIncludesHangAndCrash(t *testing.T) {
	dir := t.TempDir()
	a := &App{dataDir: dir, cfg: defaultConfig(), logger: newLogger(filepath.Join(dir, "logs"), 30)}
	diagnosticDir := a.diagnosticsDir()
	if err := os.MkdirAll(diagnosticDir, 0755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"hang-old.txt", "crash-old.txt", "diagnostics-old.txt"} {
		path := filepath.Join(diagnosticDir, name)
		if err := os.WriteFile(path, []byte("old"), 0644); err != nil {
			t.Fatal(err)
		}
		oldTime := time.Now().AddDate(0, 0, -90)
		if err := os.Chtimes(path, oldTime, oldTime); err != nil {
			t.Fatal(err)
		}
	}
	a.cleanupOldDiagnostics(time.Now())
	entries, err := os.ReadDir(diagnosticDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("old diagnostics remain: %v", entries)
	}
}

func TestCooldownStillAppliesWhenStateWriteFails(t *testing.T) {
	dir := t.TempDir()
	invalidDataDir := filepath.Join(dir, "not-a-directory")
	if err := os.WriteFile(invalidDataDir, []byte("blocked"), 0644); err != nil {
		t.Fatal(err)
	}
	a := &App{dataDir: invalidDataDir, logger: newLogger(filepath.Join(dir, "logs"), 30)}
	a.recordAutoRepairAttempt()
	remaining := a.remainingAutoRepairCooldown(10 * time.Minute)
	if remaining <= 9*time.Minute || remaining > 10*time.Minute {
		t.Fatalf("in-memory cooldown not retained after disk error: %v", remaining)
	}
}

func TestFutureDatedCooldownNeverExceedsConfiguredInterval(t *testing.T) {
	dir := t.TempDir()
	a := &App{dataDir: dir, logger: newLogger(filepath.Join(dir, "logs"), 30)}
	err := a.updatePersistentState(func(st *PersistentState) {
		st.LastAutoRepairAt = time.Now().Add(24 * time.Hour)
	})
	if err != nil {
		t.Fatal(err)
	}
	remaining := a.remainingAutoRepairCooldown(10 * time.Minute)
	if remaining > 10*time.Minute || remaining < 9*time.Minute {
		t.Fatalf("clock jump changed cooldown unexpectedly: %v", remaining)
	}
}

func TestWin11UnknownInterfaceAliasCannotTriggerReset(t *testing.T) {
	n := NetworkAssessment{
		System: SystemProbeResult{Online: false},
		WiFi:   wifiInfo{Connected: true, InterfaceGUID: "{ABC}"},
		Underlay: WiFiUnderlayStatus{StrongFault: true},
	}
	classifyNetworkAssessment(&n)
	if n.ShouldRepairWiFi {
		t.Fatalf("missing adapter alias must not authorize Wi-Fi reset: %+v", n)
	}
}
