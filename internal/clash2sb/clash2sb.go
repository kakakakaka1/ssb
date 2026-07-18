// Package clash2sb converts Clash / mihomo YAML `proxies:` entries into
// sing-box outbounds, reusing the builders from internal/link.
package clash2sb

import (
	"fmt"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"ssb/internal/link"
)

type doc struct {
	Proxies []map[string]any `yaml:"proxies"`
}

// Convert parses a Clash YAML document and returns the supported proxies.
func Convert(text string) ([]*link.Node, []error) {
	var d doc
	if err := yaml.Unmarshal([]byte(text), &d); err != nil {
		return nil, []error{fmt.Errorf("YAML 解析失败: %w", err)}
	}
	var nodes []*link.Node
	var errs []error
	for _, p := range d.Proxies {
		n, err := convertOne(p)
		if err != nil {
			errs = append(errs, fmt.Errorf("%v: %w", p["name"], err))
			continue
		}
		nodes = append(nodes, n)
	}
	return nodes, errs
}

func convertOne(p map[string]any) (*link.Node, error) {
	typ := str(p, "type")
	tag := str(p, "name")
	server := str(p, "server")
	port := integer(p, "port")
	if server == "" || port == 0 {
		return nil, fmt.Errorf("缺少 server/port")
	}

	var o map[string]any
	switch typ {
	case "ss":
		o = link.Base("shadowsocks", tag, server, port)
		o["method"] = str(p, "cipher")
		o["password"] = str(p, "password")
		if pl := str(p, "plugin"); pl != "" {
			opts := mapAny(p, "plugin-opts")
			switch pl {
			case "obfs":
				o["plugin"] = "obfs-local"
				o["plugin_opts"] = joinOpts("obfs="+str(opts, "mode"), kv("obfs-host", str(opts, "host")))
			case "v2ray-plugin":
				o["plugin"] = "v2ray-plugin"
				parts := []string{}
				if boolean(opts, "tls") {
					parts = append(parts, "tls")
				}
				parts = append(parts, kv("host", str(opts, "host")), kv("path", str(opts, "path")))
				o["plugin_opts"] = joinOpts(parts...)
			}
		}
	case "vmess":
		o = link.Base("vmess", tag, server, port)
		o["uuid"] = str(p, "uuid")
		o["security"] = strOr(p, "cipher", "auto")
		o["alter_id"] = integer(p, "alterId")
		if boolean(p, "tls") {
			o["tls"] = link.TLSBlock(tlsOpts(p, server))
		}
		setTransport(o, p)
	case "vless":
		o = link.Base("vless", tag, server, port)
		o["uuid"] = str(p, "uuid")
		link.SetIf(o, "flow", str(p, "flow"))
		reality := mapAny(p, "reality-opts")
		if boolean(p, "tls") || reality != nil {
			t := tlsOpts(p, server)
			if reality != nil {
				t.RealityPBK = str(reality, "public-key")
				t.RealitySID = str(reality, "short-id")
				if t.Fingerprint == "" {
					t.Fingerprint = "chrome"
				}
			}
			o["tls"] = link.TLSBlock(t)
		}
		setTransport(o, p)
	case "trojan":
		o = link.Base("trojan", tag, server, port)
		o["password"] = str(p, "password")
		o["tls"] = link.TLSBlock(tlsOpts(p, server))
		setTransport(o, p)
	case "hysteria2":
		o = link.Base("hysteria2", tag, server, port)
		o["password"] = strOr(p, "password", str(p, "auth"))
		if ob := str(p, "obfs"); ob != "" && ob != "none" {
			o["obfs"] = map[string]any{"type": ob, "password": str(p, "obfs-password")}
		}
		if ports := str(p, "ports"); ports != "" {
			o["server_ports"] = portRanges(ports)
		}
		t := tlsOpts(p, server)
		t.Enabled = true
		if len(t.ALPN) == 0 {
			t.ALPN = []string{"h3"}
		}
		o["tls"] = link.TLSBlock(t)
	case "tuic":
		o = link.Base("tuic", tag, server, port)
		o["uuid"] = str(p, "uuid")
		link.SetIf(o, "password", str(p, "password"))
		link.SetIf(o, "congestion_control", strOr(p, "congestion-controller", str(p, "congestion-control")))
		link.SetIf(o, "udp_relay_mode", str(p, "udp-relay-mode"))
		t := tlsOpts(p, server)
		t.Enabled = true
		if len(t.ALPN) == 0 {
			t.ALPN = []string{"h3"}
		}
		o["tls"] = link.TLSBlock(t)
	case "anytls":
		o = link.Base("anytls", tag, server, port)
		o["password"] = str(p, "password")
		t := tlsOpts(p, server)
		t.Enabled = true
		o["tls"] = link.TLSBlock(t)
	default:
		return nil, fmt.Errorf("暂不支持的 clash 代理类型 %q", typ)
	}
	o["tag"] = tag
	return &link.Node{Tag: tag, Outbound: o}, nil
}

