import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { readFile } from "node:fs/promises";
import { resolve } from "node:path";
import test from "node:test";

const workflowPath = resolve(".github/workflows/release-inputs.yml");
const workflow = await readFile(workflowPath, "utf8").then(
  (value) => value.replace(/\r\n?/gu, "\n"),
  (error) => {
    if (error?.code === "ENOENT") assert.fail("trusted release-input producer workflow is missing");
    throw error;
  },
);
const foundationWorkflow = (await readFile(resolve(".github/workflows/foundation.yml"), "utf8"))
  .replace(/\r\n?/gu, "\n");
const packageJson = JSON.parse(await readFile(resolve("package.json"), "utf8"));

const actionPins = Object.freeze({
  "actions/checkout": "d23441a48e516b6c34aea4fa41551a30e30af803",
  "actions/setup-node": "249970729cb0ef3589644e2896645e5dc5ba9c38",
  "actions/upload-artifact": "043fb46d1a93c77aae656e7c1c64a875d1fc6a0a",
  "actions/download-artifact": "d3f86a106a0bac45b974a628896c90dbdf5c8093",
});
const pwsh = process.env.PWSH?.trim() || "pwsh";
const visualStudioAstInspector = String.raw`
$ErrorActionPreference = 'Stop'
$sourceBytes = [Convert]::FromBase64String($env:UNIT_TEST_IDE_VS_PREFLIGHT_SOURCE_B64)
$source = [Text.Encoding]::UTF8.GetString($sourceBytes)
$tokens = $null
$parseErrors = $null
$ast = [System.Management.Automation.Language.Parser]::ParseInput($source, [ref]$tokens, [ref]$parseErrors)

function Get-SingleExpression([System.Management.Automation.Language.Ast] $node) {
  while ($true) {
    if ($node -is [System.Management.Automation.Language.PipelineAst]) {
      if ($node.PipelineElements.Count -ne 1) { return $null }
      $node = $node.PipelineElements[0]
      continue
    }
    if ($node -is [System.Management.Automation.Language.CommandExpressionAst]) {
      $node = $node.Expression
      continue
    }
    if ($node -is [System.Management.Automation.Language.ParenExpressionAst]) {
      $node = $node.Pipeline
      continue
    }
    return $node
  }
}

function Test-Variable([System.Management.Automation.Language.Ast] $node, [string] $name) {
  return (
    $node -is [System.Management.Automation.Language.VariableExpressionAst] -and
    $node.VariablePath.UserPath -ceq $name
  )
}

function Test-VariableMember(
  [System.Management.Automation.Language.Ast] $node,
  [string] $variable,
  [string] $member
) {
  return (
    $node -is [System.Management.Automation.Language.MemberExpressionAst] -and
    -not $node.Static -and
    $node.Member.Value -ceq $member -and
    (Test-Variable $node.Expression $variable)
  )
}

function Test-InstancesMember(
  [System.Management.Automation.Language.Ast] $node,
  [string] $member
) {
  if (
    $node -isnot [System.Management.Automation.Language.MemberExpressionAst] -or
    $node.Static -or
    $node.Member.Value -cne $member
  ) {
    return $false
  }
  $index = $node.Expression
  return (
    $index -is [System.Management.Automation.Language.IndexExpressionAst] -and
    (Test-Variable $index.Target 'instances') -and
    $index.Index -is [System.Management.Automation.Language.ConstantExpressionAst] -and
    $index.Index.Value -eq 0
  )
}

function Test-ReparsePointMember([System.Management.Automation.Language.Ast] $node) {
  return (
    $node -is [System.Management.Automation.Language.MemberExpressionAst] -and
    $node.Static -and
    $node.Member.Value -ceq 'ReparsePoint' -and
    $node.Expression -is [System.Management.Automation.Language.TypeExpressionAst] -and
    $node.Expression.TypeName.FullName -ceq 'IO.FileAttributes'
  )
}

function Test-StringType([System.Management.Automation.Language.Ast] $node) {
  return (
    $node -is [System.Management.Automation.Language.TypeExpressionAst] -and
    $node.TypeName.FullName -ceq 'string'
  )
}

function Test-DirectFailureThrow([System.Management.Automation.Language.StatementBlockAst] $body) {
  if ($null -eq $body -or $body.Traps.Count -ne 0 -or $body.Statements.Count -ne 1) { return $false }
  $throw = $body.Statements[0]
  if ($throw -isnot [System.Management.Automation.Language.ThrowStatementAst]) { return $false }
  return Test-Variable (Get-SingleExpression $throw.Pipeline) 'failure'
}

function Test-InstallationAssignment([System.Management.Automation.Language.Ast] $node) {
  if ($node -isnot [System.Management.Automation.Language.AssignmentStatementAst]) { return $false }
  if ($node.Operator -ne [System.Management.Automation.Language.TokenKind]::Equals) { return $false }
  if (-not (Test-Variable $node.Left 'installationInfo')) { return $false }
  if (
    $node.Right -isnot [System.Management.Automation.Language.PipelineAst] -or
    $node.Right.Background -or
    $node.Right.PipelineElements.Count -ne 1
  ) {
    return $false
  }
  $command = $node.Right.PipelineElements[0]
  if ($command -isnot [System.Management.Automation.Language.CommandAst] -or $command.Redirections.Count -ne 0) {
    return $false
  }
  $elements = $command.CommandElements
  return (
    $command.GetCommandName() -ceq 'Get-Item' -and
    $elements.Count -eq 4 -and
    $elements[1] -is [System.Management.Automation.Language.CommandParameterAst] -and
    $elements[1].ParameterName -ceq 'LiteralPath' -and
    (Test-InstancesMember $elements[2] 'installationPath') -and
    $elements[3] -is [System.Management.Automation.Language.CommandParameterAst] -and
    $elements[3].ParameterName -ceq 'Force'
  )
}

function Test-InstancesGuard([System.Management.Automation.Language.Ast] $node) {
  if ($node -isnot [System.Management.Automation.Language.IfStatementAst]) { return $false }
  if ($node.Clauses.Count -ne 1 -or $null -ne $node.ElseClause) { return $false }
  if (-not (Test-DirectFailureThrow $node.Clauses[0].Item2)) { return $false }
  $outerOr = Get-SingleExpression $node.Clauses[0].Item1
  if (
    $outerOr -isnot [System.Management.Automation.Language.BinaryExpressionAst] -or
    $outerOr.Operator -ne [System.Management.Automation.Language.TokenKind]::Or
  ) {
    return $false
  }
  $thirdOr = Get-SingleExpression $outerOr.Left
  $installationPathTerm = Get-SingleExpression $outerOr.Right
  if (
    $thirdOr -isnot [System.Management.Automation.Language.BinaryExpressionAst] -or
    $thirdOr.Operator -ne [System.Management.Automation.Language.TokenKind]::Or
  ) {
    return $false
  }
  $secondOr = Get-SingleExpression $thirdOr.Left
  $versionPatternTerm = Get-SingleExpression $thirdOr.Right
  if (
    $secondOr -isnot [System.Management.Automation.Language.BinaryExpressionAst] -or
    $secondOr.Operator -ne [System.Management.Automation.Language.TokenKind]::Or
  ) {
    return $false
  }
  $countTerm = Get-SingleExpression $secondOr.Left
  $versionTypeTerm = Get-SingleExpression $secondOr.Right
  return (
    $countTerm -is [System.Management.Automation.Language.BinaryExpressionAst] -and
    $countTerm.Operator -eq [System.Management.Automation.Language.TokenKind]::Ine -and
    (Test-VariableMember $countTerm.Left 'instances' 'Count') -and
    $countTerm.Right -is [System.Management.Automation.Language.ConstantExpressionAst] -and
    $countTerm.Right.Value -eq 1 -and
    $versionTypeTerm -is [System.Management.Automation.Language.BinaryExpressionAst] -and
    $versionTypeTerm.Operator -eq [System.Management.Automation.Language.TokenKind]::IsNot -and
    (Test-InstancesMember $versionTypeTerm.Left 'installationVersion') -and
    (Test-StringType $versionTypeTerm.Right) -and
    $versionPatternTerm -is [System.Management.Automation.Language.BinaryExpressionAst] -and
    $versionPatternTerm.Operator -eq [System.Management.Automation.Language.TokenKind]::Cnotmatch -and
    (Test-InstancesMember $versionPatternTerm.Left 'installationVersion') -and
    $versionPatternTerm.Right -is [System.Management.Automation.Language.StringConstantExpressionAst] -and
    $versionPatternTerm.Right.StringConstantType -eq [System.Management.Automation.Language.StringConstantType]::SingleQuoted -and
    $versionPatternTerm.Right.Value -ceq '^17\.' -and
    $installationPathTerm -is [System.Management.Automation.Language.BinaryExpressionAst] -and
    $installationPathTerm.Operator -eq [System.Management.Automation.Language.TokenKind]::IsNot -and
    (Test-InstancesMember $installationPathTerm.Left 'installationPath') -and
    (Test-StringType $installationPathTerm.Right)
  )
}

function Test-InstallationGuard([System.Management.Automation.Language.Ast] $node) {
  if ($node -isnot [System.Management.Automation.Language.IfStatementAst]) { return $false }
  if ($node.Clauses.Count -ne 1 -or $null -ne $node.ElseClause) { return $false }
  if (-not (Test-DirectFailureThrow $node.Clauses[0].Item2)) { return $false }
  $condition = Get-SingleExpression $node.Clauses[0].Item1
  if (
    $condition -isnot [System.Management.Automation.Language.BinaryExpressionAst] -or
    $condition.Operator -ne [System.Management.Automation.Language.TokenKind]::Or
  ) {
    return $false
  }
  $containerGuard = Get-SingleExpression $condition.Left
  if (
    $containerGuard -isnot [System.Management.Automation.Language.UnaryExpressionAst] -or
    $containerGuard.TokenKind -ne [System.Management.Automation.Language.TokenKind]::Not -or
    -not (Test-VariableMember $containerGuard.Child 'installationInfo' 'PSIsContainer')
  ) {
    return $false
  }
  $reparseGuard = Get-SingleExpression $condition.Right
  return (
    $reparseGuard -is [System.Management.Automation.Language.BinaryExpressionAst] -and
    $reparseGuard.Operator -eq [System.Management.Automation.Language.TokenKind]::Band -and
    (Test-VariableMember $reparseGuard.Left 'installationInfo' 'Attributes') -and
    (Test-ReparsePointMember $reparseGuard.Right)
  )
}

function Test-VswhereGuard([System.Management.Automation.Language.IfStatementAst] $node) {
  if ($null -eq $node -or $node.Clauses.Count -ne 1) { return $false }
  if (-not (Test-DirectFailureThrow $node.ElseClause)) { return $false }
  $condition = Get-SingleExpression $node.Clauses[0].Item1
  if (
    $condition -isnot [System.Management.Automation.Language.BinaryExpressionAst] -or
    $condition.Operator -ne [System.Management.Automation.Language.TokenKind]::And
  ) {
    return $false
  }
  $containerGuard = Get-SingleExpression $condition.Left
  $reparseNot = Get-SingleExpression $condition.Right
  if (
    $containerGuard -isnot [System.Management.Automation.Language.UnaryExpressionAst] -or
    $containerGuard.TokenKind -ne [System.Management.Automation.Language.TokenKind]::Not -or
    -not (Test-VariableMember $containerGuard.Child 'vswhereInfo' 'PSIsContainer') -or
    $reparseNot -isnot [System.Management.Automation.Language.UnaryExpressionAst] -or
    $reparseNot.TokenKind -ne [System.Management.Automation.Language.TokenKind]::Not
  ) {
    return $false
  }
  $reparseGuard = Get-SingleExpression $reparseNot.Child
  return (
    $reparseGuard -is [System.Management.Automation.Language.BinaryExpressionAst] -and
    $reparseGuard.Operator -eq [System.Management.Automation.Language.TokenKind]::Band -and
    (Test-VariableMember $reparseGuard.Left 'vswhereInfo' 'Attributes') -and
    (Test-ReparsePointMember $reparseGuard.Right)
  )
}

function Test-ForbiddenAncestor(
  [System.Management.Automation.Language.Ast] $node,
  [System.Management.Automation.Language.IfStatementAst] $expectedIf
) {
  $current = $node.Parent
  while ($null -ne $current) {
    if (
      $current -is [System.Management.Automation.Language.CatchClauseAst] -or
      $current -is [System.Management.Automation.Language.TrapStatementAst] -or
      $current -is [System.Management.Automation.Language.FunctionDefinitionAst] -or
      $current -is [System.Management.Automation.Language.ScriptBlockExpressionAst] -or
      $current -is [System.Management.Automation.Language.LoopStatementAst] -or
      ($current -is [System.Management.Automation.Language.IfStatementAst] -and
        -not [object]::ReferenceEquals($current, $expectedIf))
    ) {
      return $true
    }
    $current = $current.Parent
  }
  return $false
}

function Test-ExpectedPreflightBlock([System.Management.Automation.Language.StatementBlockAst] $block) {
  if ($null -eq $block -or $block.Traps.Count -ne 0) { return $false }
  $outerGuard = $block.Parent
  if (
    $outerGuard -isnot [System.Management.Automation.Language.IfStatementAst] -or
    -not [object]::ReferenceEquals($outerGuard.Clauses[0].Item2, $block) -or
    -not (Test-VswhereGuard $outerGuard) -or
    (Test-ForbiddenAncestor $block $outerGuard)
  ) {
    return $false
  }
  $tryBody = $outerGuard.Parent
  if ($tryBody -isnot [System.Management.Automation.Language.StatementBlockAst]) { return $false }
  $tryStatement = $tryBody.Parent
  return (
    $tryStatement -is [System.Management.Automation.Language.TryStatementAst] -and
    [object]::ReferenceEquals($tryStatement.Body, $tryBody) -and
    $tryStatement.Parent -is [System.Management.Automation.Language.NamedBlockAst]
  )
}

function Get-DirectStatementIndex(
  [System.Management.Automation.Language.StatementBlockAst] $block,
  [System.Management.Automation.Language.Ast] $statement
) {
  for ($index = 0; $index -lt $block.Statements.Count; $index += 1) {
    if ([object]::ReferenceEquals($block.Statements[$index], $statement)) { return $index }
  }
  return -1
}

$assignments = @($ast.FindAll({ param($node) Test-InstallationAssignment $node }, $true))
$instancesGuards = @($ast.FindAll({ param($node) Test-InstancesGuard $node }, $true))
$installationGuards = @($ast.FindAll({ param($node) Test-InstallationGuard $node }, $true))
$linearPreflightCount = 0
$earlySuccessTerminationCount = 0
if ($instancesGuards.Count -eq 1) {
  $tripleStart = $instancesGuards[0].Extent.StartOffset
  $earlySuccessTerminationCount = @($ast.EndBlock.FindAll({
    param($node)
    (
      $node -is [System.Management.Automation.Language.ReturnStatementAst] -or
      $node -is [System.Management.Automation.Language.ExitStatementAst]
    ) -and $node.Extent.StartOffset -lt $tripleStart
  }, $true)).Count
}
if ($assignments.Count -eq 1 -and $instancesGuards.Count -eq 1 -and $installationGuards.Count -eq 1) {
  $block = $assignments[0].Parent
  if (
    $block -is [System.Management.Automation.Language.StatementBlockAst] -and
    [object]::ReferenceEquals($instancesGuards[0].Parent, $block) -and
    [object]::ReferenceEquals($installationGuards[0].Parent, $block) -and
    (Test-ExpectedPreflightBlock $block)
  ) {
    $instancesIndex = Get-DirectStatementIndex $block $instancesGuards[0]
    $assignmentIndex = Get-DirectStatementIndex $block $assignments[0]
    $installationIndex = Get-DirectStatementIndex $block $installationGuards[0]
    if (
      $instancesIndex -ge 0 -and
      $assignmentIndex -eq ($instancesIndex + 1) -and
      $installationIndex -eq ($assignmentIndex + 1)
    ) {
      $linearPreflightCount = 1
    }
  }
}
$result = [ordered]@{
  parseErrorCount = $parseErrors.Count
  assignmentStatementCount = $assignments.Count
  instancesGuardStatementCount = $instancesGuards.Count
  installationGuardStatementCount = $installationGuards.Count
  linearPreflightCount = $linearPreflightCount
  earlySuccessTerminationCount = $earlySuccessTerminationCount
}
[Console]::Out.Write(($result | ConvertTo-Json -Compress))
`;

