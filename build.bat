@echo off
rem Builds bardraw.exe without a C compiler (raylib.dll is embedded by raylib-go).
setlocal
set CGO_ENABLED=0
go build -trimpath -ldflags "-s -w -H=windowsgui" -o bardraw.exe . || exit /b 1
echo Built bardraw.exe - put .svg files into the "drawings" folder and run it.
