using System.Net;
using System.Net.NetworkInformation;
using System.Net.Sockets;

namespace WiFiWatchdog.Studio.Core;

// Information-only: neither querying these properties nor opening the Studio
// adapter page modifies routing, firewall, DNS, VPN or WLAN state.
public sealed record AdapterSnapshot(
    string Id, string Name, string Description, string Kind, string Status,
    string Speed, string IPv4, string IPv6, string Gateway, string Dns,
    bool MightBeVirtual)
{
    public bool IsUp => Status == "已连接";
}

public static class AdapterInspector
{
    public static IReadOnlyList<AdapterSnapshot> Collect()
    {
        var items = new List<AdapterSnapshot>();
        foreach (var nic in NetworkInterface.GetAllNetworkInterfaces())
        {
            if (nic.NetworkInterfaceType == NetworkInterfaceType.Loopback) continue;
            try
            {
                var p = nic.GetIPProperties();
                string Addresses(AddressFamily family) => string.Join(", ",
                    p.UnicastAddresses.Where(x => x.Address.AddressFamily == family)
                      .Select(x => x.Address.ToString()).Take(4));
                var gateway = string.Join(", ", p.GatewayAddresses
                    .Where(x => !x.Address.Equals(IPAddress.Any) && !x.Address.Equals(IPAddress.IPv6Any))
                    .Select(x => x.Address.ToString()).Take(3));
                var dns = string.Join(", ", p.DnsAddresses.Select(x => x.ToString()).Take(4));
                var hint = nic.Name + " " + nic.Description;
                bool virtualHint = new[] { "wintun", "wireguard", "mihomo", "clash", "tailscale",
                    "zerotier", "openvpn", "tap-windows", "vpn", "tun" }
                    .Any(x => hint.Contains(x, StringComparison.OrdinalIgnoreCase));
                var kind = nic.NetworkInterfaceType switch
                {
                    NetworkInterfaceType.Wireless80211 => "Wi-Fi",
                    NetworkInterfaceType.Ethernet => "以太网",
                    NetworkInterfaceType.Ppp => "PPP / VPN",
                    NetworkInterfaceType.Tunnel => "隧道接口",
                    _ => nic.NetworkInterfaceType.ToString()
                };
                items.Add(new(
                    nic.Id, nic.Name, nic.Description, kind,
                    nic.OperationalStatus == OperationalStatus.Up ? "已连接" : "未连接",
                    nic.Speed > 0 ? $"{nic.Speed / 1_000_000d:0.#} Mbps（报告链路速率）" : "未知",
                    Addresses(AddressFamily.InterNetwork), Addresses(AddressFamily.InterNetworkV6),
                    gateway, dns, virtualHint));
            }
            catch (Exception ex) when (ex is NetworkInformationException or SocketException
                                         or InvalidOperationException or ObjectDisposedException)
            {
                items.Add(new(nic.Id, nic.Name, nic.Description, nic.NetworkInterfaceType.ToString(),
                    "状态不可读取", "未知", "", "", "", "", false));
            }
        }
        return items.OrderByDescending(x => x.Kind == "Wi-Fi")
            .ThenByDescending(x => x.IsUp).ThenBy(x => x.Name).ToArray();
    }
}
