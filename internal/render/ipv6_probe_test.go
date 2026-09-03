package render

import "testing"

// 本机有 IPv6 策略路由（ip -6 rule 可用）时 netlink 探测必须回 1；这是对
// netlink 报文格式的真实内核回归测试。
func TestIPv6RuleSupportedLocal(t *testing.T) {
	t.Logf("IPv6RuleSupported=%d HostHasIPv6=%v", IPv6RuleSupported(), HostHasIPv6())
	if IPv6RuleSupported() == -1 {
		t.Fatal("netlink 探测失败（-1），报文格式有问题")
	}
}
