param(
    [ValidateSet('All','Source','Archive','Publication','Access')][string]$Group='All',
    [string]$RecordDirectory
)

# Definitions only when dot-sourced; no fixture or resource operation at load.
function Assert-WIModelError([scriptblock]$Action,[string]$Code) {
    try { & $Action | Out-Null }
    catch {
        if ($_.Exception.Message -ceq $Code) { return }
        throw
    }
    throw ('WI_EXPECTED_ERROR '+$Code)
}
function Copy-WIModel($Value) {
    return ConvertFrom-BasicJsonBytes ([Text.Encoding]::UTF8.GetBytes(($Value | ConvertTo-Json -Depth 100 -Compress)))
}
function New-WIModelZip([string]$Path,[object[]]$Members) {
    $stream=[IO.FileStream]::new($Path,[IO.FileMode]::CreateNew,[IO.FileAccess]::Write,[IO.FileShare]::None)
    try {
        $zip=[IO.Compression.ZipArchive]::new($stream,[IO.Compression.ZipArchiveMode]::Create,$true)
        try {
            foreach($member in $Members) {
                $entry=$zip.CreateEntry($member.path)
                if($member.Contains('attributes')){$entry.ExternalAttributes=$member.attributes}
                $target=$entry.Open()
                try {$target.Write($member.bytes,0,$member.bytes.Length)}
                finally {$target.Dispose()}
            }
        } finally {$zip.Dispose()}
    } finally {$stream.Dispose()}
}
function New-WIModelFixture([string]$Root) {
    $root=New-WINewDirectory $Root
    $members=@()
    $expected=@()
    foreach($name in @('a','b')) {
        $bytes=[Text.Encoding]::ASCII.GetBytes($name)
        $members+=@([ordered]@{path=($name+'.txt');bytes=$bytes})
        $expected+=@([ordered]@{path=($name+'.txt');size=1;sha256=([Convert]::ToHexString([Security.Cryptography.SHA256]::HashData($bytes)).ToLowerInvariant())})
    }
    $archive=Join-Path $root 'fixture.zip'
    New-WIModelZip $archive $members
    $parts=New-WINewDirectory (Join-Path $root 'parts')
    $partPath=Join-Path $parts 'windows-inputs.zip.part-001'
    [IO.File]::Copy($archive,$partPath,$false)
    $bound=Get-BasicBinding $archive
    $manifest=[ordered]@{archive=[ordered]@{path='windows-inputs.zip';size=$bound.size;sha256=$bound.sha256};
        parts=@([ordered]@{index=1;path='windows-inputs.zip.part-001';size=$bound.size;sha256=$bound.sha256})}
    return [ordered]@{root=$root;archivePath=$archive;members=$members;expectedFiles=$expected;partsRoot=$parts;manifest=$manifest}
}
function New-WIModelLock {
    return [ordered]@{schemaVersion=1;kind='LOCAL_VERIFIED_WINDOWS_TOOL_INPUTS_V1';repository='colayc/unitTest-inputs';
        approvalState='APPROVED';coordinate=[ordered]@{commit=('1'*40);manifest=[ordered]@{
            path='snapshots/windows-inputs-v1/manifest.json';size=100;sha256=('2'*64)}}}
}
function New-WIModelAccess {
    return [ordered]@{repository='colayc/unitTest';event='pull_request';isFork=$false;isHostedWindows=$true;
        candidateSha=('1'*40);workflowSha=('2'*40);approvedCandidateSha=('1'*40);approvedWorkflowSha=('2'*40)}
}
function New-WIModelPublication([string]$Root) {
    $root=New-WINewDirectory $Root
    $repo=New-WINewDirectory (Join-Path $root 'repo')
    $verified=New-WINewDirectory (Join-Path $root 'verified')
    foreach($name in @('testgen','framework')) {
        $dir=New-WINewDirectory (Join-Path $verified $name)
        Write-NewUtf8File (Join-Path $dir 'fixture.txt') $name
    }
    return [ordered]@{root=$root;repo=$repo;verified=$verified;
        first=(Join-Path $repo '.superpowers/cache/testgen-bundle/22.1.8/windows-x64');
        second=(Join-Path $repo '.superpowers/cache/framework-bundle')}
}
function Invoke-WindowsInputModels([string]$RecordDirectory,[string]$Group='All') {
    if($Group -cnotin @('All','Source','Archive','Publication','Access')){throw 'WI_MODEL_GROUP'}
    . (Join-Path $PSScriptRoot 'windows-input-contract.ps1')
    . (Join-Path $PSScriptRoot 'windows-input-pack.ps1')
    . (Join-Path $PSScriptRoot 'windows-input-import.ps1')
    $root=New-WINewDirectory $RecordDirectory
    $sourceRoot=[IO.Path]::GetFullPath((Join-Path $PSScriptRoot '../../..'))
    $cases=[ordered]@{
        Source=[ordered]@{
            S1={ $l=New-WIModelLock;$l.approvalState='UNAPPROVED';$l.coordinate=$null;Assert-WIModelError {Assert-WISourceLock $l $true} 'WI_SOURCE_UNAPPROVED' }
            S2={ $l=New-WIModelLock;$l.coordinate.commit='master';Assert-WIModelError {Assert-WISourceLock $l $true} 'WI_COMMIT' }
            S3={ Assert-WIPartDescriptor ([ordered]@{index=1;path='windows-inputs.zip.part-001';size=1;sha256=('a'*64)}) 1 }
            S4={ Assert-WIModelError {Assert-WIPartDescriptor ([ordered]@{index=1;path='windows-inputs.zip.part-001';size=33554433L;sha256=('a'*64)}) 1} 'WI_PART_BOUND' }
            S5={ Assert-WIModelError {Assert-WIManifest ([ordered]@{schemaVersion=1;kind='LOCAL_VERIFIED_WINDOWS_TOOL_INPUTS_V1';platform='windows-x64';payloadFileCount=309;payloadBytes=126143346L;testgenManifestSha256=('a'*64);frameworkManifestSha256=('b'*64);parts=@();archive=@{};files=@()}) $sourceRoot} 'WI_PAYLOAD_BINDING' }
            S6={
                Assert-WIModelError {ConvertFrom-BasicJsonBytes ([Text.Encoding]::UTF8.GetBytes('{"a":1,"a":2}'))} 'BASIC_JSON_DUPLICATE_KEY'
                $l=New-WIModelLock;$l.extra='not allowed';Assert-WIModelError {Assert-WISourceLock $l $true} 'WI_KEYS'
            }
            S7={ Assert-WIModelError {Assert-WIFileRecords @([ordered]@{path='a.txt';size=1;sha256=('a'*64)},[ordered]@{path='A.txt';size=1;sha256=('b'*64)})} 'WI_CASE_COLLISION' }
            S8={ $l=New-WIModelLock;$l.kind='WINDOWS_RESOURCE_SET';Assert-WIModelError {Assert-WISourceLock $l $true} 'WI_LOCK_IDENTITY' }
        }
        Archive=[ordered]@{
            Z1={
                $f=New-WIModelFixture (Join-Path $root 'Z1')
                $out=Expand-WIZipCore $f.archivePath $f.expectedFiles (Join-Path $f.root 'out')
                foreach($file in $f.expectedFiles){$b=Get-BasicBinding (Join-Path $out.root $file.path);if($b.size -ne $file.size -or $b.sha256 -cne $file.sha256){throw 'WI_MODEL_ROUNDTRIP'}}
            }
            Z2={ $f=New-WIModelFixture (Join-Path $root 'Z2');$z=Join-Path $f.root 'bad.zip';New-WIModelZip $z @([ordered]@{path='../escape';bytes=[byte[]]@(1)});Assert-WIModelError {Expand-WIZipCore $z $f.expectedFiles (Join-Path $f.root 'out')} 'WI_ZIP_PATH' }
            Z3={ $f=New-WIModelFixture (Join-Path $root 'Z3');$z=Join-Path $f.root 'bad.zip';New-WIModelZip $z @([ordered]@{path='/escape';bytes=[byte[]]@(1)});Assert-WIModelError {Expand-WIZipCore $z $f.expectedFiles (Join-Path $f.root 'out')} 'WI_ZIP_PATH' }
            Z4={
                $f=New-WIModelFixture (Join-Path $root 'Z4')
                foreach($pair in @(@('slash','a\b.txt'),@('device','CON.txt'))) {
                    $z=Join-Path $f.root ($pair[0]+'.zip');New-WIModelZip $z @([ordered]@{path=$pair[1];bytes=[byte[]]@(1)})
                    Assert-WIModelError {Expand-WIZipCore $z $f.expectedFiles (Join-Path $f.root ($pair[0]+'-out'))} 'WI_ZIP_PATH'
                }
            }
            Z5={ $f=New-WIModelFixture (Join-Path $root 'Z5');$z=Join-Path $f.root 'bad.zip';New-WIModelZip $z @($f.members[0],$f.members[0]);Assert-WIModelError {Expand-WIZipCore $z $f.expectedFiles (Join-Path $f.root 'out')} 'WI_ZIP_DUPLICATE' }
            Z6={
                $f=New-WIModelFixture (Join-Path $root 'Z6');$z=Join-Path $f.root 'bad.zip'
                New-WIModelZip $z @([ordered]@{path='a.txt';bytes=[byte[]]@(97);attributes=1024},$f.members[1])
                Assert-WIModelError {Expand-WIZipCore $z $f.expectedFiles (Join-Path $f.root 'out')} 'WI_ZIP_TYPE'
            }
            Z7={ $f=New-WIModelFixture (Join-Path $root 'Z7');$z=Join-Path $f.root 'bad.zip';New-WIModelZip $z @($f.members[0]);Assert-WIModelError {Expand-WIZipCore $z $f.expectedFiles (Join-Path $f.root 'out')} 'WI_ZIP_SET' }
            Z8={
                $f=New-WIModelFixture (Join-Path $root 'Z8');$expected=Copy-WIModel $f.expectedFiles;$expected[0].size=2
                Assert-WIModelError {Expand-WIZipCore $f.archivePath $expected (Join-Path $f.root 'out')} 'WI_ZIP_SIZE'
                $bytes=[IO.File]::ReadAllBytes($f.archivePath)
                $bytes[$bytes.Length-14]=255;$bytes[$bytes.Length-13]=255
                $bytes[$bytes.Length-12]=255;$bytes[$bytes.Length-11]=255
                $bad=Join-Path $f.root 'bad-directory.zip'
                $stream=[IO.FileStream]::new($bad,[IO.FileMode]::CreateNew,[IO.FileAccess]::Write,[IO.FileShare]::None)
                try {$stream.Write($bytes,0,$bytes.Length)} finally {$stream.Dispose()}
                Assert-WIModelError {Expand-WIZipCore $bad $f.expectedFiles (Join-Path $f.root 'bad-out')} 'WI_ZIP_SIZE'
            }
        }
        Publication=[ordered]@{
            P1={ $f=New-WIModelFixture (Join-Path $root 'P1');[IO.File]::Delete((Join-Path $f.partsRoot 'windows-inputs.zip.part-001'));Assert-WIModelError {Join-WIArchiveCore $f.partsRoot $f.manifest (Join-Path $f.root 'joined.zip')} 'WI_TRANSFER_SET' }
            P2={ $f=New-WIModelFixture (Join-Path $root 'P2');Write-NewUtf8File (Join-Path $f.partsRoot 'extra.txt') 'x';Assert-WIModelError {Join-WIArchiveCore $f.partsRoot $f.manifest (Join-Path $f.root 'joined.zip')} 'WI_TRANSFER_SET' }
            P3={ $f=New-WIModelFixture (Join-Path $root 'P3');$f.manifest.parts[0].index=2;Assert-WIModelError {Join-WIArchiveCore $f.partsRoot $f.manifest (Join-Path $f.root 'joined.zip')} 'WI_PART_SEQUENCE' }
            P4={
                $f=New-WIModelFixture (Join-Path $root 'P4');$bad=Copy-WIModel $f.manifest;$bad.parts[0].sha256=('e'*64)
                Assert-WIModelError {Join-WIArchiveCore $f.partsRoot $bad (Join-Path $f.root 'bad-part.zip')} 'WI_PART_DIGEST'
                $bad=Copy-WIModel $f.manifest;$bad.archive.sha256=('e'*64)
                Assert-WIModelError {Join-WIArchiveCore $f.partsRoot $bad (Join-Path $f.root 'bad-archive.zip')} 'WI_ARCHIVE_DIGEST'
            }
            P5={ $f=New-WIModelPublication (Join-Path $root 'P5');[void](New-SafeDirectory $f.second);Assert-WIModelError {Publish-WIInputs $f.repo $f.verified} 'WI_CACHE_EXISTS';if(Test-Path -LiteralPath $f.first){throw 'WI_MODEL_PARTIAL_PUBLISH'} }
            P6={
                $f=New-WIModelPublication (Join-Path $root 'P6');$script:WIModelMoveCount=0
                function Move-WIStageDirectory([string]$From,[string]$To) {
                    $script:WIModelMoveCount++
                    if($script:WIModelMoveCount -eq 2){throw 'WI_MODEL_SECOND_PUBLISH'}
                    [IO.Directory]::Move($From,$To)
                }
                Assert-WIModelError {Publish-WIInputs $f.repo $f.verified} 'WI_MODEL_SECOND_PUBLISH'
                if(-not(Test-Path -LiteralPath $f.first) -or (Test-Path -LiteralPath $f.second) -or $script:WIModelMoveCount -ne 2){throw 'WI_MODEL_PUBLICATION_STATE'}
                $journal=Read-BasicJson (Join-Path $f.verified 'publication.json')
                if($journal.state -cne 'PARTIAL_FIRST_OWNED'){throw 'WI_MODEL_OWNERSHIP_RECEIPT'}
            }
            P7={
                $f=New-WIModelFixture (Join-Path $root 'P7');$file=Join-Path $f.root 'source.txt';Write-NewUtf8File $file 'a'
                $binding=Get-BasicBinding $file;[IO.File]::WriteAllText($file,'b')
                Assert-WIModelError {Assert-WIBoundFile $file $binding 'WI_SOURCE_DRIFT'} 'WI_SOURCE_DRIFT'
            }
            P8={
                $f=New-WIModelFixture (Join-Path $root 'P8');[void](Expand-WIZipCore $f.archivePath $f.expectedFiles (Join-Path $f.root 'out'))
                $h=[IO.FileStream]::new($f.archivePath,[IO.FileMode]::Open,[IO.FileAccess]::Read,[IO.FileShare]::None);$h.Dispose()
                $bad=Copy-WIModel $f.expectedFiles;$bad[0].sha256=('e'*64)
                Assert-WIModelError {Expand-WIZipCore $f.archivePath $bad (Join-Path $f.root 'bad-out')} 'WI_ZIP_DIGEST'
                $h=[IO.FileStream]::new($f.archivePath,[IO.FileMode]::Open,[IO.FileAccess]::Read,[IO.FileShare]::None);$h.Dispose()
            }
        }
        Access=[ordered]@{
            A1={ Assert-WIModelError {Assert-WICredentialPresent ''} 'WI_READ_TOKEN' }
            A2={ Assert-WIModelError {Assert-WIHttpStatus 302} 'WI_HTTP_STATUS';Assert-WIModelError {Assert-WIHttpStatus 504} 'WI_HTTP_STATUS' }
            A3={
                $import=[IO.File]::ReadAllText((Join-Path $sourceRoot 'tools/release/producer/windows-input-import.ps1'))
                $build=[IO.File]::ReadAllText((Join-Path $sourceRoot 'tools/release/producer/windows-resource-build.ps1'))
                if($import -notmatch 'Environment\.Clear\(\)' -or $import -match 'Write-(Host|Output).*WI_INPUT_READ_TOKEN' -or $build -match "safeEnvironment\[.WI_INPUT_READ_TOKEN"){throw 'WI_MODEL_CREDENTIAL_SCOPE'}
            }
            A4={ $o=New-WIModelAccess;$o.isFork=$true;Assert-WIModelError {Assert-WIAccessContext $o} 'WI_ACCESS' }
            A5={ $o=New-WIModelAccess;$o.isHostedWindows=$false;Assert-WIModelError {Assert-WIAccessContext $o} 'WI_ACCESS' }
            A6={ $o=New-WIModelAccess;$o.workflowSha=('3'*40);Assert-WIModelError {Assert-WIAccessContext $o} 'WI_APPROVAL_SHA' }
            A7={
                . (Join-Path $PSScriptRoot 'windows-resource-build.ps1')
                if((Get-WRInputPaths).Count -ne 58){throw 'WI_MODEL_INPUT_COUNT'}
                foreach($pair in @(@('manifest.ps1','a5031557ee082b05ea8b8107fbd42b4750adb4b2ff2b6c4ff912906c5bcd7410'),@('package.ps1','67e23a06cb39c362457c8949acea54458c8425d59cabdca5028b1cb6fc95e1f5'))){
                    if((Get-BasicBinding (Join-Path $PSScriptRoot ('../portable/'+$pair[0]))).sha256 -cne $pair[1]){throw 'WI_MODEL_HELPER_DRIFT'}
                }
            }
            A8={
                $resource=[IO.File]::ReadAllText((Join-Path $sourceRoot '.github/workflows/windows-portable-resources.yml'))
                $foundation=[IO.File]::ReadAllText((Join-Path $sourceRoot '.github/workflows/foundation.yml'))
                if($resource -notmatch 'environment: windows-inputs-read' -or ($resource.Split('WI_INPUT_READ_TOKEN:').Count-1) -ne 1){throw 'WI_MODEL_WORKFLOW_SCOPE'}
                if($foundation -notmatch 'WI_PRIVATE_INPUT_ROUTE' -or $foundation -notmatch 'windows-inputs-no-access' -or $foundation -notmatch 'WI_CACHE_RESTORE_DISABLED'){throw 'WI_MODEL_ROUTE_GUARD'}
                if(($resource.Split('uses: actions/upload-artifact@').Count-1) -ne 3 -or $resource -match "-Stage 'Import"){throw 'WI_MODEL_ARTIFACT_OR_STAGE'}
            }
        }
    }
    $results=[Collections.Generic.List[object]]::new()
    $activeGroup=$null;$activeCase=$null
    try {
        foreach($groupName in $cases.Keys) {
            if($Group -cne 'All' -and $Group -cne $groupName){continue}
            $activeGroup=$groupName
            if($cases[$groupName].Count -ne 8){throw 'WI_MODEL_CASE_COUNT'}
            foreach($caseName in $cases[$groupName].Keys) {
                $activeCase=$caseName
                & ($cases[$groupName][$caseName])
                $results.Add([ordered]@{group=$groupName;name=$caseName;state='PASS'})
            }
        }
        $expected=if($Group -ceq 'All'){32}else{8}
        if($results.Count -ne $expected){throw 'WI_MODEL_INCOMPLETE'}
        $result=[ordered]@{state='MODEL_VALIDATED';groups=$(if($Group -ceq 'All'){4}else{1});models=$expected;passed=$results.Count;cases=$results.ToArray();nativeExecutions=0;networkRequests=0}
        Write-NewJson (Join-Path $root 'model-result.json') $result
        return $result
    } catch {
        $primary=$_
        try {
            Write-NewJson (Join-Path $root 'model-failure.json') ([ordered]@{
                state='STOPPED';group=$activeGroup;name=$activeCase;completed=$results.ToArray();
                failure=$primary.Exception.Message;automaticRetries=0
            })
        } catch { }
        throw $primary
    }
}
if($MyInvocation.InvocationName -ne '.') {
    $ErrorActionPreference='Stop'
    Invoke-WindowsInputModels $RecordDirectory $Group | ConvertTo-Json -Depth 30
}

