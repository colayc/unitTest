. (Join-Path $PSScriptRoot 'windows-resource-contract.ps1')
. (Join-Path $PSScriptRoot 'windows-resource-package.ps1')

function Get-WRStages {
    return ,@('Preflight','PrepareRepositoryDependencies','VerifyChecks','FetchSource','VerifySource','CodeOssDependencies','BuildRuntime','CheckRuntimeVersion','PrepareBundles','PackageResources','WriteOrigin')
}
function Assert-WRStageName([string]$Stage) {
    if($Stage -cnotin ((Get-WRStages)+@('FailureReceipt'))){throw 'WR_STAGE'}
}
function Assert-WRNextStage([string[]]$Completed,[string]$Stage) {
    $stages=Get-WRStages
    if($Completed.Count -ge $stages.Count -or $Stage -cne $stages[$Completed.Count]){throw 'WR_ORDER'}
    for($index=0;$index -lt $Completed.Count;$index++){if($Completed[$index] -cne $stages[$index]){throw 'WR_ORDER'}}
}
function Assert-WRNativeCompletion($Result) {
    Assert-WRKeys $Result @('rawExit','pid','capturedBeforeDispose','outputComplete','timedOut','cancelled','firstFailure','cleanupFailure')
    if($Result.rawExit -isnot [int] -or $Result.rawExit -ne 0 -or $Result.pid -isnot [int] -or $Result.pid -le 0 -or
       $Result.capturedBeforeDispose -isnot [bool] -or -not $Result.capturedBeforeDispose -or
       $Result.outputComplete -isnot [bool] -or -not $Result.outputComplete -or $Result.timedOut -isnot [bool] -or $Result.timedOut -or
       $Result.cancelled -isnot [bool] -or $Result.cancelled -or $null -ne $Result.firstFailure -or $null -ne $Result.cleanupFailure){
        $message='WR_NATIVE '+$(if($null -ne $Result.firstFailure){$Result.firstFailure}else{'incomplete or unsuccessful exit'})
        if($null -ne $Result.cleanupFailure){$message+=' | cleanup: '+$Result.cleanupFailure}
        throw $message
    }
}
function Get-WRInputPaths {
    # Exact Task 0 source closure plus the eight new producer files.
    return ,@(
        'package.json'
        'pnpm-lock.yaml'
        'pnpm-workspace.yaml'
        '.npmrc'
        'packages/protocol-models/package.json'
        'packages/protocol-schema/package.json'
        'packages/coverage-models/package.json'
        'packages/coverage-schema/package.json'
        'packages/test-client/package.json'
        'tools/service-probe/package.json'
        'apps/code-oss-extension/package.json'
        'tools/release/producer/source-manifest.json'
        'tools/release/producer/source-manifest.mjs'
        'tools/release/producer/runtime-inventory.mjs'
        'tools/release/code-oss-runtime.mjs'
        'tools/release/portable-path.mjs'
        'tools/release/linux/runtime-mode-inventory.mjs'
        'tools/cmake-bundle/prepare.mjs'
        'tools/cmake-bundle/manifest.json'
        'tools/coverage-bundle/prepare.mjs'
        'tools/coverage-bundle/layout.mjs'
        'tools/coverage-bundle/manifest.json'
        'tools/coverage-bundle/manifest.schema.json'
        'tools/coverage-bundle/build-linux.sh'
        'tools/coverage-bundle/runner/NOTICE.txt'
        'tools/coverage-bundle/runner/__main__.py'
        'tools/coverage-bundle/runner/contract.py'
        'tools/coverage-bundle/licenses/dependencies.json'
        'tools/coverage-bundle/licenses/Python-3.14.6.txt'
        'tools/coverage-bundle/licenses/gcovr-8.6.txt'
        'tools/coverage-bundle/licenses/xz-5.8.1-COPYING.txt'
        'tools/testgen-bundle/prepare.mjs'
        'tools/testgen-bundle/check.mjs'
        'tools/testgen-bundle/manifest.json'
        'tools/testgen-bundle/manifest.schema.json'
        'tools/testgen-bundle/prepare.test.mjs'
        'tools/release/portable/manifest.ps1'
        'tools/release/portable/package.ps1'
        '.github/workflows/release-inputs.yml'
        '.github/workflows/foundation.yml'
        '.github/workflows/phase9-gates.yml'
        'tools/release/producer/trusted-run.mjs'
        'tools/release/stage.mjs'
        'tools/coverage-bundle/licenses/README.md'
        '.github/workflows/windows-portable-resources.yml'
        'tools/release/producer/windows-resource-policy.json'
        'tools/release/producer/windows-resource-contract.ps1'
        'tools/release/producer/windows-resource-package.ps1'
        'tools/release/producer/windows-resource-build.ps1'
        'tools/release/producer/windows-resource-models.ps1'
        'tools/release/producer/windows-resource-workflow.test.mjs'
        'tools/release/producer/README.windows-resources.zh-CN.txt'
    )
}
function Get-WRTool($Context,[string]$Role) {
    $matches=@($Context.tools | Where-Object role -CEQ $Role)
    if($matches.Count -ne 1){throw 'WR_TOOL_MISSING'}
    return $matches[0]
}
function Get-WRToolBinding($Tool) {
    return [ordered]@{path=$Tool.path;size=$Tool.size;sha256=$Tool.sha256}
}
function Add-WRTool($Context,[string]$Role,[string]$Path,[string]$Version) {
    $binding=Get-BasicBinding $Path
    $tool=[ordered]@{role=$Role;path=$binding.path;size=$binding.size;sha256=$binding.sha256;version=$Version}
    $existing=@($Context.tools | Where-Object role -CEQ $Role)
    if($existing.Count -gt 0){Assert-WRSame $existing[0] $tool 'WR_TOOL_DRIFT'}else{$Context.tools+=@($tool)}
}
function Assert-WRActiveContext($Context) {
    $active=Get-Variable -Name WRActiveContext -Scope Script -ValueOnly -ErrorAction SilentlyContinue
    if($null -eq $active -or -not [object]::ReferenceEquals($Context,$active) -or
       $env:GITHUB_ACTIONS -cne 'true' -or $env:RUNNER_OS -cne 'Windows' -or -not $IsWindows){throw 'WR_RUNNER_CONTEXT_REQUIRED'}
}
function Get-WRRemainingSeconds($Context,[int]$Maximum) {
    $elapsed=([DateTimeOffset]::UtcNow-[DateTimeOffset]::Parse($Context.startedAtUtc,[Globalization.CultureInfo]::InvariantCulture)).TotalSeconds
    $remaining=[Math]::Floor(7200-$elapsed)
    if($remaining -le 0 -or $elapsed -lt 0){throw 'WR_BUDGET_EXHAUSTED'}
    return [int][Math]::Min($Maximum,$remaining)
}
function Invoke-WRMetadata([string]$Operation,$Context) {
    # Read-only observations, not a command/argv escape hatch.
    Assert-WRActiveContext $Context
    $workdir=$Context.sourceRoot;$exe=(Get-WRTool $Context 'git').path
    $arguments=switch -CaseSensitive ($Operation){
        'Head' {@('--no-optional-locks','rev-parse','HEAD')}
        'Tree' {@('--no-optional-locks','rev-parse','HEAD^{tree}')}
        'Dirty' {@('--no-optional-locks','status','--porcelain=v1','--untracked-files=no')}
        'Tracked' {@('--no-optional-locks','ls-files','--cached','-z')}
        'UpstreamHead' {$workdir=Join-Path $Context.sourceRoot '.producer/vscode';@('--no-optional-locks','rev-parse','HEAD')}
        'VisualStudio' {$exe=(Get-WRTool $Context 'vswhere').path;@('-products','*','-version','[17.0,18.0)','-requires','Microsoft.VisualStudio.Component.VC.Tools.x86.x64','Microsoft.VisualStudio.Component.VC.Runtimes.x86.x64.Spectre','-format','json')}
        default {throw 'WR_METADATA_OPERATION'}
    }
    $binding=Get-BasicBinding $exe
    $description=[ordered]@{id=('metadata-'+$Operation+'-'+$Context.metadataSequence);toolBinding=$binding;arguments=@($arguments);workdir=$workdir;safeEnvironment=(Get-WRSafeEnvironment $Context 'Metadata' $exe);timeoutSeconds=(Get-WRRemainingSeconds $Context 30);passReadToken=$false}
    $Context.metadataSequence++
    $result=Invoke-WRBoundProcess $description $Context
    return $result.stdout
}
function Get-WRSafeEnvironment($Context,[string]$ID,[string]$Exe) {
    $environment=[ordered]@{}
    foreach($name in @('SystemRoot','WINDIR','COMSPEC','TEMP','TMP','USERPROFILE','HOMEDRIVE','HOMEPATH','APPDATA','LOCALAPPDATA','ProgramFiles','ProgramFiles(x86)','ProgramW6432','NUMBER_OF_PROCESSORS','PROCESSOR_ARCHITECTURE')){
        $value=[Environment]::GetEnvironmentVariable($name)
        if($null -ne $value){$environment[$name]=$value}
    }
    $environment.PATH=([IO.Path]::GetDirectoryName($Exe)+';'+(Join-Path $Context.executionRoot 'tooling/yarn')+';'+$Context.runnerPath)
    $environment.CI='true';$environment.GITHUB_ACTIONS='true'
    $environment.GIT_TERMINAL_PROMPT='0';$environment.GIT_CONFIG_NOSYSTEM='1';$environment.GIT_CONFIG_GLOBAL='NUL'
    $environment.GIT_CONFIG_COUNT='1';$environment.GIT_CONFIG_KEY_0='core.hooksPath';$environment.GIT_CONFIG_VALUE_0='/dev/null'
    $environment.npm_config_audit='false';$environment.npm_config_fund='false';$environment.npm_config_fetch_retries='0'
    if($ID -cin @('YarnDependencies','CodeOssBuild')){
        $environment.GYP_MSVS_VERSION='2022';$environment.npm_config_msvs_version='2022'
        $environment.SKIP_YARN_COREPACK_CHECK='1'
        $environment.npm_execpath=Join-Path $Context.executionRoot 'tooling/yarn/node_modules/yarn/bin/yarn.js'
    }
    if($ID -ceq 'RuntimeVersion'){$environment.ELECTRON_RUN_AS_NODE='1';$environment.VSCODE_DEV=''}
    # NODE_OPTIONS, ELECTRON_RUN_AS_NODE and all credentials otherwise absent.
    return $environment
}
function Get-WindowsResourceStep([string]$ID,$Context) {
    $allowed=@('FetchInit','FetchRemote','FetchCommit','FetchCheckout','Verify','YarnInstall','YarnDependencies','CodeOssBuild','RuntimeVersion','PnpmInstall','RepositoryDependencies','PowerShellModels','WorkflowTests','TarParserTests','CMake','Coverage','TestgenPrepare','TestgenCheck','ValidateRuntime','RuntimeInventory')
    if($ID -cnotin $allowed){throw 'WR_STEP'}
    $repo=$Context.sourceRoot;$execution=$Context.executionRoot
    $upstream=Join-Path $repo '.producer/vscode';$runtime=Join-Path $repo '.producer/VSCode-win32-x64'
    $yarn=Join-Path $execution 'tooling/yarn/node_modules/yarn/bin/yarn.js'
    $pnpm=Join-Path $execution 'tooling/pnpm/node_modules/pnpm/bin/pnpm.cjs'
    $role='node-repository';$workdir=$repo;$timeout=300
    $arguments=switch -CaseSensitive ($ID){
        'FetchInit' {$role='git';@('init',$upstream)}
        'FetchRemote' {$role='git';$workdir=$upstream;@('remote','add','origin','https://github.com/microsoft/vscode.git')}
        'FetchCommit' {$role='git';$workdir=$upstream;@('fetch','--depth=1','origin','b1c0a14de1414fcdaa400695b4db1c0799bc3124')}
        'FetchCheckout' {$role='git';$workdir=$upstream;@('checkout','--detach','FETCH_HEAD')}
        'Verify' {$timeout=120;@('tools/release/producer/source-manifest.mjs','verify-checkout','--manifest','tools/release/producer/source-manifest.json','--root',$upstream,'--actual-commit','b1c0a14de1414fcdaa400695b4db1c0799bc3124')}
        'YarnInstall' {$role='node-build';@($Context.npmBuild,'install','--global','--prefix',(Join-Path $execution 'tooling/yarn'),'--no-audit','--no-fund','yarn@1.22.22')}
        'YarnDependencies' {$role='node-build';$workdir=$upstream;$timeout=1200;@($yarn,'install','--frozen-lockfile')}
        'CodeOssBuild' {$role='node-build';$workdir=$upstream;$timeout=4500;@($yarn,'gulp','vscode-win32-x64')}
        'RuntimeVersion' {$role='runtime-launcher';$timeout=30;@((Join-Path $runtime 'resources/app/out/cli.js'),'--version','--user-data-dir',(Join-Path $execution 'version-user-data'))}
        'PnpmInstall' {@($Context.npmRepository,'install','--global','--prefix',(Join-Path $execution 'tooling/pnpm'),'--ignore-scripts','--no-audit','--no-fund','pnpm@11.4.0')}
        'RepositoryDependencies' {$timeout=900;@($pnpm,'install','--frozen-lockfile','--ignore-scripts')}
        'PowerShellModels' {$role='powershell';@('-NoProfile','-NonInteractive','-File','tools/release/producer/windows-resource-models.ps1','-RecordDirectory',(Join-Path $execution 'models'))}
        'WorkflowTests' {$timeout=120;@('--test','--test-reporter=tap','tools/release/producer/windows-resource-workflow.test.mjs')}
        'TarParserTests' {$timeout=120;@('--test','--test-reporter=tap','--test-name-pattern=^(archive listing parser accepts GNU tar and bsdtar metadata layouts|archive inspection rejects nested selected links and unlisted selected files before extraction)$','tools/testgen-bundle/prepare.test.mjs')}
        'CMake' {$timeout=900;@('tools/cmake-bundle/prepare.mjs')}
        'Coverage' {$timeout=900;@('tools/coverage-bundle/prepare.mjs')}
        'TestgenPrepare' {$timeout=1800;@('tools/testgen-bundle/prepare.mjs','--platform','windows-x64')}
        'TestgenCheck' {@('tools/testgen-bundle/check.mjs','--platform','windows-x64','--root',(Join-Path $repo '.superpowers/cache/testgen-bundle/22.1.8/windows-x64'))}
        'ValidateRuntime' {@('tools/release/code-oss-runtime.mjs','--platform','windows','--root',$runtime,'--launcher-sha256',$Context.launcherSha256)}
        'RuntimeInventory' {@('tools/release/producer/runtime-inventory.mjs','create','--platform','windows','--root',$runtime,'--launcher-sha256',$Context.launcherSha256,'--out',(Join-Path $execution 'runtime-inventory.json'),'--summary-out',(Join-Path $execution 'runtime-summary.json'))}
    }
    $tool=Get-WRTool $Context $role
    return [ordered]@{id=$ID;toolBinding=(Get-WRToolBinding $tool);arguments=@($arguments);workdir=$workdir;safeEnvironment=(Get-WRSafeEnvironment $Context $ID $tool.path);timeoutSeconds=$timeout;passReadToken=($ID -cin @('YarnDependencies','CodeOssBuild'))}
}
function Invoke-WRBoundProcess($Description,$Context) {
    Assert-WRActiveContext $Context
    Assert-WRCurrentBinding $Description.toolBinding
    [void](Assert-LocalPath $Description.workdir '.' $true)
    $seconds=Get-WRRemainingSeconds $Context $Description.timeoutSeconds
    $directory=Join-Path $Context.executionRoot ('native/'+$Description.id)
    if(Test-Path -LiteralPath $directory){throw 'WR_NATIVE_RETRY_FORBIDDEN'}
    [void](New-SafeDirectory $directory)
    Write-NewJson (Join-Path $directory 'command.json') ([ordered]@{id=$Description.id;toolBinding=$Description.toolBinding;arguments=$Description.arguments;workdir=$Description.workdir;safeEnvironment=$Description.safeEnvironment;timeoutSeconds=$seconds;automaticRetries=0})
    $info=[Diagnostics.ProcessStartInfo]::new()
    $info.FileName=$Description.toolBinding.path;$info.WorkingDirectory=$Description.workdir
    $info.UseShellExecute=$false;$info.CreateNoWindow=$true
    $info.RedirectStandardOutput=$true;$info.RedirectStandardError=$true
    $info.StandardOutputEncoding=[Text.UTF8Encoding]::new($false);$info.StandardErrorEncoding=[Text.UTF8Encoding]::new($false)
    foreach($argument in $Description.arguments){$info.ArgumentList.Add($argument)}
    $info.Environment.Clear()
    foreach($name in $Description.safeEnvironment.Keys){$info.Environment[$name]=[string]$Description.safeEnvironment[$name]}
    $secret=$null
    if($Description.passReadToken -and -not [string]::IsNullOrEmpty($env:WR_READ_TOKEN)){$secret=$env:WR_READ_TOKEN;$info.Environment['GITHUB_TOKEN']=$secret}
    $process=[Diagnostics.Process]::new();$process.StartInfo=$info
    $result=[ordered]@{rawExit=$null;pid=$null;capturedBeforeDispose=$false;outputComplete=$false;timedOut=$false;cancelled=$false;firstFailure=$null;cleanupFailure=$null}
    $stdout='';$stderr='';$outTask=$null;$errTask=$null;$started=$false
    $startedAt=[DateTimeOffset]::UtcNow.ToString('o')
    try {
        $started=$process.Start();if(-not $started){throw 'START_FALSE'}
        $result.pid=$process.Id
        $outTask=$process.StandardOutput.ReadToEndAsync();$errTask=$process.StandardError.ReadToEndAsync()
        if(-not $process.WaitForExit($seconds*1000)){$result.timedOut=$true;throw 'TIMEOUT'}
        $result.rawExit=$process.ExitCode;$result.capturedBeforeDispose=$true
        if(-not $outTask.Wait(5000) -or -not $errTask.Wait(5000)){throw 'OUTPUT_INCOMPLETE'}
        $stdout=$outTask.GetAwaiter().GetResult();$stderr=$errTask.GetAwaiter().GetResult();$result.outputComplete=$true
    } catch {
        $result.firstFailure=$_.Exception.Message
        if($_.Exception -is [Management.Automation.PipelineStoppedException]){$result.cancelled=$true}
    } finally {
        if($started -and -not $result.capturedBeforeDispose){
            try {
                if(-not $process.HasExited){$process.Kill($true)}
                if(-not $process.WaitForExit(15000)){throw 'OWNED_EXIT_UNCONFIRMED'}
                $result.rawExit=$process.ExitCode;$result.capturedBeforeDispose=$true
            } catch {$result.cleanupFailure=$_.Exception.Message}
        }
        if($null -ne $outTask -and $outTask.IsCompletedSuccessfully){$stdout=$outTask.GetAwaiter().GetResult()}
        if($null -ne $errTask -and $errTask.IsCompletedSuccessfully){$stderr=$errTask.GetAwaiter().GetResult()}
        try {$process.Dispose()}catch{$result.cleanupFailure='DISPOSE_FAILED '+$_.Exception.Message}
        if($null -ne $secret){
            # Never persist token values, their hashes, or common reversible encodings.
            $redactions=@($secret,[Uri]::EscapeDataString($secret),[Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes($secret)))
            foreach($redaction in $redactions){
                $stdout=$stdout.Replace($redaction,'[REDACTED]');$stderr=$stderr.Replace($redaction,'[REDACTED]')
                if($null -ne $result.firstFailure){$result.firstFailure=$result.firstFailure.Replace($redaction,'[REDACTED]')}
                if($null -ne $result.cleanupFailure){$result.cleanupFailure=$result.cleanupFailure.Replace($redaction,'[REDACTED]')}
            }
        }
        try {
            Write-NewJson (Join-Path $directory 'raw-output.json') ([ordered]@{stdout=$stdout;stderr=$stderr;secretRedactionApplied=($null -ne $secret)})
            Write-NewJson (Join-Path $directory 'raw-exit.json') ([ordered]@{schemaVersion=1;startedAtUtc=$startedAt;finishedAtUtc=[DateTimeOffset]::UtcNow.ToString('o');completion=$result;automaticRetries=0})
        } catch {$result.cleanupFailure=([string]$result.cleanupFailure+' LOG_WRITE_FAILED '+$_.Exception.Message).Trim()}
    }
    Assert-WRNativeCompletion $result
    Assert-WRCurrentBinding $Description.toolBinding
    return [ordered]@{stdout=$stdout;stderr=$stderr;completion=$result;directory=$directory}
}
function Invoke-WindowsResourceRecorded([string]$ID,$Context) {
    Assert-WRActiveContext $Context
    $stageIDs=@{PrepareRepositoryDependencies=@('PnpmInstall','RepositoryDependencies');VerifyChecks=@('PowerShellModels','WorkflowTests','TarParserTests');FetchSource=@('FetchInit','FetchRemote','FetchCommit','FetchCheckout');VerifySource=@('Verify');CodeOssDependencies=@('YarnInstall','YarnDependencies');BuildRuntime=@('CodeOssBuild');CheckRuntimeVersion=@('RuntimeVersion');PrepareBundles=@('CMake','Coverage','TestgenPrepare','TestgenCheck','ValidateRuntime','RuntimeInventory')}
    if(-not $stageIDs.ContainsKey($Context.activeStage) -or $ID -cnotin $stageIDs[$Context.activeStage]){throw 'WR_STAGE_STEP'}
    Assert-WRInputSnapshot $Context
    $description=Get-WindowsResourceStep $ID $Context
    $result=Invoke-WRBoundProcess $description $Context
    Assert-WRInputSnapshot $Context
    return $result
}
function Assert-WRInputSnapshot($Context) {
    Assert-WRActiveContext $Context
    Assert-WRSource $Context.source;Assert-WRExecution $Context.execution
    Assert-WRPolicy $Context.policy
    Assert-WRSame $Context.policy (Read-BasicJson (Join-Path $Context.sourceRoot 'tools/release/producer/windows-resource-policy.json')) 'WR_POLICY_DRIFT'
    Assert-WRSame $Context.policyBinding (Get-WRRelativeBinding $Context.sourceRoot 'tools/release/producer/windows-resource-policy.json') 'WR_POLICY_BINDING'
    Assert-WRSame (Get-WRInputPaths) @($Context.inputs | ForEach-Object path) 'WR_INPUT_COMPLETE_SET'
    foreach($inputBinding in $Context.inputs){
        Assert-WRSame $inputBinding (Get-WRRelativeBinding $Context.sourceRoot $inputBinding.path) 'WR_SOURCE_INPUT_DRIFT'
    }
    if((Invoke-WRMetadata 'Head' $Context).Trim() -cne $Context.source.commit -or
       (Invoke-WRMetadata 'Tree' $Context).Trim() -cne $Context.source.tree -or
       -not [string]::IsNullOrWhiteSpace((Invoke-WRMetadata 'Dirty' $Context))){throw 'WR_TRACKED_SOURCE_DRIFT'}
    Assert-WRSame $Context.trackedPaths @((Invoke-WRMetadata 'Tracked' $Context).Split([char]0,[StringSplitOptions]::RemoveEmptyEntries)) 'WR_TRACKED_SET_DRIFT'
    if($null -ne $Context.upstream){
        if((Invoke-WRMetadata 'UpstreamHead' $Context).Trim() -cne $Context.policy.codeOssCommit){throw 'WR_UPSTREAM_HEAD'}
        foreach($lock in $Context.upstream.lockBindings){Assert-WRSame $lock (Get-WRRelativeBinding (Join-Path $Context.sourceRoot '.producer/vscode') $lock.path) 'WR_UPSTREAM_LOCK_DRIFT'}
    }
    foreach($tool in $Context.tools){Assert-WRCurrentBinding (Get-WRToolBinding $tool)}
}
function Get-WRFixedRoots($Context) {
    return [ordered]@{
        runtime=(Join-Path $Context.sourceRoot '.producer/VSCode-win32-x64')
        cmake=(Join-Path $Context.sourceRoot '.bundled-tools/cmake/4.3.4/win32-x64/cmake-4.3.4-windows-x86_64')
        coverage=(Join-Path $Context.sourceRoot '.superpowers/runtime/coverage-bundle/windows-x64')
        testgen=(Join-Path $Context.sourceRoot '.superpowers/cache/testgen-bundle/22.1.8/windows-x64')
    }
}
function Set-WRNode($Context,[string]$Role,[string]$Version) {
    $node=(Get-Command node.exe -CommandType Application -ErrorAction Stop).Source
    $actual=(Get-Item -LiteralPath $node).VersionInfo.ProductVersion
    if($actual.Split(' ')[0].TrimStart('v') -cne $Version){throw 'WR_NODE_VERSION'}
    Add-WRTool $Context $Role $node $Version
    $npm=Join-Path ([IO.Path]::GetDirectoryName($node)) 'node_modules/npm/bin/npm-cli.js'
    Add-WRTool $Context ($Role+'-npm') $npm (Read-BasicJson (Join-Path ([IO.Path]::GetDirectoryName($node)) 'node_modules/npm/package.json')).version
    if($Role -ceq 'node-build'){$Context.npmBuild=$npm}else{$Context.npmRepository=$npm}
}
function Set-WRPackageTool($Context,[string]$Role,[string]$Version) {
    $root=Join-Path $Context.executionRoot ('tooling/'+$Role+'/node_modules/'+$Role)
    $package=Read-BasicJson (Join-Path $root 'package.json')
    if($package.name -cne $Role -or $package.version -cne $Version){throw 'WR_PACKAGE_TOOL_VERSION'}
    $entry=if($Role -ceq 'pnpm'){'bin/pnpm.cjs'}else{'bin/yarn.js'}
    Add-WRTool $Context $Role (Join-Path $root $entry) $Version
}
function Assert-WRTap([string]$Text,[string[]]$Expected) {
    $passed=[Collections.Generic.List[string]]::new()
    foreach($line in $Text.Split([char]10)){
        if($line -match '^not ok ' -or $line -match '^Bail out!'){throw 'WR_TAP_FAILED'}
        if($line -match '^ok [0-9]+ - (.+?)(?: # (SKIP|TODO).*)?$'){
            $testName=$Matches[1].TrimEnd([char]13)
            if($Matches.ContainsKey(2)){
                if($testName -cin $Expected -or $Matches[2] -ceq 'TODO'){throw 'WR_TAP_SELECTED_SKIPPED'}
            }else{$passed.Add($testName)}
        }
    }
    Assert-WRSame (Get-OrdinalStrings $Expected) (Get-OrdinalStrings $passed.ToArray()) 'WR_TAP_NAMES'
}
function New-WRContext([string]$ExpectedCommit) {
    if($ExpectedCommit -cnotmatch '^[0-9a-f]{40}$'){throw 'WR_INVOCATION'}
    if($env:GITHUB_ACTIONS -cne 'true' -or $env:RUNNER_OS -cne 'Windows' -or -not $IsWindows -or $PSVersionTable.PSVersion -lt [version]'7.4'){throw 'WR_RUNNER_CONTEXT_REQUIRED'}
    if($env:GITHUB_WORKSPACE -ne [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '../../..'))){throw 'WR_WORKSPACE'}
    $root=Assert-LocalPath $env:GITHUB_WORKSPACE '.' $true
    if($env:GITHUB_RUN_ID -cnotmatch '^[1-9][0-9]*$' -or $env:GITHUB_RUN_ATTEMPT -cnotmatch '^[1-9][0-9]*$'){throw 'WR_RUN_ID'}
    $executionRoot=Join-Path $root ('.release/windows-resources/'+$env:GITHUB_RUN_ID+'-'+$env:GITHUB_RUN_ATTEMPT)
    return [ordered]@{sourceRoot=$root;executionRoot=$executionRoot;startedAtUtc=[DateTimeOffset]::UtcNow.ToString('o');metadataSequence=0;activeStage='Preflight';runnerPath=$env:PATH;tools=@();inputs=@();source=$null;execution=$null;policy=$null;policyBinding=$null;trackedPaths=@();upstream=$null;npmBuild=$null;npmRepository=$null;launcherSha256=$null;roots=$null;completed=@();previousBinding=$null}
}
function Initialize-WRPreflight($Context,[string]$ExpectedCommit) {
    if(Test-Path -LiteralPath $Context.executionRoot){throw 'WR_OUTPUT_EXISTS'}
    if(Test-Path -LiteralPath (Join-Path $Context.sourceRoot '.producer')){throw 'WR_UPSTREAM_ROOT_EXISTS'}
    [void](New-SafeDirectory $Context.executionRoot)
    Add-WRTool $Context 'powershell' (Join-Path $PSHOME 'pwsh.exe') $PSVersionTable.PSVersion.ToString()
    $git=(Get-Command git.exe -CommandType Application -ErrorAction Stop).Source
    Add-WRTool $Context 'git' $git (Get-Item -LiteralPath $git).VersionInfo.ProductVersion
    $workflow='.github/workflows/windows-portable-resources.yml'
    $expectedWorkflowRef='colayc/unitTest/'+$workflow+'@refs/heads/master'
    if($env:GITHUB_SHA -cne $ExpectedCommit -or $env:GITHUB_WORKFLOW_REF -cne $expectedWorkflowRef){throw 'WR_INVOCATION'}
    $observed=[ordered]@{repository=$env:GITHUB_REPOSITORY;event=$env:GITHUB_EVENT_NAME;ref=$env:GITHUB_REF;workflowPath=$workflow;commit=(Invoke-WRMetadata 'Head' $Context).Trim();tree=(Invoke-WRMetadata 'Tree' $Context).Trim();workflowSha=$env:GITHUB_WORKFLOW_SHA;runId=$env:GITHUB_RUN_ID;runAttempt=[long]$env:GITHUB_RUN_ATTEMPT;trackedDirty=(-not [string]::IsNullOrWhiteSpace((Invoke-WRMetadata 'Dirty' $Context)))}
    Assert-WindowsResourceInvocation $observed $ExpectedCommit
    if($env:ImageOS -cne 'win22' -or $env:ImageVersion -cnotmatch '^[A-Za-z0-9._-]+$' -or
       $env:GITHUB_RETENTION_DAYS -cnotmatch '^[1-9][0-9]*$' -or [long]$env:GITHUB_RETENTION_DAYS -lt 7){throw 'WR_IMAGE_OR_RETENTION'}
    $Context.source=[ordered]@{repository=$observed.repository;commit=$observed.commit;tree=$observed.tree;state='CI_COMMITTED_SOURCE'}
    $Context.execution=[ordered]@{workflowPath=$workflow;ref=$observed.ref;event=$observed.event;runId=$observed.runId;runAttempt=$observed.runAttempt;runnerImage=$env:ImageVersion}
    $Context.trackedPaths=@((Invoke-WRMetadata 'Tracked' $Context).Split([char]0,[StringSplitOptions]::RemoveEmptyEntries))
    foreach($relative in (Get-WRInputPaths)){
        if($relative -cnotin $Context.trackedPaths){throw 'WR_INPUT_NOT_COMMITTED'}
        $Context.inputs+=@(Get-WRRelativeBinding $Context.sourceRoot $relative)
    }
    $Context.policyBinding=Get-WRRelativeBinding $Context.sourceRoot 'tools/release/producer/windows-resource-policy.json'
    $Context.policy=Read-BasicJson (Join-Path $Context.sourceRoot $Context.policyBinding.path)
    Assert-WRPolicy $Context.policy
    foreach($helper in @(@('manifest.ps1','a5031557ee082b05ea8b8107fbd42b4750adb4b2ff2b6c4ff912906c5bcd7410'),@('package.ps1','67e23a06cb39c362457c8949acea54458c8425d59cabdca5028b1cb6fc95e1f5'))){
        if((Get-BasicBinding (Join-Path $PSScriptRoot ('../portable/'+$helper[0]))).sha256 -cne $helper[1]){throw 'WR_HELPER_DRIFT'}
    }
    $vswhere=Join-Path ${env:ProgramFiles(x86)} 'Microsoft Visual Studio/Installer/vswhere.exe'
    Add-WRTool $Context 'vswhere' $vswhere (Get-Item -LiteralPath $vswhere).VersionInfo.ProductVersion
    $instances=@((Invoke-WRMetadata 'VisualStudio' $Context) | ConvertFrom-Json)
    if($instances.Count -ne 1 -or $instances[0].installationVersion -cnotmatch '^17\.' -or $instances[0].installationPath -isnot [string]){throw 'WR_VS2022_SPECTRE_REQUIRED'}
    $vsroot=Assert-LocalPath $instances[0].installationPath '.' $true
    Add-WRTool $Context 'msbuild' (Assert-LocalPath $vsroot 'MSBuild/Current/Bin/MSBuild.exe' $false) $instances[0].installationVersion
    $tar=(Get-Command tar.exe -CommandType Application -ErrorAction Stop).Source
    Add-WRTool $Context 'tar' $tar (Get-Item -LiteralPath $tar).VersionInfo.FileVersion
    Write-NewJson (Join-Path $Context.executionRoot 'retention-preflight.json') ([ordered]@{defaultDays=[long]$env:GITHUB_RETENTION_DAYS;requiredDays=7;repositorySettingProof='REQUIRED_BEFORE_DISPATCH';artifactActualLifetime='REQUIRED_BEFORE_APPROVAL'})
    Assert-WRInputSnapshot $Context
}
function Read-WRStageContext($Fresh,[string]$Stage,[string]$ExpectedCommit) {
    $stages=Get-WRStages;$completed=[Collections.Generic.List[string]]::new();$context=$null;$previous=$null
    foreach($stageName in $stages){
        $path=Join-Path $Fresh.executionRoot ('stages/'+$completed.Count.ToString('D2')+'-'+$stageName+'.json')
        if(-not (Test-Path -LiteralPath $path)){break}
        $read=Read-BasicBoundJson $path
        Assert-WRKeys $read.value @('schemaVersion','stage','previousBinding','context')
        if($read.value.schemaVersion -ne 1 -or $read.value.stage -cne $stageName){throw 'WR_STAGE_RECEIPT'}
        Assert-WRSame $previous $read.value.previousBinding 'WR_STAGE_CHAIN'
        $context=$read.value.context;$previous=$read.binding;$completed.Add($stageName)
    }
    if($null -eq $context){throw 'WR_PREFLIGHT_REQUIRED'}
    Assert-WRNextStage $completed.ToArray() $Stage
    if($context.sourceRoot -cne $Fresh.sourceRoot -or $context.executionRoot -cne $Fresh.executionRoot -or
       $context.source.commit -cne $ExpectedCommit -or $context.source.repository -cne $env:GITHUB_REPOSITORY -or
       $context.execution.runId -cne $env:GITHUB_RUN_ID -or $context.execution.runAttempt -ne [long]$env:GITHUB_RUN_ATTEMPT -or
       $env:GITHUB_SHA -cne $ExpectedCommit -or $env:GITHUB_WORKFLOW_SHA -cne $ExpectedCommit -or
       $env:GITHUB_EVENT_NAME -cne 'workflow_dispatch' -or $env:GITHUB_REF -cne 'refs/heads/master' -or
       $env:GITHUB_WORKFLOW_REF -cne 'colayc/unitTest/.github/workflows/windows-portable-resources.yml@refs/heads/master'){throw 'WR_REOBSERVED_CONTEXT'}
    $context.completed=@($completed.ToArray());$context.previousBinding=$previous;$context.activeStage=$Stage
    return $context
}
function Write-WRStageReceipt($Context,[string]$Stage) {
    $path=Join-Path $Context.executionRoot ('stages/'+$Context.completed.Count.ToString('D2')+'-'+$Stage+'.json')
    [void](New-SafeDirectory ([IO.Path]::GetDirectoryName($path)))
    Write-NewJson $path ([ordered]@{schemaVersion=1;stage=$Stage;previousBinding=$Context.previousBinding;context=$Context})
}
function Write-WROutput([string]$Name,[string]$Value) {
    if([string]::IsNullOrWhiteSpace($env:GITHUB_OUTPUT) -or $Value.Contains([char]10) -or $Value.Contains([char]13)){throw 'WR_OUTPUT_CHANNEL'}
    [void](Assert-AbsoluteLocalPath $env:GITHUB_OUTPUT)
    [IO.File]::AppendAllText($env:GITHUB_OUTPUT,($Name+'='+$Value+[Environment]::NewLine),[Text.UTF8Encoding]::new($false))
}
function New-WRFailureReceipt($Context,[string]$Code) {
    $directory=Join-Path $Context.executionRoot 'failure'
    if(Test-Path -LiteralPath $directory){return Get-BasicBinding (Join-Path $directory 'failure.json')}
    [void](New-SafeDirectory $directory)
    Write-NewJson (Join-Path $directory 'failure.json') ([ordered]@{schemaVersion=1;state='FAILED_RESOURCE_PRODUCTION';stage=$Context.activeStage;failure=$Code;automaticRetries=0;generationStatus='PAUSED_RUNTIME_DISABLED';signingStatus='UNSIGNED_TEST_ONLY'})
    $native=Join-Path $Context.executionRoot 'native'
    if(Test-Path -LiteralPath $native){
        foreach($relative in (Get-TreeFilePaths $native)){
            if($relative -cnotmatch '^(?:metadata-(?:Head|Tree|Dirty|Tracked|UpstreamHead|VisualStudio)-[0-9]+|FetchInit|FetchRemote|FetchCommit|FetchCheckout|Verify|YarnInstall|YarnDependencies|CodeOssBuild|RuntimeVersion|PnpmInstall|RepositoryDependencies|PowerShellModels|WorkflowTests|TarParserTests|CMake|Coverage|TestgenPrepare|TestgenCheck|ValidateRuntime|RuntimeInventory)/(?:command|raw-output|raw-exit)\.json$'){throw 'WR_FAILURE_LOG_SET'}
            $binding=Get-WRRelativeBinding $native $relative;$target=Join-Path $directory ('native/'+$relative)
            [void](New-SafeDirectory ([IO.Path]::GetDirectoryName($target)))
            [void](Copy-BoundFile (Join-Path $native $relative) $target $binding.size $binding.sha256)
        }
    }
    return Get-BasicBinding (Join-Path $directory 'failure.json')
}
function Invoke-WindowsResourceStage([string]$Stage,[string]$ExpectedCommit) {
    Assert-WRStageName $Stage
    $ErrorActionPreference='Stop'
    $context=New-WRContext $ExpectedCommit
    if($Stage -ceq 'FailureReceipt'){
        $directory=Join-Path $context.executionRoot 'failure'
        if(-not (Test-Path -LiteralPath (Join-Path $directory 'failure.json'))){
            # Action/setup/upload failure outside a Stage has no native exit to invent.
            $preflight=Join-Path $context.executionRoot 'stages/00-Preflight.json'
            if(-not (Test-Path -LiteralPath $preflight)){throw 'WR_FAILURE_RECEIPT_UNAVAILABLE'}
            $context.activeStage='ExternalWorkflowStep'
            [void](New-WRFailureReceipt $context 'EXTERNAL_WORKFLOW_STEP_FAILED_DETAILS_IN_ACTION_LOG')
        }
        Write-WROutput 'failure_directory' $directory
        return Get-BasicBinding (Join-Path $directory 'failure.json')
    }
    if(Test-Path -LiteralPath (Join-Path $context.executionRoot 'failure')){throw 'WR_FIRST_FAILURE_STOP'}
    if($Stage -cne 'Preflight'){$context=Read-WRStageContext $context $Stage $ExpectedCommit}
    $script:WRActiveContext=$context
    try {
        if($Stage -ceq 'Preflight'){Initialize-WRPreflight $context $ExpectedCommit}
        else {
            Assert-WRInputSnapshot $context
            switch -CaseSensitive ($Stage){
                'PrepareRepositoryDependencies' {
                    Set-WRNode $context 'node-repository' '24.18.0'
                    [void](Invoke-WindowsResourceRecorded 'PnpmInstall' $context)
                    Set-WRPackageTool $context 'pnpm' '11.4.0'
                    [void](Invoke-WindowsResourceRecorded 'RepositoryDependencies' $context)
                }
                'VerifyChecks' {
                    Set-WRNode $context 'node-repository' '24.18.0'
                    $model=Invoke-WindowsResourceRecorded 'PowerShellModels' $context
                    $result=ConvertFrom-BasicJsonBytes ([Text.Encoding]::UTF8.GetBytes($model.stdout))
                    Assert-WRModelResult $result
                    $workflow=Invoke-WindowsResourceRecorded 'WorkflowTests' $context
                    Assert-WRTap $workflow.stdout @('resource workflow is manual Windows read-only','resource workflow publishes payload before origin')
                    $tar=Invoke-WindowsResourceRecorded 'TarParserTests' $context
                    Assert-WRTap $tar.stdout @('archive listing parser accepts GNU tar and bsdtar metadata layouts','archive inspection rejects nested selected links and unlisted selected files before extraction')
                }
                'FetchSource' {foreach($id in @('FetchInit','FetchRemote','FetchCommit','FetchCheckout')){[void](Invoke-WindowsResourceRecorded $id $context)}}
                'VerifySource' {
                    if((Invoke-WRMetadata 'UpstreamHead' $context).Trim() -cne $context.policy.codeOssCommit){throw 'WR_UPSTREAM_HEAD'}
                    [void](Invoke-WindowsResourceRecorded 'Verify' $context)
                    $context.upstream=[ordered]@{repository='microsoft/vscode';commit=$context.policy.codeOssCommit;version='1.92.0';target='vscode-win32-x64';lockBindings=@(foreach($relative in @('.nvmrc','.yarnrc','package.json','yarn.lock','build/gulpfile.vscode.js')){Get-WRRelativeBinding (Join-Path $context.sourceRoot '.producer/vscode') $relative})}
                }
                'CodeOssDependencies' {
                    Set-WRNode $context 'node-build' '20.14.0'
                    [void](Invoke-WindowsResourceRecorded 'YarnInstall' $context)
                    Set-WRPackageTool $context 'yarn' '1.22.22'
                    [void](Invoke-WindowsResourceRecorded 'YarnDependencies' $context)
                }
                'BuildRuntime' {
                    Set-WRNode $context 'node-build' '20.14.0'
                    [void](Invoke-WindowsResourceRecorded 'CodeOssBuild' $context)
                    Add-WRTool $context 'runtime-launcher' (Join-Path $context.sourceRoot '.producer/VSCode-win32-x64/Code - OSS.exe') '1.92.0'
                    $context.launcherSha256=(Get-WRTool $context 'runtime-launcher').sha256
                }
                'CheckRuntimeVersion' {
                    $output=Invoke-WindowsResourceRecorded 'RuntimeVersion' $context
                    Assert-WRSame @('1.92.0','b1c0a14de1414fcdaa400695b4db1c0799bc3124','x64') @($output.stdout.Split([char]10) | ForEach-Object {$_.TrimEnd([char]13)} | Where-Object {$_ -ne ''}) 'WR_RUNTIME_VERSION'
                }
                'PrepareBundles' {
                    Set-WRNode $context 'node-repository' '24.18.0'
                    Set-WRPackageTool $context 'pnpm' '11.4.0'
                    $cmakeOutput=Invoke-WindowsResourceRecorded 'CMake' $context
                    $cmake=ConvertFrom-BasicJsonBytes ([Text.Encoding]::UTF8.GetBytes($cmakeOutput.stdout))
                    Assert-WRKeys $cmake @('key','cmakeVersion','root','executable','ctestExecutable','reused')
                    if($cmake.key -cne 'win32-x64' -or $cmake.cmakeVersion -cne '4.3.4'){throw 'WR_CMAKE_RESULT'}
                    $cmakeRoot=[IO.Path]::GetDirectoryName([IO.Path]::GetDirectoryName($cmake.executable))
                    $expectedCMakeRoot=Join-Path $context.sourceRoot '.bundled-tools/cmake/4.3.4/win32-x64/cmake-4.3.4-windows-x86_64'
                    if($cmakeRoot -cne $expectedCMakeRoot -or $cmake.executable -cne (Join-Path $cmakeRoot 'bin/cmake.exe') -or $cmake.ctestExecutable -cne (Join-Path $cmakeRoot 'bin/ctest.exe')){throw 'WR_CMAKE_ROOT'}
                    foreach($id in @('Coverage','TestgenPrepare','TestgenCheck','ValidateRuntime','RuntimeInventory')){[void](Invoke-WindowsResourceRecorded $id $context)}
                    $context.roots=[ordered]@{runtime=(Join-Path $context.sourceRoot '.producer/VSCode-win32-x64');cmake=$cmakeRoot;coverage=(Join-Path $context.sourceRoot '.superpowers/runtime/coverage-bundle/windows-x64');testgen=(Join-Path $context.sourceRoot '.superpowers/cache/testgen-bundle/22.1.8/windows-x64')}
                    $summary=Read-BasicJson (Join-Path $context.executionRoot 'runtime-summary.json')
                    Assert-WRKeys $summary @('schemaVersion','platform','architecture','launcherRelativePath','launcherSha256','fileCount','totalBytes','treeDigest')
                    if($summary.launcherSha256 -cne $context.launcherSha256 -or $summary.platform -cne 'windows' -or $summary.fileCount -le 0 -or $summary.totalBytes -le 0){throw 'WR_RUNTIME_SUMMARY'}
                }
                'PackageResources' {
                    $resultBinding=New-WindowsResourcePayload $context.sourceRoot $context.executionRoot $context $context.roots
                    $packageResult=Read-WRPackageResult $resultBinding $context
                    Write-WROutput 'result_path' $resultBinding.path
                    Write-WROutput 'result_size' $resultBinding.size.ToString([Globalization.CultureInfo]::InvariantCulture)
                    Write-WROutput 'result_sha256' $resultBinding.sha256
                    Write-WROutput 'payload_directory' $packageResult.payloadDirectory
                }
                'WriteOrigin' {
                    if($env:WR_RESULT_SIZE -cnotmatch '^[1-9][0-9]*$'){throw 'WR_RESULT_OUTPUTS'}
                    $binding=[ordered]@{path=$env:WR_RESULT_PATH;size=[long]$env:WR_RESULT_SIZE;sha256=$env:WR_RESULT_SHA256}
                    $upload=[ordered]@{id=$env:WR_PAYLOAD_ARTIFACT_ID;name=('windows-portable-resources-'+$context.execution.runId+'-'+$context.execution.runAttempt);digest=(ConvertTo-WRDigest $env:WR_PAYLOAD_ARTIFACT_DIGEST)}
                    $origin=New-WindowsResourceOrigin $binding $upload $context
                    Write-WROutput 'origin_path' $origin.path
                }
            }
            Assert-WRInputSnapshot $context
        }
        Write-WRStageReceipt $context $Stage
        if($Stage -ceq 'PackageResources'){return $packageResult}
    } catch {
        $firstCause=$_.Exception.Message
        try {[void](New-WRFailureReceipt $context $firstCause)}catch{throw ('WR_STAGE_FAILED '+$firstCause+' | failure receipt: '+$_.Exception.Message)}
        throw ('WR_STAGE_FAILED '+$firstCause)
    } finally {$script:WRActiveContext=$null}
}
