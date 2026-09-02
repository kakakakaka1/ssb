// Package sub fetches subscription URLs and extracts nodes from the three
// formats seen in the wild: sing-box JSON, base64/plain share-link lists,
// and Clash YAML.
package sub

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"ssb/internal/clash2sb"
	"ssb/internal/link"
)

// Userinfo mirrors the subscription-userinfo response header.
type Userinfo struct {
	Upload   int64 `json:"upload"`
	Download int64 `json:"download"`
	Total    int64 `json:"total"`
	Expire   int64 `json:"expire"` // unix seconds, 0 = unknown
}

// Result of one subscription fetch.
type Result struct {
	Nodes    []*link.Node
	Format   string // "sing-box" | "base64/uri" | "clash"
	Userinfo *Userinfo
	Warnings []string
}

// userAgents tried in order: panels sniff the UA to pick a response format.
var userAgents = []string{
	"sing-box/1.14.0 (ssb; SFA compatible)",
	"clash.meta/1.19.0 mihomo (ssb)",
	"v2rayN/7.0 (ssb)",
}

// Fetch downloads and parses a subscription. It retries with different
// User-Agents until one response yields at least one node.
func Fetch(ctx context.Context, rawURL string) (*Result, error) {
	client := &http.Client{Timeout: 20 * time.Second}
	var lastErr error
	var ui *Userinfo
	for _, ua := range userAgents {
		body, header, err := get(ctx, client, rawURL, ua)
		if err != nil {
			lastErr = err
			continue
		}
		if u := parseUserinfo(header.Get("subscription-userinfo")); u != nil {
			ui = u
		}
		res, err := ParseBody(body)
		if err != nil {
			lastErr = err
			continue
		}
		res.Userinfo = ui
		return res, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("订阅内容为空")
	}
	return nil, fmt.Errorf("订阅获取失败: %w", lastErr)
}

func get(ctx context.Context, c *http.Client, url, ua string) (string, http.Header, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", nil, err
	}
	req.Header.Set("User-Agent", ua)
	resp, err := c.Do(req)
	if err != nil {
		return "", nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return "", nil, err
	}
	return string(b), resp.Header, nil
}

// ParseBody detects the payload format and extracts nodes.
func ParseBody(body string) (*Result, error) {
	trimmed := strings.TrimSpace(body)
	if trimmed == "" {
		return nil, fmt.Errorf("内容为空")
	}

	// 1) sing-box JSON config: {"outbounds": [...]}
	if strings.HasPrefix(trimmed, "{") {
		if nodes, ok := fromSingboxJSON(trimmed); ok && len(nodes) > 0 {
			return &Result{Nodes: nodes, Format: "sing-box"}, nil
		}
	}
	// 2) Clash YAML
	if strings.Contains(trimmed, "proxies:") {
		nodes, errs := clash2sb.Convert(trimmed)
		if len(nodes) > 0 {
			return &Result{Nodes: nodes, Format: "clash", Warnings: errStrings(errs)}, nil
		}
	}
	// 3) base64 blob or plain share-link lines
	nodes, errs := link.ParseMixed(trimmed)
	if len(nodes) > 0 {
		return &Result{Nodes: nodes, Format: "base64/uri", Warnings: errStrings(errs)}, nil
	}
	return nil, fmt.Errorf("无法识别订阅格式（既不是 sing-box JSON / Clash YAML，也不是链接列表）")
}

// proxy outbound types accepted from a sing-box JSON subscription.
var groupTypes = map[string]bool{
	"selector": true, "urltest": true, "direct": true, "block": true, "dns": true,
}

func fromSingboxJSON(text string) ([]*link.Node, bool) {
	var cfg struct {
		Outbounds []map[string]any `json:"outbounds"`
	}
	if err := json.Unmarshal([]byte(text), &cfg); err != nil {
		return nil, false
	}
	var nodes []*link.Node
	for _, o := range cfg.Outbounds {
		typ, _ := o["type"].(string)
		tag, _ := o["tag"].(string)
		if typ == "" || tag == "" || groupTypes[typ] {
			continue
		}
		normalizeNumbers(o)
		nodes = append(nodes, &link.Node{Tag: tag, Outbound: o})
	}
	return nodes, true
}

// normalizeNumbers turns float64 JSON numbers into ints where sing-box expects
// integers (ports etc.) so re-marshaling stays clean.
func normalizeNumbers(m map[string]any) {
	for k, v := range m {
		switch x := v.(type) {
		case float64:
			if x == float64(int64(x)) {
				m[k] = int(x)
			}
		case map[string]any:
			normalizeNumbers(x)
		case []any:
			for _, e := range x {
				if em, ok := e.(map[string]any); ok {
					normalizeNumbers(em)
				}
			}
		}
	}
}

// parseUserinfo parses "upload=123; download=456; total=789; expire=1750000000".
func parseUserinfo(h string) *Userinfo {
	if strings.TrimSpace(h) == "" {
		return nil
	}
	u := &Userinfo{}
	for _, part := range strings.Split(h, ";") {
		k, v, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok {
			continue
		}
		n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
		if err != nil {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(k)) {
		case "upload":
			u.Upload = n
		case "download":
			u.Download = n
		case "total":
			u.Total = n
		case "expire":
			u.Expire = n
		}
	}
	return u
}

func errStrings(errs []error) []string {
	var out []string
	for _, e := range errs {
		out = append(out, e.Error())
	}
	return out
}

// HumanBytes renders byte counts for the TUI (e.g. 1.5 GB).
func HumanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}
