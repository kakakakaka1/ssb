package link

import (
	"fmt"
	"net/url"
)

// anytls://password@host:port/?sni=example.org&insecure=1&fp=chrome#name
// De-facto scheme used by mihomo / NekoBox / v2rayN; AnyTLS is TLS-always.
// sing-box has a native anytls outbound since 1.12.
func parseAnyTLS(u *url.URL, _ string) (*Node, error) {
	host, port, err := hostPort(u)
	if err != nil {
		return nil, err
	}
	pass := u.User.Username()
	if p, ok := u.User.Password(); ok && p != "" { // rare user:pass form
		pass = pass + ":" + p
	}
	if pass == "" {
		return nil, fmt.Errorf("缺少 password")
	}
	q := u.Query()

	o := Base("anytls", fragTag(u), host, port)
	o["password"] = pass
	tls := TLSOpts{
		Enabled:     true,
		SNI:         firstParam(q, "sni", "peer"),
		Insecure:    boolParam(q, "insecure", "allowInsecure"),
		ALPN:        splitCSV(q.Get("alpn")),
		Fingerprint: q.Get("fp"),
	}
	if tls.SNI == "" {
		tls.SNI = host
	}
	o["tls"] = TLSBlock(tls)
	return &Node{Tag: fragTag(u), Outbound: o}, nil
}
