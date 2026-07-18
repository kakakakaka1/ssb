// Package tui is the interactive terminal UI: 服务 / 订阅 / 节点 / 设置 四页签。
// 节点页可直接切换出口（clash_api）；测速、连接查看等仍交给官方 Dashboard，
// 其余职责：订阅/节点管理、配置生成、进程启停、设置。
package tui

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"ssb/internal/app"
	"ssb/internal/sbx"
	"ssb/internal/sub"
)

type tab int

const (
	tabService tab = iota
	tabSubs
	tabNodes
	tabSettings
	tabCount
)

var tabNames = []string{"服务", "订阅", "节点", "设置"}

var (
	styTabBar    = lipgloss.NewStyle().Padding(0, 1)
	styTab       = lipgloss.NewStyle().Padding(0, 2).Foreground(lipgloss.Color("245"))
	styTabActive = lipgloss.NewStyle().Padding(0, 2).Bold(true).
			Foreground(lipgloss.Color("15")).Background(lipgloss.Color("62"))
	styTitle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("81"))
	styDim    = lipgloss.NewStyle().Foreground(lipgloss.Color("243"))
	styOK     = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	styErr    = lipgloss.NewStyle().Foreground(lipgloss.Color("203"))
	styCursor = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("212"))
	styBody   = lipgloss.NewStyle().Padding(0, 1)
)

// ---- messages ----

type tickMsg struct {
	status  string
	logTail string
	now     string // PROXY 选择器当前出口（clash_api 不可达时为空）
}

type opDoneMsg struct {
	flash string
	isErr bool
}

type model struct {
	a             *app.App
	tab           tab
	width, height int

	status   string
	logTail  string
	nowProxy string
	busy     bool
	flash    string
	flashE   bool

	subCursor  int
	nodeCursor int
	setCursor  int

	input       textinput.Model
	inputMode   string // "" | addlink | suburl | subname | edit
	pendingURL  string
	editSetting *settingRow
}

// Run starts the TUI.
func Run(a *app.App) error {
	ti := textinput.New()
	ti.CharLimit = 4096
	ti.Width = 70
	m := model{a: a, input: ti}
	_, err := tea.NewProgram(m, tea.WithAltScreen()).Run()
	return err
}

func (m model) Init() tea.Cmd { return tea.Batch(refreshCmd(m.a), tickCmd(m.a)) }

func refreshCmd(a *app.App) tea.Cmd {
	return func() tea.Msg {
		return tickMsg{status: a.StatusText(), logTail: sbx.Tail(a.Dirs, 12), now: a.SelectedNode()}
	}
}

func tickCmd(a *app.App) tea.Cmd {
	return tea.Tick(3*time.Second, func(time.Time) tea.Msg {
		return tickMsg{status: a.StatusText(), logTail: sbx.Tail(a.Dirs, 12), now: a.SelectedNode()}
	})
}

func opCmd(f func() (string, error)) tea.Cmd {
	return func() tea.Msg {
		msg, err := f()
		if err != nil {
			return opDoneMsg{flash: err.Error(), isErr: true}
		}
		return opDoneMsg{flash: msg}
	}
}

// ---- update ----

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil

	case tickMsg:
		m.status, m.logTail, m.nowProxy = msg.status, msg.logTail, msg.now
		return m, tickCmd(m.a)

	case opDoneMsg:
		m.busy = false
		m.flash, m.flashE = msg.flash, msg.isErr
		return m, refreshCmd(m.a)

	case tea.KeyMsg:
		if m.inputMode != "" {
			return m.updateInput(msg)
		}
		return m.updateKeys(msg)
	}
	return m, nil
}

