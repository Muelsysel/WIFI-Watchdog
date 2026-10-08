$ErrorActionPreference = 'Stop'

$Root = Split-Path -Parent $MyInvocation.MyCommand.Path
Set-Location $Root

New-Item -ItemType Directory -Force dist | Out-Null

$Version = if ($env:WIFI_WATCHDOG_VERSION) { $env:WIFI_WATCHDOG_VERSION.TrimStart('v') } else { '1.5.1-rc.1' }
$LdFlags = "-H=windowsgui -s -w -X main.appVersion=$Version"

gofmt -w *.go
go test ./...
go vet ./...

$env:CGO_ENABLED = '0'
$env:GOOS = 'windows'

$env:GOARCH = 'amd64'
go build -trimpath -ldflags $LdFlags -o 'dist/WiFiWatchdog-windows-amd64.exe' .

$env:GOARCH = 'arm64'
go build -trimpath -ldflags $LdFlags -o 'dist/WiFiWatchdog-windows-arm64.exe' .

Write-Host 'Build completed:' -ForegroundColor Green
Get-ChildItem dist