function topLevelSectionFrom(source, name) {
  const lines = source.split("\n");
  const start = lines.findIndex((line) => line === `${name}:`);
  assert.notEqual(start, -1, `missing top-level ${name} section`);
  let end = lines.length;
  for (let index = start + 1; index < lines.length; index += 1) {
    if (/^[A-Za-z][A-Za-z0-9_-]*:\s*$/u.test(lines[index])) {
      end = index;
      break;
    }
  }
  return lines.slice(start, end).join("\n").trimEnd();
}

function topLevelSection(name) {
  return topLevelSectionFrom(workflow, name);
}

const jobsSource = topLevelSection("jobs");

function jobBlock(name) {
  return jobBlockFrom(jobsSource, name);
}

function jobBlockFrom(jobs, name) {
  const lines = jobs.split("\n");
  const start = lines.findIndex((line) => line === `  ${name}:`);
  assert.notEqual(start, -1, `missing ${name} job`);
  let end = lines.length;
  for (let index = start + 1; index < lines.length; index += 1) {
    if (/^  [a-z][a-z0-9-]*:\s*$/u.test(lines[index])) {
      end = index;
      break;
    }
  }
  return lines.slice(start, end).join("\n");
}

function foundationJobBlock(name) {
  return jobBlockFrom(topLevelSectionFrom(foundationWorkflow, "jobs"), name);
}

function stepBlocks(job) {
  const lines = job.split("\n");
  const starts = [];
  for (let index = 0; index < lines.length; index += 1) {
    if (/^      - (?:id|name|uses):/u.test(lines[index])) starts.push(index);
  }
  return starts.map((start, index) => lines.slice(start, starts[index + 1] ?? lines.length).join("\n"));
}

function stepHeaderValue(step, key) {
  return step.match(new RegExp(`^      - ${key}:\\s*([^\\n#]+?)\\s*(?:#.*)?$`, "mu"))?.[1];
}

function namedStep(job, name) {
  const step = stepBlocks(job).find((candidate) => candidate.startsWith(`      - name: ${name}\n`));
  assert.ok(step, `missing step: ${name}`);
  return step;
}

function identifiedStep(job, id) {
  const step = stepBlocks(job).find((candidate) => candidate.match(new RegExp(`^      - id: ${id}$`, "mu")));
  assert.ok(step, `missing step id: ${id}`);
  return step;
}

