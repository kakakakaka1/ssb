package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"ssb/internal/render"
	"ssb/internal/sub"
)

var (
	cAccent = lipgloss.Color("81")  // 青
	cOK     = lipgloss.Color("42")  // 绿
	cErr    = lipgloss.Color("203") // 红
	cWarn   = lipgloss.Color("214") // 橙
	cDim    = lipgloss.Color("243")
	cFaint  = lipgloss.Color("238")
	cWhite  = lipgloss.Color("15")

	styAccent    = lipgloss.NewStyle().Foreground(cAccent)
	styBrand     = lipgloss.NewStyle().Bold(true).Foreground(cWhite).Background(lipgloss.Color("62")).Padding(0, 1)
	styTab       = lipgloss.NewStyle().Foreground(cDim).Padding(0, 1)
	styTabActive = lipgloss.NewStyle().Bold(true).Foreground(cAccent).Underline(true).Padding(0, 1)
	styDim       = lipgloss.NewStyle().Foreground(cDim)
	styFaint     = lipgloss.NewStyle().Foreground(cFaint)
	styOK        = lipgloss.NewStyle().Foreground(cOK)
	styErr       = lipgloss.NewStyle().Foreground(cErr)
	styWarn      = lipgloss.NewStyle().Foreground(cWarn).Bold(true)
	styBold      = lipgloss.NewStyle().Bold(true)
	styCursor    = lipgloss.NewStyle().Bold(true).Foreground(cWhite).Background(lipgloss.Color("237"))
	styGroup     = lipgloss.NewStyle().Bold(true).Foreground(cAccent)
	styKey       = lipgloss.NewStyle().Foreground(cWhite).Bold(true)
	styValue     = lipgloss.NewStyle().Foreground(lipgloss.Color("222"))
	styOpt       = lipgloss.NewStyle().Foreground(cFaint)
	styOptActive = lipgloss.NewStyle().Foreground(cWhite).Background(lipgloss.Color("62")).Padding(0, 1)
)

// 布局：头 2 行（标签栏 + 分隔线），尾 3 行（分隔线 + 消息 + 帮助），中间是页面主体。
const chromeRows = 5

func (m model) bodyRows() int { return max(3, m.height-chromeRows) }

// logRows 是状态页留给日志的行数：主体减去状态块（5 行）和标题。
func (m model) logRows() int { return max(3, m.bodyRows()-7) }

func (m model) View() string {
	if m.width == 0 {
		return "加载中…"
	}
	w := m.width
	var b strings.Builder

	b.WriteString(m.viewHeader(w) + "\n")
	b.WriteString(styFaint.Render(strings.Repeat("─", w)) + "\n")

	var body string
	switch {
	case m.help:
		body = m.viewHelp()
	case m.tab == tabStatus:
		body = m.viewStatus()
	case m.tab == tabSubs:
		body = m.viewSubs()
	case m.tab == tabNodes:
		body = m.viewNodes()
	case m.tab == tabSettings:
		body = m.viewSettings()
	}
	b.WriteString(fitRows(body, m.bodyRows(), w))

	b.WriteString(styFaint.Render(strings.Repeat("─", w)) + "\n")
	b.WriteString(clip(m.viewMessage(), w) + "\n")
	b.WriteString(clip(" "+styDim.Render(m.helpLine()), w))
	return b.String()
}

// ---- header ----

func (m model) viewHeader(w int) string {
	var tabs []string
	for i, name := range tabNames {
		st := styTab
		if tab(i) == m.tab {
			st = styTabActive
		}
		tabs = append(tabs, st.Render(fmt.Sprintf("%d %s", i+1, name)))
	}
	left := styBrand.Render("ssb") + " " + strings.Join(tabs, "")

	var right string
	switch {
	case !m.loaded:
		right = styDim.Render("…")
	case m.tick.running:
		right = styOK.Render("● 运行中")
		if m.tick.now != "" {
			right += styDim.Render(" · 出口 ") + styBold.Render(m.tick.now)
		}
	default:
		right = styDim.Render("○ 未运行")
	}
	if m.dirty {
		right = styWarn.Render("⚠ 配置有改动，按 r 重启生效") + styDim.Render("  ") + right
	}
	gap := w - lipgloss.Width(left) - lipgloss.Width(right) - 1
	if gap < 1 {
		return clip(left, w)
	}
	return left + strings.Repeat(" ", gap) + right
}

// ---- footer ----

