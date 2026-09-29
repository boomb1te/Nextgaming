# Build the Windows programs on Windows. Output: dist\windows\*.exe
$ErrorActionPreference = 'Stop'
Set-Location (Join-Path $PSScriptRoot '..')
$version = if ($env:VERSION) { $env:VERSION } else { '1.0.0' }
$out = 'dist\windows'
New-Item -ItemType Directory -Force -Path $out | Out-Null
$env:GOOS = 'windows'; $env:GOARCH = 'amd64'; $env:CGO_ENABLED = '0'
$ld = "-s -w -X nextgaming/internal/server.Version=$version -X nextgaming/internal/agent.Version=$version"
go build -trimpath -ldflags $ld -o "$out\nextgaming-server.exe" ./cmd/nextgaming-server
go build -trimpath -ldflags $ld -o "$out\nextgaming-agent.exe" ./cmd/nextgaming-agent
go build -trimpath -ldflags "$ld -H windowsgui" -o "$out\nextgaming-client.exe" ./cmd/nextgaming-client
Copy-Item scripts\firewall-server.ps1, scripts\harden-gamer-account.ps1, scripts\install-agent.cmd $out
Get-ChildItem $out
