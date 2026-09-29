# Contributing

感谢参与 WiFi Watchdog。

## 开发要求

- Windows 10/11；
- Go 1.23+；
- 修改网络恢复逻辑时，请优先遵循“先判定、后恢复、尽量少侵入”的原则。

## 提交前

```powershell
gofmt -w *.go
go test ./...
go vet ./...
go build -trimpath -ldflags "-H=windowsgui" .
```

## Bug 报告

请尽量附上：

- Windows 版本；
- 无线网卡型号；
- 是否使用 VPN/TUN；
- VPN 软件及本地端口；
- `watchdog.log` 中相关时间段；
- 托盘 → “生成诊断报告”的结果（请先脱敏）。

不要公开提交账号、密码、802.1X 凭据、真实公网 IP 或不希望暴露的 SSID。

## Pull Request

- 一个 PR 尽量只解决一个主题；
- 行为变化请更新 `CHANGELOG.md`；
- 新的恢复动作必须说明触发条件和误伤风险；
- 任何可能重置系统网络栈、修改全局代理或要求重启系统的动作，默认不应自动执行。
