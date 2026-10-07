# Troubleshooting

## 设置窗口仍然卡住

v1.4 除了把 `schtasks`、网络检测、诊断生成和配置保存移出 UI 线程，还把 Win32 窗口和消息泵固定在同一个 OS 线程。正常在线时也不再运行不必要的深度 WLAN/netsh/route 探测。

如果窗口仍然出现“未响应”：

1. 记录 Windows 版本、DPI 缩放比例和是否开启安全软件；
2. 等待 15 秒，确认窗口是否自动恢复；
3. 托盘生成诊断报告；
4. 提交当天 `logs/watchdog-YYYY-MM-DD.log` 中故障前后约 1 分钟内容；
5. 如能复现，请说明是“打开设置即卡住”、编辑某个输入框时卡住，还是点击“保存/刷新/诊断”后卡住。

如果整个托盘程序也同时失去响应，请在 Issue 中注明，这通常比单独的后台网络探测问题更接近 UI 消息泵/系统注入类问题。

## 明明能上网，却被判定断网

优先确认：

- 是否开启 VPN/TUN；
- 本地 VPN 端口是否配置正确；
- 系统代理与 TUN 是否同时开启；
- 浏览器是否依赖单独代理而不是系统/TUN 路由。

如果只有浏览器设置了独立 HTTP 代理，而系统本身没有 Internet，Watchdog 可能正确地把“系统网络”判为离线。

## Wi‑Fi 重启后没有自动连接

确保目标网络曾成功连接一次，然后：

- 托盘 → “记住当前 Wi‑Fi 为恢复目标”；
- 检查 Windows WLAN Profile 仍存在；
- 对 802.1X 网络确认凭据没有过期。

程序会优先使用 Native `WlanConnect(Profile)`，再使用 `netsh wlan connect` 兜底。

## VPN 端口 2026

端口开放只说明本机有服务监听。程序还会尝试 HTTP CONNECT / SOCKS5 握手；只有握手和上游访问成功才会把它视为强 VPN 上游信号。

## 恢复间隔为什么重启软件后仍然要等

v1.3+ 把 `LastAutoRepairAt` 写入 `state.json`。这是故意设计，用于防止频繁重启程序绕过“每 10 分钟最多一次自动恢复”的安全限制。

## 日志文件在哪里

v1.4 开始按天记录日志：

```text
%LOCALAPPDATA%\WiFiWatchdog\logs\watchdog-YYYY-MM-DD.log
```

默认保留最近 30 天，可以在控制中心修改。旧日志会自动清理，诊断报告放在单独的 `diagnostics\` 目录。
