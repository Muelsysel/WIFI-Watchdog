//go:build windows

package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"math/bits"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Windows IP_UNICAST_IF. Microsoft documents this socket option as selecting
// the outgoing interface for IPv4 unicast traffic on multihomed systems.
const ipUnicastIf = 31

type HTTPProbeDetail struct {
	Name       string
	URL        string
	Reached    bool // a real HTTP response was received
	Valid      bool // response proves normal public internet for this target
	StatusCode int
	LatencyMs  int64 // total HTTP request time, including DNS/TLS
	Detail     string
}

type SystemProbeResult struct {
	Online             bool
	HTTPAttempted      int  // scheduled/configured targets (not all necessarily evaluated)
	FullScan           bool // true when every configured target was examined
	ValidHTTP          int
	ReachedHTTP        int
	TCPAttempted       int
	TCPFallbackSuccess int
	CaptiveSuspected   bool
	Details            []HTTPProbeDetail
}

type VPNStatus struct {
	Detected        bool
	LocalPortOpen   bool
	ProxyUpstreamOK bool // Validated public HTTPS over the mixed proxy
	ProxyTunnelOK   bool // CONNECT/SOCKS handshake (weaker evidence)
	ProxyProtocol   string
	EffectivePort   int
	Mihomo          MihomoStatus
	AdapterHints    []string
	RouteHints      []string
	Signals         []string
}

type WiFiUnderlayStatus struct {
	IPv4              net.IP
	InterfaceIndex    int
	Gateway           net.IP
	GatewayReachable  bool
	GatewayNeighbor   bool
	DirectProbeOK     bool
	StructuralHealthy bool
	StrongFault       bool
	Reason            string
}

type NetworkAssessment struct {
	WiFi              wifiInfo
	System            SystemProbeResult
	VPN               VPNStatus
	DeepChecked       bool
	Underlay          WiFiUnderlayStatus
	Online            bool
	ShouldRepairWiFi  bool
	VPNProtected      bool
	CaptiveProtected  bool
	UnderlayProtected bool
	Reason            string
}

