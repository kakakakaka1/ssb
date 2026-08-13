package render

import (
	"encoding/json"
	"strings"
	"testing"

	"ssb/internal/link"
	"ssb/internal/profile"
)

func testState(t *testing.T) *profile.State {
	t.Helper()
	st, err := profile.Load(profile.Dirs{Base: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	n1, err := link.Parse("vless://uuid-1@1.2.3.4:443?security=tls&sni=a.com#节点A")
	if err != nil {
		t.Fatal(err)
	}
	n2, err := link.Parse("anytls://pw@5.6.7.8:8443?sni=b.com#节点B")
	if err != nil {
		t.Fatal(err)
	}
	st.Manual = []*link.Node{n1, n2}
	return st
}

func build(t *testing.T, st *profile.State) map[string]any {
	t.Helper()
	b, err := Build(st, profile.Dirs{Base: "/tmp/ssb-test"})
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("生成的 JSON 无效: %v\n%s", err, b)
	}
	return m
}

func TestBuildRuleMode(t *testing.T) {
	st := testState(t)
	m := build(t, st)

	outs := m["outbounds"].([]any)
	sel := outs[0].(map[string]any)
	if sel["type"] != "selector" || sel["tag"] != "PROXY" || sel["default"] != "auto" {
		t.Fatalf("selector 错误: %v", sel)
	}
	members := sel["outbounds"].([]any)
	if members[0] != "auto" || members[len(members)-1] != "direct" {
		t.Fatalf("selector 成员错误: %v", members)
	}
	if outs[1].(map[string]any)["type"] != "urltest" {
		t.Fatal("第二个出站应为 urltest")
	}

	dns := m["dns"].(map[string]any)
	servers := dns["servers"].([]any)
	foundFakeIP := false
	for _, s := range servers {
		if s.(map[string]any)["type"] == "fakeip" {
			foundFakeIP = true
		}
	}
	if !foundFakeIP {
		t.Fatal("默认应启用 fakeip DNS")
	}

	route := m["route"].(map[string]any)
	if route["final"] != "PROXY" || route["auto_detect_interface"] != true {
		t.Fatalf("route 错误: %v", route)
	}
	if len(route["rule_set"].([]any)) != 2 {
		t.Fatal("rule 模式应有 geosite-cn + geoip-cn 两个规则集")
	}

	capi := m["experimental"].(map[string]any)["clash_api"].(map[string]any)
	if capi["secret"] == "" || capi["external_controller"] != "127.0.0.1:9090" {
		t.Fatalf("clash_api 错误: %v", capi)
	}
	if _, has := capi["external_ui"]; has {
		t.Fatal("Dashboard 默认应关闭（不应有 external_ui）")
	}
	if m["log"].(map[string]any)["level"] != "warn" {
		t.Fatal("日志级别默认应为 warn")
	}

	inb := m["inbounds"].([]any)
	if inb[0].(map[string]any)["type"] != "tun" {
		t.Fatal("默认应有 tun 入站")
	}
}

func TestBuildGlobalModeAndNoTun(t *testing.T) {
	st := testState(t)
	st.Settings.RouteMode = "global"
	st.Settings.TunEnabled = false
	m := build(t, st)

	route := m["route"].(map[string]any)
	if _, has := route["rule_set"]; has {
		t.Fatal("global 模式不应包含规则集")
	}
	inb := m["inbounds"].([]any)
	if len(inb) != 1 || inb[0].(map[string]any)["type"] != "mixed" {
		t.Fatalf("关 TUN 后应只有 mixed 入站: %v", inb)
	}
	capi := m["experimental"].(map[string]any)["clash_api"].(map[string]any)
	if capi["default_mode"] != "Global" {
		t.Fatal("default_mode 应为 Global")
	}
}

func TestBuildEmptyNodes(t *testing.T) {
	st, err := profile.Load(profile.Dirs{Base: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	m := build(t, st)
	outs := m["outbounds"].([]any)
	sel := outs[0].(map[string]any)
	members := sel["outbounds"].([]any)
	if len(members) != 1 || members[0] != "direct" {
		t.Fatalf("空节点时 selector 应只含 direct: %v", members)
	}
	for _, o := range outs {
		if o.(map[string]any)["type"] == "urltest" {
			t.Fatal("空节点时不应有 urltest")
		}
	}
}

func TestDashboardOff(t *testing.T) {
	st := testState(t)
	st.Settings.DashboardOff = true
	m := build(t, st)
	capi := m["experimental"].(map[string]any)["clash_api"].(map[string]any)
	for _, k := range []string{"external_ui", "external_ui_download_url", "external_ui_download_detour"} {
		if _, has := capi[k]; has {
			t.Fatalf("关闭面板后不应有 %s", k)
		}
	}
	// clash_api 本体必须保留（TUI 切换节点依赖）
	if capi["external_controller"] != "127.0.0.1:9090" || capi["secret"] == "" {
		t.Fatalf("clash_api 应保留: %v", capi)
	}
}

func TestDashboardOn(t *testing.T) {
	st := testState(t)
	st.Settings.DashboardOff = false
	m := build(t, st)
	capi := m["experimental"].(map[string]any)["clash_api"].(map[string]any)
	if !strings.Contains(capi["external_ui_download_url"].(string), "metacubexd") {
		t.Fatalf("开启面板后应有 metacubexd 下载地址: %v", capi)
	}
}

func TestCustomRouting(t *testing.T) {
	st := testState(t)
	st.Settings.CustomProxy = []string{"openai.com"}
	st.Settings.CustomDirect = []string{"steamcdn.example.com", ".edu.cn"}
	m := build(t, st)

	rules := m["route"].(map[string]any)["rules"].([]any)
	proxyIdx, directIdx, geoIdx := -1, -1, -1
	for i, r := range rules {
		rm := r.(map[string]any)
		if d, ok := rm["domain"].([]any); ok && len(d) > 0 && d[0] == "openai.com" {
			proxyIdx = i
			if rm["outbound"] != "PROXY" {
				t.Fatalf("强制代理规则出站错误: %v", rm)
			}
			if sfx := rm["domain_suffix"].([]any); sfx[0] != ".openai.com" {
				t.Fatalf("应自动补子域名后缀: %v", sfx)
			}
		}
		if d, ok := rm["domain"].([]any); ok && len(d) > 0 && d[0] == "steamcdn.example.com" {
			directIdx = i
			if rm["outbound"] != "direct" {
				t.Fatalf("强制直连规则出站错误: %v", rm)
			}
			// ".edu.cn" 以点开头，只做后缀
			found := false
			for _, s := range rm["domain_suffix"].([]any) {
				if s == ".edu.cn" {
					found = true
				}
			}
			if !found {
				t.Fatalf("缺少 .edu.cn 后缀: %v", rm)
			}
		}
		if rs, ok := rm["rule_set"].([]any); ok && len(rs) > 0 && rs[0] == "geosite-cn" && geoIdx == -1 {
			geoIdx = i
		}
	}
	if proxyIdx == -1 || directIdx == -1 || geoIdx == -1 {
		t.Fatalf("缺少规则: proxy=%d direct=%d geo=%d", proxyIdx, directIdx, geoIdx)
	}
	if !(proxyIdx < directIdx && directIdx < geoIdx) {
		t.Fatalf("自定义规则应排在 geosite 之前且代理优先: proxy=%d direct=%d geo=%d", proxyIdx, directIdx, geoIdx)
	}

	// DNS 规则同样注入：强制代理域名走 fakeip（默认开启 FakeIP），直连走 dns-cn
	dnsRules := m["dns"].(map[string]any)["rules"].([]any)
	sawProxy, sawDirect := false, false
	for _, r := range dnsRules {
		rm := r.(map[string]any)
		if d, ok := rm["domain"].([]any); ok && len(d) > 0 {
			switch d[0] {
			case "openai.com":
				sawProxy = rm["server"] == "dns-fakeip"
			case "steamcdn.example.com":
				sawDirect = rm["server"] == "dns-cn"
			}
		}
	}
	if !sawProxy || !sawDirect {
		t.Fatalf("DNS 自定义规则缺失: proxy=%v direct=%v", sawProxy, sawDirect)
	}
}

func TestRuleSetCDNAndMirrorPrefix(t *testing.T) {
	st := testState(t)
	st.Settings.MirrorPrefix = "https://ghproxy.net/"
	st.Settings.DashboardOff = false
	m := build(t, st)

	// 规则集固定走 testingcf.jsdelivr.net，不受镜像前缀影响
	rs := m["route"].(map[string]any)["rule_set"].([]any)
	for _, r := range rs {
		u := r.(map[string]any)["url"].(string)
		if !strings.HasPrefix(u, "https://testingcf.jsdelivr.net/gh/MetaCubeX/meta-rules-dat@sing/") {
			t.Fatalf("规则集应使用 testingcf.jsdelivr.net 源: %s", u)
		}
	}

	// 镜像前缀只作用于 GitHub 资源（Dashboard/内核下载）
	capi := m["experimental"].(map[string]any)["clash_api"].(map[string]any)
	ui := capi["external_ui_download_url"].(string)
	if !strings.HasPrefix(ui, "https://ghproxy.net/https://github.com/") {
		t.Fatalf("UI 下载地址镜像前缀未生效: %s", ui)
	}
}

// 内核关掉 IPv6 时（ipv6=off / auto 探测不到）配置必须是纯 IPv4：TUN 不配 v6 地址、
// 不开 strict_route，否则 sing-box 下 AF_INET6 策略路由会 EAFNOSUPPORT 启动失败。
func TestIPv6Off(t *testing.T) {
	st := testState(t)
	st.Settings.IPv6 = "off"
	m := build(t, st)

	tun := m["inbounds"].([]any)[0].(map[string]any)
	addrs := tun["address"].([]any)
	if len(addrs) != 1 || addrs[0] != "172.19.0.1/30" {
		t.Fatalf("关闭 IPv6 后 TUN 只能有 IPv4 地址: %v", addrs)
	}
	if tun["strict_route"] != false {
		t.Fatalf("关闭 IPv6 后必须关掉 strict_route: %v", tun["strict_route"])
	}

	dns := m["dns"].(map[string]any)
	for _, s := range dns["servers"].([]any) {
		sm := s.(map[string]any)
		if sm["type"] == "fakeip" {
			if _, has := sm["inet6_range"]; has {
				t.Fatalf("关闭 IPv6 后 fakeip 不应有 inet6_range: %v", sm)
			}
		}
	}
	rules := dns["rules"].([]any)
	first := rules[0].(map[string]any)
	if first["action"] != "predefined" || first["query_type"].([]any)[0] != "AAAA" {
		t.Fatalf("关闭 IPv6 后第一条 DNS 规则应拦掉 AAAA: %v", first)
	}
	for _, r := range rules {
		rm := r.(map[string]any)
		if rm["server"] != "dns-fakeip" {
			continue
		}
		for _, q := range rm["query_type"].([]any) {
			if q == "AAAA" {
				t.Fatalf("关闭 IPv6 后 fakeip 不应接管 AAAA: %v", rm)
			}
		}
	}
}

func TestIPv6On(t *testing.T) {
	st := testState(t)
	st.Settings.IPv6 = "on"
	m := build(t, st)

	tun := m["inbounds"].([]any)[0].(map[string]any)
	if len(tun["address"].([]any)) != 2 || tun["strict_route"] != true {
		t.Fatalf("ipv6=on 应保留 v6 地址与 strict_route: %v", tun)
	}
	for _, r := range m["dns"].(map[string]any)["rules"].([]any) {
		if r.(map[string]any)["action"] == "predefined" {
			t.Fatal("ipv6=on 不应拦截 AAAA")
		}
	}
}

// auto 跟随内核探测结果。
func TestIPv6Auto(t *testing.T) {
	old := HostHasIPv6
	defer func() { HostHasIPv6 = old }()

	HostHasIPv6 = func() bool { return false }
	st := testState(t)
	st.Settings.IPv6 = "auto"
	tun := build(t, st)["inbounds"].([]any)[0].(map[string]any)
	if len(tun["address"].([]any)) != 1 || tun["strict_route"] != false {
		t.Fatalf("内核无 IPv6 时 auto 应降级为纯 IPv4: %v", tun)
	}

	HostHasIPv6 = func() bool { return true }
	tun = build(t, st)["inbounds"].([]any)[0].(map[string]any)
	if len(tun["address"].([]any)) != 2 || tun["strict_route"] != true {
		t.Fatalf("内核有 IPv6 时 auto 应保留 v6: %v", tun)
	}
}
