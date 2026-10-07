# WiFi Watchdog

一个面向 Windows 的轻量级 Wi‑Fi 自动检测与恢复工具，重点适配校园网、802.1X、VPN/TUN、多网卡和无人值守场景。

> 当前版本：**v1.4.0**
>
> GitHub：`https://github.com/Muelsysel/WIFI-Watchdog`

## 为什么做这个项目

Windows 有时会出现“Wi‑Fi 显示已连接，但实际无法访问互联网”的状态。常见原因包括 AP 漫游、DHCP 租约、校园网认证、无线驱动、WLAN Profile、VPN/TUN 路由、DNS 或系统网络栈异常。

WiFi Watchdog 的目标不是看到一次失败就重启网卡，而是：

1. 先确认系统是否真的断网；
2. 再判断是不是 VPN/TUN、认证门户或系统路由问题；
3. 只有确认 Wi‑Fi 底层有足够故障证据时，才执行逐级恢复；
4. 尽量避免对正常 VPN、802.1X 和校园认证造成二次干扰。

## 核心特性

- Windows 系统托盘常驻，无控制台窗口。
- Native Wi‑Fi API 获取 WLAN Profile / SSID / 信号 / 接口 GUID。
- 自动记忆最后一次成功 Wi‑Fi Profile，不依赖 SSID 与 Profile 同名。
- VPN/TUN-aware：识别 TUN、Wintun、WireGuard、Mihomo、Clash、sing-box、OpenVPN、TAP、Tailscale、ZeroTier 等线索。
- 支持可选的本地 VPN 代理端口探测；配置后会尝试 HTTP CONNECT / SOCKS5 握手。新安装默认不绑定特定端口，升级用户保留原配置。
- 系统互联网与 Wi‑Fi Underlay 分层判断，降低 TUN 环境下误判。
- 多源 HTTP/HTTPS/TCP 探测，支持疑似 captive portal 保护。
- 分层恢复：DNS/DHCP → Profile 重连 → Native WLAN → netsh → 网卡重启 → 可选 WlanSvc。
- 两次自动恢复之间有持久化冷却时间；即使重启程序也不会绕过冷却。
- Win32 UI/message loop 固定在专用 OS 线程；耗时操作全部后台化，降低随机“未响应”风险。
- 正常在线时走快速路径，不再执行不必要的 `netsh` / route / ARP 深度探测。
- 每日日志、可配置保留天数、诊断报告分目录、原子配置写入、单实例保护。
- GitHub Actions 自动 CI / Windows x64 + ARM64 Release 构建。

## 默认策略

```text
系统网络正常
    ↓
每 10 分钟检查一次
    ↓
首次失败
    ↓
每 10 秒快速复检
    ↓
连续失败 5 次
    ↓
综合判断：VPN/TUN / Portal / Wi-Fi Underlay
    ↓
仅在允许修复 Wi-Fi 时进入恢复链
    ↓
恢复失败后至少等待 10 分钟
```

## VPN / TUN 场景

v1.4 不会把“Wi‑Fi 直连探测失败”直接等同于“电脑没网”，并且正常在线时优先完成系统 Internet 快速探测。

如果系统互联网通过 TUN 正常：

```text
Wi-Fi → TUN/VPN → Internet
```

程序直接认为系统在线，不操作 Wi‑Fi。

如果系统无网，但检测到 VPN/TUN 且 Wi‑Fi 本地链路没有强故障证据，会优先保护 Wi‑Fi，避免因为 VPN 路由故障反复重启无线网卡。

## 恢复链

1. 刷新 DNS + 定向 DHCP renew。
2. Native `WlanDisconnect` / `WlanScan` / `WlanConnect`。
3. 轮询确认真正关联到目标 Profile/SSID。
4. `netsh wlan connect` 兼容兜底。
5. 禁用/启用无线网卡。
6. 网卡恢复后再次主动连接保存 Profile。
7. 再次 DHCP / DNS 修复。
8. 可选重启 `WlanSvc`（默认关闭）。

不会自动执行 `winsock reset`、`netsh int ip reset` 等高侵入且可能要求重启 Windows 的操作。

## v1.4 的 UI 稳定性修复

v1.2 的设置窗口会在 UI 主线程里同步执行：

```text
schtasks.exe /Query
schtasks.exe /Create
schtasks.exe /Delete
```

任务计划程序或系统服务响应慢时，Win32 消息循环停止处理，因此窗口会表现为“卡住/未响应”。

