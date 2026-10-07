# WiFi Watchdog

Repository: `https://github.com/Muelsysel/WIFI-Watchdog`

WiFi Watchdog is a lightweight Windows tray utility for detecting and recovering broken Wi‑Fi connectivity, with special care for campus Wi‑Fi, 802.1X, VPN/TUN, multiple adapters, and unattended operation.

Version: **v1.4.0**

Key ideas:

- Separate **system Internet reachability** from **physical Wi‑Fi underlay health**.
- Do not reset Wi‑Fi just because a TUN/VPN route is broken.
- Remember the real Windows WLAN Profile, not only the SSID.
- Escalate recovery gradually: DNS/DHCP → profile reconnect → adapter restart → optional WlanSvc restart.
- Persist recovery cooldown across process restarts.
- Pin the Win32 window/message loop to one OS thread and keep slow work off the UI thread.
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
