//go:build windows

package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func fakeProbeTargets(serverURL string) []systemHTTPTarget {
	valid := func(code int, _ string) bool { return code == http.StatusNoContent }
	return []systemHTTPTarget{
		{name: "Quick-204", url: serverURL + "/ok", v: valid},
		{name: "Slow-204", url: serverURL + "/slow", v: valid},
		{name: "Redirected", url: serverURL + "/redirect", v: valid},
		{name: "Unavailable", url: serverURL + "/unavailable", v: valid},
	}
}

func TestFastProbeDoesNotReportUnverifiedAsFailures(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ok":
			w.WriteHeader(http.StatusNoContent)
		case "/slow", "/redirect", "/unavailable":
			select {
			case <-time.After(350 * time.Millisecond):
				w.WriteHeader(http.StatusServiceUnavailable)
			case <-r.Context().Done():
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	started := time.Now()
	res := executeHTTPProbes(ctx, cancel, newSystemHTTPClient(time.Second), fakeProbeTargets(server.URL), false)

	if !res.Online || res.ValidHTTP != 1 || res.HTTPAttempted != 4 || res.FullScan {
		t.Fatalf("unexpected fast probe state: %+v", res)
	}
	if len(res.Details) != 1 || res.Details[0].Name != "Quick-204" {
		t.Fatalf("fast probe must return one confirmed endpoint, rest unverified: %+v", res.Details)
	}
	if elapsed := time.Since(started); elapsed > 250*time.Millisecond {
		t.Fatalf("fast check should not await slower endpoints: %s", elapsed)
	}
	logged := formatSystemProbeLog(res)
	if !strings.Contains(logged, "未统计=3") || !strings.Contains(logged, "不代表失败") {
		t.Fatalf("log must explicitly distinguish skipped outcomes: %s", logged)
	}
	if strings.Contains(logged, "HTTP有效=1/4") {
		t.Fatalf("old misleading success ratio still present: %s", logged)
	}
}

func TestCompleteProbeRecordsAllTargetsAndStatusCodes(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ok":
			w.WriteHeader(http.StatusNoContent)
		case "/slow":
			time.Sleep(45 * time.Millisecond)
			w.WriteHeader(http.StatusNoContent)
		case "/redirect":
			w.Header().Set("Location", "/ok")
			w.WriteHeader(http.StatusFound)
		case "/unavailable":
			w.WriteHeader(http.StatusServiceUnavailable)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	res := executeHTTPProbes(ctx, cancel, newSystemHTTPClient(time.Second), fakeProbeTargets(server.URL), true)
	if !res.Online || !res.FullScan || res.HTTPAttempted != 4 || len(res.Details) != 4 || res.ValidHTTP != 2 {
		t.Fatalf("expected all four target results, only two valid: %+v", res)
	}
	statuses := []int{204, 204, 302, 503}
	for i, want := range statuses {
		if res.Details[i].StatusCode != want {
			t.Fatalf("target %d status %d, want %d", i, res.Details[i].StatusCode, want)
		}
		if res.Details[i].LatencyMs < 0 {
			t.Fatalf("negative elapsed duration: %+v", res.Details[i])
		}
	}
	if res.Details[1].LatencyMs < 20 {
		t.Fatalf("slow target timing missing: %+v", res.Details[1])
	}
	logged := formatSystemProbeLog(res)
	if !strings.Contains(logged, "完整模式") || !strings.Contains(logged, "HTTP已统计=4/4") ||
		!strings.Contains(logged, "HTTP=302") || !strings.Contains(logged, "HTTP=503") ||
		!strings.Contains(logged, "未统计=0") {
		t.Fatalf("complete probe log must show every individual outcome: %s", logged)
	}
}

func TestHTTPProbeCapturesFailureAndLatency(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(25 * time.Millisecond)
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	d := probeHTTP(ctx, newSystemHTTPClient(time.Second), "test", server.URL, func(code int, _ string) bool { return code == 204 })
	if !d.Reached || d.Valid || d.StatusCode != 502 || d.LatencyMs < 10 {
		t.Fatalf("HTTP error response must be recorded as reached but invalid with latency: %+v", d)
	}
	ctxCancelled, stop := context.WithCancel(context.Background())
	stop()
	d = probeHTTP(ctxCancelled, newSystemHTTPClient(time.Second), "cancelled", server.URL, func(int, string) bool { return true })
	if d.Reached || d.Valid || d.Detail == "" {
		t.Fatalf("cancelled request must not be reported as successful HTTP: %+v", d)
	}
}
