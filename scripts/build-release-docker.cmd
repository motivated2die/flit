@echo off
setlocal enabledelayedexpansion

REM Builds a release flit.exe inside a Docker container (no local Go install,
REM admin rights, or PowerShell script signing needed) and copies flit.exe and
REM SHA256SUMS.txt out to the output folder.
REM
REM   scripts\build-release-docker.cmd 0.2.3-dev.1
REM   scripts\build-release-docker.cmd 0.2.3-dev.1 dist
REM   set FLIT_DEBUG_BUILD=1 && scripts\build-release-docker.cmd 0.2.3-dev.1

if "%~1"=="" (
    echo Usage: %~nx0 ^<version^> [out-dir]
    echo Example: %~nx0 0.2.3-dev.1
    exit /b 2
)

set "VERSION=%~1"
set "OUT=%~2"
if "%OUT%"=="" set "OUT=%USERPROFILE%\Desktop\flit-%VERSION%"
if "%FLIT_DOCKER_IMAGE%"=="" set "FLIT_DOCKER_IMAGE=golang:1.27-bookworm"
if "%FLIT_SKIP_VET%"=="" set "FLIT_SKIP_VET=0"

for %%I in ("%~dp0..") do set "REPO=%%~fI"

REM Strip pre-release suffix (-...) for Windows 4-part file/product version.
for /f "tokens=1 delims=-" %%A in ("%VERSION%") do set "WIN_VERSION=%%A.0"

set "COMMIT=unknown"
for /f "usebackq tokens=*" %%C in (`git -C "%REPO%" rev-parse --short HEAD 2^>nul`) do set "COMMIT=%%C"

set "OUT_NAME=flit.exe"
set "LDFLAGS=-H=windowsgui -s -w -X github.com/motivated2die/flit/internal/app.Version=%VERSION%"
if "%FLIT_DEBUG_BUILD%"=="1" (
    set "OUT_NAME=flit-debug.exe"
    set "LDFLAGS=-X github.com/motivated2die/flit/internal/app.Version=%VERSION%"
)

set "CONTAINER=flit-build-%RANDOM%-%RANDOM%"

echo == starting build container %CONTAINER% (%FLIT_DOCKER_IMAGE%)
docker run ^
    --name "%CONTAINER%" ^
    -v "%REPO%:/src:ro" ^
    -v "flit-go-mod:/go/pkg/mod" ^
    -v "flit-go-build:/root/.cache/go-build" ^
    -e "GOTOOLCHAIN=auto" ^
    -e "VERSION=%VERSION%" ^
    -e "WIN_VERSION=%WIN_VERSION%" ^
    -e "COMMIT=%COMMIT%" ^
    -e "OUT_NAME=%OUT_NAME%" ^
    -e "LDFLAGS=%LDFLAGS%" ^
    -e "SKIP_VET=%FLIT_SKIP_VET%" ^
    "%FLIT_DOCKER_IMAGE%" ^
    sh -c "tr -d '\r' < /src/scripts/docker-build.sh | sh -s"
if errorlevel 1 (
    docker rm -f "%CONTAINER%" >nul 2>&1
    exit /b 1
)

if not exist "%OUT%" mkdir "%OUT%"
echo == copying %OUT_NAME% and SHA256SUMS.txt to %OUT%
docker cp "%CONTAINER%:/out/%OUT_NAME%" "%OUT%\%OUT_NAME%"
if errorlevel 1 (
    docker rm -f "%CONTAINER%" >nul 2>&1
    exit /b 1
)
docker cp "%CONTAINER%:/out/SHA256SUMS.txt" "%OUT%\SHA256SUMS.txt"
if errorlevel 1 (
    docker rm -f "%CONTAINER%" >nul 2>&1
    exit /b 1
)

docker rm -f "%CONTAINER%" >nul 2>&1
echo == done: %OUT%\%OUT_NAME%
type "%OUT%\SHA256SUMS.txt"
endlocal
