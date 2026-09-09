package render

import (
	"strings"
	"testing"
)

// 规则集源默认 jsdelivr 直连；切到 github 时换成 raw.githubusercontent.com 并套镜像前缀。
func TestRuleSetSourceGitHub(t *testing.T) {
	st := testState(t)
	st.Settings.RuleSetSource = "github"
	for _, r := range build(t, st)["route"].(map[string]any)["rule_set"].([]any) {
		u := r.(map[string]any)["url"].(string)
		if !strings.HasPrefix(u, "https://raw.githubusercontent.com/MetaCubeX/meta-rules-dat/sing/geo/") {
			t.Fatalf("github 源应走 raw.githubusercontent.com: %s", u)
		}
	}
	st.Settings.MirrorPrefix = "https://ghproxy.net/"
	for _, r := range build(t, st)["route"].(map[string]any)["rule_set"].([]any) {
		u := r.(map[string]any)["url"].(string)
		if !strings.HasPrefix(u, "https://ghproxy.net/https://raw.githubusercontent.com/") {
			t.Fatalf("github 源应套镜像前缀: %s", u)
		}
	}
}

func TestRuleSetSourceText(t *testing.T) {
	s := testState(t).Settings
	if got := RuleSetSourceText(s); !strings.Contains(got, "jsdelivr") || !strings.Contains(got, "直连") {
		t.Fatalf("默认应为 jsdelivr 直连: %q", got)
	}
	s.DownloadDetour = "PROXY"
	if got := RuleSetSourceText(s); !strings.Contains(got, "PROXY") {
		t.Fatalf("下载出站 PROXY 时应体现: %q", got)
	}
	s.DownloadDetour = "direct"
	s.RuleSetSource = "github"
	if got := RuleSetSourceText(s); !strings.Contains(got, "GitHub") || !strings.Contains(got, "建议") {
		t.Fatalf("github 直连应给出提醒: %q", got)
	}
	s.MirrorPrefix = "https://ghproxy.net/"
	if got := RuleSetSourceText(s); !strings.Contains(got, "镜像 https://ghproxy.net/") || strings.Contains(got, "建议") {
		t.Fatalf("配了镜像后应显示镜像且不再提醒: %q", got)
	}
	s.RouteMode = "global"
	if got := RuleSetSourceText(s); !strings.Contains(got, "global") {
		t.Fatalf("global 模式应说明不使用规则集: %q", got)
	}
}
