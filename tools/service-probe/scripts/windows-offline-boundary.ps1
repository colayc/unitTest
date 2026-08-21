[CmdletBinding()]
param(
  [Parameter(Mandatory = $true)]
  [ValidateSet('LegacyCleanup')]
  [string]$Action,

  [string]$StateRoot = '',

  [ValidateRange(1, 120)]
  [int]$DeadlineSeconds = 30
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

$ruleGroup = 'UnitTestIDE Native Offline Boundary'
$rulePattern = '^UnitTestIDE-NativeOffline-[0-9a-f]{16,64}$'
$stores = @('ActiveStore', 'PersistentStore')
$requiredMarkers = @(
  'rule-name',
  'owner.pid',
  'guardian.nonce',
  'guardian.pid',
  'release',
  'ready',
  'removed'
)
$markerMaximumBytes = 256
$utf8Strict = [Text.UTF8Encoding]::new($false, $true)

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

function Assert-PlainFile([string]$Path, [string]$Label) {
  $item = Get-Item -LiteralPath $Path -Force -ErrorAction Stop
  if ($item.PSIsContainer -or ($item.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) {
    throw "Windows native offline $Label file is unsafe"
  }
}

function New-ItemSnapshot([string]$Path, [string]$Label, [switch]$Directory) {
  $item = Get-Item -LiteralPath $Path -Force -ErrorAction Stop
  if ($Directory) {
    if (-not $item.PSIsContainer -or ($item.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) {
      throw "Windows native offline $Label directory is unsafe"
    }
  } else {
    if ($item.PSIsContainer -or ($item.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) {
      throw "Windows native offline $Label file is unsafe"
    }
  }
  [pscustomobject]@{
    Path             = $item.FullName
    Attributes       = [int]$item.Attributes
    CreationTimeUtc  = $item.CreationTimeUtc.Ticks
    LastWriteTimeUtc = $item.LastWriteTimeUtc.Ticks
    Length           = if ($Directory) { -1 } else { [int64]$item.Length }
  }
}

function Assert-ItemSnapshot([object]$Snapshot, [string]$Label, [switch]$Directory) {
  $current = New-ItemSnapshot $Snapshot.Path $Label -Directory:$Directory
  foreach ($property in @('Path', 'Attributes', 'CreationTimeUtc', 'LastWriteTimeUtc', 'Length')) {
    if ($current.$property -ne $Snapshot.$property) {
      throw "Windows native offline $Label identity changed"
    }
  }
}

function Assert-StringField([string]$Actual, [string]$Expected, [string]$Label) {
  if ($Actual -cne $Expected) {
    throw "Windows native offline $Label must equal $Expected"
  }
}

function Read-CanonicalMarker([string]$Path, [string]$Label) {
  Assert-PlainFile $Path $Label
  $bytes = [IO.File]::ReadAllBytes($Path)
  if ($bytes.Length -le 0 -or $bytes.Length -gt $markerMaximumBytes) {
    throw "Windows native offline $Label marker size is invalid"
  }
  if (
    $bytes.Length -ge 3 -and
    $bytes[0] -eq 0xEF -and
    $bytes[1] -eq 0xBB -and
    $bytes[2] -eq 0xBF
  ) {
    throw "Windows native offline $Label marker must not contain a BOM"
  }
  $text = $utf8Strict.GetString($bytes)
  if ($text.Contains("`0") -or $text.Contains("`r")) {
    throw "Windows native offline $Label marker is invalid"
  }
  if (-not $text.EndsWith("`n")) {
    throw "Windows native offline $Label marker must end with LF"
  }
  if ($text.IndexOf("`n") -ne ($text.Length - 1)) {
    throw "Windows native offline $Label marker must contain a single canonical line"
  }
  return $text
}

function Parse-PositiveUInt32([string]$Value, [string]$Pattern, [string]$Label) {
  if ($Value -cnotmatch $Pattern) {
    throw "Windows native offline $Label marker is invalid"
  }
  [uint64]$parsed = $Matches[1]
  if ($parsed -le 0 -or $parsed -gt [uint32]::MaxValue) {
    throw "Windows native offline $Label marker is invalid"
  }
  return [uint32]$parsed
}

function New-AuditedMarker([string]$Path, [string]$Label, [string]$ExpectedContent) {
  $snapshot = New-ItemSnapshot $Path $Label
  $content = Read-CanonicalMarker $Path $Label
  Assert-StringField $content $ExpectedContent "$Label content"
  [pscustomobject]@{
    Name            = Split-Path -Leaf $Path
    Path            = $snapshot.Path
    ExpectedContent = $ExpectedContent
    Snapshot        = $snapshot
  }
}

function Assert-AuditedMarkerUnchanged([object]$Marker) {
  Assert-ItemSnapshot $Marker.Snapshot $Marker.Name
  Assert-StringField (Read-CanonicalMarker $Marker.Path $Marker.Name) $Marker.ExpectedContent "$($Marker.Name) content"
}

function Get-RuleByGroup([string]$Store) {
  try {
    @(Get-NetFirewallRule -PolicyStore $Store -Group $ruleGroup -ErrorAction Stop)
  } catch {
    if ($_.FullyQualifiedErrorId -like 'CmdletizationQuery_NotFound*') {
      return @()
    }
    throw
  }
}

function Get-LegacyGuardProcesses([string]$ScriptPath) {
  $normalized = $ScriptPath.ToLowerInvariant()
  @(Get-CimInstance Win32_Process -ErrorAction Stop | Where-Object {
    $commandLine = $_.CommandLine
    if ([string]::IsNullOrWhiteSpace($commandLine)) {
      return $false
    }
    $lower = $commandLine.ToLowerInvariant()
    $lower.Contains($normalized) -and $lower.Contains('-action guard')
  })
}

function Audit-LegacyStateDirectory([IO.DirectoryInfo]$Directory) {
  Assert-PlainDirectory $Directory.FullName "legacy state directory $($Directory.Name)"
  if ($Directory.Name -cnotmatch $rulePattern) {
    throw 'Windows native offline legacy state root contains an unknown directory'
  }

  $markers = @{}
  foreach ($entry in @(Get-ChildItem -LiteralPath $Directory.FullName -Force -ErrorAction Stop)) {
    if ($entry.PSIsContainer -or ($entry.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) {
      throw 'Windows native offline legacy state contains an unsafe child'
    }
    if ($requiredMarkers -notcontains $entry.Name) {
      throw 'Windows native offline legacy state contains an unknown marker'
    }
    if ($markers.ContainsKey($entry.Name)) {
      throw 'Windows native offline legacy state contains a duplicate marker'
    }
    $markers[$entry.Name] = $entry.FullName
  }

  foreach ($marker in $requiredMarkers) {
    if (-not $markers.ContainsKey($marker)) {
      throw "Windows native offline legacy state marker $marker is missing"
    }
  }
  if ($markers.Count -ne $requiredMarkers.Count) {
    throw 'Windows native offline legacy state marker set is invalid'
  }

  $ruleName = $Directory.Name
  $directorySnapshot = New-ItemSnapshot $Directory.FullName "legacy state directory $ruleName" -Directory
  $ruleNameContent = "rule=$ruleName`n"
  $ownerPidContent = Read-CanonicalMarker $markers['owner.pid'] 'owner.pid'
  $guardianNonceLine = Read-CanonicalMarker $markers['guardian.nonce'] 'guardian.nonce'
  if ($guardianNonceLine -cnotmatch '^nonce=([0-9a-f]{64})\n$') {
    throw 'Windows native offline guardian.nonce marker is invalid'
  }
  $guardianNonce = $Matches[1]
  $guardianPidContent = Read-CanonicalMarker $markers['guardian.pid'] 'guardian.pid'
  $ownerPid = Parse-PositiveUInt32 $ownerPidContent '^owner=([1-9][0-9]{0,9})\n$' 'owner.pid'
  $guardianPid = Parse-PositiveUInt32 $guardianPidContent '^([1-9][0-9]{0,9})\n$' 'guardian.pid'
  $auditedMarkers = @(
    New-AuditedMarker $markers['rule-name'] 'rule-name' $ruleNameContent
    New-AuditedMarker $markers['owner.pid'] 'owner.pid' $ownerPidContent
    New-AuditedMarker $markers['guardian.nonce'] 'guardian.nonce' $guardianNonceLine
    New-AuditedMarker $markers['guardian.pid'] 'guardian.pid' $guardianPidContent
    New-AuditedMarker $markers['release'] 'release' "release=$ruleName`n"
    New-AuditedMarker $markers['ready'] 'ready' "ready=$ruleName`n"
    New-AuditedMarker $markers['removed'] 'removed' "removed=$ruleName`n"
  )

  [pscustomobject]@{
    RuleName    = $ruleName
    Path        = $Directory.FullName
    Snapshot    = $directorySnapshot
    OwnerPID    = $ownerPid
    GuardianPID = $guardianPid
    Nonce       = $guardianNonce
    Markers     = $auditedMarkers
  }
}

function Audit-LegacyStateRoot([string]$Root) {
  if (-not (Test-Path -LiteralPath $Root)) {
    return @()
  }
  Assert-PlainDirectory $Root 'legacy state root'
  $directories = @()
  foreach ($entry in @(Get-ChildItem -LiteralPath $Root -Force -ErrorAction Stop)) {
    if (($entry.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) {
      throw 'Windows native offline legacy state root contains a reparse entry'
    }
    if (-not $entry.PSIsContainer) {
      throw 'Windows native offline legacy state root contains an unknown file'
    }
    $directories += Audit-LegacyStateDirectory $entry
  }
  return @($directories | Sort-Object RuleName)
}

function Assert-ClosedFilters([object]$Rule, [string]$Store) {
  $application = @($Rule | Get-NetFirewallApplicationFilter -ErrorAction Stop)
  if ($application.Count -ne 1) {
    throw "Windows native offline legacy application filter audit failed in $Store"
  }
  Assert-StringField ([string]$application[0].Program) 'Any' "$Store application program"
  Assert-StringField ([string]$application[0].Package) 'Any' "$Store application package"

  $address = @($Rule | Get-NetFirewallAddressFilter -ErrorAction Stop)
  if ($address.Count -ne 1) {
    throw "Windows native offline legacy address filter audit failed in $Store"
  }
  Assert-StringField ([string]$address[0].LocalAddress) 'Any' "$Store local address"
  Assert-StringField ([string]$address[0].RemoteAddress) 'Any' "$Store remote address"

  $port = @($Rule | Get-NetFirewallPortFilter -ErrorAction Stop)
  if ($port.Count -ne 1) {
    throw "Windows native offline legacy port filter audit failed in $Store"
  }
  Assert-StringField ([string]$port[0].Protocol) 'Any' "$Store protocol"
  Assert-StringField ([string]$port[0].LocalPort) 'Any' "$Store local port"
  Assert-StringField ([string]$port[0].RemotePort) 'Any' "$Store remote port"

  $service = @($Rule | Get-NetFirewallServiceFilter -ErrorAction Stop)
  if ($service.Count -ne 1) {
    throw "Windows native offline legacy service filter audit failed in $Store"
  }
  Assert-StringField ([string]$service[0].Service) 'Any' "$Store service"

  $interface = @($Rule | Get-NetFirewallInterfaceFilter -ErrorAction Stop)
  if ($interface.Count -ne 1) {
    throw "Windows native offline legacy interface filter audit failed in $Store"
  }
  Assert-StringField ([string]$interface[0].InterfaceAlias) 'Any' "$Store interface alias"

  $interfaceType = @($Rule | Get-NetFirewallInterfaceTypeFilter -ErrorAction Stop)
  if ($interfaceType.Count -ne 1) {
    throw "Windows native offline legacy interface-type filter audit failed in $Store"
  }
  Assert-StringField ([string]$interfaceType[0].InterfaceType) 'Any' "$Store interface type"
}

function Assert-ClosedRule([object]$Rule, [string]$RuleName, [string]$Store) {
  Assert-StringField ([string]$Rule.Name) $RuleName "$Store rule name"
  Assert-StringField ([string]$Rule.DisplayName) $RuleName "$Store display name"
  Assert-StringField ([string]$Rule.Enabled) 'True' "$Store enabled"
  Assert-StringField ([string]$Rule.Direction) 'Outbound' "$Store direction"
  Assert-StringField ([string]$Rule.Action) 'Block' "$Store action"
  Assert-StringField ([string]$Rule.Profile) 'Any' "$Store profile"
  Assert-StringField ([string]$Rule.Group) $ruleGroup "$Store group"
  Assert-ClosedFilters $Rule $Store
}

function Audit-StoreRules([string]$Store, [object[]]$StateDirectories) {
  $rules = @(Get-RuleByGroup $Store)
  if ($rules.Count -ne $StateDirectories.Count) {
    throw "Windows native offline legacy rule audit failed in $Store"
  }
  $ruleMap = @{}
  foreach ($rule in $rules) {
    $name = [string]$rule.Name
    if ($name -cnotmatch $rulePattern) {
      throw "Windows native offline legacy rule name is invalid in $Store"
    }
    if ($ruleMap.ContainsKey($name)) {
      throw "Windows native offline legacy duplicate rule exists in $Store"
    }
    $ruleMap[$name] = $rule
  }

  $audited = @()
  foreach ($state in $StateDirectories) {
    $rule = $ruleMap[$state.RuleName]
    if ($null -eq $rule) {
      throw "Windows native offline legacy rule is missing in $Store"
    }
    Assert-ClosedRule $rule $state.RuleName $Store
    $audited += $rule
  }
  return $audited
}

function Remove-AuditedState([object[]]$StateDirectories, [string]$Root) {
  foreach ($state in $StateDirectories) {
    $resolved = [IO.Path]::GetFullPath($state.Path)
    if (-not $resolved.StartsWith($Root + [IO.Path]::DirectorySeparatorChar, [StringComparison]::OrdinalIgnoreCase)) {
      throw 'Windows native offline legacy state escaped its root'
    }
    Assert-ItemSnapshot $state.Snapshot "legacy state directory $($state.RuleName)" -Directory
    foreach ($marker in $state.Markers) {
      Assert-AuditedMarkerUnchanged $marker
      Remove-Item -LiteralPath $marker.Path -Force -ErrorAction Stop
    }
    Assert-PlainDirectory $resolved "legacy state directory $($state.RuleName)"
    if (@(Get-ChildItem -LiteralPath $resolved -Force -ErrorAction Stop).Count -ne 0) {
      throw 'Windows native offline legacy state directory is not empty after exact marker cleanup'
    }
    Remove-Item -LiteralPath $resolved -Force -ErrorAction Stop
  }
}

function Assert-ResidualStateAbsent([string]$Root) {
  if (-not (Test-Path -LiteralPath $Root)) {
    return
  }
  Assert-PlainDirectory $Root 'legacy state root'
  if (@(Get-ChildItem -LiteralPath $Root -Force -ErrorAction Stop).Count -ne 0) {
    throw 'Windows native offline legacy state root is not empty after cleanup'
  }
}

function Assert-NoLegacyRules {
  foreach ($store in $stores) {
    if (@(Get-RuleByGroup $store).Count -ne 0) {
      throw "Windows native offline legacy rule group still exists in $store"
    }
  }
}

function Invoke-LegacyCleanup([string]$Root) {
  $rootPath = Get-SafeFullPath $Root 'legacy state root'
  $scriptPath = [IO.Path]::GetFullPath($PSCommandPath)

  $stateDirectories = @(Audit-LegacyStateRoot $rootPath)
  if (@(Get-LegacyGuardProcesses $scriptPath).Count -ne 0) {
    throw 'Windows native offline legacy guardian process is still running'
  }

  $auditedRulesByStore = @{}
  foreach ($store in $stores) {
    $auditedRulesByStore[$store] = @(Audit-StoreRules $store $stateDirectories)
  }

  foreach ($store in $stores) {
    foreach ($rule in $auditedRulesByStore[$store]) {
      $rule | Remove-NetFirewallRule -ErrorAction Stop
    }
  }
  Assert-NoLegacyRules

  $confirmedStateDirectories = @(Audit-LegacyStateRoot $rootPath)
  if ($confirmedStateDirectories.Count -ne $stateDirectories.Count) {
    throw 'Windows native offline legacy state changed before exact cleanup'
  }
  for ($index = 0; $index -lt $stateDirectories.Count; $index++) {
    $expected = $stateDirectories[$index]
    $actual = $confirmedStateDirectories[$index]
    foreach ($property in @('RuleName', 'OwnerPID', 'GuardianPID', 'Nonce')) {
      if ($expected.$property -cne $actual.$property) {
        throw 'Windows native offline legacy state changed before exact cleanup'
      }
    }
  }

  Remove-AuditedState $confirmedStateDirectories $rootPath
  Assert-ResidualStateAbsent $rootPath
  Assert-NoLegacyRules
}

switch ($Action) {
  'LegacyCleanup' {
    Invoke-LegacyCleanup $StateRoot
    return
  }
}
