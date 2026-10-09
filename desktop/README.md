# WiFi Watchdog Studio

**独立的 Windows 11 桌面控制台**。这是与原 Go 便携版长期并存的第二种产品形态，不是它的替代品。

## 双版本定位

| | Portable | Studio |
|---|---|---|
| 用途 | 后台自动监控与网络恢复 | 可视化网络诊断、管理和分析 |
| UI | Go / Win32 托盘与小型设置窗 | 原生 WPF 控制台（Windows 11） |
| 权限 | 恢复功能需要 UAC | **普通用户权限**；手动启动引擎会请求 UAC |
| 常驻 | 可低占用常驻 | 仅在需要时打开，不安装额外后台服务 |
| 兼容 | 原配置/日志格式 | **直接读写原 `%LOCALAPPDATA%\WiFiWatchdog`** |
| 升级 | 原有 Release 不受影响 | 独立 Studio 预览版与后续独立发布 |

Studio 只进行**只读网络诊断**，不会切换 Mihomo 节点、重启代理、断开 Wi-Fi 或启停网卡。真正的网络恢复交给 Portable 引擎，支持 Windows UAC。无需两个自动修复进程。

## Studio Preview 0.3：安装、更新与崩溃恢复

**独立安装版 + ZIP 便携安装方式并存。** Release 提供 `WiFiWatchdog-Studio-Setup-x64.exe` 和原 ZIP。安装器由 Windows CI 使用 NSIS 构建，默认安装至当前用户的 `%LOCALAPPDATA%\\Programs\\WiFiWatchdogStudio`，建立开始菜单入口，支持 Windows“已安装应用”卸载，无需让 Studio 常驻管理员权限。便携恢复引擎仍在 `Engine/` 中，只有主动点击启动恢复引擎时才通过 UAC 申请权限。

**卸载保留数据。** NSIS 卸载仅清理 Studio 安装目录、HKCU 卸载注册表项和当前用户开始菜单快捷方式；**不会删除** `%LOCALAPPDATA%\\WiFiWatchdog` 中的便携引擎配置、日志、用户方案及 Studio 本机诊断历史。如果你的 Go 引擎正在安装目录的 `Engine/` 下运行，请先从托盘正常退出后再升级或卸载安装版。

**安全的官方版本检查。** “更新与维护”可手动读取 GitHub 官方 Studio Release 元数据，检查 `studio-v...` 标签并显示其 ZIP 的 SHA-256；可选在打开 Studio 时执行一次版本检查（默认关闭）。用户点击后仅打开经过固定项目 URL 构造的官方发布页面。**不会自动下载、安装、执行未经签名的更新或更换正在运行的引擎**。更新检查只访问 GitHub API，不发送诊断内容或日志。

**本机异常摘要。** 未处理的 WPF 或后台异常会按日期写入 `%LOCALAPPDATA%\\WiFiWatchdog\\Studio\\crashes`，仅保留 UTC 时间、组件及异常类型，不存储异常消息、完整堆栈、SSID、IP 或任何 Controller Secret。默认保留 14 天。Studio 意外结束不应停止单独运行的 Go 引擎。

**仍未完成的商用品质步骤：** 代码签名、企业自动化部署、自动应用升级的签名/哈希验证、Windows 11 各种 DPI/辅助功能/安全策略下的实机验收，以及压力测试。此版本仍标为 Preview。

## Studio Preview 0.2 新能力

- **网络适配器工作台**：实时列出 Wi-Fi、以太网、虚拟 VPN/TUN 接口（按名称进行保守提示），显示网络接口状态、报告链路速率、IPv4 / IPv6、网关和 DNS。全部只读；本机地址不会写入默认诊断历史或自动上报。
- **更安全的诊断历史**：每次主动诊断成功后在本机保存脱敏摘要，最多 60 条、最长 45 天。包含四目标 HTTP 状态码和耗时、混合代理验证结果与故障结论，不保存 SSID、网卡地址、Secret 或原始错误详情。
- **可解释的故障建议**：结合系统互联网、混合代理出口以及 Wi-Fi 关联 / IPv4 信息，提示优先检查 TUN/DNS、代理节点、校园网认证或 DHCP。所有建议均只读，不自动执行重置。
- **配置冲突保护**：保存前检查磁盘配置是否在后台被其他程序更新，发现冲突时取消覆盖，允许重新载入；保留原有配置备份。属于最佳努力的并发检测，不是跨进程原子事务。
- **性能和 CSV 安全**：14 天事件统计改为流式读取，不再每次构造 224 MiB 规模的临时数据；阻止日志内容变成 Excel/表格软件公式。
- **隐私边界**：诊断历史与界面显示的本机 IP 信息分开。用户主动导出的事件 CSV 仍可能包含日志里的 SSID 等网络标识，分享前应检查。

