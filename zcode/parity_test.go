package zcode

// parity_test.go — pins the bridge's network parity with the ZCode
// desktop: the settings-page proxy block becomes the same env patch
// the desktop injects when IT spawns the agent, with the same
// normalization rules (scheme handling, no-proxy token trimming).

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNormalizeProxyValue(t *testing.T) {
	cases := map[string]string{
		"proxy.local:8080":    "http://proxy.local:8080",
		"socks5://h:1080":     "socks5://h:1080",
		"  http://a.b:3128  ": "http://a.b:3128",
		"":                    "",
		"   ":                 "",
	}
	for in, want := range cases {
		if got := normalizeProxyValue(in); got != want {
			t.Fatalf("normalizeProxyValue(%q): want %q, got %q", in, want, got)
		}
	}
}

func TestNormalizeNoProxyValue(t *testing.T) {
	if got := normalizeNoProxyValue(" a.b , ,c.d ,"); got != "a.b,c.d" {
		t.Fatalf("token trim: got %q", got)
	}
	if got := normalizeNoProxyValue(" , "); got != "" {
		t.Fatalf("all-empty must fold to empty, got %q", got)
	}
}

// TestDesktopParityEnv pins the whole patch: proxy → the uppercase trio
// plus ZCODE_HTTP_PROXY; no-proxy → the pair; CA → the node + agent
// pair; and the empty cases (no file / no block) answer nil.
func TestDesktopParityEnv(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	if got := desktopParityEnv(); got != nil {
		t.Fatalf("no settings file must answer nil, got %v", got)
	}
	dir := filepath.Join(home, ".zcode", "v2")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "setting.json"),
		[]byte(`{"httpProxy":"proxy.corp:3128","httpProxyNoProxy":" a.b , c.d ","httpProxyCaCertPath":"/certs/ca.pem","theme":"dark"}`),
		0o644); err != nil {
		t.Fatal(err)
	}
	env := desktopParityEnv()
	joined := strings.Join(env, "\n")
	for _, want := range []string{
		"HTTP_PROXY=http://proxy.corp:3128",
		"HTTPS_PROXY=http://proxy.corp:3128",
		"ZCODE_HTTP_PROXY=http://proxy.corp:3128",
		"NO_PROXY=a.b,c.d",
		"ZCODE_NO_PROXY=a.b,c.d",
		"NODE_EXTRA_CA_CERTS=/certs/ca.pem",
		"ZCODE_AGENT_CA_CERT=/certs/ca.pem",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("patch missing %q in %v", want, env)
		}
	}
	// no proxy block → nil patch（未配代理的机器零影响）
	if err := os.WriteFile(filepath.Join(dir, "setting.json"), []byte(`{"theme":"dark"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := desktopParityEnv(); got != nil {
		t.Fatalf("no proxy block must answer nil, got %v", got)
	}
}
