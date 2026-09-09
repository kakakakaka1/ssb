// Package sbx locates / downloads / drives the sing-box binary: config check,
// start/stop as a detached child with pidfile, API 服务探测与节点切换, doctor.
//
// 进程、权限、体检里跟操作系统绑定的部分放在 sbx_linux.go / sbx_windows.go，
// 两个文件各自提供同一组函数（清单见 sbx_linux.go 顶部）；本文件只写跨平台逻辑。
package sbx

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"ssb/internal/profile"
)

// Self 是提示文案里指代本程序的写法：Linux 用 ./ssb，Windows（PowerShell）用 .\ssb。
var Self = selfName(runtime.GOOS)

func selfName(goos string) string {
	if goos == "windows" {
		return `.\ssb`
	}
	return "./ssb"
}

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
	return "", fmt.Errorf("未找到 sing-box（TUI 服务页按 i 或运行 %s install 自动下载；也可自行下载后复制到 data/%s）",
		Self, filepath.Base(d.SingboxBin()))
}

// Version runs `sing-box version` and returns the first line.
func Version(bin string) (string, error) {
	out, err := exec.Command(bin, "version").CombinedOutput()
	if err != nil {
		return "", err
	}
	line, _, _ := strings.Cut(strings.TrimSpace(string(out)), "\n")
	return strings.TrimRight(line, "\r"), nil
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

// ---- download ----

// Download fetches the latest stable sing-box release for this OS/arch into
// data/sing-box（Windows: data/sing-box.exe）。mirror（可空）会被拼在 GitHub 下载地址前面。
func Download(ctx context.Context, d profile.Dirs, mirror string) (string, error) {
	if err := d.Ensure(); err != nil {
		return "", err
	}
	client := &http.Client{Timeout: 10 * time.Minute} // Windows 包 30MB 以上，别让慢网络撞超时

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
	suffix := assetSuffix(runtime.GOOS, runtime.GOARCH)
	assetURL := ""
	for _, a := range rel.Assets {
		if strings.HasSuffix(a.Name, suffix) {
			assetURL = a.BrowserDownloadURL
			break
		}
	}
	if assetURL == "" {
		return "", fmt.Errorf("release %s 中未找到 *%s 资产", rel.TagName, suffix)
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

	if strings.HasSuffix(suffix, ".zip") {
		err = extractZip(d, resp2.Body)
	} else {
		err = extractTarGz(d, resp2.Body)
	}
	if err != nil {
		return "", err
	}
	return rel.TagName, nil
}

// assetSuffix 精确到官方资产名 sing-box-<ver>-<os>-<arch>.<ext> 的尾部。
// 用 Contains 会误选 linux-amd64-glibc / -musl、windows-amd64-legacy-windows-7 这些变体。
func assetSuffix(goos, goarch string) string {
	ext := ".tar.gz"
	if goos == "windows" {
		ext = ".zip"
	}
	return "-" + goos + "-" + goarch + ext
}

// extractTarGz：Linux 包是 tar.gz，流式读到 sing-box 就写盘、不再往下读。
func extractTarGz(d profile.Dirs, body io.Reader) error {
	gz, err := gzip.NewReader(body)
	if err != nil {
		return err
	}
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return fmt.Errorf("压缩包里没有 sing-box 可执行文件")
		}
		if err != nil {
			return err
		}
		if hdr.Typeflag == tar.TypeReg && path.Base(hdr.Name) == "sing-box" {
			return writeAtomic(d.SingboxBin(), tr, 0o755)
		}
	}
}

// extractZip：Windows 包是 zip（要随机访问，先落到临时文件）。除 sing-box.exe 外把
// 同包的 *.dll 也放进 data/——1.14 的官方包附带 libcronet.dll（按需加载，本工具不用
// cronet，带上只是为了跟官方包一致）；wintun 已内嵌在 exe 里，不需要单独的 wintun.dll。
func extractZip(d profile.Dirs, body io.Reader) error {
	tmp, err := os.CreateTemp(d.DataDir(), "sing-box-*.zip")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	_, err = io.Copy(tmp, body)
	tmp.Close()
	if err != nil {
		return err
	}
	zr, err := zip.OpenReader(tmp.Name())
	if err != nil {
		return err
	}
	defer zr.Close() // 先于上面的 Remove 执行：Windows 上文件开着删不掉
	found := false
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		base := path.Base(f.Name)
		var dst string
		switch {
		case base == "sing-box.exe":
			dst = d.SingboxBin()
			found = true
		case strings.HasSuffix(strings.ToLower(base), ".dll"):
			dst = filepath.Join(d.DataDir(), base)
		default:
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		err = writeAtomic(dst, rc, 0o755)
		rc.Close()
		if err != nil {
			return err
		}
	}
	if !found {
		return fmt.Errorf("压缩包里没有 sing-box.exe")
	}
	return nil
}

// writeAtomic 先写 dst.tmp 再改名，下载中断不会留下半截可执行文件。
func writeAtomic(dst string, r io.Reader, mode os.FileMode) error {
	tmp := dst + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, r); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dst)
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
	if !processAlive(pid) || !processLooksLikeSingbox(pid) { // 后者防 pid 复用
		return 0, false
	}
	return pid, true
}

// Start launches sing-box detached from this process（Linux setsid，Windows
// 无控制台的独立进程组），logging to logs/sing-box.log.
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
	cmd.SysProcAttr = detachedProcAttr()
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
	if !processAlive(pid) {
		os.Remove(d.PidFile())
		tail := tailFile(d.LogFile(), 15)
		return fmt.Errorf("sing-box 启动即退出，日志尾部：\n%s%s", tail, startHint(tail))
	}
	return nil
}

