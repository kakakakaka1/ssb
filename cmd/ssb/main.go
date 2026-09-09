// ssb — Linux / Windows 终端里的 sing-box 客户端管理器：
// 解析节点/订阅链接 → 生成 TUN+FakeIP+官方 API 服务的 config.json → 托管 sing-box 进程。
// 无参数进入 TUI；子命令供脚本/cron 使用。
package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"ssb/internal/app"
	"ssb/internal/sbx"
	"ssb/internal/sub"
	"ssb/internal/tui"
)

// version 由 release 构建注入：-ldflags "-X main.version=..."
var version = "dev"

const usage = `ssb %s — sing-box 节点/订阅管理器（TUN + FakeIP + 官方 Dashboard）

用法: ssb [--dir 目录] [命令]

  (无命令)              进入 TUI
  add <链接...>         添加节点链接（vless/anytls/ss/vmess/trojan/hy2/tuic，可多个，也可从 stdin 管道输入）
  sub add <URL> [名称]  添加订阅并立即拉取
  sub update [名称]     更新订阅（缺省全部）
  sub rm <名称>         删除订阅
  sub ls                列出订阅
  nodes                 列出全部节点
  nodes rm <tag>        删除手动节点
  gen                   重新生成 config.json（自动 sing-box check）
  install               下载 sing-box 到 data/（不装入系统；也可自行复制到 data/sing-box）
  start|stop|restart    启停 sing-box（TUN 需要 root / 管理员权限，见 doctor）
  run-core              前台运行：生成配置后在前台跑 sing-box（Docker/调试用）
  status                运行状态
  logs [-f]             查看日志（-f 跟随）
  dashboard             打印 Dashboard 地址与 secret
  doctor                环境体检
  version               版本

环境变量: SSB_DIR 指定工作目录（默认：可执行文件所在目录）
`

