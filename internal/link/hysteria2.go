package link

import (
	"fmt"
	"net/url"
	"strings"
)

// hysteria2://auth@host:port/?sni=...&insecure=1&obfs=salamander&obfs-password=xx&mport=2000-3000#name
// (hy2:// is an alias)
func parseHysteria2(u *url.URL, _ string) (*Node, error) {
	host, port, err := hostPort(u)
	if err != nil {
		return nil, err
	}
	auth := u.User.Username()
	if p, ok := u.User.Password(); ok && p != "" {
		auth = auth + ":" + p
	}
	q := u.Query()

	o := Base("hysteria2", fragTag(u), host, port)
	SetIf(o, "password", auth)
	if obfs := q.Get("obfs"); obfs != "" && obfs != "none" {
		o["obfs"] = map[string]any{"type": obfs, "password": q.Get("obfs-password")}
	}
	if mport := firstParam(q, "mport", "ports"); mport != "" {
		o["server_ports"] = portRanges(mport)
		SetIf(o, "hop_interval", hopInterval(q.Get("hop-interval")))
	}
	tls := TLSOpts{
		Enabled:  true, // hysteria2 is QUIC/TLS-always
		SNI:      firstParam(q, "sni", "peer"),
		Insecure: boolParam(q, "insecure", "allowInsecure"),
		ALPN:     []string{"h3"},
	}
	if tls.SNI == "" {
		tls.SNI = host
	}
	o["tls"] = TLSBlock(tls)
	return &Node{Tag: fragTag(u), Outbound: o}, nil
}

// portRanges converts "443,2000-3000" into sing-box ["443:443","2000:3000"].
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

func hopInterval(s string) string {
	if s == "" {
		return ""
	}
	if _, err := fmt.Sscanf(s, "%d", new(int)); err == nil && !strings.ContainsAny(s, "smh") {
		return s + "s"
	}
	return s
}
