# NextGaming Wake-on-LAN check.
# Usage: wolcheck.ps1 check|fix
# Prints one JSON object (protocol.WoLReport). Runs as SYSTEM from the agent.
param([string]$Mode = 'check')
$ErrorActionPreference = 'Stop'
[Console]::OutputEncoding = [Text.Encoding]::UTF8
$fix = ($Mode -eq 'fix')

$report = [ordered]@{
  supported    = $true
  wired        = $false
  adapter      = ''
  mac          = ''
  windowsOk    = $false
  settings     = @()
  fixedAny     = $false
  boardMaker   = ''
  boardProduct = ''
  error        = ''
}
$settings = New-Object System.Collections.ArrayList

function Add-Setting([string]$name, [string]$value, [bool]$ok, [bool]$fixed, [bool]$fixable, [string]$advice) {
  [void]$settings.Add([ordered]@{ name = $name; value = $value; ok = $ok; fixed = $fixed; fixable = $fixable; advice = $advice })
}

function Is-On([string]$v) { return ($v -match '^(Enabled|On|Enable)$') }

try {
  try {
    $bb = Get-CimInstance Win32_BaseBoard
    $report.boardMaker = "$($bb.Manufacturer)".Trim()
    $report.boardProduct = "$($bb.Product)".Trim()
  } catch {}

  # 1. Find the wired adapter (NdisPhysicalMedium 14 = 802.3 Ethernet).
  $all = @(Get-NetAdapter -Physical -ErrorAction SilentlyContinue)
  $wiredAll = @($all | Where-Object { $_.NdisPhysicalMedium -eq 14 -or $_.PhysicalMediaType -eq '802.3' })
  $adapter = $wiredAll | Where-Object { $_.Status -eq 'Up' } | Select-Object -First 1
  if (-not $adapter) {
    if ($wiredAll.Count -gt 0) {
      $report.adapter = $wiredAll[0].InterfaceDescription
      $report.mac = $wiredAll[0].MacAddress
      Add-Setting 'Ethernet cable' 'Disconnected' $false $false $false 'Plug in the network cable; Wake-on-LAN does not work over Wi-Fi.'
    } else {
      Add-Setting 'Ethernet adapter' 'Not found' $false $false $false 'This PC has no wired network adapter.'
    }
    $report.settings = [object[]]$settings.ToArray()
    $report | ConvertTo-Json -Depth 5 -Compress
    exit 0
  }
  $report.wired = $true
  $report.adapter = $adapter.InterfaceDescription
  $report.mac = $adapter.MacAddress
  $name = $adapter.Name
  $restartNeeded = $false

  # 2. Power management: "Wake on Magic Packet".
  try {
    $pm = Get-NetAdapterPowerManagement -Name $name
    $v = "$($pm.WakeOnMagicPacket)"
    if ($v -eq 'Unsupported') {
      Add-Setting 'Wake on Magic Packet (power management)' $v $false $false $false 'This network card does not support Wake-on-LAN.'
    } elseif ($v -ne 'Enabled' -and $fix) {
      Set-NetAdapterPowerManagement -Name $name -WakeOnMagicPacket Enabled -NoRestart
      $restartNeeded = $true
      Add-Setting 'Wake on Magic Packet (power management)' 'Enabled' $true $true $true ''
    } else {
      Add-Setting 'Wake on Magic Packet (power management)' $v ($v -eq 'Enabled') $false $true 'Device Manager > network adapter > Power Management.'
    }
  } catch {
    Add-Setting 'Wake on Magic Packet (power management)' 'Unknown' $true $false $false "Could not read: $($_.Exception.Message)"
  }

  # 3. "Allow this device to wake the computer" (device wake arming).
  try {
    $armed = @(powercfg /devicequery wake_armed) | ForEach-Object { "$_".Trim() }
    $desc = $adapter.InterfaceDescription
    $isArmed = $armed -contains $desc
    if (-not $isArmed -and $fix) {
      powercfg /deviceenablewake "$desc" | Out-Null
      $armed = @(powercfg /devicequery wake_armed) | ForEach-Object { "$_".Trim() }
      $isArmed = $armed -contains $desc
      Add-Setting 'Allow this device to wake the computer' ($(if ($isArmed) { 'Yes' } else { 'No' })) $isArmed $isArmed $true ($(if ($isArmed) { '' } else { 'Device Manager > network adapter > Power Management > tick "Allow this device to wake the computer".' }))
    } else {
      Add-Setting 'Allow this device to wake the computer' ($(if ($isArmed) { 'Yes' } else { 'No' })) $isArmed $false $true 'Device Manager > network adapter > Power Management > tick "Allow this device to wake the computer".'
    }
  } catch {
    Add-Setting 'Allow this device to wake the computer' 'Unknown' $true $false $false "Could not read: $($_.Exception.Message)"
  }

  # 4. Driver advanced properties (names differ between Realtek, Intel, Killer...).
  try {
    $props = @(Get-NetAdapterAdvancedProperty -Name $name -ErrorAction SilentlyContinue | Where-Object {
      $_.DisplayName -match 'Wake on Magic Packet|Shutdown Wake|Wake on LAN|Wake-on-LAN|Wake From Shutdown|Enable PME' -and
      $_.DisplayName -notmatch 'Pattern|Link|Speed|Flow'
    })
    foreach ($p in $props) {
      $cur = "$($p.DisplayValue)"
      if (Is-On $cur) {
        Add-Setting "Driver: $($p.DisplayName)" $cur $true $false $true ''
        continue
      }
      $target = @($p.ValidDisplayValues) | Where-Object { Is-On $_ } | Select-Object -First 1
      if ($fix -and $target) {
        Set-NetAdapterAdvancedProperty -Name $name -RegistryKeyword $p.RegistryKeyword -DisplayValue $target -NoRestart
        $restartNeeded = $true
        Add-Setting "Driver: $($p.DisplayName)" $target $true $true $true ''
      } else {
        Add-Setting "Driver: $($p.DisplayName)" $cur $false $false ([bool]$target) 'Device Manager > network adapter > Advanced tab.'
      }
    }
  } catch {}

  # 5. Fast Startup (hybrid shutdown) often prevents waking from "Shut down".
  try {
    $key = 'HKLM:\SYSTEM\CurrentControlSet\Control\Session Manager\Power'
    $hb = (Get-ItemProperty -Path $key -Name HiberbootEnabled -ErrorAction SilentlyContinue).HiberbootEnabled
    if ($hb -eq 1) {
      if ($fix) {
        Set-ItemProperty -Path $key -Name HiberbootEnabled -Value 0 -Type DWord
        Add-Setting 'Fast Startup' 'Off' $true $true $true ''
      } else {
        Add-Setting 'Fast Startup' 'On' $false $false $true 'Control Panel > Power Options > Choose what the power buttons do > untick "Turn on fast startup".'
      }
    } else {
      Add-Setting 'Fast Startup' 'Off' $true $false $true ''
    }
  } catch {
    Add-Setting 'Fast Startup' 'Unknown' $true $false $false "Could not read: $($_.Exception.Message)"
  }

  # Driver changes apply after the adapter restarts (a few seconds offline).
  if ($restartNeeded) {
    try { Restart-NetAdapter -Name $name -Confirm:$false } catch {}
  }

  $report.settings = [object[]]$settings.ToArray()
  $report.fixedAny = [bool](@($settings | Where-Object { $_.fixed }).Count -gt 0)
  $report.windowsOk = [bool](@($settings | Where-Object { -not $_.ok }).Count -eq 0)
} catch {
  $report.error = $_.Exception.Message
  $report.settings = [object[]]$settings.ToArray()
}
$report | ConvertTo-Json -Depth 5 -Compress
