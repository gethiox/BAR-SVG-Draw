@echo off
rem Builds into .\builds (git-ignored). No C compiler needed: raylib.dll is embedded.
setlocal
cd /d "%~dp0"
set CGO_ENABLED=0
if not exist builds mkdir builds
go build -trimpath -ldflags "-s -w -H=windowsgui" -o builds\bardraw.exe . || exit /b 1
go build -trimpath -ldflags "-s -w" -o builds\svgplan.exe .\cmd\svgplan || exit /b 1
echo Built builds\bardraw.exe and builds\svgplan.exe
echo bardraw keeps bardraw.json and the "drawings" folder next to the exe (builds\drawings).