// tlsOpts collects the common clash TLS-ish fields.
func tlsOpts(p map[string]any, server string) link.TLSOpts {
	t := link.TLSOpts{
		Enabled:     true,
		SNI:         strOr(p, "servername", str(p, "sni")),
		Insecure:    boolean(p, "skip-cert-verify"),
		ALPN:        strSlice(p, "alpn"),
		Fingerprint: str(p, "client-fingerprint"),
	}
	if t.SNI == "" {
		t.SNI = server
	}
	return t
}

func setTransport(o, p map[string]any) {
	net := str(p, "network")
	if net == "" || net == "tcp" {
		return
	}
	to := link.TransportOpts{Net: net}
	switch net {
	case "ws":
		opts := mapAny(p, "ws-opts")
		to.Path = str(opts, "path")
		if h := mapAny(opts, "headers"); h != nil {
			to.Host = strOr(h, "Host", str(h, "host"))
		}
	case "grpc":
		to.ServiceName = str(mapAny(p, "grpc-opts"), "grpc-service-name")
	case "h2":
		opts := mapAny(p, "h2-opts")
		to.Path = str(opts, "path")
		if hosts := strSlice(opts, "host"); len(hosts) > 0 {
			to.Host = hosts[0]
		}
	case "httpupgrade":
		opts := mapAny(p, "httpupgrade-opts")
		to.Path = str(opts, "path")
		if h := mapAny(opts, "headers"); h != nil {
			to.Host = strOr(h, "Host", str(h, "host"))
		}
	}
	link.SetIf(o, "transport", link.TransportBlock(to))
}

// ---- yaml value helpers (yaml.v3 decodes into map[string]any with assorted types) ----

func str(m map[string]any, k string) string {
	if m == nil {
		return ""
	}
	switch v := m[k].(type) {
	case string:
		return v
	case int:
		return strconv.Itoa(v)
	case float64:
		return strconv.Itoa(int(v))
	case bool:
		return strconv.FormatBool(v)
	}
	return ""
}

func strOr(m map[string]any, k, def string) string {
	if s := str(m, k); s != "" {
		return s
	}
	return def
}

func integer(m map[string]any, k string) int {
	if m == nil {
		return 0
	}
	switch v := m[k].(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	case string:
		n, _ := strconv.Atoi(strings.TrimSpace(v))
		return n
	}
	return 0
}

func boolean(m map[string]any, k string) bool {
	if m == nil {
		return false
	}
	switch v := m[k].(type) {
	case bool:
		return v
	case string:
		return v == "true" || v == "1"
	}
	return false
}

func mapAny(m map[string]any, k string) map[string]any {
	if m == nil {
		return nil
	}
	switch v := m[k].(type) {
	case map[string]any:
		return v
	case map[any]any: // yaml.v2 style, just in case
		out := map[string]any{}
		for kk, vv := range v {
			out[fmt.Sprint(kk)] = vv
		}
		return out
	}
	return nil
}

func strSlice(m map[string]any, k string) []string {
	if m == nil {
		return nil
	}
	switch v := m[k].(type) {
	case []string:
		return v
	case []any:
		var out []string
		for _, e := range v {
			out = append(out, fmt.Sprint(e))
		}
		return out
	case string:
		if v == "" {
			return nil
		}
		var out []string
		for _, part := range strings.Split(v, ",") {
			if part = strings.TrimSpace(part); part != "" {
				out = append(out, part)
			}
		}
		return out
	}
	return nil
}

func portRanges(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if a, b, ok := strings.Cut(part, "-"); ok {
			out = append(out, a+":"+b)
		} else {
			out = append(out, part+":"+part)
		}
	}
	return out
}

func kv(k, v string) string {
	if v == "" {
		return ""
	}
	return k + "=" + v
}

func joinOpts(parts ...string) string {
	var keep []string
	for _, p := range parts {
		if p != "" {
			keep = append(keep, p)
		}
	}
	return strings.Join(keep, ";")
}
