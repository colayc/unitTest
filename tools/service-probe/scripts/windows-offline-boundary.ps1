[CmdletBinding()]
param(
  [Parameter(Mandatory = $true)]
  [ValidateSet('Guard', 'CleanupExact', 'CleanupAll', 'AuditRemoved')]
  [string]$Action,

  [string]$RuleName = '',

  [int]$OwnerPid = 0,

  [string]$GuardianNonce = '',

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
$boundaryScriptPath = [IO.Path]::GetFullPath($MyInvocation.MyCommand.Path)
$stateMarkerNames = @(
  'rule-name',
  'owner.pid',
  'guardian.nonce',
  'guardian.pid',
  'release',
  'ready',
  'removed'
)

function Assert-RuleName([string]$Name) {
  if ($Name -cnotmatch $rulePattern) {
    throw 'Windows native offline firewall rule name is invalid'
  }
}

function Assert-GuardianNonce([string]$Nonce) {
  if ($Nonce -cnotmatch '^[0-9a-f]{64}$') {
    throw 'Windows native offline guardian nonce is invalid'
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
  [int]$ExpectedOwnerPid = 0,
  [string]$ExpectedGuardianNonce = ''
) {
  Assert-PlainDirectory $Path 'guardian state'
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
  if (-not $leaves.ContainsKey('guardian.nonce')) {
    throw 'Windows native offline guardian state is missing guardian.nonce'
  }
  $guardianNonceContent = Get-PlainMarkerContent (
    $leaves['guardian.nonce'].FullName
  ) 'guardian.nonce'
  $guardianNonceMatch = [regex]::Match(
    $guardianNonceContent,
    '\Anonce=([0-9a-f]{64})\n\z'
  )
  if (-not $guardianNonceMatch.Success) {
    throw 'Windows native offline guardian nonce marker is invalid'
  }
  $guardianNonce = $guardianNonceMatch.Groups[1].Value
  if (
    -not [string]::IsNullOrEmpty($ExpectedGuardianNonce) -and
    $guardianNonce -cne $ExpectedGuardianNonce
  ) {
    throw 'Windows native offline guardian nonce marker does not match the guardian command'
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
    GuardianNonce = $guardianNonce
    GuardianPid = $guardianPid
  }
}

function Assert-GuardianOwnedState(
  [string]$Path,
  [string]$Name,
  [string[]]$AllowedNames,
  [int]$ExpectedOwnerPid,
  [string]$ExpectedGuardianNonce,
  [int]$ExpectedGuardianPid
) {
  $state = Assert-CanonicalGuardianState `
    $Path `
    $Name `
    $AllowedNames `
    $ExpectedOwnerPid `
    $ExpectedGuardianNonce
  if ($state.GuardianPid -ne $ExpectedGuardianPid) {
    throw 'Windows native offline guardian PID marker does not match the guardian process'
  }
  $state
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

function Split-GuardianCommandLine([string]$CommandLine) {
  if ([string]::IsNullOrWhiteSpace($CommandLine)) {
    throw 'Windows native offline guardian process command line is unavailable'
  }
  $arguments = [Collections.Generic.List[string]]::new()
  $matches = [regex]::Matches($CommandLine, '(?:"([^"]*)"|([^\s"]+))')
  $cursor = 0
  foreach ($match in $matches) {
    if ($CommandLine.Substring($cursor, $match.Index - $cursor) -notmatch '^\s*$') {
      throw 'Windows native offline guardian process command line is not canonical'
    }
    if ($match.Groups[1].Success) {
      $arguments.Add($match.Groups[1].Value)
    } else {
      $arguments.Add($match.Groups[2].Value)
    }
    $cursor = $match.Index + $match.Length
  }
  if (
    $arguments.Count -eq 0 -or
    $CommandLine.Substring($cursor) -notmatch '^\s*$'
  ) {
    throw 'Windows native offline guardian process command line is not canonical'
  }
  return $arguments.ToArray()
}

function Get-GuardianArgumentValues([string[]]$Arguments, [string]$Name) {
  $values = [Collections.Generic.List[string]]::new()
  for ($index = 0; $index -lt $Arguments.Count; $index++) {
    if (-not $Arguments[$index].Equals($Name, [StringComparison]::OrdinalIgnoreCase)) {
      continue
    }
    if ($index + 1 -ge $Arguments.Count) {
      throw "Windows native offline guardian process is missing the $Name value"
    }
    $values.Add($Arguments[$index + 1])
  }
  return $values.ToArray()
}

function Get-SingleGuardianArgument([string[]]$Arguments, [string]$Name) {
  $values = @(Get-GuardianArgumentValues $Arguments $Name)
  if ($values.Count -ne 1) {
    throw "Windows native offline guardian process must bind exactly one $Name"
  }
  $values[0]
}

function Test-GuardianScriptArgument([string[]]$Arguments) {
  $fileValues = @(Get-GuardianArgumentValues $Arguments '-File')
  $boundaryValues = @(Get-GuardianArgumentValues $Arguments '-BoundaryScript')
  $matchingValues = 0
  foreach ($candidate in @($fileValues + $boundaryValues)) {
    if (-not [IO.Path]::IsPathRooted($candidate)) {
      continue
    }
    try {
      $candidatePath = [IO.Path]::GetFullPath($candidate)
    } catch {
      continue
    }
    if ($candidatePath.Equals($boundaryScriptPath, [StringComparison]::OrdinalIgnoreCase)) {
      $matchingValues++
    }
  }
  if ($matchingValues -gt 1) {
    throw 'Windows native offline guardian process binds the boundary script more than once'
  }
  $matchingValues -eq 1
}

function Initialize-GuardianProcessInspector {
  if ($null -ne ('UnitTestIDE.GuardianProcessInspector' -as [type])) {
    return
  }
  Add-Type -TypeDefinition @'
using System;
using System.ComponentModel;
using System.Runtime.InteropServices;
using System.Text;

namespace UnitTestIDE {
  public sealed class GuardianProcessSnapshot {
    public int ProcessId { get; private set; }
    public string ExecutablePath { get; private set; }
    public string CommandLine { get; private set; }

    public GuardianProcessSnapshot(int processId, string executablePath, string commandLine) {
      ProcessId = processId;
      ExecutablePath = executablePath;
      CommandLine = commandLine;
    }
  }

  public static class GuardianProcessInspector {
    private const uint ProcessQueryInformation = 0x0400;
    private const uint ProcessQueryLimitedInformation = 0x1000;
    private const int ProcessCommandLineInformation = 60;

    [DllImport("kernel32.dll", SetLastError = true)]
    private static extern IntPtr OpenProcess(uint access, bool inheritHandle, int processId);

    [DllImport("kernel32.dll", SetLastError = true)]
    private static extern bool CloseHandle(IntPtr handle);

    [DllImport("kernel32.dll", CharSet = CharSet.Unicode, SetLastError = true)]
    private static extern bool QueryFullProcessImageName(
      IntPtr process,
      int flags,
      StringBuilder executablePath,
      ref int size
    );

    [DllImport("ntdll.dll")]
    private static extern int NtQueryInformationProcess(
      IntPtr process,
      int informationClass,
      IntPtr information,
      int informationLength,
      out int returnLength
    );

    public static GuardianProcessSnapshot Inspect(int processId) {
      IntPtr process = OpenProcess(
        ProcessQueryInformation | ProcessQueryLimitedInformation,
        false,
        processId
      );
      if (process == IntPtr.Zero) {
        int error = Marshal.GetLastWin32Error();
        if (error == 87) {
          return null;
        }
        throw new Win32Exception(error, "cannot open PowerShell guardian process");
      }
      try {
        var executablePath = new StringBuilder(32768);
        int executableLength = executablePath.Capacity;
        if (!QueryFullProcessImageName(process, 0, executablePath, ref executableLength)) {
          throw new Win32Exception(
            Marshal.GetLastWin32Error(),
            "cannot query PowerShell guardian executable"
          );
        }

        int commandLineLength;
        NtQueryInformationProcess(
          process,
          ProcessCommandLineInformation,
          IntPtr.Zero,
          0,
          out commandLineLength
        );
        if (commandLineLength <= 0 || commandLineLength > 1024 * 1024) {
          throw new InvalidOperationException("PowerShell guardian command line length is invalid");
        }
        IntPtr buffer = Marshal.AllocHGlobal(commandLineLength);
        try {
          int returnedLength;
          int status = NtQueryInformationProcess(
            process,
            ProcessCommandLineInformation,
            buffer,
            commandLineLength,
            out returnedLength
          );
          if (status != 0 || returnedLength <= 0 || returnedLength > commandLineLength) {
            throw new InvalidOperationException(
              "cannot query PowerShell guardian command line (NTSTATUS " + status + ")"
            );
          }
          int textLength = (ushort)Marshal.ReadInt16(buffer, 0);
          int maximumLength = (ushort)Marshal.ReadInt16(buffer, 2);
          if (textLength <= 0 || textLength > maximumLength || textLength > commandLineLength) {
            throw new InvalidOperationException("PowerShell guardian command line is invalid");
          }
          IntPtr text = Marshal.ReadIntPtr(buffer, IntPtr.Size == 8 ? 8 : 4);
          string commandLine = Marshal.PtrToStringUni(text, textLength / 2);
          if (String.IsNullOrWhiteSpace(commandLine)) {
            throw new InvalidOperationException("PowerShell guardian command line is empty");
          }
          return new GuardianProcessSnapshot(
            processId,
            executablePath.ToString(),
            commandLine
          );
        } finally {
          Marshal.FreeHGlobal(buffer);
        }
      } finally {
        CloseHandle(process);
      }
    }
  }
}
'@ | Out-Null
}

function Get-MatchingGuardianProcesses([string]$Root) {
  $expectedPowerShell = Join-Path $env:SystemRoot 'System32\WindowsPowerShell\v1.0\powershell.exe'
  $guardians = [Collections.Generic.List[object]]::new()
  Initialize-GuardianProcessInspector
  foreach ($process in @(Get-Process -Name powershell -ErrorAction SilentlyContinue)) {
    $record = [UnitTestIDE.GuardianProcessInspector]::Inspect($process.Id)
    if ($null -eq $record) {
      continue
    }
    if (
      [string]::IsNullOrWhiteSpace($record.ExecutablePath) -or
      -not $record.ExecutablePath.Equals(
        $expectedPowerShell,
        [StringComparison]::OrdinalIgnoreCase
      )
    ) {
      continue
    }
    $arguments = Split-GuardianCommandLine $record.CommandLine
    if (-not (Test-GuardianScriptArgument $arguments)) {
      continue
    }
    $action = Get-SingleGuardianArgument $arguments '-Action'
    if ($action -cne 'Guard') {
      continue
    }
    $name = Get-SingleGuardianArgument $arguments '-RuleName'
    Assert-RuleName $name
    $ownerPid = ConvertFrom-CanonicalPid (
      "owner=$(Get-SingleGuardianArgument $arguments '-OwnerPid')`n"
    ) 'owner=' 'owner'
    $guardianNonce = Get-SingleGuardianArgument $arguments '-GuardianNonce'
    Assert-GuardianNonce $guardianNonce
    $stateRoot = Get-SafeFullPath (
      Get-SingleGuardianArgument $arguments '-StateRoot'
    ) 'guardian process state root'
    if (-not $stateRoot.Equals($Root, [StringComparison]::OrdinalIgnoreCase)) {
      continue
    }
    $stateDirectory = Get-SafeFullPath (
      Get-SingleGuardianArgument $arguments '-StateDirectory'
    ) 'guardian process state'
    if (
      -not [IO.Path]::GetDirectoryName($stateDirectory).Equals(
        $Root,
        [StringComparison]::OrdinalIgnoreCase
      ) -or
      [IO.Path]::GetFileName($stateDirectory) -cne $name
    ) {
      throw 'Windows native offline guardian process state escaped its canonical root'
    }
    $processId = 0
    if (
      -not [int]::TryParse($record.ProcessId.ToString(), [ref]$processId) -or
      $processId -le 0
    ) {
      throw 'Windows native offline guardian process PID is invalid'
    }
    $guardians.Add([pscustomobject]@{
      ProcessId = $processId
      OwnerPid = $ownerPid
      GuardianNonce = $guardianNonce
      RuleName = $name
      StateDirectory = $stateDirectory
    })
  }
  return $guardians.ToArray()
}

