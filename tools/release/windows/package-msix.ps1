[CmdletBinding()]
param(
  [Parameter(Mandatory = $true)]
  [string]$StagingRoot,

  [Parameter(Mandatory = $true)]
  [string]$Output,

  [Parameter(Mandatory = $true)]
  [string]$Version,

  [Parameter(Mandatory = $true)]
  [string]$Publisher
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

$packageName = 'OpenAI.UnitTestIDE'
$toolRoot = Split-Path -Parent $MyInvocation.MyCommand.Path
$templatePath = Join-Path $toolRoot 'AppxManifest.xml.template'
$verifyScript = Join-Path $toolRoot 'verify-msix.ps1'
$storeLogoBase64 = 'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO7+4xQAAAAASUVORK5CYII='

function Fail-Release {
  param(
    [Parameter(Mandatory = $true)][string]$Code,
    [Parameter(Mandatory = $true)][string]$Message
  )

  throw "${Code}: ${Message}"
}

function Resolve-RealDirectory {
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
  if (-not $item.PSIsContainer -or ($item.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) {
    Fail-Release -Code 'RELEASE_INPUT_MISSING' -Message "${Label} must be a real directory"
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

function Normalize-ReleaseVersion {
  param([Parameter(Mandatory = $true)][string]$InputVersion)

  if ($InputVersion -notmatch '^(\d+)\.(\d+)\.(\d+)(?:[-+].+)?$') {
    Fail-Release -Code 'RELEASE_INPUT_MISSING' -Message "version must be semver-like: $InputVersion"
  }

  return "$($Matches[1]).$($Matches[2]).$($Matches[3]).0"
}

function Require-NoReparsePoints {
  param([Parameter(Mandatory = $true)][string]$Root)

  foreach ($item in Get-ChildItem -LiteralPath $Root -Recurse -Force) {
    if (($item.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) {
      Fail-Release -Code 'RELEASE_INPUT_MISSING' -Message "staging root must not contain reparse points: $($item.FullName)"
    }
  }
}

function Copy-StagingTree {
  param(
    [Parameter(Mandatory = $true)][string]$Source,
    [Parameter(Mandatory = $true)][string]$Destination
  )

  New-Item -ItemType Directory -Force -Path $Destination | Out-Null
  foreach ($item in Get-ChildItem -LiteralPath $Source -Force) {
    Copy-Item -LiteralPath $item.FullName -Destination $Destination -Recurse -Force
  }
}

function Escape-XmlAttribute {
  param([Parameter(Mandatory = $true)][string]$Value)

  return [Security.SecurityElement]::Escape($Value)
}

function New-AppxManifest {
  param(
    [Parameter(Mandatory = $true)][string]$TemplatePath,
    [Parameter(Mandatory = $true)][string]$DestinationPath,
    [Parameter(Mandatory = $true)][string]$PublisherSubject,
    [Parameter(Mandatory = $true)][string]$PackageVersion,
    [Parameter(Mandatory = $true)][string]$Architecture
  )

  $content = Get-Content -LiteralPath $TemplatePath -Raw
  $content = $content.Replace('__PACKAGE_NAME__', $packageName)
  $content = $content.Replace('__PUBLISHER__', (Escape-XmlAttribute -Value $PublisherSubject))
  $content = $content.Replace('__PACKAGE_VERSION__', $PackageVersion)
  $content = $content.Replace('__ARCHITECTURE__', $Architecture)
  [IO.File]::WriteAllText($DestinationPath, $content, [Text.UTF8Encoding]::new($false))
}

function Write-PlaceholderResources {
  param([Parameter(Mandatory = $true)][string]$Root)

  $assetDirectory = Join-Path $Root 'Assets'
  New-Item -ItemType Directory -Force -Path $assetDirectory | Out-Null
  [IO.File]::WriteAllBytes((Join-Path $assetDirectory 'StoreLogo.png'), [Convert]::FromBase64String($storeLogoBase64))
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

function Require-SigningInputs {
  $certificatePath = [string]$env:RELEASE_SIGNING_PFX_PATH
  $certificatePassword = [string]$env:RELEASE_SIGNING_PFX_PASSWORD
  if ([string]::IsNullOrWhiteSpace($certificatePath) -or [string]::IsNullOrWhiteSpace($certificatePassword)) {
    Fail-Release -Code 'RELEASE_SIGNING_REQUIRED' -Message 'release signing is required but the certificate inputs are absent'
  }
  try {
    $resolved = (Resolve-Path -LiteralPath $certificatePath).ProviderPath
  } catch {
    Fail-Release -Code 'RELEASE_SIGNING_REQUIRED' -Message 'release signing is required but the certificate path is unavailable'
  }
  return @{
    Password = $certificatePassword
    Path = $resolved
  }
}

$stagingRootPath = Resolve-RealDirectory -Path $StagingRoot -Label 'staging root'
Require-NoReparsePoints -Root $stagingRootPath
$templateFile = Resolve-Path -LiteralPath $templatePath
$verifyFile = Resolve-Path -LiteralPath $verifyScript
$makeAppxPath = Resolve-ToolPath -Override ([string]$env:RELEASE_MAKEAPPX_PATH) -CommandName 'makeappx.exe' -StableName 'makeappx.exe'
$packageVersion = Normalize-ReleaseVersion -InputVersion $Version
$signingRequired = [string]$env:RELEASE_SIGNING_REQUIRED
if ([string]::IsNullOrWhiteSpace($signingRequired)) {
  $signingRequired = '1'
}
if ($signingRequired -notin @('0', '1')) {
  Fail-Release -Code 'RELEASE_INPUT_MISSING' -Message 'RELEASE_SIGNING_REQUIRED must be 0 or 1'
}
$signing = $null
if ($signingRequired -eq '1') {
  $signing = Require-SigningInputs
}

$manifestPath = Join-Path $stagingRootPath 'release-manifest.json'
if (-not (Test-Path -LiteralPath $manifestPath -PathType Leaf)) {
  Fail-Release -Code 'RELEASE_INPUT_MISSING' -Message 'staging release-manifest.json is required'
}
$releaseManifest = Get-Content -LiteralPath $manifestPath -Raw | ConvertFrom-Json
if ($releaseManifest.version -ne $Version) {
  Fail-Release -Code 'RELEASE_INPUT_MISSING' -Message 'staging release-manifest.json version does not match the requested version'
}
if ($releaseManifest.platform -ne 'windows') {
  Fail-Release -Code 'RELEASE_INPUT_MISSING' -Message 'staging release-manifest.json must target the windows platform'
}

$outputPath = [IO.Path]::GetFullPath($Output)
$outputDirectory = Split-Path -Parent $outputPath
if ([string]::IsNullOrWhiteSpace($outputDirectory)) {
  $outputDirectory = (Get-Location).Path
}
New-Item -ItemType Directory -Force -Path $outputDirectory | Out-Null
$temporaryRoot = Join-Path $outputDirectory ('.msix-staging-' + [Guid]::NewGuid().ToString('N'))

try {
  New-Item -ItemType Directory -Force -Path $temporaryRoot | Out-Null
  Copy-StagingTree -Source $stagingRootPath -Destination $temporaryRoot
  Write-PlaceholderResources -Root $temporaryRoot
  New-AppxManifest -TemplatePath $templateFile.ProviderPath -DestinationPath (Join-Path $temporaryRoot 'AppxManifest.xml') -PublisherSubject $Publisher -PackageVersion $packageVersion -Architecture ([string]$releaseManifest.architecture)

  if (Test-Path -LiteralPath $outputPath) {
    Remove-Item -LiteralPath $outputPath -Force
  }

  $makeAppxResult = Invoke-ExternalTool -FilePath $makeAppxPath -Arguments @('pack', '/d', $temporaryRoot, '/p', $outputPath, '/o')
  if ($makeAppxResult.ExitCode -ne 0 -or -not (Test-Path -LiteralPath $outputPath -PathType Leaf)) {
    $detail = $makeAppxResult.Combined.Trim()
    if ([string]::IsNullOrWhiteSpace($detail)) {
      $detail = 'makeappx.exe failed to create the MSIX package'
    }
    Fail-Release -Code 'RELEASE_PACKAGING_FAILED' -Message $detail
  }

  if ($signingRequired -eq '1') {
    $signToolPath = Resolve-ToolPath -Override ([string]$env:RELEASE_SIGNTOOL_PATH) -CommandName 'signtool.exe' -StableName 'signtool.exe'
    $signResult = Invoke-ExternalTool -FilePath $signToolPath -Arguments @('sign', '/fd', 'SHA256', '/f', $signing.Path, '/p', $signing.Password, $outputPath)
    if ($signResult.ExitCode -ne 0) {
      $detail = $signResult.Combined.Trim()
      if ([string]::IsNullOrWhiteSpace($detail)) {
        $detail = 'signtool.exe failed to sign the MSIX package'
      }
      Fail-Release -Code 'RELEASE_SIGNING_REQUIRED' -Message $detail
    }
    & $verifyFile.ProviderPath -Package $outputPath -Manifest $manifestPath -RequireSignature | Out-Null
  } else {
    & $verifyFile.ProviderPath -Package $outputPath -Manifest $manifestPath | Out-Null
  }
} finally {
  if (Test-Path -LiteralPath $temporaryRoot) {
    Remove-Item -LiteralPath $temporaryRoot -Recurse -Force
  }
}

Write-Output $outputPath
