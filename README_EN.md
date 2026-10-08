# WiFi Watchdog

Repository: `https://github.com/Muelsysel/WIFI-Watchdog`

WiFi Watchdog is a lightweight Windows tray utility for detecting and recovering broken Wi‑Fi connectivity, with special care for campus Wi‑Fi, 802.1X, VPN/TUN, multiple adapters, and unattended operation.

Candidate version: **v1.6.0-rc.1**

Key ideas:

- Separate **system Internet reachability** from **physical Wi‑Fi underlay health**.
- Do not reset Wi‑Fi just because a TUN/VPN route is broken.
- Remember the real Windows WLAN Profile, not only the SSID.
- Escalate recovery gradually: DNS/DHCP → profile reconnect → adapter restart → optional WlanSvc restart.
- Persist recovery cooldown across process restarts.
- Run the tray and settings window on separate locked Win32 OS UI threads, keeping slow work off both UI loops.
- Use a fast system-Internet path before deep WLAN/VPN/route probing.
- Write daily logs with configurable retention (30 days by default).

See the Chinese [README](README.md) for full documentation, or [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) for implementation details.

## Build

Go 1.23+:

```powershell
go test ./...
go vet ./...
go build -trimpath -ldflags "-H=windowsgui -s -w" -o WiFiWatchdog.exe .
```

## Privacy

No telemetry, no cloud upload, and no Wi‑Fi/802.1X credential collection. Daily logs and diagnostic reports remain local unless the user explicitly shares them.

## License

MIT.

## Windows 11 hardening candidate

This release candidate favors safe recovery over disruptive false positives. A WLAN profile/SSID mismatch or ambiguous adapter identity stops automated reconnect. Physical Wi-Fi reachability protects the interface even when the system-level probe fails. Settings save no longer performs synchronous disk logging.

Daily logs are compacted above 16 MiB, keeping the newest ~8 MiB. Hang and crash diagnostics also follow retention rules. CI success does not prove that sporadic Windows 11 UI hangs are resolved; reproduction testing on actual hardware remains necessary.

## Clash Verge Rev / Mihomo TUN integration

The watchdog optionally inspects `127.0.0.1:9097` using read-only `GET /version` and `GET /configs`. The control center now exposes the controller port and the mixed proxy port. Use 2026 for your mixed port if it cannot be discovered from the authenticated controller.

An unreachable controller is **not evidence of a dead core** (Clash Verge Rev may communicate over a Windows named pipe). We validate public HTTPS through the mixed proxy rather than assuming that a successful CONNECT handshake proves online status. API authorization uses an optional process environment variable `WIFI_WATCHDOG_MIHOMO_SECRET`; credentials never appear in JSON config or diagnostics. No automated Mihomo restart, mode modification, or proxy switching is performed.


## v1.6.0-rc.1 Fluent Lite UI

The control center uses a lightweight native Win32/GDI dashboard with a dark sidebar, light status cards and four sections (Overview, Check Policy, Wi-Fi Recovery, VPN/System). No WebView2, Electron, asset bundle or third-party GUI library is loaded. It retains keyboard-focusable native controls and stores settings in the same configuration file.

The overview reports process working set and the current Go live heap separately. GDI fonts/brushes/pens are shared across window openings, and the window does not run animation or periodic repaint timers. The background runtime uses GC percentage 70 and a 96 MiB **soft Go heap target** unless `GOGC`/`GOMEMLIMIT` are set; this is not a hard Windows working-set cap.

Verify layout, DPI scaling and idle/after-close memory behavior on actual Windows 11 hardware; automated build success is not a substitute.
