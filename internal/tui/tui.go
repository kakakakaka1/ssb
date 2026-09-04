// Package tui is the interactive terminal UI: 状态 / 订阅 / 节点 / 设置 四页签。
//
// 交互约定（全页统一）：
//   - s 启动/停止、r 应用（生成配置并重启）、g 只生成配置、q 退出，任何页都能按；
//   - 列表页 j/k 移动，回车操作当前行，a 添加，x 删除（会二次确认）；
//   - 任何会改动配置的操作完成后自动重新生成 config.json；服务在运行时
//     顶栏出现「未应用」提示，按 r 重启生效，不用再手动 g + r。
package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"ssb/internal/app"
	"ssb/internal/sbx"
)

type tab int

const (
	tabStatus tab = iota
	tabSubs
	tabNodes
	tabSettings
	tabCount
)

var tabNames = [tabCount]string{"状态", "订阅", "节点", "设置"}

// ---- messages ----

// tickMsg 是后台每 3 秒刷新一次的运行态快照。
type tickMsg struct {
	running  bool
	pid      int
	version  string // 内核版本，找不到内核时为空
	apiAlive bool
	now      string // PROXY 选择器当前出口（clash_api 不可达时为空）
	logTail  string
}

// opDoneMsg 是后台操作的结果。changed=true 表示配置已重新生成，
// 服务在跑的话需要重启才生效。
type opDoneMsg struct {
	flash   string
	isErr   bool
	changed bool
}

type mode int

const (
	modeNormal  mode = iota
	modeInput        // 底栏文本输入
	modeConfirm      // 底栏 y/N 确认
)

// inputKind 区分 modeInput 下回车要做什么。
type inputKind int

const (
	inputAddLink inputKind = iota
	inputAddSub
	inputEditSetting
)

type model struct {
	a             *app.App
	tab           tab
	width, height int

	tick    tickMsg
	loaded  bool // 收到过第一次 tick
	busy    bool
	spin    spinner.Model
	flash   string
	flashE  bool
	dirty   bool // 配置改过但服务没重启
	help    bool // ? 全屏快捷键表
	cursor  [tabCount]int
	logLine int // 状态页日志向上翻了几行（0 = 贴底）

	mode      mode
	input     textinput.Model
	inKind    inputKind
	editRow   *settingRow
	confirmQ  string
	confirmDo func() (string, error)
}

// Run starts the TUI.
func Run(a *app.App) error {
	ti := textinput.New()
	ti.CharLimit = 4096
	ti.Prompt = "› "
	sp := spinner.New(spinner.WithSpinner(spinner.MiniDot))
	sp.Style = styAccent
	m := model{a: a, input: ti, spin: sp}
	_, err := tea.NewProgram(m, tea.WithAltScreen()).Run()
	return err
}

func (m model) Init() tea.Cmd { return tea.Batch(refreshCmd(m.a), tickCmd(m.a)) }

func snapshot(a *app.App) tickMsg {
	t := tickMsg{logTail: sbx.Tail(a.Dirs, 60)}
	t.pid, t.running = sbx.Running(a.Dirs)
	if bin, err := sbx.Locate(a.Dirs, &a.State.Settings); err == nil {
		t.version, _ = sbx.Version(bin)
	}
	s := a.State.Settings
	t.apiAlive = sbx.APIAlive(s.ClashListen, s.ClashSecret)
	if t.apiAlive {
		t.now = a.SelectedNode()
	}
	return t
}

func refreshCmd(a *app.App) tea.Cmd {
	return func() tea.Msg { return snapshot(a) }
}

func tickCmd(a *app.App) tea.Cmd {
	return tea.Tick(3*time.Second, func(time.Time) tea.Msg { return snapshot(a) })
}

// ---- update ----

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.input.Width = max(20, m.width-6)
		return m, nil

	case tickMsg:
		wasRunning := m.tick.running
		m.tick, m.loaded = msg, true
		if wasRunning && !msg.running {
			m.dirty = false // 停了就谈不上"未应用"
		}
		return m, tickCmd(m.a)

	case spinner.TickMsg:
		if !m.busy {
			return m, nil
		}
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd

	case opDoneMsg:
		m.busy = false
		m.flash, m.flashE = msg.flash, msg.isErr
		if msg.changed && m.tick.running {
			m.dirty = true
		}
		return m, refreshCmd(m.a)

	case tea.KeyMsg:
		switch m.mode {
		case modeInput:
			return m.updateInput(msg)
		case modeConfirm:
			return m.updateConfirm(msg)
		}
		return m.updateKeys(msg)
	}
	return m, nil
}

