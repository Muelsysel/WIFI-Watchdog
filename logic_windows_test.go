//go:build windows

package main

import (
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
		WiFi:     wifiInfo{Connected: true},
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
		WiFi:   wifiInfo{Connected: false},
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
		WiFi:     wifiInfo{Connected: true},
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
