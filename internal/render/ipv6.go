package render

// HostHasIPv6 reports whether this host can carry the IPv6 half of the TUN
// config（TUN v6 地址、strict_route、FakeIP v6 段、放行 AAAA）。
//
// 平台实现：ipv6_linux.go 看 procfs 并用 netlink 探测 AF_INET6 策略路由（sing-box
// auto_route 真正需要的能力）；ipv6_other.go 只确认系统还开着 IPv6 栈。
// 变量而非函数，方便测试替换。
var HostHasIPv6 = hostHasIPv6

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
