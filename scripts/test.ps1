param([int]$Count = 1, [switch]$Race)
$ErrorActionPreference = 'Stop'
if ($Count -lt 1) { throw 'Count must be positive' }
$projectRoot = Split-Path -Parent $PSScriptRoot
$tmpRoot = Join-Path $projectRoot 'dist\.build\tmp'
$cacheRoot = Join-Path $projectRoot 'dist\.build\gocache'
New-Item -ItemType Directory -Force $tmpRoot,$cacheRoot | Out-Null
$env:TEMP = $tmpRoot
$env:TMP = $tmpRoot
$env:TMPDIR = $tmpRoot
$env:GOTMPDIR = $tmpRoot
$env:GOCACHE = $cacheRoot
$goArgs = @('test', "-count=$Count")
if ($Race) { $env:CGO_ENABLED = '1'; $goArgs += '-race' }
$goArgs += './...'
Push-Location $projectRoot
try {
    & go @goArgs
    if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
}
finally { Pop-Location }
