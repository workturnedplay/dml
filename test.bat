@echo off
rem 1. Prevent the current working directory from taking precedence over PATH, doesn't work with eg. "start go.exe"
set "NoDefaultCurrentDirectoryInExePath=1"

setlocal enabledelayedexpansion

::if running as admin must get back to current dir:
cd /d %~dp0

call .\prebuildcheck.bat silent
if errorlevel 1 (
    echo.
    choice /c NY /m "%lintexe% found issues. Stop tests?"
    if errorlevel 2 goto :fail
)

go test -race ./...
echo Tests succeeded.
pause
goto :eof

:fail
@echo off
echo.
echo *** TESTS FAILED ***
pause
exit /b 1

