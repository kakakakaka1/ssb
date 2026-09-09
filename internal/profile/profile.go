// Package profile persists ssb's own state (subscriptions, manual nodes,
// settings) in a single JSON file inside the working directory.
package profile

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"ssb/internal/link"
	"ssb/internal/sub"
)

// Settings are user-tunable knobs surfaced in the TUI 设置页.
type Settings struct {
	RouteMode       string   `json:"route_mode"`              // "rule"（绕过大陆）| "global"
	TunEnabled      bool     `json:"tun_enabled"`             // TUN 入站（需要 root/CAP_NET_ADMIN）
	TunAddress      string   `json:"tun_address"`             // TUN 虚拟网卡 IPv4 地址（CIDR，默认 10.255.0.1/30，避开 Docker 172.x 网段）
	IPv6            string   `json:"ipv6"`                    // auto（按内核探测）| on | off；内核关掉 IPv6 时必须 off
	AutoRedirect    bool     `json:"auto_redirect"`           // Linux nftables 加速（默认关，兼容性优先）
	MixedEnabled    bool     `json:"mixed_enabled"`           // 本地 mixed(socks/http) 入站
	MixedPort       int      `json:"mixed_port"`              // 默认 2080
	APIListen       string   `json:"api_listen"`              // sing-box API 服务监听 host:port（TUI 切节点 + 官方面板）
	APISecret       string   `json:"api_secret"`              // 首次生成随机值
	DashboardOff    bool     `json:"dashboard_off"`           // 关闭网页面板（API 服务仍监听，TUI 切换节点用）
	MirrorPrefix    string   `json:"mirror_prefix"`           // GitHub 镜像前缀（UI/内核下载用），如 https://ghproxy.net/
	FakeIP          bool     `json:"fakeip"`                  // FakeIP DNS
	DownloadDetour  string   `json:"download_detour"`         // 规则集/UI 下载出站: direct | PROXY
	RuleSetSource   string   `json:"ruleset_source"`          // 规则集来源: jsdelivr（默认，国内可直连的 CDN）| github（raw.githubusercontent.com，套镜像前缀）
	DNSCN           string   `json:"dns_cn"`                  // 国内直连 DNS（udp）
	DNSProxy        string   `json:"dns_proxy"`               // 代理侧 DNS（https）
	SingboxPath     string   `json:"singbox_path"`            // 手动指定 sing-box 路径（可空）
	LogLevel        string   `json:"log_level"`               // sing-box 日志级别（默认 warn，防日志膨胀）
	AdvancedRouting bool     `json:"advanced_routing"`        // 设置页显示自定义分流
	CustomProxy     []string `json:"custom_proxy,omitempty"`  // 强制走代理的域名（含子域名）
	CustomDirect    []string `json:"custom_direct,omitempty"` // 强制直连的域名（含子域名）
}

// Subscription is one remote subscription and its last fetch result.
type Subscription struct {
	Name      string        `json:"name"`
	URL       string        `json:"url"`
	UpdatedAt time.Time     `json:"updated_at"`
	Format    string        `json:"format,omitempty"`
	Userinfo  *sub.Userinfo `json:"userinfo,omitempty"`
	Nodes     []*link.Node  `json:"nodes,omitempty"`
}

// State is everything ssb remembers between runs.
type State struct {
	Subscriptions []*Subscription `json:"subscriptions"`
	Manual        []*link.Node    `json:"manual_nodes"`
	Settings      Settings        `json:"settings"`
}

func defaultSettings() Settings {
	return Settings{
		RouteMode:      "rule",
		TunEnabled:     true,
		TunAddress:     "10.255.0.1/30",
		IPv6:           "auto",
		AutoRedirect:   false,
		MixedEnabled:   true,
		MixedPort:      2080,
		APIListen:      "127.0.0.1:9090",
		APISecret:      RandomSecret(),
		DashboardOff:   true, // 默认只用 TUI 控制，网页面板按需开启
		FakeIP:         true,
		DownloadDetour: "direct",
		RuleSetSource:  "jsdelivr",
		DNSCN:          "223.5.5.5",
		DNSProxy:       "8.8.8.8",
		LogLevel:       "warn",
	}
}

func RandomSecret() string {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		return "ssb-secret"
	}
	return hex.EncodeToString(b)
}

// Dirs resolves every path ssb touches, all under one base directory.
type Dirs struct{ Base string }