func newSystemHTTPClient(timeout time.Duration) *http.Client {
	d := &net.Dialer{Timeout: timeout}
	tr := &http.Transport{
		Proxy:                 nil, // TUN routing still applies; avoid env-proxy ambiguity.
		DialContext:           d.DialContext,
		ForceAttemptHTTP2:     false,
		DisableKeepAlives:     true,
		TLSHandshakeTimeout:   timeout,
		ResponseHeaderTimeout: timeout,
	}
	return &http.Client{
		Transport: tr,
		Timeout:   timeout + time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func probeHTTP(ctx context.Context, client *http.Client, name, url string, validator func(int, string) bool) (out HTTPProbeDetail) {
	started := time.Now()
	defer func() { out.LatencyMs = time.Since(started).Milliseconds() }()
	out = HTTPProbeDetail{Name: name, URL: url}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		out.Detail = err.Error()
		return out
	}
	req.Header.Set("User-Agent", "WiFiWatchdog/"+appVersion)
	resp, err := client.Do(req)
	if err != nil {
		out.Detail = err.Error()
		return out
	}
	defer resp.Body.Close()
	out.Reached = true
	out.StatusCode = resp.StatusCode
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
	text := string(body)
	out.Valid = validator(resp.StatusCode, text)
	if out.Valid {
		out.Detail = fmt.Sprintf("HTTP %d", resp.StatusCode)
	} else {
		out.Detail = fmt.Sprintf("HTTP %d unexpected", resp.StatusCode)
	}
	return out
}

type systemHTTPTarget struct {
	name string
	url  string
	v    func(int, string) bool
}

type indexedHTTPProbe struct {
	index int
	probe HTTPProbeDetail
}

// executeHTTPProbes has two explicit modes. In fast mode the first valid
// response proves connectivity and the remaining results are UNVERIFIED, not
// failures. Full mode waits for the result of every scheduled endpoint.
// Both modes share the same bounded HTTP context and validation rules.
func executeHTTPProbes(ctx context.Context, cancel context.CancelFunc, client *http.Client, targets []systemHTTPTarget, full bool) SystemProbeResult {
	results := make(chan indexedHTTPProbe, len(targets))
	for i, t := range targets {
		i, t := i, t
		go func() {
			results <- indexedHTTPProbe{index: i, probe: probeHTTP(ctx, client, t.name, t.url, t.v)}
		}()
	}
	r := SystemProbeResult{HTTPAttempted: len(targets), FullScan: full, Details: make([]HTTPProbeDetail, 0, len(targets))}
	ordered := make([]HTTPProbeDetail, len(targets))
	seen := make([]bool, len(targets))
	for range targets {
		d := <-results
		ordered[d.index] = d.probe
		seen[d.index] = true
		if d.probe.Reached {
			r.ReachedHTTP++
		}
		if d.probe.Valid {
			r.ValidHTTP++
			r.Online = true
			if !full {
				cancel() // cancel slow endpoints, do NOT count them as failed
				break
			}
		}
	}
	for i, ok := range seen {
		if ok {
			r.Details = append(r.Details, ordered[i])
		}
	}
	return r
}

func (a *App) systemInternetProbe() SystemProbeResult {
	return a.systemInternetProbeMode(false)
}

func (a *App) systemInternetProbeMode(full bool) SystemProbeResult {
	c := a.getConfig()
	timeout := time.Duration(c.ConnectionTimeoutSeconds) * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), timeout+1500*time.Millisecond)
	defer cancel()
	client := newSystemHTTPClient(timeout)

	targets := []systemHTTPTarget{
		{
			name: "Microsoft-NCSI",
			url:  "http://www.msftconnecttest.com/connecttest.txt",
			v: func(code int, body string) bool {
				return code == 200 && strings.Contains(body, "Microsoft Connect Test")
			},
		},
		{
			name: "Google-204",
			url:  "http://www.gstatic.com/generate_204",
			v: func(code int, body string) bool {
				return code == 204
			},
		},
		{
			name: "Microsoft-HTTPS",
			url:  "https://www.microsoft.com/",
			v: func(code int, body string) bool {
				return code >= 200 && code < 400
			},
		},
		{
			name: "Baidu-HTTPS",
			url:  "https://www.baidu.com/",
			v: func(code int, body string) bool {
				return code >= 200 && code < 400
			},
		},
	}

	r := executeHTTPProbes(ctx, cancel, client, targets, full)
	if r.Online {
		return r
	}
	plainUnexpected := false
	for _, d := range r.Details {
		if d.Reached && !d.Valid && (d.Name == "Microsoft-NCSI" || d.Name == "Google-204") {
			plainUnexpected = true
		}
	}

	// If HTTP is filtered but raw public TCP is reachable, still prefer a
	// false-negative-avoiding "online/degraded" result over resetting Wi-Fi.
	tcpTargets := []string{"1.1.1.1:443", "223.5.5.5:53"}
	r.TCPAttempted = len(tcpTargets)
	tcpCh := make(chan bool, len(tcpTargets))
	for _, addr := range tcpTargets {
		addr := addr
		go func() {
			d := net.Dialer{Timeout: timeout}
			conn, err := d.Dial("tcp4", addr)
			if err == nil {
				_ = conn.Close()
				tcpCh <- true
				return
			}
			tcpCh <- false
		}()
	}
	for range tcpTargets {
		if <-tcpCh {
			r.TCPFallbackSuccess++
			r.Online = true
			return r
		}
	}
	if plainUnexpected {
		r.CaptiveSuspected = true
	}
	return r
}

