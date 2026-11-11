Set-StrictMode -Version Latest
. (Join-Path $PSScriptRoot 'manifest.ps1')

function Assert-NoPathOverlap([string]$First,[string]$Second) {
    $A=Assert-AbsoluteLocalPath $First
    $B=Assert-AbsoluteLocalPath $Second
    if([string]::Equals($A,$B,[StringComparison]::OrdinalIgnoreCase) -or
       $A.StartsWith($B+[IO.Path]::DirectorySeparatorChar,[StringComparison]::OrdinalIgnoreCase) -or
       $B.StartsWith($A+[IO.Path]::DirectorySeparatorChar,[StringComparison]::OrdinalIgnoreCase)){throw 'SOURCE_DESTINATION_OVERLAP'}
}
function Copy-BoundFile([string]$Source,[string]$Destination,[long]$Size,[string]$Sha256) {
    [void](Assert-AbsoluteLocalPath $Source)
    [void](Assert-AbsoluteLocalPath $Destination)
    if(Test-Path -LiteralPath $Destination){throw 'OUTPUT_EXISTS'}
    Assert-ObservedAttributes ([IO.File]::GetAttributes($Source))
    $Input=$null;$Output=$null
    try {
        $Input=[IO.FileStream]::new($Source,[IO.FileMode]::Open,[IO.FileAccess]::Read,[IO.FileShare]::Read)
        if($Input.Length -ne $Size){throw 'COPY_SOURCE_SIZE_DRIFT'}
        $Hasher=[Security.Cryptography.SHA256]::Create()
        try {$Digest=[Convert]::ToHexString($Hasher.ComputeHash($Input)).ToLowerInvariant()}
        finally {$Hasher.Dispose()}
        if($Digest -cne $Sha256.ToLowerInvariant()){throw 'COPY_SOURCE_DIGEST_DRIFT'}
        $Input.Position=0
        $Output=[IO.FileStream]::new($Destination,[IO.FileMode]::CreateNew,[IO.FileAccess]::Write,[IO.FileShare]::None)
        $Input.CopyTo($Output);$Output.Flush($true)
    } finally {
        if($null -ne $Output){$Output.Dispose()}
        if($null -ne $Input){$Input.Dispose()}
    }
    $Binding=Get-FileBinding $Destination
    if($Binding.length -ne $Size -or $Binding.sha256 -cne $Sha256.ToLowerInvariant()){throw 'COPY_DESTINATION_DRIFT'}
    return $Binding
}
function New-PayloadZip([string]$Root,[string]$Zip,[object[]]$Files) {
    [void](Assert-AbsoluteLocalPath $Zip)
    if(Test-Path -LiteralPath $Zip){throw 'OUTPUT_EXISTS'}
    $Map=[Collections.Generic.Dictionary[string,object]]::new([StringComparer]::Ordinal)
    $Folded=[Collections.Generic.HashSet[string]]::new([StringComparer]::OrdinalIgnoreCase)
    foreach($F in $Files){
        Assert-PortableRelativePath $F.path
        if(-not $Folded.Add($F.path)){throw 'ZIP_CASE_COLLISION'}
        $Map.Add($F.path,$F)
    }
    $Stream=[IO.FileStream]::new($Zip,[IO.FileMode]::CreateNew,[IO.FileAccess]::ReadWrite,[IO.FileShare]::None)
    try {
        $Archive=[IO.Compression.ZipArchive]::new($Stream,[IO.Compression.ZipArchiveMode]::Create,$true)
        try {
            foreach($Relative in (Get-OrdinalStrings ([string[]]@($Map.Keys)))){
                $Source=Assert-LocalPath $Root $Relative $false
                $Entry=$Archive.CreateEntry($Relative,[IO.Compression.CompressionLevel]::Optimal)
                $ES=$null;$SS=$null
                try {
                    $ES=$Entry.Open()
                    $SS=[IO.FileStream]::new($Source,[IO.FileMode]::Open,[IO.FileAccess]::Read,[IO.FileShare]::Read)
                    $SS.CopyTo($ES)
                } finally {
                    if($null -ne $SS){$SS.Dispose()}
                    if($null -ne $ES){$ES.Dispose()}
                }
            }
        } finally {$Archive.Dispose()}
        $Stream.Flush($true)
    } finally {$Stream.Dispose()}
}
function Assert-ZipPayload([string]$Zip,[object[]]$Expected) {
    [void](Assert-AbsoluteLocalPath $Zip)
    Assert-ObservedAttributes ([IO.File]::GetAttributes($Zip))
    $Map=[Collections.Generic.Dictionary[string,object]]::new([StringComparer]::Ordinal)
    $Names=[Collections.Generic.HashSet[string]]::new([StringComparer]::OrdinalIgnoreCase)
    foreach($F in $Expected){
        Assert-PortableRelativePath $F.path
        if(-not $Names.Add($F.path)){throw 'ZIP_CASE_COLLISION'}
        $Map.Add($F.path,$F)
    }
    $Stream=[IO.FileStream]::new($Zip,[IO.FileMode]::Open,[IO.FileAccess]::Read,[IO.FileShare]::Read)
    try {
        $Archive=[IO.Compression.ZipArchive]::new($Stream,[IO.Compression.ZipArchiveMode]::Read,$true)
        try {
            $Seen=[Collections.Generic.HashSet[string]]::new([StringComparer]::OrdinalIgnoreCase)
            foreach($Entry in $Archive.Entries){
                Assert-PortableRelativePath $Entry.FullName
                if(-not $Seen.Add($Entry.FullName)){throw 'ZIP_CASE_COLLISION'}
                $Type=($Entry.ExternalAttributes -shr 16) -band 0xF000
                if($Entry.FullName.EndsWith('/') -or $Type -notin @(0,0x8000) -or
                   ($Entry.ExternalAttributes -band 0x410) -ne 0){throw 'ZIP_SPECIAL_ENTRY'}
                if(-not $Map.ContainsKey($Entry.FullName)){throw 'ZIP_FILE_SET'}
                $Want=$Map[$Entry.FullName]
                if($Entry.Length -ne $Want.length){throw 'ZIP_BYTE_DRIFT'}
                $ES=$Entry.Open()
                $Hasher=[Security.Cryptography.SHA256]::Create()
                try {$Digest=[Convert]::ToHexString($Hasher.ComputeHash($ES)).ToLowerInvariant()}
                finally {$Hasher.Dispose();$ES.Dispose()}
                if($Digest -cne $Want.sha256){throw 'ZIP_BYTE_DRIFT'}
            }
            if($Seen.Count -ne $Expected.Count){throw 'ZIP_FILE_SET'}
        } finally {$Archive.Dispose()}
    } finally {$Stream.Dispose()}
    return [pscustomobject]@{count=$Expected.Count;verified=$true}
}
function Expand-VerifiedZip([string]$Zip,[string]$Destination,[object[]]$Expected) {
    [void](Assert-AbsoluteLocalPath $Destination)
    if(Test-Path -LiteralPath $Destination){throw 'OUTPUT_EXISTS'}
    [void](Assert-ZipPayload $Zip $Expected)
    $Map=[Collections.Generic.Dictionary[string,object]]::new([StringComparer]::Ordinal)
    foreach($F in $Expected){$Map.Add($F.path,$F)}
    [void](New-SafeDirectory $Destination)
    $Stream=[IO.FileStream]::new($Zip,[IO.FileMode]::Open,[IO.FileAccess]::Read,[IO.FileShare]::Read)
    try {
        $Archive=[IO.Compression.ZipArchive]::new($Stream,[IO.Compression.ZipArchiveMode]::Read,$true)
        try {
            $Seen=[Collections.Generic.HashSet[string]]::new([StringComparer]::OrdinalIgnoreCase)
            foreach($Entry in $Archive.Entries){
                Assert-PortableRelativePath $Entry.FullName
                if(-not $Seen.Add($Entry.FullName) -or -not $Map.ContainsKey($Entry.FullName)){throw 'ZIP_FILE_SET'}
                if($Entry.Length -ne $Map[$Entry.FullName].length){throw 'ZIP_BYTE_DRIFT'}
                $Full=[IO.Path]::Combine($Destination,$Entry.FullName.Replace('/',[IO.Path]::DirectorySeparatorChar))
                [void](New-SafeDirectory ([IO.Path]::GetDirectoryName($Full)))
                [void](Assert-AbsoluteLocalPath $Full)
                $ES=$null;$DS=$null
                try {
                    $ES=$Entry.Open()
                    $DS=[IO.FileStream]::new($Full,[IO.FileMode]::CreateNew,[IO.FileAccess]::Write,[IO.FileShare]::None)
                    $ES.CopyTo($DS);$DS.Flush($true)
                } finally {
                    if($null -ne $DS){$DS.Dispose()}
                    if($null -ne $ES){$ES.Dispose()}
                }
                $Got=Get-FileBinding $Full
                if($Got.length -ne $Map[$Entry.FullName].length -or $Got.sha256 -cne $Map[$Entry.FullName].sha256){throw 'ZIP_BYTE_DRIFT'}
            }
            if($Seen.Count -ne $Expected.Count){throw 'ZIP_FILE_SET'}
        } finally {$Archive.Dispose()}
    } finally {$Stream.Dispose()}
    [void](Assert-Tree $Destination $Expected @())
}
function Invoke-BasicLauncherBuild {
    param([string]$SourceRoot,[string]$RecordDirectory,[string]$InputsFile,[string]$ExpectedInputsSha256,[string]$GoExe,
          [string]$ApplicationBuildRecord,[string]$ExpectedApplicationBuildSha256)
    . (Join-Path $PSScriptRoot 'build-basic.ps1')
    . Initialize-BasicBuildLibrary
    $ErrorActionPreference='Stop'
    $M=Assert-BasicSourceInputs $SourceRoot $InputsFile $ExpectedInputsSha256
    if($M.stage -cne 'READY_FOR_COMPILE' -or $GoExe -cne (Get-BasicTool $M 'Go').path -or
       $RecordDirectory -cne (Join-Path $M.executionRoot 'launcher')){throw 'BASIC_LAUNCHER_INPUT'}
    $App=Read-BasicApplicationHandoff $ApplicationBuildRecord $ExpectedApplicationBuildSha256 $M $ExpectedInputsSha256
    Assert-NoPathOverlap $SourceRoot $RecordDirectory
    if(Test-Path -LiteralPath $RecordDirectory){throw 'OUTPUT_EXISTS'}
    [void](New-SafeDirectory $RecordDirectory)
    $Check={
        [void](Assert-BasicSourceInputs $SourceRoot $InputsFile $ExpectedInputsSha256)
        [void](Read-BasicApplicationHandoff $ApplicationBuildRecord $ExpectedApplicationBuildSha256 $M $ExpectedInputsSha256)
    }
    $Invoker={param($Step) Invoke-RecordedNative -Exe $Step.exe -Arguments $Step.arguments -WorkingDirectory $Step.workdir -Environment $Step.environment -RecordDirectory $Step.recordDirectory}
    $Step=Get-BasicStep $M 'B3'
    $Results=Invoke-BasicBuildSteps @($Step) $Check $Invoker
    $Tests=New-BasicNativeEvidence $Step $Results[0]
    $Observed=Read-BasicNativeEvidence $Tests $Step $M
    Assert-BasicGoJson $Observed.output.stdout @('TestPortableLaunchPlan','TestPortableLaunchRun') 'unit-test-ide.local/portable-launcher/internal/launcher'
    $Step=Get-BasicStep $M 'B4'
    $Results=Invoke-BasicBuildSteps @($Step) $Check $Invoker
    $Build=New-BasicNativeEvidence $Step $Results[0]
    $Built=Read-BasicNativeEvidence $Build $Step $M
    $Launcher=Join-Path $RecordDirectory 'UnitTestIDE.exe'
    $LB=Get-BasicBinding $Launcher
    $Version=Read-ProtectedBytes (Join-Path $M.goEnvironment.GOROOT 'VERSION')
    $Record=[ordered]@{
        schemaVersion=2;motherCommit=$M.motherCommit;motherTree=$M.motherTree;sourceState=$M.sourceState;buildId=$M.buildId
        sourceInputsFile=$InputsFile;sourceInputsSha256=$ExpectedInputsSha256;applicationBuild=$App.binding
        sourceBindings=@($M.sourceFiles|Where-Object {$_.path.StartsWith('tools/release/portable/',[StringComparison]::Ordinal)})
        toolBindings=$M.toolBindings;goVersionFromFile=([Text.UTF8Encoding]::new($false,$true).GetString($Version.bytes))
        arguments=$Step.arguments;rawGoExit=$Built.exit.rawExit;wrapperExit=0
        nativeRecords=@($Tests,$Build);launcher=[ordered]@{path='UnitTestIDE.exe';size=$LB.size;sha256=$LB.sha256}
    }
    & $Check | Out-Null
    Assert-BasicFileBindings @($LB)
    $RecordPath=Join-Path $RecordDirectory 'launcher-build.json'
    Write-NewJson $RecordPath $Record
    return [pscustomobject]@{launcher=$Launcher;launcherBuildRecord=$RecordPath;launcherBuildBinding=(Get-BasicBinding $RecordPath);rawExits=@($Tests,$Build)}
}
function New-PortablePackage {
    param([string]$SourceRoot,[string]$OutputRoot,[string]$Launcher,[string]$LauncherBuildRecord,[string]$BuildId)
    . (Join-Path $PSScriptRoot 'build-basic.ps1')
    . Initialize-BasicBuildLibrary
    $ErrorActionPreference='Stop'
    if($BuildId -cnotmatch '^\d{8}T\d{6}Z-[0-9a-f]{8}$'){throw 'BUILD_ID_INVALID'}
    Assert-NoPathOverlap $SourceRoot $OutputRoot
    $Output=Assert-AbsoluteLocalPath $OutputRoot
    $Stage=Join-Path $Output ('UTIDE-0.1.0-basic-11b4dc31-'+$BuildId)
    $Zip=$Stage+'.zip';$Expanded=$Stage+'.expanded-check'
    foreach($Path in @($Stage,$Zip,$Expanded)){if(Test-Path -LiteralPath $Path){throw 'OUTPUT_EXISTS'}}
    $BuildRead=Read-BasicBoundJson $LauncherBuildRecord
    $Build=$BuildRead.value
    Assert-KeySet $Build @('schemaVersion','motherCommit','motherTree','sourceState','buildId','sourceInputsFile','sourceInputsSha256','applicationBuild','sourceBindings','toolBindings','goVersionFromFile','arguments','rawGoExit','wrapperExit','nativeRecords','launcher')
    $FixedRead=Read-BasicBoundJson $Build.sourceInputsFile $Build.sourceInputsSha256
    $M=Assert-BasicSourceInputs $FixedRead.value.sourceRoot $Build.sourceInputsFile $Build.sourceInputsSha256
    if($Build.schemaVersion -ne 2 -or $Build.motherCommit -cne $M.motherCommit -or $Build.motherTree -cne $M.motherTree -or
       $Build.sourceState -cne $M.sourceState -or $Build.buildId -cne $BuildId -or $M.buildId -cne $BuildId -or
       $Build.wrapperExit -ne 0 -or $null -eq $Build.rawGoExit -or $Build.rawGoExit -ne 0 -or
       $Launcher -cne (Join-Path $M.executionRoot 'launcher/UnitTestIDE.exe') -or
       $LauncherBuildRecord -cne (Join-Path $M.executionRoot 'launcher/launcher-build.json') -or
       $Output -cne (Join-Path $M.executionRoot 'delivery')){throw 'LAUNCHER_BUILD_IDENTITY'}
    Assert-KeySet $Build.applicationBuild @('path','size','sha256')
    $App=Read-BasicApplicationHandoff $Build.applicationBuild.path $Build.applicationBuild.sha256 $M $Build.sourceInputsSha256
    if($App.binding.size -ne $Build.applicationBuild.size){throw 'BASIC_APPLICATION_HANDOFF'}
    if($SourceRoot -cne $App.value.applicationRoot){throw 'BASIC_APPLICATION_HANDOFF'}
    $Inventory=$App.inventory;$Application=$Inventory.manifest
    Assert-KeySet $Build.launcher @('path','size','sha256')
    $LB=Get-BasicBinding $Launcher
    if($Build.launcher.path -cne 'UnitTestIDE.exe' -or $Build.launcher.size -ne $LB.size -or $Build.launcher.sha256 -cne $LB.sha256){throw 'LAUNCHER_BUILD_IDENTITY'}
    Assert-BasicSameValue @($M.sourceFiles|Where-Object {$_.path.StartsWith('tools/release/portable/',[StringComparison]::Ordinal)}) $Build.sourceBindings 'LAUNCHER_BUILD_INPUTS'
    Assert-BasicSameValue $M.toolBindings $Build.toolBindings 'LAUNCHER_BUILD_INPUTS'
    Assert-BasicExpectedNames @('B3','B4') @($Build.nativeRecords|ForEach-Object id)
    foreach($R in $Build.nativeRecords) {
        $Step=Get-BasicStep $M $R.id
        $Native=Read-BasicNativeEvidence $R $Step $M
        if($R.id -ceq 'B3'){Assert-BasicGoJson $Native.output.stdout @('TestPortableLaunchPlan','TestPortableLaunchRun') 'unit-test-ide.local/portable-launcher/internal/launcher'}
        else{Assert-BasicSameValue $Step.arguments $Build.arguments 'LAUNCHER_BUILD_INPUTS'}
    }
    [void](New-SafeDirectory $Output)
    [void](New-SafeDirectory $Stage)
    $Copied=[Collections.Generic.List[object]]::new()
    foreach($F in (@($Inventory.files)+@($Inventory.manifestBinding))) {
        $Destination=Join-Path $Stage $F.path
        [void](New-SafeDirectory ([IO.Path]::GetDirectoryName($Destination)))
        [void](Copy-BoundFile (Join-Path $SourceRoot $F.path) $Destination $F.length $F.sha256)
        $Copied.Add([ordered]@{path=$F.path;size=$F.length;sha256=$F.sha256})
    }
    $Readme=Get-BasicBinding (Join-Path $PSScriptRoot 'README.zh-CN.txt')
    $Added=@(
        [pscustomobject]@{path='UnitTestIDE.exe';binding=$LB},
        [pscustomobject]@{path='README.zh-CN.txt';binding=$Readme},
        [pscustomobject]@{path='launcher-build.json';binding=$BuildRead.binding}
    )
    foreach($A in $Added) {
        [void](Copy-BoundFile $A.binding.path (Join-Path $Stage $A.path) $A.binding.size $A.binding.sha256)
        $Copied.Add([ordered]@{path=$A.path;size=$A.binding.size;sha256=$A.binding.sha256})
    }
    $Portable=[ordered]@{
        schemaVersion=2;product='unit-test-ide';version='0.1.0';platform='windows';architecture='x64'
        motherCommit=$M.motherCommit;motherTree=$M.motherTree;sourceState=$M.sourceState
        sourceInputsSha256=$Build.sourceInputsSha256;appInputsSha256=$App.value.appInputs.sha256;buildId=$BuildId
        productMode='basic';protocolVersion='1.7';files=@($Copied.ToArray()|Sort-Object path -CaseSensitive)
        self=[ordered]@{path='portable-manifest.json';hashExcluded=$true}
        generationStatus='PAUSED_RUNTIME_DISABLED';signingStatus='UNSIGNED_TEST_ONLY'
    }
    Write-NewJson (Join-Path $Stage 'portable-manifest.json') $Portable
    $Verified=Assert-PortableTree $Stage
    New-PayloadZip $Stage $Zip $Verified.files
    [void](Assert-ZipPayload $Zip $Verified.files)
    Expand-VerifiedZip $Zip $Expanded $Verified.files
    [void](Assert-PortableTree $Expanded)
    Assert-BasicFileBindings @($BuildRead.binding,$LB,$Readme)
    [void](Assert-BasicSourceInputs $M.sourceRoot $Build.sourceInputsFile $Build.sourceInputsSha256)
    [void](Read-BasicApplicationHandoff $Build.applicationBuild.path $Build.applicationBuild.sha256 $M $Build.sourceInputsSha256)
    $ZB=Get-BasicBinding $Zip
    Write-NewJson ($Zip+'.sha256.json') ([ordered]@{
        filename=[IO.Path]::GetFileName($Zip);size=$ZB.size;sha256=$ZB.sha256;motherCommit=$M.motherCommit
        sourceInputsSha256=$Build.sourceInputsSha256;appInputsSha256=$App.value.appInputs.sha256
        applicationBuildRecordSha256=$App.binding.sha256;launcherBuildRecordSha256=$BuildRead.binding.sha256
        portableManifestSha256=$Verified.manifestSha256;state='STATIC_PACKAGE_BUILT';extensionLoad='NOT_RUN';basicWorkflow='NOT_RUN'
    })
    return [pscustomobject]@{directory=$Stage;zip=$Zip;expandedCheck=$Expanded;zipSha256=$ZB.sha256;zipSize=$ZB.size;manifestSha256=$Verified.manifestSha256;files=$Verified.files;state='STATIC_PACKAGE_BUILT'}
}
