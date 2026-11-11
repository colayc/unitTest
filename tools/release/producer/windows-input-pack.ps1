. (Join-Path $PSScriptRoot 'windows-input-contract.ps1')

function New-WindowsInputSnapshot([string]$SourceRoot,[string]$ApprovedCacheRoot,[string]$OutputRoot) {
    $source=Assert-LocalPath $SourceRoot '.' $true
    $cache=Assert-LocalPath $ApprovedCacheRoot '.' $true
    Assert-WINoOverlap $source $OutputRoot
    Assert-WINoOverlap $cache $OutputRoot
    $out=New-WINewDirectory $OutputRoot
    $handles=[Collections.Generic.List[object]]::new()
    $primary=$null;$releaseFailure=$false;$result=$null;$phase='SOURCE'
    try {
        $locked=Get-WILockedFiles $source
        $testgen=Assert-LocalPath $cache '.superpowers/cache/testgen-bundle/22.1.8/windows-x64' $true
        $framework=Assert-LocalPath $cache '.superpowers/cache/framework-bundle' $true
        Assert-WIReady $testgen
        $files=@($locked.lockedFiles)+@($locked.metadata.manifest)
        $readyBinding=Get-BasicBinding (Assert-LocalPath $testgen 'READY' $false)
        $files+=@([ordered]@{path='testgen/READY';size=$readyBinding.size;sha256=$readyBinding.sha256})
        $map=@{};foreach($file in $files){$map.Add($file.path,$file)}
        $files=@(foreach($path in (Get-OrdinalStrings ([string[]]@($map.Keys)))){$map[$path]})
        Assert-WIFileRecords $files
        $tree=@(foreach($file in $files | Where-Object {$_.path.StartsWith('testgen/',[StringComparison]::Ordinal)}){
            [ordered]@{path=$file.path.Substring(8);length=$file.size;sha256=$file.sha256}
        })
        [void](Assert-Tree $testgen $tree)
        foreach($file in $files) {
            $path=if($file.path.StartsWith('testgen/',[StringComparison]::Ordinal)){
                Assert-LocalPath $testgen $file.path.Substring(8) $false
            } else {Assert-LocalPath $framework $file.path.Substring(10) $false}
            $stream=[IO.FileStream]::new($path,[IO.FileMode]::Open,[IO.FileAccess]::Read,[IO.FileShare]::Read)
            $owned=[ordered]@{path=$path;file=$file;stream=$stream}
            $handles.Add($owned)
            $hasher=[Security.Cryptography.SHA256]::Create()
            try {$sha=[Convert]::ToHexString($hasher.ComputeHash($stream)).ToLowerInvariant()}
            finally {$hasher.Dispose()}
            if($stream.Length -ne $file.size -or $sha -cne $file.sha256){throw 'WI_SOURCE_DRIFT'}
            $stream.Position=0
        }
        $phase='ZIP'
        $archivePath=Join-Path $out 'windows-inputs.zip'
        $archiveStream=[IO.FileStream]::new($archivePath,[IO.FileMode]::CreateNew,[IO.FileAccess]::Write,[IO.FileShare]::None)
        try {
            $zip=[IO.Compression.ZipArchive]::new($archiveStream,[IO.Compression.ZipArchiveMode]::Create,$true)
            try {
                $buffer=[byte[]]::new(65536)
                foreach($owned in $handles) {
                    [void](Assert-LocalPath ([IO.Path]::GetDirectoryName($owned.path)) ([IO.Path]::GetFileName($owned.path)) $false)
                    $entry=$zip.CreateEntry($owned.file.path,[IO.Compression.CompressionLevel]::Optimal)
                    $target=$entry.Open()
                    try {
                        [long]$copied=0
                        while(($count=$owned.stream.Read($buffer,0,$buffer.Length)) -gt 0) {
                            $copied+=$count
                            if($copied -gt $owned.file.size){throw 'WI_SOURCE_SIZE'}
                            $target.Write($buffer,0,$count)
                        }
                        if($copied -ne $owned.file.size){throw 'WI_SOURCE_SIZE'}
                    } finally {$target.Dispose()}
                }
            } finally {$zip.Dispose()}
        } finally {$archiveStream.Dispose()}
        foreach($owned in $handles){Assert-WIBoundFile $owned.path $owned.file 'WI_SOURCE_DRIFT'}
        [void](Assert-Tree $testgen $tree)
        Assert-WIReady $testgen
        [void](Get-WILockedFiles $source)
        $archiveBinding=Get-BasicBinding $archivePath
        if($archiveBinding.size -lt 1 -or $archiveBinding.size -gt 134217728){throw 'WI_ARCHIVE_BOUND'}
        $phase='SPLIT'
        $upload=New-WINewDirectory (Join-Path $out 'snapshots/windows-inputs-v1')
        $parts=[Collections.Generic.List[object]]::new()
        $inputStream=[IO.FileStream]::new($archivePath,[IO.FileMode]::Open,[IO.FileAccess]::Read,[IO.FileShare]::Read)
        try {
            $buffer=[byte[]]::new(65536);$index=1
            while($inputStream.Position -lt $inputStream.Length) {
                if($index -gt 4){throw 'WI_PART_COUNT'}
                $name='windows-inputs.zip.part-'+$index.ToString('D3')
                $partPath=Join-Path $upload $name
                $output=[IO.FileStream]::new($partPath,[IO.FileMode]::CreateNew,[IO.FileAccess]::Write,[IO.FileShare]::None)
                try {
                    [long]$written=0
                    while($written -lt 33554432 -and $inputStream.Position -lt $inputStream.Length) {
                        $maximum=[int][Math]::Min($buffer.Length,33554432-$written)
                        $count=$inputStream.Read($buffer,0,$maximum)
                        if($count -le 0){throw 'WI_ARCHIVE_TRUNCATED'}
                        $output.Write($buffer,0,$count);$written+=$count
                    }
                    $output.Flush($true)
                } finally {$output.Dispose()}
                $bound=Get-BasicBinding $partPath
                $parts.Add([ordered]@{index=$index;path=$name;size=$bound.size;sha256=$bound.sha256})
                $index++
            }
        } finally {$inputStream.Dispose()}
        $manifest=[ordered]@{
            schemaVersion=1;kind='LOCAL_VERIFIED_WINDOWS_TOOL_INPUTS_V1';platform='windows-x64'
            payloadFileCount=309;payloadBytes=126143347L
            testgenManifestSha256='e2c58c06cef1b86eda4e2c2dbdbcc2338eca9419f44eddc3b227fb42d72bf345'
            frameworkManifestSha256='6ac0e8fd1393c0d84d882445d7ab49ca2cec6b87ee9f1e4b44cfd828125a9441'
            parts=$parts.ToArray()
            archive=[ordered]@{path='windows-inputs.zip';size=$archiveBinding.size;sha256=$archiveBinding.sha256}
            files=$files
        }
        Assert-WIManifest $manifest $source
        $manifestPath=Join-Path $upload 'manifest.json'
        $bytes=[Text.UTF8Encoding]::new($false).GetBytes(($manifest|ConvertTo-Json -Depth 40 -Compress)+[Environment]::NewLine)
        if($bytes.LongLength -gt 1048576){throw 'WI_MANIFEST_BOUND'}
        Write-NewUtf8File $manifestPath ([Text.UTF8Encoding]::new($false).GetString($bytes))
        $bound=Get-BasicBinding $manifestPath
        $result=[ordered]@{state='SNAPSHOT_NOT_UPLOADED';uploadRoot=$upload;
            manifestBinding=[ordered]@{path='snapshots/windows-inputs-v1/manifest.json';size=$bound.size;sha256=$bound.sha256};
            parts=$parts.ToArray();payloadFiles=$files}
    } catch {
        $primary=$_
        try {
            $base=$primary.Exception.GetBaseException()
            $reason=if($primary.Exception.Message -cmatch '^WI_[A-Z0-9_]+$'){$primary.Exception.Message}else{'WI_SNAPSHOT_FAILED'}
            Write-NewJson (Join-Path $out 'snapshot-failure.json') ([ordered]@{
                state='STOPPED';phase=$phase;failure=$reason;exceptionType=$base.GetType().FullName;
                hresult=$base.HResult.ToString('X8');automaticRetries=0
            })
        } catch { }
    } finally {
        foreach($owned in $handles) {
            try {$owned.stream.Dispose()} catch {$releaseFailure=$true}
        }
    }
    if($releaseFailure){
        try {Write-NewJson (Join-Path $out 'source-release-failure.json') ([ordered]@{state='STOPPED';failure='WI_SOURCE_RELEASE';primaryFailureRetained=($null -ne $primary)})} catch {}
    }
    if($null -ne $primary){throw $primary}
    if($releaseFailure){throw 'WI_SOURCE_RELEASE'}
    Write-NewJson (Join-Path $out 'snapshot-result.json') $result
    return $result
}