func formatSystemProbeLog(r SystemProbeResult) string {
	mode := "快速模式（首个有效结果即结束）"
	if r.FullScan {
		mode = "完整模式（逐项核验全部目标）"
	}
	unverified := r.HTTPAttempted - len(r.Details)
	if unverified < 0 {
		unverified = 0
	}
	failed := 0
	unexpected := 0
	parts := make([]string, 0, len(r.Details))
	for _, d := range r.Details {
		state := "失败"
		if d.Valid {
			state = "成功"
		} else if d.Reached {
			state = "响应异常"
			unexpected++
		} else {
			failed++
		}
		detail := strings.ReplaceAll(strings.ReplaceAll(d.Detail, "\r", " "), "\n", " ")
		if len(detail) > 160 {
			detail = detail[:160] + "…"
		}
		parts = append(parts, fmt.Sprintf("%s=%s(HTTP=%d 耗时=%dms %s)",
			d.Name, state, d.StatusCode, d.LatencyMs, detail))
	}
	if unverified > 0 {
		parts = append(parts, fmt.Sprintf("另有%d项未统计（已提前结束，不代表失败）", unverified))
	}
	tcpStatus := "未执行"
	if r.TCPAttempted > 0 {
		tcpStatus = fmt.Sprintf("%d/%d", r.TCPFallbackSuccess, r.TCPAttempted)
	}
	return fmt.Sprintf("系统互联网探测[%s]：HTTP已统计=%d/%d，有效=%d，响应异常=%d，请求失败=%d，未统计=%d；TCP兜底=%s；Online=%t；%s",
		mode, len(r.Details), r.HTTPAttempted, r.ValidHTTP, unexpected, failed, unverified,
		tcpStatus, r.Online, strings.Join(parts, "；"))
}

func (a *App) logSystemProbe(r SystemProbeResult) {
	a.logger.info(formatSystemProbeLog(r))
}

func localPortOpen(port int, timeout time.Duration) bool {
	if port <= 0 || port > 65535 {
		return false
	}
	addrs := []string{
		net.JoinHostPort("127.0.0.1", strconv.Itoa(port)),
		net.JoinHostPort("::1", strconv.Itoa(port)),
	}
	for _, addr := range addrs {
		c, err := net.DialTimeout("tcp", addr, timeout)
		if err == nil {
			_ = c.Close()
			return true
		}
	}
	return false
}

type localProxyProbeResult struct {
	Listening bool
	Protocol  string
	Upstream  bool
}

func probeHTTPConnectProxy(port int, timeout time.Duration) localProxyProbeResult {
	r := localProxyProbeResult{}
	if port <= 0 || port > 65535 {
		return r
	}
	conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), timeout)
	if err != nil {
		return r
	}
	defer conn.Close()
	r.Listening = true
	_ = conn.SetDeadline(time.Now().Add(timeout))
	_, err = io.WriteString(conn, "CONNECT www.microsoft.com:443 HTTP/1.1\r\nHost: www.microsoft.com:443\r\nProxy-Connection: close\r\n\r\n")
	if err != nil {
		return r
	}
	line, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		return r
	}
	if strings.HasPrefix(line, "HTTP/") {
		r.Protocol = "HTTP-CONNECT"
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[1] == "200" {
			r.Upstream = true
		}
	}
	return r
}

func probeSOCKS5Proxy(port int, timeout time.Duration) localProxyProbeResult {
	r := localProxyProbeResult{}
	if port <= 0 || port > 65535 {
		return r
	}
	conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), timeout)
	if err != nil {
		return r
	}
	defer conn.Close()
	r.Listening = true
	_ = conn.SetDeadline(time.Now().Add(timeout))
	if _, err := conn.Write([]byte{0x05, 0x01, 0x00}); err != nil {
		return r
	}
	resp := make([]byte, 2)
	if _, err := io.ReadFull(conn, resp); err != nil || resp[0] != 0x05 || resp[1] != 0x00 {
		return r
	}
	r.Protocol = "SOCKS5"
	// CONNECT 1.1.1.1:443; a successful SOCKS reply proves the proxy core can
	// reach an external endpoint even if the TUN route itself is malfunctioning.
	req := []byte{0x05, 0x01, 0x00, 0x01, 0x01, 0x01, 0x01, 0x01, 0x01, 0xbb}
	if _, err := conn.Write(req); err != nil {
		return r
	}
	head := make([]byte, 4)
	if _, err := io.ReadFull(conn, head); err != nil || head[0] != 0x05 {
		return r
	}
	if head[1] == 0x00 {
		r.Upstream = true
	}
	return r
}

func probeLocalProxy(port int, timeout time.Duration) localProxyProbeResult {
	if port <= 0 || port > 65535 {
		return localProxyProbeResult{}
	}
	// mixed-port implementations generally accept HTTP CONNECT; try it first.
	h := probeHTTPConnectProxy(port, timeout)
	if h.Protocol != "" || h.Upstream {
		return h
	}
	s := probeSOCKS5Proxy(port, timeout)
	if s.Protocol != "" || s.Upstream {
		return s
	}
	// Keep the listening signal even if the port is an API/control port or uses
	// another protocol.
	return localProxyProbeResult{Listening: h.Listening || s.Listening || localPortOpen(port, timeout)}
}

