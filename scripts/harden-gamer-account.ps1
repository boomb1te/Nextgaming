# NextGaming: prepare the restricted "Gamer" Windows account on a gaming PC.
# Run as Administrator. Review before using; test on ONE PC first.
#
#   Step 1:  .\harden-gamer-account.ps1              Create the Gamer account and turn on auto-logon.
#            Restart the PC so Gamer logs on once (this creates its profile).
#   Step 2:  .\harden-gamer-account.ps1 -Apply       Apply the restrictions to Gamer.
#   Undo:    .\harden-gamer-account.ps1 -Undo        Remove restrictions and auto-logon.
#
# The lock screen blocks Alt+Tab, the Windows key and Alt+F4, and runs as SYSTEM
# so Gamer cannot close it. These policies close the remaining gaps
# (Task Manager, Run box, command prompt, registry editor, Control Panel).
# Ctrl+Alt+Del itself cannot be blocked by any program; the policies below
# remove the useful options behind it.
param(
  [string]$User = 'Gamer',
  [switch]$Apply,
  [switch]$Undo
)
$ErrorActionPreference = 'Stop'

$principal = New-Object Security.Principal.WindowsPrincipal([Security.Principal.WindowsIdentity]::GetCurrent())
if (-not $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
  throw 'Run this from an Administrator PowerShell window.'
}

$winlogon = 'HKLM:\SOFTWARE\Microsoft\Windows NT\CurrentVersion\Winlogon'
$machineSystem = 'HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\Policies\System'

function Set-Value($path, $name, $value, $type = 'DWord') {
  if (-not (Test-Path $path)) { New-Item -Path $path -Force | Out-Null }
  New-ItemProperty -Path $path -Name $name -Value $value -PropertyType $type -Force | Out-Null
}
function Remove-Value($path, $name) {
  if (Test-Path $path) { Remove-ItemProperty -Path $path -Name $name -ErrorAction SilentlyContinue }
}

# Opens the Gamer user's registry (loaded if logged on, otherwise from NTUSER.DAT).
function With-UserHive([scriptblock]$body) {
  $acct = Get-LocalUser -Name $User -ErrorAction Stop
  $sid = $acct.SID.Value
  $loadedHere = $false
  $root = "Registry::HKEY_USERS\$sid"
  if (-not (Test-Path $root)) {
    $profilePath = (Get-ItemProperty "HKLM:\SOFTWARE\Microsoft\Windows NT\CurrentVersion\ProfileList\$sid" -ErrorAction SilentlyContinue).ProfileImagePath
    if (-not $profilePath) { throw "$User has never logged on. Restart the PC, let $User log on once, then run -Apply again." }
    & reg.exe load "HKU\NextGamingUser" (Join-Path $profilePath 'NTUSER.DAT') | Out-Null
    if ($LASTEXITCODE -ne 0) { throw 'Could not load the user registry. Sign the Gamer account out and try again.' }
    $root = 'Registry::HKEY_USERS\NextGamingUser'
    $loadedHere = $true
  }
  try { & $body $root }
  finally {
    if ($loadedHere) { [gc]::Collect(); Start-Sleep -Milliseconds 500; & reg.exe unload 'HKU\NextGamingUser' | Out-Null }
  }
}

$blockedApps = @('taskmgr.exe', 'cmd.exe', 'powershell.exe', 'powershell_ise.exe', 'pwsh.exe', 'regedit.exe', 'mmc.exe', 'msconfig.exe')

if ($Undo) {
  With-UserHive {
    param($root)
    $sys = "$root\Software\Microsoft\Windows\CurrentVersion\Policies\System"
    $exp = "$root\Software\Microsoft\Windows\CurrentVersion\Policies\Explorer"
    foreach ($n in 'DisableTaskMgr', 'DisableRegistryTools', 'DisableLockWorkstation', 'DisableChangePassword') { Remove-Value $sys $n }
    foreach ($n in 'NoControlPanel', 'NoRun', 'NoLogoff', 'DisallowRun') { Remove-Value $exp $n }
    if (Test-Path "$exp\DisallowRun") { Remove-Item "$exp\DisallowRun" -Recurse -Force }
    Remove-Value "$root\Software\Policies\Microsoft\Windows\System" 'DisableCMD'
  }
  Remove-Value $machineSystem 'HideFastUserSwitching'
  Set-Value $winlogon 'AutoAdminLogon' '0' 'String'
  Remove-Value $winlogon 'DefaultPassword'
  Write-Host "Restrictions and auto-logon removed for $User."
  return
}

if ($Apply) {
  With-UserHive {
    param($root)
    $sys = "$root\Software\Microsoft\Windows\CurrentVersion\Policies\System"
    $exp = "$root\Software\Microsoft\Windows\CurrentVersion\Policies\Explorer"
    Set-Value $sys 'DisableTaskMgr' 1
    Set-Value $sys 'DisableRegistryTools' 1
    Set-Value $sys 'DisableLockWorkstation' 1
    Set-Value $sys 'DisableChangePassword' 1
    Set-Value $exp 'NoControlPanel' 1
    Set-Value $exp 'NoRun' 1
    Set-Value $exp 'NoLogoff' 1
    Set-Value $exp 'DisallowRun' 1
    $list = "$exp\DisallowRun"
    if (Test-Path $list) { Remove-Item $list -Recurse -Force }
    New-Item -Path $list -Force | Out-Null
    for ($i = 0; $i -lt $blockedApps.Count; $i++) { Set-Value $list ([string]($i + 1)) $blockedApps[$i] 'String' }
    Set-Value "$root\Software\Policies\Microsoft\Windows\System" 'DisableCMD' 1
  }
  Set-Value $machineSystem 'HideFastUserSwitching' 1
  Write-Host "Restrictions applied to $User. Sign out and back in (or restart) for them to take effect."
  return
}

# Step 1: create the account and enable auto-logon.
if (-not (Get-LocalUser -Name $User -ErrorAction SilentlyContinue)) {
  New-LocalUser -Name $User -NoPassword -AccountNeverExpires -UserMayNotChangePassword -Description 'NextGaming player account' | Out-Null
  Add-LocalGroupMember -Group (Get-LocalGroup -SID 'S-1-5-32-545').Name -Member $User   # Users (standard, not admin)
  Write-Host "Created standard account $User (no password)."
} else {
  Write-Host "$User already exists."
}
Set-Value $winlogon 'AutoAdminLogon' '1' 'String'
Set-Value $winlogon 'DefaultUserName' $User 'String'
Set-Value $winlogon 'DefaultDomainName' $env:COMPUTERNAME 'String'
Set-Value $winlogon 'DefaultPassword' '' 'String'
Write-Host "Auto-logon enabled for $User. Restart the PC; after $User has logged on once, run this script again with -Apply."
Write-Host 'Keep a separate Administrator account with a password for maintenance.'
