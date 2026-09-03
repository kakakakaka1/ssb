package tui

import (
	"fmt"
	"strconv"
	"strings"

	"ssb/internal/app"
	"ssb/internal/profile"
)

// settingRow 是设置页的一行。opts 非空表示枚举/开关（回车轮转、退格反向），
// 否则回车进入文本编辑，输入框预填当前值。
type settingRow struct {
	group string // 分组标题，连续相同的只显示一次
	label string
	desc  string // 光标停在该行时显示在底栏的说明
	opts  []string
	get   func(*app.App) string
	set   func(*app.App, string) error
}

const on, off = "开", "关"

func boolRow(group, label, desc string, p func(*app.App) *bool) settingRow {
	return settingRow{group: group, label: label, desc: desc, opts: []string{on, off},
		get: func(a *app.App) string {
			if *p(a) {
				return on
			}
			return off
		},
		set: func(a *app.App, v string) error { *p(a) = v == on; return nil },
	}
}

func enumRow(group, label, desc string, opts []string, p func(*app.App) *string) settingRow {
	return settingRow{group: group, label: label, desc: desc, opts: opts,
		get: func(a *app.App) string { return *p(a) },
		set: func(a *app.App, v string) error {
			for _, o := range opts {
				if o == v {
					*p(a) = v
					return nil
				}
			}
			return fmt.Errorf("%s 只能是 %s", label, strings.Join(opts, " / "))
		},
	}
}

func textRow(group, label, desc string, p func(*app.App) *string) settingRow {
	return settingRow{group: group, label: label, desc: desc,
		get: func(a *app.App) string { return *p(a) },
		set: func(a *app.App, v string) error { *p(a) = strings.TrimSpace(v); return nil },
	}
}

func settingRows(a *app.App) []settingRow {
	S := func(a *app.App) *profile.Settings { return &a.State.Settings }
	rows := []settingRow{
		enumRow("代理", "路由模式", "rule=国内直连、其余走代理；global=全部走代理",
			[]string{"rule", "global"}, func(a *app.App) *string { return &S(a).RouteMode }),
		boolRow("代理", "TUN 透明代理", "接管系统全部流量，需要 root 或 CAP_NET_ADMIN；关掉则只有本地 mixed 端口",
			func(a *app.App) *bool { return &S(a).TunEnabled }),
		enumRow("代理", "IPv6", "auto=按内核能力探测（推荐）；启动报 address family not supported 就选 off",
			[]string{"auto", "on", "off"}, func(a *app.App) *string { return &S(a).IPv6 }),
		boolRow("代理", "auto_redirect", "Linux 用 nftables 加速 TUN 转发，一般保持开",
			func(a *app.App) *bool { return &S(a).AutoRedirect }),
		boolRow("代理", "FakeIP", "代理域名返回假 IP、免去 DNS 往返，一般保持开",
			func(a *app.App) *bool { return &S(a).FakeIP }),

		boolRow("本地入站", "mixed 端口开关", "127.0.0.1 上的 socks5/http 入站，给不走 TUN 的程序用",
			func(a *app.App) *bool { return &S(a).MixedEnabled }),
		{group: "本地入站", label: "mixed 端口", desc: "1–65535",
			get: func(a *app.App) string { return strconv.Itoa(S(a).MixedPort) },
			set: func(a *app.App, v string) error {
				n, err := strconv.Atoi(strings.TrimSpace(v))
				if err != nil || n <= 0 || n > 65535 {
					return fmt.Errorf("端口无效：%q", v)
				}
				S(a).MixedPort = n
				return nil
			}},
		textRow("本地入站", "clash_api 监听", "host:port；改成 0.0.0.0:9090 可让局域网设备打开 Dashboard",
			func(a *app.App) *string { return &S(a).ClashListen }),

		{group: "面板", label: "Dashboard 网页面板", desc: "关=只用 TUI/API 控制（默认）；开=启动时下载网页面板到 data/ui/",
			opts: []string{on, off},
			get: func(a *app.App) string {
				if S(a).DashboardOff {
					return off
				}
				return on
			},
			set: func(a *app.App, v string) error { S(a).DashboardOff = v == off; return nil }},
		enumRow("面板", "Dashboard 类型", "metacubexd 功能最全；yacd 最轻",
			[]string{"metacubexd", "zashboard", "yacd"}, func(a *app.App) *string { return &S(a).ExternalUI }),

		textRow("DNS", "国内 DNS", "udp 地址，直连查询用，例如 223.5.5.5",
			func(a *app.App) *string { return &S(a).DNSCN }),
		textRow("DNS", "代理 DNS", "DoH 地址，经代理查询用，例如 https://1.1.1.1/dns-query",
			func(a *app.App) *string { return &S(a).DNSProxy }),

		textRow("下载", "GitHub 镜像前缀", "规则集/面板/内核下载加速，例如 https://ghproxy.com/ ；留空直连",
			func(a *app.App) *string { return &S(a).MirrorPrefix }),
		enumRow("下载", "下载出站", "规则集和面板走 direct 还是 PROXY；镜像不可用时改 PROXY",
			[]string{"direct", "PROXY"}, func(a *app.App) *string { return &S(a).DownloadDetour }),

		textRow("内核", "sing-box 路径", "留空自动：先找 data/sing-box，再找 PATH",
			func(a *app.App) *string { return &S(a).SingboxPath }),
		enumRow("内核", "日志级别", "warn 足够日常排错且不会让日志膨胀",
			[]string{"warn", "info", "debug", "error"}, func(a *app.App) *string { return &S(a).LogLevel }),

		boolRow("高级", "自定义分流", "开启后可指定强制代理/直连的域名（优先级高于内置规则）",
			func(a *app.App) *bool { return &S(a).AdvancedRouting }),
	}
	if a.State.Settings.AdvancedRouting {
		rows = append(rows,
			domainRow("高级", "  强制代理域名", func(a *app.App) *[]string { return &S(a).CustomProxy }),
			domainRow("高级", "  强制直连域名", func(a *app.App) *[]string { return &S(a).CustomDirect }),
		)
	}
	return rows
}

func domainRow(group, label string, p func(*app.App) *[]string) settingRow {
	return settingRow{group: group, label: label,
		desc: "逗号分隔；example.com 匹配自身和子域名，.cn 只匹配后缀",
		get:  func(a *app.App) string { return strings.Join(*p(a), ", ") },
		set: func(a *app.App, v string) error {
			list, err := parseDomains(v)
			if err != nil {
				return err
			}
			*p(a) = list
			return nil
		}}
}

// nextOpt 在 opts 里找 cur 的下一个（d=+1）/上一个（d=-1）；cur 不在里面时回第一个。
func nextOpt(opts []string, cur string, d int) string {
	for i, o := range opts {
		if o == cur {
			return opts[(i+d+len(opts))%len(opts)]
		}
	}
	return opts[0]
}

// parseDomains splits a comma/space separated domain list and validates it.
// 以点开头的条目（如 ".cn"）只匹配子域名/后缀，其余匹配自身+子域名。
func parseDomains(v string) ([]string, error) {
	fields := strings.FieldsFunc(v, func(r rune) bool {
		return r == ',' || r == '，' || r == ';' || r == '；' || r == ' ' || r == '\t'
	})
	var out []string
	for _, f := range fields {
		d := strings.ToLower(strings.TrimSpace(f))
		if d == "" {
			continue
		}
		if strings.ContainsAny(d, "/:") {
			return nil, fmt.Errorf("%q 不是域名：请填 example.com 这样的裸域名（不带协议/端口/路径）", d)
		}
		out = append(out, d)
	}
	return out, nil
}
