package link

import (
	"fmt"
	"net/url"
)

// vless://uuid@host:port?type=ws&security=reality&pbk=...&sid=...&fp=chrome&flow=xtls-rprx-vision&sni=...#name
// Follows the v2ray share-link standard (XTLS/Xray-core discussions#716).
func parseVLESS(u *url.URL, _ string) (*Node, error) {
	host, port, err := hostPort(u)
	if err != nil {
		return nil, err
	}
	uuid := u.User.Username()
	if uuid == "" {
		return nil, fmt.Errorf("缺少 UUID")
	}
	q := u.Query()

	o := Base("vless", fragTag(u), host, port)
	o["uuid"] = uuid
	SetIf(o, "flow", q.Get("flow"))

	sec := q.Get("security")
	tls := TLSOpts{
		Enabled:     sec == "tls" || sec == "reality" || sec == "xtls",
		SNI:         firstParam(q, "sni", "peer"),
		Insecure:    boolParam(q, "allowInsecure", "insecure"),
		ALPN:        splitCSV(q.Get("alpn")),
		Fingerprint: q.Get("fp"),
	}
	if sec == "reality" {
		tls.RealityPBK = q.Get("pbk")
		tls.RealitySID = q.Get("sid")
		if tls.Fingerprint == "" {
			tls.Fingerprint = "chrome" // reality requires uTLS
		}
	}
	if tls.Enabled && tls.SNI == "" {
		tls.SNI = host
	}
	SetIf(o, "tls", TLSBlock(tls))

	SetIf(o, "transport", TransportBlock(TransportOpts{
		Net:         q.Get("type"),
		Path:        q.Get("path"),
		Host:        firstParam(q, "host"),
		ServiceName: firstParam(q, "serviceName", "path"),
	}))
	return &Node{Tag: fragTag(u), Outbound: o}, nil
}