func (m model) updateKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()

	if m.help {
		if key == "?" || key == "esc" || key == "q" || key == "enter" {
			m.help = false
		}
		return m, nil
	}

	// 全局键：任何页面、任何时候
	switch key {
	case "ctrl+c":
		return m, tea.Quit
	case "q":
		return m, tea.Quit
	case "?":
		m.help = true
		return m, nil
	case "tab", "right", "l":
		m.tab = (m.tab + 1) % tabCount
		return m, nil
	case "shift+tab", "left", "h":
		m.tab = (m.tab + tabCount - 1) % tabCount
		return m, nil
	case "1", "2", "3", "4":
		m.tab = tab(key[0] - '1')
		return m, nil
	}
	if m.busy {
		return m, nil
	}
	switch key {
	case "s":
		if m.tick.running {
			return m.startOp("停止中", func() (string, error) {
				if err := m.a.Stop(); err != nil {
					return "", err
				}
				return "已停止", nil
			})
		}
		return m.startOp("启动中", func() (string, error) {
			if err := m.a.Start(context.Background()); err != nil {
				return "", err
			}
			return "已启动", nil
		})
	case "r":
		m.dirty = false
		return m.startOp("重启中", func() (string, error) {
			if err := m.a.Restart(context.Background()); err != nil {
				return "", err
			}
			return "已重启，配置已生效", nil
		})
	case "g":
		return m.startOp("生成配置", func() (string, error) {
			return m.regen("config.json 已生成并通过校验")
		})
	case "j", "down":
		return m.move(+1), nil
	case "k", "up":
		return m.move(-1), nil
	case "G", "end":
		return m.move(1 << 20), nil
	case "home":
		return m.move(-(1 << 20)), nil
	case "pgdown", "ctrl+d":
		return m.move(m.bodyRows() / 2), nil
	case "pgup", "ctrl+u":
		return m.move(-m.bodyRows() / 2), nil
	}

	switch m.tab {
	case tabStatus:
		return m.keysStatus(key)
	case tabSubs:
		return m.keysSubs(key)
	case tabNodes:
		return m.keysNodes(key)
	case tabSettings:
		return m.keysSettings(key)
	}
	return m, nil
}

// listLen 是当前页可移动光标的行数（状态页用日志行数做翻页）。
func (m model) listLen() int {
	switch m.tab {
	case tabStatus:
		return 0
	case tabSubs:
		return len(m.a.State.Subscriptions)
	case tabNodes:
		return len(m.a.State.AllNodes()) + 1 // 首行是 auto
	case tabSettings:
		return len(settingRows(m.a))
	}
	return 0
}

func (m model) move(d int) model {
	if m.tab == tabStatus {
		// 状态页：j/k 翻日志
		lines := strings.Count(strings.TrimRight(m.tick.logTail, "\n"), "\n") + 1
		m.logLine = clamp(m.logLine-d, 0, max(0, lines-m.logRows()))
		return m
	}
	n := m.listLen()
	if n == 0 {
		m.cursor[m.tab] = 0
		return m
	}
	m.cursor[m.tab] = clamp(m.cursor[m.tab]+d, 0, n-1)
	return m
}

// clampCursors 在列表长度变化（删除/收起高级设置）后把光标拉回范围内。
func (m model) clampCursors() model {
	saved := m.tab
	for t := tab(0); t < tabCount; t++ {
		m.tab = t
		m = m.move(0)
	}
	m.tab = saved
	return m
}

// ---- ops ----

func (m model) startOp(label string, f func() (string, error)) (tea.Model, tea.Cmd) {
	return m.startOpChanged(label, false, f)
}

