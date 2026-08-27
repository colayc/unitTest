Set-StrictMode -Version Latest

function Throw-UnsafeMsixEntryPath {
  param(
    [Parameter(Mandatory = $true)][string]$RawPath,
    [string]$Reason = 'unsafe archive entry path'
  )

  throw "RELEASE_VERIFICATION_FAILED: ${Reason}: ${RawPath}"
}

function ConvertFrom-CanonicalMsixEntryPath {
  param([Parameter(Mandatory = $true)][AllowEmptyString()][string]$Path)

  if ([string]::IsNullOrWhiteSpace($Path) -or $Path.StartsWith('/') -or $Path.Contains('\')) {
    Throw-UnsafeMsixEntryPath -RawPath $Path
  }

  $decodedSegments = @()
  foreach ($segment in $Path.Split('/')) {
    if ([string]::IsNullOrEmpty($segment)) {
      Throw-UnsafeMsixEntryPath -RawPath $Path
    }

    $decoded = $segment
    if ($segment.Contains('%')) {
      if ($segment -cnotmatch '^(?:[^%]|%20)+$') {
        Throw-UnsafeMsixEntryPath -RawPath $Path -Reason 'non-canonical archive entry path'
      }
      $decoded = $segment.Replace('%20', ' ')
    }

    $trimmed = $decoded.Trim([char]' ')
    if (
      [string]::IsNullOrEmpty($decoded) -or
      $decoded -ceq '.' -or
      $decoded -ceq '..' -or
      $decoded -cne $trimmed -or
      $decoded.EndsWith('.') -or
      $decoded -match '[\x00-\x1F\x7F]' -or
      $decoded -match '[<>:"/\\|?*]' -or
      $decoded.Contains('%') -or
      $decoded -match '^(?i:CON|PRN|AUX|NUL|COM[1-9]|LPT[1-9])(?:\..*)?$'
    ) {
      Throw-UnsafeMsixEntryPath -RawPath $Path
    }
    $decodedSegments += $decoded
  }

  return [string]::Join('/', $decodedSegments)
}

function Get-CanonicalMsixEntryMap {
  param([Parameter(Mandatory = $true)]$Archive)

  $map = @{}
  $identities = @{}
  foreach ($entry in $Archive.Entries) {
    $rawPath = [string]$entry.FullName
    $isDirectory = [string]::IsNullOrEmpty([string]$entry.Name) -and $rawPath.EndsWith('/')
    $identityPath = if ($isDirectory) { $rawPath.Substring(0, $rawPath.Length - 1) } else { $rawPath }
    $canonicalPath = ConvertFrom-CanonicalMsixEntryPath -Path $identityPath
    $canonicalKey = $canonicalPath.ToLowerInvariant()
    if ($identities.ContainsKey($canonicalKey)) {
      throw "RELEASE_VERIFICATION_FAILED: duplicate archive entry alias: ${rawPath}"
    }
    $identities[$canonicalKey] = $rawPath

    if (-not $isDirectory) {
      $map[$canonicalKey] = [pscustomobject]@{
        CanonicalPath = $canonicalPath
        Entry = $entry
        RawPath = $rawPath
      }
    }
  }
  return $map
}
