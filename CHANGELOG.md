# Changelog

## v1.4.0

### UI / responsiveness
- Pin the native Win32 window creation and message pump to one OS thread with `runtime.LockOSThread`; Win32 message queues are thread-affine.
- Coalesce tray status update messages so background state changes cannot flood the UI queue.
- Keep the control center open after saving settings and make the current configuration immediately reusable.
- Move “remember current Wi-Fi” detection off the UI thread.
- Add settings for system probe timeout and log retention.

### Network assessment
- Run real system Internet reachability first and skip `netsh` / route / ARP / underlay probing entirely on the normal online path.
- Enter the expensive Wi-Fi/VPN underlay path only after system Internet is genuinely offline.
- Extract the network classification decision into a pure function and add regression tests for VPN and captive-portal protection.
- Re-check Internet immediately before invasive profile disconnect and adapter restart steps.

### Logs / diagnostics
- Replace size-based `watchdog.log` rotation with daily `logs/watchdog-YYYY-MM-DD.log` files.
- Keep 30 days by default; retention is configurable and old daily logs are deleted automatically.
- Migrate legacy v1.3 logs into the new `logs/` directory without deleting them.
- Store diagnostics in `diagnostics/` and age old reports using the configured retention window.
- Add a privacy reminder to generated diagnostic reports.

### Engineering
- Add daily-log retention and network-decision tests.
- Make the application version link-time overridable for tagged releases.

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
