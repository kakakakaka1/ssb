package render

import "os"

// HostHasIPv6 reports whether the running kernel has IPv6 at all.
//
// 内核层面关掉 IPv6（启动参数 ipv6.disable=1，或没编译 CONFIG_IPV6）时
// /proc/net/if_inet6 不存在。这种机器上只要 TUN 配了 IPv6 地址，或者开了
// strict_route（sing-tun 会为缺失的地址族补一条 AF_INET6 unreachable 策略路由），
// sing-box 下发 AF_INET6 策略路由就会被内核拒绝：
//
//	FATAL start service: post-start inbound/tun[tun-in]: starting TUN interface:
//	set rules: add rule 2/16: address family not supported by protocol
//
// 变量而非函数，方便测试替换。
var HostHasIPv6 = func() bool {
	_, err := os.Stat("/proc/net/if_inet6")
	return err == nil
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
