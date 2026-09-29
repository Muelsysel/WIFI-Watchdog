# Privacy

WiFi Watchdog 不包含遥测、账号系统或云端上传功能。

本地可能记录：

- WLAN Profile 名称；
- SSID；
- 无线网卡名称/GUID；
- 私有 IPv4、网关和路由信息；
- VPN/TUN 接口提示；
- 网络探测结果；
- 恢复动作与时间。

程序不会读取或保存 Wi‑Fi / 802.1X 密码。

诊断报告只写入 `%LOCALAPPDATA%\WiFiWatchdog`。如果你把诊断报告上传到 GitHub Issue，请先检查并脱敏。
