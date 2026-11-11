. (Join-Path $PSScriptRoot '../portable/manifest.ps1')

# Fixed batch contracts. Loading this file performs no input transfer or tool execution.
function Get-WILimits {
    return [ordered]@{partBytes=33554432L;archiveBytes=134217728L;manifestBytes=1048576L;fileCount=309;payloadBytes=126143347L}
}
function Assert-WIKeys($Value,[string[]]$Keys) {
    try {Assert-KeySet $Value $Keys} catch {throw 'WI_KEYS'}
}
function Assert-WIBinding($Value) {
    try {Assert-SizeAndDigest $Value.size $Value.sha256} catch {throw 'WI_BINDING'}
}
function Read-WIJson([string]$Path,[string]$Sha256='',[long]$Size=-1) {
    $raw=Read-ProtectedBytes $Path 1048576
    if(($Sha256 -ne '' -and $raw.sha256 -cne $Sha256) -or ($Size -ge 0 -and $raw.length -ne $Size)){throw 'WI_JSON_BINDING'}
    return [ordered]@{value=(ConvertFrom-BasicJsonBytes $raw.bytes);binding=[ordered]@{path=[IO.Path]::GetFullPath($Path);size=$raw.length;sha256=$raw.sha256}}
}
function Assert-WISourceLock($Lock,[bool]$RequireApproved) {
    Assert-WIKeys $Lock @('schemaVersion','kind','repository','approvalState','coordinate')
    if(($Lock.schemaVersion -isnot [int] -and $Lock.schemaVersion -isnot [long]) -or $Lock.schemaVersion -ne 1 -or
       $Lock.kind -isnot [string] -or $Lock.repository -isnot [string] -or $Lock.approvalState -isnot [string] -or
       $Lock.kind -cne 'LOCAL_VERIFIED_WINDOWS_TOOL_INPUTS_V1' -or $Lock.repository -cne 'colayc/unitTest-inputs'){throw 'WI_LOCK_IDENTITY'}
    if($Lock.approvalState -ceq 'UNAPPROVED') {
        if($null -ne $Lock.coordinate){throw 'WI_LOCK_STATE'}
        if($RequireApproved){throw 'WI_SOURCE_UNAPPROVED'}
        return
    }
    if($Lock.approvalState -cne 'APPROVED'){throw 'WI_LOCK_STATE'}
    Assert-WIKeys $Lock.coordinate @('commit','manifest')
    if($Lock.coordinate.commit -isnot [string] -or $Lock.coordinate.commit -cnotmatch '^[0-9a-f]{40}$'){throw 'WI_COMMIT'}
    $m=$Lock.coordinate.manifest
    Assert-WIKeys $m @('path','size','sha256');Assert-WIBinding $m
    if($m.path -isnot [string] -or $m.path -cne 'snapshots/windows-inputs-v1/manifest.json' -or $m.size -lt 1 -or $m.size -gt 1048576){throw 'WI_MANIFEST_BOUND'}
}
function Assert-WIPartDescriptor($Part,[int]$ExpectedIndex) {
    Assert-WIKeys $Part @('index','path','size','sha256')
    if(($Part.index -isnot [int] -and $Part.index -isnot [long]) -or $Part.index -ne $ExpectedIndex -or
       $ExpectedIndex -lt 1 -or $ExpectedIndex -gt 4 -or $Part.path -isnot [string] -or $Part.path -cne ('windows-inputs.zip.part-'+$ExpectedIndex.ToString('D3'))){throw 'WI_PART_SEQUENCE'}
    Assert-WIBinding $Part
    if($Part.size -lt 1 -or $Part.size -gt 33554432){throw 'WI_PART_BOUND'}
}
function Assert-WIFileRecords([object[]]$Files) {
    $seen=[Collections.Generic.HashSet[string]]::new([StringComparer]::OrdinalIgnoreCase)
    $previous=$null
    foreach($file in $Files) {
        Assert-WIKeys $file @('path','size','sha256')
        if($file.path -isnot [string]){throw 'WI_FILE_PATH'}
        try {Assert-PortableRelativePath $file.path} catch {throw 'WI_FILE_PATH'}
        if(-not $seen.Add($file.path)){throw 'WI_CASE_COLLISION'}
        Assert-WIBinding $file
        if($file.size -gt 134217728){throw 'WI_FILE_BOUND'}
        if($null -ne $previous -and [StringComparer]::Ordinal.Compare($previous,$file.path) -ge 0){throw 'WI_FILE_ORDER'}
        $previous=$file.path
    }
}
function Get-WILockedFiles([string]$SourceRoot) {
    $root=Assert-LocalPath $SourceRoot '.' $true
    $testgen=Read-WIJson (Assert-LocalPath $root 'tools/testgen-bundle/manifest.json' $false) 'e2c58c06cef1b86eda4e2c2dbdbcc2338eca9419f44eddc3b227fb42d72bf345' 120260
    $framework=Read-WIJson (Assert-LocalPath $root 'tools/framework-bundle/manifest.json' $false) '6ac0e8fd1393c0d84d882445d7ab49ca2cec6b87ee9f1e4b44cfd828125a9441' 2907
    $selected=@($testgen.value.platforms['windows-x64'].files)+@($testgen.value.platforms['windows-x64'].licenses)
    if($selected.Count -ne 304){throw 'WI_TESTGEN_LOCK'}
    $map=[Collections.Generic.Dictionary[string,object]]::new([StringComparer]::Ordinal)
    [long]$testgenBytes=0
    foreach($file in $selected) {
        $record=[ordered]@{path=('testgen/'+$file.path);size=$file.size;sha256=$file.sha256}
        Assert-WIBinding $record
        $map.Add($record.path,$record);$testgenBytes+=$record.size
    }
    if($testgenBytes -ne 124055821){throw 'WI_TESTGEN_LOCK'}
    $approved=[ordered]@{
        cpputest=@('4.0','cpputest-4.0.tar.gz',1163749L,'21c692105db15299b5529af81a11a7ad80397f92c122bd7bf1e4a4b0e85654f7')
        unity=@('2.6.1','Unity-2.6.1.tar.gz',316918L,'b41a66d45a6b99758fb3202ace6178177014d52fc524bf1f72687d93e9867292')
        cmock=@('2.7.0','CMock-2.7.0.tar.gz',486470L,'d96282cf0286682f7628afc31cf2e3ed6ecb66944d63e098824d98196904f04c')
    }
    if($framework.value.frameworks.Count -ne 3){throw 'WI_FRAMEWORK_LOCK'}
    $ids=[Collections.Generic.HashSet[string]]::new([StringComparer]::Ordinal)
    foreach($entry in $framework.value.frameworks) {
        if(-not $approved.Contains($entry.id) -or -not $ids.Add($entry.id)){throw 'WI_FRAMEWORK_LOCK'}
        $expected=$approved[$entry.id]
        if($entry.version -cne $expected[0] -or $entry.source.filename -cne $expected[1] -or $entry.source.sha256 -cne $expected[3]){throw 'WI_FRAMEWORK_LOCK'}
        $name='framework/'+$expected[3]+'-'+$expected[1]
        $map.Add($name,([ordered]@{path=$name;size=$expected[2];sha256=$expected[3]}))
    }
    $files=@(foreach($path in (Get-OrdinalStrings ([string[]]@($map.Keys)))){$map[$path]})
    Assert-WIFileRecords $files
    return [ordered]@{lockedFiles=$files;metadata=[ordered]@{
        manifest=[ordered]@{path='testgen/manifest.json';size=120260L;sha256=$testgen.binding.sha256}
        ready=[ordered]@{path='testgen/READY';size=129L;manifestSha256=$testgen.binding.sha256}
    }}
}
function Assert-WIManifest($Manifest,[string]$SourceRoot) {
    Assert-WIKeys $Manifest @('schemaVersion','kind','platform','payloadFileCount','payloadBytes','testgenManifestSha256','frameworkManifestSha256','parts','archive','files')
    if(($Manifest.schemaVersion -isnot [int] -and $Manifest.schemaVersion -isnot [long]) -or $Manifest.schemaVersion -ne 1 -or
       $Manifest.kind -isnot [string] -or $Manifest.platform -isnot [string] -or
       $Manifest.kind -cne 'LOCAL_VERIFIED_WINDOWS_TOOL_INPUTS_V1' -or $Manifest.platform -cne 'windows-x64' -or
       ($Manifest.payloadFileCount -isnot [int] -and $Manifest.payloadFileCount -isnot [long]) -or $Manifest.payloadFileCount -ne 309 -or
       ($Manifest.payloadBytes -isnot [int] -and $Manifest.payloadBytes -isnot [long]) -or $Manifest.payloadBytes -ne 126143347){throw 'WI_PAYLOAD_BINDING'}
    if($Manifest.testgenManifestSha256 -isnot [string] -or $Manifest.frameworkManifestSha256 -isnot [string] -or
       $Manifest.testgenManifestSha256 -cne 'e2c58c06cef1b86eda4e2c2dbdbcc2338eca9419f44eddc3b227fb42d72bf345' -or
       $Manifest.frameworkManifestSha256 -cne '6ac0e8fd1393c0d84d882445d7ab49ca2cec6b87ee9f1e4b44cfd828125a9441'){throw 'WI_INPUT_LOCK'}
    Assert-WIArchiveDescriptor $Manifest
    $files=@($Manifest.files)
    if($Manifest.files -isnot [array] -or $files.Count -ne 309){throw 'WI_FILE_SET'}
    Assert-WIFileRecords $files
    $locked=Get-WILockedFiles $SourceRoot
    $expected=[Collections.Generic.Dictionary[string,object]]::new([StringComparer]::Ordinal)
    foreach($file in $locked.lockedFiles){$expected.Add($file.path,$file)}
    $expected.Add($locked.metadata.manifest.path,$locked.metadata.manifest)
    [long]$total=0;$ready=0
    foreach($file in $files) {
        $total+=$file.size
        if($file.path -ceq 'testgen/READY') {
            if($file.size -ne 129){throw 'WI_READY_BINDING'}
            $ready++;continue
        }
        if(-not $expected.ContainsKey($file.path) -or $expected[$file.path].size -ne $file.size -or
           $expected[$file.path].sha256 -cne $file.sha256){throw 'WI_FILE_LOCK'}
    }
    if($ready -ne 1 -or $total -ne 126143347){throw 'WI_FILE_SET'}
}
function Assert-WIArchiveDescriptor($Manifest) {
    Assert-WIKeys $Manifest.archive @('path','size','sha256')
    Assert-WIBinding $Manifest.archive
    if($Manifest.archive.path -isnot [string] -or $Manifest.archive.path -cne 'windows-inputs.zip' -or $Manifest.archive.size -lt 1 -or $Manifest.archive.size -gt 134217728){throw 'WI_ARCHIVE_BOUND'}
    if($Manifest.parts -isnot [array] -or $Manifest.parts.Count -lt 1 -or $Manifest.parts.Count -gt 4){throw 'WI_PART_COUNT'}
    [long]$sum=0;$index=1
    foreach($part in $Manifest.parts){Assert-WIPartDescriptor $part $index;$index++;$sum+=$part.size}
    if($sum -ne $Manifest.archive.size){throw 'WI_ARCHIVE_BOUND'}
}
function Assert-WIAccessContext($Observed) {
    Assert-WIKeys $Observed @('repository','event','isFork','isHostedWindows','candidateSha','workflowSha','approvedCandidateSha','approvedWorkflowSha')
    if($Observed.repository -isnot [string] -or $Observed.event -isnot [string] -or
       $Observed.repository -cne 'colayc/unitTest' -or $Observed.event -cnotin @('pull_request','workflow_dispatch') -or
       $Observed.isFork -isnot [bool] -or $Observed.isFork -or $Observed.isHostedWindows -isnot [bool] -or -not $Observed.isHostedWindows){throw 'WI_ACCESS'}
    foreach($name in @('candidateSha','workflowSha','approvedCandidateSha','approvedWorkflowSha')){
        if($Observed[$name] -isnot [string] -or $Observed[$name] -cnotmatch '^[0-9a-f]{40}$'){throw 'WI_APPROVAL_SHA'}
    }
    if($Observed.candidateSha -cne $Observed.approvedCandidateSha -or $Observed.workflowSha -cne $Observed.approvedWorkflowSha){throw 'WI_APPROVAL_SHA'}
}
function New-WINewDirectory([string]$Path) {
    $full=Assert-AbsoluteLocalPath $Path
    if(Test-Path -LiteralPath $full){throw 'WI_OUTPUT_EXISTS'}
    [void](New-SafeDirectory $full)
    if(@([IO.Directory]::EnumerateFileSystemEntries($full)).Count -ne 0){throw 'WI_OUTPUT_EXISTS'}
    return $full
}
function Assert-WINoOverlap([string]$Left,[string]$Right) {
    $a=(Assert-AbsoluteLocalPath $Left).TrimEnd([char[]]'\/')
    $b=(Assert-AbsoluteLocalPath $Right).TrimEnd([char[]]'\/')
    if([string]::Equals($a,$b,[StringComparison]::OrdinalIgnoreCase) -or
       $a.StartsWith($b+[IO.Path]::DirectorySeparatorChar,[StringComparison]::OrdinalIgnoreCase) -or
       $b.StartsWith($a+[IO.Path]::DirectorySeparatorChar,[StringComparison]::OrdinalIgnoreCase)){throw 'WI_OUTPUT_OVERLAP'}
}
function Assert-WIBoundFile([string]$Path,$Expected,[string]$Code) {
    $actual=Get-BasicBinding $Path
    if($actual.size -ne $Expected.size -or $actual.sha256 -cne $Expected.sha256){throw $Code}
}
function Assert-WIReady([string]$Root) {
    $manifest=Read-WIJson (Assert-LocalPath $Root 'manifest.json' $false) 'e2c58c06cef1b86eda4e2c2dbdbcc2338eca9419f44eddc3b227fb42d72bf345' 120260
    $ready=Read-WIJson (Assert-LocalPath $Root 'READY' $false)
    Assert-WIKeys $ready.value @('schemaVersion','platform','manifestSha256')
    if($ready.binding.size -ne 129 -or ($ready.value.schemaVersion -isnot [int] -and $ready.value.schemaVersion -isnot [long]) -or
       $ready.value.schemaVersion -ne 1 -or $ready.value.platform -isnot [string] -or $ready.value.manifestSha256 -isnot [string] -or $ready.value.platform -cne 'windows-x64' -or
       $ready.value.manifestSha256 -cne $manifest.binding.sha256){throw 'WI_READY_BINDING'}
}
function Assert-WIHttpStatus([int]$Status) {
    if($Status -ne 200){
        $statusError=[InvalidOperationException]::new('WI_HTTP_STATUS')
        $statusError.Data['httpStatus']=$Status
        throw $statusError
    }
}
function Assert-WICredentialPresent([string]$Token) {if([string]::IsNullOrWhiteSpace($Token)){throw 'WI_READ_TOKEN'}}

