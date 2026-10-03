param([string]$TargetRoot = (Split-Path -Parent $PSScriptRoot))
$ErrorActionPreference = 'Stop'
$root = (Resolve-Path -LiteralPath $TargetRoot).Path
$manifest = Get-Content -LiteralPath (Join-Path $PSScriptRoot 'manifest.json') -Raw | ConvertFrom-Json
# Check all files before restoring anything; preserve subsequent user edits.
foreach ($entry in $manifest) {
    $target = Join-Path $root $entry.Path
    if (-not (Test-Path -LiteralPath $target)) { throw "Missing target: $target" }
    $hash = (Get-FileHash -LiteralPath $target -Algorithm SHA256).Hash
    if ($hash -ne $entry.ModifiedHash -and $hash -ne $entry.OriginalHash) {
        throw "File changed after this update: $target"
    }
    if ($entry.OriginalHash) {
        $source = Join-Path $PSScriptRoot $entry.Backup
        if ((Get-FileHash -LiteralPath $source -Algorithm SHA256).Hash -ne $entry.OriginalHash) {
            throw "Backup checksum mismatch: $source"
        }
    }
}
foreach ($entry in $manifest) {
    $target = Join-Path $root $entry.Path
    if ($entry.OriginalHash) {
        Copy-Item -LiteralPath (Join-Path $PSScriptRoot $entry.Backup) -Destination $target -Force
        if ((Get-FileHash -LiteralPath $target -Algorithm SHA256).Hash -ne $entry.OriginalHash) {
            throw "Restore checksum mismatch: $target"
        }
    } else {
        Remove-Item -LiteralPath $target
    }
    Write-Output "Restored: $($entry.Path)"
}