var vpnNameRE = regexp.MustCompile(`(?i)(^|[^a-z])(tun|wintun|wireguard|mihomo|clash|sing[- ]?box|openvpn|tap[- ]?windows|tailscale|zerotier|vpn)([^a-z]|$)`)

func vpnAdapterHints(wifiAlias string) []string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var out []string
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || strings.EqualFold(strings.TrimSpace(iface.Name), strings.TrimSpace(wifiAlias)) {
			continue
		}
		if vpnNameRE.MatchString(iface.Name) {
			out = append(out, fmt.Sprintf("adapter:%s(ifIndex=%d)", iface.Name, iface.Index))
		}
	}
	return out
}

func vpnAdapterDescriptionHints(wifiAlias string) []string {
	script := `$ErrorActionPreference='SilentlyContinue'; Get-NetAdapter | Where-Object Status -eq 'Up' | ForEach-Object { "$($_.Name)$([char]9)$($_.InterfaceDescription)$([char]9)$($_.ifIndex)" }`
	out, err := powershellEncoded(script, 6*time.Second)
	if err != nil && len(out) == 0 {
		return nil
	}
	var hints []string
	for _, raw := range strings.Split(strings.ReplaceAll(string(out), "\r\n", "\n"), "\n") {
		fields := strings.Split(strings.TrimSpace(raw), "\t")
		if len(fields) < 2 {
			continue
		}
		name := strings.TrimSpace(fields[0])
		description := strings.TrimSpace(fields[1])
		if strings.EqualFold(name, strings.TrimSpace(wifiAlias)) {
			continue
		}
		if vpnNameRE.MatchString(name + " " + description) {
			ifIndex := ""
			if len(fields) >= 3 {
				ifIndex = strings.TrimSpace(fields[2])
			}
			hints = append(hints, fmt.Sprintf("adapter-description:%s/%s(ifIndex=%s)", name, description, ifIndex))
		}
	}
	return hints
}

func vpnRouteHints() []string {
	out, err := runHiddenTimeout(8*time.Second, "route.exe", "print", "-4")
	if err != nil && len(out) == 0 {
		return nil
	}
	var hints []string
	for _, raw := range strings.Split(strings.ReplaceAll(string(out), "\r\n", "\n"), "\n") {
		f := strings.Fields(raw)
		if len(f) < 5 {
			continue
		}
		// Common TUN split-default routes: 0.0.0.0/1 and 128.0.0.0/1.
		if (f[0] == "0.0.0.0" && f[1] == "128.0.0.0") || (f[0] == "128.0.0.0" && f[1] == "128.0.0.0") {
			hints = append(hints, "split-default:"+strings.Join(f[:minInt(len(f), 5)], "/"))
		}
	}
	return hints
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func (a *App) detectVPNStatus(wifi wifiInfo) VPNStatus {
	c := a.getConfig()
	if !c.EnableVPNAware {
		return VPNStatus{}
	}
	v := VPNStatus{}
	// 9097 can be configured in Clash Verge Rev but left unbound. An
	// unreachable TCP controller does NOT prove the core has stopped.
	v.Mihomo = probeConfiguredMihomo(c.MihomoControllerPort, 1200*time.Millisecond)
	if v.Mihomo.Available {
		v.Signals = append(v.Signals, "Mihomo controller present")
	}
	if v.Mihomo.Authenticated {
		desc := "Mihomo API authenticated"
		if v.Mihomo.Mode != "" {
			desc += " mode=" + v.Mihomo.Mode
		}
		if v.Mihomo.TunKnown {
			desc += fmt.Sprintf(" tun=%t", v.Mihomo.TunEnabled)
		}
		v.Signals = append(v.Signals, desc)
	}
	// The user's manual mixed-port setting wins. Otherwise, a confirmed
	// /configs response supplies the *actual* mixed port, e.g. 2026.
	port := c.VPNLocalPort
	if port == 0 {
		port = v.Mihomo.MixedPort
	}
	v.EffectivePort = port
	if port > 0 {
		p := probeLocalProxy(port, 1200*time.Millisecond)
		if p.Listening {
			v.LocalPortOpen = true
			v.ProxyProtocol = p.Protocol
			v.ProxyTunnelOK = p.Upstream
			if p.Protocol != "" {
				v.Signals = append(v.Signals, fmt.Sprintf("mixed-proxy localhost:%d protocol=%s tunnel=%t", port, p.Protocol, p.Upstream))
				// TLS-validated HTTPS is stronger evidence than a successful
				// CONNECT handshake, which may precede upstream failures.
				if p.Upstream {
					v.ProxyUpstreamOK = probeValidatedMixedProxy(port, 2500*time.Millisecond)
					if v.ProxyUpstreamOK {
						v.Signals = append(v.Signals, "mixed-proxy HTTPS validated")
					}
				}
			}
		}
	}
	v.AdapterHints = vpnAdapterHints(wifi.InterfaceName)
	if len(v.AdapterHints) == 0 {
		v.AdapterHints = vpnAdapterDescriptionHints(wifi.InterfaceName)
	}
	v.RouteHints = vpnRouteHints()
	v.Signals = append(v.Signals, v.AdapterHints...)
	v.Signals = append(v.Signals, v.RouteHints...)
	v.Detected = len(v.Signals) > 0
	return v
}

func wifiInterfaceByAlias(alias string) (*net.Interface, bool) {
	if strings.TrimSpace(alias) == "" {
		return nil, false
	}
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, false
	}
	for i := range ifaces {
		if strings.EqualFold(strings.TrimSpace(ifaces[i].Name), strings.TrimSpace(alias)) {
			return &ifaces[i], true
		}
	}
	return nil, false
}

