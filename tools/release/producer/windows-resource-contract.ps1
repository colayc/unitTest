# Point-loading imports definitions only; no JSON, process, network or output writes.
. (Join-Path $PSScriptRoot '../portable/manifest.ps1')

function Assert-WRKeys($Value,[string[]]$Keys) {
    try {Assert-KeySet $Value $Keys} catch {throw 'WR_KEYS'}
}
function Assert-WRSame($Expected,$Actual,[string]$Code) {
    Assert-BasicSameValue $Expected $Actual $Code
}
function Assert-WRPositive($Value,[bool]$Text=$false) {
    if($Text){if($Value -isnot [string] -or $Value -cnotmatch '^[1-9][0-9]*$'){throw 'WR_POSITIVE'}}
    elseif(($Value -isnot [int] -and $Value -isnot [long]) -or $Value -le 0 -or $Value -gt 9007199254740991){throw 'WR_POSITIVE'}
}
function ConvertTo-WRDigest([string]$Value) {
    if($Value.StartsWith('sha256:',[StringComparison]::Ordinal)){$Value=$Value.Substring(7)}
    if($Value -cnotmatch '^[0-9a-f]{64}$'){throw 'WR_DIGEST'}
    return $Value
}
function Assert-WRBinding($Value,[string]$Name='') {
    Assert-WRKeys $Value @('path','size','sha256')
    if($Value.path -isnot [string]){throw 'WR_BINDING'}
    if($Name -ne '' -and $Value.path -cne $Name){throw 'WR_BINDING'}
    Assert-SizeAndDigest $Value.size $Value.sha256
}
function Assert-WRBindings([object[]]$Values,[bool]$Relative=$true) {
    if($Values.Count -eq 0){throw 'WR_BINDING_SET'}
    $seen=[Collections.Generic.HashSet[string]]::new([StringComparer]::OrdinalIgnoreCase)
    foreach($value in $Values){
        Assert-WRBinding $value
        if($Relative){Assert-PortableRelativePath $value.path}
        if(-not $seen.Add($value.path)){throw 'WR_BINDING_SET'}
    }
}
function Assert-WRSource($Source) {
    Assert-WRKeys $Source @('repository','commit','tree','state')
    if($Source.repository -cne 'colayc/unitTest' -or $Source.state -cne 'CI_COMMITTED_SOURCE' -or
       $Source.commit -cnotmatch '^[0-9a-f]{40}$' -or $Source.tree -cnotmatch '^[0-9a-f]{40}$'){throw 'WR_SOURCE'}
}
function Assert-WRExecution($Execution) {
    Assert-WRKeys $Execution @('workflowPath','ref','event','runId','runAttempt','runnerImage')
    if($Execution.workflowPath -cne '.github/workflows/windows-portable-resources.yml' -or
       $Execution.ref -cne 'refs/heads/master' -or $Execution.event -cne 'workflow_dispatch' -or
       $Execution.runnerImage -isnot [string] -or $Execution.runnerImage -cnotmatch '^[A-Za-z0-9._-]+$'){throw 'WR_EXECUTION'}
    Assert-WRPositive $Execution.runId $true;Assert-WRPositive $Execution.runAttempt
}
function Assert-WindowsResourceInvocation($Observed,[string]$ExpectedCommit) {
    Assert-WRKeys $Observed @('repository','event','ref','workflowPath','commit','tree','workflowSha','runId','runAttempt','trackedDirty')
    if($ExpectedCommit -cnotmatch '^[0-9a-f]{40}$' -or $Observed.repository -cne 'colayc/unitTest' -or
       $Observed.event -cne 'workflow_dispatch' -or $Observed.ref -cne 'refs/heads/master' -or
       $Observed.workflowPath -cne '.github/workflows/windows-portable-resources.yml' -or
       $Observed.commit -cne $ExpectedCommit -or $Observed.workflowSha -cne $ExpectedCommit -or
       $Observed.tree -cnotmatch '^[0-9a-f]{40}$' -or $Observed.trackedDirty -isnot [bool] -or $Observed.trackedDirty){throw 'WR_INVOCATION'}
    Assert-WRPositive $Observed.runId $true;Assert-WRPositive $Observed.runAttempt
}
function Assert-WRPolicy($Policy) {
    $expected=[ordered]@{schemaVersion=1;kind='WINDOWS_RESOURCE_POLICY';repository='colayc/unitTest';workflowPath='.github/workflows/windows-portable-resources.yml';event='workflow_dispatch';ref='refs/heads/master';recipeBaseline='59928bac784fedff614f7d154ef227f15b191eee';codeOssCommit='b1c0a14de1414fcdaa400695b4db1c0799bc3124';codeOssVersion='1.92.0';buildNodeVersion='20.14.0';repositoryNodeVersion='24.18.0';yarnVersion='1.22.22';pnpmVersion='11.4.0';retentionDays=7;requiredRoles=@('runtime','cmake','coverage','testgen','licenses');maximumArchiveBytes=4294967296L;maximumExpandedBytes=8589934592L;maximumManifestBytes=67108864}
    Assert-WRKeys $Policy ([string[]]@($expected.Keys))
    Assert-WRSame $expected $Policy 'WR_POLICY'
}
function Assert-WRFiles([object[]]$Files) {
    if($Files.Count -eq 0){throw 'WR_FILES'}
    $seen=[Collections.Generic.HashSet[string]]::new([StringComparer]::OrdinalIgnoreCase)
    $roles=[Collections.Generic.HashSet[string]]::new([StringComparer]::Ordinal)
    $licenses=[Collections.Generic.HashSet[string]]::new([StringComparer]::Ordinal)
    $previous=$null;$total=0L
    foreach($file in $Files){
        Assert-WRKeys $file @('path','size','sha256','role')
        Assert-PortableRelativePath $file.path;Assert-SizeAndDigest $file.size $file.sha256
        if(-not $seen.Add($file.path)){throw 'WR_FILES_CASE_COLLISION'}
        if($null -ne $previous -and [StringComparer]::Ordinal.Compare($previous,$file.path) -ge 0){throw 'WR_FILES_ORDER'}
        $previous=$file.path
        $prefix=switch -CaseSensitive ($file.role){'runtime'{'app/code-oss-runtime/'} 'cmake'{'bundles/cmake/'} 'coverage'{'bundles/coverage/'} 'testgen'{'bundles/testgen/'} 'licenses'{'licenses/'} default{throw 'WR_ROLE'}}
        if(-not $file.path.StartsWith($prefix,[StringComparison]::Ordinal) -or
           $file.path -match '(?i)(^|/)(UnitTestIDE\.exe|unit-test-service\.exe|Appx[^/]*|app-inputs\.json|portable-manifest\.json)$' -or
           $file.path -match '(?i)(^|/)(\.git|user-data|\.cache)(/|$)'){throw 'WR_ROLE'}
        [void]$roles.Add($file.role)
        if($file.role -ceq 'licenses'){
            $parts=$file.path.Split('/')
            if($parts.Count -lt 3 -or $parts[1] -cnotin @('code-oss','cmake','coverage','testgen')){throw 'WR_ROLE_LICENSE'}
            [void]$licenses.Add($parts[1])
        }
        $total+=$file.size;if($total -gt 8589934592L){throw 'WR_FILES_TOO_LARGE'}
    }
    if($roles.Count -ne 5 -or $licenses.Count -ne 4){throw 'WR_ROLE_MISSING'}
    return $total
}
function Assert-WindowsResourceManifest($M,$Expected) {
    Assert-WRKeys $M @('schemaVersion','kind','platform','architecture','recipeBaseline','source','execution','upstream','inputs','tools','runtimeInventoryBinding','files','licenses','fileCount','totalBytes')
    Assert-WRKeys $Expected @('source','execution','upstream','inputs','tools','policyBinding')
    if($M.schemaVersion -ne 1 -or $M.kind -cne 'WINDOWS_RESOURCE_SET' -or $M.platform -cne 'windows' -or $M.architecture -cne 'x64' -or
       $M.recipeBaseline -cne '59928bac784fedff614f7d154ef227f15b191eee' -or $M.files -isnot [array] -or $M.inputs -isnot [array] -or $M.tools -isnot [array] -or $M.licenses -isnot [array]){throw 'WR_MANIFEST'}
    Assert-WRSource $M.source;Assert-WRExecution $M.execution
    Assert-WRKeys $M.upstream @('repository','commit','version','target','lockBindings')
    if($M.upstream.repository -cne 'microsoft/vscode' -or $M.upstream.commit -cne 'b1c0a14de1414fcdaa400695b4db1c0799bc3124' -or
       $M.upstream.version -cne '1.92.0' -or $M.upstream.target -cne 'vscode-win32-x64'){throw 'WR_UPSTREAM'}
    Assert-WRBindings $M.upstream.lockBindings
    Assert-WRSame @('.nvmrc','.yarnrc','package.json','yarn.lock','build/gulpfile.vscode.js') @($M.upstream.lockBindings | ForEach-Object path) 'WR_UPSTREAM_LOCKS'
    Assert-WRBindings $M.inputs
    $toolRoles=[Collections.Generic.HashSet[string]]::new([StringComparer]::Ordinal)
    foreach($tool in $M.tools){
        Assert-WRKeys $tool @('role','path','size','sha256','version')
        Assert-SizeAndDigest $tool.size $tool.sha256
        if($tool.role -isnot [string] -or -not $toolRoles.Add($tool.role) -or $tool.path -isnot [string] -or -not [IO.Path]::IsPathFullyQualified($tool.path) -or
           $tool.version -isnot [string] -or [string]::IsNullOrWhiteSpace($tool.version)){throw 'WR_TOOL'}
    }
    foreach($role in @('powershell','git','node-repository','node-build','pnpm','yarn','tar','msbuild')){if(-not $toolRoles.Contains($role)){throw 'WR_TOOL_MISSING'}}
    foreach($field in @('source','execution','upstream','inputs','tools')){Assert-WRSame $Expected[$field] $M[$field] 'WR_MANIFEST_SOURCE'}
    Assert-WRBinding $M.runtimeInventoryBinding 'runtime-inventory.json'
    $total=Assert-WRFiles $M.files
    Assert-WRPositive $M.fileCount;Assert-WRPositive $M.totalBytes
    if($M.fileCount -ne $M.files.Count -or $M.totalBytes -ne $total){throw 'WR_FILES_COUNTS'}
    Assert-WRSame @($M.files | Where-Object role -CEQ 'licenses' | ForEach-Object path) $M.licenses 'WR_LICENSE_SET'
}
function ConvertTo-WRLegacyFile($File) {
    Assert-WRKeys $File @('path','size','sha256','role');Assert-SizeAndDigest $File.size $File.sha256
    return [ordered]@{path=$File.path;length=$File.size;sha256=$File.sha256}
}
function ConvertTo-WRTransportBinding($Local,[string]$Name) {
    Assert-WRBinding $Local
    if($Name -cnotin @('windows-resources.zip','windows-resources-manifest.json','runtime-inventory.json') -or [IO.Path]::GetFileName($Local.path) -cne $Name){throw 'WR_TRANSPORT_PATH'}
    return [ordered]@{path=$Name;size=$Local.size;sha256=$Local.sha256}
}
function Assert-WRArtifactCoordinate($Artifact,[string]$Name) {
    Assert-WRKeys $Artifact @('id','name','digest');Assert-WRPositive $Artifact.id $true
    if($Artifact.name -cne $Name){throw 'WR_COORDINATE'}
    [void](ConvertTo-WRDigest $Artifact.digest)
}
function Assert-WRCoordinate($Coordinate) {
    Assert-WRKeys $Coordinate @('kind','repository','workflowPath','sourceCommit','runId','runAttempt','payload','origin')
    if($Coordinate.kind -cne 'WINDOWS_RESOURCE_SET_V1' -or $Coordinate.repository -cne 'colayc/unitTest' -or
       $Coordinate.workflowPath -cne '.github/workflows/windows-portable-resources.yml' -or $Coordinate.sourceCommit -cnotmatch '^[0-9a-f]{40}$'){throw 'WR_COORDINATE'}
    Assert-WRPositive $Coordinate.runId $true;Assert-WRPositive $Coordinate.runAttempt
    Assert-WRArtifactCoordinate $Coordinate.payload ('windows-portable-resources-'+$Coordinate.runId+'-'+$Coordinate.runAttempt)
    Assert-WRArtifactCoordinate $Coordinate.origin ('windows-portable-resources-origin-'+$Coordinate.runId+'-'+$Coordinate.runAttempt)
    if($Coordinate.payload.id -ceq $Coordinate.origin.id -or (ConvertTo-WRDigest $Coordinate.payload.digest) -ceq (ConvertTo-WRDigest $Coordinate.origin.digest)){throw 'WR_COORDINATE'}
}
function Assert-WRApproval($Coordinate,$Approval) {
    Assert-WRCoordinate $Coordinate
    Assert-WRKeys $Approval @('coordinate','policyBinding','originBinding','archiveBinding','manifestBinding','runtimeInventoryBinding')
    Assert-WRSame $Coordinate $Approval.coordinate 'WR_APPROVAL_COORDINATE'
    Assert-WRBinding $Approval.policyBinding 'tools/release/producer/windows-resource-policy.json'
    Assert-WRBinding $Approval.originBinding 'windows-resources-origin.json'
    Assert-WRBinding $Approval.archiveBinding 'windows-resources.zip'
    Assert-WRBinding $Approval.manifestBinding 'windows-resources-manifest.json'
    Assert-WRBinding $Approval.runtimeInventoryBinding 'runtime-inventory.json'
    if($Approval.archiveBinding.size -gt 4294967296L -or $Approval.manifestBinding.size -gt 67108864 -or
       $Approval.runtimeInventoryBinding.size -gt 67108864 -or $Approval.originBinding.size -gt 67108864){throw 'WR_APPROVAL_SIZE'}
}
function Assert-WROrigin($Origin,$Coordinate,$Approval) {
    Assert-WRKeys $Origin @('schemaVersion','kind','source','execution','policyBinding','payload','archiveBinding','manifestBinding','runtimeInventoryBinding','generationStatus','signingStatus','state')
    Assert-WRSource $Origin.source;Assert-WRExecution $Origin.execution
    if($Origin.schemaVersion -ne 1 -or $Origin.kind -cne 'WINDOWS_RESOURCE_ORIGIN' -or $Origin.state -cne 'RESOURCE_STATIC_VALIDATED' -or
       $Origin.generationStatus -cne 'PAUSED_RUNTIME_DISABLED' -or $Origin.signingStatus -cne 'UNSIGNED_TEST_ONLY' -or
       $Origin.source.commit -cne $Coordinate.sourceCommit -or $Origin.execution.runId -cne $Coordinate.runId -or $Origin.execution.runAttempt -ne $Coordinate.runAttempt){throw 'WR_ORIGIN'}
    Assert-WRSame $Coordinate.payload $Origin.payload 'WR_ORIGIN_PAYLOAD'
    foreach($field in @('policyBinding','archiveBinding','manifestBinding','runtimeInventoryBinding')){Assert-WRSame $Approval[$field] $Origin[$field] 'WR_ORIGIN_BINDING'}
}
function Assert-WindowsResourceTransport($Coordinate,$Approval,$Run,$Artifacts) {
    Assert-WRApproval $Coordinate $Approval
    Assert-WRKeys $Run @('id','run_attempt','head_sha','path','event','head_branch','repository','status','conclusion','observedAt')
    Assert-WRPositive $Run.id $true;Assert-WRPositive $Run.run_attempt
    if($Run.id -cne $Coordinate.runId -or $Run.run_attempt -ne $Coordinate.runAttempt -or $Run.head_sha -cne $Coordinate.sourceCommit -or
       $Run.path -cne $Coordinate.workflowPath -or $Run.repository -cne $Coordinate.repository -or $Run.event -cne 'workflow_dispatch' -or
       $Run.head_branch -cne 'master' -or $Run.status -cne 'completed' -or $Run.conclusion -cne 'success'){throw 'WR_RUN'}
    $observed=[DateTimeOffset]::ParseExact($Run.observedAt,'yyyy-MM-ddTHH:mm:ssZ',[Globalization.CultureInfo]::InvariantCulture)
    Assert-WRKeys $Artifacts @('complete','total_count','items','origin','files')
    if($Artifacts.complete -isnot [bool] -or -not $Artifacts.complete -or $Artifacts.items -isnot [array] -or $Artifacts.total_count -ne $Artifacts.items.Count){throw 'WR_ARTIFACT_PAGINATION'}
    $ids=[Collections.Generic.HashSet[string]]::new([StringComparer]::Ordinal)
    $names=[Collections.Generic.HashSet[string]]::new([StringComparer]::Ordinal)
    foreach($artifact in $Artifacts.items){
        Assert-WRKeys $artifact @('id','name','digest','expired','created_at','expires_at','size_in_bytes','workflow_run')
        Assert-WRPositive $artifact.id $true
        if(-not $ids.Add($artifact.id) -or -not $names.Add($artifact.name)){throw 'WR_ARTIFACT_DUPLICATE'}
    }
    foreach($coordinateArtifact in @($Coordinate.payload,$Coordinate.origin)){
        $artifactMatches=@($Artifacts.items | Where-Object { $_.id -ceq $coordinateArtifact.id -and $_.name -ceq $coordinateArtifact.name })
        if($artifactMatches.Count -ne 1){throw 'WR_ARTIFACT_ID'}
        $artifact=$artifactMatches[0]
        Assert-WRKeys $artifact.workflow_run @('id','head_sha','head_branch')
        if($artifact.expired -isnot [bool] -or $artifact.expired -or $artifact.workflow_run.id -cne $Coordinate.runId -or
           $artifact.workflow_run.head_sha -cne $Coordinate.sourceCommit -or $artifact.workflow_run.head_branch -cne 'master' -or
           (ConvertTo-WRDigest $artifact.digest) -cne (ConvertTo-WRDigest $coordinateArtifact.digest)){throw 'WR_ARTIFACT_BINDING'}
        Assert-WRPositive $artifact.size_in_bytes
        $created=[DateTimeOffset]::Parse($artifact.created_at,[Globalization.CultureInfo]::InvariantCulture)
        $expires=[DateTimeOffset]::Parse($artifact.expires_at,[Globalization.CultureInfo]::InvariantCulture)
        if($created -gt $observed -or $expires -le $observed -or ($expires-$created).TotalDays -lt 7){throw 'WR_ARTIFACT_LIFETIME'}
    }
    Assert-WROrigin $Artifacts.origin $Coordinate $Approval
    Assert-WRBindings $Artifacts.files
    Assert-WRSame @($Approval.originBinding,$Approval.archiveBinding,$Approval.manifestBinding,$Approval.runtimeInventoryBinding) $Artifacts.files 'WR_TRANSPORT_FILES'
}
function ConvertTo-PortableWindowsResourceInput($Coordinate,$Approval,$FileBindings) {
    Assert-WRApproval $Coordinate $Approval
    [void](Assert-WRFiles $FileBindings)
    return [ordered]@{kind='WINDOWS_RESOURCE_SET_V1';coordinate=$Coordinate;policyBinding=$Approval.policyBinding;archiveBinding=$Approval.archiveBinding;manifestBinding=$Approval.manifestBinding;originBinding=$Approval.originBinding;runtimeInventoryBinding=$Approval.runtimeInventoryBinding;fileBindings=@(foreach($file in $FileBindings){[ordered]@{path=$file.path;size=$file.size;sha256=$file.sha256}})}
}
function Get-WRModelCaseNames {
    return [ordered]@{
        invocation=@('correct context','wrong repository','non dispatch','non master ref','wrong workflow','commit disagreement','dirty tracked source','unknown and duplicate keys')
        payload=@('five roles','missing role','extra business exe','path escape and device','case collision','reparse and special zip','file set and digest drift','zip round trip and legacy size')
        provenance=@('correct pair','wrong run and attempt','incomplete and failed run','expired artifact','payload id and digest swap','origin id and digest swap','file binding substitution','self reference and legacy format')
        flow=@('unknown stage and id','argument injection','first failure halts later work','timeout and cancellation','exit captured before disposal','incomplete output','first cause and cleanup failure','new input never becomes legacy')
    }
}
function Assert-WRModelResult($Result) {
    Assert-WRKeys $Result @('schemaVersion','state','groups','children','passed','results','nativeCalls','automaticRetries','failure')
    if($Result.schemaVersion -ne 1 -or $Result.state -cne 'PASS_RESOURCE_MODELS_ONLY' -or $Result.groups -ne 4 -or
       $Result.children -ne 32 -or $Result.passed -ne 32 -or $Result.nativeCalls -ne 0 -or $Result.automaticRetries -ne 0 -or
       $null -ne $Result.failure -or $Result.results -isnot [array] -or $Result.results.Count -ne 32){throw 'WR_MODEL_RESULT'}
    $expected=Get-WRModelCaseNames;$index=0
    foreach($group in $expected.Keys){foreach($name in $expected[$group]){
        $item=$Result.results[$index];Assert-WRKeys $item @('group','name','state')
        if($item.group -cne $group -or $item.name -cne $name -or $item.state -cne 'PASS'){throw 'WR_MODEL_RESULT_NAMES'}
        $index++
    }}
}
