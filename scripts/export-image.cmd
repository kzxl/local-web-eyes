@echo off
setlocal

set "SCRIPT_DIR=%~dp0"
for %%I in ("%SCRIPT_DIR%..") do set "REPO_ROOT=%%~fI"

set "IMAGE_TAG=%~1"
if "%IMAGE_TAG%"=="" set "IMAGE_TAG=local-web-eyes:latest"

set "OUT_FILE=%~2"
if "%OUT_FILE%"=="" set "OUT_FILE=%REPO_ROOT%\dist\local-web-eyes-linux-amd64.tar"

pushd "%REPO_ROOT%" >nul || exit /b 1
if not exist "dist" mkdir "dist"

echo [EXPORT] Saving "%IMAGE_TAG%" to "%OUT_FILE%"...
docker save -o "%OUT_FILE%" "%IMAGE_TAG%"
set "EC=%ERRORLEVEL%"

popd >nul
if not "%EC%"=="0" exit /b %EC%

echo [OK] Exported image tar: %OUT_FILE%
echo On target Linux server:
echo   docker load -i local-web-eyes-linux-amd64.tar
exit /b 0