func wifiDefaultGateway(ip net.IP) net.IP {
	ip4 := ip.To4()
	if ip4 == nil {
		return nil
	}
	out, err := runHiddenTimeout(8*time.Second, "route.exe", "print", "-4")
	if err != nil && len(out) == 0 {
		return nil
	}
	want := ip4.String()
	bestMetric := int(^uint(0) >> 1)
	var best net.IP
	for _, raw := range strings.Split(strings.ReplaceAll(string(out), "\r\n", "\n"), "\n") {
		f := strings.Fields(raw)
		if len(f) < 5 || f[0] != "0.0.0.0" || f[1] != "0.0.0.0" || f[3] != want {
			continue
		}
		gw := net.ParseIP(f[2])
		if gw == nil || gw.To4() == nil {
			continue
		}
		metric, _ := strconv.Atoi(f[4])
		if metric < bestMetric {
			bestMetric = metric
			best = gw.To4()
		}
	}
	return best
}

func gatewayNeighborPresent(gateway net.IP) bool {
	if gateway == nil {
		return false
	}
	out, _ := runHiddenTimeout(5*time.Second, "arp.exe", "-a")
	g := regexp.QuoteMeta(gateway.String())
	re := regexp.MustCompile(`(?im)^\s*` + g + `\s+([0-9a-f]{2}[-:]){5}[0-9a-f]{2}\s+`)
	return re.Match(out)
}

func gatewayPing(sourceIP, gateway net.IP) bool {
	if sourceIP == nil || gateway == nil {
		return false
	}
	_, err := runHiddenTimeout(4*time.Second, "ping.exe", "-4", "-n", "1", "-w", "1200", "-S", sourceIP.String(), gateway.String())
	return err == nil
}

func interfaceBoundTCPProbe(iface *net.Interface, localIP net.IP, timeout time.Duration) bool {
	if iface == nil || localIP == nil || localIP.To4() == nil || iface.Index <= 0 {
		return false
	}
	// Windows expects IP_UNICAST_IF's IF_INDEX in network byte order for IPv4.
	ifIndexNetworkOrder := int(bits.ReverseBytes32(uint32(iface.Index)))
	targets := []string{"1.1.1.1:443", "223.5.5.5:53"}
	for _, target := range targets {
		d := net.Dialer{
			Timeout:   timeout,
			LocalAddr: &net.TCPAddr{IP: localIP.To4()},
			Control: func(network, address string, c syscall.RawConn) error {
				var inner error
				if err := c.Control(func(fd uintptr) {
					inner = syscall.SetsockoptInt(syscall.Handle(fd), syscall.IPPROTO_IP, ipUnicastIf, ifIndexNetworkOrder)
				}); err != nil {
					return err
				}
				return inner
			},
		}
		conn, err := d.Dial("tcp4", target)
		if err == nil {
			_ = conn.Close()
			return true
		}
	}
	return false
}