// startHint turns 常见的内核/权限报错 into 一句可执行的中文建议，附在启动失败信息后面。
// 平台特有的（Linux 的 IPv6 策略路由 / CAP_NET_ADMIN，Windows 的管理员权限）在 platformStartHint。
func startHint(logTail string) string {
	if h := platformStartHint(logTail); h != "" {
		return h
	}
	if strings.Contains(logTail, "address already in use") ||
		strings.Contains(logTail, "Only one usage of each socket address") { // Windows 的措辞
		return fmt.Sprintf("\n提示：端口被占用，换 mixed / API 端口或停掉占用者（%s doctor 会指出是谁）。", Self)
	}
	return ""
}

// Stop asks sing-box to exit（Linux 发 SIGTERM，Windows 只能 TerminateProcess），
// 5 秒没退干净就强杀。
func Stop(d profile.Dirs) error {
	pid, ok := Running(d)
	if !ok {
		os.Remove(d.PidFile())
		return fmt.Errorf("sing-box 未在运行")
	}
	if err := terminateProcess(pid); err != nil {
		return err
	}
	for i := 0; i < 50; i++ {
		time.Sleep(100 * time.Millisecond)
		if !processAlive(pid) {
			os.Remove(d.PidFile())
			return nil
		}
	}
	killProcess(pid)
	os.Remove(d.PidFile())
	return nil
}

// ---- API 服务客户端（状态探测 + TUI 节点切换）----
//
// sing-box 1.14 的官方 API 是 gRPC；ssb 不引入 gRPC 依赖，节点切换/查询直接复用内核
// 自带的 `sing-box api` 命令行（--url/--secret），存活探测用一次普通 HTTP 请求。

// APIAlive reports whether the API service answers on listen. 它同时承载 gRPC-Web 和
// 面板，任何 HTTP 响应（哪怕 404 / 重定向）都说明服务在；只有连不上才算不可达。
func APIAlive(listen string) bool {
	client := &http.Client{
		Timeout:       2 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	resp, err := client.Get("http://" + listen + "/")
	if err != nil {
		return false
	}
	resp.Body.Close()
	return true
}

// apiOutput runs `sing-box api <args>` against the local API service and returns its stdout.
func apiOutput(bin, listen, secret string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	full := append([]string{"api", "--url", "http://" + listen, "--secret", secret}, args...)
	cmd := exec.CommandContext(ctx, bin, full...)
	var stdout, stderr strings.Builder
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stripANSI(stderr.String()))
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("sing-box api %s: %s", strings.Join(args, " "), msg)
	}
	return stdout.String(), nil
}

// SelectedProxy returns the PROXY selector's current choice（如 "auto" 或节点名）.
func SelectedProxy(bin, listen, secret string) (string, error) {
	out, err := apiOutput(bin, listen, secret, "group", "show", "PROXY")
	if err != nil {
		return "", err
	}
	if now := parseSelected(out); now != "" {
		return now, nil
	}
	return "", fmt.Errorf("sing-box api group show 输出里没有 Selected 行")
}

// parseSelected 从 `sing-box api group show` 的块输出（"标签:   值" 每行一项）里取 Selected。
func parseSelected(out string) string {
	for _, ln := range strings.Split(out, "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(stripANSI(ln)), "Selected:"); ok {
			v = strings.TrimSpace(v)
			if v == "-" {
				return ""
			}
			return v
		}
	}
	return ""
}

// SelectProxy switches the PROXY selector to name.
func SelectProxy(bin, listen, secret, name string) error {
	_, err := apiOutput(bin, listen, secret, "group", "select", "PROXY", name)
	return err
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
// system state. 平台相关项（TUN 设备/权限、IPv6、系统 DNS）来自 platformChecks。
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

	out = append(out, platformChecks(st, bin)...)

	for _, p := range []struct{ name, addr string }{
		{"API 端口", st.Settings.APIListen},
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

	if _, err := os.Stat(d.ConfigFile()); err == nil && bin != "" {
		if warn, err := Check(bin, d.ConfigFile()); err != nil {
			add("config.json", false, err.Error())
		} else if warn != "" {
			add("config.json", false, fmt.Sprintf("check 通过，但内核有告警（多为新版弃用项，建议 %s gen 重新生成）：\n%s", Self, warn))
		} else {
			add("config.json", true, "check 通过")
		}
	} else {
		add("config.json", false, fmt.Sprintf("尚未生成（添加节点/订阅后自动生成，或运行 %s gen）", Self))
	}
	return out
}

// ipv6Check compares 本机 IPv6 能力（平台的 ipv6Status）与 settings.ipv6。
// Linux 上这是启动报 "add rule N/M: address family not supported by protocol"
// 的唯一来源：sing-box 的 auto_route 要下 AF_INET6 策略路由，内核没有 IPv6
// （或没有 IPv6 策略路由）就会被拒绝。
func ipv6Check(mode string) (name string, ok bool, detail string) {
	name = "IPv6"
	if mode == "" {
		mode = "auto"
	}
	problem, okDetail := ipv6Status()
	switch {
	case mode == "off":
		return name, true, "设置为 off：TUN 只配 IPv4、不开 strict_route"
	case problem != "" && mode == "on":
		return name, false, problem + "，但设置里 IPv6=on —— TUN 会启动失败，请改回 auto 或 off"
	case problem != "":
		return name, true, problem + "，auto 已自动降级为纯 IPv4"
	default:
		return name, true, okDetail + "（当前设置 " + mode + "）"
	}
}
