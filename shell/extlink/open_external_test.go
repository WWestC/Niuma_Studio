package extlink

// open_external_test.go — OpenExternal 的白名单门：链接出自聊天正文
// （任何成员的消息都可能带一条），scheme 白名单必须在桥这一侧把住。
// 只测拒绝路径——放行路径会真唤起默认浏览器（open/xdg-open），那是
// 用户的桌面，不是测试场。

import "testing"

func TestOpenExternalRejectsNonBrowserSchemes(t *testing.T) {
	for _, raw := range []string{
		"javascript:alert(1)", // 正文不可信文本的经典面
		"file:///etc/passwd",
		"data:text/html,<b>x</b>",
		"x-apple.systempreferences:com.apple.Notifications-Settings.extension",
		"vbscript:msgbox",
		"",        // 空串
		"////etc", // 无 scheme 的协议相对面
		"://no-scheme",
	} {
		if err := OpenExternal(raw); err == nil {
			t.Errorf("OpenExternal(%q) = nil error, want scheme gate rejection", raw)
		}
	}
}
