@echo off
setlocal

set "SCRIPT_DIR=%~dp0"
for %%I in ("%SCRIPT_DIR%..") do set "REPO_ROOT=%%~fI"

set "IMAGE_TAG=%~1"
if "%IMAGE_TAG%"=="" set "IMAGE_TAG=local-web-eyes:latest"

set "OUT_FILE=%~2"
if "%OUT_FILE%"=="" set "OUT_FILE=%REPO_ROOT%\dist\local-web-eyes-linux-amd64.tar.gz"

pushd "%REPO_ROOT%" >nul || exit /b 1
if not exist "dist" mkdir "dist"

set "TMP_TAR=%OUT_FILE%.tmp.tar"
echo [EXPORT] Saving "%IMAGE_TAG%"...
docker save -o "%TMP_TAR%" "%IMAGE_TAG%"
if errorlevel 1 (
    popd >nul
    exit /b 1
)

echo [COMPRESS] Writing "%OUT_FILE%"...
powershell -NoProfile -Command "$s=[IO.File]::OpenRead('%TMP_TAR%'); $d=[IO.File]::Create('%OUT_FILE%'); $g=New-Object IO.Compression.GZipStream($d,[IO.Compression.CompressionLevel]::Optimal); $s.CopyTo($g); $g.Dispose(); $d.Dispose(); $s.Dispose()"
set "EC=%ERRORLEVEL%"
if exist "%TMP_TAR%" del /f /q "%TMP_TAR%"

popd >nul
if not "%EC%"=="0" exit /b %EC%

echo [OK] Exported image archive: %OUT_FILE%
echo On target Linux server:
echo   docker load -i local-web-eyes-linux-amd64.tar.gz
exit /b 0
