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
$packageFootprintEntries = @(
  '[Content_Types].xml',
  'AppxBlockMap.xml',
  'AppxManifest.xml',
  'AppxMetadata/CodeIntegrity.cat',
  'AppxSignature.p7x'
)
$storeLogoPath = 'Assets/StoreLogo.png'
$square150LogoPath = 'Assets/Square150x150Logo.png'
$square44LogoPath = 'Assets/Square44x44Logo.png'
$storeLogoBase64 = 'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO7+4xQAAAAASUVORK5CYII='
$manifestValidator = Join-Path (Split-Path -Parent $PSScriptRoot) 'validate-release-manifest.mjs'

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
  if ($item.PSIsContainer -or ($item.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) {
    Fail-Release -Code 'RELEASE_INPUT_MISSING' -Message "${Label} must be a regular file"
  }

  return $item.FullName
}

function Resolve-ToolPath {
  param(
    [Parameter(Mandatory = $true)][AllowEmptyString()][string]$Override,
    [Parameter(Mandatory = $true)][string]$CommandName,
    [Parameter(Mandatory = $true)][string]$StableName
  )

  if (-not [string]::IsNullOrWhiteSpace($Override)) {
    try {
      return (Resolve-Path -LiteralPath $Override).ProviderPath
    } catch {
      Fail-Release -Code 'RELEASE_TOOL_MISSING' -Message "${StableName} is unavailable"
    }
  }

  $command = Get-Command -Name $CommandName -ErrorAction SilentlyContinue | Select-Object -First 1
  if ($null -ne $command) {
    return $command.Source
  }

  $kitsRoot = ${env:ProgramFiles(x86)}
  if (-not [string]::IsNullOrWhiteSpace($kitsRoot)) {
    $candidateRoot = Join-Path $kitsRoot 'Windows Kits\10\bin'
    if (Test-Path -LiteralPath $candidateRoot) {
      $candidate = Get-ChildItem -LiteralPath $candidateRoot -Filter $CommandName -Recurse -File -ErrorAction SilentlyContinue |
        Sort-Object -Property FullName -Descending |
        Select-Object -First 1
      if ($null -ne $candidate) {
        return $candidate.FullName
      }
    }
  }

  Fail-Release -Code 'RELEASE_TOOL_MISSING' -Message "${StableName} is unavailable"
}

