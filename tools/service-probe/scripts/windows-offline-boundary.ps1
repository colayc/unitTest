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
$markerMaximumBytes = 256
$stateMarkerNames = @('rule-name', 'owner.pid', 'guardian.pid', 'release', 'ready', 'removed')

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

function Assert-ClosedRule([object]$Rule, [string]$Name, [string]$Store) {
  if (
    $Rule.Name -cne $Name -or
    $Rule.DisplayName -cne $Name -or
    $Rule.Enabled.ToString() -ne 'True' -or
    $Rule.Direction.ToString() -ne 'Outbound' -or
    $Rule.Action.ToString() -ne 'Block' -or
    $Rule.Profile.ToString() -ne 'Any' -or
    $Rule.Group -ne $ruleGroup
  ) {
    throw "Windows native offline firewall rule has unexpected $Store policy"
  }
}

function Assert-ClosedFilters([object]$Rule, [string]$Store) {
  $application = @($Rule | Get-NetFirewallApplicationFilter)
  $address = @($Rule | Get-NetFirewallAddressFilter)
  $port = @($Rule | Get-NetFirewallPortFilter)
  $service = @($Rule | Get-NetFirewallServiceFilter)
  $interface = @($Rule | Get-NetFirewallInterfaceFilter)
  $interfaceType = @($Rule | Get-NetFirewallInterfaceTypeFilter)
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
    throw "Windows native offline firewall $Store filters are not closed"
  }
}

function Assert-RuleInstalled([string]$Name) {
  $activeRules = @(Get-RuleByName 'ActiveStore' $Name)
  $persistentRules = @(Get-RuleByName 'PersistentStore' $Name)
  if ($activeRules.Count -ne 1 -or $persistentRules.Count -ne 1) {
    throw 'Windows native offline firewall rule is not unique in both policy stores'
  }
  Assert-ClosedRule $activeRules[0] $Name 'ActiveStore'
  Assert-ClosedRule $persistentRules[0] $Name 'PersistentStore'

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

  Assert-ClosedFilters $activeRules[0] 'ActiveStore'
  Assert-ClosedFilters $persistentRules[0] 'PersistentStore'
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
    $actual = Get-PlainMarkerContent $Path ([IO.Path]::GetFileName($Path))
    if ($actual -cne $Content) {
      throw 'Windows native offline marker content is invalid'
    }
  }
}

