// Package sbx locates / downloads / drives the sing-box binary: config check,
// start/stop as a detached child with pidfile, clash_api liveness, doctor.
package sbx

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"ssb/internal/profile"
	"ssb/internal/render"
)

// Locate returns the sing-box binary path, trying in order:
// 显式设置 > data/sing-box > PATH。
func Locate(d profile.Dirs, s *profile.Settings) (string, error) {
	if s != nil && s.SingboxPath != "" {
		if _, err := os.Stat(s.SingboxPath); err == nil {
			return s.SingboxPath, nil
		}
		return "", fmt.Errorf("设置里的 sing-box 路径不存在: %s", s.SingboxPath)
	}
	if _, err := os.Stat(d.SingboxBin()); err == nil {
		return d.SingboxBin(), nil
	}
	if p, err := exec.LookPath("sing-box"); err == nil {
		return p, nil
	}
	return "", fmt.Errorf("未找到 sing-box（TUI 服务页按 i 或运行 ./ssb install 自动下载；也可自行下载后复制到 data/sing-box）")
}

// Version runs `sing-box version` and returns the first line.
func Version(bin string) (string, error) {
	out, err := exec.Command(bin, "version").CombinedOutput()
	if err != nil {
		return "", err
	}
	line, _, _ := strings.Cut(strings.TrimSpace(string(out)), "\n")
	return line, nil
}

// Check validates a config file; on failure the returned error carries
// sing-box's own message. 校验通过时返回内核打印的 WARN 行（弃用告警走这里，
// 退出码仍是 0——不回传的话新版弃用会一直悄悄躺在配置里）。
func Check(bin, cfgPath string) (warnings string, err error) {
	out, err := exec.Command(bin, "check", "-c", cfgPath).CombinedOutput()
	msg := strings.TrimSpace(string(out))
	if err != nil {
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("sing-box check 未通过:\n%s", msg)
	}
	return warnLines(msg), nil
}

// warnLines keeps only the WARN lines, stripped of ANSI color codes.
func warnLines(out string) string {
	var keep []string
	for _, ln := range strings.Split(out, "\n") {
		ln = strings.TrimSpace(stripANSI(ln))
		if strings.HasPrefix(ln, "WARN") {
			keep = append(keep, ln)
		}
	}
	return strings.Join(keep, "\n")
}

