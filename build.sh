#!/bin/sh
# Builds into ./builds (git-ignored). The overlay is Windows-only, so on
# Linux/macOS this builds svgplan natively and cross-compiles bardraw.exe.
set -eu
cd "$(dirname "$0")"
export CGO_ENABLED=0
mkdir -p builds
go build -trimpath -ldflags "-s -w" -o builds/svgplan ./cmd/svgplan
GOOS=windows GOARCH=amd64 go build -trimpath -ldflags "-s -w -H=windowsgui" -o builds/bardraw.exe .
echo "Built builds/svgplan and builds/bardraw.exe (Windows x64)"
