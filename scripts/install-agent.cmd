@echo off
rem Install NextGaming on a gaming PC. Run as Administrator from the folder
rem that contains nextgaming-agent.exe and nextgaming-client.exe.
rem Usage: install-agent.cmd SERVER-IP CLUB-KEY
if "%~2"=="" (
  echo Usage: install-agent.cmd SERVER-IP CLUB-KEY
  echo Example: install-agent.cmd 192.168.1.10 K7P2-9QXA-M3TD
  exit /b 2
)
set DEST=%ProgramFiles%\NextGaming
if not exist "%DEST%" mkdir "%DEST%"
copy /Y "%~dp0nextgaming-agent.exe" "%DEST%\" >nul || goto :fail
copy /Y "%~dp0nextgaming-client.exe" "%DEST%\" >nul || goto :fail
"%DEST%\nextgaming-agent.exe" uninstall >nul 2>&1
"%DEST%\nextgaming-agent.exe" install -server %1 -key %2 || goto :fail
echo.
echo Done. This PC will appear in the admin panel within a few seconds.
exit /b 0
:fail
echo Installation failed. Are you running as Administrator?
exit /b 1
