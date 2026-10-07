// extlink — 外链跳系统默认浏览器。对话/公告/文档正文里的链接
// （markdown .mlink、/media 附件、/kb 文档等）在 webview 壳里没有
// 新窗口面：target=_blank 无处可去，页内跳转则会顶掉整个工作台。
// 页面经 openExternal 绑定把链接交到这里，按平台唤起默认处理器——
// 调用即刻返回（Start＋后台收尸），主线程不等浏览器起程。
// 自 shell 包外迁（shell 行数预算 520 装不下这 44 行；本包零内部
// 依赖，两侧壳面同约绑定）。
package extlink

import (
	"errors"
	"net/url"
	"os/exec"
	"runtime"
)

// 仅放行 http/https/mailto。链接出自聊天正文（任何成员的消息都可能
// 带一条），file:/javascript:/自定义 scheme 一律在此拒之门外——桥是
// 页面可达的，白名单必须在桥这一侧把住。
var errExternalScheme = errors.New("openExternal: 仅支持 http/https/mailto 链接")

// OpenExternal hands one allowlisted URL to the OS default handler
// (the user's browser for web links, the mail client for mailto).
func OpenExternal(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return err
	}
	if u.Scheme != "http" && u.Scheme != "https" && u.Scheme != "mailto" {
		return errExternalScheme
	}
	var c *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		c = exec.Command("open", u.String())
	case "windows":
		c = exec.Command("rundll32", "url.dll,FileProtocolHandler", u.String())
	default:
		c = exec.Command("xdg-open", u.String())
	}
	if err := c.Start(); err != nil {
		return err
	}
	go func() { _ = c.Wait() }() // 收尸不拦线程（xdg-open 偶发不退）
	return nil
}
