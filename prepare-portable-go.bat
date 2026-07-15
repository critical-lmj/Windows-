@echo off
setlocal
cd /d %~dp0

set "GO_VERSION=1.20.14"
set "GO_ZIP=go%GO_VERSION%.windows-amd64.zip"
set "GO_URL=https://go.dev/dl/%GO_ZIP%"
set "RUNTIME_DIR=%~dp0.runtime"
set "ZIP_PATH=%RUNTIME_DIR%\%GO_ZIP%"

if exist "%RUNTIME_DIR%\go\bin\go.exe" (
  echo Portable Go already exists:
  "%RUNTIME_DIR%\go\bin\go.exe" version
  goto download_modules
)

if not exist "%RUNTIME_DIR%" mkdir "%RUNTIME_DIR%"

echo Downloading %GO_URL%
echo This step needs internet only once. The target server can stay offline later.
echo.

powershell -NoProfile -ExecutionPolicy Bypass -Command ^
  "$ErrorActionPreference='Stop'; [Net.ServicePointManager]::SecurityProtocol=[Net.SecurityProtocolType]::Tls12; Invoke-WebRequest -Uri '%GO_URL%' -OutFile '%ZIP_PATH%'"
if errorlevel 1 (
  echo.
  echo [ERROR] Download failed.
  echo You can manually download %GO_URL% and put it here:
  echo   %ZIP_PATH%
  echo Then run this script again.
  echo.
  pause
  exit /b 1
)

echo Extracting portable Go...
powershell -NoProfile -ExecutionPolicy Bypass -Command ^
  "$ErrorActionPreference='Stop'; Add-Type -AssemblyName System.IO.Compression.FileSystem; if (Test-Path '%RUNTIME_DIR%\go') { Remove-Item -Recurse -Force '%RUNTIME_DIR%\go' }; [System.IO.Compression.ZipFile]::ExtractToDirectory('%ZIP_PATH%', '%RUNTIME_DIR%')"
if errorlevel 1 (
  echo.
  echo [ERROR] Extract failed.
  echo.
  pause
  exit /b 1
)

echo.
echo Portable Go is ready:
"%RUNTIME_DIR%\go\bin\go.exe" version
echo.

:download_modules
echo Downloading Go module dependencies into .runtime\gopath ...
set "GOROOT=%RUNTIME_DIR%\go"
set "GOPATH=%RUNTIME_DIR%\gopath"
set "GOCACHE=%RUNTIME_DIR%\gocache"
set "PATH=%GOROOT%\bin;%PATH%"
set "GOPROXY=https://goproxy.cn,https://proxy.golang.org,direct"
"%RUNTIME_DIR%\go\bin\go.exe" mod download
if errorlevel 1 (
  echo.
  echo [ERROR] Module download failed.
  echo You can run this script again when network access is available.
  echo.
  pause
  exit /b 1
)

echo.
echo Portable Go and module cache are ready.
echo Now run build.bat to build TaskSchedulerManager.exe.
echo.
pause
