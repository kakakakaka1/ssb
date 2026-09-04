// Package render generates the sing-box config.json (1.14.x schema):
// TUN + FakeIP + rule-set 分流 + clash_api Dashboard。
// 生成结果始终交给 `sing-box check` 做最终校验。
package render

import (
	"encoding/json"
	"fmt"
	"strings"

	"ssb/internal/profile"
)

// —— 顶层用结构体保证键序，规则等多态对象用 map（encoding/json 对 map 按键排序，输出确定）。

type config struct {
	Log          *logCfg   `json:"log,omitempty"`
	DNS          *dnsCfg   `json:"dns,omitempty"`
	Inbounds     []any     `json:"inbounds,omitempty"`
	Outbounds    []any     `json:"outbounds,omitempty"`
	Route        *routeCfg `json:"route,omitempty"`
	HTTPClients  []any     `json:"http_clients,omitempty"`
	Experimental *expCfg   `json:"experimental,omitempty"`
}

type logCfg struct {
	Level     string `json:"level"`
	Timestamp bool   `json:"timestamp"`
}

type dnsCfg struct {
	Servers []any  `json:"servers"`
	Rules   []any  `json:"rules,omitempty"`
	Final   string `json:"final,omitempty"`
}

type routeCfg struct {
	Rules                 []any  `json:"rules,omitempty"`
	RuleSet               []any  `json:"rule_set,omitempty"`
	Final                 string `json:"final,omitempty"`
	AutoDetectInterface   bool   `json:"auto_detect_interface,omitempty"`
	DefaultDomainResolver any    `json:"default_domain_resolver,omitempty"`
}

type expCfg struct {
	CacheFile map[string]any `json:"cache_file,omitempty"`
	ClashAPI  map[string]any `json:"clash_api,omitempty"`
}

// 规则集默认走 testingcf.jsdelivr.net CDN（MetaCubeX/meta-rules-dat 的 sing 分支），
// 国内可直连，因此不套用 GitHub 镜像前缀。
const (
	geositeCNURL = "https://testingcf.jsdelivr.net/gh/MetaCubeX/meta-rules-dat@sing/geo/geosite/cn.srs"
	geoipCNURL   = "https://testingcf.jsdelivr.net/gh/MetaCubeX/meta-rules-dat@sing/geo/geoip/cn.srs"
)

// httpClientTag names the shared HTTP client used for remote rule-set
// downloads. sing-box 1.14 弃用了 rule_set 里的 download_detour，改为在顶层
// http_clients 声明客户端、规则集用 http_client 引用。
const httpClientTag = "http-download"

// uiDownloadURL returns the dashboard artifact for external_ui_download_url.
// 注意：sing-box 的下载器只支持 zip 归档（tgz 会报 "zip: not a valid zip file"）。
func uiDownloadURL(name string) string {
	switch name {
	case "zashboard":
		return "https://github.com/Zephyruso/zashboard/releases/latest/download/dist.zip"
	case "yacd":
		return "https://github.com/MetaCubeX/Yacd-meta/archive/gh-pages.zip"
	default: // metacubexd（gh-pages 分支的 zip 归档，sing-box 生态的常用来源）
		return "https://github.com/MetaCubeX/metacubexd/archive/gh-pages.zip"
	}
}

// splitDomainRule turns user domain entries into sing-box matchers:
// "example.com" → 精确 + ".example.com" 子域名；以点开头的条目只做后缀匹配。
func splitDomainRule(list []string) (exact, suffix []string) {
	for _, d := range list {
		if strings.HasPrefix(d, ".") {
			suffix = append(suffix, d)
			continue
		}
		exact = append(exact, d)
		suffix = append(suffix, "."+d)
	}
	return
}

func customRule(domains []string, extra map[string]any) map[string]any {
	exact, suffix := splitDomainRule(domains)
	r := map[string]any{}
	if len(exact) > 0 {
		r["domain"] = exact
	}
	if len(suffix) > 0 {
		r["domain_suffix"] = suffix
	}
	for k, v := range extra {
		r[k] = v
	}
	return r
}

