# Builds a release flit.exe inside a Docker container (no local Go install or
# admin rights needed) and copies flit.exe + SHA256SUMS.txt out to -Out.
# Mirrors scripts\build-release.ps1: embeds the pinned transcribe.cpp CPU DLLs,
# runs go vet for Windows, stamps the Windows icon/manifest/version resource
# via go-winres, and cross-compiles with CGO_ENABLED=0.
#
#   .\scripts\build-release-docker.ps1 -Version 0.1.0
#   .\scripts\build-release-docker.ps1 -Version 0.1.0-rc.1 -Out dist
#   .\scripts\build-release-docker.ps1 -Version 0.1.0 -DebugBuild
#
# Output: <Out>\flit.exe (or flit-debug.exe) and <Out>\SHA256SUMS.txt.
# Named Docker volumes (flit-go-mod, flit-go-build) cache Go modules and build
# artifacts across runs. On machines where unsigned .ps1 scripts are blocked,
# use scripts\build-release-docker.cmd instead.
param(
    [Parameter(Mandatory = $true)][ValidatePattern('^\d+\.\d+\.\d+(-[0-9A-Za-z.-]+)?$')][string]$Version,
    [string]$Out = (Join-Path $env:USERPROFILE "Desktop\flit-$Version"),
    [string]$Image = "golang:1.27-bookworm",
    [switch]$SkipVet,
    [switch]$DebugBuild
)
$ErrorActionPreference = "Stop"
$Out = $ExecutionContext.SessionState.Path.GetUnresolvedProviderPathFromPSPath($Out)
$repo = Split-Path -Parent $PSScriptRoot

if (-not (Get-Command docker -ErrorAction SilentlyContinue)) {
    throw "docker was not found on PATH. Start Docker Desktop and try again."
}

Push-Location $repo
try {
    $commit = git rev-parse --short HEAD 2>$null
    if ($LASTEXITCODE) { $commit = "unknown" }
    if (git status --porcelain 2>$null) {
        Write-Warning "The working tree has uncommitted changes: this build is not exactly commit $commit."
    }

    $winVersion = ($Version -replace '-.*$') + ".0"
    $outName = if ($DebugBuild) { "flit-debug.exe" } else { "flit.exe" }
    $ldflags = if ($DebugBuild) {
        "-X github.com/motivated2die/flit/internal/app.Version=$Version"
    } else {
        "-H=windowsgui -s -w -X github.com/motivated2die/flit/internal/app.Version=$Version"
    }

    $containerName = "flit-build-" + [guid]::NewGuid().ToString("N").Substring(0, 12)
    $skipVetVal = if ($SkipVet) { "1" } else { "0" }

    Write-Host "== starting build container $containerName ($Image)"
    try {
        & docker run `
            --name $containerName `
            -v "${repo}:/src:ro" `
            -v "flit-go-mod:/go/pkg/mod" `
            -v "flit-go-build:/root/.cache/go-build" `
            -e "GOTOOLCHAIN=auto" `
            -e "VERSION=$Version" `
            -e "WIN_VERSION=$winVersion" `
            -e "COMMIT=$commit" `
            -e "OUT_NAME=$outName" `
            -e "LDFLAGS=$ldflags" `
            -e "SKIP_VET=$skipVetVal" `
            $Image `
            sh -c "tr -d '\r' < /src/scripts/docker-build.sh | sh -s"
        if ($LASTEXITCODE) { throw "container build failed (exit code $LASTEXITCODE)" }

        New-Item -ItemType Directory -Force $Out | Out-Null
        $exe = Join-Path $Out $outName
        Write-Host "== copying $outName to $exe"
        & docker cp "${containerName}:/out/$outName" $exe
        if ($LASTEXITCODE) { throw "docker cp failed" }

        $hash = (Get-FileHash $exe -Algorithm SHA256).Hash.ToLowerInvariant()
        Set-Content -Path (Join-Path $Out "SHA256SUMS.txt") -Value "$hash  $outName" -Encoding ascii
        Write-Host "== done: $exe ($([math]::Round((Get-Item $exe).Length / 1MB, 1)) MB)"
        Write-Host "   SHA-256 $hash"
    } finally {
        & docker rm -f $containerName 2>$null | Out-Null
    }
} finally {
    Pop-Location
}
