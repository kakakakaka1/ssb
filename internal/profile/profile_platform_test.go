package profile

import "testing"

func TestSingboxBinName(t *testing.T) {
	if got := singboxBinName("windows"); got != "sing-box.exe" {
		t.Errorf("windows 内核文件名应带 .exe，得到 %q", got)
	}
	if got := singboxBinName("linux"); got != "sing-box" {
		t.Errorf("linux 内核文件名不应带后缀，得到 %q", got)
	}
}
