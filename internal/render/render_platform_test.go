package render

import (
	"runtime"
	"testing"
)

// auto_redirect 只在 Linux 上写入：其他平台 sing-box 会拒绝启动。
func TestAutoRedirectLinuxOnly(t *testing.T) {
	for goos, want := range map[string]bool{"linux": true, "windows": false, "darwin": false} {
		if supportsAutoRedirect(goos) != want {
			t.Errorf("supportsAutoRedirect(%s) 应为 %v", goos, want)
		}
	}
	st := testState(t)
	st.Settings.AutoRedirect = true
	tun := build(t, st)["inbounds"].([]any)[0].(map[string]any)
	if _, has := tun["auto_redirect"]; has != supportsAutoRedirect(runtime.GOOS) {
		t.Fatalf("auto_redirect 写入与否应跟随平台（%s）: %v", runtime.GOOS, tun)
	}
}
