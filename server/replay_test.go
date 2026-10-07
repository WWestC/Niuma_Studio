package server

// replay_test.go — the 一日回放 face's behavior (r_32): upload→paged read
// roundtrip, the watermark's idempotent re-send, the days list, the
// unknown-room / no-registry 404s, the day-file size fence (413), the
// share gate's full lifecycle (mint → gated read → revoke), and the
// retention prune leg.

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/projects"
)

// startReplayStudio boots a real server (ephemeral port) whose registry
// persists under a temp root with one active project "alpha".
func startReplayStudio(t *testing.T) *Server {
	t.Helper()
	root := t.TempDir()
	projStore, err := projects.Open("")
	if err != nil {
		t.Fatalf("projects.Open: %v", err)
	}
	registry := chat.NewRegistry(projStore, root, "", "")
	if _, err := projStore.Create(projects.Project{Key: "alpha", Name: "项目甲",
		Workspace: t.TempDir() + "/alpha"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := projStore.Activate("alpha"); err != nil {
		t.Fatalf("Activate: %v", err)
	}
	lobby := chat.NewHub()
	lobby.Join("房主", "boss", false)
	s, err := Start(lobby, Options{Endpoint: EndpointConfig{
		PreferredPort: -1,
	},
		Web: WebConfig{
			WebFS: fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("<html>shell</html>")}},
		},
		Registry: registry})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(s.Close)
	return s
}

// postFrames is the recorder's leg: one batch of raw frames.
func postFrames(t *testing.T, s *Server, key, body string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/p/"+key+"/replay", strings.NewReader(body))
	req.SetPathValue("key", key)
	rec := httptest.NewRecorder()
	s.handleReplayFrames(rec, req)
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

func frameLine(t int64, x float64) string {
	return fmt.Sprintf(`{"t":%d,"a":[{"n":"小牛","x":%v,"y":100,"d":2,"c":"#a03","hair":"01"}]}`, t, x)
}

func framesBody(ts ...int64) string {
	var b strings.Builder
	b.WriteString(`{"frames":[`)
	for i, t := range ts {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(frameLine(t, float64(i)*10))
	}
	b.WriteString("]}")
	return b.String()
}

// replayPage 的帧面是 raw 透传（r_32.2）——行原样进响应，测试侧再按
// 需解码（tsOf 抽 t，其余用例各自探字段）。
type replayPage struct {
	Project   string            `json:"project"`
	Date      string            `json:"date"`
	Count     int               `json:"count"`
	Frames    []json.RawMessage `json:"frames"`
	HasMore   bool              `json:"has_more"`
	NextSince int64             `json:"next_since"`
}

func getFrames(t *testing.T, s *Server, query string) (int, replayPage) {
	t.Helper()
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/p/alpha/replay%s", s.Port, query))
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Logf("GET -> %d: %s", resp.StatusCode, body)
		return resp.StatusCode, replayPage{}
	}
	var page replayPage
	if err := json.Unmarshal(body, &page); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return http.StatusOK, page
}

func tsOf(t *testing.T, page replayPage) []int64 {
	t.Helper()
	out := make([]int64, 0, len(page.Frames))
	for _, raw := range page.Frames {
		var probe struct {
			T int64 `json:"t"`
		}
		if err := json.Unmarshal(raw, &probe); err != nil {
			t.Fatalf("bad frame row: %s", raw)
		}
		out = append(out, probe.T)
	}
	return out
}

func eqTs(t *testing.T, page replayPage, want ...int64) {
	t.Helper()
	got := tsOf(t, page)
	if len(got) != len(want) {
		t.Fatalf("ts = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("ts = %v, want %v", got, want)
		}
	}
}

