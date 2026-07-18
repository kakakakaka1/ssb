package link

import (
	"encoding/base64"
	"testing"
)

func mustParse(t *testing.T, raw string) *Node {
	t.Helper()
	n, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse(%q): %v", raw, err)
	}
	return n
}

func get(t *testing.T, m map[string]any, path ...string) any {
	t.Helper()
	var cur any = m
	for _, p := range path {
		mm, ok := cur.(map[string]any)
		if !ok {
			t.Fatalf("path %v: %T 不是对象", path, cur)
		}
		cur = mm[p]
	}
	return cur
}

func TestVLESSReality(t *testing.T) {
	n := mustParse(t, "vless://5e3da52a-2d69-4e5e-b7ef-1e0e7a2e8b9c@1.2.3.4:443?encryption=none&flow=xtls-rprx-vision&security=reality&sni=www.apple.com&fp=chrome&pbk=SbVKOEMjK0sIlbwg4akyBg5mL5KZwwB-ed4eEE7YnRc&sid=6ba85179&type=tcp#我的VLESS")
	o := n.Outbound
	if o["type"] != "vless" || o["server"] != "1.2.3.4" || o["server_port"] != 443 {
		t.Fatalf("基础字段错误: %v", o)
	}
	if o["uuid"] != "5e3da52a-2d69-4e5e-b7ef-1e0e7a2e8b9c" || o["flow"] != "xtls-rprx-vision" {
		t.Fatalf("uuid/flow 错误: %v", o)
	}
	if get(t, o, "tls", "reality", "public_key") != "SbVKOEMjK0sIlbwg4akyBg5mL5KZwwB-ed4eEE7YnRc" {
		t.Fatal("reality public_key 错误")
	}
	if get(t, o, "tls", "reality", "short_id") != "6ba85179" {
		t.Fatal("reality short_id 错误")
	}
	if get(t, o, "tls", "utls", "fingerprint") != "chrome" {
		t.Fatal("utls fingerprint 错误")
	}
	if get(t, o, "tls", "server_name") != "www.apple.com" {
		t.Fatal("sni 错误")
	}
	if n.Tag != "我的VLESS" {
		t.Fatalf("tag 错误: %q", n.Tag)
	}
	if _, has := o["transport"]; has {
		t.Fatal("tcp 不应有 transport")
	}
}

func TestVLESSWsTLS(t *testing.T) {
	n := mustParse(t, "vless://uuid-1@example.com:8443?type=ws&security=tls&path=%2Fws%3Fed%3D2048&host=cdn.example.com&alpn=h2,http/1.1#ws节点")
	o := n.Outbound
	if get(t, o, "transport", "type") != "ws" {
		t.Fatal("应为 ws transport")
	}
	if get(t, o, "transport", "path") != "/ws" {
		t.Fatalf("path 应剥离 ed 参数: %v", get(t, o, "transport", "path"))
	}
	if get(t, o, "transport", "max_early_data") != 2048 {
		t.Fatal("max_early_data 错误")
	}
	if get(t, o, "transport", "headers", "Host") != "cdn.example.com" {
		t.Fatal("Host header 错误")
	}
	alpn := get(t, o, "tls", "alpn").([]string)
	if len(alpn) != 2 || alpn[0] != "h2" {
		t.Fatalf("alpn 错误: %v", alpn)
	}
}

func TestAnyTLS(t *testing.T) {
	n := mustParse(t, "anytls://p%40ss@5.6.7.8:8443/?sni=example.org&insecure=1&fp=chrome#AnyTLS-1")
	o := n.Outbound
	if o["type"] != "anytls" || o["password"] != "p@ss" {
		t.Fatalf("anytls 基础字段错误: %v", o)
	}
	if get(t, o, "tls", "server_name") != "example.org" {
		t.Fatal("sni 错误")
	}
	if get(t, o, "tls", "insecure") != true {
		t.Fatal("insecure 错误")
	}
	if n.Tag != "AnyTLS-1" {
		t.Fatalf("tag: %q", n.Tag)
	}
}

func TestSSSIP002(t *testing.T) {
	// base64url("aes-256-gcm:test1234")
	ui := base64.RawURLEncoding.EncodeToString([]byte("aes-256-gcm:test1234"))
	n := mustParse(t, "ss://"+ui+"@9.9.9.9:8388#ss1")
	o := n.Outbound
	if o["method"] != "aes-256-gcm" || o["password"] != "test1234" {
		t.Fatalf("method/password 错误: %v", o)
	}
}

func TestSS2022Plain(t *testing.T) {
	n := mustParse(t, "ss://2022-blake3-aes-256-gcm:aGVsbG8lMjB3b3JsZA%3D%3D@9.9.9.9:8388/?plugin=obfs-local%3Bobfs%3Dhttp%3Bobfs-host%3Dexample.com#ss2022")
	o := n.Outbound
	if o["method"] != "2022-blake3-aes-256-gcm" {
		t.Fatalf("method: %v", o["method"])
	}
	if o["password"] != "aGVsbG8lMjB3b3JsZA==" {
		t.Fatalf("password: %v", o["password"])
	}
	if o["plugin"] != "obfs-local" || o["plugin_opts"] != "obfs=http;obfs-host=example.com" {
		t.Fatalf("plugin: %v / %v", o["plugin"], o["plugin_opts"])
	}
}

