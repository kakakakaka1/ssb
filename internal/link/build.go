package link

// Shared builders for sing-box "tls" and "transport" blocks. Field names track
// sing-box 1.13.x; the generated config is always validated by `sing-box check`.

// TLSOpts describes what a share link told us about TLS.
type TLSOpts struct {
	Enabled     bool
	SNI         string
	Insecure    bool
	ALPN        []string
	Fingerprint string // uTLS fingerprint (fp=)
	RealityPBK  string
	RealitySID  string
}

// TLSBlock renders a sing-box tls object, or nil when disabled.
func TLSBlock(o TLSOpts) map[string]any {
	if !o.Enabled {
		return nil
	}
	t := map[string]any{"enabled": true}
	if o.SNI != "" {
		t["server_name"] = o.SNI
	}
	if o.Insecure {
		t["insecure"] = true
	}
	if len(o.ALPN) > 0 {
		t["alpn"] = o.ALPN
	}
	if o.Fingerprint != "" {
		t["utls"] = map[string]any{"enabled": true, "fingerprint": o.Fingerprint}
	}
	if o.RealityPBK != "" {
		t["reality"] = map[string]any{"enabled": true, "public_key": o.RealityPBK, "short_id": o.RealitySID}
	}
	return t
}

// TransportOpts describes a v2ray-style transport from a share link.
type TransportOpts struct {
	Net         string // ws / grpc / httpupgrade / http (h2) / tcp("")
	Path        string
	Host        string // Host header / http host
	ServiceName string // grpc
}

// TransportBlock renders a sing-box transport object, or nil for plain TCP.
func TransportBlock(o TransportOpts) map[string]any {
	switch o.Net {
	case "ws", "websocket":
		t := map[string]any{"type": "ws"}
		path, ed := splitEarlyData(o.Path)
		if path != "" {
			t["path"] = path
		}
		if o.Host != "" {
			t["headers"] = map[string]any{"Host": o.Host}
		}
		if ed > 0 {
			t["max_early_data"] = ed
			t["early_data_header_name"] = "Sec-WebSocket-Protocol"
		}
		return t
	case "grpc", "gun":
		t := map[string]any{"type": "grpc"}
		if o.ServiceName != "" {
			t["service_name"] = o.ServiceName
		}
		return t
	case "httpupgrade":
		t := map[string]any{"type": "httpupgrade"}
		if o.Path != "" {
			t["path"] = o.Path
		}
		if o.Host != "" {
			t["host"] = o.Host
		}
		return t
	case "http", "h2", "tcp-http": // h2 / http obfs
		t := map[string]any{"type": "http"}
		if o.Path != "" {
			t["path"] = o.Path
		}
		if o.Host != "" {
			t["host"] = []string{o.Host}
		}
		return t
	default: // "", "tcp", "raw"
		return nil
	}
}

// splitEarlyData strips a "?ed=2048" style suffix from a ws path.
func splitEarlyData(path string) (string, int) {
	if i := indexOf(path, "?ed="); i >= 0 {
		n := 0
		for _, c := range path[i+4:] {
			if c < '0' || c > '9' {
				break
			}
			n = n*10 + int(c-'0')
		}
		return path[:i], n
	}
	return path, 0
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

// Base assembles the common outbound skeleton.
func Base(typ, tag, server string, port int) map[string]any {
	return map[string]any{"type": typ, "tag": tag, "server": server, "server_port": port}
}

// SetIf sets k=v when v is a non-zero value.
func SetIf(m map[string]any, k string, v any) {
	switch x := v.(type) {
	case string:
		if x != "" {
			m[k] = x
		}
	case []string:
		if len(x) > 0 {
			m[k] = x
		}
	case map[string]any:
		if x != nil {
			m[k] = x
		}
	case int:
		if x != 0 {
			m[k] = x
		}
	case bool:
		if x {
			m[k] = x
		}
	default:
		if v != nil {
			m[k] = v
		}
	}
}