func (m model) updateInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc:
		m.inputMode, m.editSetting = "", nil
		m.input.Blur()
		return m, nil
	case tea.KeyEnter:
		val := strings.TrimSpace(m.input.Value())
		mode := m.inputMode
		m.inputMode = ""
		m.input.Blur()
		m.input.SetValue("")
		switch mode {
		case "addlink":
			return m.startOp(func() (string, error) {
				n, errs := m.a.AddNodes(val)
				if n == 0 {
					return "", firstErr(errs)
				}
				warn, err := m.a.Generate()
				if err != nil {
					return "", fmt.Errorf("已添加 %d 个节点，但生成失败: %w", n, err)
				}
				return okGen(fmt.Sprintf("已添加 %d 个节点", n), warn), nil
			})
		case "suburl":
			if val == "" {
				return m, nil
			}
			m.pendingURL = val
			return m.prompt("subname", "订阅名称（可空，回车确认）")
		case "subname":
			url := m.pendingURL
			m.pendingURL = ""
			return m.startOp(func() (string, error) {
				s, err := m.a.SubAdd(context.Background(), val, url)
				if err != nil {
					return "", err
				}
				warn, err := m.a.Generate()
				if err != nil {
					return "", fmt.Errorf("订阅已添加但生成失败: %w", err)
				}
				return okGen(fmt.Sprintf("✓ %s: %d 个节点 (%s)", s.Name, len(s.Nodes), s.Format), warn), nil
			})
		case "edit":
			if m.editSetting != nil {
				row := m.editSetting
				m.editSetting = nil
				if err := row.set(m.a, val); err != nil {
					m.flash, m.flashE = err.Error(), true
					return m, nil
				}
				if err := m.a.Save(); err != nil {
					m.flash, m.flashE = err.Error(), true
					return m, nil
				}
				m.flash, m.flashE = "已保存（g 重新生成、r 重启后生效）", false
			}
			return m, nil
		}
		return m, nil
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m model) updateKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	switch key {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "tab", "right":
		m.tab = (m.tab + 1) % tabCount
		return m, nil
	case "shift+tab", "left":
		m.tab = (m.tab + tabCount - 1) % tabCount
		return m, nil
	case "1", "2", "3", "4":
		m.tab = tab(key[0] - '1')
		return m, nil
	}
	if m.busy {
		return m, nil
	}
	switch m.tab {
	case tabService:
		return m.keysService(key)
	case tabSubs:
		return m.keysSubs(key)
	case tabNodes:
		return m.keysNodes(key)
	case tabSettings:
		return m.keysSettings(key)
	}
	return m, nil
}

func (m model) startOp(f func() (string, error)) (tea.Model, tea.Cmd) {
	m.busy = true
	m.flash, m.flashE = "处理中…", false
	return m, opCmd(f)
}

func (m model) prompt(mode, placeholder string) (tea.Model, tea.Cmd) {
	m.inputMode = mode
	m.input.Placeholder = placeholder
	m.input.SetValue("")
	m.input.Focus()
	return m, textinput.Blink
}

func (m model) keysService(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "s":
		if _, running := sbx.Running(m.a.Dirs); running {
			return m.startOp(func() (string, error) {
				if err := m.a.Stop(); err != nil {
					return "", err
				}
				return "已停止", nil
			})
		}
		return m.startOp(func() (string, error) {
			if err := m.a.Start(context.Background()); err != nil {
				return "", err
			}
			if m.a.State.Settings.DashboardOff {
				return "已启动（Dashboard 已关闭，节点页回车切换出口）", nil
			}
			return "已启动 · Dashboard: " + m.a.DashboardURL(), nil
		})
	case "r":
		return m.startOp(func() (string, error) {
			if err := m.a.Restart(context.Background()); err != nil {
				return "", err
			}
			return "已重启", nil
		})
	case "g":
		return m.startOp(func() (string, error) {
			warn, err := m.a.Generate()
			if err != nil {
				return "", err
			}
			return okGen("config.json 已生成并通过校验", warn), nil
		})
	case "i":
		return m.startOp(func() (string, error) {
			return m.a.Install(context.Background())
		})
	case "d":
		if m.a.State.Settings.DashboardOff {
			m.flash, m.flashE = "Dashboard 已关闭（设置页可重新开启；节点切换在节点页回车）", false
			return m, nil
		}
		m.flash, m.flashE = "Dashboard: "+m.a.DashboardURL()+"  secret: "+m.a.State.Settings.ClashSecret, false
		return m, nil
	}
	return m, nil
}