v1.4 在 v1.3 的异步化基础上继续修复一个更底层的问题：Win32 窗口/消息队列是线程绑定的，而 Go goroutine 默认可能迁移 OS 线程。现在窗口创建和消息泵会通过 `runtime.LockOSThread()` 固定在同一 UI 线程。

同时，以下操作继续保持后台执行并设置超时：

- 开机自启状态查询；
- 创建/删除计划任务；
- 网络综合评估；
- 诊断报告生成；
- 配置保存相关系统操作。

设置保存成功后窗口也不会自动关闭，可继续调整；后台状态更新通过 `PostMessage` 回到 UI 线程，并对重复状态消息做合并。

## 使用

### 直接运行

下载 Release 中对应架构：

- `WiFiWatchdog-windows-amd64.zip`：绝大多数 Intel/AMD Windows 电脑；
- `WiFiWatchdog-windows-arm64.zip`：Windows on ARM。

解压后运行 `WiFiWatchdog.exe`。

当前版本需要管理员权限，因为禁用/启用网卡、DHCP renew、WlanSvc 等恢复动作需要提升权限。

### 推荐首次配置

1. 正常连接目标校园 Wi‑Fi；
2. 托盘右键 → **记住当前 Wi‑Fi 为恢复目标**；
3. 如果电脑长期固定使用该网络，可开启“Wi‑Fi 意外断开时主动连接”；
4. VPN/TUN 保护建议保持开启；
5. `WlanSvc` 最后兜底建议保持关闭，除非普通恢复链仍无法解决问题。

## 数据与日志

默认目录：

```text
%LOCALAPPDATA%\WiFiWatchdog\
```

包含：

- `config.json`：配置；
- `state.json`：恢复目标和自动恢复冷却状态；
- `logs/watchdog-YYYY-MM-DD.log`：按天保存的运行日志，默认保留 30 天；
- `diagnostics/diagnostics-*.txt`：手动生成的诊断报告。

日志保留天数可在控制中心修改；程序每天首次写日志时自动清理过期文件。升级自 v1.3 时，旧的 `watchdog.log` / `watchdog.log.1` 会迁移到 `logs/` 下保存。

程序不会读取或保存 Wi‑Fi / 802.1X 密码。认证凭据始终由 Windows WLAN Profile 管理。

## 配置说明

完整参数表与安全范围见 [docs/CONFIGURATION.md](docs/CONFIGURATION.md)，机器可读的 JSON Schema 见 [config.schema.json](config.schema.json)。

全新安装默认不会假设某个 VPN 本地代理端口；如使用 Clash/Mihomo 的 mixed-port（例如你自己的 `2026`），可在控制中心显式填写。已有配置升级时不会被覆盖。

## 从源码构建

要求：Go 1.23+。

```powershell
go test ./...
go vet ./...
go build -trimpath -ldflags "-H=windowsgui -s -w" -o WiFiWatchdog.exe .
```

也可以运行：

```powershell
.\build-windows.ps1
```

## 仓库结构

```text
.
├── main_windows.go          # App 生命周期、托盘、控制中心、监控状态机
├── network_windows.go       # 系统 Internet / VPN-TUN / Underlay 判断
├── recovery_windows.go      # Profile 持久化、恢复链、诊断报告
├── wlan_windows.go          # Native Wi-Fi API 封装
├── logic_windows_test.go    # 关键纯逻辑与持久化测试
├── docs/
│   ├── ARCHITECTURE.md
│   └── TROUBLESHOOTING.md
└── .github/
    ├── workflows/
    └── ISSUE_TEMPLATE/
```

## 安全边界

本项目尽量保守，但不能保证任何校园网故障都能由本机恢复。例如：

- 学校认证服务器或 AP 故障；
- 账号被禁用或 802.1X 凭据过期；
- 无线驱动内核级崩溃；
- VPN 服务端不可用；
- 网络管理员主动限制；
- Windows 网络组件本身损坏。

遇到问题请通过托盘菜单生成诊断报告，再提交 Issue。

## 隐私

- 无遥测；
- 无云端上传；
- 不收集 Wi‑Fi 密码；
- 日志与诊断信息只保存在本机。

诊断报告可能包含本机网卡名称、私有 IP、路由表、SSID/Profile 等信息。公开提交 Issue 前请自行检查并脱敏。

## License

MIT License。见 [LICENSE](LICENSE)。
