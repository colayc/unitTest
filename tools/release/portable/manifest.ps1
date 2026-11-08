Set-StrictMode -Version Latest
$script:PortableBaseCommit = '59928bac784fedff614f7d154ef227f15b191eee'
$script:PortableBaseManifestSha256 = '2915294DD0F49A91EDC8CBF29B5F24149D02E20C41183E6FEC0D8986B6C67870'
$script:PortableSourceMetadata = @('AppxManifest.xml','AppxBlockMap.xml','Assets/Square150x150Logo.png','Assets/Square44x44Logo.png','Assets/StoreLogo.png')
$script:PortableAddedFiles = @('UnitTestIDE.exe','README.zh-CN.txt','launcher-build.json','portable-manifest.json')

function Assert-PortableRelativePath([string]$RelativePath) {
    if ([string]::IsNullOrEmpty($RelativePath)) { throw 'PATH_EMPTY' }
    foreach ($Part in $RelativePath.Split('/')) {
        if ($Part -notmatch '^[A-Za-z0-9._+@() -]+$' -or $Part -in @('.','..') -or
            $Part.StartsWith(' ') -or $Part.EndsWith(' ') -or $Part.EndsWith('.') -or
            $Part.Split('.')[0] -match '^(?i:CON|PRN|AUX|NUL|COM[1-9]|LPT[1-9])$') { throw 'PATH_NOT_PORTABLE' }
    }
}
function Assert-ObservedAttributes([IO.FileAttributes]$Attributes) {
    if (($Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) { throw 'PATH_REPARSE' }
    if (($Attributes -band [IO.FileAttributes]::Device) -ne 0) { throw 'PATH_DEVICE' }
}
function Assert-AbsoluteLocalPath([string]$Path) {
    if (-not [IO.Path]::IsPathFullyQualified($Path) -or $Path.StartsWith('\\')) { throw 'PATH_NOT_LOCAL_ABSOLUTE' }
    $Full = [IO.Path]::GetFullPath($Path)
    $Volume = [IO.Path]::GetPathRoot($Full)
    if ($Full.Length -gt $Volume.Length) { $Full = $Full.TrimEnd([char[]]'\/') }
    $Current = $Volume
    Assert-ObservedAttributes ([IO.File]::GetAttributes($Current))
    foreach ($Part in $Full.Substring($Volume.Length).Split([char[]]'\/',[StringSplitOptions]::RemoveEmptyEntries)) {
        $Current = [IO.Path]::Combine($Current,$Part)
        if ([IO.File]::Exists($Current) -or [IO.Directory]::Exists($Current)) {
            Assert-ObservedAttributes ([IO.File]::GetAttributes($Current))
        } else {
            $Parent = [IO.Path]::GetDirectoryName($Current)
            if ([IO.Directory]::Exists($Parent)) {
                foreach ($Entry in [IO.Directory]::EnumerateFileSystemEntries($Parent)) {
                    if ([string]::Equals($Entry,$Current,[StringComparison]::OrdinalIgnoreCase)) {
                        Assert-ObservedAttributes ([IO.File]::GetAttributes($Entry))
                    }
                }
            }
        }
    }
    return $Full
}
function Assert-LocalPath([string]$Root,[string]$RelativePath,[bool]$Directory) {
    $Base = Assert-AbsoluteLocalPath $Root
    if ($RelativePath -eq '.') { $Full = $Base } else {
        Assert-PortableRelativePath $RelativePath
        $Full = [IO.Path]::GetFullPath([IO.Path]::Combine($Base,$RelativePath.Replace('/',[IO.Path]::DirectorySeparatorChar)))
        if (-not $Full.StartsWith($Base+[IO.Path]::DirectorySeparatorChar,[StringComparison]::OrdinalIgnoreCase)) { throw 'PATH_ESCAPE' }
    }
    [void](Assert-AbsoluteLocalPath $Full)
    $Attr = [IO.File]::GetAttributes($Full)
    Assert-ObservedAttributes $Attr
    if ((($Attr -band [IO.FileAttributes]::Directory) -ne 0) -ne $Directory) { throw 'PATH_TYPE' }
    return $Full
}
function Get-FileBinding([string]$Path) {
    [void](Assert-AbsoluteLocalPath $Path)
    Assert-ObservedAttributes ([IO.File]::GetAttributes($Path))
    $Stream = [IO.FileStream]::new($Path,[IO.FileMode]::Open,[IO.FileAccess]::Read,[IO.FileShare]::Read)
    try {
        $Size = $Stream.Length
        $Hasher = [Security.Cryptography.SHA256]::Create()
        try { $Digest = [Convert]::ToHexString($Hasher.ComputeHash($Stream)).ToLowerInvariant() }
        finally { $Hasher.Dispose() }
        return [pscustomobject]@{path=$Path;length=$Size;sha256=$Digest}
    } finally { $Stream.Dispose() }
}
function Read-ProtectedBytes([string]$Path,[long]$MaximumBytes=67108864) {
    [void](Assert-AbsoluteLocalPath $Path)
    Assert-ObservedAttributes ([IO.File]::GetAttributes($Path))
    $Stream=[IO.FileStream]::new($Path,[IO.FileMode]::Open,[IO.FileAccess]::Read,[IO.FileShare]::Read)
    $Memory=[IO.MemoryStream]::new()
    try {
        if($MaximumBytes -lt 1 -or $Stream.Length -gt $MaximumBytes){throw 'BASIC_JSON_TOO_LARGE'}
        $Stream.CopyTo($Memory); $Bytes=$Memory.ToArray()
    }
    finally { $Stream.Dispose(); $Memory.Dispose() }
    $Digest=[Convert]::ToHexString([Security.Cryptography.SHA256]::HashData($Bytes)).ToLowerInvariant()
    return [pscustomobject]@{bytes=$Bytes;length=$Bytes.LongLength;sha256=$Digest}
}
function Assert-KeySet($Value,[string[]]$Keys) {
    if ($Value -isnot [System.Collections.IDictionary] -or $Value.Count -ne $Keys.Count) { throw 'JSON_KEYS' }
    $Actual=[Collections.Generic.HashSet[string]]::new([StringComparer]::Ordinal)
    foreach($K in $Value.Keys){[void]$Actual.Add([string]$K)}
    foreach($K in $Keys){if(-not $Actual.Contains($K)){throw 'JSON_KEYS'}}
}
function Assert-SizeAndDigest($Size,$Digest) {
    if (($Size -isnot [long] -and $Size -isnot [int]) -or $Size -lt 0 -or $Size -gt 9007199254740991 -or
        $Digest -isnot [string] -or $Digest -cnotmatch '^[0-9a-f]{64}$') { throw 'RECORD_SIZE_OR_DIGEST' }
}
function Get-OrdinalStrings([string[]]$Values) {
    $Copy=[string[]]$Values.Clone()
    [Array]::Sort($Copy,[StringComparer]::Ordinal)
    return ,$Copy
}
function Get-TreeFilePaths([string]$Root) {
    $Base=Assert-LocalPath $Root '.' $true
    $Queue=[Collections.Generic.Queue[IO.DirectoryInfo]]::new()
    $Queue.Enqueue([IO.DirectoryInfo]::new($Base))
    $Files=[Collections.Generic.List[string]]::new()
    while($Queue.Count -gt 0){
        $Dir=$Queue.Dequeue()
        foreach($Entry in $Dir.EnumerateFileSystemInfos()){
            Assert-ObservedAttributes $Entry.Attributes
            if($Entry -is [IO.DirectoryInfo]){$Queue.Enqueue($Entry)}
            elseif($Entry -is [IO.FileInfo]){
                $Relative=[IO.Path]::GetRelativePath($Base,$Entry.FullName).Replace('\','/')
                Assert-PortableRelativePath $Relative
                $Files.Add($Relative)
            } else { throw 'TREE_SPECIAL_ENTRY' }
        }
    }
    return ,(Get-OrdinalStrings $Files.ToArray())
}
function Assert-Tree([string]$Root,[object[]]$Expected,[string[]]$AllowedMetadata=@()) {
    $Paths=Get-TreeFilePaths $Root
    $Map=[Collections.Generic.Dictionary[string,object]]::new([StringComparer]::Ordinal)
    $Folded=[Collections.Generic.HashSet[string]]::new([StringComparer]::OrdinalIgnoreCase)
    foreach($Record in $Expected){
        Assert-PortableRelativePath $Record.path
        if(-not $Folded.Add($Record.path)){throw 'TREE_CASE_COLLISION'}
        $Map.Add($Record.path,$Record)
    }
    foreach($Extra in $AllowedMetadata){
        Assert-PortableRelativePath $Extra
        if(-not $Folded.Add($Extra)){throw 'TREE_CASE_COLLISION'}
    }
    if($Paths.Count -ne $Expected.Count+$AllowedMetadata.Count){throw 'TREE_FILE_SET'}
    foreach($Path in $Paths){if(-not $Map.ContainsKey($Path) -and $Path -cnotin $AllowedMetadata){throw 'TREE_FILE_SET'}}
    $Actual=[Collections.Generic.List[object]]::new()
    foreach($Record in $Expected){
        $Full=Assert-LocalPath $Root $Record.path $false
        $Binding=Get-FileBinding $Full
        if($Binding.length -ne $Record.length -or $Binding.sha256 -cne $Record.sha256){throw "TREE_DIGEST $($Record.path)"}
        $Actual.Add([pscustomobject]@{path=$Record.path;length=$Binding.length;sha256=$Binding.sha256})
    }
    foreach($Extra in $AllowedMetadata){[void](Assert-LocalPath $Root $Extra $false)}
    return [pscustomobject]@{count=$Actual.Count;bindings=$Actual.ToArray()}
}
function Read-InventoryCore {
    param([string]$Root,[string]$ManifestSha256,[string]$ExpectedCommit,
          [int]$ArtifactCount,[int]$LicenseCount,[string[]]$AllowedMetadata=@())
    $Path=Assert-LocalPath $Root 'release-manifest.json' $false
    $Raw=Read-ProtectedBytes $Path
    if($Raw.sha256 -cne $ManifestSha256.ToLowerInvariant()){throw 'BASE_MANIFEST_DRIFT'}
    $M=[Text.UTF8Encoding]::new($false,$true).GetString($Raw.bytes)|ConvertFrom-Json -AsHashtable -Depth 100
    Assert-KeySet $M @('schemaVersion','product','version','platform','architecture','sourceCommit','artifacts','licenses','generatedAt')
    if($M.schemaVersion -ne 1 -or $M.product -cne 'unit-test-ide' -or $M.version -cne '0.1.0' -or
        $M.platform -cne 'windows' -or $M.architecture -cne 'x64' -or $M.sourceCommit -cne $ExpectedCommit){throw 'BASE_SOURCE_IDENTITY'}
    if($M.artifacts -isnot [array] -or $M.licenses -isnot [array] -or
        $M.artifacts.Count -ne $ArtifactCount -or $M.licenses.Count -ne $LicenseCount){throw 'BASE_COUNTS'}
    $IDs=[Collections.Generic.HashSet[string]]::new([StringComparer]::Ordinal)
    $Paths=[Collections.Generic.HashSet[string]]::new([StringComparer]::OrdinalIgnoreCase)
    $Forbidden=@('release-manifest.json')+$script:PortableAddedFiles
    $Records=[Collections.Generic.List[object]]::new()
    foreach($A in $M.artifacts){
        Assert-KeySet $A @('id','kind','relativePath','size','sha256','executable')
        if($A.id -isnot [string] -or $A.id -cnotmatch '^[a-z0-9]+(?:-[a-z0-9]+)*$' -or
           $A.kind -isnot [string] -or $A.kind -cnotmatch '^[a-z][a-z0-9-]*$' -or
           $A.executable -isnot [bool] -or -not $IDs.Add($A.id)){throw 'ARTIFACT_SHAPE'}
        Assert-PortableRelativePath $A.relativePath
        Assert-SizeAndDigest $A.size $A.sha256
        if($A.relativePath -in $Forbidden -or -not $Paths.Add($A.relativePath)){throw 'TREE_CASE_COLLISION'}
        $Records.Add([pscustomobject]@{path=$A.relativePath;length=[long]$A.size;sha256=$A.sha256})
    }
    foreach($L in $M.licenses){
        Assert-KeySet $L @('path','size','sha256')
        Assert-PortableRelativePath $L.path
        Assert-SizeAndDigest $L.size $L.sha256
        if($L.path -in $Forbidden -or -not $Paths.Add($L.path)){throw 'TREE_CASE_COLLISION'}
        $Records.Add([pscustomobject]@{path=$L.path;length=[long]$L.size;sha256=$L.sha256})
    }
    return [pscustomobject]@{
        manifestBinding=[pscustomobject]@{path='release-manifest.json';length=$Raw.length;sha256=$Raw.sha256}
        sourceCommit=$M.sourceCommit;version=$M.version;files=$Records.ToArray()
    }
}
function Read-BaseInventory([string]$Root) {
    $Inventory=Read-InventoryCore -Root $Root -ManifestSha256 $script:PortableBaseManifestSha256 -ExpectedCommit $script:PortableBaseCommit -ArtifactCount 10036 -LicenseCount 47
    if($Inventory.manifestBinding.length -ne 3527252){throw 'BASE_MANIFEST_SIZE'}
    [void](Assert-Tree $Root (@($Inventory.files)+@($Inventory.manifestBinding)) $script:PortableSourceMetadata)
    return $Inventory
}
function Assert-LegacyPortableTree([string]$Root) {
    $Base=Read-InventoryCore -Root $Root -ManifestSha256 $script:PortableBaseManifestSha256 -ExpectedCommit $script:PortableBaseCommit -ArtifactCount 10036 -LicenseCount 47
    if($Base.manifestBinding.length -ne 3527252){throw 'BASE_MANIFEST_SIZE'}
    $Raw=Read-ProtectedBytes (Assert-LocalPath $Root 'portable-manifest.json' $false)
    $P=[Text.UTF8Encoding]::new($false,$true).GetString($Raw.bytes)|ConvertFrom-Json -AsHashtable -Depth 100
    Assert-KeySet $P @('schemaVersion','product','version','platform','architecture','baseSourceCommit','baseManifestSha256','buildId','launcherSourceState','files','self','generationStatus','signingStatus')
    Assert-KeySet $P.self @('path','hashExcluded')
    if($P.schemaVersion -ne 1 -or $P.product -cne 'unit-test-ide' -or $P.version -cne '0.1.0' -or
       $P.platform -cne 'windows' -or $P.architecture -cne 'x64' -or $P.baseSourceCommit -cne $script:PortableBaseCommit -or
       $P.baseManifestSha256 -cne $script:PortableBaseManifestSha256.ToLowerInvariant() -or
       $P.generationStatus -cne 'PAUSED_NOT_ACCEPTED' -or $P.signingStatus -cne 'UNSIGNED_TEST_ONLY' -or
       $P.launcherSourceState -cne 'LOCAL_UNCOMMITTED' -or $P.self.path -cne 'portable-manifest.json' -or
       $P.self.hashExcluded -isnot [bool] -or -not $P.self.hashExcluded -or
       $P.buildId -cnotmatch '^[0-9]{8}T[0-9]{6}Z-[0-9a-f]{8}$' -or $P.files.Count -ne 10087){throw 'PORTABLE_SHAPE'}
    $Expected=[Collections.Generic.List[object]]::new()
    $Map=[Collections.Generic.Dictionary[string,object]]::new([StringComparer]::Ordinal)
    foreach($F in $P.files){
        Assert-KeySet $F @('path','size','sha256')
        Assert-PortableRelativePath $F.path
        Assert-SizeAndDigest $F.size $F.sha256
        if($F.path -ceq 'portable-manifest.json' -or $Map.ContainsKey($F.path)){throw 'PORTABLE_FILE_SET'}
        $R=[pscustomobject]@{path=$F.path;length=[long]$F.size;sha256=$F.sha256}
        $Map.Add($F.path,$R);$Expected.Add($R)
    }
    $Original=@($Base.files)+@($Base.manifestBinding)
    foreach($F in $Original){
        if(-not $Map.ContainsKey($F.path)){throw 'PORTABLE_BASE_MISSING'}
        $V=$Map[$F.path]
        if($V.length -ne $F.length -or $V.sha256 -cne $F.sha256){throw 'PORTABLE_BASE_DRIFT'}
    }
    $Added=@('UnitTestIDE.exe','README.zh-CN.txt','launcher-build.json')
    foreach($A in $Added){if(-not $Map.ContainsKey($A)){throw 'PORTABLE_ADDED_MISSING'}}
    [void](Assert-Tree $Root $Expected.ToArray() @('portable-manifest.json'))
    $Expected.Add([pscustomobject]@{path='portable-manifest.json';length=$Raw.length;sha256=$Raw.sha256})
    return [pscustomobject]@{count=$Expected.Count;files=$Expected.ToArray();manifestSha256=$Raw.sha256;buildId=$P.buildId}
}

function New-SafeDirectory([string]$Path) {
    $Full=Assert-AbsoluteLocalPath $Path
    [void][IO.Directory]::CreateDirectory($Full)
    [void](Assert-LocalPath $Full '.' $true)
    return $Full
}
function Write-NewUtf8File([string]$Path,[string]$Text) {
    [void](Assert-AbsoluteLocalPath ([IO.Path]::GetDirectoryName($Path)))
    $Stream=[IO.FileStream]::new($Path,[IO.FileMode]::CreateNew,[IO.FileAccess]::Write,[IO.FileShare]::None)
    try { $Bytes=[Text.UTF8Encoding]::new($false).GetBytes($Text);$Stream.Write($Bytes,0,$Bytes.Length);$Stream.Flush($true) }
    finally { $Stream.Dispose() }
}
function Write-NewJson([string]$Path,$Value) {
    Write-NewUtf8File $Path (($Value|ConvertTo-Json -Depth 100 -Compress)+[Environment]::NewLine)
}

# v2 basic package validation. Legacy raw-resource validation above is intentionally retained.
function ConvertFrom-BasicJsonBytes([byte[]]$Bytes) {
    $Text=[Text.UTF8Encoding]::new($false,$true).GetString($Bytes)
    $Document=[Text.Json.JsonDocument]::Parse($Text)
    try {
        function Assert-UniqueBasicJsonElement($Element,[int]$Depth) {
            if($Depth -gt 100){throw 'BASIC_JSON_DEPTH'}
            if($Element.ValueKind -eq [Text.Json.JsonValueKind]::Object) {
                $Seen=[Collections.Generic.HashSet[string]]::new([StringComparer]::Ordinal)
                foreach($Property in $Element.EnumerateObject()) {
                    if(-not $Seen.Add($Property.Name)){throw 'BASIC_JSON_DUPLICATE_KEY'}
                    Assert-UniqueBasicJsonElement $Property.Value ($Depth+1)
                }
            } elseif($Element.ValueKind -eq [Text.Json.JsonValueKind]::Array) {
                foreach($Item in $Element.EnumerateArray()){Assert-UniqueBasicJsonElement $Item ($Depth+1)}
            }
        }
        Assert-UniqueBasicJsonElement $Document.RootElement 0
    } finally {$Document.Dispose()}
    return ($Text|ConvertFrom-Json -AsHashtable -Depth 100)
}
function Read-BasicBoundJson([string]$Path,[string]$ExpectedSha256='',[long]$ExpectedSize=-1) {
    $Raw=Read-ProtectedBytes $Path
    if($ExpectedSha256 -ne '' -and ($ExpectedSha256 -notmatch '^[0-9a-fA-F]{64}$' -or $Raw.sha256 -cne $ExpectedSha256.ToLowerInvariant())){throw 'BASIC_JSON_BINDING_DRIFT'}
    if($ExpectedSize -ge 0 -and $Raw.length -ne $ExpectedSize){throw 'BASIC_JSON_BINDING_DRIFT'}
    return [pscustomobject]@{value=(ConvertFrom-BasicJsonBytes $Raw.bytes);binding=[ordered]@{path=[IO.Path]::GetFullPath($Path);size=$Raw.length;sha256=$Raw.sha256}}
}
function Read-BasicJson([string]$Path) {
    return (Read-BasicBoundJson $Path).value
}
function Get-BasicBinding([string]$Path) {
    $B=Get-FileBinding $Path
    return [ordered]@{path=[IO.Path]::GetFullPath($Path);size=$B.length;sha256=$B.sha256}
}
function ConvertTo-BasicCanonicalJson($Value) {
    if($null -eq $Value){return 'null'}
    if($Value -is [Collections.IDictionary]) {
        $Parts=@(foreach($K in (Get-OrdinalStrings ([string[]]@($Value.Keys)))) {
            ($K|ConvertTo-Json -Compress)+':'+(ConvertTo-BasicCanonicalJson $Value[$K])
        })
        return '{'+($Parts -join ',')+'}'
    }
    if($Value -is [Collections.IEnumerable] -and $Value -isnot [string]) {
        $Parts=@(foreach($Item in $Value){ConvertTo-BasicCanonicalJson $Item})
        return '['+($Parts -join ',')+']'
    }
    return ($Value|ConvertTo-Json -Depth 100 -Compress)
}
function Assert-BasicSameValue($Expected,$Actual,[string]$Code) {
    if((ConvertTo-BasicCanonicalJson $Expected) -cne (ConvertTo-BasicCanonicalJson $Actual)){throw $Code}
}
function Get-BasicResourceFiles($M) {
    Assert-KeySet $M @('schemaVersion','product','version','platform','architecture','sourceCommit','artifacts','licenses','generatedAt')
    if($M.schemaVersion -ne 1 -or $M.product -cne 'unit-test-ide' -or $M.version -cne '0.1.0' -or
       $M.platform -cne 'windows' -or $M.architecture -cne 'x64' -or $M.sourceCommit -cne $script:PortableBaseCommit -or
       $M.artifacts -isnot [array] -or $M.licenses -isnot [array] -or $M.artifacts.Count -eq 0 -or $M.licenses.Count -eq 0){throw 'BASIC_RESOURCE_IDENTITY'}
    $Records=[Collections.Generic.List[object]]::new()
    $Seen=[Collections.Generic.HashSet[string]]::new([StringComparer]::OrdinalIgnoreCase)
    foreach($A in $M.artifacts) {
        Assert-KeySet $A @('id','kind','relativePath','size','sha256','executable')
        Assert-PortableRelativePath $A.relativePath
        Assert-SizeAndDigest $A.size $A.sha256
        if(-not $Seen.Add($A.relativePath)){throw 'BASIC_RESOURCE_DUPLICATE'}
        if($A.relativePath.StartsWith('app/code-oss-runtime/',[StringComparison]::Ordinal) -or
           $A.relativePath.StartsWith('bundles/',[StringComparison]::Ordinal) -or
           $A.relativePath.StartsWith('licenses/',[StringComparison]::Ordinal)) {
            $Records.Add([ordered]@{path=$A.relativePath;size=$A.size;sha256=$A.sha256})
        }
    }
    foreach($L in $M.licenses) {
        Assert-KeySet $L @('path','size','sha256')
        Assert-PortableRelativePath $L.path;Assert-SizeAndDigest $L.size $L.sha256
        if(-not $Seen.Add($L.path) -or $L.path.StartsWith('service/') -or $L.path.StartsWith('app/extensions/')){throw 'BASIC_RESOURCE_DUPLICATE_OR_APPLICATION'}
        $Records.Add($L)
    }
    return ,@($Records.ToArray()|Sort-Object -Property path -CaseSensitive)
}
function Assert-BasicSourceIdentity($Value) {
    if($Value.motherCommit -cne '11b4dc310622776f5504bfc77515d07c55d63b85' -or
       $Value.motherTree -cne '8350115f9d8fa1947ea1f91681175fd351b3da3e' -or
       $Value.sourceState -cne 'LOCAL_UNCOMMITTED_DERIVATIVE' -or $Value.productMode -cne 'basic' -or
       $Value.protocolVersion -cne '1.7' -or $Value.buildId -cnotmatch '^[0-9]{8}T[0-9]{6}Z-[0-9a-f]{8}$'){throw 'BASIC_SOURCE_IDENTITY'}
}
function Convert-BasicFiles([object[]]$Files,[string[]]$Forbidden=@()) {
    if($Files.Count -eq 0){throw 'BASIC_FILE_SET'}
    $Seen=[Collections.Generic.HashSet[string]]::new([StringComparer]::OrdinalIgnoreCase)
    return ,@(foreach($F in $Files) {
        Assert-KeySet $F @('path','size','sha256')
        Assert-PortableRelativePath $F.path
        Assert-SizeAndDigest $F.size $F.sha256
        if($F.path -cin $Forbidden -or -not $Seen.Add($F.path)){throw 'BASIC_FILE_SET'}
        [pscustomobject]@{path=$F.path;length=$F.size;sha256=$F.sha256}
    })
}
function Read-BasicAppInventoryCore([string]$Root,[string]$InputsFile,[string]$ExpectedSha256,[string]$ExpectedResourceManifestSha256,[switch]$PortableView) {
    if($ExpectedSha256 -cnotmatch '^[0-9a-f]{64}$'){throw 'BASIC_APP_INPUT_HASH'}
    $Read=Read-BasicBoundJson $InputsFile $ExpectedSha256
    $B=[pscustomobject]@{length=$Read.binding.size;sha256=$Read.binding.sha256}
    if(
       -not [string]::Equals([IO.Path]::GetFullPath($InputsFile),(Join-Path (Assert-AbsoluteLocalPath $Root) 'app-inputs.json'),[StringComparison]::OrdinalIgnoreCase)){throw 'BASIC_APP_INPUT_DRIFT'}
    $M=$Read.value
    Assert-KeySet $M @('schemaVersion','buildId','motherCommit','motherTree','sourceState','fixedInputsSha256','resourceSource','productMode','protocolVersion','files','self','generationStatus')
    Assert-BasicSourceIdentity $M
    Assert-KeySet $M.self @('path','hashExcluded')
    Assert-KeySet $M.resourceSource @('sourceCommit','msixSha256','manifestSha256')
    if($M.schemaVersion -ne 1 -or $M.fixedInputsSha256 -cnotmatch '^[0-9a-f]{64}$' -or $M.generationStatus -cne 'PAUSED_RUNTIME_DISABLED' -or
       $M.self.path -cne 'app-inputs.json' -or $M.self.hashExcluded -isnot [bool] -or -not $M.self.hashExcluded -or
       $M.resourceSource.sourceCommit -cne $script:PortableBaseCommit -or $M.resourceSource.manifestSha256 -cne $ExpectedResourceManifestSha256 -or
       $M.resourceSource.msixSha256 -cne 'b9311444f1b1c831c3f513eb37188b5339dca133bb236525031a70fd0548737b'){throw 'BASIC_APP_SHAPE'}
    $Files=Convert-BasicFiles $M.files @('app-inputs.json','portable-manifest.json','UnitTestIDE.exe','launcher-build.json','README.zh-CN.txt')
    $Metadata=@('app-inputs.json')
    if($PortableView){$Metadata+=@('UnitTestIDE.exe','README.zh-CN.txt','launcher-build.json','portable-manifest.json')}
    [void](Assert-Tree $Root $Files $Metadata)
    $Paths=@($Files|ForEach-Object path)
    foreach($P in @('portable-product.json','service/unit-test-service.exe','app/code-oss-runtime/Code - OSS.exe','bundles/cmake/bin/cmake.exe','app/extensions/unit-test-ide/package.json','app/extensions/unit-test-ide/dist/src/extension-entry.cjs','app/extensions/unit-test-ide/dist/src/extension.js','provenance/base-release-manifest.json','provenance/extension-bundle.json')) {
        if($P -cnotin $Paths){throw 'BASIC_APP_REQUIRED_FILE'}
    }
    foreach($Prefix in @('bundles/coverage/','bundles/testgen/','licenses/')) {
        if(@($Paths|Where-Object {$_.StartsWith($Prefix,[StringComparison]::Ordinal)}).Count -eq 0){throw 'BASIC_APP_BUNDLE_MISSING'}
    }
    $Original=Read-BasicBoundJson (Join-Path $Root 'provenance/base-release-manifest.json') $ExpectedResourceManifestSha256
    $Resources=Get-BasicResourceFiles $Original.value
    foreach($R in $Resources) {
        $Found=@($M.files|Where-Object path -CEQ $R.path)
        if($Found.Count -ne 1){throw 'BASIC_RESOURCE_SET'}
        Assert-BasicSameValue $R $Found[0] 'BASIC_RESOURCE_DRIFT'
    }
    $ResourcePaths=@($Resources|ForEach-Object path)
    foreach($F in $M.files) {
        if(($F.path.StartsWith('app/code-oss-runtime/') -or $F.path.StartsWith('bundles/') -or $F.path.StartsWith('licenses/')) -and $F.path -cnotin $ResourcePaths){throw 'BASIC_RESOURCE_SET'}
        if($F.path -cnotin $ResourcePaths -and $F.path -cnotin @('portable-product.json','service/unit-test-service.exe','app/extensions/unit-test-ide/package.json','provenance/base-release-manifest.json','provenance/extension-bundle.json') -and
           -not $F.path.StartsWith('app/extensions/unit-test-ide/dist/src/',[StringComparison]::Ordinal)){throw 'BASIC_APPLICATION_OUTPUT_SET'}
        if($F.path -match '(^|/)(test|fixtures|node_modules)(/|$)' -or $F.path -match '\.test\.(js|d\.ts)$'){throw 'BASIC_APPLICATION_OUTPUT_SET'}
    }
    $P=Read-BasicJson (Join-Path $Root 'portable-product.json')
    Assert-KeySet $P @('schemaVersion','productMode','protocolVersion','buildId','motherCommit','service')
    Assert-KeySet $P.service @('path','size','sha256')
    $Service=Get-FileBinding (Join-Path $Root 'service/unit-test-service.exe')
    if($P.schemaVersion -ne 1 -or $P.productMode -cne 'basic' -or $P.protocolVersion -cne '1.7' -or
       $P.buildId -cne $M.buildId -or $P.motherCommit -cne $M.motherCommit -or
       $P.service.path -cne 'service/unit-test-service.exe' -or $P.service.size -ne $Service.length -or $P.service.sha256 -cne $Service.sha256){throw 'BASIC_SERVICE_PAIR_MISMATCH'}
    $Bundle=Read-BasicJson (Join-Path $Root 'provenance/extension-bundle.json')
    if($Bundle.productMode -cne 'basic' -or $Bundle.protocolVersion -cne '1.7' -or $Bundle.serviceSha256 -cne $Service.sha256 -or $Bundle.unexpectedExternalImports.Count -ne 0){throw 'BASIC_SERVICE_PAIR_MISMATCH'}
    $Extension=Read-BasicJson (Join-Path $Root 'app/extensions/unit-test-ide/package.json')
    if($Extension.main -cne './dist/src/extension-entry.cjs'){throw 'BASIC_EXTENSION_ENTRY'}
    $Paused=@('generateTests','generateTestsForSymbol','generateTestsForFile','generateTestsForTarget','generateTestsForCoverageGap','reviewGeneratedTests','acceptGeneratedTests','cancelTestGeneration','generateManagedTestsForFunction','generateManagedTestsForFile','generateManagedTestsForCoverageGap','listManagedTests','reviewManagedTests','applyManagedReview','rejectManagedReview')
    foreach($Name in $Paused) {
        $ID='unitTestIde.'+$Name
        $Command=@($Extension.contributes.commands|Where-Object command -CEQ $ID)
        $Palette=@($Extension.contributes.menus.commandPalette|Where-Object command -CEQ $ID)
        if($Command.Count -ne 1 -or $Palette.Count -ne 1 -or $Command[0].enablement -cne 'false' -or $Palette[0].when -cne 'false'){throw 'BASIC_GENERATION_COMMAND_VISIBLE'}
        foreach($Menu in $Extension.contributes.menus.Keys) {
            if($Menu -cne 'commandPalette' -and @($Extension.contributes.menus[$Menu]|Where-Object command -CEQ $ID).Count -ne 0){throw 'BASIC_GENERATION_COMMAND_VISIBLE'}
        }
    }
    return [pscustomobject]@{manifest=$M;files=$Files;manifestBinding=[pscustomobject]@{path='app-inputs.json';length=$B.length;sha256=$B.sha256};count=$Files.Count+1}
}
function Read-BasicAppInventory([string]$Root,[string]$InputsFile,[string]$ExpectedSha256) {
    return Read-BasicAppInventoryCore $Root $InputsFile $ExpectedSha256 $script:PortableBaseManifestSha256.ToLowerInvariant()
}
function Assert-BasicPortableCore([string]$Root,[string]$ExpectedResourceManifestSha256) {
    $Path=Assert-LocalPath $Root 'portable-manifest.json' $false
    $P=Read-BasicJson $Path
    Assert-KeySet $P @('schemaVersion','product','version','platform','architecture','motherCommit','motherTree','sourceState','sourceInputsSha256','appInputsSha256','buildId','productMode','protocolVersion','files','self','generationStatus','signingStatus')
    Assert-KeySet $P.self @('path','hashExcluded')
    Assert-BasicSourceIdentity $P
    if($P.schemaVersion -ne 2 -or $P.product -cne 'unit-test-ide' -or $P.version -cne '0.1.0' -or $P.platform -cne 'windows' -or $P.architecture -cne 'x64' -or
       $P.sourceInputsSha256 -cnotmatch '^[0-9a-f]{64}$' -or $P.appInputsSha256 -cnotmatch '^[0-9a-f]{64}$' -or
       $P.generationStatus -cne 'PAUSED_RUNTIME_DISABLED' -or $P.signingStatus -cne 'UNSIGNED_TEST_ONLY' -or
       $P.self.path -cne 'portable-manifest.json' -or $P.self.hashExcluded -isnot [bool] -or -not $P.self.hashExcluded){throw 'BASIC_PORTABLE_SHAPE'}
    $Expected=Convert-BasicFiles $P.files @('portable-manifest.json')
    [void](Assert-Tree $Root $Expected @('portable-manifest.json'))
    # Verify application in a view containing only its bound file set, not by old count.
    $AppPath=Join-Path $Root 'app-inputs.json'
    $AppRead=Read-BasicBoundJson $AppPath $P.appInputsSha256
    $App=$AppRead.value
    $AB=[pscustomobject]@{length=$AppRead.binding.size;sha256=$AppRead.binding.sha256}
    if($AB.sha256 -cne $P.appInputsSha256 -or $App.fixedInputsSha256 -cne $P.sourceInputsSha256 -or $App.buildId -cne $P.buildId){throw 'BASIC_APP_INPUT_DRIFT'}
    $ApplicationFiles=Convert-BasicFiles $App.files @('app-inputs.json','portable-manifest.json','UnitTestIDE.exe','launcher-build.json','README.zh-CN.txt')
    $All=@($ApplicationFiles)+@([pscustomobject]@{path='app-inputs.json';length=$AB.length;sha256=$AB.sha256})
    foreach($Added in @('UnitTestIDE.exe','README.zh-CN.txt','launcher-build.json')) {
        $A=@($Expected|Where-Object path -CEQ $Added)
        if($A.Count -ne 1){throw 'BASIC_PORTABLE_ADDED_MISSING'}
        $All+=@($A[0])
    }
    if($All.Count -ne $Expected.Count){throw 'BASIC_PORTABLE_FILE_SET'}
    $Map=@{};foreach($F in $All){$Map[$F.path]=$F}
    foreach($F in $Expected){if(-not $Map.ContainsKey($F.path) -or $Map[$F.path].length -ne $F.length -or $Map[$F.path].sha256 -cne $F.sha256){throw 'BASIC_PORTABLE_FILE_SET'}}
    # App validation supports explicit portable additions without copying or mutating the root.
    $Inventory=Read-BasicAppInventoryCore $Root $AppPath $P.appInputsSha256 $ExpectedResourceManifestSha256 -PortableView
    $PB=Get-FileBinding $Path
    return [pscustomobject]@{count=$Expected.Count+1;files=(@($Expected)+@([pscustomobject]@{path='portable-manifest.json';length=$PB.length;sha256=$PB.sha256}));manifestSha256=$PB.sha256;buildId=$P.buildId;appInputsSha256=$AB.sha256}
}
function Assert-PortableTree([string]$Root) {
    return Assert-BasicPortableCore $Root $script:PortableBaseManifestSha256.ToLowerInvariant()
}
