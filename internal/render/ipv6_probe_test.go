package render

import (
	"encoding/binary"
	"syscall"
	"testing"
)

// 本机有 IPv6 策略路由（ip -6 rule 可用）时 netlink 探测必须回 1；这是对
// netlink 报文格式的真实内核回归测试。
func TestIPv6RuleSupportedLocal(t *testing.T) {
	t.Logf("IPv6RuleSupported=%d HostHasIPv6=%v", IPv6RuleSupported(), HostHasIPv6())
	if IPv6RuleSupported() == -1 {
		t.Fatal("netlink 探测失败（-1），报文格式有问题")
	}
}

func TestParseRuleProbeMsgs(t *testing.T) {
	makeDoneMsg := func(code int32) syscall.NetlinkMessage {
		data := make([]byte, 4)
		binary.LittleEndian.PutUint32(data, uint32(code))
		return syscall.NetlinkMessage{
			Header: syscall.NlMsghdr{Type: syscall.NLMSG_DONE},
			Data:   data,
		}
	}

	// 1. NLMSG_DONE with code -97 (-EAFNOSUPPORT) -> 0 (不支持)
	// Linux 内核在 fib_rules_dump 不支持时返回 -EAFNOSUPPORT，放在 NLMSG_DONE 负载中。
	st, done := parseRuleProbeMsgs([]syscall.NetlinkMessage{makeDoneMsg(-int32(syscall.EAFNOSUPPORT))})
	if !done || st != 0 {
		t.Fatalf("NLMSG_DONE with -EAFNOSUPPORT 应返回 0 (不支持), got status=%d done=%v", st, done)
	}

	// 2. NLMSG_DONE with code 0 -> 1 (支持)
	st, done = parseRuleProbeMsgs([]syscall.NetlinkMessage{makeDoneMsg(0)})
	if !done || st != 1 {
		t.Fatalf("NLMSG_DONE with 0 应返回 1 (支持), got status=%d done=%v", st, done)
	}

	// 3. NLMSG_DONE with empty payload -> 1 (支持)
	st, done = parseRuleProbeMsgs([]syscall.NetlinkMessage{{Header: syscall.NlMsghdr{Type: syscall.NLMSG_DONE}}})
	if !done || st != 1 {
		t.Fatalf("NLMSG_DONE 空负载应返回 1 (支持), got status=%d done=%v", st, done)
	}

	// 4. NLMSG_DONE with other error (e.g. -EPERM) -> -1
	st, done = parseRuleProbeMsgs([]syscall.NetlinkMessage{makeDoneMsg(-int32(syscall.EPERM))})
	if !done || st != -1 {
		t.Fatalf("NLMSG_DONE with -EPERM 应返回 -1, got status=%d done=%v", st, done)
	}

	// 5. NLMSG_ERROR with -EAFNOSUPPORT -> 0
	makeErrMsg := func(code int32) syscall.NetlinkMessage {
		data := make([]byte, 4)
		binary.LittleEndian.PutUint32(data, uint32(code))
		return syscall.NetlinkMessage{
			Header: syscall.NlMsghdr{Type: syscall.NLMSG_ERROR},
			Data:   data,
		}
	}
	st, done = parseRuleProbeMsgs([]syscall.NetlinkMessage{makeErrMsg(-int32(syscall.EAFNOSUPPORT))})
	if !done || st != 0 {
		t.Fatalf("NLMSG_ERROR with -EAFNOSUPPORT 应返回 0 (不支持), got status=%d done=%v", st, done)
	}
}