func (a *App) assessWiFiUnderlay(wifi wifiInfo) WiFiUnderlayStatus {
	u := WiFiUnderlayStatus{}
	if !wifi.Connected {
		u.StrongFault = true
		u.Reason = "Wi-Fi 未关联"
		return u
	}
	if strings.TrimSpace(wifi.InterfaceName) == "" {
		// Windows 11 privacy restrictions or WLAN API issues can hide the
		// interface alias. Missing metadata is not evidence of link failure.
		u.Reason = "无线网卡已关联但接口别名未知，无法可靠检查物理网络"
		return u
	}
	ip, found := wifiIPv4ByAlias(wifi.InterfaceName)
	if !found || ip == nil {
		u.StrongFault = true
		u.Reason = "Wi-Fi 已关联但没有 IPv4 地址"
		return u
	}
	u.IPv4 = ip.To4()
	iface, ok := wifiInterfaceByAlias(wifi.InterfaceName)
	if ok {
		u.InterfaceIndex = iface.Index
	}
	u.Gateway = wifiDefaultGateway(u.IPv4)
	if u.Gateway == nil {
		// Some IPv6-only networks are legitimate. This is strong evidence only
		// for this app's IPv4-oriented campus use, but VPN protection will still
		// err on the non-invasive side if a VPN is detected.
		u.Reason = "Wi-Fi IPv4 存在但未找到 IPv4 默认网关"
		return u
	}

	// Ping is useful when supported. ARP-neighbor presence is the stronger local
	// L2 signal when the gateway drops ICMP Echo.
	u.GatewayReachable = gatewayPing(u.IPv4, u.Gateway)
	u.GatewayNeighbor = gatewayNeighborPresent(u.Gateway)
	if iface != nil {
		timeout := time.Duration(a.getConfig().ConnectionTimeoutSeconds) * time.Second
		u.DirectProbeOK = interfaceBoundTCPProbe(iface, u.IPv4, timeout)
	}
	u.StructuralHealthy = u.GatewayReachable || u.GatewayNeighbor || u.DirectProbeOK
	if u.StructuralHealthy {
		u.Reason = "Wi-Fi 已关联且本地三层链路存在"
	} else {
		u.StrongFault = true
		u.Reason = "Wi-Fi 网关无 ICMP/邻居响应且接口直连探测失败"
	}
	return u
}

