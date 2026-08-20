[CmdletBinding()]
param(
  [Parameter(Mandatory = $true)]
  [ValidateSet('Guard', 'CleanupExact', 'CleanupAll', 'AuditRemoved')]
  [string]$Action,

  [string]$RuleName = '',

  [int]$OwnerPid = 0,

  [string]$StateRoot = '',

  [string]$StateDirectory = '',

  [ValidateRange(1, 120)]
  [int]$DeadlineSeconds = 30
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
$ruleGroup = 'UnitTestIDE Native Offline Boundary'
$rulePattern = '^UnitTestIDE-NativeOffline-[0-9a-f]{16,64}$'
$cleanupPollMilliseconds = 100
$requiredStableAudits = 3

function Assert-RuleName([string]$Name) {
  if ($Name -cnotmatch $rulePattern) {
    throw 'Windows native offline firewall rule name is invalid'
  }
}

function Get-RuleByName([string]$Store, [string]$Name) {
  try {
    @(Get-NetFirewallRule -PolicyStore $Store -Name $Name -ErrorAction Stop)
  } catch {
    if ($_.FullyQualifiedErrorId -like 'CmdletizationQuery_NotFound*') {
      return
    }
    throw
  }
}

function Get-RuleByGroup([string]$Store) {
  try {
    @(Get-NetFirewallRule -PolicyStore $Store -Group $ruleGroup -ErrorAction Stop)
  } catch {
    if ($_.FullyQualifiedErrorId -like 'CmdletizationQuery_NotFound*') {
      return
    }
    throw
  }
}

function Assert-RuleRemoved([string]$Name) {
  $active = @(Get-RuleByName 'ActiveStore' $Name)
  $persistent = @(Get-RuleByName 'PersistentStore' $Name)
  if ($active.Count -ne 0 -or $persistent.Count -ne 0) {
    throw 'Windows native offline firewall rule still exists'
  }
}

function Assert-GroupRemoved {
  $active = @(Get-RuleByGroup 'ActiveStore')
  $persistent = @(Get-RuleByGroup 'PersistentStore')
  if ($active.Count -ne 0 -or $persistent.Count -ne 0) {
    throw 'Windows native offline firewall rule group still exists'
  }
}

function Assert-RuleInstalled([string]$Name) {
  $rules = @(Get-RuleByName 'ActiveStore' $Name)
  $persistent = @(Get-RuleByName 'PersistentStore' $Name)
  if ($rules.Count -ne 1 -or $persistent.Count -ne 1) {
    throw 'Windows native offline firewall rule is not unique in both policy stores'
  }
  $rule = $rules[0]
  if (
    $rule.Name -cne $Name -or
    $rule.DisplayName -cne $Name -or
    $rule.Enabled.ToString() -ne 'True' -or
    $rule.Direction.ToString() -ne 'Outbound' -or
    $rule.Action.ToString() -ne 'Block' -or
    $rule.Profile.ToString() -ne 'Any' -or
    $rule.Group -ne $ruleGroup
  ) {
    throw 'Windows native offline firewall rule has unexpected policy'
  }

  # Get-NetFirewallProfile without PolicyStore reads PersistentStore. The
  # effective protection decision must be audited from ActiveStore explicitly.
  $profiles = @(Get-NetFirewallProfile -PolicyStore ActiveStore -ErrorAction Stop)
  $expectedProfileNames = @('Domain', 'Private', 'Public')
  $actualProfileNames = @($profiles | ForEach-Object { $_.Name } | Sort-Object -Unique)
  if (
    $profiles.Count -ne $expectedProfileNames.Count -or
    $actualProfileNames.Count -ne $expectedProfileNames.Count
  ) {
    throw 'Windows Firewall ActiveStore profile set is not closed'
  }
  for ($index = 0; $index -lt $expectedProfileNames.Count; $index++) {
    if ($actualProfileNames[$index] -cne $expectedProfileNames[$index]) {
      throw 'Windows Firewall ActiveStore profile set is not closed'
    }
  }
  $disabledProfiles = @($profiles | Where-Object { $_.Enabled.ToString() -ne 'True' })
  if ($disabledProfiles.Count -ne 0) {
    throw 'Windows Firewall must be enabled for every ActiveStore profile'
  }

  $application = @($rule | Get-NetFirewallApplicationFilter)
  $address = @($rule | Get-NetFirewallAddressFilter)
  $port = @($rule | Get-NetFirewallPortFilter)
  $service = @($rule | Get-NetFirewallServiceFilter)
  $interface = @($rule | Get-NetFirewallInterfaceFilter)
  $interfaceType = @($rule | Get-NetFirewallInterfaceTypeFilter)
  if (
    $application.Count -ne 1 -or ($application[0].Program -join ',') -ne 'Any' -or
    -not ([string]::IsNullOrEmpty($application[0].Package) -or $application[0].Package -eq 'Any') -or
    $address.Count -ne 1 -or
    ($address[0].LocalAddress -join ',') -ne 'Any' -or
    ($address[0].RemoteAddress -join ',') -ne 'Any' -or
    $port.Count -ne 1 -or $port[0].Protocol.ToString() -ne 'Any' -or
    ($port[0].LocalPort -join ',') -ne 'Any' -or
    ($port[0].RemotePort -join ',') -ne 'Any' -or
    $service.Count -ne 1 -or $service[0].Service.ToString() -ne 'Any' -or
    $interface.Count -ne 1 -or ($interface[0].InterfaceAlias -join ',') -ne 'Any' -or
    $interfaceType.Count -ne 1 -or $interfaceType[0].InterfaceType.ToString() -ne 'Any'
  ) {
    throw 'Windows native offline firewall filters are not closed'
  }
}

function Remove-RuleByName([string]$Name) {
  $rules = @(Get-RuleByName 'PersistentStore' $Name)
  if ($rules.Count -gt 0) {
    $rules | Remove-NetFirewallRule -ErrorAction Stop
  }
}

function Remove-RuleGroup {
  $rules = @(Get-RuleByGroup 'PersistentStore')
  if ($rules.Count -gt 0) {
    $rules | Remove-NetFirewallRule -ErrorAction Stop
  }
}

function Invoke-StableCleanup(
  [scriptblock]$Remove,
  [scriptblock]$Audit,
  [scriptblock]$HasBlockers
) {
  $deadline = [DateTime]::UtcNow.AddSeconds($DeadlineSeconds)
  $stableAudits = 0
  $lastError = $null
  while ([DateTime]::UtcNow -lt $deadline) {
    try {
      & $Remove
      & $Audit
      if (& $HasBlockers) {
        throw 'Windows native offline guardian is not yet disarmed'
      }
      $stableAudits++
      $lastError = $null
      if ($stableAudits -ge $requiredStableAudits) {
        return
      }
    } catch {
      # Query and removal failures are retryable until the bounded deadline;
      # none may terminate cleanup before a stable Active/Persistent audit.
      $lastError = $_
      $stableAudits = 0
    }
    Start-Sleep -Milliseconds $cleanupPollMilliseconds
  }
  if ($null -ne $lastError) {
    throw "Windows native offline firewall cleanup did not converge: $($lastError.Exception.Message)"
  }
  throw 'Windows native offline firewall cleanup did not converge before its deadline'
}

function Get-SafeFullPath([string]$Path, [string]$Label) {
  if ([string]::IsNullOrWhiteSpace($Path) -or -not [IO.Path]::IsPathRooted($Path)) {
    throw "Windows native offline $Label path is invalid"
  }
  [IO.Path]::GetFullPath($Path).TrimEnd([IO.Path]::DirectorySeparatorChar)
}

function Assert-PlainDirectory([string]$Path, [string]$Label) {
  $item = Get-Item -LiteralPath $Path -Force -ErrorAction Stop
  if (-not $item.PSIsContainer -or ($item.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) {
    throw "Windows native offline $Label directory is unsafe"
  }
}

function Assert-StateDirectory([string]$Root, [string]$Directory, [string]$Name) {
  $rootPath = Get-SafeFullPath $Root 'state root'
  $directoryPath = Get-SafeFullPath $Directory 'state'
  Assert-PlainDirectory $rootPath 'state root'
  Assert-PlainDirectory $directoryPath 'state'
  if (
    [IO.Path]::GetDirectoryName($directoryPath) -cne $rootPath -or
    [IO.Path]::GetFileName($directoryPath) -cne $Name
  ) {
    throw 'Windows native offline state directory escaped its root'
  }
  $directoryPath
}

function Write-ExclusiveMarker([string]$Path, [string]$Content) {
  $bytes = [Text.Encoding]::UTF8.GetBytes($Content)
  $stream = [IO.File]::Open(
    $Path,
    [IO.FileMode]::CreateNew,
    [IO.FileAccess]::Write,
    [IO.FileShare]::None
  )
  try {
    $stream.Write($bytes, 0, $bytes.Length)
    $stream.Flush($true)
  } finally {
    $stream.Dispose()
  }
}

function Write-MarkerUnlessPresent([string]$Path, [string]$Content) {
  try {
    Write-ExclusiveMarker $Path $Content
  } catch [IO.IOException] {
    $item = Get-Item -LiteralPath $Path -Force -ErrorAction Stop
    if ($item.PSIsContainer -or ($item.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) {
      throw 'Windows native offline marker is unsafe'
    }
  }
}

function Test-OwnerAlive([Diagnostics.Process]$Owner) {
  try {
    $Owner.Refresh()
    -not $Owner.HasExited
  } catch {
    $false
  }
}

function Request-GuardianRelease([IO.DirectoryInfo]$Directory) {
  Write-MarkerUnlessPresent (
    Join-Path $Directory.FullName 'release'
  ) "$($Directory.Name)`n"
}

function Test-GuardianBlockers([string]$Root) {
  $directories = @(Get-ChildItem -LiteralPath $Root -Directory -Force -ErrorAction Stop)
  $hasBlocker = $false
  foreach ($directory in $directories) {
    if ($directory.Name -cnotmatch $rulePattern) {
      throw 'Windows native offline state root contains an unexpected directory'
    }
    if (($directory.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) {
      throw 'Windows native offline state root contains a reparse point'
    }
    Request-GuardianRelease $directory
    if (Test-Path -LiteralPath (Join-Path $directory.FullName 'removed') -PathType Leaf) {
      continue
    }
    $pidPath = Join-Path $directory.FullName 'guardian.pid'
    if (-not (Test-Path -LiteralPath $pidPath -PathType Leaf)) {
      # The guardian may have been spawned but not scheduled yet. Until it
      # writes its PID it remains a possible late creator.
      $hasBlocker = $true
      continue
    }
    $guardianPidText = (Get-Content -LiteralPath $pidPath -Raw -ErrorAction Stop).Trim()
    $guardianPid = 0
    if (-not [int]::TryParse($guardianPidText, [ref]$guardianPid) -or $guardianPid -le 0) {
      throw 'Windows native offline guardian PID marker is invalid'
    }
    try {
      Get-Process -Id $guardianPid -ErrorAction Stop | Out-Null
      $hasBlocker = $true
    } catch {
      if ($_.FullyQualifiedErrorId -notlike 'NoProcessFoundForGivenId*') {
        throw
      }
    }
  }
  $hasBlocker
}

switch ($Action) {
  'Guard' {
    Assert-RuleName $RuleName
    if ($OwnerPid -le 0) {
      throw 'Windows native offline guardian owner PID is invalid'
    }
    $statePath = Assert-StateDirectory $StateRoot $StateDirectory $RuleName
    $pidPath = Join-Path $statePath 'guardian.pid'
    $readyPath = Join-Path $statePath 'ready'
    $releasePath = Join-Path $statePath 'release'
    $removedPath = Join-Path $statePath 'removed'
    $failedPath = Join-Path $statePath 'failed'
    Write-ExclusiveMarker $pidPath "$PID`n"

    $primaryError = $null
    $cleanupError = $null
    try {
      $owner = Get-Process -Id $OwnerPid -ErrorAction Stop
      Assert-RuleRemoved $RuleName
      $continueGuard = -not (Test-Path -LiteralPath $releasePath -PathType Leaf) -and (Test-OwnerAlive $owner)
      if ($continueGuard) {
        # This guardian process is the only rule creator. Once this call returns,
        # the rest of its lifetime contains no create path.
        New-NetFirewallRule `
          -PolicyStore PersistentStore `
          -Name $RuleName `
          -DisplayName $RuleName `
          -Group $ruleGroup `
          -Direction Outbound `
          -Action Block `
          -Enabled True `
          -Profile Any `
          -Protocol Any | Out-Null
        $continueGuard = -not (Test-Path -LiteralPath $releasePath -PathType Leaf) -and (Test-OwnerAlive $owner)
      }
      if ($continueGuard) {
        Assert-RuleInstalled $RuleName
        $continueGuard = -not (Test-Path -LiteralPath $releasePath -PathType Leaf) -and (Test-OwnerAlive $owner)
      }
      if ($continueGuard) {
        Write-ExclusiveMarker $readyPath "$RuleName`n"
        while ((Test-OwnerAlive $owner) -and -not (Test-Path -LiteralPath $releasePath -PathType Leaf)) {
          Start-Sleep -Milliseconds $cleanupPollMilliseconds
        }
      }
    } catch {
      $primaryError = $_
    } finally {
      try {
        Invoke-StableCleanup `
          { Remove-RuleByName $RuleName } `
          { Assert-RuleRemoved $RuleName } `
          { $false }
        Write-MarkerUnlessPresent $removedPath "$RuleName`n"
      } catch {
        $cleanupError = $_
      }
    }
    if ($null -ne $primaryError -or $null -ne $cleanupError) {
      Write-MarkerUnlessPresent $failedPath "guardian failed`n"
      if ($null -ne $cleanupError) {
        throw $cleanupError
      }
      throw $primaryError
    }
  }
  'CleanupExact' {
    Assert-RuleName $RuleName
    $statePath = Assert-StateDirectory $StateRoot $StateDirectory $RuleName
    Invoke-StableCleanup `
      { Remove-RuleByName $RuleName } `
      { Assert-RuleRemoved $RuleName } `
      { $false }
    Write-MarkerUnlessPresent (Join-Path $statePath 'removed') "$RuleName`n"
  }
  'CleanupAll' {
    $rootPath = Get-SafeFullPath $StateRoot 'state root'
    Assert-PlainDirectory $rootPath 'state root'
    Invoke-StableCleanup `
      { Remove-RuleGroup } `
      { Assert-GroupRemoved } `
      { Test-GuardianBlockers $rootPath }
  }
  'AuditRemoved' {
    Assert-RuleName $RuleName
    Assert-RuleRemoved $RuleName
  }
}