func (m model) keysSubs(key string) (tea.Model, tea.Cmd) {
	subs := m.a.State.Subscriptions
	switch key {
	case "j", "down":
		if m.subCursor < len(subs)-1 {
			m.subCursor++
		}
	case "k", "up":
		if m.subCursor > 0 {
			m.subCursor--
		}
	case "a":
		return m.prompt("suburl", "订阅 URL（回车确认，Esc 取消）")
	case "u":
		if len(subs) == 0 {
			return m, nil
		}
		name := subs[m.subCursor].Name
		return m.startOp(func() (string, error) {
			report, err := m.a.SubUpdate(context.Background(), name)
			if err != nil {
				return "", fmt.Errorf("%s", strings.Join(report, " / "))
			}
			warn, gerr := m.a.Generate()
			if gerr != nil {
				return "", gerr
			}
			return okGen(strings.Join(report, " / "), warn), nil
		})
	case "U":
		if len(subs) == 0 {
			return m, nil
		}
		return m.startOp(func() (string, error) {
			report, _ := m.a.SubUpdate(context.Background(), "")
			warn, gerr := m.a.Generate()
			if gerr != nil {
				return "", gerr
			}
			return okGen(strings.Join(report, " / "), warn), nil
		})
	case "x":
		if len(subs) == 0 {
			return m, nil
		}
		name := subs[m.subCursor].Name
		if m.subCursor > 0 {
			m.subCursor--
		}
		return m.startOp(func() (string, error) {
			if err := m.a.SubRemove(name); err != nil {
				return "", err
			}
			warn, err := m.a.Generate()
			if err != nil {
				return "", err
			}
			return okGen("已删除 "+name, warn), nil
		})
	}
	return m, nil
}

func (m model) keysNodes(key string) (tea.Model, tea.Cmd) {
	nodes := m.a.State.AllNodes()
	switch key {
	case "j", "down":
		if m.nodeCursor < len(nodes)-1 {
			m.nodeCursor++
		}
	case "k", "up":
		if m.nodeCursor > 0 {
			m.nodeCursor--
		}
	case "a":
		return m.prompt("addlink", "粘贴分享链接（支持多条空格分隔 / base64，Esc 取消）")
	case "enter", " ":
		if len(nodes) == 0 {
			return m, nil
		}
		tag := nodes[m.nodeCursor].Tag
		return m.startOp(func() (string, error) {
			if err := m.a.SelectNode(tag); err != nil {
				return "", err
			}
			return "已切换出口 → " + tag, nil
		})
	case "A":
		return m.startOp(func() (string, error) {
			if err := m.a.SelectNode("auto"); err != nil {
				return "", err
			}
			return "已切换出口 → auto（自动测速）", nil
		})
	case "x":
		if len(nodes) == 0 {
			return m, nil
		}
		tag := nodes[m.nodeCursor].Tag
		if m.nodeCursor > 0 {
			m.nodeCursor--
		}
		return m.startOp(func() (string, error) {
			if err := m.a.RemoveManual(tag); err != nil {
				return "", fmt.Errorf("%v（订阅节点请到订阅页删除订阅）", err)
			}
			warn, err := m.a.Generate()
			if err != nil {
				return "", err
			}
			return okGen("已删除 "+tag, warn), nil
		})
	}
	return m, nil
}

func (m model) keysSettings(key string) (tea.Model, tea.Cmd) {
	rows := settingRows()
	switch key {
	case "j", "down":
		if m.setCursor < len(rows)-1 {
			m.setCursor++
		}
	case "k", "up":
		if m.setCursor > 0 {
			m.setCursor--
		}
	case "enter", " ":
		row := rows[m.setCursor]
		if row.cycle != nil { // 开关/枚举：直接轮转
			row.cycle(m.a)
			if err := m.a.Save(); err != nil {
				m.flash, m.flashE = err.Error(), true
				return m, nil
			}
			m.flash, m.flashE = "已保存（g 重新生成、r 重启后生效）", false
			return m, nil
		}
		m.editSetting = &row
		mm, cmd := m.prompt("edit", row.label+" = "+row.get(m.a))
		return mm, cmd
	case "g":
		return m.startOp(func() (string, error) {
			warn, err := m.a.Generate()
			if err != nil {
				return "", err
			}
			return okGen("config.json 已生成并通过校验", warn), nil
		})
	case "r":
		return m.startOp(func() (string, error) {
			if err := m.a.Restart(context.Background()); err != nil {
				return "", err
			}
			return "已重启", nil
		})
	}
	return m, nil
}

