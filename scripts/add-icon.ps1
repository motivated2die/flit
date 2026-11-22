<#
.SYNOPSIS
Fetches Tabler icons into internal\ui\icons, where they are embedded into flit.exe.

.DESCRIPTION
Icons come from one pinned Tabler release and are committed; nothing is
loaded at run time. Outline icons keep their name ("refresh"), filled ones
get "-filled" ("player-play-filled"). In code, an icon is its file name
without ".svg" (add a Glyph constant in internal\ui\widgets.go).

Browse: https://tabler.io/icons

.EXAMPLE
.\scripts\add-icon.ps1 refresh, folder
.\scripts\add-icon.ps1 -Filled player-play
#>
param(
    [Parameter(Mandatory, Position = 0)][string[]]$Name,
    [switch]$Filled
)

$ErrorActionPreference = 'Stop'
$Version = '3.49.0' # Tabler Icons, MIT (icons\LICENSE)

$dir = Join-Path $PSScriptRoot '..\internal\ui\icons'
foreach ($n in $Name) {
    $style, $file = 'outline', "$n.svg"
    if ($Filled) { $style, $file = 'filled', "$n-filled.svg" }
    Invoke-WebRequest -UseBasicParsing -Uri "https://unpkg.com/@tabler/icons@$Version/icons/$style/$n.svg" -OutFile (Join-Path $dir $file)
    Write-Host "added internal\ui\icons\$file"
}
Write-Host 'Rebuild to embed. go test ./internal/ui checks that every icon parses and draws.'
