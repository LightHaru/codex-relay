@echo off
setlocal

set "ROUTER_REPOSITORY=%~dp0"
powershell.exe -NoProfile -ExecutionPolicy Bypass -File "%ROUTER_REPOSITORY%scripts\install_windows.ps1" %*
set "ROUTER_EXIT_CODE=%ERRORLEVEL%"

if not "%ROUTER_EXIT_CODE%"=="0" (
  echo.
  echo Installation did not complete. The Microsoft Store ChatGPT app was not modified.
  pause
)

exit /b %ROUTER_EXIT_CODE%
