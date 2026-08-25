[CmdletBinding()]
param(
  [Parameter(Mandatory = $true)][string]$EvidencePath,
  [Parameter(Mandatory = $true)][string]$PackagePath,
  [Parameter(Mandatory = $true)][string]$PackageSha256,
  [Parameter(Mandatory = $true)][string]$ManifestPath,
  [Parameter(Mandatory = $true)][string]$ManifestSha256,
  [Parameter(Mandatory = $true)][string]$Version,
  [Parameter(Mandatory = $true)][string]$BaselinePackagePath,
  [Parameter(Mandatory = $true)][string]$BaselinePackageSha256,
  [Parameter(Mandatory = $true)][string]$BaselineManifestPath,
  [Parameter(Mandatory = $true)][string]$BaselineManifestSha256,
  [Parameter(Mandatory = $true)][string]$BaselineVersion,
  [string]$Root
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
$scriptRoot = Split-Path -Parent $PSCommandPath
$updateScript = Join-Path $scriptRoot 'update.mjs'
$verifyScript = Join-Path $scriptRoot 'windows\verify-msix.ps1'
$createdRoot = $false

function Resolve-RealFile {
  param([string]$Path, [string]$Label)
  $item = Get-Item -LiteralPath (Resolve-Path -LiteralPath $Path).ProviderPath -Force
  if (-not $item.PSIsContainer -and ($item.Attributes -band [IO.FileAttributes]::ReparsePoint) -eq 0) { return $item.FullName }
  throw "$Label must be a real file"
}

function Require-Digest {
  param([string]$Value, [string]$Label)
  if ($Value -cnotmatch '^[0-9a-f]{64}$') { throw "$Label must be a lowercase SHA-256 digest" }
}

function Require-FileDigest {
  param([string]$Path, [string]$Expected, [string]$Label)
  $actual = (Get-FileHash -LiteralPath $Path -Algorithm SHA256).Hash.ToLowerInvariant()
  if ($actual -cne $Expected) { throw "$Label SHA-256 mismatch" }
}

function Expand-VerifiedPayload {
  param([string]$Manifest, [string]$ExtractRoot, [string]$PayloadRoot)
  $releaseManifest = Get-Content -LiteralPath $Manifest -Raw | ConvertFrom-Json
  New-Item -ItemType Directory -Path $PayloadRoot | Out-Null
  $paths = @($releaseManifest.artifacts | ForEach-Object { [string]$_.relativePath })
  $paths += @($releaseManifest.licenses | ForEach-Object { [string]$_.path })
  $paths += 'release-manifest.json'
  foreach ($relativePath in $paths) {
    if ($relativePath -match '\\|:|(^|/)\.\.(/|$)|^/') { throw "unsafe package payload path: $relativePath" }
    $nativePath = $relativePath.Replace('/', [IO.Path]::DirectorySeparatorChar)
    $source = Join-Path $ExtractRoot $nativePath
    $destination = Join-Path $PayloadRoot $nativePath
    New-Item -ItemType Directory -Force -Path (Split-Path -Parent $destination) | Out-Null
    Copy-Item -LiteralPath $source -Destination $destination
  }
}

Require-Digest -Value $PackageSha256 -Label 'PackageSha256'
Require-Digest -Value $ManifestSha256 -Label 'ManifestSha256'
Require-Digest -Value $BaselinePackageSha256 -Label 'BaselinePackageSha256'
Require-Digest -Value $BaselineManifestSha256 -Label 'BaselineManifestSha256'
$resolvedPackage = Resolve-RealFile -Path $PackagePath -Label 'PackagePath'
$resolvedManifest = Resolve-RealFile -Path $ManifestPath -Label 'ManifestPath'
$resolvedBaselinePackage = Resolve-RealFile -Path $BaselinePackagePath -Label 'BaselinePackagePath'
$resolvedBaselineManifest = Resolve-RealFile -Path $BaselineManifestPath -Label 'BaselineManifestPath'
Require-FileDigest -Path $resolvedPackage -Expected $PackageSha256 -Label 'package'
Require-FileDigest -Path $resolvedManifest -Expected $ManifestSha256 -Label 'manifest'
Require-FileDigest -Path $resolvedBaselinePackage -Expected $BaselinePackageSha256 -Label 'baseline package'
Require-FileDigest -Path $resolvedBaselineManifest -Expected $BaselineManifestSha256 -Label 'baseline manifest'

if ([string]::IsNullOrWhiteSpace($Root)) {
  $Root = Join-Path ([IO.Path]::GetTempPath()) "unit-test-ide-install-smoke-$([Guid]::NewGuid().ToString('N'))"
}
$resolvedRoot = [IO.Path]::GetFullPath($Root)
$resolvedEvidence = [IO.Path]::GetFullPath($EvidencePath)
if ($resolvedRoot -eq [IO.Path]::GetPathRoot($resolvedRoot)) { throw 'Refusing to use a filesystem root for install smoke' }
if ($resolvedEvidence.StartsWith($resolvedRoot + [IO.Path]::DirectorySeparatorChar, [StringComparison]::OrdinalIgnoreCase)) {
  throw 'EvidencePath must be outside the disposable smoke root'
}
if (Test-Path -LiteralPath $resolvedRoot) { throw "Disposable smoke root already exists: $resolvedRoot" }

try {
  New-Item -ItemType Directory -Path $resolvedRoot | Out-Null
  $createdRoot = $true
  New-Item -ItemType Directory -Force -Path (Split-Path -Parent $resolvedEvidence) | Out-Null
  Add-Type -AssemblyName System.IO.Compression.FileSystem
  $targetExtract = Join-Path $resolvedRoot 'target-msix'
  $baselineExtract = Join-Path $resolvedRoot 'baseline-msix'
  New-Item -ItemType Directory -Path $targetExtract | Out-Null
  New-Item -ItemType Directory -Path $baselineExtract | Out-Null
  [IO.Compression.ZipFile]::ExtractToDirectory($resolvedPackage, $targetExtract)
  [IO.Compression.ZipFile]::ExtractToDirectory($resolvedBaselinePackage, $baselineExtract)
  $embeddedManifest = Join-Path $targetExtract 'release-manifest.json'
  $embeddedBaselineManifest = Join-Path $baselineExtract 'release-manifest.json'
  Require-FileDigest -Path $embeddedManifest -Expected $ManifestSha256 -Label 'embedded manifest'
  Require-FileDigest -Path $embeddedBaselineManifest -Expected $BaselineManifestSha256 -Label 'embedded baseline manifest'
  if ((Get-Content -LiteralPath $embeddedManifest -Raw | ConvertFrom-Json).version -cne $Version) { throw 'embedded manifest version mismatch' }
  if ((Get-Content -LiteralPath $embeddedBaselineManifest -Raw | ConvertFrom-Json).version -cne $BaselineVersion) { throw 'embedded baseline manifest version mismatch' }
  & $verifyScript -Package $resolvedPackage -Manifest $embeddedManifest | Out-Null
  & $verifyScript -Package $resolvedBaselinePackage -Manifest $embeddedBaselineManifest | Out-Null
  $targetPayload = Join-Path $resolvedRoot 'target-payload'
  $baselinePayload = Join-Path $resolvedRoot 'baseline-payload'
  Expand-VerifiedPayload -Manifest $embeddedManifest -ExtractRoot $targetExtract -PayloadRoot $targetPayload
  Expand-VerifiedPayload -Manifest $embeddedBaselineManifest -ExtractRoot $baselineExtract -PayloadRoot $baselinePayload

  & node $updateScript smoke `
    --platform windows `
    --root (Join-Path $resolvedRoot 'lifecycle') `
    --evidence $resolvedEvidence `
    --package $resolvedPackage `
    --package-sha256 $PackageSha256 `
    --manifest-sha256 $ManifestSha256 `
    --version $Version `
    --artifact $targetPayload `
    --baseline-artifact $baselinePayload | Out-Null
  if ($LASTEXITCODE -ne 0) { throw "Install smoke failed with exit code $LASTEXITCODE" }

  $evidence = Get-Content -LiteralPath $resolvedEvidence -Raw | ConvertFrom-Json
  if (
    $evidence.platform -ne 'windows' -or
    $evidence.packageSha256 -cne $PackageSha256 -or
    $evidence.manifestSha256 -cne $ManifestSha256 -or
    $evidence.version -cne $Version -or
    $evidence.rollbackVersion -cne $BaselineVersion -or
    $evidence.outcomes.upgradeLaunch -cne 'failed-as-expected' -or
    $evidence.outcomes.rollbackLaunch -cne 'pass' -or
    $evidence.outcomes.packageResidueAbsent -ne 'pass'
  ) { throw 'Install smoke evidence failed closed validation' }
} finally {
  if ($createdRoot -and (Test-Path -LiteralPath $resolvedRoot)) { Remove-Item -LiteralPath $resolvedRoot -Recurse -Force }
}