## 目前可用的功能

- Windows 11 风格：深色侧栏、浅色信息卡、响应式布局、原生字体与控件
- 网络总览：引擎运行状态、最近日志、今日告警和恢复事件
- 历史趋势：过去 14 天警告与错误柱状图，最近三日日志筛选/导出 CSV
- 完整诊断：4 个不同的公网 HTTP/HTTPS 目标（状态码、耗时、真实验证）
- Mihomo：控制端口（默认 9097）的本地只读 API + Rule/TUN 参数检查
- Mixed-Port：通过用户配置的 2026 等混合端口进行真实 HTTPS 出口验证
- 配置中心：全部关键恢复参数，范围验证、原配置备份、保留未知字段
- 网络方案：校园 / 家庭 / 移动热点等预设，路径防穿越
- 隐私导出：诊断 JSON 默认排除 SSID、私有 IP 和 Secret
- 引擎管理：检测后台运行状态、选择或启动便携引擎、打开数据目录

**注意**：Studio 修改 `config.json` 后，运行中的 Go 引擎不会自动重读。请在托盘正常退出并重新启动引擎以应用新配置。Studio 不会直接杀进程或禁用网卡。

## 安装

推荐从项目的 Studio Release 或对应 Studio CI 的 Artifacts 下载 Win-x64 ZIP。包含：

```text
WiFiWatchdog.Studio.exe
Engine/WiFiWatchdog.exe       # 便携恢复引擎
README.md
LICENSE
(若干 .NET 自包含运行时文件)
```

解压后双击 Studio EXE。**Studio 不需要管理员权限**，首次手动启动 Engine 时才申请 UAC。如果已经运行你原有的便携版，请保持它运行；Studio 不会自动安装或再启动第二个恢复引擎。

自包含发行包不要求另外安装 .NET Desktop Runtime，但比极小的 Go 便携 ZIP 大很多，且 WPF 打开时的内存占用高于托盘引擎；关闭 Studio 后便携引擎仍能正常监控。

## 编译与测试

- Windows 10/11（优先支持 Windows 11）
- .NET SDK 8.x 和 Go 1.23+

```powershell
dotnet run -c Release --project desktop/WiFiWatchdog.Studio.Tests
dotnet build -c Release desktop/WiFiWatchdog.Studio/WiFiWatchdog.Studio.csproj

dotnet publish desktop/WiFiWatchdog.Studio/WiFiWatchdog.Studio.csproj -c Release -r win-x64 --self-contained true -p:PublishSingleFile=true -o dist/Studio
go build -trimpath -ldflags "-H=windowsgui -s -w" -o dist/Studio/Engine/WiFiWatchdog.exe .
```

`.github/workflows/studio-ci.yml` 单独构建 WPF 并运行核心测试，原有 Go CI 和便携版 Release 保持独立。

## 安全、合规和商用准备度

本版本是 **Studio Preview 0.1**，功能真实可用，但还不能称为正式认证的商用产品。上线商业发行前仍需完成：

1. Windows 11 多 DPI / 高对比度 / 键盘操作 / 屏幕阅读器验收。
2. 安装器、受控自动更新、签名证书、可复现构建与供应链清单。
3. 真实 Wi-Fi/802.1X/TUN/多网卡兼容性及长期压力测试。
4. 最小权限 IPC：以后如需 Studio 实时修改引擎状态，应设计具 ACL 和身份认证的本地通道，而不是开放无鉴权 HTTP。
5. 隐私及企业设备部署策略、安全评审、用户支持与文档。

当前不含付费、账号、上传、遥测、广告或任何未经授权的系统更改。用户主动输入的 Controller Secret 仅保留在当前输入框到本次诊断结束，不落盘；CSV 日志导出可能包含敏感网络标识，分享前请自行检查。
