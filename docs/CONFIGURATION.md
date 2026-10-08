# Configuration

The control center writes configuration to:

```text
%LOCALAPPDATA%\WiFiWatchdog\config.json
```

The application starts from built-in defaults, overlays fields found in the JSON file, then clamps numeric values to safe ranges. This means upgrading from an older version automatically receives defaults for newly introduced fields.

A machine-readable schema is available at [config.schema.json](../config.schema.json).

## Monitoring

| Field | Default | Range | Meaning |
| --- | ---: | ---: | --- |
| `normalCheckIntervalMinutes` | 10 | 1–1440 | Normal online check interval |
| `failureCheckIntervalSeconds` | 10 | 1–3600 | Fast retry interval after the first failure |
| `failureThreshold` | 5 | 1–100 | Consecutive failures before recovery is considered |
| `repairRetryIntervalMinutes` | 10 | 1–1440 | Minimum interval between automatic recovery rounds |
| `connectionTimeoutSeconds` | 4 | 1–30 | Per-probe timeout |

## Wi-Fi recovery

| Field | Default | Range | Meaning |
| --- | ---: | ---: | --- |
| `wifiDisableWaitSeconds` | 3 | 1–120 | Time between adapter disable and re-enable |
| `wifiStartupWaitSeconds` | 15 | 1–600 | Post-restart association / 802.1X settling time |
| `connectRetryCount` | 3 | 1–10 | Profile reconnect attempts |
| `connectRetryDelaySeconds` | 6 | 1–60 | Association verification window per attempt |
| `dhcpRenewWaitSeconds` | 8 | 1–120 | Wait after DHCP renew |
| `autoReconnectDisconnected` | false | — | Reconnect remembered Profile when Wi-Fi association disappears |
| `enableWlanServiceRestart` | false | — | Last-resort WlanSvc restart; intentionally opt-in |

## VPN/TUN protection

| Field | Default | Meaning |
| --- | ---: | --- |
| `enableVpnAware` | true | Raise the evidence threshold before modifying Wi-Fi when VPN/TUN signals exist |
| `vpnLocalPort` | 0 | Explicit local HTTP CONNECT / SOCKS5 mixed port; `0` lets the Mihomo controller supply the actual mixed-port if authenticated |
| `mihomoControllerPort` | 9097 | Read-only controller API on **127.0.0.1 only**; `0` disables it |

Existing installations keep their saved port. For example, a user who previously configured port `2026` keeps `2026` after upgrading.

## Logs

| Field | Default | Range | Meaning |
| --- | ---: | ---: | --- |
| `logRetentionDays` | 30 | 1–3650 | Number of daily logs and old diagnostic reports retained |

Runtime logs are stored as:

```text
%LOCALAPPDATA%\WiFiWatchdog\logs\watchdog-YYYY-MM-DD.log
```

## Startup

`startWithWindows` mirrors the highest-privilege Task Scheduler entry managed by the control center.

Do not hand-edit the file while the control center is saving settings. Writes use a temporary file plus Windows replace semantics to reduce corruption risk.

## Recovery safety and disk bounds (v1.5 candidate)

Saved WLAN profiles are reused only when the adapter identity matches. A connected interface with an unknown or mismatched SSID/Profile is not considered proof of successful recovery. If the physical Wi-Fi interface itself can connect to a public endpoint, automatic adapter reset is suppressed even if a system-level TUN/DNS/proxy probe fails.

Each date has one `watchdog-YYYY-MM-DD.log`. At approximately 16 MiB it is compacted to preserve the latest ~8 MiB (best effort; a file held open by another Windows process may block replacement). The retention policy also removes old `hang-*.txt` and `crash-*.txt` diagnostic reports. These files may contain personal network identifiers; inspect them before sharing.


### Clash Verge Rev + Mihomo TUN example

The following values represent one actual setup (not global defaults):

```json
{
  "enableVpnAware": true,
  "vpnLocalPort": 2026,
  "mihomoControllerPort": 9097
}
```

Keep all other keys from your existing `config.json` when editing manually. Prefer setting the two port values through the control center instead.

The watchdog reads only `GET http://127.0.0.1:9097/version` and `GET /configs` to inspect current mode (`rule`/`global`/`direct`), `tun.enable`, and `mixed-port`. It will **never** PATCH the mode, switch nodes, restart Mihomo, or delete connections. It also tests validated public HTTPS through the mixed port; a successful HTTP CONNECT / SOCKS5 handshake alone is not treated as proof of working Internet.

**Important:** some Clash Verge Rev versions display `127.0.0.1:9097` in their configuration while the core is accessible only through an internal Windows named pipe. When the controller port is closed, watchdog classifies the controller as *unknown*, not *stopped*. Set the explicit mixed port to `2026` in this case so proxy probing still works.

**Controller Secret:** the API password is **never stored in the repository, JSON config, logs or diagnostic reports**. An optional process environment variable named `WIFI_WATCHDOG_MIHOMO_SECRET` supplies the Bearer token. Its value is read at runtime and sent only to local loopback in an HTTP Authorization header. If omitted/incorrect, the app reports authentication required and still uses mixed-proxy/WLAN evidence. Do not paste the token into bug reports or terminal transcripts. Prefer changing any shared/default controller Secret to a strong unique token inside Clash Verge Rev.

### Conservative repair policy

- **System Internet works:** no Wi-Fi repair; skip heavy VPN diagnostics.
- **Mihomo mixed proxy can validate HTTPS but system TUN cannot:** suspect DNS/TUN routes, protect Wi-Fi.
- **Mihomo API is unauthorized/unbound:** that does not prove the core is down. Assess physical WLAN and mixed proxy separately.
- **Mihomo/TUN present and Wi-Fi still associated with IPv4:** failed ICMP/ARP/public probes can be routing artifacts; avoid disruptive adapter resets.
- **Wi-Fi genuinely disconnected:** allow saved-profile reconnect on the verified wireless interface, respecting the 10-minute recovery cooldown.

The program intentionally does **not** restart Clash Verge Rev, toggle TUN, or rotate proxy nodes without explicit user action. This avoids interfering with campus Wi-Fi authentication and potentially sensitive VPN sessions.
