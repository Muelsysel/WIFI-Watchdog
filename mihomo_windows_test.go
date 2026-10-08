//go:build windows

package main

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

func testLocalController(t *testing.T, handler http.Handler) (int, func()) {
	t.Helper()
	server := httptest.NewServer(handler)
	host, portText, err := net.SplitHostPort(strings.TrimPrefix(server.URL, "http://"))
	if err != nil {
		server.Close()
		t.Fatal(err)
	}
	if host != "127.0.0.1" {
		server.Close()
		t.Fatalf("unexpected test listener: %s", host)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		server.Close()
		t.Fatal(err)
	}
	return port, server.Close
}

func TestMihomoControllerReadsRuleTunAndMixedPort(t *testing.T) {
	const testSecret = "unit-test-token-not-a-real-secret"
	port, cleanup := testLocalController(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+testSecret {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if r.Method != http.MethodGet {
			http.Error(w, "read-only API required", http.StatusMethodNotAllowed)
			return
		}
		switch r.URL.Path {
		case "/version":
			_, _ = fmt.Fprint(w, `{"meta":true,"version":"v1-test"}`)
		case "/configs":
			_, _ = fmt.Fprint(w, `{"mode":"rule","mixed-port":2026,"tun":{"enable":true}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer cleanup()
	s := probeMihomoController(port, testSecret, 1*time.Second)
	if !s.Available || !s.Authenticated || s.Unauthorized ||
		s.Mode != "rule" || s.MixedPort != 2026 || !s.TunKnown || !s.TunEnabled || s.Version != "v1-test" {
		t.Fatalf("unexpected controller status: %+v", s)
	}
	if strings.Contains(fmt.Sprintf("%+v", s), testSecret) {
		t.Fatal("controller status leaked authentication secret")
	}
}

func TestMihomoUnauthorizedCannotBeMistakenForCoreDown(t *testing.T) {
	port, cleanup := testLocalController(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}))
	defer cleanup()
	s := probeMihomoController(port, "", 1*time.Second)
	if !s.Available || !s.Unauthorized || s.Authenticated {
		t.Fatalf("must classify 401 as available but unauthorized: %+v", s)
	}
}

func TestMihomoUnboundPortIsUnknownNotCoreStopped(t *testing.T) {
	// Take a port returned by a listener that has already been closed.
	l, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil { t.Fatal(err) }
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()
	s := probeMihomoController(port, "", 300*time.Millisecond)
	if s.Available || s.Authenticated || s.Unauthorized {
		t.Fatalf("a closed controller socket cannot prove Mihomo stopped: %+v", s)
	}
}

func TestMihomoRejectsUnrecognizedServiceOnPort(t *testing.T) {
	port, cleanup := testLocalController(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, "some other service")
	}))
	defer cleanup()
	s := probeMihomoController(port, "", time.Second)
	if s.Available || s.Authenticated {
		t.Fatalf("unrelated service must not be identified as Mihomo: %+v", s)
	}
}

func TestMihomoControllerDoesNotFollowRedirects(t *testing.T) {
	var followed bool
	port, cleanup := testLocalController(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/version" {
			w.Header().Set("Location", "/unexpected")
			w.WriteHeader(http.StatusFound)
			return
		}
		followed = true
	}))
	defer cleanup()
	s := probeMihomoController(port, "", time.Second)
	if followed || s.Available {
		t.Fatalf("control API must not follow redirects: %+v", s)
	}
}

func TestProxyCONNECTAloneDoesNotProveHTTPSInternet(t *testing.T) {
	// A fake proxy can return '200 Connection established' without being able
	// to forward TLS packets. The monitor must not count this as working VPN.
	port, cleanup := testLocalController(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodConnect {
			w.WriteHeader(http.StatusOK)
			return
		}
		http.Error(w, "blocked", http.StatusBadGateway)
	}))
	defer cleanup()
	if probeValidatedMixedProxy(port, 500*time.Millisecond) {
		t.Fatal("HTTP CONNECT alone must not be treated as validated HTTPS")
	}
}

func TestProxyHTTPSHealthProtectsWiFiEvenWithUnderlayFailures(t *testing.T) {
	n := NetworkAssessment{
		System: SystemProbeResult{Online: false},
		WiFi: wifiInfo{Connected: true, InterfaceName: "WLAN"},
		VPN: VPNStatus{Detected: true, ProxyUpstreamOK: true},
		Underlay: WiFiUnderlayStatus{StrongFault: true, IPv4: net.IPv4(192, 168, 1, 42)},
	}
	classifyNetworkAssessment(&n)
	if !n.VPNProtected || n.ShouldRepairWiFi {
		t.Fatalf("working Mihomo HTTPS must prevent destructive WLAN resets: %+v", n)
	}
}

func TestMihomoTCPAuthFailureProtectsHealthyAssociatedWiFi(t *testing.T) {
	n := NetworkAssessment{
		System: SystemProbeResult{Online: false},
		WiFi: wifiInfo{Connected: true, InterfaceName: "WLAN"},
		VPN: VPNStatus{Detected: true, Mihomo: MihomoStatus{Available: true, Unauthorized: true}},
		Underlay: WiFiUnderlayStatus{StructuralHealthy: true},
	}
	classifyNetworkAssessment(&n)
	if !n.VPNProtected || n.ShouldRepairWiFi {
		t.Fatalf("controller auth failure is not a reason to restart WLAN: %+v", n)
	}
}

func TestControllerPortConfigMigrationAndValidation(t *testing.T) {
	def := defaultConfig()
	if def.MihomoControllerPort != 9097 || def.VPNLocalPort != 0 {
		t.Fatalf("controller should default to 9097, proxy port should be discovered: %+v", def)
	}
	c := normalizeConfig(Config{MihomoControllerPort: 99999})
	if c.MihomoControllerPort != 65535 {
		t.Fatalf("controller port not clamped: %d", c.MihomoControllerPort)
	}
}
