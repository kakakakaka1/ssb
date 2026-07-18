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
	if !strings.Contains(capi["external_ui_download_url"].(string), "metacubexd") {
		t.Fatal("默认 Dashboard 应为 metacubexd")
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

func TestRuleSetCDNAndMirrorPrefix(t *testing.T) {
	st := testState(t)
	st.Settings.MirrorPrefix = "https://ghproxy.net/"
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
