[CmdletBinding()]
param(
  [Parameter(Mandatory = $true)]
  [ValidateSet('Install', 'AuditInstalled', 'Remove', 'AuditRemoved', 'Watch')]
  [string]$Action,

  [Parameter(Mandatory = $true)]
  [ValidatePattern('^UnitTestIDE-NativeOffline-[0-9a-f]{16,64}$')]
  [string]$RuleName,

  [int]$OwnerPid = 0,

  [string]$ReadyPath = ''
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
$ruleGroup = 'UnitTestIDE Native Offline Boundary'

function Get-ActiveRule {
  try {
    @(Get-NetFirewallRule -PolicyStore ActiveStore -Name $RuleName -ErrorAction Stop)
  } catch {
    if ($_.FullyQualifiedErrorId -like 'CmdletizationQuery_NotFound*') {
      return
    }
    throw
  }
}

function Get-PersistentRule {
  try {
    @(Get-NetFirewallRule -PolicyStore PersistentStore -Name $RuleName -ErrorAction Stop)
  } catch {
    if ($_.FullyQualifiedErrorId -like 'CmdletizationQuery_NotFound*') {
      return
    }
    throw
  }
}

function Remove-Rule {
  $rules = @(Get-PersistentRule)
  if ($rules.Count -gt 0) {
    $rules | Remove-NetFirewallRule -ErrorAction Stop
  }
}

function Assert-RuleRemoved {
  $active = @(Get-ActiveRule)
  $persistent = @(Get-PersistentRule)
  if ($active.Count -ne 0 -or $persistent.Count -ne 0) {
    throw 'Windows native offline firewall rule still exists'
  }
}

function Assert-RuleInstalled {
  $rules = @(Get-ActiveRule)
  $persistent = @(Get-PersistentRule)
  if ($rules.Count -ne 1 -or $persistent.Count -ne 1) {
    throw 'Windows native offline firewall rule is not unique in both policy stores'
  }
  $rule = $rules[0]
  if (
    $rule.Name -cne $RuleName -or
    $rule.DisplayName -cne $RuleName -or
    $rule.Enabled.ToString() -ne 'True' -or
    $rule.Direction.ToString() -ne 'Outbound' -or
    $rule.Action.ToString() -ne 'Block' -or
    $rule.Profile.ToString() -ne 'Any' -or
    $rule.Group -ne $ruleGroup
  ) {
    throw 'Windows native offline firewall rule has unexpected policy'
  }
  $disabledProfiles = @(Get-NetFirewallProfile | Where-Object { -not $_.Enabled })
  if ($disabledProfiles.Count -ne 0) {
    throw 'Windows Firewall must be enabled for every profile'
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

switch ($Action) {
  'Install' {
    Assert-RuleRemoved
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
  }
  'AuditInstalled' {
    Assert-RuleInstalled
  }
  'Remove' {
    Remove-Rule
  }
  'AuditRemoved' {
    Assert-RuleRemoved
  }
  'Watch' {
    if ($OwnerPid -le 0) {
      throw 'Windows native offline watchdog owner PID is invalid'
    }
    if ([string]::IsNullOrWhiteSpace($ReadyPath) -or -not [IO.Path]::IsPathRooted($ReadyPath)) {
      throw 'Windows native offline watchdog readiness path is invalid'
    }
    $owner = Get-Process -Id $OwnerPid -ErrorAction Stop
    try {
      $readyBytes = [Text.Encoding]::UTF8.GetBytes("$OwnerPid`n")
      $ready = [IO.File]::Open(
        $ReadyPath,
        [IO.FileMode]::CreateNew,
        [IO.FileAccess]::Write,
        [IO.FileShare]::None
      )
      try {
        $ready.Write($readyBytes, 0, $readyBytes.Length)
        $ready.Flush($true)
      } finally {
        $ready.Dispose()
      }
      $owner.WaitForExit()
      # A child PowerShell that was creating the rule can briefly outlive its
      # Node owner. Repeated idempotent removal closes that crash race.
      for ($attempt = 0; $attempt -lt 60; $attempt++) {
        Remove-Rule
        Start-Sleep -Milliseconds 250
      }
      Assert-RuleRemoved
    } finally {
      Remove-Item -LiteralPath $ReadyPath -Force -ErrorAction SilentlyContinue
    }
  }
}
