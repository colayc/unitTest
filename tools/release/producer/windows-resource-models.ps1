param([string]$RecordDirectory)

# Definitions only when dot-sourced. No native invoker is exposed to a model.
function Assert-WRModelError([scriptblock]$Action,[string]$Code) {
    try { & $Action | Out-Null } catch {
        if ($_.Exception.Message.StartsWith($Code,[StringComparison]::Ordinal)) { return }
        throw
    }
    throw ('WR_MODEL_EXPECTED_ERROR '+$Code)
}
function Copy-WRModel($Value) {
    return ConvertFrom-BasicJsonBytes ([Text.Encoding]::UTF8.GetBytes(($Value | ConvertTo-Json -Depth 100 -Compress)))
}
function New-WRModelFixture {
    $digest='c'*64
    $source=[ordered]@{repository='colayc/unitTest';commit=('a'*40);tree=('b'*40);state='CI_COMMITTED_SOURCE'}
    $execution=[ordered]@{workflowPath='.github/workflows/windows-portable-resources.yml';ref='refs/heads/master';event='workflow_dispatch';runId='11';runAttempt=1;runnerImage='20261101.1'}
    $observed=[ordered]@{repository=$source.repository;event=$execution.event;ref=$execution.ref;workflowPath=$execution.workflowPath;commit=$source.commit;tree=$source.tree;workflowSha=$source.commit;runId='11';runAttempt=1;trackedDirty=$false}
    $inputBinding=[ordered]@{path='package.json';size=1;sha256=$digest}
    $tools=@(foreach($role in @('powershell','git','node-repository','node-build','pnpm','yarn','tar','msbuild')) {
        [ordered]@{role=$role;path=('C:/model/'+$role+'.txt');size=1;sha256=$digest;version='model-only'}
    })
    $upstream=[ordered]@{repository='microsoft/vscode';commit='b1c0a14de1414fcdaa400695b4db1c0799bc3124';version='1.92.0';target='vscode-win32-x64';lockBindings=@(foreach($name in @('.nvmrc','.yarnrc','package.json','yarn.lock','build/gulpfile.vscode.js')){[ordered]@{path=$name;size=1;sha256=$digest}})}
    $files=@(foreach($entry in @(@('app/code-oss-runtime/sample.txt','runtime'),@('bundles/cmake/sample.txt','cmake'),@('bundles/coverage/sample.txt','coverage'),@('bundles/testgen/sample.txt','testgen'),@('licenses/code-oss/license.txt','licenses'),@('licenses/cmake/license.txt','licenses'),@('licenses/coverage/license.txt','licenses'),@('licenses/testgen/license.txt','licenses'))) {
        [ordered]@{path=$entry[0];size=1;sha256=$digest;role=$entry[1]}
    })
    $fileMap=@{};foreach($file in $files){$fileMap[$file.path]=$file}
    $files=@(foreach($name in (Get-OrdinalStrings ([string[]]@($fileMap.Keys)))){$fileMap[$name]})
    $archive=[ordered]@{path='windows-resources.zip';size=10;sha256=('d'*64)}
    $manifestBinding=[ordered]@{path='windows-resources-manifest.json';size=10;sha256=('e'*64)}
    $inventory=[ordered]@{path='runtime-inventory.json';size=10;sha256=('f'*64)}
    $policy=[ordered]@{path='tools/release/producer/windows-resource-policy.json';size=10;sha256=$digest}
    $manifest=[ordered]@{schemaVersion=1;kind='WINDOWS_RESOURCE_SET';platform='windows';architecture='x64';recipeBaseline='59928bac784fedff614f7d154ef227f15b191eee';source=$source;execution=$execution;upstream=$upstream;inputs=@($inputBinding);tools=$tools;runtimeInventoryBinding=$inventory;files=$files;licenses=@($files | Where-Object role -CEQ 'licenses' | ForEach-Object path);fileCount=8;totalBytes=8}
    $payload=[ordered]@{id='21';name='windows-portable-resources-11-1';digest=('1'*64)}
    $originCoordinate=[ordered]@{id='22';name='windows-portable-resources-origin-11-1';digest=('2'*64)}
    $coordinate=[ordered]@{kind='WINDOWS_RESOURCE_SET_V1';repository='colayc/unitTest';workflowPath=$execution.workflowPath;sourceCommit=$source.commit;runId='11';runAttempt=1;payload=$payload;origin=$originCoordinate}
    $originBinding=[ordered]@{path='windows-resources-origin.json';size=10;sha256=('3'*64)}
    $approval=[ordered]@{coordinate=$coordinate;policyBinding=$policy;originBinding=$originBinding;archiveBinding=$archive;manifestBinding=$manifestBinding;runtimeInventoryBinding=$inventory}
    $origin=[ordered]@{schemaVersion=1;kind='WINDOWS_RESOURCE_ORIGIN';source=$source;execution=$execution;policyBinding=$policy;payload=$payload;archiveBinding=$archive;manifestBinding=$manifestBinding;runtimeInventoryBinding=$inventory;generationStatus='PAUSED_RUNTIME_DISABLED';signingStatus='UNSIGNED_TEST_ONLY';state='RESOURCE_STATIC_VALIDATED'}
    $run=[ordered]@{id='11';run_attempt=1;head_sha=$source.commit;path=$execution.workflowPath;event='workflow_dispatch';head_branch='master';repository='colayc/unitTest';status='completed';conclusion='success';observedAt='2026-11-08T00:00:00Z'}
    $items=@(foreach($item in @($payload,$originCoordinate)) {[ordered]@{id=$item.id;name=$item.name;digest=$item.digest;expired=$false;created_at='2026-11-07T00:00:00Z';expires_at='2026-11-15T00:00:00Z';size_in_bytes=100;workflow_run=[ordered]@{id='11';head_sha=$source.commit;head_branch='master'}}})
    $artifacts=[ordered]@{complete=$true;total_count=2;items=$items;origin=$origin;files=@($originBinding,$archive,$manifestBinding,$inventory)}
    return [ordered]@{observed=$observed;manifest=$manifest;expected=[ordered]@{source=$source;execution=$execution;upstream=$upstream;inputs=@($inputBinding);tools=$tools;policyBinding=$policy};coordinate=$coordinate;approval=$approval;run=$run;artifacts=$artifacts}
}
function Invoke-WindowsResourceModels([string]$RecordDirectory) {
    . (Join-Path $PSScriptRoot 'windows-resource-contract.ps1')
    . (Join-Path $PSScriptRoot 'windows-resource-package.ps1')
    . (Join-Path $PSScriptRoot 'windows-resource-build.ps1')
    $ErrorActionPreference='Stop'
    if(Test-Path -LiteralPath $RecordDirectory){throw 'WR_MODEL_OUTPUT_EXISTS'}
    [void](New-SafeDirectory $RecordDirectory)
    $fixture=New-WRModelFixture
    $results=[Collections.Generic.List[object]]::new()
    $cases=[ordered]@{
        invocation=[ordered]@{
            'correct context'={Assert-WindowsResourceInvocation $fixture.observed ('a'*40)}
            'wrong repository'={$value=Copy-WRModel $fixture.observed;$value.repository='other/repo';Assert-WRModelError {Assert-WindowsResourceInvocation $value ('a'*40)} 'WR_INVOCATION'}
            'non dispatch'={$value=Copy-WRModel $fixture.observed;$value.event='pull_request';Assert-WRModelError {Assert-WindowsResourceInvocation $value ('a'*40)} 'WR_INVOCATION'}
            'non master ref'={$value=Copy-WRModel $fixture.observed;$value.ref='refs/heads/other';Assert-WRModelError {Assert-WindowsResourceInvocation $value ('a'*40)} 'WR_INVOCATION'}
            'wrong workflow'={$value=Copy-WRModel $fixture.observed;$value.workflowPath='.github/workflows/release-inputs.yml';Assert-WRModelError {Assert-WindowsResourceInvocation $value ('a'*40)} 'WR_INVOCATION'}
            'commit disagreement'={foreach($field in @('commit','workflowSha')){$value=Copy-WRModel $fixture.observed;$value[$field]='d'*40;Assert-WRModelError {Assert-WindowsResourceInvocation $value ('a'*40)} 'WR_INVOCATION'};Assert-WRModelError {Assert-WindowsResourceInvocation $fixture.observed ('e'*40)} 'WR_INVOCATION'}
            'dirty tracked source'={$value=Copy-WRModel $fixture.observed;$value.trackedDirty=$true;Assert-WRModelError {Assert-WindowsResourceInvocation $value ('a'*40)} 'WR_INVOCATION'}
            'unknown and duplicate keys'={$value=Copy-WRModel $fixture.observed;$value.extra=1;Assert-WRModelError {Assert-WindowsResourceInvocation $value ('a'*40)} 'WR_KEYS';Assert-WRModelError {ConvertFrom-BasicJsonBytes ([Text.Encoding]::UTF8.GetBytes('{"a":1,"a":2}'))} 'BASIC_JSON_DUPLICATE_KEY'}
        }
        payload=[ordered]@{
            'five roles'={Assert-WindowsResourceManifest $fixture.manifest $fixture.expected}
            'missing role'={$value=Copy-WRModel $fixture.manifest;$value.files=@($value.files | Where-Object role -CNE 'coverage');$value.fileCount--;$value.totalBytes--;Assert-WRModelError {Assert-WindowsResourceManifest $value $fixture.expected} 'WR_ROLE'}
            'extra business exe'={$value=Copy-WRModel $fixture.manifest;$value.files[0].path='service/unit-test-service.exe';Assert-WRModelError {Assert-WindowsResourceManifest $value $fixture.expected} 'WR_ROLE'}
            'path escape and device'={foreach($name in @('../escape','app/code-oss-runtime/CON.txt')){$value=Copy-WRModel $fixture.manifest;$value.files[0].path=$name;Assert-WRModelError {Assert-WindowsResourceManifest $value $fixture.expected} 'PATH_'}}
            'case collision'={$value=Copy-WRModel $fixture.manifest;$value.files+=@([ordered]@{path='APP/code-oss-runtime/sample.txt';size=1;sha256=('c'*64);role='runtime'});Assert-WRModelError {Assert-WindowsResourceManifest $value $fixture.expected} 'WR_FILES'}
            'reparse and special zip'={Assert-WRModelError {Assert-ObservedAttributes ([IO.FileAttributes]::ReparsePoint)} 'PATH_REPARSE';$zip=Join-Path $RecordDirectory 'special.zip';$stream=[IO.File]::Open($zip,[IO.FileMode]::CreateNew);try{$archive=[IO.Compression.ZipArchive]::new($stream,[IO.Compression.ZipArchiveMode]::Create,$true);try{$entry=$archive.CreateEntry('link.txt');$entry.ExternalAttributes=-1610612704}finally{$archive.Dispose()}}finally{$stream.Dispose()};Assert-WRModelError {Assert-ZipPayload $zip @([ordered]@{path='link.txt';length=0;sha256=('c'*64)})} 'ZIP_SPECIAL_ENTRY'}
            'file set and digest drift'={$value=Copy-WRModel $fixture.manifest;$value.fileCount++;Assert-WRModelError {Assert-WindowsResourceManifest $value $fixture.expected} 'WR_FILES';$binding=Copy-WRModel $fixture.approval.archiveBinding;$binding.sha256='0'*64;Assert-WRModelError {Assert-WRSame $binding $fixture.approval.archiveBinding 'WR_BINDING'} 'WR_BINDING'}
            'zip round trip and legacy size'={$root=New-SafeDirectory (Join-Path $RecordDirectory 'roundtrip');Write-NewUtf8File (Join-Path $root 'sample.txt') 'x';$binding=Get-BasicBinding (Join-Path $root 'sample.txt');$legacy=ConvertTo-WRLegacyFile ([ordered]@{path='sample.txt';size=$binding.size;sha256=$binding.sha256;role='runtime'});if($legacy.length -ne 1 -or $legacy.Contains('size')){throw 'WR_MODEL_SIZE'};$zip=Join-Path $RecordDirectory 'roundtrip.zip';New-PayloadZip $root $zip @($legacy);Expand-VerifiedZip $zip (Join-Path $RecordDirectory 'expanded') @($legacy);[void](Assert-Tree (Join-Path $RecordDirectory 'expanded') @($legacy) @())}
        }
        provenance=[ordered]@{
            'correct pair'={Assert-WindowsResourceTransport $fixture.coordinate $fixture.approval $fixture.run $fixture.artifacts}
            'wrong run and attempt'={foreach($field in @('id','run_attempt')){$value=Copy-WRModel $fixture.run;$value[$field]=if($field -ceq 'id'){'12'}else{2};Assert-WRModelError {Assert-WindowsResourceTransport $fixture.coordinate $fixture.approval $value $fixture.artifacts} 'WR_RUN'}}
            'incomplete and failed run'={foreach($field in @('status','conclusion')){$value=Copy-WRModel $fixture.run;$value[$field]='failure';Assert-WRModelError {Assert-WindowsResourceTransport $fixture.coordinate $fixture.approval $value $fixture.artifacts} 'WR_RUN'}}
            'expired artifact'={$value=Copy-WRModel $fixture.artifacts;$value.items[0].expired=$true;Assert-WRModelError {Assert-WindowsResourceTransport $fixture.coordinate $fixture.approval $fixture.run $value} 'WR_ARTIFACT'}
            'payload id and digest swap'={foreach($field in @('id','digest')){$value=Copy-WRModel $fixture.artifacts;$value.items[0][$field]=$fixture.coordinate.origin[$field];Assert-WRModelError {Assert-WindowsResourceTransport $fixture.coordinate $fixture.approval $fixture.run $value} 'WR_ARTIFACT'}}
            'origin id and digest swap'={foreach($field in @('id','digest')){$value=Copy-WRModel $fixture.artifacts;$value.items[1][$field]=$fixture.coordinate.payload[$field];Assert-WRModelError {Assert-WindowsResourceTransport $fixture.coordinate $fixture.approval $fixture.run $value} 'WR_ARTIFACT'}}
            'file binding substitution'={foreach($field in @('archiveBinding','manifestBinding','runtimeInventoryBinding')){$value=Copy-WRModel $fixture.artifacts;$value.origin[$field].sha256='0'*64;Assert-WRModelError {Assert-WindowsResourceTransport $fixture.coordinate $fixture.approval $fixture.run $value} 'WR_ORIGIN'}}
            'self reference and legacy format'={$value=Copy-WRModel $fixture.artifacts;$value.origin.origin=$fixture.coordinate.origin;Assert-WRModelError {Assert-WindowsResourceTransport $fixture.coordinate $fixture.approval $fixture.run $value} 'WR_KEYS';$value=Copy-WRModel $fixture.coordinate;$value.kind='MSIX';Assert-WRModelError {Assert-WindowsResourceTransport $value $fixture.approval $fixture.run $fixture.artifacts} 'WR_COORDINATE'}
        }
        flow=[ordered]@{
            'unknown stage and id'={Assert-WRModelError {Assert-WRStageName 'BuildApplication'} 'WR_STAGE';Assert-WRModelError {Get-WindowsResourceStep 'arbitrary' @{}} 'WR_STEP'}
            'argument injection'={Assert-WRModelError {Assert-WindowsResourceInvocation $fixture.observed ('a'*40+'; calc')} 'WR_INVOCATION';Assert-WRModelError {Get-WindowsResourceStep 'CMake --evil' @{}} 'WR_STEP'}
            'first failure halts later work'={Assert-WRModelError {Assert-WRNextStage @('Preflight','FAILED') 'VerifyChecks'} 'WR_ORDER'}
            'timeout and cancellation'={foreach($field in @('timedOut','cancelled')){$value=New-WRCompletionFixture;$value[$field]=$true;Assert-WRModelError {Assert-WRNativeCompletion $value} 'WR_NATIVE'}}
            'exit captured before disposal'={$value=New-WRCompletionFixture;$value.capturedBeforeDispose=$false;Assert-WRModelError {Assert-WRNativeCompletion $value} 'WR_NATIVE';$value=New-WRCompletionFixture;$value.rawExit=$null;Assert-WRModelError {Assert-WRNativeCompletion $value} 'WR_NATIVE'}
            'incomplete output'={$value=New-WRCompletionFixture;$value.outputComplete=$false;Assert-WRModelError {Assert-WRNativeCompletion $value} 'WR_NATIVE'}
            'first cause and cleanup failure'={$value=New-WRCompletionFixture;$value.firstFailure='first';$value.cleanupFailure='cleanup';$value.rawExit=1;try{Assert-WRNativeCompletion $value;throw 'WR_MODEL_NO_ERROR'}catch{if($_.Exception.Message -cne 'WR_NATIVE first | cleanup: cleanup'){throw}}}
            'new input never becomes legacy'={$value=ConvertTo-PortableWindowsResourceInput $fixture.coordinate $fixture.approval $fixture.manifest.files;if($value.kind -cne 'WINDOWS_RESOURCE_SET_V1' -or $value.fileBindings[0].Contains('role')){throw 'WR_MODEL_ADAPTER'};$coordinate=Copy-WRModel $fixture.coordinate;$coordinate.kind='legacyFoundation';Assert-WRModelError {ConvertTo-PortableWindowsResourceInput $coordinate $fixture.approval $fixture.manifest.files} 'WR_COORDINATE'}
        }
    }
    $failure=$null
    try {
        $expectedCases=Get-WRModelCaseNames
        Assert-WRSame @($expectedCases.Keys) @($cases.Keys) 'WR_MODEL_CASE_GROUPS'
        foreach($groupName in $cases.Keys){
            if($cases[$groupName].Count -ne 8){throw 'WR_MODEL_CASE_SET'}
            Assert-WRSame $expectedCases[$groupName] @($cases[$groupName].Keys) 'WR_MODEL_CASE_NAMES'
            foreach($caseName in $cases[$groupName].Keys){
                & $cases[$groupName][$caseName] | Out-Null
                $results.Add([ordered]@{group=$groupName;name=$caseName;state='PASS'})
            }
        }
    } catch {$failure=$_.Exception.Message}
    $result=[ordered]@{schemaVersion=1;state=$(if($null -eq $failure){'PASS_RESOURCE_MODELS_ONLY'}else{'FAILED_RESOURCE_MODELS'});groups=4;children=32;passed=$results.Count;results=$results.ToArray();nativeCalls=0;automaticRetries=0;failure=$failure}
    if($null -eq $failure){Assert-WRModelResult $result}
    Write-NewJson (Join-Path $RecordDirectory 'model-results.json') $result
    return $result
}
function New-WRCompletionFixture {
    return [ordered]@{rawExit=0;pid=123;capturedBeforeDispose=$true;outputComplete=$true;timedOut=$false;cancelled=$false;firstFailure=$null;cleanupFailure=$null}
}
if($MyInvocation.InvocationName -ne '.') {
    $ErrorActionPreference='Stop'
    $modelResult=Invoke-WindowsResourceModels $RecordDirectory
    $modelResult | ConvertTo-Json -Depth 100 -Compress
    if($modelResult.state -cne 'PASS_RESOURCE_MODELS_ONLY'){exit 1}
}
