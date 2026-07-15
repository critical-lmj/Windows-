@echo off
setlocal EnableDelayedExpansion
cd /d %~dp0

set "LOCAL_GO=%~dp0.runtime\go\bin\go.exe"
if exist "%LOCAL_GO%" (
  set "GOROOT=%~dp0.runtime\go"
  set "GOPATH=%~dp0.runtime\gopath"
  set "GOCACHE=%~dp0.runtime\gocache-build"
  set "PATH=%~dp0.runtime\go\bin;%PATH%"
  set "GOEXE=%LOCAL_GO%"
) else (
  where go >nul 2>nul
  if errorlevel 1 (
    echo [ERROR] Cannot find portable Go or system Go.
    echo.
    echo Expected portable Go:
    echo   %LOCAL_GO%
    echo.
    echo Run prepare-portable-go.bat on a machine with internet,
    echo or copy a Go 1.20.x Windows amd64 ZIP into .runtime and extract it as .runtime\go.
    echo.
    pause
    exit /b 1
  )
  set "GOEXE=go"
)

echo Building TaskSchedulerManager.exe and TaskSchedulerManager-debug.exe ...
"%GOEXE%" version
set "OUTPUT=TaskSchedulerManager.exe"
"%GOEXE%" build -buildvcs=false -trimpath -ldflags="-H windowsgui" -o "!OUTPUT!" .
if errorlevel 1 (
  echo.
  echo [WARN] TaskSchedulerManager.exe may be running and locked.
  echo Building timestamped executable instead...
  for /f %%a in ('powershell -NoProfile -Command "Get-Date -Format yyyyMMdd-HHmmss"') do set "BUILD_STAMP=%%a"
  set "OUTPUT=TaskSchedulerManager-!BUILD_STAMP!.exe"
  "%GOEXE%" build -buildvcs=false -trimpath -ldflags="-H windowsgui" -o "!OUTPUT!" .
)
if errorlevel 1 (
  echo.
  echo [ERROR] Build failed. Please check the error above.
  echo.
  pause
  exit /b 1
)

set "DEBUG_OUTPUT=TaskSchedulerManager-debug.exe"
"%GOEXE%" build -buildvcs=false -trimpath -o "!DEBUG_OUTPUT!" .
if errorlevel 1 (
  echo.
  echo [ERROR] Debug build failed. Please check the error above.
  echo.
  pause
  exit /b 1
)

echo.
echo Build complete:
echo   %~dp0!OUTPUT!
echo   %~dp0!DEBUG_OUTPUT!
echo.
pause
