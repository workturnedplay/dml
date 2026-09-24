@echo off
rem 1. Prevent the current working directory from taking precedence over PATH, doesn't work with eg. "start go.exe"
set "NoDefaultCurrentDirectoryInExePath=1"

::if running as admin must get back to current dir:
:: 1. Change directory and store path BEFORE enabling delayed expansion.
::    While delayed expansion is disabled, '!' is treated as a literal character.
cd /d "%~dp0"
set "SCRIPT_DIR=%~dp0"

:: 2. Enable delayed expansion for script logic
setlocal enabledelayedexpansion

:: 3. Test: Use !SCRIPT_DIR! (safe), do NOT use %SCRIPT_DIR% or %~dp0 (unsafe)
echo Current DIR: "!SCRIPT_DIR!"

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

