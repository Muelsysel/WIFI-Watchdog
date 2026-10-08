//go:build windows

package main

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// The Mihomo controller is a privileged management API. Only GET requests to
// numeric loopback are permitted. Never copy the bearer secret into config.json,
// logs, diagnostic reports or URL query parameters.
const mihomoSecretEnv = "WIFI_WATCHDOG_MIHOMO_SECRET"

type MihomoStatus struct {
	Configured    bool
	Available     bool // validated Mihomo /version reply or authentication challenge
	Authenticated bool
	Unauthorized  bool
	Version       string
	Mode          string
	TunKnown      bool
	TunEnabled    bool
	MixedPort     int
	Detail        string
}

func mihomoClient(timeout time.Duration) *http.Client {
	tr := &http.Transport{
		Proxy: nil,
		DisableKeepAlives: true,
		DialContext: (&net.Dialer{Timeout: timeout}).DialContext,
		ResponseHeaderTimeout: timeout,
	}
	return &http.Client{
		Transport: tr,
		Timeout: timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func mihomoGET(ctx context.Context, client *http.Client, port int, path, secret string) (int, []byte, error) {
	// No caller-controlled hostname or path is accepted here.
	if port < 1 || port > 65535 || (path != "/version" && path != "/configs") {
		return 0, nil, errInvalidControllerEndpoint
	}
	endpoint := "http://127.0.0.1:" + strconv.Itoa(port) + path
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return 0, nil, err
	}
	if secret != "" {
		req.Header.Set("Authorization", "Bearer "+secret)
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 32*1024))
	return resp.StatusCode, body, err
}

type invalidControllerEndpoint struct{}
func (invalidControllerEndpoint) Error() string { return "invalid local Mihomo controller endpoint" }
var errInvalidControllerEndpoint error = invalidControllerEndpoint{}

func probeMihomoController(port int, secret string, timeout time.Duration) MihomoStatus {
	out := MihomoStatus{Configured: port > 0 && port <= 65535}
	if !out.Configured {
		out.Detail = "控制接口探测已关闭"
		return out
	}
	if timeout < 150*time.Millisecond {
		timeout = 150*time.Millisecond
	}
	if timeout > 4*time.Second {
		timeout = 4*time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*timeout)
	defer cancel()
	client := mihomoClient(timeout)
	code, body, err := mihomoGET(ctx, client, port, "/version", secret)
	if err != nil {
		// Clash Verge Rev may use an internal Windows named pipe instead of
		// binding 9097 to TCP. Treat this as UNKNOWN, not 'core down'.
		out.Detail = "TCP 控制端口不可访问（可能仅启用了命名管道），状态未知"
		return out
	}
	if code == http.StatusUnauthorized || code == http.StatusForbidden {
		out.Available = true
		out.Unauthorized = true
		out.Detail = "控制接口要求认证；请通过进程环境变量提供 Secret"
		return out
	}
	if code != http.StatusOK {
		out.Detail = "端口可达，但未确认是 Mihomo 控制接口"
		return out
	}
	var version struct {
		Version string `json:"version"`
	}
	if json.Unmarshal(body, &version) != nil || strings.TrimSpace(version.Version) == "" {
		out.Detail = "端口可达，但 /version 不是有效的 Mihomo 响应"
		return out
	}
	out.Available = true
	out.Authenticated = true
	out.Version = version.Version
	out.Detail = "Mihomo 控制接口正常"
	code, body, err = mihomoGET(ctx, client, port, "/configs", secret)
	if err != nil {
		out.Detail = "Mihomo 可访问，但读取运行配置超时"
		return out
	}
	if code == http.StatusUnauthorized || code == http.StatusForbidden {
		out.Unauthorized = true
		out.Authenticated = false
		out.Detail = "Mihomo 运行配置需要认证"
		return out
	}
	if code != http.StatusOK {
		out.Detail = "Mihomo 可访问，但无法读取当前运行配置"
		return out
	}
	var cfg struct {
		Mode      string `json:"mode"`
		MixedPort int    `json:"mixed-port"`
		Tun       *struct {
			Enable bool `json:"enable"`
		} `json:"tun"`
	}
	if json.Unmarshal(body, &cfg) != nil {
		out.Detail = "Mihomo 运行配置响应格式不符合预期"
		return out
	}
	switch strings.ToLower(cfg.Mode) {
	case "rule", "global", "direct":
		out.Mode = strings.ToLower(cfg.Mode)
	}
	if cfg.MixedPort > 0 && cfg.MixedPort <= 65535 {
		out.MixedPort = cfg.MixedPort
	}
	if cfg.Tun != nil {
		out.TunKnown = true
		out.TunEnabled = cfg.Tun.Enable
	}
	return out
}

// Unlike successful CONNECT/SOCKS negotiation, validated HTTPS through the
// mixed proxy proves useful upstream connectivity rather than only a tunnel.
func probeValidatedMixedProxy(port int, timeout time.Duration) bool {
	if port <= 0 || port > 65535 {
		return false
	}
	if timeout < 300*time.Millisecond {
		timeout = 300*time.Millisecond
	}
	if timeout > 5*time.Second {
		timeout = 5*time.Second
	}
	proxyURL := &url.URL{Scheme: "http", Host: net.JoinHostPort("127.0.0.1", strconv.Itoa(port))}
	tr := &http.Transport{
		Proxy: http.ProxyURL(proxyURL),
		DisableKeepAlives: true,
		TLSHandshakeTimeout: timeout,
		ResponseHeaderTimeout: timeout,
	}
	client := &http.Client{
		Transport: tr,
		Timeout: timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	defer tr.CloseIdleConnections()
	targets := []struct {
		url string
		valid func(int, string) bool
	}{
		{"https://www.gstatic.com/generate_204", func(code int, _ string) bool { return code == 204 }},
		{"https://www.microsoft.com/", func(code int, _ string) bool { return code >= 200 && code < 400 }},
	}
	// A bounded parallel probe is much faster than waiting sequentially for a
	// regionally blocked destination; never call it from the Win32 UI thread.
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	results := make(chan bool, len(targets))
	for _, t := range targets {
		t := t
		go func() {
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, t.url, nil)
			if err != nil { results <- false; return }
			res, err := client.Do(req)
			if err != nil { results <- false; return }
			defer res.Body.Close()
			b, _ := io.ReadAll(io.LimitReader(res.Body, 256))
			results <- t.valid(res.StatusCode, string(b))
		}()
	}
	for range targets {
		select {
		case valid := <-results:
			if valid { return true }
		case <-ctx.Done():
			return false
		}
	}
	return false
}

func probeConfiguredMihomo(port int, timeout time.Duration) MihomoStatus {
	return probeMihomoController(port, os.Getenv(mihomoSecretEnv), timeout)
}