function Get-PlainMarkerContent([string]$Path, [string]$Label) {
  $item = Get-Item -LiteralPath $Path -Force -ErrorAction Stop
  if (
    $item.PSIsContainer -or
    ($item.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0 -or
    $item.Length -gt $markerMaximumBytes
  ) {
    throw "Windows native offline $Label marker is unsafe"
  }
  $bytes = [IO.File]::ReadAllBytes($item.FullName)
  if ($bytes.Length -gt $markerMaximumBytes) {
    throw "Windows native offline $Label marker is too large"
  }
  try {
    [Text.UTF8Encoding]::new($false, $true).GetString($bytes)
  } catch {
    throw "Windows native offline $Label marker is not canonical UTF-8"
  }
}

function Get-ClosedStateLeaves([string]$Path, [string[]]$AllowedNames) {
  $leaves = [Collections.Generic.Dictionary[string, IO.FileInfo]]::new(
    [StringComparer]::Ordinal
  )
  foreach ($item in @(Get-ChildItem -LiteralPath $Path -Force -ErrorAction Stop)) {
    if (
      $item.PSIsContainer -or
      ($item.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0 -or
      -not ($AllowedNames -ccontains $item.Name) -or
      $leaves.ContainsKey($item.Name)
    ) {
      throw 'Windows native offline guardian state contains an unsafe or unexpected leaf'
    }
    $leaves.Add($item.Name, $item)
  }
  return ,$leaves
}

function Assert-RequiredMarker(
  [Collections.Generic.Dictionary[string, IO.FileInfo]]$Leaves,
  [string]$Name,
  [string]$Expected
) {
  if (-not $Leaves.ContainsKey($Name)) {
    throw "Windows native offline guardian state is missing $Name"
  }
  $actual = Get-PlainMarkerContent $Leaves[$Name].FullName $Name
  if ($actual -cne $Expected) {
    throw "Windows native offline $Name marker content is invalid"
  }
}

function Assert-OptionalMarker(
  [Collections.Generic.Dictionary[string, IO.FileInfo]]$Leaves,
  [string]$Name,
  [string]$Expected
) {
  if (-not $Leaves.ContainsKey($Name)) {
    return
  }
  $actual = Get-PlainMarkerContent $Leaves[$Name].FullName $Name
  if ($actual -cne $Expected) {
    throw "Windows native offline $Name marker content is invalid"
  }
}

function ConvertFrom-CanonicalPid([string]$Content, [string]$Prefix, [string]$Label) {
  $match = [regex]::Match($Content, "\A$([regex]::Escape($Prefix))([1-9][0-9]{0,9})`n\z")
  $value = 0
  if (
    -not $match.Success -or
    -not [int]::TryParse($match.Groups[1].Value, [ref]$value) -or
    $value -le 0
  ) {
    throw "Windows native offline $Label PID marker is invalid"
  }
  $value
}

function Assert-CanonicalGuardianState(
  [string]$Path,
  [string]$Name,
  [string[]]$AllowedNames,
  [int]$ExpectedOwnerPid = 0
) {
  $leaves = Get-ClosedStateLeaves $Path $AllowedNames
  Assert-RequiredMarker $leaves 'rule-name' "rule=$Name`n"
  if (-not $leaves.ContainsKey('owner.pid')) {
    throw 'Windows native offline guardian state is missing owner.pid'
  }
  $ownerPid = ConvertFrom-CanonicalPid (
    Get-PlainMarkerContent $leaves['owner.pid'].FullName 'owner.pid'
  ) 'owner=' 'owner'
  if ($ExpectedOwnerPid -gt 0 -and $ownerPid -ne $ExpectedOwnerPid) {
    throw 'Windows native offline owner PID marker does not match the guardian owner'
  }

  $guardianPid = 0
  if ($leaves.ContainsKey('guardian.pid')) {
    $guardianPid = ConvertFrom-CanonicalPid (
      Get-PlainMarkerContent $leaves['guardian.pid'].FullName 'guardian.pid'
    ) '' 'guardian'
  }
  Assert-OptionalMarker $leaves 'release' "release=$Name`n"
  Assert-OptionalMarker $leaves 'ready' "ready=$Name`n"
  Assert-OptionalMarker $leaves 'removed' "removed=$Name`n"
  [pscustomobject]@{
    Leaves = $leaves
    OwnerPid = $ownerPid
    GuardianPid = $guardianPid
  }
}

function Test-CanonicalMarker([string]$Path, [string]$Expected, [string]$Label) {
  if (-not (Test-Path -LiteralPath $Path)) {
    return $false
  }
  $actual = Get-PlainMarkerContent $Path $Label
  if ($actual -cne $Expected) {
    throw "Windows native offline $Label marker content is invalid"
  }
  $true
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
  ) "release=$($Directory.Name)`n"
}

function Test-CorrespondingGuardianAlive(
  [int]$GuardianPid,
  [int]$OwnerPid,
  [string]$StatePath,
  [string]$Name
) {
  try {
    $process = Get-Process -Id $GuardianPid -ErrorAction Stop
  } catch {
    if ($_.FullyQualifiedErrorId -like 'NoProcessFoundForGivenId*') {
      return $false
    }
    throw
  }

  $expectedPowerShell = Join-Path $env:SystemRoot 'System32\WindowsPowerShell\v1.0\powershell.exe'
  if (
    [string]::IsNullOrWhiteSpace($process.Path) -or
    -not $process.Path.Equals($expectedPowerShell, [StringComparison]::OrdinalIgnoreCase)
  ) {
    throw 'Windows native offline guardian PID does not identify Windows PowerShell'
  }
  $records = @(Get-CimInstance -ClassName Win32_Process -Filter "ProcessId = $GuardianPid" -ErrorAction Stop)
  if ($records.Count -eq 0) {
    return $false
  }
  if ($records.Count -ne 1 -or [string]::IsNullOrWhiteSpace($records[0].CommandLine)) {
    throw 'Windows native offline guardian process identity is not auditable'
  }
  $commandLine = $records[0].CommandLine
  $actionPattern = '(?i)(?:^|\s)-Action\s+"?Guard"?(?:\s|$)'
  $rulePatternExact = "(?i)(?:^|\s)-RuleName\s+`"?$([regex]::Escape($Name))`"?(?:\s|$)"
  $ownerPattern = "(?i)(?:^|\s)-OwnerPid\s+`"?$OwnerPid`"?(?:\s|$)"
  $statePattern = "(?i)(?:^|\s)-StateDirectory\s+`"?$([regex]::Escape($StatePath))`"?(?:\s|$)"
  if (
    $commandLine -notmatch $actionPattern -or
    $commandLine -notmatch $rulePatternExact -or
    $commandLine -notmatch $ownerPattern -or
    $commandLine -notmatch $statePattern
  ) {
    throw 'Windows native offline guardian PID identifies an unrelated process'
  }
  $true
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
    $state = Assert-CanonicalGuardianState $directory.FullName $directory.Name $stateMarkerNames
    Request-GuardianRelease $directory
    $state = Assert-CanonicalGuardianState $directory.FullName $directory.Name $stateMarkerNames
    if ($state.GuardianPid -le 0) {
      # The guardian may have been spawned but not scheduled yet. Until it
      # writes its PID it remains a possible late creator.
      $hasBlocker = $true
      continue
    }
    if (
      Test-CorrespondingGuardianAlive `
        $state.GuardianPid `
        $state.OwnerPid `
        $directory.FullName `
        $directory.Name
    ) {
      $hasBlocker = $true
      continue
    }
    if (-not $state.Leaves.ContainsKey('removed')) {
      throw 'Windows native offline guardian exited without canonical removal proof'
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
    Assert-CanonicalGuardianState `
      $statePath `
      $RuleName `
      @('rule-name', 'owner.pid', 'release') `
      $OwnerPid | Out-Null
    Write-ExclusiveMarker $pidPath "$PID`n"
    Assert-CanonicalGuardianState `
      $statePath `
      $RuleName `
      @('rule-name', 'owner.pid', 'guardian.pid', 'release') `
      $OwnerPid | Out-Null

    $primaryError = $null
    $cleanupError = $null
    try {
      $owner = Get-Process -Id $OwnerPid -ErrorAction Stop
      Assert-RuleRemoved $RuleName
      $continueGuard = -not (
        Test-CanonicalMarker $releasePath "release=$RuleName`n" 'release'
      ) -and (Test-OwnerAlive $owner)
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
        $state = Assert-CanonicalGuardianState `
          $statePath `
          $RuleName `
          @('rule-name', 'owner.pid', 'guardian.pid', 'release', 'removed') `
          $OwnerPid
        $continueGuard = `
          -not $state.Leaves.ContainsKey('release') -and `
          -not $state.Leaves.ContainsKey('removed') -and `
          (Test-OwnerAlive $owner)
      }
      if ($continueGuard) {
        Assert-RuleInstalled $RuleName
        $state = Assert-CanonicalGuardianState `
          $statePath `
          $RuleName `
          @('rule-name', 'owner.pid', 'guardian.pid', 'release', 'removed') `
          $OwnerPid
        $continueGuard = `
          -not $state.Leaves.ContainsKey('release') -and `
          -not $state.Leaves.ContainsKey('removed') -and `
          (Test-OwnerAlive $owner)
      }
      if ($continueGuard) {
        Write-ExclusiveMarker $readyPath "ready=$RuleName`n"
        Assert-CanonicalGuardianState `
          $statePath `
          $RuleName `
          @('rule-name', 'owner.pid', 'guardian.pid', 'release', 'ready') `
          $OwnerPid | Out-Null
        while (
          (Test-OwnerAlive $owner) -and
          -not (Test-CanonicalMarker $releasePath "release=$RuleName`n" 'release')
        ) {
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
        Write-MarkerUnlessPresent $removedPath "removed=$RuleName`n"
      } catch {
        $cleanupError = $_
      }
    }
    if ($null -ne $primaryError -or $null -ne $cleanupError) {
      if ($null -ne $cleanupError) {
        throw $cleanupError
      }
      throw $primaryError
    }
  }
  'CleanupExact' {
    Assert-RuleName $RuleName
    $statePath = Assert-StateDirectory $StateRoot $StateDirectory $RuleName
    Assert-CanonicalGuardianState $statePath $RuleName $stateMarkerNames | Out-Null
    Invoke-StableCleanup `
      { Remove-RuleByName $RuleName } `
      { Assert-RuleRemoved $RuleName } `
      { $false }
    Write-MarkerUnlessPresent (
      Join-Path $statePath 'removed'
    ) "removed=$RuleName`n"
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