// startOpChanged 跑一个后台操作；changed 表示它会重新生成配置。
func (m model) startOpChanged(label string, changed bool, f func() (string, error)) (tea.Model, tea.Cmd) {
	m.busy = true
	m.flash, m.flashE = label+"…", false
	cmd := func() tea.Msg {
		msg, err := f()
		if err != nil {
			return opDoneMsg{flash: err.Error(), isErr: true, changed: changed}
		}
		return opDoneMsg{flash: msg, changed: changed}
	}
	return m, tea.Batch(cmd, m.spin.Tick)
}

// regen 重新生成配置，把 check 告警拼进成功提示。
func (m model) regen(okMsg string) (string, error) {
	warn, err := m.a.Generate()
	if err != nil {
		return "", err
	}
	if warn != "" {
		return okMsg + "（" + warn + "）", nil
	}
	return okMsg, nil
}

// changeAndRegen 是"改数据 → 保存 → 重新生成"的统一路径。
func (m model) changeAndRegen(label string, change func() (string, error)) (tea.Model, tea.Cmd) {
	return m.startOpChanged(label, true, func() (string, error) {
		msg, err := change()
		if err != nil {
			return "", err
		}
		return m.regen(msg)
	})
}

func (m model) prompt(kind inputKind, placeholder, value string) (tea.Model, tea.Cmd) {
	m.mode, m.inKind = modeInput, kind
	m.input.Placeholder = placeholder
	m.input.SetValue(value)
	m.input.CursorEnd()
	m.input.Focus()
	return m, textinput.Blink
}

func (m model) confirm(q string, do func() (string, error)) (tea.Model, tea.Cmd) {
	m.mode, m.confirmQ, m.confirmDo = modeConfirm, q, do
	return m, nil
}

func (m model) updateConfirm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	do := m.confirmDo
	m.mode, m.confirmQ, m.confirmDo = modeNormal, "", nil
	switch msg.String() {
	case "y", "Y", "enter":
		mm, cmd := m.changeAndRegen("处理中", do)
		return mm.(model).clampCursors(), cmd
	}
	m.flash, m.flashE = "已取消", false
	return m, nil
}

func (m model) updateInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc, tea.KeyCtrlC:
		m.mode, m.editRow = modeNormal, nil
		m.input.Blur()
		m.flash, m.flashE = "已取消", false
		return m, nil
	case tea.KeyEnter:
		val := strings.TrimSpace(m.input.Value())
		m.mode = modeNormal
		m.input.Blur()
		m.input.SetValue("")
		switch m.inKind {
		case inputAddLink:
			if val == "" {
				return m, nil
			}
			return m.changeAndRegen("解析链接", func() (string, error) {
				n, errs := m.a.AddNodes(val)
				if n == 0 {
					return "", firstErr(errs)
				}
				msg := fmt.Sprintf("已添加 %d 个节点", n)
				if len(errs) > 0 {
					msg += fmt.Sprintf("，%d 条解析失败：%v", len(errs), errs[0])
				}
				return msg, nil
			})
		case inputAddSub:
			if val == "" {
				return m, nil
			}
			// "URL 名称" 一行搞定；名称可省略
			url, name := val, ""
			if i := strings.IndexAny(val, " \t"); i > 0 {
				url, name = val[:i], strings.TrimSpace(val[i:])
			}
			return m.changeAndRegen("拉取订阅", func() (string, error) {
				s, err := m.a.SubAdd(context.Background(), name, url)
				if err != nil {
					return "", err
				}
				return fmt.Sprintf("已添加 %s：%d 个节点（%s）", s.Name, len(s.Nodes), s.Format), nil
			})
		case inputEditSetting:
			row := m.editRow
			m.editRow = nil
			if row == nil {
				return m, nil
			}
			return m.applySetting(row, val)
		}
		return m, nil
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

// applySetting 写入一项设置，保存并重新生成配置。
func (m model) applySetting(row *settingRow, val string) (tea.Model, tea.Cmd) {
	if err := row.set(m.a, val); err != nil {
		m.flash, m.flashE = err.Error(), true
		return m, nil
	}
	if err := m.a.Save(); err != nil {
		m.flash, m.flashE = err.Error(), true
		return m, nil
	}
	m = m.clampCursors()
	return m.changeAndRegen("保存并生成", func() (string, error) {
		return row.label + " = " + valueOrEmpty(row.get(m.a)), nil
	})
}

