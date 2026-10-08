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
| `vpnLocalPort` | 0 | Optional local HTTP CONNECT / SOCKS5 / mixed proxy port. `0` disables port probing; adapter/route detection still works |

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
