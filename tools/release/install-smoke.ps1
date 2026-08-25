[CmdletBinding()]
param(
  [Parameter(Mandatory = $true)]
  [string]$EvidencePath,

  [string]$Root
)

$ErrorActionPreference = 'Stop'
$scriptRoot = Split-Path -Parent $PSCommandPath
$updateScript = Join-Path $scriptRoot 'update.mjs'
$createdRoot = $false

if ([string]::IsNullOrWhiteSpace($Root)) {
  $Root = Join-Path ([IO.Path]::GetTempPath()) "unit-test-ide-install-smoke-$([Guid]::NewGuid().ToString('N'))"
}

$resolvedRoot = [IO.Path]::GetFullPath($Root)
$resolvedEvidence = [IO.Path]::GetFullPath($EvidencePath)
$filesystemRoot = [IO.Path]::GetPathRoot($resolvedRoot)
if ($resolvedRoot -eq $filesystemRoot) {
  throw 'Refusing to use a filesystem root for install smoke'
}
if ($resolvedEvidence.StartsWith($resolvedRoot + [IO.Path]::DirectorySeparatorChar, [StringComparison]::OrdinalIgnoreCase)) {
  throw 'EvidencePath must be outside the disposable smoke root'
}
if (Test-Path -LiteralPath $resolvedRoot) {
  throw "Disposable smoke root already exists: $resolvedRoot"
}

try {
  New-Item -ItemType Directory -Path $resolvedRoot | Out-Null
  $createdRoot = $true
  $evidenceDirectory = Split-Path -Parent $resolvedEvidence
  New-Item -ItemType Directory -Force -Path $evidenceDirectory | Out-Null

  & node $updateScript smoke `
    --platform windows `
    --root $resolvedRoot `
    --evidence $resolvedEvidence | Out-Null
  if ($LASTEXITCODE -ne 0) {
    throw "Install smoke failed with exit code $LASTEXITCODE"
  }

  $evidence = Get-Content -LiteralPath $resolvedEvidence -Raw | ConvertFrom-Json
  if ($evidence.platform -ne 'windows' -or $evidence.outcomes.packageResidueAbsent -ne 'pass') {
    throw 'Install smoke evidence failed closed validation'
  }
} finally {
  if ($createdRoot -and (Test-Path -LiteralPath $resolvedRoot)) {
    Remove-Item -LiteralPath $resolvedRoot -Recurse -Force
  }
}
