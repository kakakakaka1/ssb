# ssb 开发说明（给 Claude Code）

Go 单二进制工具：解析代理分享链接/订阅 → 生成 sing-box 1.14 config.json（TUN+FakeIP+官方 API 服务）→ 托管 sing-box 进程。TUI(bubbletea v1) + CLI 双入口。

## 构建与测试

```bash
# Go 工具链装在 ~/toolchains/go（不污染系统）；本机另有 sing-box 1.14.0 in PATH
export PATH=$HOME/toolchains/go/bin:$PATH
go build -o ssb ./cmd/ssb
go test ./...
gofmt -l .   # 提交前保持干净
GOOS=windows GOARCH=amd64 go vet ./... && CGO_ENABLED=0 GOOS=windows go build -o /dev/null ./cmd/ssb   # Windows 只能交叉编译校验
```

## 验证（重要）

- 任何 render 改动后：`SSB_DIR=/tmp/ssb-e2e ./ssb add "<链接>"` 会触发 gen + 本机 `sing-box check`——这是 1.14 字段名的最终裁判，不要只信单测。
- **check 不够**：弃用告警只是 WARN，退出码仍是 0；而且有一类（规则集下载、隐式默认 HTTP 客户端）只在真正 `sing-box run` 时才打印。所以改完 render 一定要跑一次
  `timeout 20 sing-box run -c <config>` 看日志里有没有 WARN/FATAL——1.14 适配里的两个坑（detour 指向裸 direct 被拒、不写 http_client 会走 PROXY 而不是直连）都只能这样发现。
- 无 root e2e：把 `$SSB_DIR/data/state.json` 的 `tun_enabled` 改 false（顺手把 `dashboard_off` 改 false 可一起验面板）→ `./ssb start` → `sing-box api version --url http://127.0.0.1:9090 --secret <secret>`、curl `http://127.0.0.1:9090/dashboard/` 期望 200 → `./ssb status` 应显示"API 服务: 可达" → `./ssb stop`。secret 在 state.json 的 `api_secret`。
- TUN 配置合法性用 `./ssb gen`（check 不需要 root）验证即可；**不要**在这台远程 VM 上真开 TUN/auto_route（会断 SSH）。

## 结构

- internal/link — 分享链接解析器（每协议一文件 + 金样例测试）；build.go 里的 TLSBlock/TransportBlock 被 clash2sb 复用
- internal/sub — 订阅拉取（UA 退避：sing-box→clash.meta→v2rayN）+ 三格式探测
- internal/clash2sb — Clash YAML proxies → sing-box outbound
- internal/profile — state.json（订阅/手动节点/设置）；Dirs 集中管理所有路径
- internal/render — config.json 生成（锁 1.14 语法：新版 DNS type 字段、rule actions、rule-set remote + http_clients、`services` 里的 api 服务 + dashboard 选项）
- internal/sbx — 内核定位/下载/check/进程管理（setsid+pidfile）/doctor/API 服务客户端（复用内核自带的 `sing-box api` 命令行，ssb 不引入 gRPC 依赖；存活探测是对 API 监听地址的一次普通 HTTP 请求）
- internal/app — CLI/TUI 共用的业务层；Generate() 带 check+回滚
- cmd/ssb — 手写子命令分发（无 cobra）；internal/tui — 四页签（tui.go 模型/按键、view.go 渲染、settings.go 设置表）；改动配置的操作统一走 changeAndRegen，服务运行时置 dirty、按 r 重启

## Windows 兼容

- 平台相关代码只在四处：`internal/sbx/sbx_linux.go` / `sbx_windows.go`（进程管理、权限、doctor 平台项、启动失败提示；两个文件提供同一组函数，清单在 sbx_linux.go 顶部）、`internal/render/ipv6_linux.go` / `ipv6_other.go`（IPv6 探测）、`profile.Dirs.SingboxBin()`（.exe 后缀）、`render.supportsAutoRedirect`（auto_redirect 仅 Linux，设置页也按平台隐藏）。其余全是跨平台代码，别在别处加 GOOS 分支。
- 本机是 Linux，Windows 只能交叉编译 + vet；进程管理 / TUN / doctor 的 Windows 行为没法在这里实测，改动这些后要在真机 Win11 上过一遍：管理员终端 `.\ssb start` → `status` → 关终端重开 `status` 仍在运行 → `stop`。
- Windows 上 stop 是 TerminateProcess（没有 SIGTERM 可发）；sing-box 起的是 DETACHED_PROCESS + 新进程组，别改成共享控制台，否则关终端会连带杀掉内核。
- 提示文案里指代本程序用 `sbx.Self`（./ssb 或 .\ssb），不要硬编码 `./ssb`。

## 面板 / API

- clash_api 已整体移除（用户决定，不兼容旧版 state.json 的 clash_* 字段）。网页面板是 sing-box 1.14 官方 Dashboard：由 `services: [{type: api, dashboard: {...}}]` 自动下载到 data/dashboard/ 并在 `/dashboard/` 提供；dashboard 的 `http_client` 必须显式指向顶层 http_clients（global 模式也要声明），否则内核回退到已弃用的隐式客户端。
- 1.14 的 API 服务把 Clash 模式（Rule/Global/Direct）委托给 clash_api，没有 clash_api 时 `clash_mode` 规则永远不匹配、面板里的模式开关也不可用——所以不再生成 clash_mode 规则，分流模式由设置页「路由模式」静态决定。别为了这个开关把 clash_api 加回来，除非用户要求。

- 规则集源（设置 `ruleset_source`）：默认 jsdelivr 直连（testingcf），可选 github（raw.githubusercontent.com，套镜像前缀）；两者都是 MetaCubeX/meta-rules-dat@sing 的同一份内容。远程规则集无缓存时首次拉取失败 = sing-box 启动失败（rule_set_remote.go "initial rule-set"），所以默认源不能依赖代理；`render.RuleSetSourceText` 负责把当前源和传输路径显示在状态页 / `ssb status`。

## 约定

- 生成的配置永远先 `sing-box check` 再落地（Generate 里已实现，勿绕过）
- 工具绝不改系统文件（resolved 等只打印建议）
- 出错信息用中文，面向终端用户
