@echo off
setlocal

set "SCRIPT_DIR=%~dp0"
for %%I in ("%SCRIPT_DIR%..") do set "REPO_ROOT=%%~fI"

set "IMAGE_TAG=%~1"
if "%IMAGE_TAG%"=="" set "IMAGE_TAG=local-web-eyes:latest"

pushd "%REPO_ROOT%" >nul || exit /b 1

docker info >nul 2>&1
if errorlevel 1 (
    echo [ERROR] Docker daemon is not running. Please start Docker Desktop first.
    popd >nul
    exit /b 1
)

echo [BUILD] Building Docker image "%IMAGE_TAG%" from "%REPO_ROOT%"...
docker build -f deployments\Dockerfile -t "%IMAGE_TAG%" .
set "EXIT_CODE=%ERRORLEVEL%"

popd >nul
if not "%EXIT_CODE%"=="0" (
    echo [ERROR] Docker build failed with exit code %EXIT_CODE%.
    exit /b %EXIT_CODE%
)

echo [OK] Built image: %IMAGE_TAG%
echo Run standalone HTTP MCP server:
echo   docker run --rm -p 127.0.0.1:8090:8090 -v web_eyes_data:/app/data --tmpfs /tmp:rw,noexec,nosuid,size=128m --read-only --cap-drop ALL "%IMAGE_TAG%"
exit /b 0