func classifyNetworkAssessment(n *NetworkAssessment) {
	if n.System.Online {
		n.Online = true
		n.Reason = "系统互联网可用"
		return
	}
	if !n.WiFi.Connected {
		if n.WiFi.InterfaceGUID == "" && n.WiFi.InterfaceName == "" {
			n.Reason = "无法识别物理 Wi-Fi 接口，避免误操作其他网卡"
			return
		}
		n.ShouldRepairWiFi = true
		n.Reason = "系统无网且 Wi-Fi 未关联"
		return
	}
	if strings.TrimSpace(n.WiFi.InterfaceName) == "" {
		n.Reason = "Wi-Fi 已关联但物理接口别名不可确认，暂停自动断开/重启"
		return
	}
	if n.VPN.ProxyUpstreamOK {
		// HTTPS through Mihomo works; system TUN/routing/DNS checks are the
		// failing layer. Restarting the physical Wi-Fi is counterproductive.
		n.VPNProtected = true
		n.Reason = "Mihomo 混合代理 HTTPS 可用，但系统互联网探测失败；优先排查 TUN 路由/DNS，禁止重启 Wi-Fi"
		return
	}
	if n.Underlay.DirectProbeOK {
		// A successful probe explicitly bound to the physical Wi-Fi adapter
		// proves the underlay still has internet. A VPN, DNS or system-route
		// failure must not trigger a disruptive Wi-Fi reconnect.
		n.UnderlayProtected = true
		n.Reason = "系统探测异常，但绑定物理 Wi-Fi 的直连探测成功；保护无线连接"
		return
	}
	if n.System.CaptiveSuspected && !n.Underlay.StrongFault {
		n.CaptiveProtected = true
		n.Reason = "疑似认证门户/受限网络，避免自动重启 Wi-Fi"
		return
	}
	if n.VPN.Mihomo.Available && n.VPN.Mihomo.Unauthorized && !n.Underlay.StrongFault {
		n.VPNProtected = true
		n.Reason = "Mihomo 控制接口可达但鉴权失败；VPN 状态尚不能确认，保护 Wi-Fi"
		return
	}
	if n.VPN.Detected && !n.Underlay.StrongFault {
		n.VPNProtected = true
		if n.VPN.ProxyUpstreamOK {
			n.Reason = "系统互联网异常，但本地 VPN 代理上游可达；更像 TUN/系统路由问题，保护 Wi-Fi"
		} else {
			n.Reason = "系统互联网异常，但检测到 VPN/TUN 且 Wi-Fi 底层未发现强故障证据"
		}
		return
	}
	if n.VPN.Detected && n.Underlay.StrongFault && n.WiFi.Connected && n.WiFi.InterfaceName != "" {
		// A failed ICMP/ARP/bound TCP probe is not strong proof of broken Wi-Fi
		// when TUN and campus AP policies may block all three. Require a
		// structural failure such as no Wi-Fi IPv4 before invasive recovery.
		if n.Underlay.IPv4 != nil {
			n.VPNProtected = true
			n.Reason = "Mihomo/TUN 环境下 Wi-Fi 已关联且有 IPv4；深度探测失败可能受路由/防火墙影响，暂不重启网卡"
			return
		}
	}
	if n.VPN.Detected && n.Underlay.StrongFault {
		n.ShouldRepairWiFi = true
		n.Reason = "VPN/TUN 存在，但 Wi-Fi 底层存在强故障证据：" + n.Underlay.Reason
		return
	}

	// No VPN/TUN evidence: preserve the watchdog behavior. A confirmed system
	// internet failure is enough to allow recovery, but the multi-stage repair
	// still starts from the least invasive operation.
	n.ShouldRepairWiFi = true
	n.Reason = "系统互联网不可用且未检测到 VPN/TUN 保护条件"
}

func fastNativeWifiSnapshot() wifiInfo {
	info, err := detectWifiNative()
	if err != nil {
		return wifiInfo{}
	}
	return info
}

func (a *App) assessNetwork() NetworkAssessment {
	return a.assessNetworkMode(false)
}

// assessNetworkMode is used only for explicit user-initiated checks/reports
// when full=true. Regular monitoring always uses the fast early-success mode.
func (a *App) assessNetworkMode(full bool) NetworkAssessment {
	// Heavy assessment is serialized so the monitor loop, settings page and
	// manual checks cannot stampede route/ARP/ping/HTTP probes.
	a.assessmentMu.Lock()
	defer a.assessmentMu.Unlock()

	n := NetworkAssessment{}

	// Fast path first: when Windows really has usable Internet, there is no need
	// to run netsh/route/ARP/underlay probes at all. This dramatically shortens
	// the common path and removes a major source of perceived UI "hangs".
	n.System = a.systemInternetProbeMode(full)
	a.logSystemProbe(n.System)
	if n.System.Online {
		n.WiFi = fastNativeWifiSnapshot()
		classifyNetworkAssessment(&n)
		return n
	}

	// Only enter the expensive/deep path after the system-level Internet probe
	// has actually failed.
	n.DeepChecked = true
	n.WiFi = detectWifi()
	n.VPN = a.detectVPNStatus(n.WiFi)
	if n.VPN.Detected {
		a.logger.info("检测到 VPN/TUN 信号：" + strings.Join(n.VPN.Signals, "; "))
	}

	if n.WiFi.Connected {
		n.Underlay = a.assessWiFiUnderlay(n.WiFi)
	}
	classifyNetworkAssessment(&n)
	return n
}

// hasInternet is intentionally a *system* internet check in v1.3. It must not
// bind to Wi-Fi, because TUN/VPN software is allowed to be the system's actual
// egress path. Wi-Fi-specific health is assessed separately by assessNetwork.
func (a *App) hasInternet() bool {
	r := a.systemInternetProbe()
	a.logSystemProbe(r)
	return r.Online
}
