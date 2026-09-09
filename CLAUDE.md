# ssb 开发说明（给 Claude Code）

Go 单二进制：分享链接/订阅 → sing-box 1.14 config.json（TUN + FakeIP + 官方 API 服务）→ 托管 sing-box 进程。TUI(bubbletea v1) + CLI 双入口，Linux / Windows 通用。

## 构建与测试

```bash
export PATH=$HOME/toolchains/go/bin:$PATH   # Go 装在 ~/toolchains/go；本机另有 sing-box 1.14.0 in PATH
go build -o ssb ./cmd/ssb && go test ./... && gofmt -l .
GOOS=windows GOARCH=amd64 go vet ./... && CGO_ENABLED=0 GOOS=windows go build -o /dev/null ./cmd/ssb   # Windows 只能交叉编译
```

## 验证（改 render / sbx 后必做）

- `SSB_DIR=/tmp/ssb-e2e ./ssb add "<链接>"` 或 `./ssb gen`：触发生成 + 本机 `sing-box check`，这是 1.14 字段名的最终裁判，别只信单测。
- check 退出码 0 也可能带弃用 WARN，而且规则集/面板下载、隐式 HTTP 客户端一类问题只在真正 run 时暴露，所以要跑一次真实启动看日志：把 `$SSB_DIR/data/state.json` 的 `tun_enabled` 改 false（无 root 可跑；顺手把 `dashboard_off` 改 false 可连面板一起验）→ `./ssb start` → `./ssb status` 应显示"API 服务: 可达" → curl `http://127.0.0.1:9090/dashboard/` 期望 200 → `sing-box api group show PROXY --url http://127.0.0.1:9090 --secret <api_secret>` → `./ssb stop` → `grep -E 'WARN|FATAL' logs/sing-box.log` 应为空。
- **不要**在这台远程 VM 上真开 TUN / auto_route（会断 SSH）；TUN 配置合法性用 `./ssb gen` 的 check 验证即可。
- Windows 端的进程管理 / TUN / doctor 在这里无法实测，改动后需在 Win11 上过一遍：管理员运行 → start → status → 关终端再开 status 仍在运行 → stop。

## 结构

- cmd/ssb — 手写子命令分发；internal/tui — 四页签（tui.go 模型/按键、view.go 渲染、settings.go 设置表），改配置统一走 changeAndRegen，服务运行时置 dirty、按 r 重启
- internal/app — CLI/TUI 共用业务层；Generate() 带 check + 回滚
- internal/link — 分享链接解析（每协议一文件 + 金样例测试）；internal/sub — 订阅拉取（UA 退避 + 三格式探测）；internal/clash2sb — Clash YAML → outbound
- internal/profile — state.json 与 Dirs（所有路径）；internal/render — config.json 生成；internal/sbx — 内核定位/下载/check/进程管理/API 客户端/doctor
- 平台相关代码只在：`sbx/sbx_linux.go` 与 `sbx/sbx_windows.go`（同一组函数，清单在 linux 文件顶部）、`render/ipv6_linux.go` 与 `ipv6_other.go`、`profile.Dirs.SingboxBin()`（.exe 后缀）、`render.supportsAutoRedirect`。别在别处加 GOOS 分支。

## 关键设计决定（别无意撤销）

- 生成的配置永远先 `sing-box check` 再落地；工具绝不改系统文件（resolved 等只打印建议）；出错信息用中文；提示里指代本程序用 `sbx.Self`（./ssb 或 .\ssb），不要硬编码。
- 官方 API 服务（`services: [{type: api}]`）常驻，TUI 切节点/查询走内核自带的 `sing-box api` 命令行，ssb 不引入 gRPC 依赖；存活探测是对监听地址的一次 HTTP GET，任何响应即存活。
- clash_api 已整体移除（用户决定）：不生成 `clash_mode` 规则，官方面板的模式切换与连接跟踪不可用，分流模式由设置页「路由模式」静态决定。除非用户要求，不要加回。
- 1.14 的 `http_clients` 必须显式声明并被规则集、面板的 `http_client` 引用（global 模式开面板也要声明），否则内核回退到已弃用的隐式客户端；`detour` 只在 PROXY 时写，写到裸 direct 会被内核拒绝。
- 规则集源默认 jsdelivr 直连：远程规则集无缓存时首次拉取失败 = sing-box 启动失败，默认源不能依赖代理。github 源套镜像前缀，两者内容同为 MetaCubeX/meta-rules-dat@sing。`render.RuleSetSourceText` 负责状态页 / `ssb status` 的显示。
- IPv6 `auto`：Linux 用 procfs + netlink 探测策略路由能力，探不到就全程纯 IPv4（不配 v6 地址、不开 strict_route、route_address 限 IPv4、AAAA 返回空）；其他平台只看 IPv6 栈是否可用。
- Windows：sing-box 以 DETACHED_PROCESS + 新进程组启动（关终端不会连带杀掉），stop 是 TerminateProcess；`auto_redirect` 仅 Linux，生成时省略、设置页隐藏；内核包是 zip，解压 sing-box.exe 与同包 dll，wintun 已内嵌不需单独文件。
