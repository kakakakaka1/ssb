//go:build windows

package sbx

// Windows 平台实现，符号清单见 sbx_linux.go 顶部。
//
// 要点：sing-box 以"无控制台 + 独立进程组"的方式启动，这样关掉终端窗口它也不会跟着死；
// 代价是没法给它投递 Ctrl+C，停止只能 TerminateProcess。wintun 网卡、auto_route 的
// 路由、strict_route 的 WFP 规则都挂在进程生命周期上，进程一退系统自动收回，硬杀是安全的。

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/sys/windows"

	"ssb/internal/profile"
	"ssb/internal/render"
)

// stillActive 是 GetExitCodeProcess 对"还没退出"的返回值（STATUS_PENDING）。
const stillActive = 259

func detachedProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_PROCESS_GROUP | windows.DETACHED_PROCESS}
}

func openProcess(pid int) (windows.Handle, error) {
	return windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
}

// processAlive：OpenProcess 成功还不够——句柄被别人（比如本进程里 Start 留下的
// cmd.Wait）攥着时进程对象还在，要看退出码是否仍为 STILL_ACTIVE。
func processAlive(pid int) bool {
	h, err := openProcess(pid)
	if err != nil {
		// 拒绝访问说明进程存在（PROCESS_QUERY_LIMITED_INFORMATION 不受 UAC 完整性级别限制，
		// 一般只有别的用户的进程才会拒绝）；不存在的 pid 报 ERROR_INVALID_PARAMETER。
		return errors.Is(err, windows.ERROR_ACCESS_DENIED)
	}
	defer windows.CloseHandle(h)
	var code uint32
	if err := windows.GetExitCodeProcess(h, &code); err != nil {
		return false
	}
	return code == stillActive
}

// processLooksLikeSingbox 用映像文件名防 pid 复用（重启后 pidfile 里的 pid 可能落到
// 任何进程头上）。打不开时放行；打开了却查不到映像名的是 System/Idle 一类，肯定不是。
func processLooksLikeSingbox(pid int) bool {
	h, err := openProcess(pid)
	if err != nil {
		return true
	}
	defer windows.CloseHandle(h)
	buf := make([]uint16, windows.MAX_LONG_PATH)
	n := uint32(len(buf))
	if err := windows.QueryFullProcessImageName(h, 0, &buf[0], &n); err != nil {
		return false
	}
	name := strings.ToLower(filepath.Base(windows.UTF16ToString(buf[:n])))
	return strings.Contains(name, "sing-box")
}

func terminateProcess(pid int) error {
	h, err := windows.OpenProcess(windows.PROCESS_TERMINATE, false, uint32(pid))
	if err != nil {
		return fmt.Errorf("终止 sing-box 失败: %w（若它是以管理员身份启动的，请在管理员终端里执行）", err)
	}
	defer windows.CloseHandle(h)
	return windows.TerminateProcess(h, 1)
}

func killProcess(pid int) { _ = terminateProcess(pid) }

// RunForeground runs sing-box in this console until it exits. Windows 没有 exec，
// 改为子进程共享本控制台：Ctrl+C 会同时送到两个进程，ssb 忽略它、只等 sing-box
// 自己优雅退出，退出状态原样带回。
func RunForeground(bin string, args ...string) error {
	cmd := exec.Command(bin, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	signal.Ignore(os.Interrupt)
	return cmd.Run()
}

// IsElevated reports whether the process token is elevated（UAC 管理员）.
func IsElevated() bool { return windows.GetCurrentProcessToken().IsElevated() }

// HasTunPrivilege: 创建 wintun 网卡需要管理员，没有 setcap 之类的一次性授权。
func HasTunPrivilege(string) bool { return IsElevated() }

// TunPrivilegeHint is the actionable advice shown when HasTunPrivilege is false.
func TunPrivilegeHint(string) string {
	return "已启用 TUN 但当前没有管理员权限（创建 wintun 网卡需要管理员）。\n" +
		"  右键 ssb.exe 或终端 → 「以管理员身份运行」，再启动\n" +
		"（或在设置中关闭 TUN，仅用本地 mixed 端口）"
}

func platformStartHint(logTail string) string {
	l := strings.ToLower(logTail)
	// wintun 的错误由系统按当前语言格式化：英文 "Access is denied."，中文 "拒绝访问。"
	if strings.Contains(l, "access is denied") || strings.Contains(logTail, "拒绝访问") ||
		strings.Contains(l, "requires elevation") {
		return "\n提示：" + TunPrivilegeHint("")
	}
	return ""
}

// ipv6Status：Windows 没有策略路由缺失这类问题，只看 IPv6 栈是否被整体禁用。
func ipv6Status() (problem, okDetail string) {
	if !render.HostHasIPv6() {
		return "系统已禁用 IPv6 栈（注册表 DisabledComponents）", ""
	}
	return "", "系统 IPv6 栈可用"
}

func platformChecks(st *profile.State, _ string) []CheckResult {
	var out []CheckResult
	add := func(name string, ok bool, detail string) {
		out = append(out, CheckResult{name, ok, detail})
	}
	if st.Settings.TunEnabled {
		if IsElevated() {
			add("TUN 权限", true, "当前是管理员")
		} else {
			add("TUN 权限", false, "未以管理员身份运行，无法创建 wintun 网卡。"+
				"右键 ssb.exe 或终端 → 「以管理员身份运行」（或在设置中关闭 TUN）")
		}
		name, ok, detail := ipv6Check(st.Settings.IPv6)
		add(name, ok, detail)
	}
	return out
}
