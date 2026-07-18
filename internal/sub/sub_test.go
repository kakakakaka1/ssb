package sub

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const clashYAML = `
port: 7890
proxies:
  - name: "香港 01"
    type: vless
    server: hk1.example.com
    port: 443
    uuid: 11112222-3333-4444-5555-666677778888
    tls: true
    flow: xtls-rprx-vision
    servername: www.apple.com
    reality-opts:
      public-key: pbk_value
      short-id: "0123abcd"
    client-fingerprint: chrome
    network: tcp
  - name: "美国 02"
    type: anytls
    server: us1.example.com
    port: 8443
    password: anytls-pass
    sni: us1.example.com
    skip-cert-verify: true
  - name: "日本 03"
    type: ss
    server: jp1.example.com
    port: 8388
    cipher: aes-256-gcm
    password: sspass
  - name: "韩国 04"
    type: hysteria2
    server: kr1.example.com
    port: 36712
    password: hy2pass
    obfs: salamander
    obfs-password: obpw
    sni: kr1.example.com
proxy-groups: []
rules: []
`

const singboxJSON = `{
  "outbounds": [
    {"type": "selector", "tag": "PROXY", "outbounds": ["a"]},
    {"type": "vless", "tag": "sg-01", "server": "sg.example.com", "server_port": 443,
     "uuid": "9999", "tls": {"enabled": true, "server_name": "sg.example.com"}},
    {"type": "direct", "tag": "direct"}
  ]
}`

func TestParseBodyClashYAML(t *testing.T) {
	r, err := ParseBody(clashYAML)
	if err != nil {
		t.Fatal(err)
	}
	if r.Format != "clash" || len(r.Nodes) != 4 {
		t.Fatalf("format=%s nodes=%d", r.Format, len(r.Nodes))
	}
	o := r.Nodes[0].Outbound
	if o["type"] != "vless" || o["flow"] != "xtls-rprx-vision" {
		t.Fatalf("vless 转换错误: %v", o)
	}
	tls := o["tls"].(map[string]any)
	re := tls["reality"].(map[string]any)
	if re["public_key"] != "pbk_value" || re["short_id"] != "0123abcd" {
		t.Fatalf("reality 转换错误: %v", re)
	}
	if r.Nodes[1].Outbound["type"] != "anytls" {
		t.Fatal("anytls 转换错误")
	}
	hy := r.Nodes[3].Outbound
	if hy["type"] != "hysteria2" || hy["obfs"].(map[string]any)["password"] != "obpw" {
		t.Fatalf("hysteria2 转换错误: %v", hy)
	}
}

func TestParseBodySingboxJSON(t *testing.T) {
	r, err := ParseBody(singboxJSON)
	if err != nil {
		t.Fatal(err)
	}
	if r.Format != "sing-box" || len(r.Nodes) != 1 {
		t.Fatalf("format=%s nodes=%d（应过滤 selector/direct）", r.Format, len(r.Nodes))
	}
	if r.Nodes[0].Tag != "sg-01" {
		t.Fatal("tag 错误")
	}
	if r.Nodes[0].Outbound["server_port"] != 443 {
		t.Fatalf("端口应归一化为 int: %T", r.Nodes[0].Outbound["server_port"])
	}
}

func TestParseBodyBase64(t *testing.T) {
	links := "trojan://pw@1.1.1.1:443?sni=a.com#t1\nvless://u@2.2.2.2:443?security=tls#t2"
	r, err := ParseBody(base64.StdEncoding.EncodeToString([]byte(links)))
	if err != nil {
		t.Fatal(err)
	}
	if r.Format != "base64/uri" || len(r.Nodes) != 2 {
		t.Fatalf("format=%s nodes=%d", r.Format, len(r.Nodes))
	}
}

func TestFetchUAFallbackAndUserinfo(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("subscription-userinfo", "upload=100; download=200; total=1073741824; expire=1893456000")
		ua := req.Header.Get("User-Agent")
		switch {
		case strings.Contains(ua, "sing-box"):
			w.Write([]byte("这不是有效内容")) // 第一个 UA 给坏响应，逼出退避
		case strings.Contains(ua, "clash"):
			w.Write([]byte(clashYAML))
		default:
			w.Write([]byte("x"))
		}
	}))
	defer srv.Close()

	r, err := Fetch(context.Background(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if r.Format != "clash" || len(r.Nodes) != 4 {
		t.Fatalf("UA 退避失败: format=%s nodes=%d", r.Format, len(r.Nodes))
	}
	if r.Userinfo == nil || r.Userinfo.Total != 1073741824 {
		t.Fatalf("userinfo: %+v", r.Userinfo)
	}
}

func TestHumanBytes(t *testing.T) {
	if HumanBytes(1073741824) != "1.0 GB" {
		t.Fatal(HumanBytes(1073741824))
	}
}