func (m model) viewMessage() string {
	switch m.mode {
	case modeInput:
		return " " + m.input.View()
	case modeConfirm:
		return " " + styWarn.Render(m.confirmQ) + styDim.Render("  y 确认 / 其他键取消")
	}
	if m.busy {
		return " " + m.spin.View() + " " + styDim.Render(m.flash)
	}
	if m.flash == "" {
		return ""
	}
	if m.flashE {
		return " " + styErr.Render("✗ "+oneLine(m.flash))
	}
	return " " + styOK.Render("✓ ") + oneLine(m.flash)
}

func (m model) helpLine() string {
	if m.mode == modeInput {
		return "回车 确认 · Esc 取消"
	}
	if m.mode == modeConfirm {
		return ""
	}
	if m.help {
		return "任意键返回"
	}
	common := "s 启停 · r 应用重启 · g 生成 · ? 全部按键 · q 退出"
	switch m.tab {
	case tabStatus:
		return "j/k 翻日志 · i 下载内核 · " + common
	case tabSubs:
		return "a 添加 · 回车 更新 · U 全部更新 · x 删除 · " + common
	case tabNodes:
		return "回车 切换出口 · a 添加链接 · x 删除手动节点 · " + common
	case tabSettings:
		rows := settingRows(m.a)
		if c := m.cursor[tabSettings]; c < len(rows) && rows[c].desc != "" {
			return rows[c].desc
		}
		return "回车 修改 · " + common
	}
	return common
}

// ---- help overlay ----

func (m model) viewHelp() string {
	type kv struct{ k, v string }
	groups := []struct {
		title string
		keys  []kv
	}{
		{"全局", []kv{
			{"1-4 / Tab / h l", "切换页签"},
			{"s", "启动 / 停止 sing-box"},
			{"r", "重启（生成配置 + 重启，改动生效）"},
			{"g", "只重新生成 config.json"},
			{"j k / ↑ ↓ / PgUp PgDn / Home End", "移动光标、翻页"},
			{"q / Ctrl-C", "退出（sing-box 继续后台运行）"},
		}},
		{"状态页", []kv{
			{"i", "下载 sing-box 内核到 data/"},
			{"j k", "翻看日志"},
		}},
		{"订阅页", []kv{
			{"a", "添加订阅：粘贴链接，后面可空格加名称"},
			{"回车 / u", "更新当前订阅"},
			{"U", "更新全部订阅"},
			{"x", "删除当前订阅（确认后生效）"},
		}},
		{"节点页", []kv{
			{"回车 / 空格", "把出口切到当前行（首行 auto 为自动测速）"},
			{"a", "粘贴分享链接添加节点"},
			{"x", "删除手动节点（订阅节点请删订阅）"},
		}},
		{"设置页", []kv{
			{"回车 / 空格", "开关和枚举：切到下一个；文本：进入编辑"},
			{"退格", "开关和枚举：切到上一个"},
			{"", "改完自动保存并生成配置；服务在跑时按 r 重启生效"},
		}},
	}
	var b strings.Builder
	for _, g := range groups {
		b.WriteString(" " + styGroup.Render(g.title) + "\n")
		for _, e := range g.keys {
			b.WriteString("   " + pad(styKey.Render(e.k), 40) + styDim.Render(e.v) + "\n")
		}
		b.WriteString("\n")
	}
	return b.String()
}

// ---- status ----