function Invoke-ExternalTool {
  param(
    [Parameter(Mandatory = $true)][string]$FilePath,
    [Parameter(Mandatory = $true)][string[]]$Arguments
  )

  $previousExitCodeVariable = Get-Variable -Name LASTEXITCODE -Scope Global -ErrorAction SilentlyContinue
  $previousExitCode = if ($null -ne $previousExitCodeVariable) { $previousExitCodeVariable.Value } else { $null }
  $global:LASTEXITCODE = $null
  $output = & $FilePath @Arguments 2>&1
  $succeeded = $?
  $currentExitCodeVariable = Get-Variable -Name LASTEXITCODE -Scope Global -ErrorAction SilentlyContinue
  $exitCode = if ($null -ne $currentExitCodeVariable -and $null -ne $currentExitCodeVariable.Value) {
    [int]$currentExitCodeVariable.Value
  } elseif ($succeeded) {
    0
  } else {
    1
  }
  if ($null -ne $previousExitCodeVariable) {
    $global:LASTEXITCODE = $previousExitCode
  } else {
    Clear-Variable -Name LASTEXITCODE -Scope Global -ErrorAction SilentlyContinue
  }
  $merged = (@($output | ForEach-Object {
        if ($_ -is [System.Management.Automation.ErrorRecord]) {
          $_.ToString()
        } else {
          [string]$_
        }
      }) | Where-Object { -not [string]::IsNullOrEmpty($_) }) -join [Environment]::NewLine
  return [pscustomobject]@{
    ExitCode = $exitCode
    Combined = $merged
  }
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

function Decode-ArchiveEntryPath {
  param([Parameter(Mandatory = $true)][string]$Path)

  $decodedSegments = @()
  foreach ($segment in $Path.Split('/')) {
    $decoded = $segment
    if ($segment.Contains('%')) {
      if ($segment -cnotmatch '^(?:[^%]|%[0-9A-Fa-f]{2})+$') {
        Fail-Release -Code 'RELEASE_VERIFICATION_FAILED' -Message "unsafe archive entry path: $Path"
      }
      $decoded = [Uri]::UnescapeDataString($segment)
      if ([Uri]::EscapeDataString($decoded) -cne $segment) {
        Fail-Release -Code 'RELEASE_VERIFICATION_FAILED' -Message "non-canonical archive entry path: $Path"
      }
    }
    if (
      [string]::IsNullOrWhiteSpace($decoded) -or
      $decoded -ceq '.' -or
      $decoded -ceq '..' -or
      $decoded.Contains('/') -or
      $decoded.Contains('\') -or
      $decoded.Contains('%') -or
      $decoded.Contains(':') -or
      $decoded -match '[\x00-\x1F\x7F]'
    ) {
      Fail-Release -Code 'RELEASE_VERIFICATION_FAILED' -Message "unsafe archive entry path: $Path"
    }
    $decodedSegments += $decoded
  }
  return [string]::Join('/', $decodedSegments)
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

function Get-ValidatedEntryMap {
  param([Parameter(Mandatory = $true)]$Archive)

  $map = @{}
  foreach ($entry in $Archive.Entries) {
    if ([string]::IsNullOrWhiteSpace($entry.Name) -and $entry.FullName.EndsWith('/')) {
      continue
    }
    $rawPath = [string]$entry.FullName
    $normalizedPath = Decode-ArchiveEntryPath -Path $rawPath
    $segments = @($normalizedPath.Split('/'))
    if (
      [string]::IsNullOrWhiteSpace($rawPath) -or
      $normalizedPath.StartsWith('/') -or
      $normalizedPath.Contains(':') -or
      (@($segments | Where-Object { [string]::IsNullOrWhiteSpace($_) -or $_ -eq '.' -or $_ -eq '..' })).Count -gt 0
    ) {
      Fail-Release -Code 'RELEASE_VERIFICATION_FAILED' -Message "unsafe archive entry path: $rawPath"
    }

    $canonicalPath = [string]::Join('/', $segments)
    $canonicalKey = $canonicalPath.ToLowerInvariant()
    if ($map.Contains($canonicalKey)) {
      Fail-Release -Code 'RELEASE_VERIFICATION_FAILED' -Message "duplicate archive entry alias: $rawPath"
    }
    $map[$canonicalKey] = [pscustomobject]@{
      CanonicalPath = $canonicalPath
      Entry = $entry
    }
  }
  return $map
}

function Resolve-StagedFile {
  param(
    [Parameter(Mandatory = $true)][string]$Root,
    [Parameter(Mandatory = $true)][string]$RelativePath,
    [Parameter(Mandatory = $true)][string]$Label
  )

  $invalidSegments = @($RelativePath.Split('/') | Where-Object { $_ -eq '.' -or $_ -eq '..' -or [string]::IsNullOrWhiteSpace($_) })
  if (
    [string]::IsNullOrWhiteSpace($RelativePath) -or
    $RelativePath.Contains('\') -or
    $RelativePath.Contains(':') -or
    $RelativePath.StartsWith('/') -or
    $invalidSegments.Count -gt 0
  ) {
    Fail-Release -Code 'RELEASE_VERIFICATION_FAILED' -Message "unsafe ${Label} path: $RelativePath"
  }

  $absolute = Join-Path $Root ($RelativePath -replace '/', [IO.Path]::DirectorySeparatorChar)
  try {
    $resolved = (Resolve-Path -LiteralPath $absolute).ProviderPath
  } catch {
    Fail-Release -Code 'RELEASE_VERIFICATION_FAILED' -Message "${Label} is missing from the staged root: $RelativePath"
  }
  $item = Get-Item -LiteralPath $resolved -Force
  if ($item.PSIsContainer -or ($item.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) {
    Fail-Release -Code 'RELEASE_VERIFICATION_FAILED' -Message "${Label} must be a regular file: $RelativePath"
  }
  $rootPrefix = ([IO.Path]::GetFullPath($Root)).TrimEnd([IO.Path]::DirectorySeparatorChar, [IO.Path]::AltDirectorySeparatorChar) + [IO.Path]::DirectorySeparatorChar
  $resolvedFull = [IO.Path]::GetFullPath($resolved)
  if (-not $resolvedFull.StartsWith($rootPrefix, [StringComparison]::OrdinalIgnoreCase)) {
    Fail-Release -Code 'RELEASE_VERIFICATION_FAILED' -Message "unsafe ${Label} path escapes the staged root: $RelativePath"
  }
  return $resolvedFull
}

function Get-PlaceholderLogoBytes {
  return [Convert]::FromBase64String($storeLogoBase64)
}

function Verify-Signature {
  param([Parameter(Mandatory = $true)][string]$PackagePath)

  $signatureTool = Resolve-ToolPath -Override ([string]$env:RELEASE_SIGNTOOL_PATH) -CommandName 'signtool.exe' -StableName 'signtool.exe'
  $result = Invoke-ExternalTool -FilePath $signatureTool -Arguments @('verify', '/pa', '/v', $PackagePath)
  if ($result.ExitCode -ne 0) {
    $detail = $result.Combined.Trim()
    if ([string]::IsNullOrWhiteSpace($detail)) {
      $detail = 'signtool.exe rejected the MSIX package signature'
    }
    Fail-Release -Code 'RELEASE_SIGNATURE_INVALID' -Message $detail
  }
}

Add-Type -AssemblyName System.IO.Compression.FileSystem

$packagePath = Resolve-RealFile -Path $Package -Label 'package'
$manifestPath = Resolve-RealFile -Path $Manifest -Label 'manifest'
$stagingRoot = Split-Path -Parent $manifestPath
$externalManifestBytes = [IO.File]::ReadAllBytes($manifestPath)
$nodePath = (Get-Command -Name 'node.exe' -ErrorAction SilentlyContinue | Select-Object -First 1).Source
if ([string]::IsNullOrWhiteSpace($nodePath)) {
  Fail-Release -Code 'RELEASE_TOOL_MISSING' -Message 'node.exe is unavailable for release manifest validation'
}
$manifestValidation = Invoke-ExternalTool -FilePath $nodePath -Arguments @($manifestValidator, '--manifest', $manifestPath, '--platform', 'windows')
if ($manifestValidation.ExitCode -ne 0) {
  Fail-Release -Code 'RELEASE_VERIFICATION_FAILED' -Message "release manifest schema/semantics are invalid: $($manifestValidation.Combined.Trim())"
}
$releaseManifest = Get-Content -LiteralPath $manifestPath -Raw | ConvertFrom-Json
$launcherArtifacts = @($releaseManifest.artifacts | Where-Object {
    [string]$_.relativePath -ceq 'app/code-oss-runtime/Code - OSS.exe' -and
    [string]$_.kind -ceq 'runtime' -and
    $_.executable -eq $true
  })
if ($launcherArtifacts.Count -ne 1) {
  Fail-Release -Code 'RELEASE_VERIFICATION_FAILED' -Message 'release manifest must bind exactly one runtime executable app/code-oss-runtime/Code - OSS.exe launcher'
}

$expectedPayloads = [ordered]@{}
foreach ($artifact in $releaseManifest.artifacts) {
  $stagedPath = Resolve-StagedFile -Root $stagingRoot -RelativePath ([string]$artifact.relativePath) -Label 'artifact'
  $stagedBytes = [IO.File]::ReadAllBytes($stagedPath)
  if ($stagedBytes.Length -ne [int64]$artifact.size) {
    Fail-Release -Code 'RELEASE_VERIFICATION_FAILED' -Message "artifact size does not match the release manifest: $($artifact.relativePath)"
  }
  if ((Get-Sha256Hex -Bytes $stagedBytes) -ne [string]$artifact.sha256) {
    Fail-Release -Code 'RELEASE_VERIFICATION_FAILED' -Message "artifact hash does not match the release manifest: $($artifact.relativePath)"
  }
  $expectedPayloads[[string]$artifact.relativePath] = [pscustomobject]@{
    Bytes = $stagedBytes
    Label = 'artifact'
  }
}
foreach ($license in $releaseManifest.licenses) {
  if ($null -eq $license.path -or [string]::IsNullOrWhiteSpace([string]$license.path)) {
    Fail-Release -Code 'RELEASE_VERIFICATION_FAILED' -Message 'license path is invalid in the embedded release manifest'
  }
  $stagedPath = Resolve-StagedFile -Root $stagingRoot -RelativePath ([string]$license.path) -Label 'license'
  $stagedBytes = [IO.File]::ReadAllBytes($stagedPath)
  if ($stagedBytes.Length -ne [int64]$license.size) {
    Fail-Release -Code 'RELEASE_VERIFICATION_FAILED' -Message "license size does not match the release manifest: $($license.path)"
  }
  if ((Get-Sha256Hex -Bytes $stagedBytes) -ne [string]$license.sha256) {
    Fail-Release -Code 'RELEASE_VERIFICATION_FAILED' -Message "license hash does not match the release manifest: $($license.path)"
  }
  $expectedPayloads[[string]$license.path] = [pscustomobject]@{
    Bytes = $stagedBytes
    Label = 'license'
  }
}
$expectedPayloads['release-manifest.json'] = [pscustomobject]@{
  Bytes = $externalManifestBytes
  Label = 'release manifest'
}
$expectedPayloads[$storeLogoPath] = [pscustomobject]@{
  Bytes = Get-PlaceholderLogoBytes
  Label = 'packaged logo'
}
foreach ($logoPath in @($square150LogoPath, $square44LogoPath)) {
  $expectedPayloads[$logoPath] = [pscustomobject]@{
    Bytes = Get-PlaceholderLogoBytes
    Label = 'packaged logo'
  }
}

$archive = [System.IO.Compression.ZipFile]::OpenRead($packagePath)
try {
  $entryMap = Get-ValidatedEntryMap -Archive $archive
  $entries = @($entryMap.Values | ForEach-Object { $_.CanonicalPath }) | Sort-Object
  $embeddedManifestEntry = $entryMap['appxmanifest.xml'].Entry
  if ($null -eq $embeddedManifestEntry) {
    Fail-Release -Code 'RELEASE_VERIFICATION_FAILED' -Message 'package does not contain AppxManifest.xml'
  }

  $embeddedReleaseManifestEntry = $entryMap['release-manifest.json'].Entry
  if ($null -eq $embeddedReleaseManifestEntry) {
    Fail-Release -Code 'RELEASE_VERIFICATION_FAILED' -Message 'package does not contain release-manifest.json'
  }

  $signaturePresent = $entries -contains 'AppxSignature.p7x'
  if ($RequireSignature) {
    if (-not $signaturePresent) {
      Fail-Release -Code 'RELEASE_VERIFICATION_FAILED' -Message 'required MSIX signature is absent'
    }
    Verify-Signature -PackagePath $packagePath
  }

  $payloadEntries = @(
    $entries |
      Where-Object {
        ($_ -notin $packageFootprintEntries) -and
        (-not $_.StartsWith('AppxMetadata/'))
      }
  ) | Sort-Object
  $expectedEntries = @($expectedPayloads.Keys) | Sort-Object
  if (($payloadEntries.Count -ne $expectedEntries.Count) -or (Compare-Object -ReferenceObject $expectedEntries -DifferenceObject $payloadEntries)) {
    Fail-Release -Code 'RELEASE_VERIFICATION_FAILED' -Message 'package payload does not match the expected staged payload set'
  }

  foreach ($path in $expectedEntries) {
    $entryRecord = $entryMap[$path.ToLowerInvariant()]
    if ($null -eq $entryRecord) {
      Fail-Release -Code 'RELEASE_VERIFICATION_FAILED' -Message "package payload is missing: $path"
    }
    $entry = $entryRecord.Entry
    $entryBytes = Read-EntryBytes -Entry $entry
    $expectedRecord = $expectedPayloads[$path]
    $expectedBytes = $expectedRecord.Bytes
    $expectedLabel = [string]$expectedRecord.Label
    if ($entryBytes.Length -ne $expectedBytes.Length) {
      Fail-Release -Code 'RELEASE_VERIFICATION_FAILED' -Message "${expectedLabel} size does not match the staged file: $path"
    }
    if ((Get-Sha256Hex -Bytes $entryBytes) -ne (Get-Sha256Hex -Bytes $expectedBytes)) {
      Fail-Release -Code 'RELEASE_VERIFICATION_FAILED' -Message "${expectedLabel} hash does not match the staged file: $path"
    }
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

  $properties = $embeddedAppxManifest.Package.Properties
  if ($null -eq $properties -or $properties.Logo -ne $storeLogoPath) {
    Fail-Release -Code 'RELEASE_VERIFICATION_FAILED' -Message 'AppxManifest.xml is missing the required deterministic logo entry'
  }
  $dependencies = $embeddedAppxManifest.Package.Dependencies
  if ($null -eq $dependencies -or $null -eq $dependencies.TargetDeviceFamily) {
    Fail-Release -Code 'RELEASE_VERIFICATION_FAILED' -Message 'AppxManifest.xml is missing the required dependency declaration'
  }
  $applications = $embeddedAppxManifest.Package.SelectSingleNode("*[local-name()='Applications']")
  $application = if ($null -ne $applications) { $applications.SelectSingleNode("*[local-name()='Application']") } else { $null }
  if ($null -eq $application) {
    Fail-Release -Code 'RELEASE_VERIFICATION_FAILED' -Message 'AppxManifest.xml is missing a runnable application entry point'
  }
  if ([string]$application.Executable -cne 'app\code-oss-runtime\Code - OSS.exe' -or [string]$application.EntryPoint -cne 'Windows.FullTrustApplication') {
    Fail-Release -Code 'RELEASE_VERIFICATION_FAILED' -Message 'AppxManifest.xml application entry point does not target the staged Code-OSS executable'
  }
} finally {
  $archive.Dispose()
}

Write-Output $packagePath
