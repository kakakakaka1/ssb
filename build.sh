#!/bin/sh
# 构建静态单二进制（在仓库根目录执行）。需要 Go 1.25+。
set -e
cd "$(dirname "$0")"
CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o ssb ./cmd/ssb
echo "构建完成: $(pwd)/ssb"