func (m model) viewStatus() string {
	var b strings.Builder
	kv := func(k, v string) {
		b.WriteString(" " + styDim.Render(pad(k, 10)) + v + "\n")
	}
	s := m.a.State.Settings
	t := m.tick
	switch {
	case !m.loaded:
		kv("服务", styDim.Render("读取中…"))
	case t.running:
		kv("服务", styOK.Render(fmt.Sprintf("运行中  pid %d", t.pid)))
	default:
		kv("服务", styDim.Render("未运行")+styDim.Render("  （s 启动）"))
	}
	if t.version != "" {
		kv("内核", strings.TrimPrefix(t.version, "sing-box version "))
	} else if m.loaded {
		kv("内核", styErr.Render("未找到")+styDim.Render("  （i 下载到 data/）"))
	}
	if t.running {
		if t.apiAlive {
			out := t.now
			if out == "" {
				out = styDim.Render("（未知）")
			}
			kv("出口", styBold.Render(out)+styDim.Render("  节点页回车切换"))
		} else {
			kv("出口", styWarn.Render("API 服务不可达"))
		}
	}
	kv("节点", fmt.Sprintf("%d 个（手动 %d，订阅 %d 个源）",
		len(m.a.State.AllNodes()), len(m.a.State.Manual), len(m.a.State.Subscriptions)))
	mode := s.RouteMode
	if s.TunEnabled {
		mode += " · TUN"
	}
	if s.MixedEnabled || !s.TunEnabled {
		mode += fmt.Sprintf(" · mixed 127.0.0.1:%d", s.MixedPort)
	}
	kv("模式", mode)
	kv("规则集", render.RuleSetSourceText(s))
	kv("API", styAccent.Render(s.APIListen)+styDim.Render("  secret ")+styBold.Render(s.APISecret))
	if s.DashboardOff {
		kv("面板", styDim.Render("已关闭（设置页开启）"))
	} else {
		kv("面板", styAccent.Render(m.a.DashboardURL())+styDim.Render("  打开后填上面的 secret"))
	}

	b.WriteString("\n " + styGroup.Render("日志") + styDim.Render("  logs/sing-box.log"))
	tail := strings.TrimRight(m.tick.logTail, "\n")
	if strings.TrimSpace(tail) == "" || strings.Contains(tail, "无法读取日志") {
		b.WriteString("\n " + styDim.Render("（暂无日志）") + "\n")
		return b.String()
	}
	lines := strings.Split(tail, "\n")
	rows := m.logRows()
	end := len(lines) - m.logLine
	start := max(0, end-rows)
	if m.logLine > 0 {
		b.WriteString(styDim.Render(fmt.Sprintf("  ↑ 还有 %d 行，j 回到底部", m.logLine)))
	}
	b.WriteString("\n")
	for _, line := range lines[start:end] {
		b.WriteString(" " + colorLog(clip(line, max(20, m.width-2))) + "\n")
	}
	return b.String()
}

func colorLog(line string) string {
	switch {
	case strings.Contains(line, "FATAL"), strings.Contains(line, "ERROR"):
		return styErr.Render(line)
	case strings.Contains(line, "WARN"):
		return styWarn.Render(line)
	}
	return styDim.Render(line)
}

// ---- subs ----

func (m model) viewSubs() string {
	subs := m.a.State.Subscriptions
	if len(subs) == 0 {
		return empty("还没有订阅", "按 a 粘贴订阅链接；订阅节点会和手动节点一起出现在节点页")
	}
	var b strings.Builder
	cur := m.cursor[tabSubs]
	rowsPer := 2
	start, end := window(len(subs), cur, m.bodyRows()/rowsPer)
	for i := start; i < end; i++ {
		s := subs[i]
		name := pad(clip(s.Name, 16), 16)
		info := fmt.Sprintf("%3d 节点", len(s.Nodes))
		if s.UpdatedAt.IsZero() {
			info += styDim.Render(" · 从未更新")
		} else {
			info += styDim.Render(" · " + s.UpdatedAt.Format("01-02 15:04"))
		}
		if u := s.Userinfo; u != nil && u.Total > 0 {
			info += styDim.Render(" · ") + fmt.Sprintf("%s / %s", sub.HumanBytes(u.Upload+u.Download), sub.HumanBytes(u.Total))
			if u.Expire > 0 {
				info += styDim.Render(" · 到期 " + time.Unix(u.Expire, 0).Format("2006-01-02"))
			}
		}
		if i == cur {
			b.WriteString(styCursor.Render(" ▸ "+name) + " " + info + "\n")
		} else {
			b.WriteString("   " + name + " " + info + "\n")
		}
		b.WriteString("      " + styFaint.Render(clip(s.URL, max(20, m.width-8))) + "\n")
	}
	b.WriteString(pager(start, end, len(subs)))
	return b.String()
}

// ---- nodes ----

