@echo off
setlocal
cd /d "%~dp0"
powershell.exe -NoLogo -NoProfile -ExecutionPolicy Bypass -File "%~dp0scripts\local.ps1" -Action refresh
if errorlevel 1 goto :failed
echo.
echo Refresh completed. Open http://localhost:4173
pause
exit /b 0

:failed
echo.
echo Refresh failed. Keep this window open and copy the error output.
pause
exit /b 1
