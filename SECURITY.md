# Security Policy

## Supported version

当前仅维护最新稳定版本。

## Reporting a vulnerability

请不要把可能暴露凭据、提权绕过或任意命令执行的问题直接公开到 Issue。

在尚未配置专用安全邮箱的情况下，请先创建一个不包含 exploit 细节的 Issue，说明“存在安全问题，需要私下联系维护者”。维护者可随后提供私下沟通渠道。

## Privilege model

当前版本整体以管理员权限运行，这是为了执行网卡启停、DHCP renew 和可选 WlanSvc 恢复。长期路线图会考虑将 UI 与高权限恢复 helper 拆分，缩小常驻高权限代码面。
