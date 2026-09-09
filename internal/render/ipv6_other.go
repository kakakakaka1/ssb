//go:build !linux

package render

import "net"

// hostHasIPv6：Linux 之外没有"有 IPv6 地址却下不了 IPv6 策略路由"这类内核差异，
// sing-tun 在 Windows 上只要系统还开着 IPv6 栈就能给 wintun 网卡配 v6 地址。
// Windows 可以通过注册表 DisabledComponents 整体禁掉 IPv6，那时 AF_INET6 socket
// 直接创建失败——用一次回环监听探一下即可，不需要权限。
func hostHasIPv6() bool {
	ln, err := net.Listen("tcp6", "[::1]:0")
	if err != nil {
		return false
	}
	ln.Close()
	return true
}