// stripANSI removes the color escapes sing-box writes when stderr is a pipe.
func stripANSI(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == 0x1b {
			for i < len(s) && s[i] != 'm' {
				i++
			}
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// Download fetches the latest stable sing-box release for this OS/arch into
// data/sing-box. mirror（可空）会被拼在 GitHub 下载地址前面。
func Download(ctx context.Context, d profile.Dirs, mirror string) (string, error) {
	if err := d.Ensure(); err != nil {
		return "", err
	}
	client := &http.Client{Timeout: 120 * time.Second}

	// GitHub API 拿最新版本号与资产列表（API 地址不套镜像）
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet,
		"https://api.github.com/repos/SagerNet/sing-box/releases/latest", nil)
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("查询 GitHub release 失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("查询 GitHub release: HTTP %d", resp.StatusCode)
	}
	var rel struct {
		TagName string `json:"tag_name"`
		Assets  []struct {
			Name               string `json:"name"`
			BrowserDownloadURL string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return "", err
	}
	want := fmt.Sprintf("linux-%s.tar.gz", runtime.GOARCH)
	assetURL := ""
	for _, a := range rel.Assets {
		if strings.Contains(a.Name, "linux-"+runtime.GOARCH) && strings.HasSuffix(a.Name, ".tar.gz") {
			assetURL = a.BrowserDownloadURL
			break
		}
	}
	if assetURL == "" {
		return "", fmt.Errorf("release %s 中未找到 %s 资产", rel.TagName, want)
	}

	req2, _ := http.NewRequestWithContext(ctx, http.MethodGet, mirror+assetURL, nil)
	resp2, err := client.Do(req2)
	if err != nil {
		return "", fmt.Errorf("下载失败: %w", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		return "", fmt.Errorf("下载 %s: HTTP %d", assetURL, resp2.StatusCode)
	}

	gz, err := gzip.NewReader(resp2.Body)
	if err != nil {
		return "", err
	}
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return "", fmt.Errorf("压缩包里没有 sing-box 可执行文件")
		}
		if err != nil {
			return "", err
		}
		if filepath.Base(hdr.Name) == "sing-box" && hdr.Typeflag == tar.TypeReg {
			tmp := d.SingboxBin() + ".tmp"
			f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
			if err != nil {
				return "", err
			}
			if _, err := io.Copy(f, tr); err != nil {
				f.Close()
				return "", err
			}
			f.Close()
			if err := os.Rename(tmp, d.SingboxBin()); err != nil {
				return "", err
			}
			return rel.TagName, nil
		}
	}
}

// ---- process management ----

// Running reports the pid recorded in the pidfile if that process is still a
// live sing-box.
func Running(d profile.Dirs) (int, bool) {
	b, err := os.ReadFile(d.PidFile())
	if err != nil {
		return 0, false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil || pid <= 0 {
		return 0, false
	}
	if err := syscall.Kill(pid, 0); err != nil {
		return 0, false
	}
	// 防 pid 复用：确认 cmdline 里是 sing-box
	if cl, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid)); err == nil {
		if !bytes.Contains(cl, []byte("sing-box")) {
			return 0, false
		}
	}
	return pid, true
}

// Start launches sing-box detached (setsid), logging to logs/sing-box.log.
func Start(bin string, d profile.Dirs) error {
	if pid, ok := Running(d); ok {
		return fmt.Errorf("sing-box 已在运行 (pid %d)", pid)
	}
	if err := d.Ensure(); err != nil {
		return err
	}
	// 日志轮转：超过 8MB 就换名保留一份，防止长期运行占满磁盘
	if fi, err := os.Stat(d.LogFile()); err == nil && fi.Size() > 8<<20 {
		_ = os.Rename(d.LogFile(), d.LogFile()+".1")
	}
	logf, err := os.OpenFile(d.LogFile(), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer logf.Close()

	cmd := exec.Command(bin, "run", "-c", d.ConfigFile())
	cmd.Dir = d.Base
	cmd.Stdout = logf
	cmd.Stderr = logf
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	pid := cmd.Process.Pid
	if err := os.WriteFile(d.PidFile(), []byte(strconv.Itoa(pid)), 0o644); err != nil {
		return err
	}
	go cmd.Wait() // 回收子进程，避免僵尸（ssb 常驻 TUI 时）

	// 稍等确认没有立刻退出（权限不足/端口占用等最常见）
	time.Sleep(1200 * time.Millisecond)
	if err := syscall.Kill(pid, 0); err != nil {
		os.Remove(d.PidFile())
		tail := tailFile(d.LogFile(), 15)
		return fmt.Errorf("sing-box 启动即退出，日志尾部：\n%s%s", tail, startHint(tail))
	}
	return nil
}

// startHint turns 常见的内核/权限报错 into 一句可执行的中文建议，附在启动失败信息后面。
func startHint(logTail string) string {
	switch {
	case strings.Contains(logTail, "address family not supported by protocol"):
		return "\n提示：内核不支持 IPv6 策略路由（启动参数 ipv6.disable=1，或内核缺 CONFIG_IPV6 / CONFIG_IPV6_MULTIPLE_TABLES；网卡上有 IPv6 地址也可能缺后者）。\n" +
			"     设置为 auto 时请重新 ./ssb gen（新版会通过 netlink 探测并自动降级为纯 IPv4）；设置为 on 请改成 off 或 auto 再 ./ssb gen。"
	case strings.Contains(logTail, "operation not permitted"), strings.Contains(logTail, "permission denied"):
		return "\n提示：TUN 需要 root 或 CAP_NET_ADMIN，用 sudo ./ssb start，或按 ./ssb doctor 的提示 setcap。"
	case strings.Contains(logTail, "address already in use"):
		return "\n提示：端口被占用，换 mixed / clash_api 端口或停掉占用者（./ssb doctor 会指出是谁）。"
	}
	return ""
}

// Stop sends SIGTERM (then SIGKILL after 5s) to the recorded pid.
func Stop(d profile.Dirs) error {
	pid, ok := Running(d)
	if !ok {
		os.Remove(d.PidFile())
		return fmt.Errorf("sing-box 未在运行")
	}
	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
		return err
	}
	for i := 0; i < 50; i++ {
		time.Sleep(100 * time.Millisecond)
		if err := syscall.Kill(pid, 0); err != nil {
			os.Remove(d.PidFile())
			return nil
		}
	}
	syscall.Kill(pid, syscall.SIGKILL)
	os.Remove(d.PidFile())
	return nil
}

