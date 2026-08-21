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
$legacyMarkers = @(
  'rule-name',
  'owner.pid',
  'guardian.nonce',
  'guardian.pid',
  'release',
  'ready',
  'removed'
)

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

function Assert-NoLegacyRules {
  foreach ($store in @('ActiveStore', 'PersistentStore')) {
    if (@(Get-RuleByGroup $store).Count -ne 0) {
      throw "Windows native offline legacy rule group still exists in $store"
    }
  }
}

function Remove-LegacyRules {
  foreach ($store in @('ActiveStore', 'PersistentStore')) {
    $rules = @(Get-RuleByGroup $store)
    if ($rules.Count -gt 0) {
      $rules | Remove-NetFirewallRule -ErrorAction Stop
    }
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

function Assert-KnownLegacyStateRoot([string]$Root) {
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
    if ($entry.Name -cnotmatch $rulePattern) {
      throw 'Windows native offline legacy state root contains an unknown directory'
    }
    foreach ($leaf in @(Get-ChildItem -LiteralPath $entry.FullName -Force -ErrorAction Stop)) {
      if ($leaf.PSIsContainer -or ($leaf.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) {
        throw 'Windows native offline legacy state contains an unsafe child'
      }
      if ($legacyMarkers -notcontains $leaf.Name) {
        throw 'Windows native offline legacy state contains an unknown marker'
      }
    }
    $directories += $entry
  }
  return $directories
}

function Remove-KnownLegacyState([IO.DirectoryInfo[]]$Directories, [string]$Root) {
  foreach ($directory in $Directories) {
    $resolved = [IO.Path]::GetFullPath($directory.FullName)
    if (-not $resolved.StartsWith($Root + [IO.Path]::DirectorySeparatorChar, [StringComparison]::OrdinalIgnoreCase)) {
      throw 'Windows native offline legacy state escaped its root'
    }
    Remove-Item -LiteralPath $resolved -Recurse -Force -ErrorAction Stop
  }
}

function Invoke-LegacyCleanup([string]$Root) {
  $rootPath = Get-SafeFullPath $Root 'legacy state root'
  $scriptPath = [IO.Path]::GetFullPath($MyInvocation.MyCommand.Path)
  $deadline = [DateTime]::UtcNow.AddSeconds($DeadlineSeconds)
  do {
    $directories = @(Assert-KnownLegacyStateRoot $rootPath)
    $liveCreators = @(Get-LegacyGuardProcesses $scriptPath)
    if ($liveCreators.Count -ne 0) {
      throw 'Windows native offline legacy guardian process is still running'
    }
    Remove-LegacyRules
    Assert-NoLegacyRules
    Remove-KnownLegacyState $directories $rootPath
    if (-not (Test-Path -LiteralPath $rootPath) -or @(Get-ChildItem -LiteralPath $rootPath -Force -ErrorAction Stop).Count -eq 0) {
      return
    }
    Start-Sleep -Milliseconds 200
  } while ([DateTime]::UtcNow -lt $deadline)
  throw 'Windows native offline legacy cleanup did not converge before its deadline'
}

switch ($Action) {
  'LegacyCleanup' {
    Invoke-LegacyCleanup $StateRoot
    return
  }
}