func main() {
	args := os.Args[1:]
	dir := ""
	var rest []string
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--dir" && i+1 < len(args):
			dir = args[i+1]
			i++
		case strings.HasPrefix(args[i], "--dir="):
			dir = strings.TrimPrefix(args[i], "--dir=")
		default:
			rest = append(rest, args[i])
		}
	}

	a, err := app.Open(app.ResolveBase(dir))
	if err != nil {
		die(err)
	}

	if len(rest) == 0 {
		if err := tui.Run(a); err != nil {
			die(err)
		}
		return
	}

	ctx := context.Background()
	cmd, rest := rest[0], rest[1:]
	switch cmd {
	case "add":
		text := strings.Join(rest, "\n")
		if text == "" {
			text = readStdin()
		}
		if text == "" {
			die(fmt.Errorf("请提供链接参数或从 stdin 输入"))
		}
		n, errs := a.AddNodes(text)
		for _, e := range errs {
			fmt.Fprintln(os.Stderr, "跳过:", e)
		}
		if n == 0 {
			os.Exit(1)
		}
		fmt.Printf("已添加 %d 个节点\n", n)
		regen(a)

	case "sub":
		if len(rest) == 0 {
			die(fmt.Errorf("用法: ssb sub add|update|rm|ls"))
		}
		sc, rest := rest[0], rest[1:]
		switch sc {
		case "add":
			if len(rest) < 1 {
				die(fmt.Errorf("用法: ssb sub add <URL> [名称]"))
			}
			name := ""
			if len(rest) > 1 {
				name = rest[1]
			}
			s, err := a.SubAdd(ctx, name, rest[0])
			if err != nil {
				die(err)
			}
			fmt.Printf("✓ %s: %d 个节点 (%s)\n", s.Name, len(s.Nodes), s.Format)
			regen(a)
		case "update":
			name := ""
			if len(rest) > 0 {
				name = rest[0]
			}
			report, err := a.SubUpdate(ctx, name)
			for _, line := range report {
				fmt.Println(line)
			}
			if err != nil {
				os.Exit(1)
			}
			regen(a)
		case "rm":
			if len(rest) < 1 {
				die(fmt.Errorf("用法: ssb sub rm <名称>"))
			}
			if err := a.SubRemove(rest[0]); err != nil {
				die(err)
			}
			fmt.Println("已删除")
			regen(a)
		case "ls":
			if len(a.State.Subscriptions) == 0 {
				fmt.Println("（无订阅）")
				return
			}
			for _, s := range a.State.Subscriptions {
				info := ""
				if s.Userinfo != nil && s.Userinfo.Total > 0 {
					info = fmt.Sprintf("  流量 %s/%s", sub.HumanBytes(s.Userinfo.Upload+s.Userinfo.Download), sub.HumanBytes(s.Userinfo.Total))
				}
				fmt.Printf("%-16s %3d 节点  更新于 %s%s\n  %s\n", s.Name, len(s.Nodes), fmtTime(s.UpdatedAt), info, s.URL)
			}
		default:
			die(fmt.Errorf("未知子命令 sub %s", sc))
		}

	case "nodes":
		if len(rest) >= 2 && rest[0] == "rm" {
			if err := a.RemoveManual(rest[1]); err != nil {
				die(err)
			}
			fmt.Println("已删除")
			regen(a)
			return
		}
		nodes := a.State.AllNodes()
		if len(nodes) == 0 {
			fmt.Println("（无节点，用 ssb add 或 ssb sub add 添加）")
			return
		}
		manual := map[string]bool{}
		for _, n := range a.State.Manual {
			manual[n.Tag] = true
		}
		for _, n := range nodes {
			src := " "
			if manual[n.Tag] {
				src = "M"
			}
			fmt.Printf("%s %-30s %-12v %v\n", src, n.Tag, n.Outbound["type"], n.Outbound["server"])
		}

	case "gen":
		regen(a)

	case "install":
		msg, err := a.Install(ctx)
		if err != nil {
			die(err)
		}
		fmt.Println(msg)
		if v, verr := sbx.Version(a.Dirs.SingboxBin()); verr == nil {
			fmt.Println("版本:", v)
		}

	case "start":
		if err := a.Start(ctx); err != nil {
			die(err)
		}
		fmt.Println("已启动")
		if a.State.Settings.DashboardOff {
			fmt.Println("Dashboard 已关闭（TUI 节点页可直接切换节点）")
		} else {
			fmt.Println("Dashboard:", a.DashboardURL())
			fmt.Println("secret   :", a.State.Settings.APISecret)
		}

	case "stop":
		if err := a.Stop(); err != nil {
			die(err)
		}
		fmt.Println("已停止")

	case "restart":
		if err := a.Restart(ctx); err != nil {
			die(err)
		}
		fmt.Println("已重启")

	case "run-core": // 前台运行（Docker/调试）：gen + exec sing-box
		if err := a.RunCore(ctx); err != nil {
			die(err)
		}

	case "status":
		fmt.Print(a.StatusText())

	case "logs":
		follow := len(rest) > 0 && rest[0] == "-f"
		printLogs(a, follow)

	case "dashboard":
		if a.State.Settings.DashboardOff {
			fmt.Println("Dashboard 网页面板已在设置中关闭（TUI 节点页可直接切换节点）")
			break
		}
		fmt.Println("地址  :", a.DashboardURL())
		fmt.Println("secret:", a.State.Settings.APISecret)
		fmt.Println("（官方 sing-box Dashboard 由 API 服务托管；首次打开按提示填入 secret）")

	case "doctor":
		fail := false
		for _, c := range sbx.Doctor(a.Dirs, a.State) {
			mark := "✓"
			if !c.OK {
				mark, fail = "✗", true
			}
			fmt.Printf("%s %s: %s\n", mark, c.Name, c.Detail)
		}
		if fail {
			os.Exit(1)
		}

	case "version":
		fmt.Println("ssb", version)

	case "help", "-h", "--help":
		fmt.Printf(usage, version)

	default:
		fmt.Fprintf(os.Stderr, "未知命令 %q\n\n", cmd)
		fmt.Printf(usage, version)
		os.Exit(1)
	}
}

func regen(a *app.App) {
	warn, err := a.Generate()
	if err != nil {
		die(err)
	}
	if warn != "" {
		fmt.Fprintln(os.Stderr, "警告:", warn)
	}
	fmt.Println("config.json 已生成并通过校验:", a.Dirs.ConfigFile())
	if _, ok := sbx.Running(a.Dirs); ok {
		fmt.Printf("提示: sing-box 正在运行，执行 %s restart 使新配置生效\n", sbx.Self)
	}
}

func printLogs(a *app.App, follow bool) {
	f, err := os.Open(a.Dirs.LogFile())
	if err != nil {
		die(fmt.Errorf("暂无日志: %w", err))
	}
	defer f.Close()
	if !follow {
		io.Copy(os.Stdout, f)
		return
	}
	// 简易 tail -f
	io.Copy(os.Stdout, f)
	for {
		time.Sleep(500 * time.Millisecond)
		io.Copy(os.Stdout, f)
	}
}

func readStdin() string {
	stat, err := os.Stdin.Stat()
	if err != nil || (stat.Mode()&os.ModeCharDevice) != 0 {
		return ""
	}
	b, _ := io.ReadAll(bufio.NewReader(os.Stdin))
	return string(b)
}

func fmtTime(t time.Time) string {
	if t.IsZero() {
		return "从未"
	}
	return t.Format("2006-01-02 15:04")
}

func die(err error) {
	fmt.Fprintln(os.Stderr, "错误:", err)
	os.Exit(1)
}
