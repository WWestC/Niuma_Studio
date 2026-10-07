// MCPServer: the studio's model-context asset — one http/sse MCP
// server a member's session can be born with. Assembled servers ride
// session/create's mcpServers override, which REPLACES the member's
// default MCP fleet for that session — the manual says so in prose.
// stdio servers are not in v1: the zcode wire only ever carried
// http/sse shapes.
package capability

import (
	"errors"
	"net/url"
	"strings"

	"github.com/WWestC/Niuma_Studio/i18n"
)

// MCPServer limits. Header values may carry tokens — list/receipt
// faces never echo them back; only the loopback detail read does.
const (
	MaxMCPs        = 16 // global library cap
	MaxMCPHeaders  = 8  // per server
	maxMCPURLRunes = 512
	maxHeaderName  = 64
	maxHeaderValue = 512
)

// MCPHeader is one request header stamped on every call to the
// server (Authorization and friends).
type MCPHeader struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// MCPServer is one registered MCP server — a global, cross-project
// asset like a skill; assemblies reference it by key.
type MCPServer struct {
	Key       string      `json:"key"`
	Name      string      `json:"name"`
	Desc      string      `json:"desc,omitempty"`
	Type      string      `json:"type"`              // http | sse
	URL       string      `json:"url"`               // http(s) endpoint
	Headers   []MCPHeader `json:"headers,omitempty"` // request headers
	CreatedBy string      `json:"created_by,omitempty"`
	CreatedTS int64       `json:"created_ts,omitempty"`
}

// Validate checks the entity's own fields: key slug, prose bounds,
// the transport vocabulary, a parseable http(s) URL and sane header
// names/values.
func (m MCPServer) Validate() error {
	if !ValidKey(m.Key) {
		return errors.New(i18n.Sf("非法 MCP 服务 key %q（[a-z0-9_-]{1,32}，创建后不可改名）", m.Key))
	}
	if n := len([]rune(m.Name)); n == 0 {
		return errors.New(i18n.S("MCP 服务名称不能为空"))
	} else if n > maxNameRunes {
		return errors.New(i18n.Sf("MCP 服务 %s 名称超长：%d rune（上限 %d）", m.Key, n, maxNameRunes))
	}
	if n := len([]rune(m.Desc)); n > maxDescRunes {
		return errors.New(i18n.Sf("MCP 服务 %s 描述超长：%d rune（上限 %d）", m.Key, n, maxDescRunes))
	}
	switch m.Type {
	case "http", "sse":
	default:
		return errors.New(i18n.Sf("MCP 服务 %s 的 type 非法: %q（http|sse）", m.Key, m.Type))
	}
	if n := len([]rune(m.URL)); n == 0 || n > maxMCPURLRunes {
		return errors.New(i18n.Sf("MCP 服务 %s 的 url 非法：1–%d rune", m.Key, maxMCPURLRunes))
	}
	u, err := url.Parse(m.URL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return errors.New(i18n.Sf("MCP 服务 %s 的 url 非法: %q（须为 http/https 绝对地址）", m.Key, m.URL))
	}
	if len(m.Headers) > MaxMCPHeaders {
		return errors.New(i18n.Sf("MCP 服务 %s 的请求头超数：%d 条（上限 %d）", m.Key, len(m.Headers), MaxMCPHeaders))
	}
	seen := make(map[string]bool, len(m.Headers))
	for _, h := range m.Headers {
		n := strings.TrimSpace(h.Name)
		if n == "" || len([]rune(n)) > maxHeaderName || !noWhitespace(n) {
			return errors.New(i18n.Sf("MCP 服务 %s 的请求头名非法: %q（1–64 rune，不含空白）", m.Key, h.Name))
		}
		if len([]rune(h.Value)) > maxHeaderValue {
			return errors.New(i18n.Sf("MCP 服务 %s 的请求头 %s 值超长（上限 %d rune）", m.Key, n, maxHeaderValue))
		}
		if seen[strings.ToLower(n)] {
			return errors.New(i18n.Sf("MCP 服务 %s 的请求头 %s 重复（大小写不敏感）", m.Key, n))
		}
		seen[strings.ToLower(n)] = true
	}
	return nil
}

// sanitize clamps the prose fields, trims the URL and header
// names/values, and drops blank header rows — identifiers
// validate-or-reject, display text clamps, house style. The key is
// never rewritten.
func (m MCPServer) sanitize() MCPServer {
	m.Name = clampRunes(strings.TrimSpace(m.Name), maxNameRunes)
	m.Desc = clampRunes(strings.TrimSpace(m.Desc), maxDescRunes)
	m.Type = strings.TrimSpace(m.Type)
	m.URL = strings.TrimSpace(m.URL)
	if m.Headers != nil {
		hs := make([]MCPHeader, 0, len(m.Headers))
		for _, h := range m.Headers {
			h.Name = strings.TrimSpace(h.Name)
			h.Value = strings.TrimSpace(h.Value)
			if h.Name == "" {
				continue
			}
			hs = append(hs, h)
		}
		if len(hs) == 0 {
			hs = nil
		}
		m.Headers = hs
	}
	m.CreatedBy = sanitizeBy(m.CreatedBy)
	return m
}

// MaskedHeaders returns the header list with values reduced to their
// last 4 characters — enough to recognize a token in a list face,
// never enough to reuse it.
func (m MCPServer) MaskedHeaders() []MCPHeader {
	out := make([]MCPHeader, 0, len(m.Headers))
	for _, h := range m.Headers {
		v := h.Value
		if r := []rune(v); len(r) > 4 {
			v = "…" + string(r[len(r)-4:])
		}
		out = append(out, MCPHeader{Name: h.Name, Value: v})
	}
	return out
}
