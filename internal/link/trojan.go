package link

import (
	"fmt"
	"net/url"
)

// trojan://password@host:port?sni=...&type=ws&path=/x&host=h#name
func parseTrojan(u *url.URL, _ string) (*Node, error) {
	host, port, err := hostPort(u)
	if err != nil {
		return nil, err
	}
	pass := u.User.Username()
	if p, ok := u.User.Password(); ok && p != "" {
		pass = pass + ":" + p
	}
	if pass == "" {
		return nil, fmt.Errorf("缺少 password")
	}
	q := u.Query()

	o := Base("trojan", fragTag(u), host, port)
	o["password"] = pass
	tls := TLSOpts{ // trojan implies TLS unless explicitly security=none
		Enabled:     q.Get("security") != "none",
		SNI:         firstParam(q, "sni", "peer"),
		Insecure:    boolParam(q, "allowInsecure", "insecure"),
		ALPN:        splitCSV(q.Get("alpn")),
		Fingerprint: q.Get("fp"),
	}
	if tls.Enabled && tls.SNI == "" {
		tls.SNI = host
	}
	SetIf(o, "tls", TLSBlock(tls))
	SetIf(o, "transport", TransportBlock(TransportOpts{
		Net:         q.Get("type"),
		Path:        q.Get("path"),
		Host:        q.Get("host"),
		ServiceName: firstParam(q, "serviceName", "path"),
	}))
	return &Node{Tag: fragTag(u), Outbound: o}, nil
}
