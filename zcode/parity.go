package zcode

// parity.go — the bridge's network parity with the ZCode desktop:
// injected as env ONLY when the desktop itself spawns its agent
// (services/runtime-tools/agentProxyEnv.ts) — a bridge we spawn
// ourselves inherits none of it, and a proxy-requiring machine splits
// exactly the way we kept hitting (desktop chats fine, the bridge's
// first model call black-holes); reading the same settings file and
// applying the same patch means: wherever the desktop works, the
// bridge works.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// desktopParityEnv translates the desktop settings' proxy block into
// the spawn-time env patch the desktop itself applies: uppercase proxy
// trio + ZCODE_HTTP_PROXY, the NO_PROXY pair, NODE_EXTRA_CA_CERTS +
// ZCODE_AGENT_CA_CERT. Appended last so it wins over inherited values
// (the desktop's settings-page-over-shell precedence). nil when no
// settings or no proxy block.
func desktopParityEnv() []string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return nil
	}
	data, err := os.ReadFile(filepath.Join(home, ".zcode", "v2", "setting.json"))
	if err != nil {
		return nil
	}
	var s struct {
		HTTPProxy  string `json:"httpProxy"`
		NoProxy    string `json:"httpProxyNoProxy"`
		CaCertPath string `json:"httpProxyCaCertPath"`
	}
	if json.Unmarshal(data, &s) != nil {
		return nil
	}
	var env []string
	if p := normalizeProxyValue(s.HTTPProxy); p != "" {
		for _, k := range []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "ZCODE_HTTP_PROXY"} {
			env = append(env, k+"="+p)
		}
	}
	if np := normalizeNoProxyValue(s.NoProxy); np != "" {
		for _, k := range []string{"NO_PROXY", "no_proxy", "ZCODE_NO_PROXY"} {
			env = append(env, k+"="+np)
		}
	}
	if ca := strings.TrimSpace(s.CaCertPath); ca != "" {
		env = append(env, "NODE_EXTRA_CA_CERTS="+ca, "ZCODE_AGENT_CA_CERT="+ca)
	}
	return env
}

var proxyScheme = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9+.-]*://`)

// normalizeProxyValue keeps a schemed value as-is and prefixes a bare
// host:port with http:// — the desktop's own rule, verbatim.
func normalizeProxyValue(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return ""
	}
	if proxyScheme.MatchString(v) {
		return v
	}
	return "http://" + v
}

// normalizeNoProxyValue trims empty tokens and re-joins — verbatim.
func normalizeNoProxyValue(v string) string {
	tokens := strings.Split(v, ",")
	kept := make([]string, 0, len(tokens))
	for _, t := range tokens {
		if t = strings.TrimSpace(t); t != "" {
			kept = append(kept, t)
		}
	}
	return strings.Join(kept, ",")
}
