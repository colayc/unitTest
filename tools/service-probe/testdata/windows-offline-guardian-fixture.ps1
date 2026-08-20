[CmdletBinding()]
param(
  [Parameter(Mandatory = $true)]
  [string]$BoundaryScript,

  [Parameter(Mandatory = $true)]
  [string]$Action,

  [Parameter(Mandatory = $true)]
  [string]$RuleName,

  [Parameter(Mandatory = $true)]
  [string]$StateRoot,

  [string]$StateDirectory = '',

  [int]$OwnerPid = 0,

  [string]$GuardianNonce = 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa',

  [ValidateSet('Valid', 'MissingProfile', 'ExtraProfile', 'DisabledProfile')]
  [string]$ProfileScenario = 'Valid',

  [ValidateSet('None', 'ActiveStore', 'PersistentStore')]
  [string]$TamperStore = 'None',

  [ValidateSet(
    'None',
    'Name',
    'DisplayName',
    'Enabled',
    'Direction',
    'Action',
    'Profile',
    'Group',
    'Application',
    'Address',
    'Port',
    'Service',
    'Interface',
    'InterfaceType'
  )]
  [string]$TamperField = 'None',

  [ValidateRange(0, 30000)]
  [int]$InstallDelayMilliseconds = 0,

  [ValidateRange(0, 30000)]
  [int]$PreInstallDelayMilliseconds = 0,

  [ValidateRange(0, 20)]
  [int]$RemoveFailures = 0,

  [ValidateRange(0, 100)]
  [int]$QueryFailures = 0,

  [ValidateRange(1, 30)]
  [int]$DeadlineSeconds = 5,

  [ValidatePattern('^[a-z0-9-]+\.log$')]
  [string]$TraceName = 'fixture-trace.log'
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

$script:installed = $Action -eq 'CleanupAll'
$script:removeAttempts = 0
$script:queryAttempts = 0
$script:preInstallPaused = $false
$tracePath = Join-Path ([IO.Path]::GetDirectoryName([IO.Path]::GetFullPath($StateRoot))) $TraceName

# The real Node launcher owns these three durable provenance markers. Keep the
# PowerShell fixture faithful so guardian state validation exercises the same
# handshake without replacing production behavior.
if ($Action -ceq 'Guard') {
  if ([string]::IsNullOrEmpty($StateDirectory) -or $OwnerPid -le 0) {
    throw 'fixture guardian state initialization is invalid'
  }
  $utf8NoBom = [Text.UTF8Encoding]::new($false)
  [IO.File]::WriteAllText(
    (Join-Path $StateDirectory 'rule-name'),
    "rule=$RuleName`n",
    $utf8NoBom
  )
  [IO.File]::WriteAllText(
    (Join-Path $StateDirectory 'owner.pid'),
    "owner=$OwnerPid`n",
    $utf8NoBom
  )
  if (-not [string]::IsNullOrEmpty($GuardianNonce)) {
    [IO.File]::WriteAllText(
      (Join-Path $StateDirectory 'guardian.nonce'),
      "nonce=$GuardianNonce`n",
      $utf8NoBom
    )
  }
}

function Add-FixtureTrace([string]$Message) {
  $bytes = [Text.UTF8Encoding]::new($false).GetBytes("$Message`n")
  $stream = [IO.File]::Open(
    $tracePath,
    [IO.FileMode]::OpenOrCreate,
    [IO.FileAccess]::Write,
    [IO.FileShare]::ReadWrite
  )
  try {
    $stream.Seek(0, [IO.SeekOrigin]::End) | Out-Null
    $stream.Write($bytes, 0, $bytes.Length)
    $stream.Flush()
  } finally {
    $stream.Dispose()
  }
}

function Test-FixtureTamper([string]$Store, [string]$Field) {
  $TamperStore -ceq $Store -and $TamperField -ceq $Field
}

function New-FixtureRule([string]$Store) {
  $rule = [ordered]@{
    Name = $RuleName
    DisplayName = $RuleName
    Enabled = 'True'
    Direction = 'Outbound'
    Action = 'Block'
    Profile = 'Any'
    Group = 'UnitTestIDE Native Offline Boundary'
    FixtureStore = $Store
  }
  if ($TamperStore -ceq $Store) {
    switch ($TamperField) {
      'Name' { $rule.Name = "$RuleName-tampered" }
      'DisplayName' { $rule.DisplayName = "$RuleName-tampered" }
      'Enabled' { $rule.Enabled = 'False' }
      'Direction' { $rule.Direction = 'Inbound' }
      'Action' { $rule.Action = 'Allow' }
      'Profile' { $rule.Profile = 'Public' }
      'Group' { $rule.Group = 'Tampered Group' }
    }
  }
  [pscustomobject]$rule
}

function Get-NetFirewallRule {
  [CmdletBinding()]
  param(
    [string]$PolicyStore,
    [string]$Name,
    [string]$Group
  )
  Add-FixtureTrace "rule-query:$PolicyStore"
  $script:queryAttempts++
  if ($script:queryAttempts -le $QueryFailures) {
    Add-FixtureTrace "query-error:$script:queryAttempts"
    throw "fixture firewall query failure $script:queryAttempts"
  }
  if (
    $Action -ceq 'Guard' -and
    -not $script:installed -and
    $PolicyStore -ceq 'PersistentStore' -and
    -not $script:preInstallPaused -and
    $PreInstallDelayMilliseconds -gt 0
  ) {
    $script:preInstallPaused = $true
    Add-FixtureTrace 'preinstall-audit-finished'
    Start-Sleep -Milliseconds $PreInstallDelayMilliseconds
  }
  if (-not $script:installed) {
    return
  }
  if (-not [string]::IsNullOrEmpty($Name) -and $Name -cne $RuleName) {
    return
  }
  if (
    -not [string]::IsNullOrEmpty($Group) -and
    $Group -cne 'UnitTestIDE Native Offline Boundary'
  ) {
    return
  }
  New-FixtureRule $PolicyStore
}

function New-NetFirewallRule {
  [CmdletBinding()]
  param(
    [string]$PolicyStore,
    [string]$Name,
    [string]$DisplayName,
    [string]$Group,
    [string]$Direction,
    [string]$Action,
    [object]$Enabled,
    [string]$Profile,
    [string]$Protocol
  )
  Add-FixtureTrace 'install-start'
  if ($InstallDelayMilliseconds -gt 0) {
    Start-Sleep -Milliseconds $InstallDelayMilliseconds
  }
  $script:installed = $true
  Add-FixtureTrace 'install-finished'
  New-FixtureRule 'PersistentStore'
}

function Remove-NetFirewallRule {
  [CmdletBinding()]
  param(
    [Parameter(ValueFromPipeline = $true)]
    [object]$InputObject
  )
  process {
    $script:removeAttempts++
    Add-FixtureTrace "remove:$script:removeAttempts"
    if ($script:removeAttempts -le $RemoveFailures) {
      throw "fixture removal failure $script:removeAttempts"
    }
    $script:installed = $false
  }
}

function Get-NetFirewallProfile {
  [CmdletBinding()]
  param([string]$PolicyStore)
  Add-FixtureTrace "profile-query:$PolicyStore"
  if ($PolicyStore -cne 'ActiveStore') {
    throw 'fixture rejected a default or non-ActiveStore firewall profile query'
  }
  $profiles = @(
    [pscustomobject]@{ Name = 'Domain'; Enabled = 'True' },
    [pscustomobject]@{ Name = 'Private'; Enabled = 'True' },
    [pscustomobject]@{ Name = 'Public'; Enabled = 'True' }
  )
  switch ($ProfileScenario) {
    'MissingProfile' { return $profiles[0..1] }
    'ExtraProfile' {
      return @($profiles + [pscustomobject]@{ Name = 'Unexpected'; Enabled = 'True' })
    }
    'DisabledProfile' {
      $profiles[2].Enabled = 'False'
      return $profiles
    }
    default { return $profiles }
  }
}

function Get-NetFirewallApplicationFilter {
  [CmdletBinding()]
  param([Parameter(ValueFromPipeline = $true)][object]$InputObject)
  process {
    Add-FixtureTrace "filter:$($InputObject.FixtureStore):application"
    if (Test-FixtureTamper $InputObject.FixtureStore 'Application') {
      [pscustomobject]@{ Program = 'C:\tampered.exe'; Package = 'Any' }
    } else {
      [pscustomobject]@{ Program = 'Any'; Package = 'Any' }
    }
  }
}

function Get-NetFirewallAddressFilter {
  [CmdletBinding()]
  param([Parameter(ValueFromPipeline = $true)][object]$InputObject)
  process {
    Add-FixtureTrace "filter:$($InputObject.FixtureStore):address"
    if (Test-FixtureTamper $InputObject.FixtureStore 'Address') {
      [pscustomobject]@{ LocalAddress = 'Any'; RemoteAddress = '8.8.8.8' }
    } else {
      [pscustomobject]@{ LocalAddress = 'Any'; RemoteAddress = 'Any' }
    }
  }
}

function Get-NetFirewallPortFilter {
  [CmdletBinding()]
  param([Parameter(ValueFromPipeline = $true)][object]$InputObject)
  process {
    Add-FixtureTrace "filter:$($InputObject.FixtureStore):port"
    if (Test-FixtureTamper $InputObject.FixtureStore 'Port') {
      [pscustomobject]@{ Protocol = 'TCP'; LocalPort = 'Any'; RemotePort = '443' }
    } else {
      [pscustomobject]@{ Protocol = 'Any'; LocalPort = 'Any'; RemotePort = 'Any' }
    }
  }
}

function Get-NetFirewallServiceFilter {
  [CmdletBinding()]
  param([Parameter(ValueFromPipeline = $true)][object]$InputObject)
  process {
    Add-FixtureTrace "filter:$($InputObject.FixtureStore):service"
    if (Test-FixtureTamper $InputObject.FixtureStore 'Service') {
      [pscustomobject]@{ Service = 'TamperedService' }
    } else {
      [pscustomobject]@{ Service = 'Any' }
    }
  }
}

function Get-NetFirewallInterfaceFilter {
  [CmdletBinding()]
  param([Parameter(ValueFromPipeline = $true)][object]$InputObject)
  process {
    Add-FixtureTrace "filter:$($InputObject.FixtureStore):interface"
    if (Test-FixtureTamper $InputObject.FixtureStore 'Interface') {
      [pscustomobject]@{ InterfaceAlias = 'Ethernet' }
    } else {
      [pscustomobject]@{ InterfaceAlias = 'Any' }
    }
  }
}

function Get-NetFirewallInterfaceTypeFilter {
  [CmdletBinding()]
  param([Parameter(ValueFromPipeline = $true)][object]$InputObject)
  process {
    Add-FixtureTrace "filter:$($InputObject.FixtureStore):interface-type"
    if (Test-FixtureTamper $InputObject.FixtureStore 'InterfaceType') {
      [pscustomobject]@{ InterfaceType = 'Wireless' }
    } else {
      [pscustomobject]@{ InterfaceType = 'Any' }
    }
  }
}

$parameters = @{
  Action = $Action
  RuleName = $RuleName
  StateRoot = $StateRoot
  DeadlineSeconds = $DeadlineSeconds
}
if (-not [string]::IsNullOrEmpty($StateDirectory)) {
  $parameters.StateDirectory = $StateDirectory
}
if ($OwnerPid -gt 0) {
  $parameters.OwnerPid = $OwnerPid
}
if (-not [string]::IsNullOrEmpty($GuardianNonce)) {
  $parameters.GuardianNonce = $GuardianNonce
}

. $BoundaryScript @parameters