function directMappingValue(source, indentation, key) {
  const prefix = " ".repeat(indentation);
  return source.match(new RegExp(`^${prefix}${key}:\\s*([^\\n#]+?)\\s*(?:#.*)?$`, "mu"))?.[1];
}

function directList(source, indentation, key) {
  const prefix = " ".repeat(indentation);
  const match = source.match(new RegExp(`^${prefix}${key}:\\n((?:${prefix}  - [^\\n]+\\n?)+)`, "mu"));
  assert.ok(match, `missing ${key} list`);
  return [...match[1].matchAll(new RegExp(`^${prefix}  - ([^\\n]+)$`, "gmu"))].map((entry) => entry[1]);
}

function assertOrdered(source, labels) {
  let previous = -1;
  for (const label of labels) {
    const current = source.indexOf(label);
    assert.ok(current > previous, `${label} must occur after the preceding gate`);
    previous = current;
  }
}

function inputValue(step, name) {
  return step.match(new RegExp(`^          ${name}:\\s*([^\\n#]+?)\\s*$`, "mu"))?.[1];
}

function jobOutputKeys(job) {
  const outputBlock = job.match(/^    outputs:\n((?:      [a-z0-9_]+:[^\n]*\n?)+)/mu)?.[1] ?? "";
  return [...outputBlock.matchAll(/^      ([a-z0-9_]+):/gmu)].map((match) => match[1]);
}

function powerShellRunBody(step) {
  const body = step.match(/^        shell: pwsh\n        run: \|\n([\s\S]*)$/mu)?.[1];
  assert.notEqual(body, undefined, "step must contain a PowerShell literal run block");
  return body.split("\n").map((line) => {
    assert.match(line, /^ {10}/u, "PowerShell run block line must retain workflow indentation");
    return line.slice(10);
  }).join("\n");
}

function bashRunBody(step) {
  const body = step.match(/^        shell: bash\n        run: \|\n([\s\S]*)$/mu)?.[1];
  assert.notEqual(body, undefined, "step must contain a Bash literal run block");
  return body.trimEnd().split("\n").map((line) => {
    assert.match(line, /^ {10}/u, "Bash run block line must retain workflow indentation");
    return line.slice(10);
  }).join("\n");
}

function inspectVisualStudioPreflightAst(preflight) {
  const source = powerShellRunBody(preflight);
  const result = spawnSync(pwsh, [
    "-NoLogo",
    "-NoProfile",
    "-NonInteractive",
    "-Command",
    "$script = [Console]::In.ReadToEnd(); & ([scriptblock]::Create($script))",
  ], {
    cwd: resolve("."),
    encoding: "utf8",
    env: {
      ...process.env,
      UNIT_TEST_IDE_VS_PREFLIGHT_SOURCE_B64: Buffer.from(source, "utf8").toString("base64"),
    },
    input: visualStudioAstInspector,
    windowsHide: true,
  });
  assert.equal(result.error, undefined, `PowerShell AST inspector failed to start: ${result.error?.message ?? "unknown error"}`);
  assert.equal(result.status, 0, `PowerShell AST inspector failed: ${result.stderr.trim()}`);
  assert.equal(result.stderr, "", "PowerShell AST inspector must not emit diagnostics");
  return JSON.parse(result.stdout);
}

function assertExecutableVisualStudioInstallationGuard(inspection) {
  assert.equal(inspection.parseErrorCount, 0, "Visual Studio preflight PowerShell must parse cleanly");
  assert.equal(inspection.instancesGuardStatementCount, 1, "Visual Studio instance guard must be one exact if statement with a direct throw $failure");
  assert.equal(inspection.assignmentStatementCount, 1, "Visual Studio installation Get-Item assignment must be one exact statement");
  assert.equal(inspection.installationGuardStatementCount, 1, "Visual Studio installation guard must be one exact if statement with a direct throw $failure");
  assert.equal(inspection.linearPreflightCount, 1, "Visual Studio instance guard, installation assignment, and installation guard must share one executable top-level linear block");
  assert.equal(inspection.earlySuccessTerminationCount, 0, "Visual Studio preflight must not contain return or exit before its linear validation triple");
}

function assertVisualStudioPreflightContract(preflight) {
  assert.match(preflight, /vswhere\.exe/u);
  assert.match(preflight, /-products '\*' -version '\[17\.0,18\.0\)'[\s\S]*?-requires 'Microsoft\.VisualStudio\.Component\.VC\.Tools\.x86\.x64' 'Microsoft\.VisualStudio\.Component\.VC\.Runtimes\.x86\.x64\.Spectre'/u);
  assert.match(preflight, /\$instances\.Count -ne 1/u);
  assert.match(preflight, /installationVersion[\s\S]*?\^17\\\./u);
  assertExecutableVisualStudioInstallationGuard(inspectVisualStudioPreflightAst(preflight));
  assert.match(preflight, /\$failure = 'RELEASE_PRODUCER_BUILD_FAILED: Visual Studio 2022 toolchain preflight failed'/u);
  assert.match(preflight, /GYP_MSVS_VERSION=2022/u);
  assert.match(preflight, /npm_config_msvs_version=2022/u);
  assert.doesNotMatch(preflight, /(?:winget|choco|visualstudio\.microsoft\.com|vs_installer|--add|--remove|fallback|2019|2017)/iu);
}

const visualStudioInstallationGuard = [
  "              $installationInfo = Get-Item -LiteralPath $instances[0].installationPath -Force",
  "              if (-not $installationInfo.PSIsContainer `",
  "                -or ($installationInfo.Attributes -band [IO.FileAttributes]::ReparsePoint)) {",
  "                throw $failure",
  "              }",
].join("\n");

const visualStudioInstancesGuard = [
  "              if ($instances.Count -ne 1 `",
  "                -or $instances[0].installationVersion -isnot [string] `",
  "                -or $instances[0].installationVersion -cnotmatch '^17\\.' `",
  "                -or $instances[0].installationPath -isnot [string]) {",
  "                throw $failure",
  "              }",
].join("\n");

const visualStudioPreflightLinearTriple = `${visualStudioInstancesGuard}\n${visualStudioInstallationGuard}`;

function replaceVisualStudioInstallationGuard(preflight, replacement) {
  assert.ok(preflight.includes(visualStudioInstallationGuard), "fixture must contain the executable Visual Studio installation guard");
  return preflight.replace(visualStudioInstallationGuard, replacement);
}

function replaceVisualStudioPreflightLinearTriple(preflight, replacement) {
  assert.ok(preflight.includes(visualStudioPreflightLinearTriple), "fixture must contain the executable Visual Studio preflight linear triple");
  return preflight.replace(visualStudioPreflightLinearTriple, replacement);
}

test("workflow exposes only an input-free manual trigger and minimum read permissions", () => {
  assert.equal(topLevelSection("on"), "on:\n  workflow_dispatch:");
  assert.equal(topLevelSection("permissions"), "permissions:\n  actions: read\n  contents: read");
  assert.doesNotMatch(workflow, /^\s*(?:push|pull_request|schedule|workflow_call|workflow_run|release):/mu);
  assert.doesNotMatch(workflow, /^\s+inputs:/mu);
  assert.doesNotMatch(jobsSource, /^    permissions:/mu);
});

test("workflow contains only the four closed jobs on fixed hosted runners", () => {
  const names = [...jobsSource.matchAll(/^  ([a-z][a-z0-9-]*):\s*$/gmu)].map((match) => match[1]);
  assert.deepEqual(names, ["authorize", "build-windows", "build-linux", "attest"]);
  assert.match(jobBlock("authorize"), /^    runs-on: ubuntu-24\.04$/mu);
  assert.match(jobBlock("build-windows"), /^    runs-on: windows-2022$/mu);
  assert.match(jobBlock("build-linux"), /^    runs-on: ubuntu-24\.04$/mu);
  assert.match(jobBlock("attest"), /^    runs-on: ubuntu-24\.04$/mu);
  assert.doesNotMatch(workflow, /(?:self-hosted|unit-test-wfp|windows-2025-vs2026|\b(?:windows|ubuntu)-latest\b)/iu);
});

test("authorization is fail-closed and every producer job depends on it", () => {
  const authorize = jobBlock("authorize");
  assert.doesNotMatch(authorize, /^    if:/mu);
  assert.match(authorize, /source-manifest\.mjs authorize/u);
  assert.match(authorize, /PRODUCER_REPOSITORY:\s*\$\{\{ github\.repository \}\}/u);
  assert.match(authorize, /PRODUCER_EVENT:\s*\$\{\{ github\.event_name \}\}/u);
  assert.match(authorize, /PRODUCER_REF:\s*\$\{\{ github\.ref \}\}/u);
  assert.match(authorize, /PRODUCER_WORKFLOW_REF:\s*\$\{\{ github\.workflow_ref \}\}/u);
  assert.match(authorize, /pnpm test:release-producer/u);
  assert.match(authorize, /node-version: 24\.18\.0/u);
  assert.match(authorize, /pnpm@11\.4\.0/u);
  assert.match(jobBlock("build-windows"), /^    needs: authorize$/mu);
  assert.match(jobBlock("build-linux"), /^    needs: authorize$/mu);
  assert.match(jobBlock("attest"), /^    needs:\n      - build-windows\n      - build-linux$/mu);
});