// ---- clash_api client（状态探测 + TUI 节点切换）----

func apiDo(method, listen, secret, path string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequest(method, "http://"+listen+path, body)
	if err != nil {
		return nil, err
	}
	if secret != "" {
		req.Header.Set("Authorization", "Bearer "+secret)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return (&http.Client{Timeout: 2 * time.Second}).Do(req)
}

// APIAlive probes the clash_api /version endpoint.
func APIAlive(listen, secret string) bool {
	resp, err := apiDo(http.MethodGet, listen, secret, "/version", nil)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// SelectedProxy returns the PROXY selector's current choice（如 "auto" 或节点名）.
func SelectedProxy(listen, secret string) (string, error) {
	resp, err := apiDo(http.MethodGet, listen, secret, "/proxies/PROXY", nil)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("clash_api HTTP %d", resp.StatusCode)
	}
	var v struct {
		Now string `json:"now"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		return "", err
	}
	return v.Now, nil
}

// SelectProxy switches the PROXY selector to name via clash_api.
func SelectProxy(listen, secret, name string) error {
	b, _ := json.Marshal(map[string]string{"name": name})
	resp, err := apiDo(http.MethodPut, listen, secret, "/proxies/PROXY", bytes.NewReader(b))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("clash_api HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(msg)))
	}
	return nil
}

func tailFile(path string, n int) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return "(无法读取日志)"
	}
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// Tail returns the last n log lines (TUI 服务页用).
func Tail(d profile.Dirs, n int) string { return tailFile(d.LogFile(), n) }

// ---- doctor ----

// CheckResult is one doctor finding.
type CheckResult struct {
	Name   string
	OK     bool
	Detail string
}

// Doctor runs environment sanity checks. It only reports — it never mutates
// system state.
func Doctor(d profile.Dirs, st *profile.State) []CheckResult {
	var out []CheckResult
	add := func(name string, ok bool, detail string) {
		out = append(out, CheckResult{name, ok, detail})
	}

	bin, err := Locate(d, &st.Settings)
	if err != nil {
		add("sing-box 二进制", false, err.Error())
	} else if v, verr := Version(bin); verr == nil {
		add("sing-box 二进制", true, bin+" ("+v+")")
	} else {
		add("sing-box 二进制", false, bin+" 无法执行: "+verr.Error())
	}

	if _, err := os.Stat("/dev/net/tun"); err == nil {
		add("/dev/net/tun", true, "存在")
	} else {
		add("/dev/net/tun", false, "不存在——TUN 模式不可用（容器内需映射该设备）")
	}

	if st.Settings.TunEnabled {
		if os.Geteuid() == 0 {
			add("TUN 权限", true, "当前是 root")
		} else {
			cap := ""
			if bin != "" {
				if out2, err := exec.Command("getcap", bin).Output(); err == nil && strings.Contains(string(out2), "cap_net_admin") {
					cap = "已 setcap cap_net_admin"
				}
			}
			if cap != "" {
				add("TUN 权限", true, cap)
			} else {
				add("TUN 权限", false,
					"非 root 且二进制无 CAP_NET_ADMIN。二选一：sudo ./ssb start；或一次性授权（只改本目录文件）: sudo setcap cap_net_admin+ep "+bin)
			}
		}
		name, ok, detail := ipv6Check(st.Settings.IPv6)
		add(name, ok, detail)
	}

	for _, p := range []struct{ name, addr string }{
		{"clash_api 端口", st.Settings.ClashListen},
		{"mixed 端口", net.JoinHostPort("127.0.0.1", strconv.Itoa(st.Settings.MixedPort))},
	} {
		conn, err := net.DialTimeout("tcp", p.addr, 500*time.Millisecond)
		if err != nil {
			add(p.name, true, p.addr+" 空闲")
			continue
		}
		conn.Close()
		if _, ok := Running(d); ok {
			add(p.name, true, p.addr+" 被本工具管理的 sing-box 占用（正常）")
		} else {
			add(p.name, false, p.addr+" 已被其他进程占用，请换端口或停掉占用者")
		}
	}

	if b, err := os.ReadFile("/etc/resolv.conf"); err == nil && bytes.Contains(b, []byte("127.0.0.53")) {
		add("systemd-resolved", true,
			"检测到 127.0.0.53 存根。TUN+hijack-dns 通常可正常工作；若遇解析异常，可手动设置 DNSStubListener=no（见 README，工具不会代改系统文件）")
	}

	if _, err := os.Stat(d.ConfigFile()); err == nil && bin != "" {
		if warn, err := Check(bin, d.ConfigFile()); err != nil {
			add("config.json", false, err.Error())
		} else if warn != "" {
			add("config.json", false, "check 通过，但内核有告警（多为新版弃用项，建议 ./ssb gen 重新生成）：\n"+warn)
		} else {
			add("config.json", true, "check 通过")
		}
	} else {
		add("config.json", false, "尚未生成（添加节点/订阅后自动生成，或运行 ./ssb gen）")
	}
	return out
}

// ipv6Check compares 内核 IPv6 能力 与 settings.ipv6，这是启动报
// "add rule N/M: address family not supported by protocol" 的唯一来源：
// sing-box 的 auto_route 要下 AF_INET6 策略路由，内核没有 IPv6（或没有 IPv6
// 策略路由）就会被拒绝。
func ipv6Check(mode string) (name string, ok bool, detail string) {
	name = "IPv6"
	if mode == "" {
		mode = "auto"
	}
	_, stackErr := os.Stat("/proc/net/if_inet6")
	hasStack := stackErr == nil
	rules := render.IPv6RuleSupported()
	var why string
	switch {
	case !hasStack:
		why = "内核没有 IPv6（无 /proc/net/if_inet6，ipv6.disable=1 或缺 CONFIG_IPV6）"
	case rules == 0:
		why = "内核有 IPv6 地址但不支持 IPv6 策略路由（缺 CONFIG_IPV6_MULTIPLE_TABLES）"
	}
	switch {
	case mode == "off":
		return name, true, "设置为 off：TUN 只配 IPv4、不开 strict_route"
	case why != "" && mode == "on":
		return name, false, why + "，但设置里 IPv6=on —— TUN 会启动失败，请改回 auto 或 off"
	case why != "":
		return name, true, why + "，auto 已自动降级为纯 IPv4"
	case rules < 0:
		return name, true, "netlink 探测失败，按支持 IPv6 处理（当前设置 " + mode + "）；启动报 address family not supported 就改成 off"
	default:
		return name, true, "内核支持 IPv6 策略路由（当前设置 " + mode + "）"
	}
}