// Build renders config.json bytes for the current state.
func Build(st *profile.State, dirs profile.Dirs) ([]byte, error) {
	s := st.Settings
	nodes := st.AllNodes()
	v6 := useIPv6(s.IPv6) // 内核没有 IPv6 时全程降级为纯 IPv4，见 ipv6.go

	mirror := func(u string) string { return s.MirrorPrefix + u }
	detour := s.DownloadDetour
	if detour != "PROXY" {
		detour = "direct"
	}

	// ---- outbounds ----
	var nodeTags []string
	outbounds := []any{}
	proxyMembers := []string{}
	if len(nodes) > 0 {
		proxyMembers = append(proxyMembers, "auto")
	}
	for _, n := range nodes {
		nodeTags = append(nodeTags, n.Tag)
		outbounds = append(outbounds, n.Outbound)
	}
	proxyMembers = append(proxyMembers, nodeTags...)
	proxyMembers = append(proxyMembers, "direct")

	selector := map[string]any{
		"type": "selector", "tag": "PROXY",
		"outbounds":                   proxyMembers,
		"interrupt_exist_connections": true,
	}
	if len(nodes) > 0 {
		selector["default"] = "auto"
	}
	head := []any{selector}
	if len(nodes) > 0 {
		head = append(head, map[string]any{
			"type": "urltest", "tag": "auto",
			"outbounds": nodeTags,
			"url":       "https://www.gstatic.com/generate_204",
			"interval":  "3m",
			"tolerance": 50,
		})
	}
	head = append(head, map[string]any{"type": "direct", "tag": "direct"})
	outbounds = append(head, outbounds...)

	// ---- dns ----
	dnsServers := []any{
		map[string]any{"type": "udp", "tag": "dns-cn", "server": s.DNSCN},
		map[string]any{"type": "https", "tag": "dns-proxy", "server": s.DNSProxy, "detour": "PROXY"},
	}
	var dnsRules []any
	if !v6 {
		// 没有 IPv6 栈：AAAA 直接回空 NOERROR，免得程序拿到一个根本连不上的 v6 地址干等超时
		dnsRules = append(dnsRules, map[string]any{
			"query_type": []string{"AAAA"}, "action": "predefined", "rcode": "NOERROR",
		})
	}
	dnsRules = append(dnsRules, map[string]any{"clash_mode": "Direct", "server": "dns-cn"})
	// 自定义分流（高级设置）：优先级高于 geosite-cn
	if len(s.CustomProxy) > 0 {
		server := "dns-proxy"
		if s.FakeIP {
			server = "dns-fakeip"
		}
		dnsRules = append(dnsRules, customRule(s.CustomProxy, map[string]any{"server": server}))
	}
	if len(s.CustomDirect) > 0 {
		dnsRules = append(dnsRules, customRule(s.CustomDirect, map[string]any{"server": "dns-cn"}))
	}
	if s.RouteMode != "global" {
		dnsRules = append(dnsRules, map[string]any{"rule_set": "geosite-cn", "server": "dns-cn"})
	}
	if s.FakeIP {
		fake := map[string]any{
			"type": "fakeip", "tag": "dns-fakeip",
			"inet4_range": "198.18.0.0/15",
		}
		qtype := []string{"A"}
		if v6 {
			fake["inet6_range"] = "fc00::/18"
			qtype = append(qtype, "AAAA")
		}
		dnsServers = append(dnsServers, fake)
		dnsRules = append(dnsRules, map[string]any{"query_type": qtype, "server": "dns-fakeip"})
	}

	// ---- inbounds ----
	var inbounds []any
	if s.TunEnabled {
		addrs := []string{"172.19.0.1/30"}
		if v6 {
			addrs = append(addrs, "fdfe:dcba:9876::1/126")
		}
		tun := map[string]any{
			"type": "tun", "tag": "tun-in",
			"address":    addrs,
			"mtu":        9000,
			"auto_route": true,
			// strict_route 只在有 IPv6 时开：没有 v6 地址时 sing-tun 会补一条
			// AF_INET6 unreachable 策略路由，在无 IPv6 内核上直接 EAFNOSUPPORT 启动失败。
			"strict_route": v6,
			"stack":        "mixed",
		}
		if !v6 {
			// 没有 IPv6 时必须显式限定 route_address 为纯 IPv4：auto_route
			// 默认会同时为 IPv4 和 IPv6 创建策略路由规则（fib rules），
			// 在内核没有 IPv6 或缺少 CONFIG_IPV6_MULTIPLE_TABLES 时
			// 添加 AF_INET6 规则会返回 EAFNOSUPPORT 导致启动失败。
			tun["route_address"] = []string{"0.0.0.0/0"}
		}
		if s.AutoRedirect {
			tun["auto_redirect"] = true
		}
		inbounds = append(inbounds, tun)
	}
	if s.MixedEnabled || !s.TunEnabled { // 至少保证有一个入站
		inbounds = append(inbounds, map[string]any{
			"type": "mixed", "tag": "mixed-in",
			"listen": "127.0.0.1", "listen_port": s.MixedPort,
		})
	}

	// ---- route ----
	rules := []any{
		map[string]any{"action": "sniff"},
		map[string]any{"protocol": "dns", "action": "hijack-dns"},
		map[string]any{"ip_is_private": true, "outbound": "direct"},
		map[string]any{"clash_mode": "Direct", "outbound": "direct"},
		map[string]any{"clash_mode": "Global", "outbound": "PROXY"},
	}
	// 自定义分流（高级设置）：排在 geosite/geoip 之前，可覆盖默认分流
	if len(s.CustomProxy) > 0 {
		rules = append(rules, customRule(s.CustomProxy, map[string]any{"outbound": "PROXY"}))
	}
	if len(s.CustomDirect) > 0 {
		rules = append(rules, customRule(s.CustomDirect, map[string]any{"outbound": "direct"}))
	}
	var ruleSets []any
	var httpClients []any
	needCN := s.RouteMode != "global"
	if needCN {
		rules = append(rules,
			map[string]any{"rule_set": []string{"geosite-cn"}, "outbound": "direct"},
			map[string]any{"rule_set": []string{"geoip-cn"}, "outbound": "direct"},
		)
		geosite := map[string]any{
			"type": "remote", "tag": "geosite-cn", "format": "binary",
			"url": geositeCNURL, "http_client": httpClientTag, "update_interval": "1d",
		}
		geoip := map[string]any{
			"type": "remote", "tag": "geoip-cn", "format": "binary",
			"url": geoipCNURL, "http_client": httpClientTag, "update_interval": "1d",
		}
		ruleSets = append(ruleSets, geosite, geoip)

		// 1.14 用顶层 http_clients + rule_set.http_client 取代了 download_detour，
		// 且必须显式声明：不写 http_client 时下载会走 route.final（也就是 PROXY），
		// 而不是直连——1.14 已把这个隐式行为标为弃用。
		// detour 只在走代理时写：显式 detour 到裸 direct 出站会被内核拒绝
		// （"detour to an empty direct outbound makes no sense"），省掉即为直连。
		client := map[string]any{"tag": httpClientTag}
		if detour == "PROXY" {
			client["detour"] = detour
		}
		httpClients = append(httpClients, client)
	}

	defaultMode := "Rule"
	if s.RouteMode == "global" {
		defaultMode = "Global"
	}

	clashAPI := map[string]any{
		"external_controller": s.ClashListen,
		"secret":              s.ClashSecret,
		"default_mode":        defaultMode,
	}
	if !s.DashboardOff { // 关面板时仍保留 clash_api（TUI 切换节点依赖它）
		clashAPI["external_ui"] = dirs.UIDir()
		clashAPI["external_ui_download_url"] = mirror(uiDownloadURL(s.ExternalUI))
		clashAPI["external_ui_download_detour"] = detour
	}

	logLevel := s.LogLevel
	if logLevel == "" {
		logLevel = "warn"
	}
	cfg := config{
		Log: &logCfg{Level: logLevel, Timestamp: true},
		DNS: &dnsCfg{
			Servers: dnsServers,
			Rules:   dnsRules,
			Final:   "dns-proxy",
		},
		Inbounds:  inbounds,
		Outbounds: outbounds,
		Route: &routeCfg{
			Rules:                 rules,
			RuleSet:               ruleSets,
			Final:                 "PROXY",
			AutoDetectInterface:   true,
			DefaultDomainResolver: map[string]any{"server": "dns-cn"},
		},
		HTTPClients: httpClients,
		Experimental: &expCfg{
			CacheFile: map[string]any{
				"enabled":      true,
				"path":         dirs.CacheDB(),
				"store_fakeip": true,
				"store_dns":    true,
			},
			ClashAPI: clashAPI,
		},
	}

	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("生成 config.json 失败: %w", err)
	}
	return append(b, '\n'), nil
}