// 上传→分页读：整日三帧，limit=2 首页两帧 has_more，游标翻页拿余下。
func TestReplayUploadAndPagedRead(t *testing.T) {
	s := startReplayStudio(t)
	day := time.Now().Format("20060102")
	base := time.Now().UnixMilli()
	code, out := postFrames(t, s, "alpha", framesBody(base, base+500, base+1000))
	if code != http.StatusOK || out["stored"] != float64(3) {
		t.Fatalf("POST = %d %v, want stored 3", code, out)
	}
	q := fmt.Sprintf("?date=%s", day)
	code, page := getFrames(t, s, q)
	if code != http.StatusOK || page.Count != 3 {
		t.Fatalf("GET all = %d count %d, want 200/3", code, page.Count)
	}
	eqTs(t, page, base, base+500, base+1000)
	code, page = getFrames(t, s, q+"&limit=2")
	if code != http.StatusOK || !page.HasMore || page.NextSince != base+500 {
		t.Fatalf("GET page1 = %d more=%v next=%d, want more true next=%d", code, page.HasMore, page.NextSince, base+500)
	}
	eqTs(t, page, base, base+500)
	code, page = getFrames(t, s, fmt.Sprintf("%s&limit=2&since=%d", q, page.NextSince))
	if code != http.StatusOK || page.HasMore || page.Count != 1 {
		t.Fatalf("GET page2 = %d more=%v count=%d, want tail 1", code, page.HasMore, page.Count)
	}
	eqTs(t, page, base+1000)
}

// r_32.1 头顶面五列的落盘 round-trip＋r_32.2 透传根治证明：bt/dk/lab/
// dn/fk 经落盘再读出不丢（dk 是 tri-state 的痛处——0 是真工位、缺省是
// 无工位）；更要紧的是「服务端不认识的未来字段」也原样存活——首版
// 结构体解码再编码会把前端新加的列静默剥掉（r_32.1 断线的 bug 类），
// raw 透传之后此类从构造上不可能。
func TestReplayActorOverheadColumnsRoundTrip(t *testing.T) {
	s := startReplayStudio(t)
	base := time.Now().UnixMilli()
	rich := fmt.Sprintf(`{"t":%d,"a":[{"n":"小牛","x":1,"y":1,"d":0,"b":"你好","w":true,`+
		`"bt":2,"dk":0,"lab":"t_9","dn":true,"fk":"coffee",`+
		`"mi":1,"gr":true,"rk":3,"loc":true,"npc":false,"po":"cheer","em":"happy","sp":true,`+
		`"zz":{"future":["field",true,1]}}],"r":{"lit":true,"note":"公告","pour":0.55,`+
		`"pw":{"2":4.5},"prn":{"job":{"ph":"paper","k":"task","t":0.5},"un":[["done",3]]},`+
		`"en":{"bin":2,"snacks":0,"lt":[[10.5,20,8,"coffee"]]},"tro":3,`+
		`"ia":[["coffee",0.25,560,154]],"futureRoom":"whatever"}}`, base)
	bare := frameLine(base+500, 2)
	code, out := postFrames(t, s, "alpha", `{"frames":[`+rich+`,`+bare+`]}`)
	if code != http.StatusOK || out["stored"] != float64(2) {
		t.Fatalf("POST = %d %v, want stored 2", code, out)
	}
	_, page := getFrames(t, s, "?date="+time.Now().Format("20060102"))
	if len(page.Frames) != 2 {
		t.Fatalf("frames = %d, want 2", len(page.Frames))
	}
	var row struct {
		A []struct {
			Bt  int    `json:"bt"`
			Dk  *int   `json:"dk"`
			Lab string `json:"lab"`
			Dn  bool   `json:"dn"`
			Fk  string `json:"fk"`
			Mi  int    `json:"mi"`
			Gr  bool   `json:"gr"`
			Rk  int    `json:"rk"`
			Loc bool   `json:"loc"`
			Po  string `json:"po"`
			Em  string `json:"em"`
			Sp  bool   `json:"sp"`
			ZZ  *struct {
				Future []any `json:"future"`
			} `json:"zz"`
		} `json:"a"`
		R struct {
			Lit      bool    `json:"lit"`
			Note     string  `json:"note"`
			Pour     float64 `json:"pour"`
			FutureRm string  `json:"futureRoom"`
		} `json:"r"`
	}
	if err := json.Unmarshal(page.Frames[0], &row); err != nil {
		t.Fatalf("decode rich: %v", err)
	}
	a := row.A[0]
	if a.Bt != 2 || a.Dk == nil || *a.Dk != 0 || a.Lab != "t_9" || !a.Dn || a.Fk != "coffee" {
		t.Fatalf("r_32.1 columns = %+v, want bt=2 dk=0 lab=t_9 dn fk=coffee", a)
	}
	if a.Mi != 1 || !a.Gr || a.Rk != 3 || !a.Loc || a.Po != "cheer" || a.Em != "happy" || !a.Sp {
		t.Fatalf("r_32.2 columns = %+v, want mi/gr/rk/loc/po/em/sp all through", a)
	}
	if a.ZZ == nil || len(a.ZZ.Future) != 3 {
		t.Fatalf("unknown actor field lost: %+v (raw passthrough must keep it)", a.ZZ)
	}
	if !row.R.Lit || row.R.Note != "公告" || row.R.Pour != 0.55 || row.R.FutureRm != "whatever" {
		t.Fatalf("room state = %+v, want lit/note/pour + unknown room field kept", row.R)
	}
	var bareRow struct {
		A []struct {
			Bt int  `json:"bt"`
			Dk *int `json:"dk"`
		} `json:"a"`
		R json.RawMessage `json:"r"`
	}
	if err := json.Unmarshal(page.Frames[1], &bareRow); err != nil {
		t.Fatalf("decode bare: %v", err)
	}
	if bareRow.A[0].Bt != 0 || bareRow.A[0].Dk != nil || len(bareRow.R) > 0 {
		t.Fatalf("bare row = %+v, want all-zero overhead columns (dk nil, no r)", bareRow)
	}
}