function Request-MatchingGuardianRelease([object]$Guardian) {
  try {
    $directory = Get-Item -LiteralPath $Guardian.StateDirectory -Force -ErrorAction Stop
  } catch [Management.Automation.ItemNotFoundException] {
    return
  }
  if (
    -not $directory.PSIsContainer -or
    ($directory.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0
  ) {
    return
  }
  Write-MarkerUnlessPresent (
    Join-Path $directory.FullName 'release'
  ) "release=$($Guardian.RuleName)`n"
}

function Get-ClosedGuardianDirectories([string]$Root) {
  Assert-PlainDirectory $Root 'state root'
  $directories = [Collections.Generic.List[IO.DirectoryInfo]]::new()
  foreach ($item in @(Get-ChildItem -LiteralPath $Root -Force -ErrorAction Stop)) {
    if (
      -not $item.PSIsContainer -or
      ($item.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0 -or
      $item.Name -cnotmatch $rulePattern
    ) {
      throw 'Windows native offline state root contains an unsafe or unexpected entry'
    }
    $directories.Add($item)
  }
  return $directories.ToArray()
}

function Test-GuardianBlockers([string]$Root) {
  $guardians = @(Get-MatchingGuardianProcesses $Root)
  $guardiansByState = [Collections.Generic.Dictionary[string, object]]::new(
    [StringComparer]::OrdinalIgnoreCase
  )
  foreach ($guardian in $guardians) {
    Request-MatchingGuardianRelease $guardian
    if ($guardiansByState.ContainsKey($guardian.StateDirectory)) {
      throw 'Windows native offline state has more than one matching guardian process'
    }
    $guardiansByState.Add($guardian.StateDirectory, $guardian)
  }

  $hasBlocker = $guardians.Count -gt 0
  foreach ($directory in @(Get-ClosedGuardianDirectories $Root)) {
    $expectedOwnerPid = 0
    $expectedGuardianNonce = ''
    $liveGuardian = $null
    if ($guardiansByState.ContainsKey($directory.FullName)) {
      $liveGuardian = $guardiansByState[$directory.FullName]
      $expectedOwnerPid = $liveGuardian.OwnerPid
      $expectedGuardianNonce = $liveGuardian.GuardianNonce
    }
    $state = Assert-CanonicalGuardianState `
      $directory.FullName `
      $directory.Name `
      $stateMarkerNames `
      $expectedOwnerPid `
      $expectedGuardianNonce
    if ($null -ne $liveGuardian -and $state.GuardianPid -ne $liveGuardian.ProcessId) {
      throw 'Windows native offline guardian PID marker does not match its command-bound process'
    }
    Request-GuardianRelease $directory
    $state = Assert-CanonicalGuardianState `
      $directory.FullName `
      $directory.Name `
      $stateMarkerNames `
      $expectedOwnerPid `
      $expectedGuardianNonce
    if ($null -ne $liveGuardian -and $state.GuardianPid -ne $liveGuardian.ProcessId) {
      throw 'Windows native offline guardian PID marker does not match its command-bound process'
    }
    if ($state.GuardianPid -le 0) {
      # The guardian may have been spawned but not scheduled yet. Until it
      # writes its PID it remains a possible late creator.
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
    Assert-GuardianNonce $GuardianNonce
    if ($OwnerPid -le 0) {
      throw 'Windows native offline guardian owner PID is invalid'
    }
    $statePath = Assert-StateDirectory $StateRoot $StateDirectory $RuleName
    $pidPath = Join-Path $statePath 'guardian.pid'
    $readyPath = Join-Path $statePath 'ready'
    $removedPath = Join-Path $statePath 'removed'
    Assert-CanonicalGuardianState `
      $statePath `
      $RuleName `
      @('rule-name', 'owner.pid', 'guardian.nonce', 'release') `
      $OwnerPid `
      $GuardianNonce | Out-Null
    Write-ExclusiveMarker $pidPath "$PID`n"
    Assert-GuardianOwnedState `
      $statePath `
      $RuleName `
      @('rule-name', 'owner.pid', 'guardian.nonce', 'guardian.pid', 'release') `
      $OwnerPid `
      $GuardianNonce `
      $PID | Out-Null

    $primaryError = $null
    $cleanupError = $null
    try {
      $owner = Get-Process -Id $OwnerPid -ErrorAction Stop
      Assert-RuleRemoved $RuleName
      $state = Assert-GuardianOwnedState `
        $statePath `
        $RuleName `
        @('rule-name', 'owner.pid', 'guardian.nonce', 'guardian.pid', 'release') `
        $OwnerPid `
        $GuardianNonce `
        $PID
      $continueGuard = `
        -not $state.Leaves.ContainsKey('release') -and `
        (Test-OwnerAlive $owner)
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
        $state = Assert-GuardianOwnedState `
          $statePath `
          $RuleName `
          @('rule-name', 'owner.pid', 'guardian.nonce', 'guardian.pid', 'release') `
          $OwnerPid `
          $GuardianNonce `
          $PID
        $continueGuard = `
          -not $state.Leaves.ContainsKey('release') -and `
          (Test-OwnerAlive $owner)
      }
      if ($continueGuard) {
        Assert-RuleInstalled $RuleName
        $state = Assert-GuardianOwnedState `
          $statePath `
          $RuleName `
          @('rule-name', 'owner.pid', 'guardian.nonce', 'guardian.pid', 'release') `
          $OwnerPid `
          $GuardianNonce `
          $PID
        $continueGuard = `
          -not $state.Leaves.ContainsKey('release') -and `
          (Test-OwnerAlive $owner)
      }
      if ($continueGuard) {
        Write-ExclusiveMarker $readyPath "ready=$RuleName`n"
        Assert-GuardianOwnedState `
          $statePath `
          $RuleName `
          @('rule-name', 'owner.pid', 'guardian.nonce', 'guardian.pid', 'release', 'ready') `
          $OwnerPid `
          $GuardianNonce `
          $PID | Out-Null
        while (Test-OwnerAlive $owner) {
          $state = Assert-GuardianOwnedState `
            $statePath `
            $RuleName `
            @('rule-name', 'owner.pid', 'guardian.nonce', 'guardian.pid', 'release', 'ready') `
            $OwnerPid `
            $GuardianNonce `
            $PID
          if ($state.Leaves.ContainsKey('release')) {
            break
          }
          Start-Sleep -Milliseconds $cleanupPollMilliseconds
        }
      }
    } catch {
      $primaryError = $_
    } finally {
      try {
        $transitionError = $null
        try {
          Assert-GuardianOwnedState `
            $statePath `
            $RuleName `
            @('rule-name', 'owner.pid', 'guardian.nonce', 'guardian.pid', 'release', 'ready') `
            $OwnerPid `
            $GuardianNonce `
            $PID | Out-Null
        } catch {
          $transitionError = $_
        }
        Invoke-StableCleanup `
          { Remove-RuleByName $RuleName } `
          { Assert-RuleRemoved $RuleName } `
          { $false }
        if ($null -ne $transitionError) {
          throw $transitionError
        }
        Assert-GuardianOwnedState `
          $statePath `
          $RuleName `
          @('rule-name', 'owner.pid', 'guardian.nonce', 'guardian.pid', 'release', 'ready') `
          $OwnerPid `
          $GuardianNonce `
          $PID | Out-Null
        Write-MarkerUnlessPresent $removedPath "removed=$RuleName`n"
        Assert-GuardianOwnedState `
          $statePath `
          $RuleName `
          @('rule-name', 'owner.pid', 'guardian.nonce', 'guardian.pid', 'release', 'ready', 'removed') `
          $OwnerPid `
          $GuardianNonce `
          $PID | Out-Null
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
    Assert-GuardianNonce $GuardianNonce
    $statePath = Assert-StateDirectory $StateRoot $StateDirectory $RuleName
    Assert-CanonicalGuardianState `
      $statePath `
      $RuleName `
      $stateMarkerNames `
      0 `
      $GuardianNonce | Out-Null
    Invoke-StableCleanup `
      { Remove-RuleByName $RuleName } `
      { Assert-RuleRemoved $RuleName } `
      { $false }
    Write-MarkerUnlessPresent (
      Join-Path $statePath 'removed'
    ) "removed=$RuleName`n"
    Assert-CanonicalGuardianState `
      $statePath `
      $RuleName `
      $stateMarkerNames `
      0 `
      $GuardianNonce | Out-Null
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
