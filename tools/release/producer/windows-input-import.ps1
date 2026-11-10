. (Join-Path $PSScriptRoot 'windows-input-contract.ps1')

function Join-WIArchiveCore([string]$SnapshotRoot,$Manifest,[string]$ArchivePath) {
    Assert-WIArchiveDescriptor $Manifest
    $root=Assert-LocalPath $SnapshotRoot '.' $true
    $allowed=[Collections.Generic.HashSet[string]]::new([StringComparer]::Ordinal)
    [void]$allowed.Add('manifest.json')
    foreach($part in $Manifest.parts){[void]$allowed.Add($part.path)}
    $actual=Get-TreeFilePaths $root
    foreach($path in $actual){if(-not $allowed.Contains($path)){throw 'WI_TRANSFER_SET'}}
    foreach($part in $Manifest.parts){if($part.path -cnotin $actual){throw 'WI_TRANSFER_SET'}}
    foreach($part in $Manifest.parts){
        $path=Assert-LocalPath $root $part.path $false
        Assert-WIBoundFile $path $part 'WI_PART_DIGEST'
    }
    [void](Assert-AbsoluteLocalPath $ArchivePath)
    $target=[IO.FileStream]::new($ArchivePath,[IO.FileMode]::CreateNew,[IO.FileAccess]::Write,[IO.FileShare]::None)
    try {
        $buffer=[byte[]]::new(65536)
        [long]$total=0
        foreach($part in $Manifest.parts) {
            $inputStream=[IO.FileStream]::new((Assert-LocalPath $root $part.path $false),[IO.FileMode]::Open,[IO.FileAccess]::Read,[IO.FileShare]::Read)
            try {
                [long]$partBytes=0
                while(($count=$inputStream.Read($buffer,0,$buffer.Length)) -gt 0) {
                    $partBytes+=$count;$total+=$count
                    if($partBytes -gt $part.size -or $total -gt $Manifest.archive.size){throw 'WI_TRANSFER_SIZE'}
                    $target.Write($buffer,0,$count)
                }
                if($partBytes -ne $part.size){throw 'WI_TRANSFER_SIZE'}
            } finally {$inputStream.Dispose()}
        }
        if($total -ne $Manifest.archive.size){throw 'WI_TRANSFER_SIZE'}
        $target.Flush($true)
    } finally {$target.Dispose()}
    Assert-WIBoundFile $ArchivePath $Manifest.archive 'WI_ARCHIVE_DIGEST'
    return (Get-BasicBinding $ArchivePath)
}
function Expand-WIZipCore([string]$ArchivePath,[object[]]$ExpectedFiles,[string]$NewRoot) {
    Assert-WIFileRecords $ExpectedFiles
    if($ExpectedFiles.Count -lt 1 -or $ExpectedFiles.Count -gt 309){throw 'WI_ZIP_SET'}
    [long]$expectedTotal=0
    $expected=[Collections.Generic.Dictionary[string,object]]::new([StringComparer]::Ordinal)
    $directories=[Collections.Generic.HashSet[string]]::new([StringComparer]::Ordinal)
    foreach($file in $ExpectedFiles) {
        $expected.Add($file.path,$file);$expectedTotal+=$file.size
        $parent=[IO.Path]::GetDirectoryName($file.path).Replace('\','/')
        while(-not [string]::IsNullOrEmpty($parent)){
            [void]$directories.Add($parent)
            $parent=[IO.Path]::GetDirectoryName($parent).Replace('\','/')
        }
    }
    if($expectedTotal -gt 126143347){throw 'WI_ZIP_SIZE'}
    $source=Assert-AbsoluteLocalPath $ArchivePath
    Assert-ObservedAttributes ([IO.File]::GetAttributes($source))
    $inputStream=[IO.FileStream]::new($source,[IO.FileMode]::Open,[IO.FileAccess]::Read,[IO.FileShare]::Read)
    try {
        if($inputStream.Length -lt 22 -or $inputStream.Length -gt 134217728){throw 'WI_ARCHIVE_BOUND'}
        # Our bounded seekable ZIP writer needs neither ZIP64, multi-disk nor comments.
        # Bound central-directory allocation before ZipArchive materializes Entries.
        $inputStream.Position=$inputStream.Length-22
        $end=[byte[]]::new(22);$read=0
        while($read -lt 22){
            $count=$inputStream.Read($end,$read,22-$read)
            if($count -le 0){throw 'WI_ZIP_SIZE'}
            $read+=$count
        }
        $entries=[BitConverter]::ToUInt16($end,10)
        [long]$centralSize=[BitConverter]::ToUInt32($end,12)
        [long]$centralOffset=[BitConverter]::ToUInt32($end,16)
        if([BitConverter]::ToUInt32($end,0) -ne 0x06054b50 -or
           [BitConverter]::ToUInt16($end,4) -ne 0 -or [BitConverter]::ToUInt16($end,6) -ne 0 -or
           [BitConverter]::ToUInt16($end,8) -ne $entries -or [BitConverter]::ToUInt16($end,20) -ne 0 -or
           $entries -lt 1 -or $entries -gt $ExpectedFiles.Count+$directories.Count -or
           $centralSize -gt 1048576 -or $centralOffset+$centralSize -ne $inputStream.Length-22){throw 'WI_ZIP_SIZE'}
        $inputStream.Position=0
        $zip=[IO.Compression.ZipArchive]::new($inputStream,[IO.Compression.ZipArchiveMode]::Read,$true)
        try {
            $seen=[Collections.Generic.HashSet[string]]::new([StringComparer]::OrdinalIgnoreCase)
            $members=[Collections.Generic.List[object]]::new()
            foreach($entry in $zip.Entries) {
                $isDirectory=$entry.FullName.EndsWith('/',[StringComparison]::Ordinal)
                $path=if($isDirectory){$entry.FullName.Substring(0,$entry.FullName.Length-1)}else{$entry.FullName}
                try {Assert-PortableRelativePath $path} catch {throw 'WI_ZIP_PATH'}
                if(-not $seen.Add($path)){throw 'WI_ZIP_DUPLICATE'}
                $type=($entry.ExternalAttributes -shr 16) -band 61440
                if(($entry.ExternalAttributes -band 1088) -ne 0 -or
                   (-not $isDirectory -and ($entry.ExternalAttributes -band 16) -ne 0) -or
                   ($type -ne 0 -and $type -ne $(if($isDirectory){16384}else{32768}))){throw 'WI_ZIP_TYPE'}
                if($isDirectory) {
                    if(-not $directories.Contains($path) -or $entry.Length -ne 0){throw 'WI_ZIP_SET'}
                    continue
                }
                if(-not $expected.ContainsKey($path)){throw 'WI_ZIP_SET'}
                if($entry.Length -ne $expected[$path].size){throw 'WI_ZIP_SIZE'}
                $members.Add([ordered]@{entry=$entry;file=$expected[$path]})
            }
            if($members.Count -ne $ExpectedFiles.Count){throw 'WI_ZIP_SET'}
            $root=New-WINewDirectory $NewRoot
            [long]$total=0;$buffer=[byte[]]::new(65536)
            foreach($member in $members) {
                $file=$member.file
                $destination=[IO.Path]::GetFullPath((Join-Path $root $file.path))
                if(-not $destination.StartsWith($root.TrimEnd([char[]]'\/')+[IO.Path]::DirectorySeparatorChar,[StringComparison]::OrdinalIgnoreCase)){throw 'WI_ZIP_PATH'}
                [void](New-SafeDirectory ([IO.Path]::GetDirectoryName($destination)))
                [void](Assert-AbsoluteLocalPath $destination)
                $output=[IO.FileStream]::new($destination,[IO.FileMode]::CreateNew,[IO.FileAccess]::Write,[IO.FileShare]::None)
                try {
                    $entryStream=$member.entry.Open()
                    try {
                        [long]$written=0
                        while(($count=$entryStream.Read($buffer,0,$buffer.Length)) -gt 0) {
                            $written+=$count;$total+=$count
                            if($written -gt $file.size -or $total -gt $expectedTotal){throw 'WI_ZIP_SIZE'}
                            $output.Write($buffer,0,$count)
                        }
                        if($written -ne $file.size){throw 'WI_ZIP_SIZE'}
                    } finally {$entryStream.Dispose()}
                    $output.Flush($true)
                } finally {$output.Dispose()}
                Assert-WIBoundFile $destination $file 'WI_ZIP_DIGEST'
            }
            if($total -ne $expectedTotal){throw 'WI_ZIP_SIZE'}
            $tree=@(foreach($file in $ExpectedFiles){[ordered]@{path=$file.path;length=$file.size;sha256=$file.sha256}})
            [void](Assert-Tree $root $tree)
            return [ordered]@{root=$root;files=$ExpectedFiles}
        } finally {$zip.Dispose()}
    } finally {$inputStream.Dispose()}
}
function Move-WIStageDirectory([string]$From,[string]$To) {[IO.Directory]::Move($From,$To)}
function Publish-WIInputs([string]$SourceRoot,[string]$VerifiedRoot) {
    $source=Assert-LocalPath $SourceRoot '.' $true
    $verified=Assert-LocalPath $VerifiedRoot '.' $true
    Assert-WINoOverlap $source $verified
    $testgen=Assert-LocalPath $verified 'testgen' $true
    $framework=Assert-LocalPath $verified 'framework' $true
    $first=Assert-AbsoluteLocalPath (Join-Path $source '.superpowers/cache/testgen-bundle/22.1.8/windows-x64')
    $second=Assert-AbsoluteLocalPath (Join-Path $source '.superpowers/cache/framework-bundle')
    if((Test-Path -LiteralPath $first) -or (Test-Path -LiteralPath $second)){throw 'WI_CACHE_EXISTS'}
    Write-NewJson (Join-Path $verified 'publication-start.json') ([ordered]@{state='STARTED';testgenRoot=$first;frameworkCacheRoot=$second})
    $owned=$false
    try {
        [void](New-SafeDirectory ([IO.Path]::GetDirectoryName($first)))
        [void](Assert-LocalPath $verified 'testgen' $true)
        Move-WIStageDirectory $testgen $first
        $owned=$true
        [void](Assert-AbsoluteLocalPath $second)
        [void](Assert-LocalPath $verified 'framework' $true)
        Move-WIStageDirectory $framework $second
        $result=[ordered]@{state='INPUT_BYTES_IMPORTED';testgenRoot=$first;frameworkCacheRoot=$second}
    } catch {
        $primary=$_
        try {
            $base=$primary.Exception.GetBaseException()
            $reason=if($primary.Exception.Message -cmatch '^WI_[A-Z0-9_]+$'){$primary.Exception.Message}else{'WI_PUBLISH_FAILED'}
            Write-NewJson (Join-Path $verified 'publication.json') ([ordered]@{
                state=$(if($owned){'PARTIAL_FIRST_OWNED'}else{'NOT_PUBLISHED'});
                testgenRoot=$first;frameworkCacheRoot=$second;automaticRetries=0;failure=$reason;
                exceptionType=$base.GetType().FullName;hresult=$base.HResult.ToString('X8')
            })
        } catch { }
        throw $primary
    }
    Write-NewJson (Join-Path $verified 'publication.json') $result
    return $result
}
function Import-WindowsInputSnapshot([string]$SourceRoot,[string]$TransferRoot) {
    $source=Assert-LocalPath $SourceRoot '.' $true
    $transfer=Assert-LocalPath $TransferRoot '.' $true
    Assert-WINoOverlap $source $transfer
    $lockRead=Read-WIJson (Assert-LocalPath $source 'tools/release/producer/windows-input-source.json' $false)
    $lock=$lockRead.value;Assert-WISourceLock $lock $true
    $snapshot=Assert-LocalPath $transfer 'snapshot' $true
    $manifestRead=Read-WIJson (Assert-LocalPath $snapshot 'manifest.json' $false) $lock.coordinate.manifest.sha256 $lock.coordinate.manifest.size
    $manifest=$manifestRead.value;Assert-WIManifest $manifest $source
    [void](Join-WIArchiveCore $snapshot $manifest (Join-Path $transfer 'windows-inputs.zip'))
    $expanded=Expand-WIZipCore (Join-Path $transfer 'windows-inputs.zip') $manifest.files (Join-Path $transfer 'verified')
    Assert-WIReady (Assert-LocalPath $expanded.root 'testgen' $true)
    Assert-WIBoundFile (Join-Path $source 'tools/release/producer/windows-input-source.json') $lockRead.binding 'WI_LOCK_DRIFT'
    $published=Publish-WIInputs $source $expanded.root
    return [ordered]@{state=$published.state;testgenRoot=$published.testgenRoot;frameworkCacheRoot=$published.frameworkCacheRoot;
        manifestBinding=$manifestRead.binding;sourceLockBinding=$lockRead.binding}
}

function Invoke-WIReadOnlyGit([string]$SourceRoot,[string]$Operation) {
    $arguments=switch -CaseSensitive ($Operation) {
        'Head' {@('--no-optional-locks','rev-parse','HEAD')}
        'Dirty' {@('--no-optional-locks','status','--porcelain=v1','--untracked-files=no')}
        'Tracked' {@('--no-optional-locks','ls-files','--cached','-z')}
        default {throw 'WI_SOURCE_OPERATION'}
    }
    $git=(Get-Command git.exe -CommandType Application -ErrorAction Stop).Source
    $info=[Diagnostics.ProcessStartInfo]::new()
    $info.FileName=$git;$info.WorkingDirectory=$SourceRoot
    $info.UseShellExecute=$false;$info.CreateNoWindow=$true
    $info.RedirectStandardOutput=$true;$info.RedirectStandardError=$true
    $info.Environment.Clear()
    foreach($name in @('SystemRoot','WINDIR','TEMP','TMP','USERPROFILE','HOMEDRIVE','HOMEPATH')){
        $value=[Environment]::GetEnvironmentVariable($name);if($null -ne $value){$info.Environment[$name]=$value}
    }
    $info.Environment['PATH']=[IO.Path]::GetDirectoryName($git)+';'+(Join-Path $env:SystemRoot 'System32')
    $info.Environment['GIT_TERMINAL_PROMPT']='0'
    $info.Environment['GIT_CONFIG_NOSYSTEM']='1';$info.Environment['GIT_CONFIG_GLOBAL']='NUL'
    $info.Environment['GIT_CONFIG_COUNT']='2'
    $info.Environment['GIT_CONFIG_KEY_0']='core.hooksPath';$info.Environment['GIT_CONFIG_VALUE_0']='NUL'
    $info.Environment['GIT_CONFIG_KEY_1']='core.fsmonitor';$info.Environment['GIT_CONFIG_VALUE_1']='false'
    $tracking=[Environment]::GetEnvironmentVariable('RUNNER_TRACKING_ID')
    if(-not [string]::IsNullOrEmpty($tracking)){
        if($tracking.Length -gt 256 -or @($tracking.ToCharArray() | Where-Object {[char]::IsControl($_)}).Count -gt 0){throw 'WI_RUNNER_TRACKING'}
        $info.Environment['RUNNER_TRACKING_ID']=$tracking
    }
    foreach($argument in $arguments){[void]$info.ArgumentList.Add($argument)}
    $process=[Diagnostics.Process]::new();$process.StartInfo=$info
    try {
        if(-not $process.Start()){throw 'WI_SOURCE_QUERY'}
        $stdout=$process.StandardOutput.ReadToEndAsync();$stderr=$process.StandardError.ReadToEndAsync()
        if(-not $process.WaitForExit(30000)){
            try {$process.Kill($true);[void]$process.WaitForExit(5000)} catch {}
            throw 'WI_SOURCE_QUERY_TIMEOUT'
        }
        $exit=$process.ExitCode
        $out=$stdout.GetAwaiter().GetResult();[void]$stderr.GetAwaiter().GetResult()
        if($exit -ne 0){throw 'WI_SOURCE_QUERY'}
        return $out
    } finally {$process.Dispose()}
}
function Get-WIHostedObservation([string]$SourceRoot) {
    if($env:GITHUB_ACTIONS -cne 'true' -or -not $IsWindows){throw 'WI_LIVE_CONTEXT'}
    $source=Assert-LocalPath $SourceRoot '.' $true
    if(-not [string]::Equals($source,[IO.Path]::GetFullPath($env:GITHUB_WORKSPACE),[StringComparison]::OrdinalIgnoreCase)){throw 'WI_LIVE_CONTEXT'}
    $head=(Invoke-WIReadOnlyGit $source 'Head').Trim()
    if(-not [string]::IsNullOrWhiteSpace((Invoke-WIReadOnlyGit $source 'Dirty'))){throw 'WI_SOURCE_DIRTY'}
    $tracked=@((Invoke-WIReadOnlyGit $source 'Tracked').Split([char]0,[StringSplitOptions]::RemoveEmptyEntries))
    . (Join-Path $PSScriptRoot 'windows-resource-build.ps1')
    $paths=Get-WRInputPaths
    if($paths.Count -ne 58){throw 'WI_SOURCE_CLOSURE'}
    $bindings=@(foreach($path in $paths){
        if($path -cnotin $tracked){throw 'WI_SOURCE_UNCOMMITTED'}
        $bound=Get-BasicBinding (Assert-LocalPath $source $path $false)
        [ordered]@{path=$path;size=$bound.size;sha256=$bound.sha256}
    })
    if($head -cne $env:WI_CANDIDATE_SHA){throw 'WI_CANDIDATE_SHA'}
    $observed=[ordered]@{repository=$env:GITHUB_REPOSITORY;event=$env:GITHUB_EVENT_NAME;
        isFork=($env:WI_IS_FORK -ceq 'true');isHostedWindows=($env:WI_HOSTED_WINDOWS -ceq 'true');
        candidateSha=$head;workflowSha=$env:GITHUB_WORKFLOW_SHA;
        approvedCandidateSha=$env:WI_APPROVED_CANDIDATE_SHA;approvedWorkflowSha=$env:WI_APPROVED_WORKFLOW_SHA}
    return [ordered]@{observed=$observed;bindings=$bindings}
}
function Assert-WIHostedSourceProof([string]$SourceRoot,[object[]]$Bindings) {
    if($Bindings.Count -ne 58){throw 'WI_SOURCE_CLOSURE'}
    foreach($binding in $Bindings){
        Assert-WIBoundFile (Assert-LocalPath $SourceRoot $binding.path $false) $binding 'WI_SOURCE_DRIFT'
    }
}
function Assert-WILiveAccess([string]$SourceRoot,$Observed) {
    Assert-WIAccessContext $Observed
    if($env:GITHUB_ACTIONS -cne 'true' -or -not $IsWindows -or $env:RUNNER_OS -cne 'Windows' -or
       $env:WI_HOSTED_WINDOWS -cne 'true' -or $env:WI_IS_FORK -cne 'false' -or
       $Observed.repository -cne $env:GITHUB_REPOSITORY -or $Observed.event -cne $env:GITHUB_EVENT_NAME -or
       $Observed.candidateSha -cne $env:WI_CANDIDATE_SHA -or $Observed.workflowSha -cne $env:GITHUB_WORKFLOW_SHA -or
       $Observed.approvedCandidateSha -cne $env:WI_APPROVED_CANDIDATE_SHA -or
       $Observed.approvedWorkflowSha -cne $env:WI_APPROVED_WORKFLOW_SHA -or
       -not [string]::Equals([IO.Path]::GetFullPath($SourceRoot),[IO.Path]::GetFullPath($env:GITHUB_WORKSPACE),[StringComparison]::OrdinalIgnoreCase)){throw 'WI_LIVE_CONTEXT'}
}
function Receive-WIObject($Client,[string]$Commit,[string]$RelativePath,[string]$Destination,$Binding,$CancellationToken) {
    if($Commit -cnotmatch '^[0-9a-f]{40}$' -or
       $RelativePath -cnotmatch '^snapshots/windows-inputs-v1/(manifest\.json|windows-inputs\.zip\.part-00[1-4])$'){throw 'WI_HTTP_COORDINATE'}
    Assert-WIBinding $Binding
    if($Binding.size -lt 1 -or $Binding.size -gt $(if($RelativePath.EndsWith('/manifest.json')){1048576}else{33554432})){throw 'WI_TRANSFER_SIZE'}
    $uri=[Uri]::new('https://api.github.com/repos/colayc/unitTest-inputs/contents/'+$RelativePath+'?ref='+$Commit)
    $response=$Client.GetAsync($uri,[Net.Http.HttpCompletionOption]::ResponseHeadersRead,$CancellationToken).GetAwaiter().GetResult()
    try {
        Assert-WIHttpStatus ([int]$response.StatusCode)
        if($null -ne $response.Content.Headers.ContentLength -and $response.Content.Headers.ContentLength -ne $Binding.size){throw 'WI_TRANSFER_SIZE'}
        [void](Assert-AbsoluteLocalPath $Destination)
        $output=[IO.FileStream]::new($Destination,[IO.FileMode]::CreateNew,[IO.FileAccess]::Write,[IO.FileShare]::None)
        try {
            $inputStream=$response.Content.ReadAsStreamAsync($CancellationToken).GetAwaiter().GetResult()
            try {
                $buffer=[byte[]]::new(65536);[long]$total=0
                while(($count=$inputStream.ReadAsync($buffer,0,$buffer.Length,$CancellationToken).GetAwaiter().GetResult()) -gt 0){
                    $total+=$count;if($total -gt $Binding.size){throw 'WI_TRANSFER_SIZE'}
                    $output.Write($buffer,0,$count)
                }
                if($total -ne $Binding.size){throw 'WI_TRANSFER_SIZE'}
                $output.Flush($true)
            } finally {$inputStream.Dispose()}
        } finally {$output.Dispose()}
        Assert-WIBoundFile $Destination $Binding 'WI_TRANSFER_DIGEST'
    } finally {$response.Dispose()}
}
function Read-WITransfer([string]$SourceRoot,[string]$DownloadRoot,$Observed,[int]$RemainingSeconds) {
    $client=$null;$handler=$null;$deadline=$null;$token=$null;$root=$null
    try {
        Assert-WILiveAccess $SourceRoot $Observed
        if($RemainingSeconds -lt 1 -or $RemainingSeconds -gt 7200){throw 'WI_BUDGET'}
        $source=Assert-LocalPath $SourceRoot '.' $true
        $lockRead=Read-WIJson (Assert-LocalPath $source 'tools/release/producer/windows-input-source.json' $false)
        $lock=$lockRead.value;Assert-WISourceLock $lock $true
        $token=[Environment]::GetEnvironmentVariable('WI_INPUT_READ_TOKEN')
        Assert-WICredentialPresent $token
        [Environment]::SetEnvironmentVariable('WI_INPUT_READ_TOKEN',$null,'Process')
        if($token.Length -gt 4096 -or @($token.ToCharArray() | Where-Object {[char]::IsControl($_)}).Count -gt 0){throw 'WI_READ_TOKEN'}
        Assert-WINoOverlap $source $DownloadRoot
        $root=New-WINewDirectory $DownloadRoot
        $snapshot=New-WINewDirectory (Join-Path $root 'snapshot')
        $deadline=[Threading.CancellationTokenSource]::new([TimeSpan]::FromSeconds($RemainingSeconds))
        $handler=[Net.Http.HttpClientHandler]::new();$handler.AllowAutoRedirect=$false
        $client=[Net.Http.HttpClient]::new($handler);$client.Timeout=[Threading.Timeout]::InfiniteTimeSpan
        $client.DefaultRequestHeaders.UserAgent.ParseAdd('unitTest-windows-inputs/1')
        $client.DefaultRequestHeaders.Accept.ParseAdd('application/vnd.github.raw+json')
        $client.DefaultRequestHeaders.Authorization=[Net.Http.Headers.AuthenticationHeaderValue]::new('Bearer',$token)
        $manifestPath=Join-Path $snapshot 'manifest.json'
        Receive-WIObject $client $lock.coordinate.commit $lock.coordinate.manifest.path $manifestPath $lock.coordinate.manifest $deadline.Token
        $read=Read-WIJson $manifestPath $lock.coordinate.manifest.sha256 $lock.coordinate.manifest.size
        Assert-WIManifest $read.value $source
        foreach($part in $read.value.parts){
            Receive-WIObject $client $lock.coordinate.commit ('snapshots/windows-inputs-v1/'+$part.path) (Join-Path $snapshot $part.path) $part $deadline.Token
        }
        Assert-WIBoundFile (Join-Path $source 'tools/release/producer/windows-input-source.json') $lockRead.binding 'WI_LOCK_DRIFT'
        return [ordered]@{snapshotRoot=$root;manifestBinding=$read.binding;sourceLockBinding=$lockRead.binding}
    } catch {
        $message=$_.Exception.Message
        $base=$_.Exception.GetBaseException()
        $safe=if($message -cmatch '^WI_[A-Z0-9_]+$'){$message}else{'WI_TRANSFER_FAILED'}
        $httpStatus=$null
        if($base.Data.Contains('httpStatus') -and $base.Data['httpStatus'] -is [int] -and
           $base.Data['httpStatus'] -ge 100 -and $base.Data['httpStatus'] -le 599){$httpStatus=$base.Data['httpStatus']}
        if($null -ne $root){
            try {Write-NewJson (Join-Path $root 'transfer-failure.json') ([ordered]@{
                state='STOPPED';failure=$safe;httpStatus=$httpStatus;exceptionType=$base.GetType().FullName;
                hresult=$base.HResult.ToString('X8');automaticRetries=0
            })} catch {}
        }
        throw $safe
    } finally {
        [Environment]::SetEnvironmentVariable('WI_INPUT_READ_TOKEN',$null,'Process')
        $token=$null
        if($null -ne $client){$client.DefaultRequestHeaders.Authorization=$null;$client.Dispose()}
        elseif($null -ne $handler){$handler.Dispose()}
        if($null -ne $deadline){$deadline.Dispose()}
    }
}
function Invoke-WIHostedImport([string]$SourceRoot,[int]$MaximumSeconds=900) {
    try {
        if($MaximumSeconds -lt 1 -or $MaximumSeconds -gt 7200){throw 'WI_BUDGET'}
        $watch=[Diagnostics.Stopwatch]::StartNew()
        $probe=Get-WIHostedObservation $SourceRoot
        $observed=$probe.observed
        Assert-WIAccessContext $observed
        Assert-WIHostedSourceProof $SourceRoot $probe.bindings
        $remaining=[int][Math]::Floor($MaximumSeconds-$watch.Elapsed.TotalSeconds)
        if($remaining -le 0){throw 'WI_BUDGET'}
        foreach($value in @($env:GITHUB_RUN_ID,$env:GITHUB_RUN_ATTEMPT)){
            if($value -cnotmatch '^[1-9][0-9]*$'){throw 'WI_RUN_ID'}
        }
        if($env:GITHUB_JOB -cnotmatch '^[A-Za-z0-9_-]+$'){throw 'WI_JOB'}
        $download=Join-Path $env:RUNNER_TEMP ('windows-inputs-'+$env:GITHUB_RUN_ID+'-'+$env:GITHUB_RUN_ATTEMPT+'-'+$env:GITHUB_JOB)
        $transfer=Read-WITransfer $SourceRoot $download $observed $remaining
        Assert-WIHostedSourceProof $SourceRoot $probe.bindings
        $result=Import-WindowsInputSnapshot $SourceRoot $transfer.snapshotRoot
        Assert-WIHostedSourceProof $SourceRoot $probe.bindings
        $result['access']=$observed
        return $result
    } finally {
        [Environment]::SetEnvironmentVariable('WI_INPUT_READ_TOKEN',$null,'Process')
    }
}


