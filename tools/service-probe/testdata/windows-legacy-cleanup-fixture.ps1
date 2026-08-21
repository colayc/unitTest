[CmdletBinding()]
param(
  [Parameter(Mandatory = $true)]
  [string]$BoundaryScript,

  [Parameter(Mandatory = $true)]
  [string]$RuleName,

  [Parameter(Mandatory = $true)]
  [string]$StateRoot,

  [ValidateSet('Valid', 'WrongNonce', 'WrongOwnerPid', 'WrongRuleMarker', 'ExtraMarker', 'UnknownState')]
  [string]$StateScenario = 'Valid',

  [ValidateSet('Valid', 'ExtraRule', 'WrongAction')]
  [string]$RuleScenario = 'Valid',

  [ValidateSet('None', 'LateUnknownMarker', 'LateExtraLeaf', 'LateReparseReplacement')]
  [string]$MutationScenario = 'None'
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

$script:ruleGroup = 'UnitTestIDE Native Offline Boundary'
$script:stateDirectory = Join-Path $StateRoot $RuleName
$script:rules = @{
  ActiveStore = @()
  PersistentStore = @()
}
$script:mutationInjected = $false
$utf8NoBom = [Text.UTF8Encoding]::new($false)

function Write-Marker([string]$Path, [string]$Content) {
  [IO.File]::WriteAllText($Path, $Content, $utf8NoBom)
}

function New-FixtureRule([string]$Store, [string]$Name) {
  $rule = [ordered]@{
    Name = $Name
    DisplayName = $Name
    Enabled = 'True'
    Direction = 'Outbound'
    Action = 'Block'
    Profile = 'Any'
    Group = $script:ruleGroup
    FixtureStore = $Store
  }
  if ($RuleScenario -ceq 'WrongAction' -and $Name -ceq $RuleName) {
    $rule.Action = 'Allow'
  }
  [pscustomobject]$rule
}

function Initialize-FixtureState {
  New-Item -ItemType Directory -Force -Path $StateRoot, $script:stateDirectory | Out-Null
  Write-Marker (Join-Path $script:stateDirectory 'rule-name') "rule=$RuleName`n"
  Write-Marker (Join-Path $script:stateDirectory 'owner.pid') "owner=4242`n"
  Write-Marker (Join-Path $script:stateDirectory 'guardian.nonce') ("nonce=" + ('a' * 64) + "`n")
  Write-Marker (Join-Path $script:stateDirectory 'guardian.pid') "7777`n"
  Write-Marker (Join-Path $script:stateDirectory 'release') "release=$RuleName`n"
  Write-Marker (Join-Path $script:stateDirectory 'ready') "ready=$RuleName`n"
  Write-Marker (Join-Path $script:stateDirectory 'removed') "removed=$RuleName`n"

  switch ($StateScenario) {
    'WrongNonce' {
      Write-Marker (Join-Path $script:stateDirectory 'guardian.nonce') "nonce=bad`n"
    }
    'WrongOwnerPid' {
      Write-Marker (Join-Path $script:stateDirectory 'owner.pid') "owner=0`n"
    }
    'WrongRuleMarker' {
      Write-Marker (Join-Path $script:stateDirectory 'rule-name') "rule=$RuleName-tampered`n"
    }
    'ExtraMarker' {
      Write-Marker (Join-Path $script:stateDirectory 'unexpected.marker') "boom`n"
    }
    'UnknownState' {
      Write-Marker (Join-Path $StateRoot 'unknown.txt') "boom`n"
    }
  }
}

function Initialize-FixtureRules {
  $script:rules.ActiveStore = @(New-FixtureRule 'ActiveStore' $RuleName)
  $script:rules.PersistentStore = @(New-FixtureRule 'PersistentStore' $RuleName)
  if ($RuleScenario -ceq 'ExtraRule') {
    $script:rules.ActiveStore += New-FixtureRule 'ActiveStore' "$RuleName-extra"
    $script:rules.PersistentStore += New-FixtureRule 'PersistentStore' "$RuleName-extra"
  }
}

function Inject-LateMutation {
  if ($script:mutationInjected -or $MutationScenario -ceq 'None') {
    return
  }
  if (@($script:rules.ActiveStore).Count -ne 0 -or @($script:rules.PersistentStore).Count -ne 0) {
    return
  }

  switch ($MutationScenario) {
    'LateUnknownMarker' {
      Write-Marker (Join-Path $script:stateDirectory 'late.marker') "boom`n"
    }
    'LateExtraLeaf' {
      Write-Marker (Join-Path $StateRoot 'late-extra.txt') "boom`n"
    }
    'LateReparseReplacement' {
      $lateTarget = Join-Path (Split-Path -Parent $StateRoot) 'late-reparse-target'
      if (Test-Path -LiteralPath $lateTarget) {
        Remove-Item -LiteralPath $lateTarget -Recurse -Force
      }
      Move-Item -LiteralPath $script:stateDirectory -Destination $lateTarget -Force
      New-Item -ItemType Junction -Path $script:stateDirectory -Target $lateTarget | Out-Null
    }
  }

  $script:mutationInjected = $true
}

function Get-NetFirewallRule {
  [CmdletBinding()]
  param(
    [string]$PolicyStore,
    [string]$Name,
    [string]$Group
  )
  $rules = @($script:rules[$PolicyStore])
  if (-not [string]::IsNullOrEmpty($Name)) {
    return @($rules | Where-Object { $_.Name -ceq $Name })
  }
  if (-not [string]::IsNullOrEmpty($Group)) {
    Inject-LateMutation
    return @($rules | Where-Object { $_.Group -ceq $Group })
  }
  return $rules
}

function Remove-NetFirewallRule {
  [CmdletBinding()]
  param(
    [Parameter(ValueFromPipeline = $true)]
    [object]$InputObject
  )
  process {
    $store = $InputObject.FixtureStore
    $script:rules[$store] = @($script:rules[$store] | Where-Object { $_.Name -cne $InputObject.Name })
  }
}

function Get-NetFirewallApplicationFilter {
  [CmdletBinding()]
  param([Parameter(ValueFromPipeline = $true)][object]$InputObject)
  process { [pscustomobject]@{ Program = 'Any'; Package = 'Any' } }
}

function Get-NetFirewallAddressFilter {
  [CmdletBinding()]
  param([Parameter(ValueFromPipeline = $true)][object]$InputObject)
  process { [pscustomobject]@{ LocalAddress = 'Any'; RemoteAddress = 'Any' } }
}

function Get-NetFirewallPortFilter {
  [CmdletBinding()]
  param([Parameter(ValueFromPipeline = $true)][object]$InputObject)
  process { [pscustomobject]@{ Protocol = 'Any'; LocalPort = 'Any'; RemotePort = 'Any' } }
}

function Get-NetFirewallServiceFilter {
  [CmdletBinding()]
  param([Parameter(ValueFromPipeline = $true)][object]$InputObject)
  process { [pscustomobject]@{ Service = 'Any' } }
}

function Get-NetFirewallInterfaceFilter {
  [CmdletBinding()]
  param([Parameter(ValueFromPipeline = $true)][object]$InputObject)
  process { [pscustomobject]@{ InterfaceAlias = 'Any' } }
}

function Get-NetFirewallInterfaceTypeFilter {
  [CmdletBinding()]
  param([Parameter(ValueFromPipeline = $true)][object]$InputObject)
  process { [pscustomobject]@{ InterfaceType = 'Any' } }
}

function Get-CimInstance {
  [CmdletBinding()]
  param([string]$ClassName)
  return @()
}

Initialize-FixtureState
Initialize-FixtureRules

. $BoundaryScript -Action LegacyCleanup -StateRoot $StateRoot -DeadlineSeconds 2
