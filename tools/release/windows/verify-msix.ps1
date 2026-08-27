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
. (Join-Path $PSScriptRoot 'msix-entry-path.ps1')

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

function Get-PlaceholderLogoBytes {
  return [Convert]::FromBase64String($storeLogoBase64)
}

function Assert-UniquePackagePayloadPath {
  param(
    [Parameter(Mandatory = $true)][string]$Path,
    [Parameter(Mandatory = $true)]$Seen,
    [Parameter(Mandatory = $true)]$Reserved
  )

  if (
    -not $Seen.Add($Path) -or
    $Reserved.Contains($Path) -or
    $Path.StartsWith('AppxMetadata/', [StringComparison]::OrdinalIgnoreCase)
  ) {
    Fail-Release -Code 'RELEASE_VERIFICATION_FAILED' -Message 'duplicate or reserved release payload path'
  }
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
$expectedPayloadIdentities = [Collections.Generic.HashSet[string]]::new([StringComparer]::OrdinalIgnoreCase)
$reservedPackagePayloads = [Collections.Generic.HashSet[string]]::new([StringComparer]::OrdinalIgnoreCase)
foreach ($reservedPath in @(
    'release-manifest.json',
    $storeLogoPath,
    $square150LogoPath,
    $square44LogoPath
  ) + $packageFootprintEntries) {
  [void]$reservedPackagePayloads.Add([string]$reservedPath)
}
foreach ($artifact in $releaseManifest.artifacts) {
  $artifactPath = [string]$artifact.relativePath
  Assert-UniquePackagePayloadPath -Path $artifactPath -Seen $expectedPayloadIdentities -Reserved $reservedPackagePayloads
  $expectedPayloads[$artifactPath] = [pscustomobject]@{
    Sha256 = [string]$artifact.sha256
    Size = [int64]$artifact.size
    Label = 'artifact'
  }
}
foreach ($license in $releaseManifest.licenses) {
  if ($null -eq $license.path -or [string]::IsNullOrWhiteSpace([string]$license.path)) {
    Fail-Release -Code 'RELEASE_VERIFICATION_FAILED' -Message 'license path is invalid in the embedded release manifest'
  }
  $licensePath = [string]$license.path
  Assert-UniquePackagePayloadPath -Path $licensePath -Seen $expectedPayloadIdentities -Reserved $reservedPackagePayloads
  $expectedPayloads[$licensePath] = [pscustomobject]@{
    Sha256 = [string]$license.sha256
    Size = [int64]$license.size
    Label = 'license'
  }
}
$expectedPayloads['release-manifest.json'] = [pscustomobject]@{
  Sha256 = Get-Sha256Hex -Bytes $externalManifestBytes
  Size = $externalManifestBytes.Length
  Label = 'release manifest'
}
$expectedPayloads[$storeLogoPath] = [pscustomobject]@{
  Sha256 = Get-Sha256Hex -Bytes (Get-PlaceholderLogoBytes)
  Size = (Get-PlaceholderLogoBytes).Length
  Label = 'packaged logo'
}
foreach ($logoPath in @($square150LogoPath, $square44LogoPath)) {
  $expectedPayloads[$logoPath] = [pscustomobject]@{
    Sha256 = Get-Sha256Hex -Bytes (Get-PlaceholderLogoBytes)
    Size = (Get-PlaceholderLogoBytes).Length
    Label = 'packaged logo'
  }
}

$archive = [System.IO.Compression.ZipFile]::OpenRead($packagePath)
try {
  $entryMap = Get-CanonicalMsixEntryMap -Archive $archive
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
    $expectedLabel = [string]$expectedRecord.Label
    if ($entryBytes.Length -ne [int64]$expectedRecord.Size) {
      Fail-Release -Code 'RELEASE_VERIFICATION_FAILED' -Message "${expectedLabel} size does not match the release manifest: $path"
    }
    if ((Get-Sha256Hex -Bytes $entryBytes) -ne [string]$expectedRecord.Sha256) {
      Fail-Release -Code 'RELEASE_VERIFICATION_FAILED' -Message "${expectedLabel} hash does not match the release manifest: $path"
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
