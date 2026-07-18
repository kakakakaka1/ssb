// Package link parses proxy share links (vless://, anytls://, ss://, vmess://,
// trojan://, hysteria2://, tuic://) into sing-box outbound objects.
package link

import (
	"encoding/base64"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// Node is one parsed proxy node.
type Node struct {
	Tag      string         `json:"tag"`
	Raw      string         `json:"raw"`      // original share link (empty if from clash yaml / sing-box json)
	Outbound map[string]any `json:"outbound"` // complete sing-box outbound, including "type" and "tag"
}

type parser func(u *url.URL, raw string) (*Node, error)

var parsers = map[string]parser{
	"vless":     parseVLESS,
	"anytls":    parseAnyTLS,
	"trojan":    parseTrojan,
	"ss":        parseSS,
	"vmess":     parseVMess,
	"hysteria2": parseHysteria2,
	"hy2":       parseHysteria2,
	"tuic":      parseTUIC,
}

// Supported reports whether the URI scheme can be parsed.
func Supported(scheme string) bool { _, ok := parsers[strings.ToLower(scheme)]; return ok }

// Parse parses a single share link.
func Parse(raw string) (*Node, error) {
	raw = strings.TrimSpace(raw)
	scheme, _, ok := strings.Cut(raw, "://")
	if !ok {
		return nil, fmt.Errorf("不是分享链接: %q", truncate(raw, 40))
	}
	p, ok := parsers[strings.ToLower(scheme)]
	if !ok {
		return nil, fmt.Errorf("暂不支持的协议 %s://", scheme)
	}
	// ss:// legacy form and vmess:// carry base64 that url.Parse can choke on;
	// their parsers handle raw themselves. Others go through url.Parse.
	u, err := url.Parse(raw)
	if err != nil {
		u = nil
		if scheme != "vmess" && scheme != "ss" {
			return nil, fmt.Errorf("链接格式错误: %w", err)
		}
	}
	n, err := p(u, raw)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", scheme, err)
	}
	if n.Tag == "" {
		n.Tag = fmt.Sprintf("%v:%v", n.Outbound["server"], n.Outbound["server_port"])
	}
	n.Outbound["tag"] = n.Tag
	n.Raw = raw
	return n, nil
}

// ParseMixed extracts nodes from free-form text: one link per line, or a
// base64-encoded blob of links (the common subscription body format).
func ParseMixed(text string) (nodes []*Node, errs []error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, nil
	}
	if !strings.Contains(text, "://") {
		if dec, ok := b64Decode(text); ok && strings.Contains(dec, "://") {
			text = dec
		}
	}
	for _, line := range strings.FieldsFunc(text, func(r rune) bool { return r == '\n' || r == '\r' || r == ' ' || r == '\t' }) {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "//") {
			continue
		}
		if !strings.Contains(line, "://") {
			continue
		}
		n, err := Parse(line)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		nodes = append(nodes, n)
	}
	return nodes, errs
}

// ---- shared helpers ----

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}

// b64Decode tries std/url-safe base64 with or without padding.
func b64Decode(s string) (string, bool) {
	s = strings.TrimSpace(s)
	for _, enc := range []*base64.Encoding{
		base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding,
	} {
		if b, err := enc.DecodeString(s); err == nil {
			return string(b), true
		}
	}
	return "", false
}

func hostPort(u *url.URL) (string, int, error) {
	host := u.Hostname()
	if host == "" {
		return "", 0, fmt.Errorf("缺少服务器地址")
	}
	ps := u.Port()
	if ps == "" {
		return "", 0, fmt.Errorf("缺少端口")
	}
	port, err := strconv.Atoi(ps)
	if err != nil || port <= 0 || port > 65535 {
		return "", 0, fmt.Errorf("端口无效: %q", ps)
	}
	return host, port, nil
}

func fragTag(u *url.URL) string {
	if u == nil {
		return ""
	}
	if u.Fragment != "" {
		return strings.TrimSpace(u.Fragment)
	}
	return ""
}

func splitCSV(s string) []string {
	if s == "" {
		return nil
	}
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func boolParam(q url.Values, keys ...string) bool {
	for _, k := range keys {
		switch strings.ToLower(q.Get(k)) {
		case "1", "true", "yes":
			return true
		}
	}
	return false
}

func firstParam(q url.Values, keys ...string) string {
	for _, k := range keys {
		if v := q.Get(k); v != "" {
			return v
		}
	}
	return ""
}
