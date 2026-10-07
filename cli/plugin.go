package cli

// plugin.go — niuma plugin SUBCMD：本机插件库管理。全部本地操作（扫
// ~/.niuma/plugins、读写 ~/.niuma/plugins.json、目录复制），不拨任何
// 连接——GUI 进程下一次加载 /plugins.json 时自然看到变化（衣橱贡献
// 随该请求重放，板块随窗口刷新出现）。

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/WWestC/Niuma_Studio/plugins"
)

func runPlugin(args []string) int {
	if len(args) == 0 {
		pluginUsage(os.Stderr)
		return 2
	}
	store := mustPluginStore()
	switch args[0] {
	case "list":
		return pluginList(store)
	case "enable":
		return pluginToggle(store, args[1:], true)
	case "disable":
		return pluginToggle(store, args[1:], false)
	case "install":
		return pluginInstall(store, args[1:])
	case "remove":
		return pluginRemove(store, args[1:])
	case "help", "-h", "--help":
		pluginUsage(os.Stdout)
		return 0
	default:
		fmt.Fprintf(os.Stderr, "plugin: 未知子命令 %q\n\n", args[0])
		pluginUsage(os.Stderr)
		return 2
	}
}

// mustPluginStore opens the default store; the only failure mode is a
// homeless environment, which no subcommand can work around.
func mustPluginStore() *plugins.Store {
	root, state, err := plugins.DefaultPaths()
	if err != nil {
		fmt.Fprintf(os.Stderr, "plugin: 插件目录不可用：%v\n", err)
		root, state = "", ""
	}
	return plugins.Open(root, state)
}

func pluginList(store *plugins.Store) int {
	ps := store.Load()
	if len(ps) == 0 {
		fmt.Println("没有已安装的插件（装一个试试：niuma plugin install <目录>）")
		return 0
	}
	for _, p := range ps {
		state := "停用"
		if p.Enabled {
			state = "启用"
		}
		fmt.Printf("%s  %-6s  %s\n", p.ID, state, pluginSummary(p))
		for _, prob := range p.Problems {
			fmt.Printf("    ⚠ %s\n", prob)
		}
	}
	return 0
}

func pluginSummary(p *plugins.Plugin) string {
	if p.Manifest == nil {
		return "（plugin.json 缺失或不可解析）"
	}
	m := p.Manifest
	var bits []string
	if n := len(m.Contributes.Boards); n > 0 {
		titles := make([]string, 0, n)
		for _, b := range m.Contributes.Boards {
			titles = append(titles, b.Title)
		}
		bits = append(bits, fmt.Sprintf("板块 %s", strings.Join(titles, "/")))
	}
	if n := len(m.Contributes.Wardrobe); n > 0 {
		bits = append(bits, fmt.Sprintf("衣橱×%d", n))
	}
	desc := m.Name
	if m.Desc != "" {
		desc += "—" + m.Desc
	}
	if len(bits) > 0 {
		desc += "（" + strings.Join(bits, "，") + "）"
	}
	return desc
}

func pluginToggle(store *plugins.Store, args []string, on bool) int {
	fs := flag.NewFlagSet("plugin toggle", flag.ContinueOnError)
	_ = fs.Parse(args)
	rest := fs.Args()
	if len(rest) != 1 {
		fmt.Fprintln(os.Stderr, "用法：niuma plugin enable|disable <publisher.name>")
		return 2
	}
	if err := store.SetEnabled(rest[0], on); err != nil {
		fmt.Fprintf(os.Stderr, "plugin: %v\n", err)
		return 1
	}
	verb := "已启用"
	if !on {
		verb = "已停用"
	}
	fmt.Printf("%s %s（刷新工作室窗口生效）\n", verb, rest[0])
	return 0
}

func pluginInstall(store *plugins.Store, args []string) int {
	fs := flag.NewFlagSet("plugin install", flag.ContinueOnError)
	force := fs.Bool("force", false, "覆盖已安装的同名插件")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	rest := fs.Args()
	if len(rest) != 1 {
		fmt.Fprintln(os.Stderr, "用法：niuma plugin install <插件目录> [--force]")
		return 2
	}
	id, err := store.Install(rest[0], *force)
	if err != nil {
		fmt.Fprintf(os.Stderr, "plugin: %v\n", err)
		return 1
	}
	fmt.Printf("已安装 %s（默认启用；刷新工作室窗口生效）\n", id)
	return 0
}

func pluginRemove(store *plugins.Store, args []string) int {
	fs := flag.NewFlagSet("plugin remove", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	rest := fs.Args()
	if len(rest) != 1 {
		fmt.Fprintln(os.Stderr, "用法：niuma plugin remove <publisher.name>")
		return 2
	}
	if err := store.Remove(rest[0]); err != nil {
		fmt.Fprintf(os.Stderr, "plugin: %v\n", err)
		return 1
	}
	fmt.Printf("已卸载 %s（刷新工作室窗口生效）\n", rest[0])
	return 0
}

func pluginUsage(w io.Writer) {
	fmt.Fprint(w, `plugin SUBCMD — 插件管理（本地操作，不拨连接）：
  plugin list                     列出已安装插件与问题
  plugin install <目录> [--force] 从一个解开的插件目录安装
  plugin enable <publisher.name>  启用
  plugin disable <publisher.name> 停用
  plugin remove <publisher.name>  卸载（删除目录）
`)
}
