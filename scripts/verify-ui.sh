#!/bin/bash
# 复验 Dashboard 修复：重建 → 重生成 → 启动 → /ui/ 探活 → 停止。
set -euo pipefail
cd "$(dirname "$0")/.."
export PATH="$HOME/toolchains/go/bin:$PATH"
export SSB_DIR=/tmp/ssb-e2e

go build -trimpath -o ssb ./cmd/ssb
go test ./internal/render/

./ssb gen
./ssb start
sleep 6   # 留时间给 sing-box 下载 UI zip

SECRET=$(python3 -c "import json;print(json.load(open('$SSB_DIR/data/state.json'))['settings']['clash_secret'])")
echo "==> GET /ui/"
curl -fsS -o /dev/null -w "  /ui/ -> HTTP %{http_code}\n" "http://127.0.0.1:19090/ui/"
echo "==> data/ui 内容:"
ls "$SSB_DIR/data/ui" | head -6
echo "==> 日志中 external ui 相关行:"
grep -iE "external ui" "$SSB_DIR/logs/sing-box.log" | tail -3 || true

./ssb stop
echo "Dashboard 复验通过 ✅"
