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
	Detail     string
}

type SystemProbeResult struct {
	Online             bool
	HTTPAttempted      int
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
	ProxyUpstreamOK bool
	ProxyProtocol   string
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
	WiFi             wifiInfo
	System           SystemProbeResult
	VPN              VPNStatus
	DeepChecked      bool
	Underlay         WiFiUnderlayStatus
	Online           bool
	ShouldRepairWiFi bool
	VPNProtected     bool
	CaptiveProtected bool
	Reason           string
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

func probeHTTP(ctx context.Context, client *http.Client, name, url string, validator func(int, string) bool) HTTPProbeDetail {
	out := HTTPProbeDetail{Name: name, URL: url}
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

func (a *App) systemInternetProbe() SystemProbeResult {
	c := a.getConfig()
	timeout := time.Duration(c.ConnectionTimeoutSeconds) * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), timeout+1500*time.Millisecond)
	defer cancel()
	client := newSystemHTTPClient(timeout)

	type target struct {
		name string
		url  string
		v    func(int, string) bool
	}
	targets := []target{
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

	ch := make(chan HTTPProbeDetail, len(targets))
	for _, t := range targets {
		t := t
		go func() { ch <- probeHTTP(ctx, client, t.name, t.url, t.v) }()
	}

	r := SystemProbeResult{HTTPAttempted: len(targets), Details: make([]HTTPProbeDetail, 0, len(targets))}
	plainUnexpected := false
	for range targets {
		d := <-ch
		r.Details = append(r.Details, d)
		if d.Reached {
			r.ReachedHTTP++
		}
		if d.Valid {
			r.ValidHTTP++
			r.Online = true
			// A single validated public response is already strong proof of usable
			// system Internet. Cancel slower endpoints (for example a blocked
			// regional target) instead of making every healthy check wait for them.
			cancel()
			return r
		}
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

func (a *App) logSystemProbe(r SystemProbeResult) {
	parts := make([]string, 0, len(r.Details))
	for _, d := range r.Details {
		state := "失败"
		if d.Valid {
			state = "成功"
		} else if d.Reached {
			state = "有响应但不符合预期"
		}
		parts = append(parts, fmt.Sprintf("%s=%s", d.Name, state))
	}
	a.logger.info(fmt.Sprintf("系统互联网探测：HTTP有效=%d/%d，TCP兜底=%d/%d，Online=%v；%s",
		r.ValidHTTP, r.HTTPAttempted, r.TCPFallbackSuccess, r.TCPAttempted, r.Online, strings.Join(parts, ", ")))
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
	if c.VPNLocalPort > 0 {
		p := probeLocalProxy(c.VPNLocalPort, 1200*time.Millisecond)
		if p.Listening {
			v.LocalPortOpen = true
			v.ProxyProtocol = p.Protocol
			v.ProxyUpstreamOK = p.Upstream
			msg := fmt.Sprintf("localhost:%d listening", c.VPNLocalPort)
			if p.Protocol != "" {
				msg += " protocol=" + p.Protocol
			}
			if p.Upstream {
				msg += " upstream=OK"
			}
			v.Signals = append(v.Signals, msg)
		}
	}
	v.AdapterHints = vpnAdapterHints(wifi.InterfaceName)
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
		n.ShouldRepairWiFi = true
		n.Reason = "系统无网且 Wi-Fi 未关联"
		return
	}
	if n.System.CaptiveSuspected && !n.Underlay.StrongFault {
		n.CaptiveProtected = true
		n.Reason = "疑似认证门户/受限网络，避免自动重启 Wi-Fi"
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
	// Heavy assessment is serialized so the monitor loop, settings page and
	// manual checks cannot stampede route/ARP/ping/HTTP probes.
	a.assessmentMu.Lock()
	defer a.assessmentMu.Unlock()

	n := NetworkAssessment{}

	// Fast path first: when Windows really has usable Internet, there is no need
	// to run netsh/route/ARP/underlay probes at all. This dramatically shortens
	// the common path and removes a major source of perceived UI "hangs".
	n.System = a.systemInternetProbe()
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
