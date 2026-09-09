# ssb — sing-box 节点/订阅管理器

单个可执行文件，Linux 与 Windows 通用：把节点分享链接（`vless://`、`anytls://`、`ss://`、
`vmess://`、`trojan://`、`hysteria2://`、`tuic://`）和订阅链接变成 sing-box 1.14 可直接运行的
`config.json`（TUN + FakeIP + 国内直连分流），并托管 sing-box 进程。带 TUI，也有命令行。

- 所有东西（工具、sing-box 内核、配置、状态、日志）都在一个目录里，卸载 = 删目录，不改系统文件
- 订阅自动识别 sing-box JSON / base64 链接列表 / Clash YAML
- 生成的配置先过 `sing-box check` 再上线，失败自动保留旧配置
- 内置 sing-box 官方 API 服务：TUI 直接切换出口，可选开启官方网页面板

## 安装

从 [Releases](../../releases) 下载对应系统和架构的包。`-with-singbox` 完整包自带 sing-box 内核，
解压即用；精简包解压后运行一次 `ssb install` 下载内核（或自行把 sing-box 放到 `data/`）。

- Linux：`ssb-<版本>-linux-<arch>[-with-singbox].tar.gz`
- Windows：`ssb-<版本>-windows-<arch>[-with-singbox].zip`

## 使用

**Linux**

```bash
mkdir -p ~/ssb && tar -xzf ssb-*-with-singbox.tar.gz -C ~/ssb && cd ~/ssb
sudo ./ssb            # 进入 TUI（TUN 需要 root，见下文「权限」）
```

**Windows**

解压后双击 `ssb.exe` 进入 TUI。要用 TUN（默认开启）需要管理员权限：右键 `ssb.exe` →
「以管理员身份运行」。在 PowerShell 里用 `.\ssb <命令>` 也可以。

**TUI** 有状态 / 订阅 / 节点 / 设置四个页签（`1-4` 或 `Tab` 切换）：订阅页和节点页按 `a`
粘贴链接添加，节点页回车切换出口，任何页面 `s` 启停、`r` 重启、`?` 看全部按键，`q` 退出后
sing-box 继续在后台运行。改过设置或增删节点后自动重新生成配置，服务运行中顶栏会提示按 `r`
重启生效。

**命令行**（Windows 把 `./ssb` 换成 `.\ssb`）：

```
./ssb add "vless://…" "anytls://…"        添加节点（可多个，也可从 stdin 输入）
./ssb sub add <URL> [名称]                 添加订阅并拉取
./ssb sub update [名称]                    更新订阅（缺省全部）
./ssb sub ls | sub rm <名称>               列出 / 删除订阅
./ssb nodes | nodes rm <tag>              列出节点 / 删除手动节点
./ssb start | stop | restart | status      启停与状态
./ssb doctor                               体检：内核、权限、端口、IPv6、配置
./ssb dashboard                            打印面板地址与 secret
./ssb logs [-f]                            查看日志
./ssb gen                                  重新生成 config.json
```

工作目录默认是可执行文件所在目录，可用 `--dir` 或环境变量 `SSB_DIR` 指定。

## 权限（TUN）

TUN 要接管系统全部流量，需要特权：

- Linux：`sudo ./ssb`，或一次性 `sudo setcap cap_net_admin+ep data/sing-box` 之后免 sudo
- Windows：以管理员身份运行

不想要特权：设置页关掉 TUN，让程序自己走 `socks5://127.0.0.1:2080`（mixed 端口）。

## 网页面板（可选）

默认关闭，日常切换出口在 TUI 节点页即可。需要时在设置页打开「Dashboard 网页面板」，按 `r`
重启，sing-box 会自动下载官方 [sing-box Dashboard](https://github.com/SagerNet/sing-box-dashboard)
到 `data/dashboard/`。浏览器打开 `http://127.0.0.1:9090/dashboard/`，首次进入填 secret
（状态页、设置页和 `./ssb dashboard` 都能看到）。要在局域网其他设备打开，把设置页的
「API 监听」改成 `0.0.0.0:9090`。

面板里的「Clash 模式」和「连接」页依赖 clash_api，本工具不再生成 clash_api，这两处不可用；
分流模式用设置页的「路由模式」（rule / global）。

## 分流与下载

默认国内域名/IP（geosite-cn / geoip-cn）直连，其余走代理。设置页「高级」→「自定义分流」
可指定强制代理 / 强制直连的域名（含子域名；`.cn` 这样点开头的只按后缀匹配），优先级高于
内置规则，DNS 同步分流。

规则集默认从 jsDelivr CDN 直连下载（国内可达）；「规则集源」可切到 GitHub，国内需配合
「GitHub 镜像前缀」或把「下载出站」改成 PROXY。镜像前缀同时用于内核和面板下载。状态页会
显示当前规则集源。

## 常见问题

- **启动报 `address family not supported by protocol`（Linux）**：内核没有 IPv6 策略路由。
  「IPv6」设为 `auto` 时重新 `./ssb gen` 会自动降级为纯 IPv4；设为 `on` 请改回 `auto` 或 `off`。
  `./ssb doctor` 会指出原因。
- **Ubuntu 的 systemd-resolved（127.0.0.53）**：TUN + hijack-dns 通常正常；若解析异常，可手动在
  `/etc/systemd/resolved.conf` 设 `DNSStubListener=no`，本工具不会代改系统文件。
- **日志**：`logs/sing-box.log`，默认 `warn` 级别，超过 8MB 在启动时轮转一份。排查问题时在
  设置页临时调到 `info` / `debug`。

## Docker（Linux）

```bash
docker compose build
docker compose run --rm ssb add "vless://…"
docker compose up -d      # host 网络 + NET_ADMIN + /dev/net/tun，前台跑 sing-box
```

## 目录布局

```
ssb / ssb.exe           本工具
data/sing-box[.exe]     内核
data/config.json        生成的 sing-box 配置（.bak 为上一版）
data/state.json         订阅 / 节点 / 设置（含 API secret）
data/dashboard/         官方面板（开面板后自动下载）
data/cache.db           sing-box 缓存（FakeIP、规则集、节点选择）
logs/  run/             日志与 pid
```

## 许可

ssb 以 [MIT License](LICENSE) 开源。`-with-singbox` 完整包附带未修改的
[sing-box](https://github.com/SagerNet/sing-box) 官方二进制（GPL-3.0，版权归 SagerNet），
仅打包在一起分发，版本号见 Release 说明。
