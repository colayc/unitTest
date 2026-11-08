. (Join-Path $PSScriptRoot 'windows-resource-contract.ps1')
. (Join-Path $PSScriptRoot '../portable/package.ps1')

function Assert-WRCurrentBinding($Binding) {
    Assert-WRBinding $Binding
    Assert-WRSame $Binding (Get-BasicBinding $Binding.path) 'WR_LOCAL_BINDING_DRIFT'
}
function Get-WRRelativeBinding([string]$Root,[string]$Relative) {
    $binding=Get-BasicBinding (Assert-LocalPath $Root $Relative $false)
    return [ordered]@{path=$Relative;size=$binding.size;sha256=$binding.sha256}
}
function Test-WRLicensePath([string]$Relative) {
    # Same predicates as stage.mjs:isLicenseRelativePath, without stageRelease.
    $basename=$Relative.Split('/')[-1]
    return ($Relative.Split('/') -ccontains 'licenses' -or
        $basename -match '^(?:licen[cs]e(?:s)?|notice(?:s)?|copying)(?:$|[._-].+)$' -or
        $basename -match '^thirdpartynotices(?:$|[._-].+)$' -or
        $basename -match '(?:^|[._-])(?:licen[cs]e|notice)(?:$|(?:\.[^.]+)+$)')
}
function Get-WRExpectedManifest($Context) {
    return [ordered]@{source=$Context.source;execution=$Context.execution;upstream=$Context.upstream;inputs=$Context.inputs;tools=$Context.tools;policyBinding=$Context.policyBinding}
}
function Assert-WRRuntimeInventory($Inventory,[object[]]$RuntimeFiles) {
    Assert-WRKeys $Inventory @('schemaVersion','platform','architecture','launcherRelativePath','launcherSha256','files','totalBytes','treeDigest')
    if($Inventory.schemaVersion -ne 1 -or $Inventory.platform -cne 'windows' -or $Inventory.architecture -cne 'x64' -or
       $Inventory.launcherRelativePath -cne 'Code - OSS.exe' -or $Inventory.files -isnot [array]){throw 'WR_INVENTORY'}
    $records=@($RuntimeFiles | Where-Object role -CEQ 'runtime')
    if($records.Count -ne $Inventory.files.Count){throw 'WR_INVENTORY_FILES'}
    $hash=[Security.Cryptography.IncrementalHash]::CreateHash([Security.Cryptography.HashAlgorithmName]::SHA256)
    $total=0L
    try {
        for($index=0;$index -lt $records.Count;$index++){
            $actual=$records[$index];$file=$Inventory.files[$index]
            Assert-WRKeys $file @('path','size','sha256','executable')
            Assert-PortableRelativePath $file.path;Assert-SizeAndDigest $file.size $file.sha256
            if($file.executable -isnot [bool] -or $actual.path -cne ('app/code-oss-runtime/'+$file.path) -or $actual.size -ne $file.size -or $actual.sha256 -cne $file.sha256){throw 'WR_INVENTORY_FILES'}
            $entry=$file.path+"`0"+$file.size.ToString([Globalization.CultureInfo]::InvariantCulture)+"`0"+$file.sha256+"`0"+$(if($file.executable){'1'}else{'0'})+"`0"
            $hash.AppendData([Text.Encoding]::UTF8.GetBytes($entry));$total+=$file.size
        }
        if([Convert]::ToHexString($hash.GetHashAndReset()).ToLowerInvariant() -cne $Inventory.treeDigest -or $total -ne $Inventory.totalBytes){throw 'WR_INVENTORY_DIGEST'}
    } finally {$hash.Dispose()}
    $launcher=@($Inventory.files | Where-Object path -CEQ 'Code - OSS.exe')
    if($launcher.Count -ne 1 -or $launcher[0].sha256 -cne $Inventory.launcherSha256){throw 'WR_INVENTORY_LAUNCHER'}
}
function New-WindowsResourcePayload($SourceRoot,$ExecutionRoot,$Context,$Roots) {
    Assert-WRSame $Context.sourceRoot $SourceRoot 'WR_SOURCE_ROOT'
    Assert-WRSame $Context.executionRoot $ExecutionRoot 'WR_EXECUTION_ROOT'
    Assert-WRKeys $Roots @('runtime','cmake','coverage','testgen')
    Assert-WRSame $Context.roots $Roots 'WR_ROOTS'
    Assert-WRSame (Get-WRFixedRoots $Context) $Roots 'WR_FIXED_ROOTS'
    Assert-WRInputSnapshot $Context
    $stage=Join-Path $ExecutionRoot 'payload-tree'
    $payloadDirectory=Join-Path $ExecutionRoot 'payload'
    foreach($newPath in @($stage,$payloadDirectory,(Join-Path $ExecutionRoot 'expanded-check'),(Join-Path $ExecutionRoot 'package-result.json'))){if(Test-Path -LiteralPath $newPath){throw 'WR_OUTPUT_EXISTS'}}
    [void](New-SafeDirectory $stage);[void](New-SafeDirectory $payloadDirectory)
    $prefixes=[ordered]@{runtime='app/code-oss-runtime';cmake='bundles/cmake';coverage='bundles/coverage';testgen='bundles/testgen'}
    $namespaces=@{runtime='code-oss';cmake='cmake';coverage='coverage';testgen='testgen'}
    $sourceSets=[ordered]@{}
    $files=[Collections.Generic.Dictionary[string,object]]::new([StringComparer]::Ordinal)
    foreach($role in $prefixes.Keys){
        Assert-NoPathOverlap $Roots[$role] $stage
        $sourceFiles=@(foreach($relative in (Get-TreeFilePaths $Roots[$role])){Get-WRRelativeBinding $Roots[$role] $relative})
        if($sourceFiles.Count -eq 0){throw 'WR_EMPTY_ROOT'}
        $sourceSets[$role]=$sourceFiles
        foreach($binding in $sourceFiles){
            $destinations=@([ordered]@{path=($prefixes[$role]+'/'+$binding.path);role=$role})
            if(Test-WRLicensePath $binding.path){$destinations+=@([ordered]@{path=('licenses/'+$namespaces[$role]+'/'+$binding.path);role='licenses'})}
            foreach($destination in $destinations){
                $record=[ordered]@{path=$destination.path;size=$binding.size;sha256=$binding.sha256;role=$destination.role}
                Assert-PortableRelativePath $record.path
                $target=Join-Path $stage $record.path
                [void](New-SafeDirectory ([IO.Path]::GetDirectoryName($target)))
                [void](Copy-BoundFile (Assert-LocalPath $Roots[$role] $binding.path $false) $target $binding.size $binding.sha256)
                $files.Add($record.path,$record)
            }
        }
    }
    $orderedFiles=@(foreach($relative in (Get-OrdinalStrings ([string[]]@($files.Keys)))){$files[$relative]})
    $total=Assert-WRFiles $orderedFiles
    $inventorySource=Join-Path $ExecutionRoot 'runtime-inventory.json'
    $inventoryRead=Read-BasicBoundJson $inventorySource
    Assert-WRRuntimeInventory $inventoryRead.value $orderedFiles
    $inventoryPath=Join-Path $payloadDirectory 'runtime-inventory.json'
    [void](Copy-BoundFile $inventorySource $inventoryPath $inventoryRead.binding.size $inventoryRead.binding.sha256)
    $manifest=[ordered]@{schemaVersion=1;kind='WINDOWS_RESOURCE_SET';platform='windows';architecture='x64';recipeBaseline=$Context.policy.recipeBaseline;source=$Context.source;execution=$Context.execution;upstream=$Context.upstream;inputs=$Context.inputs;tools=$Context.tools;runtimeInventoryBinding=(ConvertTo-WRTransportBinding (Get-BasicBinding $inventoryPath) 'runtime-inventory.json');files=$orderedFiles;licenses=@($orderedFiles | Where-Object role -CEQ 'licenses' | ForEach-Object path);fileCount=$orderedFiles.Count;totalBytes=$total}
    Assert-WindowsResourceManifest $manifest (Get-WRExpectedManifest $Context)
    $manifestPath=Join-Path $payloadDirectory 'windows-resources-manifest.json'
    Write-NewJson $manifestPath $manifest
    if((Get-Item -LiteralPath $manifestPath).Length -gt $Context.policy.maximumManifestBytes){throw 'WR_MANIFEST_TOO_LARGE'}
    $legacy=@($orderedFiles | ForEach-Object {ConvertTo-WRLegacyFile $_})
    [void](Assert-Tree $stage $legacy @())
    $zip=Join-Path $payloadDirectory 'windows-resources.zip'
    New-PayloadZip $stage $zip $legacy
    if((Get-Item -LiteralPath $zip).Length -gt $Context.policy.maximumArchiveBytes){throw 'WR_ARCHIVE_TOO_LARGE'}
    [void](Assert-ZipPayload $zip $legacy)
    Expand-VerifiedZip $zip (Join-Path $ExecutionRoot 'expanded-check') $legacy
    [void](Assert-Tree (Join-Path $ExecutionRoot 'expanded-check') $legacy @())
    foreach($role in $prefixes.Keys){
        $after=@(foreach($relative in (Get-TreeFilePaths $Roots[$role])){Get-WRRelativeBinding $Roots[$role] $relative})
        Assert-WRSame $sourceSets[$role] $after 'WR_OUTPUT_SOURCE_DRIFT'
    }
    Assert-WRInputSnapshot $Context
    $result=[ordered]@{schemaVersion=1;state='RESOURCE_STATIC_VALIDATED';source=$Context.source;execution=$Context.execution;policyBinding=$Context.policyBinding;archiveBinding=(Get-BasicBinding $zip);manifestBinding=(Get-BasicBinding $manifestPath);runtimeInventoryBinding=(Get-BasicBinding $inventoryPath);fileCount=$orderedFiles.Count;totalBytes=$total;payloadDirectory=$payloadDirectory;failure=$null}
    [void](Read-WindowsResourcePayload $payloadDirectory ([ordered]@{manifest=(Get-WRExpectedManifest $Context);bindings=@($result.archiveBinding,$result.manifestBinding,$result.runtimeInventoryBinding)}))
    $resultPath=Join-Path $ExecutionRoot 'package-result.json'
    Write-NewJson $resultPath $result
    return Get-BasicBinding $resultPath
}
function Read-WindowsResourcePayload($Directory,$Expected) {
    Assert-WRKeys $Expected @('manifest','bindings')
    Assert-WRBindings $Expected.bindings $false
    $names=@('windows-resources.zip','windows-resources-manifest.json','runtime-inventory.json')
    $actual=Get-TreeFilePaths $Directory
    Assert-WRSame (Get-OrdinalStrings $names) $actual 'WR_PAYLOAD_SET'
    $leases=[Collections.Generic.List[IO.FileStream]]::new()
    try {
        for($index=0;$index -lt $names.Count;$index++){
            $path=Assert-LocalPath $Directory $names[$index] $false
            $lease=[IO.FileStream]::new($path,[IO.FileMode]::Open,[IO.FileAccess]::Read,[IO.FileShare]::Read)
            $leases.Add($lease)
            if($lease.Length -gt $(if($index -eq 0){4294967296L}else{67108864})){throw 'WR_PAYLOAD_SIZE'}
            Assert-WRCurrentBinding $Expected.bindings[$index]
            if($Expected.bindings[$index].path -cne $path){throw 'WR_PAYLOAD_BINDING_PATH'}
        }
        $manifest=(Read-BasicBoundJson (Join-Path $Directory 'windows-resources-manifest.json')).value
        Assert-WindowsResourceManifest $manifest $Expected.manifest
        $inventoryRead=Read-BasicBoundJson (Join-Path $Directory 'runtime-inventory.json')
        Assert-WRSame $manifest.runtimeInventoryBinding (ConvertTo-WRTransportBinding $inventoryRead.binding 'runtime-inventory.json') 'WR_INVENTORY_BINDING'
        Assert-WRRuntimeInventory $inventoryRead.value $manifest.files
        $legacy=@($manifest.files | ForEach-Object {ConvertTo-WRLegacyFile $_})
        [void](Assert-ZipPayload (Join-Path $Directory 'windows-resources.zip') $legacy)
        return [ordered]@{manifest=$manifest;files=$manifest.files}
    } finally {foreach($lease in $leases){$lease.Dispose()}}
}
function Read-WRPackageResult($ResultBinding,$Context) {
    if($ResultBinding.path -cne (Join-Path $Context.executionRoot 'package-result.json')){throw 'WR_RESULT_PATH'}
    Assert-WRCurrentBinding $ResultBinding
    $result=(Read-BasicBoundJson $ResultBinding.path $ResultBinding.sha256 $ResultBinding.size).value
    Assert-WRKeys $result @('schemaVersion','state','source','execution','policyBinding','archiveBinding','manifestBinding','runtimeInventoryBinding','fileCount','totalBytes','payloadDirectory','failure')
    if($result.schemaVersion -ne 1 -or $result.state -cne 'RESOURCE_STATIC_VALIDATED' -or $null -ne $result.failure -or
       $result.payloadDirectory -cne (Join-Path $Context.executionRoot 'payload')){throw 'WR_RESULT'}
    Assert-WRSame $Context.source $result.source 'WR_RESULT_SOURCE'
    Assert-WRSame $Context.execution $result.execution 'WR_RESULT_SOURCE'
    Assert-WRSame $Context.policyBinding $result.policyBinding 'WR_RESULT_SOURCE'
    $verified=Read-WindowsResourcePayload $result.payloadDirectory ([ordered]@{manifest=(Get-WRExpectedManifest $Context);bindings=@($result.archiveBinding,$result.manifestBinding,$result.runtimeInventoryBinding)})
    if($verified.files.Count -ne $result.fileCount -or $verified.manifest.totalBytes -ne $result.totalBytes){throw 'WR_RESULT_COUNTS'}
    return $result
}
function New-WindowsResourceOrigin($ResultBinding,$PayloadUpload,$Context) {
    Assert-WRInputSnapshot $Context
    $result=Read-WRPackageResult $ResultBinding $Context
    Assert-WRArtifactCoordinate $PayloadUpload ('windows-portable-resources-'+$Context.execution.runId+'-'+$Context.execution.runAttempt)
    $origin=[ordered]@{schemaVersion=1;kind='WINDOWS_RESOURCE_ORIGIN';source=$Context.source;execution=$Context.execution;policyBinding=$Context.policyBinding;payload=$PayloadUpload;archiveBinding=(ConvertTo-WRTransportBinding $result.archiveBinding 'windows-resources.zip');manifestBinding=(ConvertTo-WRTransportBinding $result.manifestBinding 'windows-resources-manifest.json');runtimeInventoryBinding=(ConvertTo-WRTransportBinding $result.runtimeInventoryBinding 'runtime-inventory.json');generationStatus='PAUSED_RUNTIME_DISABLED';signingStatus='UNSIGNED_TEST_ONLY';state='RESOURCE_STATIC_VALIDATED'}
    $directory=Join-Path $Context.executionRoot 'origin'
    if(Test-Path -LiteralPath $directory){throw 'WR_OUTPUT_EXISTS'}
    [void](New-SafeDirectory $directory)
    $path=Join-Path $directory 'windows-resources-origin.json'
    Write-NewJson $path $origin
    Assert-WRSame $origin (Read-BasicBoundJson $path).value 'WR_ORIGIN_READBACK'
    Assert-WRInputSnapshot $Context
    return Get-BasicBinding $path
}
