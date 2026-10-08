# Architecture

## Layers

### 1. Native Win32 frontend

`main_windows.go`

- hidden message window + system tray;
- control-center/settings window;
- the entire Win32 window lifecycle and message loop are pinned to one OS thread with `runtime.LockOSThread`;
- all slow work is dispatched to goroutines;
- results return to the UI thread through custom `WM_APP` messages;
- UI thread never waits on `schtasks`, network probes, diagnostics, or recovery commands.

### 2. Monitoring state machine

The monitor distinguishes:

- system online;
- Wi‑Fi disconnected;
- fast confirmation;
- VPN protected;
- captive portal protected;
- repair allowed;
- repair cooldown.

Automatic repair requires repeated failure confirmation and a positive safety decision.

### 3. Network assessment

`network_windows.go`

The common online path starts with system Internet reachability. Deep WLAN/netsh/route/ARP work is skipped entirely when the system is already online.

When the system is offline, three independent views are combined:

1. **System Internet** — real HTTP/HTTPS/TCP reachability using normal Windows routing, including TUN/VPN.
2. **VPN/TUN signals** — local port, proxy handshake, adapter hints, split-default route hints.
3. **Wi‑Fi underlay** — WLAN association, IPv4, gateway, ARP neighbor, gateway ping, interface-bound direct probe.

A TUN/VPN signal does not mean the VPN is healthy. It only changes how aggressively Wi‑Fi may be modified.

### 4. WLAN/native recovery

`wlan_windows.go`

Uses `wlanapi.dll` for:

- interface enumeration;
- current connection / Profile / SSID;
- profile existence;
- scan;
- connect;
- disconnect.

`WlanConnect` is asynchronous, so the app polls real association state before considering the operation successful.

### 5. Recovery chain

`recovery_windows.go`

Escalation order:

1. DNS flush + interface-scoped DHCP renew;
2. native WLAN disconnect/reconnect;
3. `netsh wlan connect` fallback;
4. adapter disable/enable;
5. reconnect saved Profile;
6. DHCP/DNS retry;
7. optional WlanSvc restart.

The recovery target and automatic cooldown timestamp are persisted in `state.json` using atomic file replacement.

## Concurrency rules

- `assessmentMu`: only one expensive network assessment at a time;
- `repairMu`: only one recovery sequence at a time;
- `stateMu`: serializes `state.json` access;
- `cfgMu`: protects live configuration;
- UI performs no slow command synchronously;
- background workers return UI results with `PostMessage`;
- repeated tray status updates are coalesced so the message queue cannot be flooded.

## Design principles

- false positive recovery is worse than delayed recovery;
- VPN/TUN presence raises the evidence threshold for touching Wi‑Fi;
- saved Windows WLAN Profiles own credentials;
- recovery cooldown survives app restarts;
- external commands always need a timeout.

## Logs and diagnostics

- runtime logs live in `%LOCALAPPDATA%\WiFiWatchdog\logs\watchdog-YYYY-MM-DD.log`;
- retention is configurable (30 days by default);
- old daily logs are cleaned once per day when the first log entry is written;
- legacy `watchdog.log` files are migrated into `logs/` on upgrade;
- diagnostic reports live separately in `diagnostics/` and follow the same retention window.

## Fluent Lite native UI and resource policy (v1.6.0 candidate)

The settings window is a dedicated Win32 OS thread. Each child control is created once per opening and belongs to one of four pages. Switching pages shows/hides existing controls instead of reallocating or re-creating them. GDI fonts, pens and brushes are process-wide singletons; no image resources, rendering loop, browser engine or WebView2 is loaded.

All long-running network/system operations remain off the UI thread. The overview receives monitor changes via asynchronous `PostMessage`. A memory readout is updated when the overview opens or an explicit network refresh completes, with no permanent UI sampling goroutine.

Go GC defaults to `GOGC=70` and `GOMEMLIMIT=96MiB` equivalent soft target, both overridable through environment variables. The limit only applies to memory managed by the Go runtime and does not guarantee a process RSS limit.

Physical working set (reported by the Windows process memory API) and Go live heap should not be compared directly; Windows-native allocations and shared pages can contribute to working set.
