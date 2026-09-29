# Architecture

## Layers

### 1. Native Win32 frontend

`main_windows.go`

- hidden message window + system tray;
- control-center/settings window;
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

Three independent views are combined:

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
- UI performs no slow command synchronously.

## Design principles

- false positive recovery is worse than delayed recovery;
- VPN/TUN presence raises the evidence threshold for touching Wi‑Fi;
- saved Windows WLAN Profiles own credentials;
- recovery cooldown survives app restarts;
- external commands always need a timeout.
