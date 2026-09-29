//go:build windows

package main

import (
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
		logger:  &Logger{path: filepath.Join(dir, "test.log")},
	}
	a.recordAutoRepairAttempt()
	remaining := a.remainingAutoRepairCooldown(10 * time.Minute)
	if remaining <= 9*time.Minute || remaining > 10*time.Minute {
		t.Fatalf("unexpected remaining cooldown: %v", remaining)
	}

	// Simulate a new App instance after process restart reading the same state.json.
	b := &App{
		dataDir: dir,
		logger:  &Logger{path: filepath.Join(dir, "test2.log")},
	}
	remaining2 := b.remainingAutoRepairCooldown(10 * time.Minute)
	if remaining2 <= 9*time.Minute || remaining2 > 10*time.Minute {
		t.Fatalf("cooldown was not persisted: %v", remaining2)
	}
}
