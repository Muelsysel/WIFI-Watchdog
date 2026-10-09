# WiFi Watchdog

一个面向 Windows 的轻量级 Wi‑Fi 自动检测与恢复工具，重点适配校园网、802.1X、VPN/TUN、多网卡和无人值守场景。

> 当前测试版本：**v1.6.1-rc.1**（Win11 现代界面 / 低内存候选版）
>
> GitHub：`https://github.com/Muelsysel/WIFI-Watchdog`

## Studio v0.3 Preview：原生安装、官方更新检查和隐私化崩溃日志

Studio 现在增加**每用户安装/卸载程序**（同时保留 ZIP）、自愿启用的 GitHub 官方版本检查、SHA-256 显示和仅本机记录的最小化崩溃摘要。Studio 仍然**默认普通权限运行**，不会自己替换或重启便携版引擎。详见 [Studio 使用说明](desktop/README.md)。

## Studio v0.2 Preview：网络适配器与智能诊断历史

Studio 独立 WPF 桌面版现已增加**适配器与 DNS/IP 详情、45 天本地脱敏诊断历史、分层故障建议、配置修改冲突检测**，以及降低日志图表刷新内存分配的改进。Go 便携版和既有 v1.6.1 下载包仍保持不变。

## 两种产品版本（Portable + Studio）

本项目现在维护**两个互不取代的 Windows 版本**：

- **Portable 便携版**：原有 Go/Win32 托盘程序，体积小、适合后台长期自动检测及恢复；原有下载和配置不受影响。
- **Studio 桌面版（Preview）**：独立 WPF/.NET 8 原生 Windows 11 控制台，包含美观的工作台、网络诊断、历史趋势、日志检索、配置方案、脱敏导出与便携引擎管理。它仅在打开时消耗桌面 UI 内存，关闭后便携引擎继续运行。

[详细功能、独立构建与安全说明](desktop/README.md)。

> Studio Preview 是实际可以运行的功能性预览版，**不是已经完成代码签名、企业部署、安全审计和长时间实机验证的正式商用产品**。两个版本使用同一份引擎数据与配置，Studio 修改配置后需要正常重启便携引擎才能生效。

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
- 托盘与设置窗口分别运行在独立的 Win32 OS UI 线程；耗时操作全部后台化，降低 Win11 随机“未响应”风险。
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

## v1.5.0-rc.1 的稳定性验证重点

这轮优先降低误重连风险：仅在确认 WLAN Profile/SSID 身份时判断重连成功；没有足够身份信息时宁可跳过自动恢复，不去断开其他 Wi-Fi。系统网络探测失败但物理 Wi-Fi 绑定探测成功时，也不执行网卡重启。

设置保存路径已移除同步日志写入。Win11 设置窗口仍需在真实机器上反复开关、编辑、保存验证，**GitHub CI 通过不代表随机卡死已彻底修复**。

日志每天一个文件；当某天日志超过约 16 MiB 时，自动尽力保留最近约 8 MiB 的内容，避免异常高频日志把磁盘写满。所有诊断、挂起、崩溃报告共享保留天数策略。若卡死持续，请同时保留当天日志、`diagnostics/hang-*.txt`，以及 Windows 任务管理器创建的进程转储文件；公开提交前清理 SSID、IP、个人路径等敏感信息。

## Clash Verge Rev / Mihomo TUN 联动诊断（v1.5.1-rc.1）

适用于 Clash Verge Rev、TUN、Rule 模式。设置控制中心中的 **混合代理端口=2026**、**Mihomo 控制端口=9097**（实际使用其他端口请据实调整）。程序可用只读 Mihomo API 查询运行模式、TUN 状态、实际混合代理端口，并通过代理执行 TLS 校验的 HTTPS 探测。仅 CONNECT 成功并不算代理互联网可用。

注意：部分 Clash Verge Rev 版本的 9097 地址可能未真正监听 TCP，而是经由内部命名管道工作。因此 **9097 连接失败不等于 VPN 停止**，软件将自动降级至 2026 混合代理检测、网卡状态、VPN 路由等证据。

如已启用控制接口 Secret，可将其通过进程环境变量 `WIFI_WATCHDOG_MIHOMO_SECRET` 提供；**不要将密码写入 GitHub、日志或公开 issue**。没有正确 Secret 时仍可检测本地代理端口和保护 Wi-Fi。该功能只读、不重启 VPN、不切换节点。

说明与示例见 [配置文档](docs/CONFIGURATION.md)。

## v1.6.0-rc.1：Fluent Lite 原生控制中心

控制中心采用 **Windows 11 风格深色侧栏 + 浅色卡片**，分为运行概览、检测策略、Wi-Fi 恢复与 VPN/系统四个页面。原有配置项仍可编辑，保存方式、日志与网络恢复策略均兼容。

这套界面只使用 Windows 原生 `user32.dll` / `gdi32.dll`，**没有引入 WebView2、Electron、浏览器进程或图片资源**；设置窗口关闭后，窗口及其控件会由 Windows 销毁。GDI 字体和画刷只创建一次并复用。

概览页面显示实时监控状态、Wi-Fi/VPN 详情，以及 **工作集（Windows Working Set）/Go 堆（HeapAlloc）**。这两个数字含义不同，工作集更接近任务管理器中看到的物理内存驻留，但仍会受系统缓存与内存压力影响。

为了减少闲置内存，默认设置 `GOGC=70` 的等效 GC 策略和 **96 MiB 的 Go 堆软目标**（不是总进程 RSS 硬上限）。已有 `GOGC`、`GOMEMLIMIT` 环境变量优先；更积极的 GC 可能稍微增加 CPU 使用率。程序不会在后台常驻重绘或刷新窗口。

建议升级后用任务管理器对比运行 15 分钟时的“内存（专用工作集）”，并反复打开关闭设置窗口 20 次，确认资源占用是否回落。Win11 UI 响应性仍需实机确认。


## v1.6.1：网络检测统计说明

日常 10 分钟一次的自动联网检测，仍是**四个探测并发，首个有效响应立即判定在线**。因此日志里的 `HTTP已统计=1/4` 只表示“已经收到一个可验证响应”；其他三个会注明为**未统计**，不能理解为“3 个网址打不开”。

想知道所有网址是否都可访问，请从托盘点击**立即检测**、在控制中心点击**刷新网络状态**，或选择**生成诊断报告**。上述手动操作会等待四个端点的结果，并在当天 `logs/watchdog-YYYY-MM-DD.log` 和诊断报告中显示每项的状态码、耗时（毫秒）及错误说明；网络成功判定标准未变。

通过 TUN 的系统直连探测不会自动使用 HTTP 代理端口 2026；它依赖当前 Windows 网络路由，与浏览器的代理设置可能不同。
