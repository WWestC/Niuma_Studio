package server

// 输入图片的 HTTP 面（media.go）：POST /media 落仓库换引用、GET
// /media/{id} 直出字节（ETag 协商）；白名单与尺寸帽在仓库收口后以
// 400 回因；未配置仓库的面上传 503。

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/media"
	"github.com/WWestC/Niuma_Studio/wire"

	"github.com/coder/websocket/wsjson"
)

func startMedia(t *testing.T, store *media.Store) *Server {
	t.Helper()
	s, err := Start(chat.NewHub(), Options{Endpoint: EndpointConfig{
		PreferredPort: -1,
	},
		Stores: StoreSet{
			MediaStore: store,
		}})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(s.Close)
	return s
}

func TestMediaUploadAndServeRoundTrip(t *testing.T) {
	s := startMedia(t, media.NewStore(t.TempDir()))
	png := "\x89PNG\r\n\x1a\n" + strings.Repeat("pixel", 100)

	up, err := http.Post(addrHTTP(s)+"/media?name="+strings.ReplaceAll("设计稿.png", " ", ""),
		"image/png", bytes.NewReader([]byte(png)))
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	body, _ := io.ReadAll(up.Body)
	up.Body.Close()
	if up.StatusCode != 200 {
		t.Fatalf("upload status=%d body=%s", up.StatusCode, body)
	}
	if !strings.Contains(string(body), `"id":"img_`) || !strings.Contains(string(body), "设计稿.png") {
		t.Fatalf("回执应带引用与原名: %s", body)
	}

	// 取回：字节一致、类型正确、ETag 协商 304
	id := "img_" + strings.SplitN(strings.Split(string(body), `"id":"img_`)[1], `"`, 2)[0]
	get, err := http.Get(addrHTTP(s) + "/media/" + id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	b2, _ := io.ReadAll(get.Body)
	get.Body.Close()
	if get.StatusCode != 200 || string(b2) != png {
		t.Fatalf("取回字节不符: status=%d len=%d", get.StatusCode, len(b2))
	}
	if ct := get.Header.Get("Content-Type"); ct != "image/png" {
		t.Fatalf("Content-Type = %q", ct)
	}
	etag := get.Header.Get("ETag")
	if etag == "" {
		t.Fatal("应带 ETag（内容寻址 id 即校验器）")
	}
	req, _ := http.NewRequest("GET", addrHTTP(s)+"/media/"+id, nil)
	req.Header.Set("If-None-Match", etag)
	reval, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("revalidate: %v", err)
	}
	reval.Body.Close()
	if reval.StatusCode != http.StatusNotModified {
		t.Fatalf("ETag 协商应 304，得到 %d", reval.StatusCode)
	}

	// 坏 id：404（形状不对/盘上没有都 miss）
	for _, bad := range []string{"zzz", "img_short", "img_000000000000"} {
		r, _ := http.Get(addrHTTP(s) + "/media/" + bad)
		r.Body.Close()
		if r.StatusCode != 404 {
			t.Fatalf("坏 id %q 应 404，得到 %d", bad, r.StatusCode)
		}
	}
}

func TestMediaUploadRefusesNonImage(t *testing.T) {
	s := startMedia(t, media.NewStore(t.TempDir()))
	r, err := http.Post(addrHTTP(s)+"/media?name=note.txt", "text/plain",
		bytes.NewReader([]byte("just text")))
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	body, _ := io.ReadAll(r.Body)
	r.Body.Close()
	if r.StatusCode != 400 {
		t.Fatalf("非图应 400，得到 %d body=%s", r.StatusCode, body)
	}
	if !strings.Contains(string(body), "只收图片") {
		t.Fatalf("400 应带可读原因: %s", body)
	}
}

func TestMediaFacesAbsentWithoutStore(t *testing.T) {
	// 仓库未配置：上传 503（可读原因），取回 404——面在但无后端
	s, err := Start(chat.NewHub(), Options{Endpoint: EndpointConfig{PreferredPort: -1}})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(s.Close)
	r, err := http.Post(addrHTTP(s)+"/media?name=a.png", "image/png",
		bytes.NewReader([]byte("x")))
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	io.Copy(io.Discard, r.Body)
	r.Body.Close()
	if r.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("无仓库上传应 503，得到 %d", r.StatusCode)
	}
	g, _ := http.Get(addrHTTP(s) + "/media/img_0123456789ab")
	g.Body.Close()
	if g.StatusCode != 404 {
		t.Fatalf("无仓库取回应 404，得到 %d", g.StatusCode)
	}
}

// TestSeatlessOwnerSayCarriesImages — web 房主写面的真实路径（无座
// owner 通道）：上传换引用后，带 images 的 say 要原样进房（回显带图、
// 历史带图），@解析照常。
func TestSeatlessOwnerSayCarriesImages(t *testing.T) {
	lobby := chat.NewHub()
	seat, _ := lobby.Join("房主", "boss", false)
	lobby.SetOwnerSeat(seat)
	s, err := Start(lobby, Options{Endpoint: EndpointConfig{
		PreferredPort: -1,
	},
		Stores: StoreSet{
			MediaStore: media.NewStore(t.TempDir()),
		},
		LocalName: "房主"})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(s.Close)

	// 先上传换引用（composer 的两步）
	up, err := http.Post(addrHTTP(s)+"/media?name=demo.png", "image/png",
		bytes.NewReader([]byte("\x89PNG demo")))
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	var receipt struct {
		Image wire.Image `json:"image"`
	}
	if err := json.NewDecoder(up.Body).Decode(&receipt); err != nil {
		t.Fatalf("receipt: %v", err)
	}
	up.Body.Close()
	if receipt.Image.ID == "" {
		t.Fatal("上传未回引用")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn := dialOwnerSeatlessCred(t, s, lobby) // 带图 say 是署名动词——凭证门后由带凭路径走
	if err := wsjson.Write(ctx, conn, chat.Message{
		Type:   chat.MsgSay,
		Text:   "看图",
		Images: []wire.Image{receipt.Image},
	}); err != nil {
		t.Fatalf("say: %v", err)
	}
	var echo chat.Message
	if err := wsjson.Read(ctx, conn, &echo); err != nil {
		t.Fatalf("echo: %v", err)
	}
	if echo.Type != chat.MsgSay || echo.From != "房主" || len(echo.Images) != 1 ||
		echo.Images[0].ID != receipt.Image.ID {
		t.Fatalf("回显应带原图片引用，got %+v", echo)
	}
	found := false
	for _, m := range lobby.History() {
		if m.Seq == echo.Seq && len(m.Images) == 1 {
			found = true
		}
	}
	if !found {
		t.Fatal("无座 owner 带图 say 应进历史")
	}
}
