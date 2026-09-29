$ErrorActionPreference = 'Stop'

$Root = Split-Path -Parent $MyInvocation.MyCommand.Path
Set-Location $Root

New-Item -ItemType Directory -Force dist | Out-Null

gofmt -w *.go
go test ./...
go vet ./...

$env:CGO_ENABLED = '0'
$env:GOOS = 'windows'

$env:GOARCH = 'amd64'
go build -trimpath -ldflags '-H=windowsgui -s -w' -o 'dist/WiFiWatchdog-windows-amd64.exe' .

$env:GOARCH = 'arm64'
go build -trimpath -ldflags '-H=windowsgui -s -w' -o 'dist/WiFiWatchdog-windows-arm64.exe' .

Write-Host 'Build completed:' -ForegroundColor Green
Get-ChildItem dist
