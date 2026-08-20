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

  [ValidateSet('Valid', 'MissingProfile', 'ExtraProfile', 'DisabledProfile')]
  [string]$ProfileScenario = 'Valid',

  [ValidateRange(0, 30000)]
  [int]$InstallDelayMilliseconds = 0,

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
$tracePath = Join-Path $StateRoot $TraceName

function Add-FixtureTrace([string]$Message) {
  Add-Content -LiteralPath $tracePath -Value $Message -Encoding utf8
}

function New-FixtureRule {
  [pscustomobject]@{
    Name = $RuleName
    DisplayName = $RuleName
    Enabled = 'True'
    Direction = 'Outbound'
    Action = 'Block'
    Profile = 'Any'
    Group = 'UnitTestIDE Native Offline Boundary'
  }
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
  New-FixtureRule
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
  New-FixtureRule
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
    Add-FixtureTrace 'filter:application'
    [pscustomobject]@{ Program = 'Any'; Package = 'Any' }
  }
}

function Get-NetFirewallAddressFilter {
  [CmdletBinding()]
  param([Parameter(ValueFromPipeline = $true)][object]$InputObject)
  process {
    Add-FixtureTrace 'filter:address'
    [pscustomobject]@{ LocalAddress = 'Any'; RemoteAddress = 'Any' }
  }
}

function Get-NetFirewallPortFilter {
  [CmdletBinding()]
  param([Parameter(ValueFromPipeline = $true)][object]$InputObject)
  process {
    Add-FixtureTrace 'filter:port'
    [pscustomobject]@{ Protocol = 'Any'; LocalPort = 'Any'; RemotePort = 'Any' }
  }
}

function Get-NetFirewallServiceFilter {
  [CmdletBinding()]
  param([Parameter(ValueFromPipeline = $true)][object]$InputObject)
  process {
    Add-FixtureTrace 'filter:service'
    [pscustomobject]@{ Service = 'Any' }
  }
}

function Get-NetFirewallInterfaceFilter {
  [CmdletBinding()]
  param([Parameter(ValueFromPipeline = $true)][object]$InputObject)
  process {
    Add-FixtureTrace 'filter:interface'
    [pscustomobject]@{ InterfaceAlias = 'Any' }
  }
}

function Get-NetFirewallInterfaceTypeFilter {
  [CmdletBinding()]
  param([Parameter(ValueFromPipeline = $true)][object]$InputObject)
  process {
    Add-FixtureTrace 'filter:interface-type'
    [pscustomobject]@{ InterfaceType = 'Any' }
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

. $BoundaryScript @parameters
