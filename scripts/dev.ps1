param(
    [ValidateSet('start','bootstrap','test','race','build')][string]$Task = 'start',
    [string]$Config = 'config.dev.json'
)
$ErrorActionPreference = 'Stop'
$projectRoot = Split-Path $PSScriptRoot -Parent
$localGo = Join-Path $projectRoot '.tools/go/bin/go.exe'
$go = if (Test-Path $localGo) { $localGo } else { (Get-Command go -ErrorAction Stop).Source }
if (-not $PSBoundParameters.ContainsKey('Config') -and (Test-Path (Join-Path $projectRoot 'config.local.json'))) { $Config = 'config.local.json' }
$oldCGO = $env:CGO_ENABLED
$oldCC = $env:CC
Push-Location $projectRoot
try {
    switch ($Task) {
        'bootstrap' { $goArgs = @('run','./cmd/pauseproxy','bootstrap-ca','--config',$Config) }
        'start' { $goArgs = @('run','./cmd/pauseproxy','--config',$Config) }
        'test' { $goArgs = @('test','./...') }
        'race' {
            $env:CGO_ENABLED = '1'
            $compiler = Join-Path $projectRoot '.tools/w64devkit/bin/gcc.exe'
            if (Test-Path $compiler) { $env:CC = $compiler }
            elseif (-not (Get-Command gcc -ErrorAction SilentlyContinue)) { throw 'Run ./scripts/setup.ps1 -WithRaceCompiler first.' }
            $goArgs = @('test','-race','./...')
        }
        'build' { New-Item -ItemType Directory -Force dist | Out-Null; $goArgs = @('build','-o','dist/pauseproxy.exe','./cmd/pauseproxy') }
    }
    & $go @goArgs
    if ($LASTEXITCODE -ne 0) { throw "Go command failed ($LASTEXITCODE)" }
} finally { Pop-Location; $env:CGO_ENABLED = $oldCGO; $env:CC = $oldCC }
