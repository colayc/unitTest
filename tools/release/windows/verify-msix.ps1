[CmdletBinding()]
param(
  [Parameter(Mandatory = $true)]
  [string]$Package,

  [Parameter(Mandatory = $true)]
  [string]$Manifest,

  [switch]$RequireSignature
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

$packageName = 'OpenAI.UnitTestIDE'
$allowedMetadata = @(
  '[Content_Types].xml',
  'AppxBlockMap.xml',
  'AppxManifest.xml',
  'AppxMetadata/CodeIntegrity.cat',
  'AppxSignature.p7x'
)

function Fail-Release {
  param(
    [Parameter(Mandatory = $true)][string]$Code,
    [Parameter(Mandatory = $true)][string]$Message
  )

  throw "${Code}: ${Message}"
}

function Resolve-RealFile {
  param(
    [Parameter(Mandatory = $true)][string]$Path,
    [Parameter(Mandatory = $true)][string]$Label
  )

  try {
    $resolved = Resolve-Path -LiteralPath $Path
  } catch {
    Fail-Release -Code 'RELEASE_INPUT_MISSING' -Message "${Label} is required"
  }

  $item = Get-Item -LiteralPath $resolved.ProviderPath -Force
  if (-not $item.Exists -or -not $item.PSIsContainer -and ($item.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) {
    Fail-Release -Code 'RELEASE_INPUT_MISSING' -Message "${Label} must be a regular file"
  }
  if (-not $item.Exists -or $item.PSIsContainer) {
    Fail-Release -Code 'RELEASE_INPUT_MISSING' -Message "${Label} must be a regular file"
  }

  return $item.FullName
}

function Normalize-ReleaseVersion {
  param([Parameter(Mandatory = $true)][string]$Version)

  if ($Version -notmatch '^(\d+)\.(\d+)\.(\d+)(?:[-+].+)?$') {
    Fail-Release -Code 'RELEASE_VERIFICATION_FAILED' -Message "release version is not semver-like: $Version"
  }

  return "$($Matches[1]).$($Matches[2]).$($Matches[3]).0"
}

function Get-Sha256Hex {
  param([Parameter(Mandatory = $true)][byte[]]$Bytes)

  $sha = [Security.Cryptography.SHA256]::Create()
  try {
    return ([BitConverter]::ToString($sha.ComputeHash($Bytes))).Replace('-', '').ToLowerInvariant()
  } finally {
    $sha.Dispose()
  }
}

function Read-EntryBytes {
  param([Parameter(Mandatory = $true)]$Entry)

  $stream = $Entry.Open()
  $memory = New-Object IO.MemoryStream
  try {
    $stream.CopyTo($memory)
    return $memory.ToArray()
  } finally {
    $memory.Dispose()
    $stream.Dispose()
  }
}

function Get-NormalizedEntries {
  param([Parameter(Mandatory = $true)]$Archive)

  $results = @()
  foreach ($entry in $Archive.Entries) {
    if ([string]::IsNullOrWhiteSpace($entry.Name) -and $entry.FullName.EndsWith('/')) {
      continue
    }
    $results += ($entry.FullName -replace '\\', '/')
  }
  return $results
}

Add-Type -AssemblyName System.IO.Compression.FileSystem

$packagePath = Resolve-RealFile -Path $Package -Label 'package'
$manifestPath = Resolve-RealFile -Path $Manifest -Label 'manifest'
$externalManifestBytes = [IO.File]::ReadAllBytes($manifestPath)
$releaseManifest = Get-Content -LiteralPath $manifestPath -Raw | ConvertFrom-Json

$archive = [System.IO.Compression.ZipFile]::OpenRead($packagePath)
try {
  $entries = Get-NormalizedEntries -Archive $archive | Sort-Object -Unique
  $embeddedManifestEntry = $archive.GetEntry('AppxManifest.xml')
  if ($null -eq $embeddedManifestEntry) {
    Fail-Release -Code 'RELEASE_VERIFICATION_FAILED' -Message 'package does not contain AppxManifest.xml'
  }

  $embeddedReleaseManifestEntry = $archive.GetEntry('release-manifest.json')
  if ($null -eq $embeddedReleaseManifestEntry) {
    Fail-Release -Code 'RELEASE_VERIFICATION_FAILED' -Message 'package does not contain release-manifest.json'
  }

  $signaturePresent = $entries -contains 'AppxSignature.p7x'
  if ($RequireSignature -and -not $signaturePresent) {
    Fail-Release -Code 'RELEASE_VERIFICATION_FAILED' -Message 'required MSIX signature is absent'
  }

  $payloadEntries = @(
    $entries |
      Where-Object {
        ($_ -notin $allowedMetadata) -and
        (-not $_.StartsWith('AppxMetadata/'))
      }
  ) | Sort-Object
  $expectedEntries = @(
    @($releaseManifest.artifacts | ForEach-Object { $_.relativePath }) +
    @($releaseManifest.licenses) +
    @('release-manifest.json')
  ) | Sort-Object
  if (($payloadEntries.Count -ne $expectedEntries.Count) -or (Compare-Object -ReferenceObject $expectedEntries -DifferenceObject $payloadEntries)) {
    Fail-Release -Code 'RELEASE_VERIFICATION_FAILED' -Message 'package payload does not match release-manifest.json'
  }

  $embeddedReleaseManifestHash = Get-Sha256Hex -Bytes (Read-EntryBytes -Entry $embeddedReleaseManifestEntry)
  $externalReleaseManifestHash = Get-Sha256Hex -Bytes $externalManifestBytes
  if ($embeddedReleaseManifestHash -ne $externalReleaseManifestHash) {
    Fail-Release -Code 'RELEASE_VERIFICATION_FAILED' -Message 'embedded release-manifest.json hash does not match the staged manifest'
  }

  $embeddedAppxManifest = [xml][Text.Encoding]::UTF8.GetString((Read-EntryBytes -Entry $embeddedManifestEntry))
  $identity = $embeddedAppxManifest.Package.Identity
  if ($null -eq $identity) {
    Fail-Release -Code 'RELEASE_VERIFICATION_FAILED' -Message 'AppxManifest.xml does not declare Package/Identity'
  }
  if ($identity.Name -ne $packageName) {
    Fail-Release -Code 'RELEASE_VERIFICATION_FAILED' -Message "unexpected package identity name: $($identity.Name)"
  }
  if ([string]::IsNullOrWhiteSpace($identity.Publisher)) {
    Fail-Release -Code 'RELEASE_VERIFICATION_FAILED' -Message 'AppxManifest.xml publisher is empty'
  }
  if ($identity.Version -ne (Normalize-ReleaseVersion -Version $releaseManifest.version)) {
    Fail-Release -Code 'RELEASE_VERIFICATION_FAILED' -Message 'AppxManifest.xml version does not match release-manifest.json'
  }
  if ($identity.ProcessorArchitecture -ne $releaseManifest.architecture) {
    Fail-Release -Code 'RELEASE_VERIFICATION_FAILED' -Message 'AppxManifest.xml architecture does not match release-manifest.json'
  }
} finally {
  $archive.Dispose()
}

Write-Output $packagePath
