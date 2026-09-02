// Package app is the glue layer shared by the CLI and the TUI:
// state loading, config generation with check+rollback, and service control.
package app

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"ssb/internal/link"
	"ssb/internal/profile"
	"ssb/internal/render"
	"ssb/internal/sbx"
	"ssb/internal/sub"
)

// App carries everything a command needs.
type App struct {
	Dirs  profile.Dirs
	State *profile.State
}

// ResolveBase picks the working directory: --dir flag > SSB_DIR env >
// 可执行文件所在目录（部署形态：所有东西跟二进制放一起）。
func ResolveBase(flagDir string) string {
	if flagDir != "" {
		return flagDir
	}
	if env := os.Getenv("SSB_DIR"); env != "" {
		return env
	}
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		// go run 的临时目录没有意义，退回 cwd
		if !strings.Contains(dir, "go-build") {
			return dir
		}
	}
	wd, _ := os.Getwd()
	return wd
}

// Open loads (or initializes) state under base.
func Open(base string) (*App, error) {
	d := profile.Dirs{Base: base}
	st, err := profile.Load(d)
	if err != nil {
		return nil, err
	}
	return &App{Dirs: d, State: st}, nil
}

func (a *App) Save() error { return a.State.Save(a.Dirs) }

// Generate renders config.json. If sing-box is available the new config is
// validated first and the previous one kept as config.json.bak; a failed
// check leaves the old config untouched.
func (a *App) Generate() (warn string, err error) {
	b, err := render.Build(a.State, a.Dirs)
	if err != nil {
		return "", err
	}
	if err := a.Dirs.Ensure(); err != nil {
		return "", err
	}
	tmp := a.Dirs.ConfigFile() + ".new"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return "", err
	}
	bin, lerr := sbx.Locate(a.Dirs, &a.State.Settings)
	if lerr == nil {
		cwarn, cerr := sbx.Check(bin, tmp)
		if cerr != nil {
			os.Remove(tmp)
			return "", cerr
		}
		// check 退出码为 0 但仍可能打印弃用告警，直接透出，别让它烂在配置里
		if cwarn != "" {
			warn = "sing-box 对本配置有告警：\n" + cwarn
		}
	} else {
		warn = "未找到 sing-box，本次跳过配置校验（" + lerr.Error() + "）"
	}
	if _, err := os.Stat(a.Dirs.ConfigFile()); err == nil {
		_ = os.Rename(a.Dirs.ConfigFile(), a.Dirs.BackupFile())
	}
	if err := os.Rename(tmp, a.Dirs.ConfigFile()); err != nil {
		return warn, err
	}
	return warn, nil
}

// AddNodes parses free-form text (one or many links, or a base64 blob) into
// manual nodes.
func (a *App) AddNodes(text string) (int, []error) {
	nodes, errs := link.ParseMixed(text)
	if len(nodes) == 0 {
		if len(errs) == 0 {
			errs = append(errs, fmt.Errorf("没有发现可解析的链接"))
		}
		return 0, errs
	}
	a.State.Manual = append(a.State.Manual, nodes...)
	if err := a.Save(); err != nil {
		return 0, append(errs, err)
	}
	return len(nodes), errs
}

// RemoveManual deletes a manual node by tag.
func (a *App) RemoveManual(tag string) error {
	for i, n := range a.State.Manual {
		if n.Tag == tag {
			a.State.Manual = append(a.State.Manual[:i], a.State.Manual[i+1:]...)
			return a.Save()
		}
	}
	return fmt.Errorf("手动节点 %q 不存在", tag)
}

// SubAdd registers a subscription and fetches it immediately.
func (a *App) SubAdd(ctx context.Context, name, url string) (*profile.Subscription, error) {
	if name == "" {
		name = fmt.Sprintf("sub-%d", len(a.State.Subscriptions)+1)
	}
	if a.State.FindSub(name) != nil {
		return nil, fmt.Errorf("订阅 %q 已存在", name)
	}
	s := &profile.Subscription{Name: name, URL: url}
	a.State.Subscriptions = append(a.State.Subscriptions, s)
	if err := a.refreshSub(ctx, s); err != nil {
		// 保留条目（URL 可能临时故障），但报告错误
		_ = a.Save()
		return s, err
	}
	return s, a.Save()
}

