package link

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// vmess://base64(JSON) — the v2rayN share format:
// {"v":"2","ps":"name","add":"host","port":"443","id":"uuid","aid":"0","scy":"auto",
//
//	"net":"ws","type":"none","host":"h","path":"/p","tls":"tls","sni":"s","alpn":"h2,http/1.1","fp":"chrome"}
type vmessJSON struct {
	Ps   string          `json:"ps"`
	Add  string          `json:"add"`
	Port json.RawMessage `json:"port"`
	ID   string          `json:"id"`
	Aid  json.RawMessage `json:"aid"`
	Scy  string          `json:"scy"`
	Net  string          `json:"net"`
	Type string          `json:"type"`
	Host string          `json:"host"`
	Path string          `json:"path"`
	TLS  string          `json:"tls"`
	SNI  string          `json:"sni"`
	ALPN string          `json:"alpn"`
	Fp   string          `json:"fp"`
}

func rawToInt(r json.RawMessage) int {
	s := strings.Trim(strings.TrimSpace(string(r)), `"`)
	n, _ := strconv.Atoi(s)
	return n
}

func parseVMess(_ *url.URL, raw string) (*Node, error) {
	body := strings.TrimPrefix(strings.TrimSpace(raw), "vmess://")
	dec, ok := b64Decode(body)
	if !ok {
		return nil, fmt.Errorf("无法解码 base64")
	}
	var v vmessJSON
	if err := json.Unmarshal([]byte(dec), &v); err != nil {
		return nil, fmt.Errorf("JSON 无效: %w", err)
	}
	port := rawToInt(v.Port)
	if v.Add == "" || port == 0 || v.ID == "" {
		return nil, fmt.Errorf("缺少 add/port/id")
	}

	o := Base("vmess", v.Ps, v.Add, port)
	o["uuid"] = v.ID
	o["security"] = orDefault(v.Scy, "auto")
	o["alter_id"] = rawToInt(v.Aid)

	if v.TLS == "tls" || v.TLS == "reality" {
		sni := orDefault(v.SNI, orDefault(v.Host, v.Add))
		o["tls"] = TLSBlock(TLSOpts{Enabled: true, SNI: sni, ALPN: splitCSV(v.ALPN), Fingerprint: v.Fp})
	}
	net := v.Net
	if net == "tcp" && v.Type == "http" {
		net = "http" // tcp + http 伪装
	}
	SetIf(o, "transport", TransportBlock(TransportOpts{
		Net: net, Path: v.Path, Host: v.Host, ServiceName: v.Path,
	}))
	return &Node{Tag: v.Ps, Outbound: o}, nil
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}