// 水位线幂等：重发/乱序批只收新顶，文件里不重不乱。
func TestReplayUploadDedupesOutOfOrder(t *testing.T) {
	s := startReplayStudio(t)
	base := time.Now().UnixMilli()
	postFrames(t, s, "alpha", framesBody(base, base+500, base+1000))
	code, out := postFrames(t, s, "alpha", framesBody(base+150, base+1000, base+1500))
	if code != http.StatusOK || out["stored"] != float64(1) || out["skipped"] != float64(2) {
		t.Fatalf("re-POST = %d %v, want stored 1 skipped 2", code, out)
	}
	_, page := getFrames(t, s, "?date="+time.Now().Format("20060102"))
	eqTs(t, page, base, base+500, base+1000, base+1500)
}

// days 列 newest-first（YYYY-MM-DD）；未知项目/无 Registry 整面 404。
func TestReplayDaysAndUnknownRoom(t *testing.T) {
	s := startReplayStudio(t)
	now := time.Now()
	postFrames(t, s, "alpha", framesBody(now.UnixMilli()))
	postFrames(t, s, "alpha", framesBody(now.AddDate(0, 0, -1).UnixMilli()))
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/p/alpha/replay/days", s.Port))
	if err != nil {
		t.Fatalf("GET days: %v", err)
	}
	defer resp.Body.Close()
	var out struct {
		Days []string `json:"days"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	if len(out.Days) != 2 || out.Days[0] != now.Format("2006-01-02") ||
		out.Days[1] != now.AddDate(0, 0, -1).Format("2006-01-02") {
		t.Fatalf("days = %v, want [%s %s]", out.Days, now.Format("2006-01-02"), now.AddDate(0, 0, -1).Format("2006-01-02"))
	}
	resp2, _ := http.Get(fmt.Sprintf("http://127.0.0.1:%d/p/nope/replay?date=%s", s.Port, now.Format("20060102")))
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown room = %d, want 404", resp2.StatusCode)
	}
	bare := &Server{}
	rec := httptest.NewRecorder()
	bare.handleReplayFrames(rec, httptest.NewRequest(http.MethodGet, "/p/alpha/replay?date=20260101", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("no registry = %d, want 404", rec.Code)
	}
}

// 单日文件帽：超帽 413（帽是 var——测试调小，产线 256MB）。
func TestReplayDayCapFences413(t *testing.T) {
	old := replayDayCap
	replayDayCap = 64
	t.Cleanup(func() { replayDayCap = old })
	s := startReplayStudio(t)
	day := time.Now().Format("20060102")
	path := filepath.Join(s.opts.Registry.Root(), "replay", "alpha", day+".jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Repeat("x", 128)), 0o600); err != nil {
		t.Fatal(err)
	}
	code, out := postFrames(t, s, "alpha", framesBody(time.Now().UnixMilli()))
	if code != http.StatusRequestEntityTooLarge {
		t.Fatalf("over cap = %d %v, want 413", code, out)
	}
}

// 分享门全周期：无确认 400；空日 409；铸 token → 链接页 200/错 token 404；
// 帧读面带 token 放行该日、放行不了别的日；撤回后全死。
func TestReplayShareLifecycle(t *testing.T) {
	s := startReplayStudio(t)
	day := time.Now().Format("20060102")
	dashed := time.Now().Format("2006-01-02")
	base := time.Now().UnixMilli()
	postFrames(t, s, "alpha", framesBody(base))

	share := func(body string) (int, map[string]any) {
		req := httptest.NewRequest(http.MethodPost, "/p/alpha/replay/share", strings.NewReader(body))
		req.SetPathValue("key", "alpha")
		rec := httptest.NewRecorder()
		s.handleReplayShare(rec, req)
		var out map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out
	}
	if code, _ := share(`{"date":"` + day + `"}`); code != http.StatusBadRequest {
		t.Fatalf("no confirm = %d, want 400", code)
	}
	if code, _ := share(`{"date":"20260101","confirm":"REPLAY"}`); code != http.StatusConflict {
		t.Fatalf("empty day = %d, want 409", code)
	}
	code, out := share(`{"date":"` + day + `","confirm":"REPLAY"}`)
	if code != http.StatusOK || out["token"] == "" || out["path"] == "" {
		t.Fatalf("share = %d %v, want token+path", code, out)
	}
	token, _ := out["token"].(string)
	link := fmt.Sprintf("http://127.0.0.1:%d/dayreplay/alpha?t=%s&date=%s", s.Port, token, dashed)

	// 链接页：对 token 200（同壳 HTML），错 token 404。
	resp, _ := http.Get(link)
	html1, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(html1), "<html") {
		t.Fatalf("share page = %d, want 200 html", resp.StatusCode)
	}
	resp, _ = http.Get(fmt.Sprintf("http://127.0.0.1:%d/dayreplay/alpha?t=deadbeef&date=%s", s.Port, dashed))
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("wrong token = %d, want 404", resp.StatusCode)
	}

	// 帧读面：token 只放行授权日。
	code, page := getFrames(t, s, fmt.Sprintf("?date=%s&t=%s", day, token))
	if code != http.StatusOK || page.Count != 1 {
		t.Fatalf("token read granted day = %d count %d, want 200/1", code, page.Count)
	}
	resp, _ = http.Get(fmt.Sprintf("http://127.0.0.1:%d/p/alpha/replay?date=20260101&t=%s", s.Port, token))
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("token read other day = %d, want 404", resp.StatusCode)
	}

	// 撤回：链接页与帧读面同拍全死。
	if code, _ := share(`{"off":true}`); code != http.StatusOK {
		t.Fatalf("revoke = %d, want 200", code)
	}
	resp, _ = http.Get(link)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("revoked page = %d, want 404", resp.StatusCode)
	}
	resp, _ = http.Get(fmt.Sprintf("http://127.0.0.1:%d/p/alpha/replay?date=%s&t=%s", s.Port, day, token))
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("revoked read = %d, want 404", resp.StatusCode)
	}
}

// 保留期腿：文件名日期早于保留界的整文件删，今日保留。旧文件取
// 3 天前——默认 7 天的启动扫（异步）不动它、1 天策略才删，测试与
// Start 的 boot sweep 不竞速。
func TestReplayRetentionPrune(t *testing.T) {
	s := startReplayStudio(t)
	root := s.opts.Registry.Root()
	dir := filepath.Join(root, "replay", "alpha")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	old := filepath.Join(dir, time.Now().AddDate(0, 0, -3).Format("20060102")+".jsonl")
	today := filepath.Join(dir, time.Now().Format("20060102")+".jsonl")
	os.WriteFile(old, []byte(frameLine(time.Now().UnixMilli(), 1)+"\n"), 0o600)
	os.WriteFile(today, []byte(frameLine(time.Now().UnixMilli(), 2)+"\n"), 0o600)
	if err := chat.SaveRetention(filepath.Join(root, "retention.json"), 1); err != nil {
		t.Fatal(err)
	}
	if dropped := s.roomopsFace().SweepRetention(time.Now()); dropped < 1 {
		t.Fatalf("sweep dropped %d, want ≥1", dropped)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatalf("old day file survived: %v", err)
	}
	if _, err := os.Stat(today); err != nil {
		t.Fatalf("today file pruned: %v", err)
	}
}
