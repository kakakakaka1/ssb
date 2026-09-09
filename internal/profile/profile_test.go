package profile

import (
	"testing"

	"ssb/internal/link"
)

func TestLoadSaveRoundtrip(t *testing.T) {
	d := Dirs{Base: t.TempDir()}
	st, err := Load(d)
	if err != nil {
		t.Fatal(err)
	}
	if st.Settings.MixedPort != 2080 || !st.Settings.TunEnabled || st.Settings.TunAddress != "10.255.0.1/30" || st.Settings.ClashSecret == "" {
		t.Fatalf("默认设置错误: %+v", st.Settings)
	}
	n, err := link.Parse("trojan://pw@1.1.1.1:443?sni=a.com#节点一")
	if err != nil {
		t.Fatal(err)
	}
	st.Manual = append(st.Manual, n)
	secret := st.Settings.ClashSecret
	if err := st.Save(d); err != nil {
		t.Fatal(err)
	}

	st2, err := Load(d)
	if err != nil {
		t.Fatal(err)
	}
	if len(st2.Manual) != 1 || st2.Manual[0].Tag != "节点一" {
		t.Fatalf("节点没有存续: %+v", st2.Manual)
	}
	if st2.Settings.ClashSecret != secret {
		t.Fatal("secret 应保持稳定")
	}
	if st2.Manual[0].Outbound["type"] != "trojan" {
		t.Fatalf("outbound 反序列化错误: %v", st2.Manual[0].Outbound)
	}
}

func TestAllNodesDedup(t *testing.T) {
	d := Dirs{Base: t.TempDir()}
	st, _ := Load(d)
	n1, _ := link.Parse("trojan://pw@1.1.1.1:443#同名")
	n2, _ := link.Parse("trojan://pw@2.2.2.2:443#同名")
	st.Manual = []*link.Node{n1, n2}
	all := st.AllNodes()
	if all[0].Tag == all[1].Tag {
		t.Fatalf("tag 未去重: %q vs %q", all[0].Tag, all[1].Tag)
	}
	if all[1].Outbound["tag"] != all[1].Tag {
		t.Fatal("outbound tag 未同步")
	}
	// 去重不应污染原始节点
	if n2.Outbound["tag"] != "同名" {
		t.Fatalf("原始节点被改写: %v", n2.Outbound["tag"])
	}
}