test("authorize exports the fixed local pnpm bin before running the producer suite", () => {
  const step = namedStep(jobBlock("authorize"), "Run producer contract tests on hosted Ubuntu");
  assert.equal(bashRunBody(step), [
    "set -euo pipefail",
    "npm install --global --prefix .release/tooling/pnpm --no-audit --no-fund pnpm@11.4.0",
    "[[ \"$(.release/tooling/pnpm/bin/pnpm --version)\" == '11.4.0' ]] || { echo 'RELEASE_PRODUCER_CONFIG_INVALID: pnpm version mismatch' >&2; exit 1; }",
    "export PATH=\"$PWD/.release/tooling/pnpm/bin:$PATH\"",
    ".release/tooling/pnpm/bin/pnpm test:release-producer",
  ].join("\n"));
});

test("all action references are reviewed full commit pins", () => {
  const references = [...workflow.matchAll(/^\s+uses:\s*([^\s#]+)(?:\s+#.*)?$/gmu)].map((match) => match[1]);
  assert.ok(references.length > 0);
  for (const reference of references) {
    const match = /^(actions\/[a-z-]+)@([0-9a-f]{40})$/u.exec(reference);
    assert.ok(match, `action is not full-SHA pinned: ${reference}`);
    assert.equal(match[2], actionPins[match[1]], `unreviewed action pin: ${reference}`);
  }
  for (const [action, pin] of Object.entries(actionPins)) {
    assert.ok(references.includes(`${action}@${pin}`), `missing reviewed ${action} action`);
  }
});

test("workflow has no secret, local-runtime, mutable-tool, or expression-in-shell escape hatch", () => {
  assert.doesNotMatch(workflow, /secrets\./iu);
  assert.doesNotMatch(workflow, /(?:^|[\s'"])[A-Za-z]:[\\/]|release-inputs[\\/]code-oss\.exe/imu);
  assert.doesNotMatch(workflow, /github\.com\/AppImage\/appimagetool\/releases\/(?:download|latest)|\/continuous\//iu);
  assert.doesNotMatch(workflow, /^\s+run:\s*[|>]?\s*\n(?:\s{8,}.*\$\{\{.*\n)+/gmu);
  for (const jobName of ["authorize", "build-windows", "build-linux", "attest"]) {
    for (const step of stepBlocks(jobBlock(jobName))) {
      const run = step.match(/^        run:\s*[|>]?-?\s*\n([\s\S]*)/mu)?.[1];
      if (run !== undefined) assert.doesNotMatch(run, /\$\{\{/u, "GitHub expressions must enter scripts through env, not interpolation");
    }
  }
});

test("both builds use the fixed fresh checkout, toolchains, Gulp targets, and output roots", () => {
  for (const name of ["build-windows", "build-linux"]) {
    const job = jobBlock(name);
    assert.match(job, /git init \.producer[\\/]vscode/u);
    assert.match(job, /https:\/\/github\.com\/microsoft\/vscode\.git/u);
    assert.match(job, /fetch --depth=1 origin b1c0a14de1414fcdaa400695b4db1c0799bc3124/u);
    assert.match(job, /checkout --detach FETCH_HEAD/u);
    assert.match(job, /rev-parse HEAD/u);
    assert.match(job, /source-manifest\.mjs verify-checkout/u);
    assert.match(job, /node-version: 20\.14\.0/u);
    assert.match(job, /yarn@1\.22\.22/u);
    assert.match(job, /install --frozen-lockfile/u);
  }
  const windows = jobBlock("build-windows");
  assert.match(windows, /gulp vscode-win32-x64/u);
  assert.match(windows, /\.producer[\\/]VSCode-win32-x64/u);
  assert.match(windows, /VSCode-\*/u);
  const linux = jobBlock("build-linux");
  assert.match(linux, /gulp vscode-linux-x64/u);
  assert.match(linux, /\.producer\/VSCode-linux-x64/u);
  assert.match(linux, /VSCode-\*/u);
  assert.match(linux, /apt-get install --no-install-recommends -y build-essential g\+\+ libx11-dev libx11-xcb-dev libxkbfile-dev libsecret-1-dev libkrb5-dev pkg-config python-is-python3/u);
  assert.doesNotMatch(workflow, /setup-go|fallback|--ignore-(?:engines|scripts)|(?:disable|without|no)[-_ ]spectre/iu);
});

test("fixed Yarn exports the executable location required by Code-OSS preinstall", () => {
  const step = namedStep(jobBlock("build-windows"), "Install fixed Yarn");
  assert.equal(powerShellRunBody(step), [
    "$ErrorActionPreference = 'Stop'",
    "npm install --global --prefix .release/tooling/yarn --no-audit --no-fund yarn@1.22.22",
    "if ($LASTEXITCODE -ne 0) { throw 'RELEASE_PRODUCER_BUILD_FAILED: Yarn installation failed' }",
    "$yarnRoot = [IO.Path]::GetFullPath('.release/tooling/yarn')",
    "$actualYarn = (& (Join-Path $yarnRoot 'yarn.cmd') --version).Trim()",
    "if ($LASTEXITCODE -ne 0 -or $actualYarn -cne '1.22.22') { throw 'RELEASE_PRODUCER_BUILD_FAILED: Yarn version mismatch' }",
    "$yarnRoot | Out-File -FilePath $env:GITHUB_PATH -Encoding utf8 -Append",
  ].join("\n"));
});

test("build steps invoke pinned Yarn JS with a narrowly scoped parent-manager bypass", () => {
  const windows = powerShellRunBody(namedStep(jobBlock("build-windows"), "Build fixed Windows Code-OSS target"));
  assert.equal(windows, [
    "$ErrorActionPreference = 'Stop'",
    "$yarnJs = Join-Path ([IO.Path]::GetFullPath('.release/tooling/yarn')) 'node_modules/yarn/bin/yarn.js'",
    "if (-not (Test-Path -LiteralPath $yarnJs -PathType Leaf)) { throw 'RELEASE_PRODUCER_BUILD_FAILED: Yarn entrypoint missing' }",
    "$yarnRoot = [IO.Path]::GetFullPath('.release/tooling/yarn')",
    "$previousSkipYarnCorepackCheck = $env:SKIP_YARN_COREPACK_CHECK",
    "$previousNpmExecPath = $env:npm_execpath",
    "$previousPath = $env:Path",
    "Push-Location '.producer/vscode'",
    "try {",
    "  $env:SKIP_YARN_COREPACK_CHECK = '1'",
    "  $env:npm_execpath = $yarnJs",
    "  $env:Path = \"$yarnRoot;$env:Path\"",
    "  $actualYarn = (& node $yarnJs --version).Trim()",
    "  if ($LASTEXITCODE -ne 0 -or $actualYarn -cne '1.22.22') { throw 'RELEASE_PRODUCER_BUILD_FAILED: pinned Yarn entrypoint failed' }",
    "  & node $yarnJs install --frozen-lockfile",
    "  if ($LASTEXITCODE -ne 0) { throw 'RELEASE_PRODUCER_BUILD_FAILED: dependency installation failed' }",
    "  & node $yarnJs gulp vscode-win32-x64",
    "  if ($LASTEXITCODE -ne 0) { throw 'RELEASE_PRODUCER_BUILD_FAILED: Windows build failed' }",
    "} finally {",
    "  if ($null -eq $previousSkipYarnCorepackCheck) {",
    "    Remove-Item Env:SKIP_YARN_COREPACK_CHECK -ErrorAction SilentlyContinue",
    "  } else {",
    "    $env:SKIP_YARN_COREPACK_CHECK = $previousSkipYarnCorepackCheck",
    "  }",
    "  if ($null -eq $previousNpmExecPath) {",
    "    Remove-Item Env:npm_execpath -ErrorAction SilentlyContinue",
    "  } else {",
    "    $env:npm_execpath = $previousNpmExecPath",
    "  }",
    "  $env:Path = $previousPath",
    "  Pop-Location",
    "}",
  ].join("\n"));

  const linux = bashRunBody(namedStep(jobBlock("build-linux"), "Build fixed Linux Code-OSS target"));
  assert.equal(linux, [
    "set -euo pipefail",
    'yarn_js="$PWD/.release/tooling/yarn/lib/node_modules/yarn/bin/yarn.js"',
    'test -f "$yarn_js"',
    'yarn_bin="$PWD/.release/tooling/yarn/bin"',
    'export PATH="$yarn_bin:$PATH"',
    "cd .producer/vscode",
    'actual_yarn="$(node "$yarn_js" --version)"',
    '[[ "$actual_yarn" == \'1.22.22\' ]] || { echo \'RELEASE_PRODUCER_BUILD_FAILED: pinned Yarn entrypoint failed\' >&2; exit 1; }',
    'YARN_SILENT=0 SKIP_YARN_COREPACK_CHECK=1 npm_execpath="$yarn_js" node "$yarn_js" install --frozen-lockfile --verbose',
    'SKIP_YARN_COREPACK_CHECK=1 npm_execpath="$yarn_js" node "$yarn_js" gulp vscode-linux-x64',
  ].join("\n"));

  for (const body of [windows, linux]) {
    assert.doesNotMatch(body, /yarn\.cmd|\.release\/tooling\/yarn\/bin\/yarn(?:\s|["'])/iu);
    assert.doesNotMatch(body, /^\s*yarn (?:install|gulp)\b/mu);
  }
});

test("Windows validates, bounds launcher execution, inventories, and stages before upload", () => {
  const job = jobBlock("build-windows");
  assertOrdered(job, [
    "name: Install fixed Yarn",
    "name: Validate Visual Studio 2022 toolchain",
    "name: Build fixed Windows Code-OSS target",
    "name: Validate Windows output root",
    "name: Validate Windows runtime",
    "name: Check Windows launcher version",
    "name: Stage and inventory Windows runtime",
    "name: Upload Windows runtime",
  ]);
  assert.match(job, /WaitForExit\(30000\)/u);
  assert.match(job, /1\.92\.0/u);
  assert.match(job, /runtime-inventory\.mjs create/u);
  assert.match(job, /\.release[\\/]producer[\\/]windows[\\/]code-oss-windows-x64/u);
  assert.match(job, /\$fileCount = \[int64\]0[\s\S]*?TryParse\([^\n]+\[ref\]\$fileCount\)/u);
  assert.match(job, /\$totalBytes = \[int64\]0[\s\S]*?TryParse\([^\n]+\[ref\]\$totalBytes\)/u);
  const outputs = ["launcher_sha256", "file_count", "total_bytes", "tree_digest", "windows_artifact_id", "windows_artifact_digest"];
  assert.deepEqual(jobOutputKeys(job), outputs);
  for (const output of outputs) {
    assert.match(job, new RegExp(`^      ${output}:`, "mu"));
  }
  const preflight = namedStep(job, "Validate Visual Studio 2022 toolchain");
  assert.ok(job.indexOf("name: Validate Visual Studio 2022 toolchain") < job.indexOf("name: Build fixed Windows Code-OSS target"), "executable Visual Studio preflight must precede the build step");
  assertVisualStudioPreflightContract(preflight);
});

test("Visual Studio preflight contract rejects installation guards hidden in comments", () => {
  const preflight = namedStep(jobBlock("build-windows"), "Validate Visual Studio 2022 toolchain");
  const commentedGuard = visualStudioInstallationGuard
    .split("\n")
    .map((line) => line.replace(/^(\s*)/u, "$1# "))
    .join("\n");
  const mutated = replaceVisualStudioInstallationGuard(preflight, commentedGuard);
  const inspection = inspectVisualStudioPreflightAst(mutated);
  assert.equal(inspection.parseErrorCount, 0, "comment mutation must remain valid PowerShell");
  assert.throws(() => assertExecutableVisualStudioInstallationGuard(inspection), /must be one exact statement/u);
});

test("Visual Studio preflight contract rejects installation guards in a constant-false branch", () => {
  const preflight = namedStep(jobBlock("build-windows"), "Validate Visual Studio 2022 toolchain");
  const deadGuard = [
    "              if ($false) {",
    ...visualStudioInstallationGuard.split("\n").map((line) => `  ${line}`),
    "              }",
  ].join("\n");
  const mutated = replaceVisualStudioInstallationGuard(preflight, deadGuard);
  const inspection = inspectVisualStudioPreflightAst(mutated);
  assert.equal(inspection.parseErrorCount, 0, "constant-false mutation must remain valid PowerShell");
  assert.throws(() => assertExecutableVisualStudioInstallationGuard(inspection), /must share one executable top-level linear block/u);
});

test("Visual Studio preflight contract rejects installation guards in a never-entered catch", () => {
  const preflight = namedStep(jobBlock("build-windows"), "Validate Visual Studio 2022 toolchain");
  const deadGuard = [
    "              try {",
    "                $null = 1",
    "              } catch {",
    ...visualStudioInstallationGuard.split("\n").map((line) => `  ${line}`),
    "              }",
  ].join("\n");
  const mutated = replaceVisualStudioInstallationGuard(preflight, deadGuard);
  const inspection = inspectVisualStudioPreflightAst(mutated);
  assert.equal(inspection.parseErrorCount, 0, "never-entered catch mutation must remain valid PowerShell");
  assert.throws(() => assertExecutableVisualStudioInstallationGuard(inspection), /must share one executable top-level linear block/u);
});

test("Visual Studio preflight contract rejects installation guards in a constant-false loop", () => {
  const preflight = namedStep(jobBlock("build-windows"), "Validate Visual Studio 2022 toolchain");
  const deadGuard = [
    "              while ($false) {",
    ...visualStudioInstallationGuard.split("\n").map((line) => `  ${line}`),
    "              }",
  ].join("\n");
  const mutated = replaceVisualStudioInstallationGuard(preflight, deadGuard);
  const inspection = inspectVisualStudioPreflightAst(mutated);
  assert.equal(inspection.parseErrorCount, 0, "constant-false loop mutation must remain valid PowerShell");
  assert.throws(() => assertExecutableVisualStudioInstallationGuard(inspection), /must share one executable top-level linear block/u);
});

test("Visual Studio preflight contract rejects an installation guard whose throw is unreachable", () => {
  const preflight = namedStep(jobBlock("build-windows"), "Validate Visual Studio 2022 toolchain");
  const deadThrowGuard = visualStudioInstallationGuard.replace(
    "                throw $failure",
    [
      "                if ($false) {",
      "                  throw $failure",
      "                }",
    ].join("\n"),
  );
  const mutated = replaceVisualStudioInstallationGuard(preflight, deadThrowGuard);
  const inspection = inspectVisualStudioPreflightAst(mutated);
  assert.equal(inspection.parseErrorCount, 0, "unreachable throw mutation must remain valid PowerShell");
  assert.throws(() => assertExecutableVisualStudioInstallationGuard(inspection), /installation guard must be one exact if statement/u);
});

test("Visual Studio preflight contract rejects return before its linear validation triple", () => {
  const preflight = namedStep(jobBlock("build-windows"), "Validate Visual Studio 2022 toolchain");
  const mutated = replaceVisualStudioPreflightLinearTriple(preflight, [
    "              return",
    visualStudioPreflightLinearTriple,
  ].join("\n"));
  const inspection = inspectVisualStudioPreflightAst(mutated);
  assert.equal(inspection.parseErrorCount, 0, "early return mutation must remain valid PowerShell");
  assert.throws(() => assertExecutableVisualStudioInstallationGuard(inspection), /must not contain return or exit before/u);
});

test("Visual Studio preflight contract rejects successful exit before its linear validation triple", () => {
  const preflight = namedStep(jobBlock("build-windows"), "Validate Visual Studio 2022 toolchain");
  const mutated = replaceVisualStudioPreflightLinearTriple(preflight, [
    "              exit 0",
    visualStudioPreflightLinearTriple,
  ].join("\n"));
  const inspection = inspectVisualStudioPreflightAst(mutated);
  assert.equal(inspection.parseErrorCount, 0, "successful exit mutation must remain valid PowerShell");
  assert.throws(() => assertExecutableVisualStudioInstallationGuard(inspection), /must not contain return or exit before/u);
});

test("Linux creates and validates modes, bounds launcher execution, inventories, and stages exact roots", () => {
  const job = jobBlock("build-linux");
  assertOrdered(job, [
    "name: Validate Linux output root",
    "name: Create Linux mode inventory",
    "name: Stage and validate Linux runtime",
    "name: Check Linux launcher version",
    "name: Inventory staged Linux runtime",
    "name: Acquire and validate appimagetool",
    "name: Upload Linux runtime",
    "name: Upload appimagetool",
  ]);
  assert.match(job, /runtime-mode-inventory\.mjs create/u);
  assert.match(job, /runtime-mode-inventory\.mjs restore/u);
  assert.match(job, /timeout 30s/u);
  assert.match(job, /1\.92\.0/u);
  assert.match(job, /code-oss-runtime-mode\.json/u);
  assert.match(job, /runtime-inventory\.mjs create/u);
  assert.match(job, /https:\/\/api\.github\.com\/repos\/AppImage\/appimagetool\/releases\/assets\/324406882/u);
  assert.match(job, /Accept: application\/octet-stream/u);
  assert.match(job, /15092216/u);
  assert.match(job, /a6d71e2b6cd66f8e8d16c37ad164658985e0cf5fcaa950c90a482890cb9d13e0/u);
  assert.match(job, /find "\$tool_root" -mindepth 1 -maxdepth 1 -printf '%f\\0'/u);
  assert.match(job, /-f "\$tool" && ! -L "\$tool"/u);
  const outputs = ["launcher_sha256", "file_count", "total_bytes", "tree_digest", "mode_inventory_sha256", "appimagetool_sha256", "appimagetool_size", "linux_artifact_id", "linux_artifact_digest", "appimagetool_artifact_id", "appimagetool_artifact_digest"];
  assert.deepEqual(jobOutputKeys(job), outputs);
  for (const output of outputs) {
    assert.match(job, new RegExp(`^      ${output}:`, "mu"));
  }
});

test("launcher version checks execute packaged CLI wrappers instead of GUI binaries", () => {
  const windows = namedStep(jobBlock("build-windows"), "Check Windows launcher version");
  const linux = namedStep(jobBlock("build-linux"), "Check Linux launcher version");
  const transportedLinux = namedStep(jobBlock("attest"), "Check transported Linux launcher version");

  assert.match(windows, /Start-Process -FilePath '\.producer\/VSCode-win32-x64\/bin\/code-oss\.cmd'/u);
  assert.doesNotMatch(windows, /Start-Process -FilePath '\.producer\/VSCode-win32-x64\/Code - OSS\.exe'/u);
  assert.match(linux, /timeout 30s \.release\/producer\/linux\/code-oss-linux-x64\/runtime\/bin\/code-oss --version/u);
  assert.doesNotMatch(linux, /runtime\/code-oss --version/u);
  assert.match(transportedLinux, /timeout 30s \.release\/transport\/linux\/runtime\/bin\/code-oss --version/u);
  assert.doesNotMatch(transportedLinux, /linux\/runtime\/code-oss --version/u);
});

test("uploads expose immutable identities under attempt-qualified transport names", () => {
  const uploads = [];
  for (const name of ["build-windows", "build-linux", "attest"]) {
    for (const step of stepBlocks(jobBlock(name))) {
      if (step.includes(`uses: actions/upload-artifact@${actionPins["actions/upload-artifact"]}`)) uploads.push(step);
    }
  }
  assert.equal(uploads.length, 4);
  const expected = new Map([
    ["code-oss-windows-x64-${{ github.run_attempt }}", { id: "upload-windows-runtime", hidden: true, path: ".release/producer/windows/code-oss-windows-x64" }],
    ["code-oss-linux-x64-${{ github.run_attempt }}", { id: "upload-linux-runtime", hidden: true, path: ".release/producer/linux/code-oss-linux-x64" }],
    ["appimagetool-linux-x64-${{ github.run_attempt }}", { id: "upload-appimagetool", hidden: false, path: ".release/producer/linux/appimagetool-linux-x64/appimagetool-x86_64.AppImage" }],
    ["release-input-provenance-${{ github.run_attempt }}", { id: "upload-provenance", hidden: false, path: ".release/attestation/release-input-provenance.json" }],
  ]);
  for (const step of uploads) {
    const name = inputValue(step, "name");
    assert.ok(expected.has(name), `unexpected upload artifact ${name}`);
    assert.equal(inputValue(step, "id"), undefined, "upload identifiers must be step-level IDs");
    assert.match(step, new RegExp(`^        id: ${expected.get(name).id}$`, "mu"));
    assert.equal(inputValue(step, "retention-days"), "1", `${name} retention drifted`);
    assert.equal(inputValue(step, "if-no-files-found"), "error", `${name} may silently upload no files`);
    assert.equal(inputValue(step, "path"), expected.get(name).path, `${name} upload root drifted`);
    assert.doesNotMatch(step, /^        if:\s*\$\{\{\s*always\(\)\s*\}\}/mu);
    if (expected.get(name).hidden) assert.equal(inputValue(step, "include-hidden-files"), "true", `${name} drops hidden runtime files`);
  }
  assert.equal(directMappingValue(jobBlock("build-windows"), 6, "windows_artifact_id"), "${{ steps.upload-windows-runtime.outputs.artifact-id }}");
  assert.equal(directMappingValue(jobBlock("build-windows"), 6, "windows_artifact_digest"), "${{ steps.upload-windows-runtime.outputs.artifact-digest }}");
  assert.equal(directMappingValue(jobBlock("build-linux"), 6, "linux_artifact_id"), "${{ steps.upload-linux-runtime.outputs.artifact-id }}");
  assert.equal(directMappingValue(jobBlock("build-linux"), 6, "linux_artifact_digest"), "${{ steps.upload-linux-runtime.outputs.artifact-digest }}");
  assert.equal(directMappingValue(jobBlock("build-linux"), 6, "appimagetool_artifact_id"), "${{ steps.upload-appimagetool.outputs.artifact-id }}");
  assert.equal(directMappingValue(jobBlock("build-linux"), 6, "appimagetool_artifact_digest"), "${{ steps.upload-appimagetool.outputs.artifact-digest }}");
});

test("attestation validates immutable coordinates before ID-only downloads and independently revalidates them", () => {
  const job = jobBlock("attest");
  const downloads = stepBlocks(job).filter((step) => step.includes(`uses: actions/download-artifact@${actionPins["actions/download-artifact"]}`));
  assert.equal(downloads.length, 3);
  assert.deepEqual(downloads.map((step) => inputValue(step, "artifact-ids")), [
    "${{ needs.build-windows.outputs.windows_artifact_id }}",
    "${{ needs.build-linux.outputs.linux_artifact_id }}",
    "${{ needs.build-linux.outputs.appimagetool_artifact_id }}",
  ]);
  assert.deepEqual(downloads.map((step) => inputValue(step, "path")), [
    ".release/transport/windows",
    ".release/transport/linux",
    ".release/transport/appimagetool",
  ]);
  for (const step of downloads) {
    assert.equal(inputValue(step, "name"), undefined, "attestation must not select artifacts by name");
    assert.equal(inputValue(step, "merge-multiple"), "true", "attestation must flatten its pinned artifact into its transport root");
    assert.equal(inputValue(step, "run-id"), undefined, "attestation must consume only artifacts from its current run");
  }
  const preflight = namedStep(job, "Validate immutable artifact identities");
  assert.doesNotMatch(preflight, /^          GITHUB_RUN_(?:ID|ATTEMPT):/mu, "preflight must use GitHub's canonical runtime variables");
  assert.match(preflight, /WINDOWS_ARTIFACT_ID: \$\{\{ needs\.build-windows\.outputs\.windows_artifact_id \}\}/u);
  assert.match(preflight, /WINDOWS_ARTIFACT_DIGEST: \$\{\{ needs\.build-windows\.outputs\.windows_artifact_digest \}\}/u);
  assert.match(preflight, /LINUX_ARTIFACT_ID: \$\{\{ needs\.build-linux\.outputs\.linux_artifact_id \}\}/u);
  assert.match(preflight, /LINUX_ARTIFACT_DIGEST: \$\{\{ needs\.build-linux\.outputs\.linux_artifact_digest \}\}/u);
  assert.match(preflight, /APPIMAGETOOL_ARTIFACT_ID: \$\{\{ needs\.build-linux\.outputs\.appimagetool_artifact_id \}\}/u);
  assert.match(preflight, /APPIMAGETOOL_ARTIFACT_DIGEST: \$\{\{ needs\.build-linux\.outputs\.appimagetool_artifact_digest \}\}/u);
  for (const value of ["$GITHUB_RUN_ID", "$GITHUB_RUN_ATTEMPT", "$WINDOWS_ARTIFACT_ID", "$LINUX_ARTIFACT_ID", "$APPIMAGETOOL_ARTIFACT_ID"]) {
    assert.match(preflight, new RegExp(`\\[\\[ "${value.replaceAll("$", "\\$")}" =~ \\^\\[1-9\\]\\[0-9\\]\\*\\$ \\]\\]`, "u"));
  }
  for (const value of ["$WINDOWS_ARTIFACT_DIGEST", "$LINUX_ARTIFACT_DIGEST", "$APPIMAGETOOL_ARTIFACT_DIGEST"]) {
    assert.match(preflight, new RegExp(`\\[\\[ "${value.replaceAll("$", "\\$")}" =~ \\^\\[0-9a-f\\]\\{64\\}\\$ \\]\\]`, "u"));
  }
  assertOrdered(job, [
    "name: Validate immutable artifact identities",
    "name: Download Windows runtime",
    "name: Download Linux runtime",
    "name: Download appimagetool",
    "name: Validate exact transport roots",
    "name: Validate transported Linux mode inventory",
    "name: Restore transported Linux modes",
    "name: Inventory transported runtimes and tool",
    "name: Compare transported summaries",
    "name: Check transported Linux launcher version",
    "name: Create and validate provenance",
    "name: Write validated workflow summary",
    "name: Upload release input provenance",
  ]);
  const modeGate = namedStep(job, "Validate transported Linux mode inventory");
  const restore = namedStep(job, "Restore transported Linux modes");
  const inventory = namedStep(job, "Inventory transported runtimes and tool");
  const compare = namedStep(job, "Compare transported summaries");
  const execute = namedStep(job, "Check transported Linux launcher version");
  assert.match(modeGate, /validateRuntimeModeInventory/u);
  assert.match(modeGate, /mode_inventory_sha256[\s\S]*?EXPECTED_MODE_INVENTORY_SHA256/u);
  assert.doesNotMatch(modeGate, /runtime-mode-inventory\.mjs restore|timeout 30s/u);
  assert.match(restore, /runtime-mode-inventory\.mjs restore/u);
  assert.doesNotMatch(restore, /timeout 30s/u);
  assert.match(inventory, /runtime-inventory\.mjs create/u);
  assert.doesNotMatch(inventory, /timeout 30s/u);
  assert.match(compare, /EXPECTED_WINDOWS_TREE_DIGEST/u);
  assert.match(compare, /EXPECTED_LINUX_TREE_DIGEST/u);
  assert.match(compare, /EXPECTED_MODE_INVENTORY_SHA256/u);
  assert.match(compare, /EXPECTED_APPIMAGETOOL_SHA256/u);
  assert.doesNotMatch(compare, /timeout 30s/u);
  assert.match(execute, /timeout 30s \.release\/transport\/linux\/runtime\/bin\/code-oss/u);
  assert.match(job, /runtime-inventory\.mjs create/u);
  assert.match(job, /provenance\.mjs create/u);
  assert.match(job, /provenance\.mjs validate/u);
  assert.match(job, /test ! -e \.release\/attestation/u);
  assert.doesNotMatch(job, /mkdir[^\n]*\.release\/attestation/u);
  assert.match(job, /find \.release\/transport\/appimagetool -mindepth 1 -maxdepth 1 -printf '%f\\0'/u);
  assert.match(job, /-f "\$tool" && ! -L "\$tool"/u);
  assert.match(job, /find \.release\/attestation -mindepth 1 -maxdepth 1 -printf '%f\\0'/u);
});

test("provenance and post-validation summary bind and expose only validated immutable identities", () => {
  const job = jobBlock("attest");
  const provenance = namedStep(job, "Create and validate provenance");
  for (const [option, value] of [
    ["--producer-run-id", "$GITHUB_RUN_ID"],
    ["--producer-run-attempt", "$GITHUB_RUN_ATTEMPT"],
    ["--windows-artifact-id", "$WINDOWS_ARTIFACT_ID"],
    ["--windows-artifact-digest", "$WINDOWS_ARTIFACT_DIGEST"],
    ["--windows-artifact-transport-name", "code-oss-windows-x64-$GITHUB_RUN_ATTEMPT"],
    ["--linux-artifact-id", "$LINUX_ARTIFACT_ID"],
    ["--linux-artifact-digest", "$LINUX_ARTIFACT_DIGEST"],
    ["--linux-artifact-transport-name", "code-oss-linux-x64-$GITHUB_RUN_ATTEMPT"],
    ["--appimagetool-artifact-id", "$APPIMAGETOOL_ARTIFACT_ID"],
    ["--appimagetool-artifact-digest", "$APPIMAGETOOL_ARTIFACT_DIGEST"],
    ["--appimagetool-artifact-transport-name", "appimagetool-linux-x64-$GITHUB_RUN_ATTEMPT"],
  ]) {
    assert.match(provenance, new RegExp(`${option} "${value.replaceAll("$", "\\$")}"`, "u"));
  }
  const validateIndex = job.indexOf("provenance.mjs validate");
  const summaryIndex = job.indexOf("name: Write validated workflow summary");
  assert.ok(validateIndex !== -1 && summaryIndex > validateIndex);
  const summary = namedStep(job, "Write validated workflow summary");
  const run = summary.match(/^        run:\s*\|\s*\n([\s\S]*)/mu)?.[1];
  assert.ok(run);
  assert.doesNotMatch(run, /(?:[A-Za-z]:[\\/]|\.release[\\/]|tools[\\/]|\/home\/|\$\{\{)/u);
  const labels = [...run.matchAll(/printf -- '- ([^:]+):/gu)].map((match) => match[1]);
  assert.deepEqual(labels, [
    "Producer run ID",
    "Producer run attempt",
    "Source commit",
    "Windows artifact ID",
    "Linux artifact ID",
    "appimagetool artifact ID",
    "Windows launcher SHA-256",
    "Linux launcher SHA-256",
    "appimagetool SHA-256",
    "Artifact retention",
  ]);
});

test("package scripts run the closed producer suite early and enumerate every producer contract", () => {
  assert.equal(
    packageJson.scripts["test:release-producer"],
    "node --test tools/release/producer/source-manifest.test.mjs tools/release/producer/runtime-inventory.test.mjs tools/release/producer/provenance.test.mjs tools/release/producer/trusted-run.test.mjs tools/release/producer/workflow-contract.test.mjs",
  );
  assert.match(packageJson.scripts.test, /^pnpm run test:release-producer && /u);
});

test("foundation validates producer identity before downloading one exact provenance artifact", () => {
  const trust = foundationJobBlock("verify-release-input-run");
  const packageWindows = foundationJobBlock("package-windows");
  const packageLinux = foundationJobBlock("package-linux");
  const predicate = "${{ github.event_name == 'workflow_dispatch' || startsWith(github.ref, 'refs/tags/v') }}";
  assert.equal(directMappingValue(trust, 4, "if"), predicate);
  assert.equal(directMappingValue(packageWindows, 4, "if"), predicate);
  assert.equal(directMappingValue(packageLinux, 4, "if"), predicate);

  assert.deepEqual(jobOutputKeys(trust), [
    "run_id",
    "run_attempt",
    "windows_launcher_sha256",
    "linux_launcher_sha256",
    "appimagetool_sha256",
    "windows_artifact_id",
    "windows_artifact_digest",
    "linux_artifact_id",
    "linux_artifact_digest",
    "appimagetool_artifact_id",
    "appimagetool_artifact_digest",
  ]);
  for (const key of jobOutputKeys(trust)) {
    assert.equal(
      directMappingValue(trust, 6, key),
      `\${{ steps.verify-provenance.outputs.${key} }}`,
      `${key} must come from the final validation step`,
    );
  }

  const checkout = stepBlocks(trust)[0];
  assert.deepEqual(
    stepBlocks(trust).map((step) => stepHeaderValue(step, "uses") ?? directMappingValue(step, 8, "uses")).filter(Boolean),
    [
      `actions/checkout@${actionPins["actions/checkout"]}`,
      `actions/setup-node@${actionPins["actions/setup-node"]}`,
      `actions/download-artifact@${actionPins["actions/download-artifact"]}`,
    ],
  );
  assert.equal(stepHeaderValue(checkout, "uses"), `actions/checkout@${actionPins["actions/checkout"]}`);
  assert.equal(inputValue(checkout, "ref"), "${{ github.sha }}");
  const setupNode = stepBlocks(trust).find((step) => stepHeaderValue(step, "uses")?.startsWith("actions/setup-node@"));
  assert.ok(setupNode, "trust job must install Node through a real step");
  assert.equal(stepHeaderValue(setupNode, "uses"), `actions/setup-node@${actionPins["actions/setup-node"]}`);
  assert.equal(inputValue(setupNode, "node-version"), "24.18.0");

  const select = namedStep(trust, "Select release input coordinates");
  assert.deepEqual([...select.matchAll(/^          ([A-Z0-9_]+):/gmu)].map((match) => match[1]), [
    "RELEASE_INPUT_RUN_ID",
    "WINDOWS_LAUNCHER_SHA256",
    "LINUX_LAUNCHER_SHA256",
    "APPIMAGETOOL_SHA256",
  ]);
  assert.match(select, /^          RELEASE_INPUT_RUN_ID: \$\{\{ github\.event_name == 'workflow_dispatch' && inputs\.release_input_run_id \|\| vars\.RELEASE_INPUT_RUN_ID \}\}$/mu);
  assert.match(select, /^          WINDOWS_LAUNCHER_SHA256: \$\{\{ github\.event_name == 'workflow_dispatch' && inputs\.windows_code_oss_sha256 \|\| vars\.RELEASE_CODE_OSS_WINDOWS_SHA256 \}\}$/mu);
  assert.match(select, /^          LINUX_LAUNCHER_SHA256: \$\{\{ github\.event_name == 'workflow_dispatch' && inputs\.linux_code_oss_sha256 \|\| vars\.RELEASE_CODE_OSS_LINUX_SHA256 \}\}$/mu);
  assert.match(select, /^          APPIMAGETOOL_SHA256: \$\{\{ github\.event_name == 'workflow_dispatch' && inputs\.linux_appimagetool_sha256 \|\| vars\.RELEASE_APPIMAGETOOL_LINUX_SHA256 \}\}$/mu);
  const selectRun = select.match(/^        run: \|\n([\s\S]*)$/mu)?.[1] ?? "";
  assert.match(selectRun, /^          \[\[ "\$RELEASE_INPUT_RUN_ID" =~ \^\[1-9\]\[0-9\]\*\$ \]\]/mu);
  assert.equal((selectRun.match(/^          \[\[ "\$(?:WINDOWS_LAUNCHER_SHA256|LINUX_LAUNCHER_SHA256|APPIMAGETOOL_SHA256)" =~ \^\[0-9a-f\]\{64\}\$ \]\]/gmu) ?? []).length, 3);
  assertOrdered(selectRun, [
    '[[ "$RELEASE_INPUT_RUN_ID"',
    '[[ "$WINDOWS_LAUNCHER_SHA256"',
    '[[ "$LINUX_LAUNCHER_SHA256"',
    '[[ "$APPIMAGETOOL_SHA256"',
    '} >> "$GITHUB_ENV"',
  ]);

  const apiBefore = namedStep(trust, "Fetch trusted producer metadata before provenance download");
  assert.equal(directMappingValue(apiBefore, 10, "GH_TOKEN"), "${{ github.token }}");
  assert.match(apiBefore, /^          gh api "repos\/colayc\/unitTest\/actions\/runs\/\$RELEASE_INPUT_RUN_ID" > "\$verify_root\/producer-run-before\.json"$/mu);
  assert.match(apiBefore, /^          gh api "repos\/colayc\/unitTest\/actions\/runs\/\$RELEASE_INPUT_RUN_ID\/artifacts\?per_page=100" > "\$verify_root\/producer-artifacts-before\.json"$/mu);
  assert.doesNotMatch(apiBefore, /(?:set -x|echo .*GH_TOKEN|cat \.release\/producer-(?:run|artifacts)-before\.json)/u);

  const precheck = identifiedStep(trust, "precheck");
  const provenanceDownload = namedStep(trust, "Download trusted release input provenance");
  const apiAfter = namedStep(trust, "Refetch trusted producer metadata after provenance download");
  const finalValidation = identifiedStep(trust, "verify-provenance");
  assertOrdered(trust, [
    "name: Fetch trusted producer metadata before provenance download",
    "id: precheck",
    "name: Download trusted release input provenance",
    "name: Refetch trusted producer metadata after provenance download",
    "id: verify-provenance",
  ]);
  assert.match(precheck, /^          node tools\/release\/producer\/trusted-run\.mjs validate-run \\\n            --run-json \.release\/producer-verification\/producer-run-before\.json \\\n            --artifacts-json \.release\/producer-verification\/producer-artifacts-before\.json \\\n            --run-id "\$RELEASE_INPUT_RUN_ID" \\\n            --consumer-commit "\$GITHUB_SHA" \\\n            --github-output "\$GITHUB_OUTPUT"$/mu);
  assert.equal(directMappingValue(provenanceDownload, 8, "uses"), `actions/download-artifact@${actionPins["actions/download-artifact"]}`);
  assert.equal(inputValue(provenanceDownload, "artifact-ids"), "${{ steps.precheck.outputs.provenance_artifact_id }}");
  assert.equal(inputValue(provenanceDownload, "merge-multiple"), "true");
  assert.equal(inputValue(provenanceDownload, "path"), ".release/producer-verification/provenance");
  assert.equal(inputValue(provenanceDownload, "run-id"), "${{ steps.precheck.outputs.run_id }}");
  assert.equal(inputValue(provenanceDownload, "github-token"), "${{ github.token }}");
  assert.equal(inputValue(provenanceDownload, "name"), undefined);
  assert.equal(directMappingValue(apiAfter, 10, "GH_TOKEN"), "${{ github.token }}");
  assert.match(apiAfter, /^          gh api "repos\/colayc\/unitTest\/actions\/runs\/\$RELEASE_INPUT_RUN_ID" > "\$verify_root\/producer-run-after\.json"$/mu);
  assert.match(apiAfter, /^          gh api "repos\/colayc\/unitTest\/actions\/runs\/\$RELEASE_INPUT_RUN_ID\/artifacts\?per_page=100" > "\$verify_root\/producer-artifacts-after\.json"$/mu);
  assert.doesNotMatch(apiAfter, /(?:set -x|echo .*GH_TOKEN|cat \.release\/producer-(?:run|artifacts)-after\.json)/u);
  assert.match(finalValidation, /^          mapfile -d '' -t provenance_entries < <\(find \.release\/producer-verification\/provenance -mindepth 1 -maxdepth 1 -printf '%f\\0' \| LC_ALL=C sort -z\)$/mu);
  assert.match(finalValidation, /release-input-provenance\.json/u);
  assert.match(finalValidation, /^          node tools\/release\/producer\/trusted-run\.mjs validate-provenance \\\n            --run-json \.release\/producer-verification\/producer-run-after\.json \\\n            --artifacts-json \.release\/producer-verification\/producer-artifacts-after\.json \\\n            --run-id "\$\{\{ steps\.precheck\.outputs\.run_id \}\}" \\\n            --run-attempt "\$\{\{ steps\.precheck\.outputs\.run_attempt \}\}" \\\n            --consumer-commit "\$GITHUB_SHA" \\\n            --github-output "\$GITHUB_OUTPUT" \\\n            --provenance \.release\/producer-verification\/provenance\/release-input-provenance\.json \\\n            --provenance-artifact-id "\$\{\{ steps\.precheck\.outputs\.provenance_artifact_id \}\}" \\\n            --provenance-artifact-digest "\$\{\{ steps\.precheck\.outputs\.provenance_artifact_digest \}\}"/mu);
  for (const [option, variable] of [
    ["--windows-launcher-sha256", "WINDOWS_LAUNCHER_SHA256"],
    ["--linux-launcher-sha256", "LINUX_LAUNCHER_SHA256"],
    ["--appimagetool-sha256", "APPIMAGETOOL_SHA256"],
  ]) {
    assert.ok(finalValidation.includes(`${option} "$${variable}"`), `${option} must use $${variable}`);
  }
  assert.equal((trust.match(/GITHUB_OUTPUT/gu) ?? []).length, 2, "only trusted-run may append step outputs");
  assert.equal((trust.match(/GH_TOKEN/gu) ?? []).length, 2, "GH_TOKEN must exist only as the two gh step environment keys");
});

test("foundation package jobs consume only the trust job closed coordinates", () => {
  const rawCoordinates = /(?:inputs\.(?:release_input_run_id|windows_code_oss_sha256|linux_code_oss_sha256|linux_appimagetool_sha256)|vars\.(?:RELEASE_INPUT_RUN_ID|RELEASE_CODE_OSS_[A-Z_]+|RELEASE_APPIMAGETOOL_[A-Z_]+))/u;
  const trust = foundationJobBlock("verify-release-input-run");
  assert.doesNotMatch(foundationWorkflow.replace(trust, ""), rawCoordinates, "raw coordinates must occur only in the trust job");
  for (const [name, expectedEnv] of [
    ["package-windows", {
      RELEASE_INPUT_RUN_ID: "${{ needs.verify-release-input-run.outputs.run_id }}",
      RELEASE_INPUT_RUN_ATTEMPT: "${{ needs.verify-release-input-run.outputs.run_attempt }}",
      WINDOWS_ARTIFACT_ID: "${{ needs.verify-release-input-run.outputs.windows_artifact_id }}",
      WINDOWS_ARTIFACT_DIGEST: "${{ needs.verify-release-input-run.outputs.windows_artifact_digest }}",
      CODE_OSS_SHA256: "${{ needs.verify-release-input-run.outputs.windows_launcher_sha256 }}",
    }],
    ["package-linux", {
      RELEASE_INPUT_RUN_ID: "${{ needs.verify-release-input-run.outputs.run_id }}",
      RELEASE_INPUT_RUN_ATTEMPT: "${{ needs.verify-release-input-run.outputs.run_attempt }}",
      LINUX_ARTIFACT_ID: "${{ needs.verify-release-input-run.outputs.linux_artifact_id }}",
      LINUX_ARTIFACT_DIGEST: "${{ needs.verify-release-input-run.outputs.linux_artifact_digest }}",
      APPIMAGETOOL_ARTIFACT_ID: "${{ needs.verify-release-input-run.outputs.appimagetool_artifact_id }}",
      APPIMAGETOOL_ARTIFACT_DIGEST: "${{ needs.verify-release-input-run.outputs.appimagetool_artifact_digest }}",
      CODE_OSS_SHA256: "${{ needs.verify-release-input-run.outputs.linux_launcher_sha256 }}",
      APPIMAGETOOL_SHA256: "${{ needs.verify-release-input-run.outputs.appimagetool_sha256 }}",
    }],
  ]) {
    const job = foundationJobBlock(name);
    assert.deepEqual(directList(job, 4, "needs"), ["verify-windows", "verify-linux", "verify-release-input-run"]);
    assert.doesNotMatch(job, rawCoordinates, `${name} must not read unvalidated coordinates`);
    for (const [key, value] of Object.entries(expectedEnv)) assert.equal(directMappingValue(job, 6, key), value);

    const platform = name === "package-windows" ? "Windows" : "Linux";
    const before = namedStep(job, `Validate producer attempt before ${platform} artifact download`);
    const after = namedStep(job, `Validate producer attempt after ${platform} artifact download`);
    assert.equal(directMappingValue(before, 10, "GH_TOKEN"), "${{ github.token }}");
    assert.equal(directMappingValue(after, 10, "GH_TOKEN"), "${{ github.token }}");
    for (const [step, phase] of [[before, "before"], [after, "after"]]) {
      const runJson = `.release/producer-run-${name === "package-windows" ? "windows" : "linux"}-${phase}.json`;
      const expectedGate = name === "package-windows"
        ? [
            `          gh api "repos/colayc/unitTest/actions/runs/$env:RELEASE_INPUT_RUN_ID" > ${runJson}`,
            "          node tools/release/producer/trusted-run.mjs validate-attempt `",
            "            --run-json " + runJson + " `",
            "            --run-id $env:RELEASE_INPUT_RUN_ID `",
            "            --run-attempt $env:RELEASE_INPUT_RUN_ATTEMPT `",
            "            --consumer-commit $env:GITHUB_SHA",
          ].join("\n")
        : [
            `          gh api "repos/colayc/unitTest/actions/runs/$RELEASE_INPUT_RUN_ID" > ${runJson}`,
            "          node tools/release/producer/trusted-run.mjs validate-attempt \\",
            `            --run-json ${runJson} \\`,
            '            --run-id "$RELEASE_INPUT_RUN_ID" \\',
            '            --run-attempt "$RELEASE_INPUT_RUN_ATTEMPT" \\',
            '            --consumer-commit "$GITHUB_SHA"',
          ].join("\n");
      assert.ok(step.includes(expectedGate), `${name} must fetch a fresh exact ${phase} snapshot immediately before exact attempt validation`);
      assert.equal(step.match(/^          gh api /gmu)?.length, 1, `${name} ${phase} gate must fetch exactly once`);
      assert.equal(step.match(/trusted-run\.mjs validate-attempt/gu)?.length, 1, `${name} ${phase} gate must validate exactly once`);
      assert.doesNotMatch(step, /(?:set -x|echo .*GH_TOKEN|cat \.release\/producer-run)/u);
    }

    const downloads = stepBlocks(job).filter((step) => directMappingValue(step, 8, "uses") === `actions/download-artifact@${actionPins["actions/download-artifact"]}`);
    const expectedIds = name === "package-windows"
      ? ["${{ needs.verify-release-input-run.outputs.windows_artifact_id }}"]
      : ["${{ needs.verify-release-input-run.outputs.linux_artifact_id }}", "${{ needs.verify-release-input-run.outputs.appimagetool_artifact_id }}"];
    assert.deepEqual(downloads.map((step) => inputValue(step, "artifact-ids")), expectedIds);
    for (const download of downloads) {
      assert.equal(inputValue(download, "merge-multiple"), "true");
      assert.equal(inputValue(download, "run-id"), "${{ needs.verify-release-input-run.outputs.run_id }}");
      assert.equal(inputValue(download, "github-token"), "${{ github.token }}");
      assert.equal(inputValue(download, "name"), undefined);
    }
    assertOrdered(job, [before, ...downloads, after]);
    const firstSensitiveOperation = name === "package-windows"
      ? job.indexOf("Verify and export release inputs")
      : job.indexOf("node tools/release/linux/runtime-mode-inventory.mjs restore");
    assert.ok(job.indexOf(after) < firstSensitiveOperation, `${name} must revalidate the attempt before staging, restoration, or execution`);
  }
});
