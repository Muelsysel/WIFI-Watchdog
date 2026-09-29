# Changelog

## v1.3.0

### UI / responsiveness
- Fixed settings-window freezes caused by synchronous `schtasks.exe` calls on the Win32 UI thread.
- Startup-task query, startup-task mutation, network refresh, and diagnostics now run asynchronously.
- Added a control-center style settings window with live system/Wi‑Fi/VPN summary.
- Added background save state, refresh button, diagnostics shortcut, restore-defaults button, and high-DPI awareness.
- Serialized expensive network assessments so monitor/manual/UI probes do not stampede the system.

### Recovery safety
- Persist automatic recovery cooldown to `state.json` so restarting the app cannot bypass the configured minimum repair interval.
- Added timeouts to localized `netsh` detection calls.
- Preserved VPN/TUN-aware protection and Native WLAN Profile recovery from v1.2.

### Open-source readiness
- Added MIT license, contribution/security/privacy documentation, issue templates, CI, release workflow, tests, and reproducible build scripts.
- Added Windows amd64 and arm64 release build support.

## v1.2.0
- Added VPN/TUN-aware system Internet vs Wi‑Fi underlay separation.
- Added local VPN port detection and HTTP CONNECT / SOCKS5 upstream checks.
- Added captive portal protection and interface-bound probes.

## v1.1.0
- Added Native Wi‑Fi API, persistent Profile/SSID recovery target, DHCP/DNS repair, adapter restart, and diagnostics.

## v1.0.0
- Initial tray watchdog implementation.
