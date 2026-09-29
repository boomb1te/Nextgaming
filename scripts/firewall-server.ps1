# Run on the front-desk (server) PC as Administrator.
# Opens the admin panel port and the agent port to the local network only.
$ErrorActionPreference = 'Stop'
foreach ($r in @(
  @{ Name = 'NextGaming admin panel'; Port = 8080 },
  @{ Name = 'NextGaming agents';      Port = 47810 }
)) {
  Get-NetFirewallRule -DisplayName $r.Name -ErrorAction SilentlyContinue | Remove-NetFirewallRule
  New-NetFirewallRule -DisplayName $r.Name -Direction Inbound -Protocol TCP -LocalPort $r.Port `
    -RemoteAddress LocalSubnet -Action Allow -Profile Any | Out-Null
  Write-Host "Allowed TCP $($r.Port) from the local network ($($r.Name))"
}