// ---- settings table ----

type settingRow struct {
	label string
	get   func(*app.App) string
	set   func(*app.App, string) error
	cycle func(*app.App) // 非 nil 表示回车轮转（bool / 枚举）
}

func settingRows() []settingRow {
	boolStr := func(b bool) string {
		if b {
			return "开"
		}
		return "关"
	}
	return []settingRow{
		{label: "路由模式（rule=绕过大陆 / global=全代理）",
			get: func(a *app.App) string { return a.State.Settings.RouteMode },
			cycle: func(a *app.App) {
				s := &a.State.Settings
				if s.RouteMode == "rule" {
					s.RouteMode = "global"
				} else {
					s.RouteMode = "rule"
				}
			}},
		{label: "TUN 透明代理（需 root/CAP_NET_ADMIN）",
			get:   func(a *app.App) string { return boolStr(a.State.Settings.TunEnabled) },
			cycle: func(a *app.App) { a.State.Settings.TunEnabled = !a.State.Settings.TunEnabled }},
		{label: "auto_redirect（Linux nftables 加速）",
			get:   func(a *app.App) string { return boolStr(a.State.Settings.AutoRedirect) },
			cycle: func(a *app.App) { a.State.Settings.AutoRedirect = !a.State.Settings.AutoRedirect }},
		{label: "FakeIP",
			get:   func(a *app.App) string { return boolStr(a.State.Settings.FakeIP) },
			cycle: func(a *app.App) { a.State.Settings.FakeIP = !a.State.Settings.FakeIP }},
		{label: "本地 mixed 入站（socks/http）",
			get:   func(a *app.App) string { return boolStr(a.State.Settings.MixedEnabled) },
			cycle: func(a *app.App) { a.State.Settings.MixedEnabled = !a.State.Settings.MixedEnabled }},
		{label: "mixed 端口",
			get: func(a *app.App) string { return strconv.Itoa(a.State.Settings.MixedPort) },
			set: func(a *app.App, v string) error {
				n, err := strconv.Atoi(v)
				if err != nil || n <= 0 || n > 65535 {
					return fmt.Errorf("端口无效")
				}
				a.State.Settings.MixedPort = n
				return nil
			}},
		{label: "clash_api 监听（0.0.0.0 可局域网访问）",
			get: func(a *app.App) string { return a.State.Settings.ClashListen },
			set: func(a *app.App, v string) error { a.State.Settings.ClashListen = v; return nil }},
		{label: "Dashboard 网页面板（关=仅 TUI/API 控制）",
			get:   func(a *app.App) string { return boolStr(!a.State.Settings.DashboardOff) },
			cycle: func(a *app.App) { a.State.Settings.DashboardOff = !a.State.Settings.DashboardOff }},
		{label: "Dashboard（metacubexd / zashboard / yacd）",
			get: func(a *app.App) string { return a.State.Settings.ExternalUI },
			cycle: func(a *app.App) {
				s := &a.State.Settings
				switch s.ExternalUI {
				case "metacubexd":
					s.ExternalUI = "zashboard"
				case "zashboard":
					s.ExternalUI = "yacd"
				default:
					s.ExternalUI = "metacubexd"
				}
			}},
		{label: "GitHub 镜像前缀（UI/内核下载加速，可空）",
			get: func(a *app.App) string { return a.State.Settings.MirrorPrefix },
			set: func(a *app.App, v string) error { a.State.Settings.MirrorPrefix = v; return nil }},
		{label: "规则集/UI 下载出站（direct / PROXY）",
			get: func(a *app.App) string { return a.State.Settings.DownloadDetour },
			cycle: func(a *app.App) {
				s := &a.State.Settings
				if s.DownloadDetour == "direct" {
					s.DownloadDetour = "PROXY"
				} else {
					s.DownloadDetour = "direct"
				}
			}},
		{label: "国内 DNS（udp）",
			get: func(a *app.App) string { return a.State.Settings.DNSCN },
			set: func(a *app.App, v string) error { a.State.Settings.DNSCN = v; return nil }},
		{label: "代理 DNS（https）",
			get: func(a *app.App) string { return a.State.Settings.DNSProxy },
			set: func(a *app.App, v string) error { a.State.Settings.DNSProxy = v; return nil }},
		{label: "sing-box 路径（空=自动：data/ 或 PATH）",
			get: func(a *app.App) string { return a.State.Settings.SingboxPath },
			set: func(a *app.App, v string) error { a.State.Settings.SingboxPath = v; return nil }},
	}
}

