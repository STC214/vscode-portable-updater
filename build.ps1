# Stable project-root entry point; implementation lives under scripts/.
$ErrorActionPreference = 'Stop'
& (Join-Path $PSScriptRoot 'scripts\build.ps1') @args
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
