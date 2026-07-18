#!/bin/bash
# 一键验证：工具链 → 依赖 → 静态检查 → 单测 → 构建 → sing-box check → 无 root e2e。
# 用法: bash scripts/verify.sh
set -euo pipefail
cd "$(dirname "$0")/.."
ROOT=$(pwd)

# ---- 0. Go 工具链（装到用户目录，不污染系统）----
if ! command -v go >/dev/null 2>&1; then
  if [ ! -x "$HOME/toolchains/go/bin/go" ]; then
    echo "==> 下载 Go 1.25.9 (arm64) 到 ~/toolchains"
    mkdir -p "$HOME/toolchains"
    curl -fsSL https://go.dev/dl/go1.25.9.linux-arm64.tar.gz | tar -xz -C "$HOME/toolchains"
  fi
  export PATH="$HOME/toolchains/go/bin:$PATH"
fi
go version

# ---- 1. 依赖 / 格式 / 静态检查 ----
echo "==> go mod tidy"
go mod tidy
echo "==> gofmt"
UNFMT=$(gofmt -l . | grep -v '^vendor/' || true)
if [ -n "$UNFMT" ]; then gofmt -w $UNFMT; echo "已格式化: $UNFMT"; fi
echo "==> go vet"
go vet ./...

# ---- 2. 单测 ----
echo "==> go test"
go test ./...

# ---- 3. 构建 ----
echo "==> go build"
CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o ssb ./cmd/ssb
./ssb version

# ---- 4. e2e（不需要 root；用系统 sing-box 1.13.x 做 check 裁判）----
E2E=/tmp/ssb-e2e
echo "==> e2e at $E2E"
rm -rf "$E2E" && mkdir -p "$E2E"
export SSB_DIR="$E2E"

# 4.1 添加假节点 → 自动 gen + sing-box check（TUN 配置的合法性在这里被真实校验）
./ssb add "vless://5e3da52a-2d69-4e5e-b7ef-1e0e7a2e8b9c@203.0.113.10:443?encryption=none&flow=xtls-rprx-vision&security=reality&sni=www.apple.com&fp=chrome&pbk=SbVKOEMjK0sIlbwg4akyBg5mL5KZwwB-ed4eEE7YnRc&sid=6ba85179&type=tcp#e2e-vless" \
        "anytls://testpass@203.0.113.11:8443/?sni=example.org&insecure=1#e2e-anytls" \
        "hy2://pw@203.0.113.12:36712/?sni=hy.example.com&insecure=1#e2e-hy2"
test -f "$E2E/config.json"
echo "  config.json 生成且 check 通过 ✓"

# 4.2 关 TUN（无 root e2e），换端口避免与本机可能存在的服务冲突
python3 - "$E2E/data/state.json" <<'EOF'
import json, sys
p = sys.argv[1]
st = json.load(open(p))
st["settings"]["tun_enabled"] = False
st["settings"]["clash_listen"] = "127.0.0.1:19090"
st["settings"]["mixed_port"] = 12080
json.dump(st, open(p, "w"), ensure_ascii=False, indent=2)
EOF
./ssb gen

# 4.3 启动 → clash_api 探活 → mixed 端口探活 → 状态 → 停止
./ssb start
SECRET=$(python3 -c "import json;print(json.load(open('$E2E/data/state.json'))['settings']['clash_secret'])")
sleep 1
curl -fsS -H "Authorization: Bearer $SECRET" http://127.0.0.1:19090/version && echo "  clash_api ✓"
python3 - <<'EOF'
import socket
s = socket.create_connection(("127.0.0.1", 12080), timeout=3)
s.close()
print("  mixed 端口 ✓")
EOF
./ssb status
./ssb doctor || true   # doctor 在无 root 下对 TUN 权限报警属预期，此处已关 TUN
./ssb stop
echo "==> e2e 全部通过"

# ---- 5. TUN 配置合法性单独再验一次（开 TUN 只 gen/check，不启动）----
python3 - "$E2E/data/state.json" <<'EOF'
import json, sys
p = sys.argv[1]
st = json.load(open(p))
st["settings"]["tun_enabled"] = True
json.dump(st, open(p, "w"), ensure_ascii=False, indent=2)
EOF
./ssb gen
echo "==> TUN 配置 check 通过"

echo
echo "全部验证通过 ✅"
