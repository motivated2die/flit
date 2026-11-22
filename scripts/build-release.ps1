# Builds a release flit.exe with matching version numbers everywhere and the
# engine DLLs embedded. The release workflow (.github/workflows/release.yml)
# runs this same script, so a local build and a GitHub build of the same
# commit are made the same way (docs/RELEASING.md).
#
#   .\scripts\build-release.ps1 -Version 0.1.0
#   .\scripts\build-release.ps1 -Version 0.1.0-rc.1 -Out dist
#
# Output: <Out>\flit.exe and <Out>\SHA256SUMS.txt. The default output folder
# is outside the repository, so the exe isn't "portable" and installs itself
# when started.
param(
    [Parameter(Mandatory = $true)][ValidatePattern('^\d+\.\d+\.\d+(-[0-9A-Za-z.-]+)?$')][string]$Version,
    [string]$Out = (Join-Path $env:USERPROFILE "Desktop\flit-$Version")
)
$ErrorActionPreference = "Stop"
$Out = $ExecutionContext.SessionState.Path.GetUnresolvedProviderPathFromPSPath($Out)
$repo = Split-Path -Parent $PSScriptRoot
Push-Location $repo
try {
    $commit = git rev-parse --short HEAD 2>$null
    if ($LASTEXITCODE) { $commit = "unknown" }
    if (git status --porcelain 2>$null) {
        Write-Warning "The working tree has uncommitted changes: this build is not exactly commit $commit."
    }

    Write-Host "== engine DLLs"
    go run ./cmd/flitdev engine
    if ($LASTEXITCODE) { throw "fetching the engine failed" }

    Write-Host "== vet and test"
    go vet ./...
    if ($LASTEXITCODE) { throw "go vet failed" }
    go test ./...
    if ($LASTEXITCODE) { throw "go test failed" }

    $winVersion = ($Version -replace '-.*$') + ".0"
    Write-Host "== resources (icon, manifest, version $winVersion)"
    go run ./cmd/flitdev icon
    if ($LASTEXITCODE) { throw "making the icon failed" }
    go-winres make --in winres\winres.json --out cmd\flit\rsrc --arch amd64 `
        --product-version $winVersion --file-version $winVersion
    if ($LASTEXITCODE) { throw "go-winres failed (go install github.com/tc-hib/go-winres@v0.3.3)" }

    New-Item -ItemType Directory -Force $Out | Out-Null
    $exe = Join-Path $Out "flit.exe"
    Write-Host "== build $exe (v$Version, commit $commit)"
    go build -trimpath `
        -ldflags "-H=windowsgui -s -w -X github.com/motivated2die/flit/internal/app.Version=$Version" `
        -o $exe ./cmd/flit
    if ($LASTEXITCODE) { throw "go build failed" }

    $hash = (Get-FileHash $exe -Algorithm SHA256).Hash.ToLowerInvariant()
    Set-Content -Path (Join-Path $Out "SHA256SUMS.txt") -Value "$hash  flit.exe" -Encoding ascii
    Write-Host "== done: $exe ($([math]::Round((Get-Item $exe).Length / 1MB, 1)) MB)"
    Write-Host "   SHA-256 $hash"
} finally {
    Pop-Location
}
