package link

import (
	"fmt"
	"net/url"
	"strings"
)

// ss:// comes in three shapes:
//  1. SIP002:        ss://base64url(method:password)@host:port/?plugin=...#tag
//  2. SIP002 (2022): ss://2022-blake3-aes-256-gcm:pct-encoded-key@host:port#tag
//  3. legacy:        ss://base64(method:password@host:port)#tag
func parseSS(_ *url.URL, raw string) (*Node, error) {
	body := strings.TrimPrefix(strings.TrimSpace(raw), "ss://")
	tag := ""
	if i := strings.LastIndex(body, "#"); i >= 0 {
		tag, _ = url.PathUnescape(body[i+1:])
		body = body[:i]
	}
	if !strings.Contains(body, "@") { // legacy whole-base64 form
		dec, ok := b64Decode(body)
		if !ok || !strings.Contains(dec, "@") {
			return nil, fmt.Errorf("无法解码 legacy base64")
		}
		body = dec
	}
	u, err := url.Parse("ss://" + body)
	if err != nil {
		return nil, fmt.Errorf("链接格式错误: %w", err)
	}

	host, port, err := hostPort(u)
	if err != nil {
		return nil, err
	}
	method, pass := "", ""
	if p, ok := u.User.Password(); ok { // plain "method:password" userinfo (2022 style / legacy decoded)
		method, pass = u.User.Username(), p
	} else {
		ui := u.User.Username()
		if dec, ok := b64Decode(ui); ok && strings.Contains(dec, ":") {
			method, pass, _ = strings.Cut(dec, ":")
		} else if strings.Contains(ui, ":") {
			method, pass, _ = strings.Cut(ui, ":")
		} else {
			return nil, fmt.Errorf("无法解析 method:password")
		}
	}
	if method == "" || pass == "" {
		return nil, fmt.Errorf("method/password 为空")
	}

	o := Base("shadowsocks", tag, host, port)
	o["method"] = method
	o["password"] = pass

	if plugin := u.Query().Get("plugin"); plugin != "" {
		name, opts, _ := strings.Cut(plugin, ";")
		switch name {
		case "obfs-local", "simple-obfs":
			o["plugin"] = "obfs-local"
		case "v2ray-plugin":
			o["plugin"] = "v2ray-plugin"
		default:
			o["plugin"] = name
		}
		SetIf(o, "plugin_opts", opts)
	}
	return &Node{Tag: tag, Outbound: o}, nil
}