func (d Dirs) StateFile() string    { return filepath.Join(d.Base, "data", "state.json") }
func (d Dirs) ConfigFile() string   { return filepath.Join(d.Base, "data", "config.json") }
func (d Dirs) BackupFile() string   { return filepath.Join(d.Base, "data", "config.json.bak") }
func (d Dirs) SingboxBin() string   { return filepath.Join(d.Base, "data", singboxBinName(runtime.GOOS)) }
func (d Dirs) DataDir() string      { return filepath.Join(d.Base, "data") }
func (d Dirs) DashboardDir() string { return filepath.Join(d.Base, "data", "dashboard") }
func (d Dirs) CacheDB() string      { return filepath.Join(d.Base, "data", "cache.db") }
func (d Dirs) LogDir() string       { return filepath.Join(d.Base, "logs") }
func (d Dirs) LogFile() string      { return filepath.Join(d.Base, "logs", "sing-box.log") }
func (d Dirs) RunDir() string       { return filepath.Join(d.Base, "run") }
func (d Dirs) PidFile() string      { return filepath.Join(d.Base, "run", "sing-box.pid") }

// singboxBinName: 内核文件名，Windows 上带 .exe（exec.LookPath 找 PATH 时会自动补，
// 但 data/ 里的那份要我们自己写对）。
func singboxBinName(goos string) string {
	if goos == "windows" {
		return "sing-box.exe"
	}
	return "sing-box"
}

func (d Dirs) Ensure() error {
	for _, p := range []string{d.DataDir(), d.DashboardDir(), d.LogDir(), d.RunDir()} {
		if err := os.MkdirAll(p, 0o755); err != nil {
			return err
		}
	}
	return nil
}

// Load reads state.json, creating defaults on first run.
func Load(d Dirs) (*State, error) {
	st := &State{Settings: defaultSettings()}
	b, err := os.ReadFile(d.StateFile())
	if os.IsNotExist(err) {
		return st, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, st); err != nil {
		return nil, fmt.Errorf("state.json 损坏: %w", err)
	}
	// 补齐旧版本缺失的默认值
	if st.Settings.MixedPort == 0 {
		st.Settings.MixedPort = 2080
	}
	if st.Settings.APIListen == "" {
		st.Settings.APIListen = "127.0.0.1:9090"
	}
	if st.Settings.APISecret == "" {
		st.Settings.APISecret = RandomSecret()
	}
	if st.Settings.RouteMode == "" {
		st.Settings.RouteMode = "rule"
	}
	if st.Settings.DownloadDetour == "" {
		st.Settings.DownloadDetour = "direct"
	}
	if st.Settings.RuleSetSource == "" {
		st.Settings.RuleSetSource = "jsdelivr"
	}
	if st.Settings.DNSCN == "" {
		st.Settings.DNSCN = "223.5.5.5"
	}
	if st.Settings.DNSProxy == "" {
		st.Settings.DNSProxy = "8.8.8.8"
	}
	if st.Settings.LogLevel == "" {
		st.Settings.LogLevel = "warn"
	}
	if st.Settings.IPv6 == "" {
		st.Settings.IPv6 = "auto"
	}
	if st.Settings.TunAddress == "" {
		st.Settings.TunAddress = "10.255.0.1/30"
	}
	return st, nil
}

// Save writes state.json atomically (tmp + rename).
func (st *State) Save(d Dirs) error {
	if err := d.Ensure(); err != nil {
		return err
	}
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	tmp := d.StateFile() + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, d.StateFile())
}

// AllNodes returns every node (manual first, then per subscription) with
// duplicate tags disambiguated, since sing-box requires unique outbound tags.
func (st *State) AllNodes() []*link.Node {
	seen := map[string]int{}
	var out []*link.Node
	add := func(n *link.Node) {
		if n == nil || n.Outbound == nil {
			return
		}
		tag := n.Tag
		if tag == "" {
			tag = "node"
		}
		seen[tag]++
		if c := seen[tag]; c > 1 {
			tag = fmt.Sprintf("%s #%d", tag, c)
		}
		// 拷贝一份 outbound，避免重名改写污染原始数据
		ob := make(map[string]any, len(n.Outbound))
		for k, v := range n.Outbound {
			ob[k] = v
		}
		ob["tag"] = tag
		out = append(out, &link.Node{Tag: tag, Raw: n.Raw, Outbound: ob})
	}
	for _, n := range st.Manual {
		add(n)
	}
	for _, s := range st.Subscriptions {
		for _, n := range s.Nodes {
			add(n)
		}
	}
	return out
}

// FindSub returns the subscription with the given name, or nil.
func (st *State) FindSub(name string) *Subscription {
	for _, s := range st.Subscriptions {
		if s.Name == name {
			return s
		}
	}
	return nil
}
