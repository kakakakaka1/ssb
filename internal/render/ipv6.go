package render

import (
	"encoding/binary"
	"errors"
	"os"
	"strings"
	"syscall"
	"unsafe"
)

// HostHasIPv6 reports whether the running kernel can carry IPv6 policy routing,
// which is what sing-box's auto_route actually needs.
//
// 两种内核会让 sing-box 启动时报
//
//		FATAL start service: post-start inbound/tun[tun-in]: starting TUN interface:
//		set rules: add rule 2/16: address family not supported by protocol
//
//	 1. 内核层面关掉 IPv6（启动参数 ipv6.disable=1，或没编译 CONFIG_IPV6，或 disable_ipv6=1）：
//	    /proc/net/if_inet6 不存在或 disable_ipv6 生效。
//	 2. 有 IPv6（网卡上甚至有公网 v6 地址），但内核没编 CONFIG_IPV6_MULTIPLE_TABLES
//	    （不少嵌入式/发行版精简内核如此）：AF_INET6 的 fib rule 操作全部 EAFNOSUPPORT。
//
// 只看 if_inet6 抓不到第 2 种，所以再用 netlink 向内核 dump 一次 AF_INET6 的
// 策略路由表（RTM_GETRULE，无需 root、不依赖 iproute2）：内核不支持时直接回
// EAFNOSUPPORT，跟 sing-tun 下规则时收到的错误一模一样。
//
// 变量而非函数，方便测试替换。
var HostHasIPv6 = func() bool {
	if _, err := os.Stat("/proc/net/if_inet6"); err != nil {
		return false
	}
	if b, err := os.ReadFile("/proc/sys/net/ipv6/conf/all/disable_ipv6"); err == nil {
		if strings.TrimSpace(string(b)) == "1" {
			return false
		}
	}
	return IPv6RuleSupported() == 1
}

// IPv6RuleSupported probes AF_INET6 fib rules via netlink:
// 1 支持 / 0 不支持（EAFNOSUPPORT）/ -1 判断不了（netlink 本身失败）。
func IPv6RuleSupported() int {
	fd, err := syscall.Socket(syscall.AF_NETLINK, syscall.SOCK_RAW|syscall.SOCK_CLOEXEC, syscall.NETLINK_ROUTE)
	if err != nil {
		return -1
	}
	defer syscall.Close(fd)

	tv := syscall.NsecToTimeval(1000 * 1000 * 1000) // 1s 超时，防止探测挂住
	_ = syscall.SetsockoptTimeval(fd, syscall.SOL_SOCKET, syscall.SO_RCVTIMEO, &tv)

	if err := syscall.Bind(fd, &syscall.SockaddrNetlink{Family: syscall.AF_NETLINK}); err != nil {
		return -1
	}

	// nlmsghdr + rtmsg（fib rule dump 用的也是 rtmsg 头，只关心第 0 字节 Family）
	req := make([]byte, syscall.NLMSG_HDRLEN+int(unsafe.Sizeof(syscall.RtMsg{})))
	binary.LittleEndian.PutUint32(req[0:4], uint32(len(req)))
	binary.LittleEndian.PutUint16(req[4:6], syscall.RTM_GETRULE)
	binary.LittleEndian.PutUint16(req[6:8], syscall.NLM_F_REQUEST|syscall.NLM_F_DUMP)
	binary.LittleEndian.PutUint32(req[8:12], 1) // seq
	req[syscall.NLMSG_HDRLEN] = syscall.AF_INET6
	if err := syscall.Sendto(fd, req, 0, &syscall.SockaddrNetlink{Family: syscall.AF_NETLINK}); err != nil {
		return -1
	}

	buf := make([]byte, 64*1024)
	for {
		n, _, err := syscall.Recvfrom(fd, buf, 0)
		if err != nil {
			if errors.Is(err, syscall.EINTR) {
				continue
			}
			return -1
		}
		msgs, err := syscall.ParseNetlinkMessage(buf[:n])
		if err != nil {
			return -1
		}
		if status, done := parseRuleProbeMsgs(msgs); done {
			return status
		}
	}
}

func parseRuleProbeMsgs(msgs []syscall.NetlinkMessage) (status int, done bool) {
	for _, m := range msgs {
		switch m.Header.Type {
		case syscall.NLMSG_DONE:
			// Linux 内核在 dump 失败时（如 lookup_rules_ops 为 NULL 返回 -EAFNOSUPPORT）
			// 不会发 NLMSG_ERROR，而是发 NLMSG_DONE，并在负载里携带 int32 类型的负数 errno。
			// 成功时 errno 为 0（或无负载）。
			if len(m.Data) >= 4 {
				code := -int32(binary.LittleEndian.Uint32(m.Data[:4]))
				if code == 0 {
					return 1, true
				}
				if syscall.Errno(code) == syscall.EAFNOSUPPORT {
					return 0, true
				}
				return -1, true
			}
			return 1, true
		case syscall.NLMSG_ERROR:
			if len(m.Data) < 4 {
				return -1, true
			}
			code := -int32(binary.LittleEndian.Uint32(m.Data[:4]))
			if code == 0 {
				return 1, true
			}
			if syscall.Errno(code) == syscall.EAFNOSUPPORT {
				return 0, true
			}
			return -1, true
		}
	}
	return 0, false
}

// useIPv6 resolves the ipv6 setting（auto | on | off）为最终开关。
func useIPv6(mode string) bool {
	switch mode {
	case "on":
		return true
	case "off":
		return false
	default: // auto（含空值：旧 state.json）
		return HostHasIPv6()
	}
}