// ---- view ----

func (m model) View() string {
	var b strings.Builder

	var tabs []string
	for i, name := range tabNames {
		st := styTab
		if tab(i) == m.tab {
			st = styTabActive
		}
		tabs = append(tabs, st.Render(fmt.Sprintf("%d %s", i+1, name)))
	}
	b.WriteString(styTabBar.Render(lipgloss.JoinHorizontal(lipgloss.Top, tabs...)))
	b.WriteString("\n\n")

	switch m.tab {
	case tabService:
		b.WriteString(m.viewService())
	case tabSubs:
		b.WriteString(m.viewSubs())
	case tabNodes:
		b.WriteString(m.viewNodes())
	case tabSettings:
		b.WriteString(m.viewSettings())
	}

	b.WriteString("\n")
	if m.inputMode != "" {
		b.WriteString("\n " + m.input.View() + "\n")
	}
	if m.flash != "" {
		st := styOK
		if m.flashE {
			st = styErr
		}
		b.WriteString("\n " + st.Render(wrap(m.flash, max(20, m.width-2))) + "\n")
	}
	b.WriteString("\n " + styDim.Render(m.helpLine()))
	return b.String()
}

func (m model) helpLine() string {
	common := "1-4/Tab 切页 · q 退出"
	if m.inputMode != "" {
		return "回车 确认 · Esc 取消"
	}
	switch m.tab {
	case tabService:
		return "s 启动/停止 · r 重启 · g 生成配置 · i 下载内核 · d Dashboard 地址 · " + common
	case tabSubs:
		return "a 添加 · u 更新选中 · U 全部更新 · x 删除 · j/k 移动 · " + common
	case tabNodes:
		return "回车 切换出口 · A 自动测速 · a 添加 · x 删除(手动) · j/k 移动 · " + common
	case tabSettings:
		return "回车/空格 修改 · g 生成配置 · r 重启 · j/k 移动 · " + common
	}
	return common
}

func (m model) viewService() string {
	var b strings.Builder
	b.WriteString(styBody.Render(styTitle.Render("状态")) + "\n")
	status := m.status
	if status == "" {
		status = "加载中…"
	}
	for _, line := range strings.Split(strings.TrimRight(status, "\n"), "\n") {
		b.WriteString(styBody.Render(" "+line) + "\n")
	}
	b.WriteString("\n" + styBody.Render(styTitle.Render("日志尾部")) + "\n")
	tail := m.logTail
	if strings.TrimSpace(tail) == "" || strings.Contains(tail, "无法读取日志") {
		tail = "（暂无日志）"
	}
	for _, line := range strings.Split(strings.TrimRight(tail, "\n"), "\n") {
		b.WriteString(styBody.Render(" "+styDim.Render(clip(line, max(20, m.width-4)))) + "\n")
	}
	return b.String()
}

func (m model) viewSubs() string {
	subs := m.a.State.Subscriptions
	if len(subs) == 0 {
		return styBody.Render("（无订阅，按 a 添加）")
	}
	var b strings.Builder
	for i, s := range subs {
		cursor := "  "
		st := lipgloss.NewStyle()
		if i == m.subCursor {
			cursor, st = "▸ ", styCursor
		}
		info := ""
		if s.Userinfo != nil && s.Userinfo.Total > 0 {
			used := s.Userinfo.Upload + s.Userinfo.Download
			info = fmt.Sprintf(" · %s/%s", sub.HumanBytes(used), sub.HumanBytes(s.Userinfo.Total))
			if s.Userinfo.Expire > 0 {
				info += " · 到期 " + time.Unix(s.Userinfo.Expire, 0).Format("2006-01-02")
			}
		}
		when := "从未更新"
		if !s.UpdatedAt.IsZero() {
			when = s.UpdatedAt.Format("01-02 15:04")
		}
		b.WriteString(styBody.Render(cursor+st.Render(pad(clip(s.Name, 14), 14))+
			fmt.Sprintf(" %3d 节点 · %s%s", len(s.Nodes), when, info)) + "\n")
		b.WriteString(styBody.Render("   "+styDim.Render(clip(s.URL, max(20, m.width-6)))) + "\n")
	}
	return b.String()
}