// ---- per-tab keys ----

func (m model) keysStatus(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "i":
		return m.startOp("下载内核", func() (string, error) { return m.a.Install(context.Background()) })
	}
	return m, nil
}

func (m model) keysSubs(key string) (tea.Model, tea.Cmd) {
	subs := m.a.State.Subscriptions
	cur := m.cursor[tabSubs]
	switch key {
	case "a":
		return m.prompt(inputAddSub, "订阅链接，可在后面空格加名称：https://… 机场A", "")
	case "enter", "u":
		if len(subs) == 0 {
			return m, nil
		}
		name := subs[cur].Name
		return m.changeAndRegen("更新 "+name, func() (string, error) {
			report, err := m.a.SubUpdate(context.Background(), name)
			if err != nil {
				return "", fmt.Errorf("%s", strings.Join(report, " / "))
			}
			return strings.Join(report, " / "), nil
		})
	case "U":
		if len(subs) == 0 {
			return m, nil
		}
		return m.changeAndRegen("更新全部订阅", func() (string, error) {
			report, _ := m.a.SubUpdate(context.Background(), "")
			return strings.Join(report, " / "), nil
		})
	case "x", "delete":
		if len(subs) == 0 {
			return m, nil
		}
		name := subs[cur].Name
		return m.confirm(fmt.Sprintf("删除订阅 %s（含其 %d 个节点）？", name, len(subs[cur].Nodes)),
			func() (string, error) {
				if err := m.a.SubRemove(name); err != nil {
					return "", err
				}
				return "已删除 " + name, nil
			})
	}
	return m, nil
}

func (m model) keysNodes(key string) (tea.Model, tea.Cmd) {
	nodes := m.a.State.AllNodes()
	cur := m.cursor[tabNodes] // 0 = auto，其后是节点
	switch key {
	case "a":
		return m.prompt(inputAddLink, "粘贴分享链接（多条用空格分隔，也可以是 base64）", "")
	case "enter", " ":
		if !m.tick.running {
			m.flash, m.flashE = "服务未运行，先按 s 启动", true
			return m, nil
		}
		tag := "auto"
		if cur > 0 && cur-1 < len(nodes) {
			tag = nodes[cur-1].Tag
		}
		return m.startOp("切换出口", func() (string, error) {
			if err := m.a.SelectNode(tag); err != nil {
				return "", err
			}
			return "出口 → " + tag, nil
		})
	case "x", "delete":
		if cur == 0 || cur-1 >= len(nodes) {
			return m, nil
		}
		n := nodes[cur-1]
		if !m.isManual(n.Tag) {
			m.flash, m.flashE = "这是订阅节点，请到订阅页删除整个订阅", true
			return m, nil
		}
		return m.confirm("删除节点 "+n.Tag+"？", func() (string, error) {
			if err := m.a.RemoveManual(n.Tag); err != nil {
				return "", err
			}
			return "已删除 " + n.Tag, nil
		})
	}
	return m, nil
}

func (m model) isManual(tag string) bool {
	for _, n := range m.a.State.Manual {
		if n.Tag == tag {
			return true
		}
	}
	return false
}

func (m model) keysSettings(key string) (tea.Model, tea.Cmd) {
	rows := settingRows(m.a)
	cur := m.cursor[tabSettings]
	if cur >= len(rows) {
		return m, nil
	}
	row := rows[cur]
	switch key {
	case "enter", " ", "e":
		if len(row.opts) > 0 { // 枚举：轮转到下一个
			return m.applySetting(&row, nextOpt(row.opts, row.get(m.a), +1))
		}
		m.editRow = &rows[cur]
		return m.prompt(inputEditSetting, row.label, row.get(m.a))
	case "backspace":
		if len(row.opts) > 0 {
			return m.applySetting(&row, nextOpt(row.opts, row.get(m.a), -1))
		}
	}
	return m, nil
}

// ---- small utils ----

func firstErr(errs []error) error {
	if len(errs) > 0 {
		return errs[0]
	}
	return fmt.Errorf("未知错误")
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
