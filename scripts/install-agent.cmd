@echo off
rem Install or update NextGaming on a gaming PC. Run as Administrator from the
rem folder that contains nextgaming-agent.exe and nextgaming-client.exe.
rem Usage: install-agent.cmd SERVER-IP CLUB-KEY
if "%~2"=="" (
  echo Usage: install-agent.cmd SERVER-IP CLUB-KEY
  echo Example: install-agent.cmd 192.168.1.10 K7P2-9QXA-M3TD
  exit /b 2
)
set DEST=%ProgramFiles%\NextGaming
if not exist "%DEST%" mkdir "%DEST%"

rem Stop an older version first so its files can be replaced.
if exist "%DEST%\nextgaming-agent.exe" "%DEST%\nextgaming-agent.exe" uninstall >nul 2>&1
taskkill /F /IM nextgaming-agent.exe >nul 2>&1
taskkill /F /IM nextgaming-client.exe >nul 2>&1
timeout /t 2 /nobreak >nul

copy /Y "%~dp0nextgaming-agent.exe" "%DEST%\" >nul || goto :fail
copy /Y "%~dp0nextgaming-client.exe" "%DEST%\" >nul || goto :fail
"%DEST%\nextgaming-agent.exe" install -server %1 -key %2 || goto :fail
echo.
echo Done. This PC will appear in the admin panel within a few seconds.
exit /b 0
:fail
echo Installation failed. Are you running as Administrator?
exit /b 1
