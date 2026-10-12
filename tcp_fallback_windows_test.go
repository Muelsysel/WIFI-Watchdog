//go:build windows

package main

import (
	"net"
	"strings"
	"testing"
)

func TestRawTCPHandshakeCannotProveHTTPInternet(t *testing.T) {
	r := SystemProbeResult{
		HTTPAttempted: 4,
		ValidHTTP:     0,
		Details: []HTTPProbeDetail{
			{Name: "Microsoft-NCSI", Detail: "timeout"},
			{Name: "Google-204", Detail: "timeout"},
			{Name: "Microsoft-HTTPS", Detail: "TLS handshake timeout"},
			{Name: "Baidu-HTTPS", Detail: "TLS handshake timeout"},
		},
	}
	// Reproduce user's real trace: 4/4 HTTP fail, 1/2 TCP handshakes work.
	applyTCPHandshakeResults(&r, []bool{true, false})
	if r.Online {
		t.Fatal("one TCP SYN/ACK was incorrectly treated as working Internet")
	}
	if !r.TCPReachable || r.TCPAttempted != 2 || r.TCPFallbackSuccess != 1 {
		t.Fatalf("lost TCP-only diagnostic evidence: %+v", r)
	}
	logText := formatSystemProbeLog(r)
	if !strings.Contains(logText, "Online=false") ||
		!strings.Contains(logText, "仅TCP握手可达") ||
		!strings.Contains(logText, "TCP握手=1/2") {
		t.Fatalf("log must not call TCP-only signal 'Internet normal': %s", logText)
	}
}

func TestTCPFallbackCountsAllEndpointsWithoutEarlyExit(t *testing.T) {
	r := SystemProbeResult{HTTPAttempted: 4}
	applyTCPHandshakeResults(&r, []bool{false, true})
	if r.Online || !r.TCPReachable || r.TCPFallbackSuccess != 1 {
		t.Fatalf("all TCP results must be counted without declaring online: %+v", r)
	}
	applyTCPHandshakeResults(&r, []bool{false, false})
	if r.Online || r.TCPReachable || r.TCPFallbackSuccess != 0 || r.TCPAttempted != 2 {
		t.Fatalf("zero TCP handshakes must not claim connectivity: %+v", r)
	}
}

func TestTCPOnlyWithAssociatedWiFiEntersProtectedOfflineFlow(t *testing.T) {
	n := NetworkAssessment{
		System: SystemProbeResult{HTTPAttempted: 4, TCPAttempted: 2,
			TCPFallbackSuccess: 1, TCPReachable: true},
		WiFi: wifiInfo{Connected: true, SSID: "ZZU-WLAN",
			InterfaceName: "WLAN"},
		Underlay: WiFiUnderlayStatus{
			IPv4: net.IPv4(192, 168, 20, 4),
		},
	}
	classifyNetworkAssessment(&n)
	if n.Online || !n.UnderlayProtected || n.ShouldRepairWiFi {
		t.Fatalf("TCP-only signal should notify offline but protect associated Wi-Fi: %+v", n)
	}
	if !strings.Contains(n.Reason, "HTTP") || !strings.Contains(n.Reason, "TCP") {
		t.Fatalf("explain degraded connectivity: %s", n.Reason)
	}
}

func TestTCPOnlyWithLostWiFiStillAllowsProfileRecovery(t *testing.T) {
	n := NetworkAssessment{
		System: SystemProbeResult{HTTPAttempted: 4, TCPReachable: true},
		WiFi:   wifiInfo{Connected: false, InterfaceGUID: "{ABC}"},
	}
	classifyNetworkAssessment(&n)
	if n.Online || !n.ShouldRepairWiFi || n.UnderlayProtected {
		t.Fatalf("verified WLAN association loss must remain repairable: %+v", n)
	}
}

func TestTCPOnlyWithoutIPv4AllowsRepairWhenVPNUnderlayBroken(t *testing.T) {
	n := NetworkAssessment{
		System: SystemProbeResult{HTTPAttempted: 4, TCPReachable: true},
		WiFi:   wifiInfo{Connected: true, InterfaceName: "WLAN"},
		VPN:    VPNStatus{Detected: true},
		Underlay: WiFiUnderlayStatus{
			StrongFault: true,
			Reason:      "no IPv4",
		},
	}
	classifyNetworkAssessment(&n)
	if n.Online || !n.ShouldRepairWiFi || n.UnderlayProtected {
		t.Fatalf("missing WLAN IPv4 is stronger than remote TCP hints: %+v", n)
	}
}

func TestVerifiedHTTPStillWinsOverWeakTCPMetadata(t *testing.T) {
	n := NetworkAssessment{
		System: SystemProbeResult{Online: true, ValidHTTP: 1,
			TCPReachable: true, TCPFallbackSuccess: 1},
		WiFi: wifiInfo{Connected: true, InterfaceName: "WLAN"},
	}
	classifyNetworkAssessment(&n)
	if !n.Online || n.ShouldRepairWiFi {
		t.Fatalf("working HTTP must still indicate healthy Internet: %+v", n)
	}
}
