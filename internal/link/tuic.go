package link

import (
	"fmt"
	"net/url"
)

// tuic://uuid:password@host:port?congestion_control=bbr&alpn=h3&sni=...&udp_relay_mode=native#name
func parseTUIC(u *url.URL, _ string) (*Node, error) {
	host, port, err := hostPort(u)
	if err != nil {
		return nil, err
	}
	uuid := u.User.Username()
	pass, _ := u.User.Password()
	if uuid == "" {
		return nil, fmt.Errorf("缺少 uuid")
	}
	q := u.Query()

	o := Base("tuic", fragTag(u), host, port)
	o["uuid"] = uuid
	SetIf(o, "password", pass)
	SetIf(o, "congestion_control", firstParam(q, "congestion_control", "congestion_controller"))
	SetIf(o, "udp_relay_mode", q.Get("udp_relay_mode"))
	tls := TLSOpts{
		Enabled:  true, // TUIC is QUIC/TLS-always
		SNI:      firstParam(q, "sni", "peer"),
		Insecure: boolParam(q, "allow_insecure", "allowInsecure", "insecure"),
		ALPN:     splitCSV(q.Get("alpn")),
	}
	if tls.SNI == "" {
		tls.SNI = host
	}
	if len(tls.ALPN) == 0 {
		tls.ALPN = []string{"h3"}
	}
	o["tls"] = TLSBlock(tls)
	return &Node{Tag: fragTag(u), Outbound: o}, nil
}
