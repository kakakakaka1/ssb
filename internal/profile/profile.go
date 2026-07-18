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
	"time"

	"ssb/internal/link"
	"ssb/internal/sub"
)

// Settings are user-tunable knobs surfaced in the TUI 设置页.
type Settings struct {
	RouteMode      string `json:"route_mode"`      // "rule"（绕过大陆）| "global"
	TunEnabled     bool   `json:"tun_enabled"`     // TUN 入站（需要 root/CAP_NET_ADMIN）
	AutoRedirect   bool   `json:"auto_redirect"`   // Linux nftables 加速（默认关，兼容性优先）
	MixedEnabled   bool   `json:"mixed_enabled"`   // 本地 mixed(socks/http) 入站
	MixedPort      int    `json:"mixed_port"`      // 默认 2080
	ClashListen    string `json:"clash_listen"`    // clash_api external_controller
	ClashSecret    string `json:"clash_secret"`    // 首次生成随机值
	ExternalUI     string `json:"external_ui"`     // metacubexd | zashboard | yacd
	DashboardOff   bool   `json:"dashboard_off"`   // 关闭网页面板（clash_api 仍监听，TUI 切换节点用）
	MirrorPrefix   string `json:"mirror_prefix"`   // GitHub 镜像前缀（UI/内核下载用），如 https://ghproxy.net/
	FakeIP         bool   `json:"fakeip"`          // FakeIP DNS
	DownloadDetour string `json:"download_detour"` // 规则集/UI 下载出站: direct | PROXY
	DNSCN          string `json:"dns_cn"`          // 国内直连 DNS（udp）
	DNSProxy       string `json:"dns_proxy"`       // 代理侧 DNS（https）
	SingboxPath    string `json:"singbox_path"`    // 手动指定 sing-box 路径（可空）
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
		AutoRedirect:   false,
		MixedEnabled:   true,
		MixedPort:      2080,
		ClashListen:    "127.0.0.1:9090",
		ClashSecret:    randomSecret(),
		ExternalUI:     "metacubexd",
		FakeIP:         true,
		DownloadDetour: "direct",
		DNSCN:          "223.5.5.5",
		DNSProxy:       "8.8.8.8",
	}
}

func randomSecret() string {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		return "ssb-secret"
	}
	return hex.EncodeToString(b)
}

// Dirs resolves every path ssb touches, all under one base directory.
type Dirs struct{ Base string }

func (d Dirs) StateFile() string  { return filepath.Join(d.Base, "data", "state.json") }
func (d Dirs) ConfigFile() string { return filepath.Join(d.Base, "data", "config.json") }
func (d Dirs) BackupFile() string { return filepath.Join(d.Base, "data", "config.json.bak") }
func (d Dirs) SingboxBin() string { return filepath.Join(d.Base, "data", "sing-box") }
func (d Dirs) DataDir() string    { return filepath.Join(d.Base, "data") }
func (d Dirs) UIDir() string      { return filepath.Join(d.Base, "data", "ui") }
func (d Dirs) CacheDB() string    { return filepath.Join(d.Base, "data", "cache.db") }
func (d Dirs) LogDir() string     { return filepath.Join(d.Base, "logs") }
func (d Dirs) LogFile() string    { return filepath.Join(d.Base, "logs", "sing-box.log") }
func (d Dirs) RunDir() string     { return filepath.Join(d.Base, "run") }
func (d Dirs) PidFile() string    { return filepath.Join(d.Base, "run", "sing-box.pid") }

func (d Dirs) Ensure() error {
	for _, p := range []string{d.DataDir(), d.UIDir(), d.LogDir(), d.RunDir()} {
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
	if st.Settings.ClashListen == "" {
		st.Settings.ClashListen = "127.0.0.1:9090"
	}
	if st.Settings.ClashSecret == "" {
		st.Settings.ClashSecret = randomSecret()
	}
	if st.Settings.ExternalUI == "" {
		st.Settings.ExternalUI = "metacubexd"
	}
	if st.Settings.RouteMode == "" {
		st.Settings.RouteMode = "rule"
	}
	if st.Settings.DownloadDetour == "" {
		st.Settings.DownloadDetour = "direct"
	}
	if st.Settings.DNSCN == "" {
		st.Settings.DNSCN = "223.5.5.5"
	}
	if st.Settings.DNSProxy == "" {
		st.Settings.DNSProxy = "8.8.8.8"
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
