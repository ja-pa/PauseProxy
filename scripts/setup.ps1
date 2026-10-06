param([switch]$WithRaceCompiler)
$ErrorActionPreference = 'Stop'
$projectRoot = Split-Path $PSScriptRoot -Parent
$toolsDir = Join-Path $projectRoot '.tools'
New-Item -ItemType Directory -Force $toolsDir | Out-Null
if (-not (Test-Path (Join-Path $toolsDir 'go/bin/go.exe'))) {
    $archive = Join-Path $toolsDir 'go.zip'
    Invoke-WebRequest 'https://go.dev/dl/go1.27.1.windows-amd64.zip' -OutFile $archive
    if ((Get-FileHash $archive -Algorithm SHA256).Hash.ToLower() -ne 'a3911b5e0e1b1053f25ed0675f4c1c6aad1e2bfcf253df2b9be4caabd2edd95d') { throw 'Go checksum mismatch' }
    Expand-Archive -LiteralPath $archive -DestinationPath $toolsDir -Force
}
if ($WithRaceCompiler -and -not (Test-Path (Join-Path $toolsDir 'w64devkit/bin/gcc.exe'))) {
    $archive = Join-Path $toolsDir 'w64devkit.7z.exe'
    Invoke-WebRequest 'https://github.com/skeeto/w64devkit/releases/download/v2.10.0/w64devkit-x64-2.10.0.7z.exe' -OutFile $archive
    if ((Get-FileHash $archive -Algorithm SHA256).Hash.ToLower() -ne '18d0a4c71a166f8401ab6305781bec5882b40b5e06ba9807c61cb5f3b3c6325e') { throw 'Compiler checksum mismatch' }
    & $archive -y "-o$toolsDir" | Out-Null
    if ($LASTEXITCODE -ne 0) { throw 'Compiler extraction failed' }
}
& (Join-Path $toolsDir 'go/bin/go.exe') version
Write-Host 'Toolchain is local to .tools; system PATH was not changed.'
