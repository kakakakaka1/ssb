//go:build linux

package sbx

// Linux 平台实现。sbx_windows.go 必须提供同一组符号：
//
//	detachedProcAttr        子进程脱离本进程（setsid / 独立进程组）的 SysProcAttr
//	processAlive            pid 是否还活着
//	processLooksLikeSingbox pid 的命令行/映像是否是 sing-box（防 pid 复用）
//	terminateProcess        优雅终止（SIGTERM / TerminateProcess）
//	killProcess             强杀
//	RunForeground           前台运行 sing-box 直到退出（run-core）
//	IsElevated              是否 root / 管理员
//	HasTunPrivilege         能否起 TUN（root/管理员，或 Linux 上二进制带 CAP_NET_ADMIN）
//	TunPrivilegeHint        没权限时的中文建议
//	platformStartHint       启动失败日志里平台特有的错误 → 建议
//	ipv6Status              本机 IPv6 能力（problem 非空 = 不能用 v6）
//	platformChecks          doctor 里的平台项

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"

	"ssb/internal/profile"
	"ssb/internal/render"
)

func detachedProcAttr() *syscall.SysProcAttr { return &syscall.SysProcAttr{Setsid: true} }

func processAlive(pid int) bool { return syscall.Kill(pid, 0) == nil }

// processLooksLikeSingbox 读 /proc/<pid>/cmdline；读不到（hidepid 等）时放行。
func processLooksLikeSingbox(pid int) bool {
	cl, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
	if err != nil {
		return true
	}
	return bytes.Contains(cl, []byte("sing-box"))
}

func terminateProcess(pid int) error { return syscall.Kill(pid, syscall.SIGTERM) }

func killProcess(pid int) { _ = syscall.Kill(pid, syscall.SIGKILL) }

// RunForeground replaces this process with sing-box（Docker/调试用：信号直达内核进程）。
func RunForeground(bin string, args ...string) error {
	return syscall.Exec(bin, append([]string{bin}, args...), os.Environ())
}

// IsElevated reports whether we run as root.
func IsElevated() bool { return os.Geteuid() == 0 }

// hasNetAdminCap checks whether the binary was granted CAP_NET_ADMIN via setcap.
func hasNetAdminCap(bin string) bool {
	if bin == "" {
		return false
	}
	out, err := exec.Command("getcap", bin).Output()
	return err == nil && strings.Contains(string(out), "cap_net_admin")
}

// HasTunPrivilege: TUN + auto_route 需要 CAP_NET_ADMIN——root，或给二进制 setcap 过。
func HasTunPrivilege(bin string) bool { return IsElevated() || hasNetAdminCap(bin) }

// TunPrivilegeHint is the actionable advice shown when HasTunPrivilege is false.
func TunPrivilegeHint(bin string) string {
	return fmt.Sprintf("已启用 TUN 但当前不是 root。二选一：\n"+
		"  sudo %s start\n"+
		"  sudo setcap cap_net_admin+ep %s   # 一次性授权，只改本目录内文件\n"+
		"（或在设置中关闭 TUN，仅用本地 mixed 端口）", Self, bin)
}

func platformStartHint(logTail string) string {
	switch {
	case strings.Contains(logTail, "address family not supported by protocol"):
		return fmt.Sprintf("\n提示：内核不支持 IPv6 策略路由（启动参数 ipv6.disable=1，或内核缺 CONFIG_IPV6 / CONFIG_IPV6_MULTIPLE_TABLES；网卡上有 IPv6 地址也可能缺后者）。\n"+
			"     设置为 auto 时请重新 %s gen（新版会通过 netlink 探测并自动降级为纯 IPv4）；设置为 on 请改成 off 或 auto 再 %s gen。", Self, Self)
	case strings.Contains(logTail, "operation not permitted"), strings.Contains(logTail, "permission denied"):
		return fmt.Sprintf("\n提示：TUN 需要 root 或 CAP_NET_ADMIN，用 sudo %s start，或按 %s doctor 的提示 setcap。", Self, Self)
	}
	return ""
}

// ipv6Status：内核能否承载 IPv6 策略路由，探测细节见 render/ipv6_linux.go。
func ipv6Status() (problem, okDetail string) {
	_, stackErr := os.Stat("/proc/net/if_inet6")
	hasStack := stackErr == nil
	if b, err := os.ReadFile("/proc/sys/net/ipv6/conf/all/disable_ipv6"); err == nil && strings.TrimSpace(string(b)) == "1" {
		hasStack = false
	}
	rules := render.IPv6RuleSupported()
	switch {
	case !hasStack:
		return "内核没有 IPv6（无 /proc/net/if_inet6 或 disable_ipv6=1）", ""
	case rules == 0:
		return "内核有 IPv6 地址但不支持 IPv6 策略路由（缺 CONFIG_IPV6_MULTIPLE_TABLES）", ""
	case rules < 0:
		return "netlink 探测失败，无法确认 IPv6 策略路由支持情况", ""
	}
	return "", "内核支持 IPv6 策略路由"
}

func platformChecks(st *profile.State, bin string) []CheckResult {
	var out []CheckResult
	add := func(name string, ok bool, detail string) {
		out = append(out, CheckResult{name, ok, detail})
	}

	if _, err := os.Stat("/dev/net/tun"); err == nil {
		add("/dev/net/tun", true, "存在")
	} else {
		add("/dev/net/tun", false, "不存在——TUN 模式不可用（容器内需映射该设备）")
	}

	if st.Settings.TunEnabled {
		switch {
		case IsElevated():
			add("TUN 权限", true, "当前是 root")
		case hasNetAdminCap(bin):
			add("TUN 权限", true, "已 setcap cap_net_admin")
		default:
			add("TUN 权限", false,
				fmt.Sprintf("非 root 且二进制无 CAP_NET_ADMIN。二选一：sudo %s start；或一次性授权（只改本目录文件）: sudo setcap cap_net_admin+ep %s", Self, bin))
		}
		name, ok, detail := ipv6Check(st.Settings.IPv6)
		add(name, ok, detail)
	}

	if b, err := os.ReadFile("/etc/resolv.conf"); err == nil && bytes.Contains(b, []byte("127.0.0.53")) {
		add("systemd-resolved", true,
			"检测到 127.0.0.53 存根。TUN+hijack-dns 通常可正常工作；若遇解析异常，可手动设置 DNSStubListener=no（见 README，工具不会代改系统文件）")
	}
	return out
}