func (m model) viewNodes() string {
	nodes := m.a.State.AllNodes()
	if len(nodes) == 0 {
		return styBody.Render("（无节点，按 a 粘贴分享链接，或到订阅页添加订阅）")
	}
	manual := map[string]bool{}
	for _, n := range m.a.State.Manual {
		manual[n.Tag] = true
	}
	var b strings.Builder
	now := m.nowProxy
	if now == "" {
		b.WriteString(styBody.Render(styDim.Render("当前出口: （未运行或 clash_api 不可达）")) + "\n")
	} else {
		b.WriteString(styBody.Render(styDim.Render("当前出口: ")+styOK.Render(now)) + "\n")
	}
	rows := max(3, m.height-9)
	start := 0
	if m.nodeCursor >= rows {
		start = m.nodeCursor - rows + 1
	}
	for i := start; i < len(nodes) && i < start+rows; i++ {
		n := nodes[i]
		cursor := "  "
		st := lipgloss.NewStyle()
		if i == m.nodeCursor {
			cursor, st = "▸ ", styCursor
		}
		sel := "  "
		if now != "" && n.Tag == now {
			sel = styOK.Render("● ")
		}
		src := styDim.Render("订")
		if manual[n.Tag] {
			src = styOK.Render("手")
		}
		b.WriteString(styBody.Render(fmt.Sprintf("%s%s%s %s %-10v %v", cursor, sel, src,
			st.Render(pad(clip(n.Tag, 28), 28)), n.Outbound["type"], n.Outbound["server"])) + "\n")
	}
	if len(nodes) > rows {
		b.WriteString(styBody.Render(styDim.Render(fmt.Sprintf("  … %d/%d", m.nodeCursor+1, len(nodes)))) + "\n")
	}
	return b.String()
}

func (m model) viewSettings() string {
	var b strings.Builder
	for i, row := range settingRows() {
		cursor := "  "
		st := lipgloss.NewStyle()
		if i == m.setCursor {
			cursor, st = "▸ ", styCursor
		}
		val := row.get(m.a)
		if val == "" {
			val = styDim.Render("(空)")
		}
		b.WriteString(styBody.Render(cursor+st.Render(pad(clip(row.label, 44), 44))+" "+val) + "\n")
	}
	b.WriteString("\n" + styBody.Render(styDim.Render("secret: "+m.a.State.Settings.ClashSecret)) + "\n")
	return b.String()
}

// ---- small utils ----

func firstErr(errs []error) error {
	if len(errs) > 0 {
		return errs[0]
	}
	return fmt.Errorf("未知错误")
}

func okGen(msg, warn string) string {
	if warn != "" {
		return msg + "（" + warn + "）"
	}
	return msg
}

// pad right-pads s with spaces to display width n（fmt 的 %-Ns 按字符数不按
// 终端列宽，中文占两格会歪，必须用 lipgloss.Width）.
func pad(s string, n int) string {
	if d := n - lipgloss.Width(s); d > 0 {
		return s + strings.Repeat(" ", d)
	}
	return s
}

func clip(s string, n int) string {
	if lipgloss.Width(s) <= n {
		return s
	}
	r := []rune(s)
	for len(r) > 0 && lipgloss.Width(string(r)) > n-1 {
		r = r[:len(r)-1]
	}
	return string(r) + "…"
}

func wrap(s string, width int) string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		for lipgloss.Width(line) > width {
			r := []rune(line)
			cut := len(r)
			for cut > 0 && lipgloss.Width(string(r[:cut])) > width {
				cut--
			}
			out = append(out, string(r[:cut]))
			line = string(r[cut:])
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}
