// Package render generates the sing-box config.json (1.14.x schema):
// TUN + FakeIP + rule-set 分流 + 官方 API 服务（TUI 控制 + sing-box Dashboard）。
// 生成结果始终交给 `sing-box check` 做最终校验。
package render

import (
	"encoding/json"
	"fmt"
	"net"
	"runtime"
	"strconv"
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
	Services     []any     `json:"services,omitempty"`
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
}

// 规则集来源（设置「规则集源」），两者内容相同（MetaCubeX/meta-rules-dat 的 sing 分支），只是传输路径不同：
//   - jsdelivr（默认）：testingcf.jsdelivr.net 的 Cloudflare 节点，国内可直连，不套镜像前缀
//   - github：raw.githubusercontent.com，国内一般不通，套镜像前缀，或把下载出站改成 PROXY
//
// 远程规则集没有缓存时首次拉取失败会让 sing-box 直接启动失败（rule_set_remote.go
// "initial rule-set"），所以默认必须是直连可达的源，别改成依赖代理的。
const (
	ruleSetJsdelivrBase = "https://testingcf.jsdelivr.net/gh/MetaCubeX/meta-rules-dat@sing/geo/"
	ruleSetGitHubBase   = "https://raw.githubusercontent.com/MetaCubeX/meta-rules-dat/sing/geo/"
)

// ruleSetURLs returns the geosite-cn / geoip-cn download URLs for the configured source.
func ruleSetURLs(s profile.Settings) (geosite, geoip string) {
	base := ruleSetJsdelivrBase
	if s.RuleSetSource == "github" {
		base = s.MirrorPrefix + ruleSetGitHubBase
	}
	return base + "geosite/cn.srs", base + "geoip/cn.srs"
}

// RuleSetSourceText 描述当前规则集从哪里、经什么拉取（TUI 状态页 / ssb status 显示用）。
func RuleSetSourceText(s profile.Settings) string {
	if s.RouteMode == "global" {
		return "global 模式不使用规则集"
	}
	name, host := "jsdelivr", "testingcf.jsdelivr.net"
	if s.RuleSetSource == "github" {
		name, host = "GitHub", "raw.githubusercontent.com"
	}
	var via []string
	if s.RuleSetSource == "github" && s.MirrorPrefix != "" {
		via = append(via, "镜像 "+s.MirrorPrefix)
	}
	if s.DownloadDetour == "PROXY" {
		via = append(via, "PROXY 出站")
	}
	text := name + "（" + host + "）"
	if len(via) == 0 {
		text += "，直连"
		if s.RuleSetSource == "github" {
			text += "；国内可能不通，建议配镜像前缀或把下载出站改 PROXY"
		}
		return text
	}
	return text + "，经 " + strings.Join(via, " + ")
}

// httpClientTag names the shared HTTP client used for remote rule-set
// downloads. sing-box 1.14 弃用了 rule_set 里的 download_detour，改为在顶层
// http_clients 声明客户端、规则集用 http_client 引用。
const httpClientTag = "http-download"

// dashboardURL is the official sing-box Dashboard archive the API service
// downloads when dashboard is enabled（与内核默认值相同，显式写出是为了套镜像前缀）。
const dashboardURL = "https://github.com/SagerNet/sing-box-dashboard/archive/refs/heads/gh-pages.zip"

// splitListen 把设置里的 host:port 拆成 sing-box Listen Fields 需要的 listen（IP）+ listen_port。
func splitListen(addr string) (string, int, error) {
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return "", 0, err
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port <= 0 || port > 65535 {
		return "", 0, fmt.Errorf("端口无效: %q", portStr)
	}
	if host == "" { // ":9090" 写法：监听全部地址
		host = "0.0.0.0"
	}
	if net.ParseIP(host) == nil {
		return "", 0, fmt.Errorf("listen 必须是 IP 地址: %q", host)
	}
	return host, port, nil
}

// supportsAutoRedirect: auto_redirect 靠 nftables，sing-box 只在 Linux 上实现，
// 其他平台写进配置会直接拒绝启动，所以生成时按平台静默省略（设置页也不显示）。
func supportsAutoRedirect(goos string) bool { return goos == "linux" }

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
		addrs := []string{s.TunAddress}
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
		if s.AutoRedirect && supportsAutoRedirect(runtime.GOOS) {
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
	// 没有 clash_api 时 clash_mode 规则永远不会匹配（1.14 的 API 服务把 Clash 模式委托给
	// clash_api），所以不生成；rule/global 由 ssb 的路由模式静态决定。
	rules := []any{
		map[string]any{"action": "sniff"},
		map[string]any{"protocol": "dns", "action": "hijack-dns"},
		map[string]any{"ip_is_private": true, "outbound": "direct"},
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
		geositeURL, geoipURL := ruleSetURLs(s)
		geosite := map[string]any{
			"type": "remote", "tag": "geosite-cn", "format": "binary",
			"url": geositeURL, "http_client": httpClientTag, "update_interval": "1d",
		}
		geoip := map[string]any{
			"type": "remote", "tag": "geoip-cn", "format": "binary",
			"url": geoipURL, "http_client": httpClientTag, "update_interval": "1d",
		}
		ruleSets = append(ruleSets, geosite, geoip)
	}

	// 1.14 用顶层 http_clients + http_client 引用取代了 download_detour（规则集、面板下载
	// 都走它），且必须显式声明：不写时下载会走 route.final（也就是 PROXY）而不是直连，
	// 1.14 已把这个隐式行为标为弃用。
	// detour 只在走代理时写：显式 detour 到裸 direct 出站会被内核拒绝
	// （"detour to an empty direct outbound makes no sense"），省掉即为直连。
	if needCN || !s.DashboardOff {
		client := map[string]any{"tag": httpClientTag}
		if detour == "PROXY" {
			client["detour"] = detour
		}
		httpClients = append(httpClients, client)
	}

	// ---- services：官方 API（gRPC + gRPC-Web）----
	// TUI 通过内核自带的 `sing-box api` 命令切换/查询节点；开面板时由它下载并托管官方
	// sing-box Dashboard（/dashboard/）。
	apiHost, apiPort, err := splitListen(s.APIListen)
	if err != nil {
		return nil, fmt.Errorf("API 监听地址无效 %q: %w", s.APIListen, err)
	}
	api := map[string]any{
		"type": "api", "tag": "api",
		"listen": apiHost, "listen_port": apiPort,
		"secret": s.APISecret,
	}
	if !s.DashboardOff {
		api["dashboard"] = map[string]any{
			"enabled":         true,
			"path":            dirs.DashboardDir(),
			"download_url":    mirror(dashboardURL),
			"http_client":     httpClientTag,
			"update_interval": "1d",
		}
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
		Services:    []any{api},
		Experimental: &expCfg{
			CacheFile: map[string]any{
				"enabled":      true,
				"path":         dirs.CacheDB(),
				"store_fakeip": true,
				"store_dns":    true,
			},
		},
	}

	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("生成 config.json 失败: %w", err)
	}
	return append(b, '\n'), nil
}