// SubUpdate refreshes one subscription (by name) or all (name == "").
func (a *App) SubUpdate(ctx context.Context, name string) ([]string, error) {
	var targets []*profile.Subscription
	if name == "" {
		targets = a.State.Subscriptions
	} else if s := a.State.FindSub(name); s != nil {
		targets = []*profile.Subscription{s}
	} else {
		return nil, fmt.Errorf("订阅 %q 不存在", name)
	}
	if len(targets) == 0 {
		return nil, fmt.Errorf("还没有添加任何订阅")
	}
	var report []string
	var firstErr error
	for _, s := range targets {
		if err := a.refreshSub(ctx, s); err != nil {
			report = append(report, fmt.Sprintf("✗ %s: %v", s.Name, err))
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		report = append(report, fmt.Sprintf("✓ %s: %d 个节点 (%s)", s.Name, len(s.Nodes), s.Format))
	}
	if err := a.Save(); err != nil {
		return report, err
	}
	return report, firstErr
}

func (a *App) refreshSub(ctx context.Context, s *profile.Subscription) error {
	res, err := sub.Fetch(ctx, s.URL)
	if err != nil {
		return err
	}
	s.Nodes = res.Nodes
	s.Format = res.Format
	s.Userinfo = res.Userinfo
	s.UpdatedAt = time.Now()
	return nil
}

// SubRemove drops a subscription.
func (a *App) SubRemove(name string) error {
	for i, s := range a.State.Subscriptions {
		if s.Name == name {
			a.State.Subscriptions = append(a.State.Subscriptions[:i], a.State.Subscriptions[i+1:]...)
			return a.Save()
		}
	}
	return fmt.Errorf("订阅 %q 不存在", name)
}

// EnsureBinary locates sing-box, downloading it into data/ when absent.
func (a *App) EnsureBinary(ctx context.Context) (string, error) {
	if bin, err := sbx.Locate(a.Dirs, &a.State.Settings); err == nil {
		return bin, nil
	}
	ver, err := sbx.Download(ctx, a.Dirs, a.State.Settings.MirrorPrefix)
	if err != nil {
		return "", fmt.Errorf("自动下载 sing-box 失败: %w（可自行下载后复制到 %s，或在设置里配置镜像前缀）", err, a.Dirs.SingboxBin())
	}
	fmt.Fprintf(os.Stderr, "已下载 sing-box %s → %s\n", ver, a.Dirs.SingboxBin())
	return a.Dirs.SingboxBin(), nil
}

// Install makes sure data/sing-box exists（发布形态：内核跟工具放一起），
// 即使 PATH 里已有 sing-box 也会下载一份到 data/。
func (a *App) Install(ctx context.Context) (string, error) {
	if _, err := os.Stat(a.Dirs.SingboxBin()); err == nil {
		return fmt.Sprintf("data/sing-box 已存在（如需更新请删除后重新下载）: %s", a.Dirs.SingboxBin()), nil
	}
	ver, err := sbx.Download(ctx, a.Dirs, a.State.Settings.MirrorPrefix)
	if err != nil {
		return "", fmt.Errorf("下载 sing-box 失败: %w（可自行下载后复制到 %s，或在设置里配置镜像前缀）", err, a.Dirs.SingboxBin())
	}
	return fmt.Sprintf("已下载 sing-box %s → %s", ver, a.Dirs.SingboxBin()), nil
}

// Start generates (with check) and launches sing-box.
func (a *App) Start(ctx context.Context) error {
	bin, err := a.EnsureBinary(ctx)
	if err != nil {
		return err
	}
	if warn, err := a.Generate(); err != nil {
		return err
	} else if warn != "" {
		fmt.Fprintln(os.Stderr, "警告: "+warn)
	}
	if err := a.precheckTunPerms(bin); err != nil {
		return err
	}
	return sbx.Start(bin, a.Dirs)
}

// precheckTunPerms fails fast with actionable advice instead of a cryptic
// permission error in the log.
func (a *App) precheckTunPerms(bin string) error {
	if !a.State.Settings.TunEnabled || os.Geteuid() == 0 {
		return nil
	}
	if out, err := exec.Command("getcap", bin).Output(); err == nil &&
		strings.Contains(string(out), "cap_net_admin") {
		return nil
	}
	return fmt.Errorf("已启用 TUN 但当前不是 root。二选一：\n"+
		"  sudo ./ssb start\n"+
		"  sudo setcap cap_net_admin+ep %s   # 一次性授权，只改本目录内文件\n"+
		"（或在设置中关闭 TUN，仅用本地 mixed 端口）", bin)
}

func (a *App) Stop() error { return sbx.Stop(a.Dirs) }
func (a *App) Restart(ctx context.Context) error {
	if _, ok := sbx.Running(a.Dirs); ok {
		if err := a.Stop(); err != nil {
			return err
		}
	}
	return a.Start(ctx)
}

// SelectNode switches the PROXY selector at runtime via clash_api.
// tag 可以是节点名或 "auto"（自动测速）。选择会由 cache_file 持久化。
func (a *App) SelectNode(tag string) error {
	if _, ok := sbx.Running(a.Dirs); !ok {
		return fmt.Errorf("sing-box 未运行（服务页 s 启动后再切换）")
	}
	s := a.State.Settings
	if err := sbx.SelectProxy(s.ClashListen, s.ClashSecret, tag); err != nil {
		return fmt.Errorf("切换失败: %w（若刚增删过节点，先 g 生成、r 重启）", err)
	}
	return nil
}

// SelectedNode returns the PROXY selector's current choice, "" when
// sing-box isn't running or clash_api is unreachable.
func (a *App) SelectedNode() string {
	s := a.State.Settings
	now, err := sbx.SelectedProxy(s.ClashListen, s.ClashSecret)
	if err != nil {
		return ""
	}
	return now
}

// RunCore generates the config then replaces this process with sing-box in
// the foreground（Docker/调试用：信号直达内核进程）。
func (a *App) RunCore(ctx context.Context) error {
	bin, err := a.EnsureBinary(ctx)
	if err != nil {
		return err
	}
	if warn, err := a.Generate(); err != nil {
		return err
	} else if warn != "" {
		fmt.Fprintln(os.Stderr, "警告: "+warn)
	}
	if err := a.precheckTunPerms(bin); err != nil {
		return err
	}
	return syscall.Exec(bin, []string{bin, "run", "-c", a.Dirs.ConfigFile()}, os.Environ())
}

// StatusText renders a short human status block.
func (a *App) StatusText() string {
	var b strings.Builder
	if pid, ok := sbx.Running(a.Dirs); ok {
		fmt.Fprintf(&b, "sing-box: 运行中 (pid %d)\n", pid)
	} else {
		b.WriteString("sing-box: 未运行\n")
	}
	if bin, err := sbx.Locate(a.Dirs, &a.State.Settings); err == nil {
		if v, err := sbx.Version(bin); err == nil {
			fmt.Fprintf(&b, "二进制: %s (%s)\n", bin, v)
		}
	} else {
		b.WriteString("二进制: 未找到\n")
	}
	alive := sbx.APIAlive(a.State.Settings.ClashListen, a.State.Settings.ClashSecret)
	fmt.Fprintf(&b, "clash_api: %s\n", map[bool]string{true: "可达", false: "不可达"}[alive])
	if alive {
		if now := a.SelectedNode(); now != "" {
			fmt.Fprintf(&b, "当前出口: %s\n", now)
		}
	}
	fmt.Fprintf(&b, "节点数: %d（手动 %d + 订阅 %d 个源）\n",
		len(a.State.AllNodes()), len(a.State.Manual), len(a.State.Subscriptions))
	if a.State.Settings.DashboardOff {
		b.WriteString("Dashboard: 已关闭（节点页可直接切换节点）\n")
	} else {
		fmt.Fprintf(&b, "Dashboard: %s\n", a.DashboardURL())
	}
	return b.String()
}

// DashboardURL builds the external_ui URL served by sing-box's clash_api.
func (a *App) DashboardURL() string {
	listen := a.State.Settings.ClashListen
	host, port, err := net.SplitHostPort(listen)
	if err != nil {
		return "http://" + listen + "/ui/"
	}
	if host == "0.0.0.0" || host == "::" || host == "" {
		host = lanIP()
	}
	return fmt.Sprintf("http://%s/ui/?secret=%s", net.JoinHostPort(host, port), a.State.Settings.ClashSecret)
}

func lanIP() string {
	conn, err := net.Dial("udp", "223.5.5.5:53")
	if err != nil {
		return "127.0.0.1"
	}
	defer conn.Close()
	return conn.LocalAddr().(*net.UDPAddr).IP.String()
}
