$ErrorActionPreference = "Stop"

function New-PngIconFromImage {
    param(
        [Parameter(Mandatory = $true)]
        [string]$SourceImage,
        [Parameter(Mandatory = $true)]
        [string]$IconPath
    )

    Add-Type -AssemblyName System.Drawing

    $source = [System.Drawing.Image]::FromFile($SourceImage)
    try {
        $cropSize = [Math]::Min($source.Width, $source.Height)
        $offsetX = [Math]::Floor(($source.Width - $cropSize) / 2)
        $offsetY = [Math]::Floor(($source.Height - $cropSize) / 3)
        if ($offsetY -lt 0) {
            $offsetY = 0
        }
        if ($offsetY -gt ($source.Height - $cropSize)) {
            $offsetY = $source.Height - $cropSize
        }

        $bitmap = New-Object System.Drawing.Bitmap 256, 256
        try {
            $graphics = [System.Drawing.Graphics]::FromImage($bitmap)
            try {
                $graphics.InterpolationMode = [System.Drawing.Drawing2D.InterpolationMode]::HighQualityBicubic
                $graphics.SmoothingMode = [System.Drawing.Drawing2D.SmoothingMode]::HighQuality
                $graphics.PixelOffsetMode = [System.Drawing.Drawing2D.PixelOffsetMode]::HighQuality
                $graphics.Clear([System.Drawing.Color]::Transparent)
                $graphics.DrawImage(
                    $source,
                    [System.Drawing.Rectangle]::new(0, 0, 256, 256),
                    [System.Drawing.Rectangle]::new([int]$offsetX, [int]$offsetY, [int]$cropSize, [int]$cropSize),
                    [System.Drawing.GraphicsUnit]::Pixel
                )
            }
            finally {
                $graphics.Dispose()
            }

            $memory = New-Object System.IO.MemoryStream
            try {
                $bitmap.Save($memory, [System.Drawing.Imaging.ImageFormat]::Png)
                $pngBytes = $memory.ToArray()
            }
            finally {
                $memory.Dispose()
            }
        }
        finally {
            $bitmap.Dispose()
        }
    }
    finally {
        $source.Dispose()
    }

    $file = [System.IO.File]::Open($IconPath, [System.IO.FileMode]::Create, [System.IO.FileAccess]::Write)
    try {
        $writer = New-Object System.IO.BinaryWriter($file)
        try {
            $writer.Write([UInt16]0)
            $writer.Write([UInt16]1)
            $writer.Write([UInt16]1)
            $writer.Write([byte]0)
            $writer.Write([byte]0)
            $writer.Write([byte]0)
            $writer.Write([byte]0)
            $writer.Write([UInt16]1)
            $writer.Write([UInt16]32)
            $writer.Write([UInt32]$pngBytes.Length)
            $writer.Write([UInt32]22)
            $writer.Write($pngBytes)
        }
        finally {
            $writer.Dispose()
        }
    }
    finally {
        $file.Dispose()
    }
}

$tmpRoot = Join-Path $PSScriptRoot ".tmp"
$cacheRoot = Join-Path $PSScriptRoot ".gocache"
$distRoot = Join-Path $PSScriptRoot "dist"
$iconPath = Join-Path $PSScriptRoot "app-icon.ico"
$sysoPath = Join-Path $PSScriptRoot "app-icon.syso"

New-Item -ItemType Directory -Force -Path $tmpRoot | Out-Null
New-Item -ItemType Directory -Force -Path $cacheRoot | Out-Null
New-Item -ItemType Directory -Force -Path $distRoot | Out-Null

$env:TMP = $tmpRoot
$env:TEMP = $tmpRoot
$env:TMPDIR = $tmpRoot
$env:GOTMPDIR = $tmpRoot
$env:GOCACHE = $cacheRoot

$sourceImage = $null

foreach ($preferredName in @("app-icon.ico", "app-icon.png", "app-icon.jpg", "app-icon.jpeg", "app-icon.bmp")) {
    $preferredPath = Join-Path $PSScriptRoot $preferredName
    if (Test-Path $preferredPath) {
        $sourceImage = Get-Item $preferredPath
        break
    }
}

if (-not $sourceImage) {
    $sourceImage = Get-ChildItem -Path $PSScriptRoot -File | Where-Object {
        $_.Extension -in @(".png", ".jpg", ".jpeg", ".bmp", ".ico") -and $_.FullName -ne $iconPath
    } | Sort-Object Name | Select-Object -First 1
}

if (-not $sourceImage) {
    throw "No image file found in project root for icon generation."
}

if ($sourceImage.Extension -ieq ".ico") {
    if ($sourceImage.FullName -ne $iconPath) {
        Copy-Item -Force $sourceImage.FullName $iconPath
    }
}
else {
    New-PngIconFromImage -SourceImage $sourceImage.FullName -IconPath $iconPath
}

$rsrc = (Get-Command rsrc.exe -ErrorAction Stop).Source
& $rsrc -ico $iconPath -o $sysoPath
if ($LASTEXITCODE -ne 0) {
    exit $LASTEXITCODE
}

& go build -ldflags "-H=windowsgui" -o (Join-Path $distRoot "vscode-portable-updater.exe") .
if ($LASTEXITCODE -ne 0) {
    exit $LASTEXITCODE
}
