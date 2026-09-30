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

  # 2. Driver settings (Device Manager > network adapter > Advanced tab).
  # Matched by registry keyword and checked by registry value, NOT by the
  # displayed text: display names and values are translated on non-English
  # Windows/drivers (e.g. "Включено"), keywords and values are not.
  $wolKeywords = @('*WakeOnMagicPacket', 'S5WakeOnLan', 'EnablePME', 'WakeFromS5', 'WakeOnMagicPacketFromS5')
  $hasMagicKeyword = $false
  try {
    $props = @(Get-NetAdapterAdvancedProperty -Name $name -ErrorAction SilentlyContinue | Where-Object {
      ($wolKeywords -contains $_.RegistryKeyword) -or
      ($_.DisplayName -match 'Wake on Magic Packet|Shutdown Wake|Wake From Shutdown|Enable PME' -and $_.DisplayName -notmatch 'Pattern|Link|Speed|Flow|S0ix')
    })
    foreach ($p in $props) {
      if ($p.RegistryKeyword -eq '*WakeOnMagicPacket') { $hasMagicKeyword = $true }
      $rv = "$(@($p.RegistryValue)[0])"
      $valid = @($p.ValidRegistryValues | ForEach-Object { "$_" })
      $isOn = ($rv -eq '1') -or (Is-On "$($p.DisplayValue)")
      $label = "Driver: $($p.DisplayName)"
      if ($isOn) {
        Add-Setting $label "$($p.DisplayValue)" $true $false $true ''
        continue
      }
      $canFix = ($valid.Count -eq 0) -or ($valid -contains '1')
      if ($fix -and $canFix) {
        try {
          Set-NetAdapterAdvancedProperty -Name $name -RegistryKeyword $p.RegistryKeyword -RegistryValue 1 -NoRestart
          $restartNeeded = $true
          Add-Setting $label 'Enabled' $true $true $true ''
        } catch {
          Add-Setting $label "$($p.DisplayValue)" $false $false $true "Could not change it automatically: $($_.Exception.Message). Set it in Device Manager > network adapter > Advanced tab."
        }
      } else {
        Add-Setting $label "$($p.DisplayValue)" $false $false $canFix 'Device Manager > network adapter > Advanced tab: set it to Enabled.'
      }
    }
  } catch {}

  # 3. Power management "Wake on Magic Packet". This is the same switch as the
  # *WakeOnMagicPacket driver keyword, so only check it when that keyword is absent.
  if (-not $hasMagicKeyword) {
    try {
      $pm = Get-NetAdapterPowerManagement -Name $name
      $v = "$($pm.WakeOnMagicPacket)"   # enum name: Enabled / Disabled / Unsupported (never translated)
      if ($v -eq 'Unsupported') {
        Add-Setting 'Wake on Magic Packet' $v $false $false $false 'This network card does not support Wake-on-LAN.'
      } elseif ($v -ne 'Enabled' -and $fix) {
        Set-NetAdapterPowerManagement -Name $name -WakeOnMagicPacket Enabled -NoRestart
        $restartNeeded = $true
        Add-Setting 'Wake on Magic Packet' 'Enabled' $true $true $true ''
      } else {
        Add-Setting 'Wake on Magic Packet' $v ($v -eq 'Enabled') $false $true 'Device Manager > network adapter > Advanced tab: Wake on Magic Packet = Enabled.'
      }
    } catch {
      Add-Setting 'Wake on Magic Packet' 'Unknown' $true $false $false "Could not read: $($_.Exception.Message)"
    }
  }

  # 4. "Allow this device to wake the computer". Read from WMI by the device's
  # hardware ID (exact and language independent); powercfg names as a fallback.
  $wakeAdvice = 'Device Manager > network adapter > Power Management tab: tick "Allow this device to wake the computer".'
  try {
    $pnpId = "$($adapter.PnPDeviceID)".ToUpper()
    $findWake = {
      @(Get-CimInstance -Namespace root/wmi -ClassName MSPower_DeviceWakeEnable -ErrorAction SilentlyContinue |
        Where-Object { $pnpId -and "$($_.InstanceName)".ToUpper().StartsWith($pnpId) }) | Select-Object -First 1
    }
    $wake = & $findWake
    $isArmed = $false
    $known = $false
    if ($wake) {
      $known = $true
      $isArmed = [bool]$wake.Enable
    } else {
      $names = @($adapter.InterfaceDescription)
      try { $names += (Get-PnpDevice -InstanceId $adapter.PnPDeviceID -ErrorAction Stop).FriendlyName } catch {}
      $armed = @(powercfg /devicequery wake_armed) | ForEach-Object { "$_".Trim() }
      $isArmed = @($names | Where-Object { $armed -contains $_ }).Count -gt 0
      $known = $true
    }
    $fixedIt = $false
    if ($known -and -not $isArmed -and $fix) {
      if ($wake) {
        try { $wake.Enable = $true; Set-CimInstance -InputObject $wake -ErrorAction Stop } catch {}
        $w2 = & $findWake
        if ($w2) { $isArmed = [bool]$w2.Enable }
      }
      if (-not $isArmed) {
        powercfg /deviceenablewake "$($adapter.InterfaceDescription)" 2>$null | Out-Null
        $armed = @(powercfg /devicequery wake_armed) | ForEach-Object { "$_".Trim() }
        $isArmed = $armed -contains $adapter.InterfaceDescription
      }
      $fixedIt = $isArmed
    }
    Add-Setting 'Allow this device to wake the computer' ($(if ($isArmed) { 'Yes' } else { 'No' })) $isArmed $fixedIt $true ($(if ($isArmed) { '' } else { $wakeAdvice }))
  } catch {
    Add-Setting 'Allow this device to wake the computer' 'Unknown' $true $false $false "Could not read: $($_.Exception.Message)"
  }

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
