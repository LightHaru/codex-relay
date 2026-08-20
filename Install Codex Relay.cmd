@echo off
setlocal

set "RELAY_REPOSITORY=%~dp0"
powershell.exe -NoProfile -ExecutionPolicy Bypass -File "%RELAY_REPOSITORY%scripts\install_windows.ps1" %*
set "RELAY_EXIT_CODE=%ERRORLEVEL%"

if not "%RELAY_EXIT_CODE%"=="0" (
  echo.
  echo Installation did not complete. The Microsoft Store ChatGPT app was not modified.
  pause
)

exit /b %RELAY_EXIT_CODE%