func (m model) viewNodes() string {
	nodes := m.a.State.AllNodes()
	cur := m.cursor[tabNodes]
	total := len(nodes) + 1
	now := m.tick.now
	if !m.tick.running {
		now = ""
	}

	var b strings.Builder
	start, end := window(total, cur, m.bodyRows()-1)
	for i := start; i < end; i++ {
		mark := "  "
		var line string
		if i == 0 {
			if now == "auto" {
				mark = styOK.Render("● ")
			}
			line = pad("auto", 30) + styDim.Render("自动测速，选延迟最低的节点")
		} else {
			n := nodes[i-1]
			if now != "" && n.Tag == now {
				mark = styOK.Render("● ")
			}
			src := styDim.Render("订")
			if m.isManual(n.Tag) {
				src = styAccent.Render("手")
			}
			line = pad(clip(n.Tag, 30), 30) + src + " " +
				styDim.Render(pad(fmt.Sprint(n.Outbound["type"]), 12)) +
				styFaint.Render(clip(fmt.Sprint(n.Outbound["server"]), max(10, m.width-52)))
		}
		if i == cur {
			b.WriteString(styCursor.Render(" ▸ ") + mark + line + "\n")
		} else {
			b.WriteString("   " + mark + line + "\n")
		}
	}
	if len(nodes) == 0 {
		b.WriteString("\n" + empty("还没有节点", "按 a 粘贴分享链接，或到订阅页添加订阅"))
	}
	b.WriteString(pager(start, end, total))
	return b.String()
}

// ---- settings ----

func (m model) viewSettings() string {
	rows := settingRows(m.a)
	cur := m.cursor[tabSettings]

	// 先渲染成行（含分组标题），再按光标所在行开窗
	var lines []string
	curLine := 0
	prevGroup := ""
	for i, r := range rows {
		if r.group != prevGroup {
			if i > 0 {
				lines = append(lines, "")
			}
			lines = append(lines, " "+styGroup.Render(r.group))
			prevGroup = r.group
		}
		if i == cur {
			curLine = len(lines)
		}
		label := pad(clip(r.label, 20), 20)
		var val string
		if len(r.opts) > 0 {
			val = renderOpts(r.opts, r.get(m.a))
		} else {
			val = " " + styValue.Render(valueOrEmpty(r.get(m.a)))
		}
		if i == cur {
			lines = append(lines, styCursor.Render(" ▸ "+label)+" "+val)
		} else {
			lines = append(lines, "   "+label+" "+val)
		}
	}
	rowsAvail := m.bodyRows()
	start, end := window(len(lines), curLine, rowsAvail)
	return strings.Join(lines[start:end], "\n") + "\n"
}

func renderOpts(opts []string, cur string) string {
	parts := make([]string, 0, len(opts))
	for _, o := range opts {
		if o == cur {
			parts = append(parts, styOptActive.Render(o))
		} else {
			parts = append(parts, styOpt.Render(" "+o+" "))
		}
	}
	return strings.Join(parts, "")
}

// ---- helpers ----

func empty(title, hint string) string {
	return "\n   " + styBold.Render(title) + "\n   " + styDim.Render(hint) + "\n"
}

// window 返回让 cur 可见的 [start,end) 区间。
func window(total, cur, rows int) (int, int) {
	rows = max(1, rows)
	if total <= rows {
		return 0, total
	}
	start := clamp(cur-rows/2, 0, total-rows)
	return start, start + rows
}

func pager(start, end, total int) string {
	if total > end-start {
		return styFaint.Render(fmt.Sprintf("   %d–%d / %d", start+1, end, total)) + "\n"
	}
	return ""
}

func valueOrEmpty(v string) string {
	if v == "" {
		return styDim.Render("（空）")
	}
	return v
}

func oneLine(s string) string {
	s = strings.ReplaceAll(strings.TrimSpace(s), "\n", " ⏎ ")
	return s
}

// fitRows 把 body 裁/补到正好 rows 行，保证底栏位置固定。
func fitRows(body string, rows, width int) string {
	lines := strings.Split(strings.TrimRight(body, "\n"), "\n")
	if len(lines) > rows {
		lines = lines[:rows]
	}
	for len(lines) < rows {
		lines = append(lines, "")
	}
	for i, l := range lines {
		lines[i] = clip(l, width)
	}
	return strings.Join(lines, "\n") + "\n"
}

// pad right-pads s with spaces to display width n（fmt 的 %-Ns 按字符数不按
// 终端列宽，中文占两格会歪，必须用 lipgloss.Width）.
func pad(s string, n int) string {
	if d := n - lipgloss.Width(s); d > 0 {
		return s + strings.Repeat(" ", d)
	}
	return s
}

// clip truncates s to display width n（对含 ANSI 样式的串只在超宽时截断）。
func clip(s string, n int) string {
	if n <= 0 || lipgloss.Width(s) <= n {
		return s
	}
	r := []rune(s)
	for len(r) > 0 && lipgloss.Width(string(r)) > n-1 {
		r = r[:len(r)-1]
	}
	return string(r) + "…"
}