func TestSSLegacy(t *testing.T) {
	body := base64.StdEncoding.EncodeToString([]byte("rc4-md5:passwd@3.3.3.3:1234"))
	n := mustParse(t, "ss://"+body+"#legacy%20node")
	o := n.Outbound
	if o["method"] != "rc4-md5" || o["password"] != "passwd" || o["server"] != "3.3.3.3" || o["server_port"] != 1234 {
		t.Fatalf("legacy 解析错误: %v", o)
	}
	if n.Tag != "legacy node" {
		t.Fatalf("tag: %q", n.Tag)
	}
}

func TestVMess(t *testing.T) {
	j := `{"v":"2","ps":"vm节点","add":"vm.example.com","port":"443","id":"aaaabbbb-cccc-dddd-eeee-ffff00001111","aid":"0","scy":"auto","net":"ws","type":"none","host":"vm.example.com","path":"/vmws","tls":"tls","sni":"vm.example.com","alpn":"","fp":""}`
	n := mustParse(t, "vmess://"+base64.StdEncoding.EncodeToString([]byte(j)))
	o := n.Outbound
	if o["type"] != "vmess" || o["server"] != "vm.example.com" || o["server_port"] != 443 {
		t.Fatalf("vmess 基础字段: %v", o)
	}
	if o["uuid"] != "aaaabbbb-cccc-dddd-eeee-ffff00001111" || o["security"] != "auto" || o["alter_id"] != 0 {
		t.Fatalf("uuid/security/alter_id: %v", o)
	}
	if get(t, o, "transport", "type") != "ws" || get(t, o, "transport", "path") != "/vmws" {
		t.Fatal("transport 错误")
	}
	if get(t, o, "tls", "server_name") != "vm.example.com" {
		t.Fatal("sni 错误")
	}
}

func TestTrojanGRPC(t *testing.T) {
	n := mustParse(t, "trojan://pw123@7.7.7.7:443?security=tls&sni=t.example.com&type=grpc&serviceName=grpcSvc&fp=safari#tj")
	o := n.Outbound
	if o["type"] != "trojan" || o["password"] != "pw123" {
		t.Fatalf("trojan 基础: %v", o)
	}
	if get(t, o, "transport", "type") != "grpc" || get(t, o, "transport", "service_name") != "grpcSvc" {
		t.Fatal("grpc transport 错误")
	}
}

func TestHysteria2(t *testing.T) {
	n := mustParse(t, "hy2://letmein@8.8.4.4:36712/?insecure=1&obfs=salamander&obfs-password=obfspw&sni=hy.example.com&mport=2000-3000#hy2节点")
	o := n.Outbound
	if o["type"] != "hysteria2" || o["password"] != "letmein" {
		t.Fatalf("hy2 基础: %v", o)
	}
	if get(t, o, "obfs", "type") != "salamander" || get(t, o, "obfs", "password") != "obfspw" {
		t.Fatal("obfs 错误")
	}
	sp := o["server_ports"].([]string)
	if len(sp) != 1 || sp[0] != "2000:3000" {
		t.Fatalf("server_ports: %v", sp)
	}
	alpn := get(t, o, "tls", "alpn").([]string)
	if len(alpn) != 1 || alpn[0] != "h3" {
		t.Fatal("hy2 alpn 应为 h3")
	}
}

func TestTUIC(t *testing.T) {
	n := mustParse(t, "tuic://uuid-x:pass-y@6.6.6.6:443?congestion_control=bbr&udp_relay_mode=native&alpn=h3&sni=tu.example.com#tuic1")
	o := n.Outbound
	if o["uuid"] != "uuid-x" || o["password"] != "pass-y" || o["congestion_control"] != "bbr" {
		t.Fatalf("tuic 字段: %v", o)
	}
}

func TestParseMixedBase64Blob(t *testing.T) {
	links := "trojan://pw@1.1.1.1:443?sni=a.com#t1\nvless://u1@2.2.2.2:443?security=tls&sni=b.com#t2\n"
	blob := base64.StdEncoding.EncodeToString([]byte(links))
	nodes, errs := ParseMixed(blob)
	if len(errs) != 0 {
		t.Fatalf("errs: %v", errs)
	}
	if len(nodes) != 2 || nodes[0].Tag != "t1" || nodes[1].Tag != "t2" {
		t.Fatalf("nodes: %+v", nodes)
	}
}

func TestUnsupportedScheme(t *testing.T) {
	if _, err := Parse("wireguard://x@1.2.3.4:51820"); err == nil {
		t.Fatal("应报不支持")
	}
	nodes, errs := ParseMixed("ssh://root@1.2.3.4:22\ntrojan://pw@1.1.1.1:443#ok")
	if len(nodes) != 1 || len(errs) != 1 {
		t.Fatalf("混合容错失败: %d nodes, %d errs", len(nodes), len(errs))
	}
}
