@echo off
rem 1. Prevent the current working directory from taking precedence over PATH, doesn't work with eg. "start go.exe"
set "NoDefaultCurrentDirectoryInExePath=1"

::if running as admin must get back to current dir:
:: 1. Change directory and store path BEFORE enabling delayed expansion.
::    While delayed expansion is disabled, '!' is treated as a literal character.
setlocal disabledelayedexpansion
:: ^ needed if called by a 'call me.bat' command where it's already enabled in parent!
cd /d "%~dp0"
set "SCRIPT_DIR=%~dp0"

:: 2. Enable delayed expansion for script logic
setlocal enabledelayedexpansion

:: 3. Test: Use !SCRIPT_DIR! (safe), do NOT use %SCRIPT_DIR% or %~dp0 (unsafe)
echo Current DIR: "!SCRIPT_DIR!"

call .\prebuildcheck.bat silent
if errorlevel 1 (
    echo.
    choice /c NY /m "Vet/lint found issues. Stop tests?"
    if errorlevel 2 goto :fail
)

go test -race ./...
if errorlevel 1 goto :fail
echo Those tests succeeded.

echo Compiling firewall-requiring ^(ie. Portmaster-ready^) test binary...
rem The tag includes wire_firewalled_test.go, which the run above does not compile.
go test -race -c -tags portmasterFirewalled -o dml_fw_test.exe .
if errorlevel 1 (
    echo Compilation failed.
    goto :fail
)

echo Running only the firewall-requiring ^(loopback TCP^) tests...
.\dml_fw_test.exe -test.run "^TestFWNeeded"
if errorlevel 1 (
    echo You will have to allow "127.0.0.1 tcp/49152-65535" in firewall ^(eg. Portmaster^), both IN and OUT, for dml_fw_test.exe for these tests to pass
    goto :fail
)

echo All tests succeeded.
pause
goto :eof

:fail
@echo off
echo.
echo *** TESTS FAILED ***
pause
exit /b 1

