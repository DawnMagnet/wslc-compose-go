@echo off
rem Drop-in shim for cmd.exe: place this directory BEFORE "C:\Program Files\WSL"
rem in PATH so that `wslc compose ...` works everywhere, including scripts.
if /I "%~1"=="compose" (
  for /f "tokens=1,* delims= " %%a in ("%*") do wslc-compose.exe --wslc "%ProgramFiles%\WSL\wslc.exe" %%b
  exit /b %ERRORLEVEL%
)
"%ProgramFiles%\WSL\wslc.exe" %*
exit /b %ERRORLEVEL%
