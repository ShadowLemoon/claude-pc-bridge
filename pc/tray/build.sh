#!/usr/bin/env bash
# 编译 Windows 和 Linux 版本，输出到 dist/
set -euo pipefail
cd "$(dirname "$0")"
mkdir -p dist
# -H=windowsgui：编译成 GUI 程序，启动时不弹命令行窗口
GOOS=windows GOARCH=amd64 go build -trimpath -ldflags "-H=windowsgui -s -w" -o dist/claude-pc-bridge-tray.exe .
GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "-s -w" -o dist/claude-pc-bridge-tray .
ls -l dist
