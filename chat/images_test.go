package chat

// 输入图片在 hub 半边的契约：say 帧的 images 引用随行落史＋广播；
// 纯图（空正文）也是合法发言；空正文空图仍拒；超额截到 MaxPerSay。

import (
	"fmt"
	"testing"

	"github.com/WWestC/Niuma_Studio/media"
	"github.com/WWestC/Niuma_Studio/wire"
)

func TestSayImagesRidesHistoryAndBroadcast(t *testing.T) {
	h := NewHub()
	a, _ := h.Join("房主", "", false)
	b, _ := h.Join("张三", "", false)

	imgs := []wire.Image{{ID: "img_000000000001", Name: "截图.png", Mime: "image/png", W: 800, H: 600, Bytes: 1234}}
	msg, ok := h.SayImages(a, "看这张图 @张三", imgs)
	if !ok {
		t.Fatal("带图 say 未入史")
	}
	if len(msg.Images) != 1 || msg.Images[0].ID != imgs[0].ID {
		t.Fatalf("回执不带图片引用: %+v", msg.Images)
	}
	// 广播帧送达在座成员（先排掉 join 时排队的 welcome）
	var got Message
	for got = range b.Receive() {
		if got.Type == MsgSay {
			break
		}
	}
	if got.Type != MsgSay || len(got.Images) != 1 || got.Images[0].W != 800 {
		t.Fatalf("广播帧不带图片: %+v", got)
	}
	// 历史里的同一条也带
	found := false
	for _, m := range h.History() {
		if m.Seq == msg.Seq && len(m.Images) == 1 && m.Images[0].ID == imgs[0].ID {
			found = true
		}
	}
	if !found {
		t.Fatal("历史不带图片引用——回放会丢图")
	}
}

func TestImageOnlySayIsSpeakable(t *testing.T) {
	h := NewHub()
	a, _ := h.Join("房主", "", false)
	// 空正文＋有图：合法（飞书式纯图消息）
	if _, ok := h.SayImages(a, "", []wire.Image{{ID: "img_000000000002"}}); !ok {
		t.Fatal("纯图 say 应入史")
	}
	// 空正文＋空图：照旧拒
	if _, ok := h.SayImages(a, "", nil); ok {
		t.Fatal("空说仍应被拒")
	}
}

func TestSayImagesCapsBatch(t *testing.T) {
	h := NewHub()
	a, _ := h.Join("房主", "", false)
	var imgs []wire.Image
	for i := 0; i < media.MaxPerSay+3; i++ {
		imgs = append(imgs, wire.Image{ID: fmt.Sprintf("img_%012x", i+1)})
	}
	msg, ok := h.SayImages(a, "九宫格", imgs)
	if !ok {
		t.Fatal("带图 say 未入史")
	}
	if len(msg.Images) != media.MaxPerSay {
		t.Fatalf("超额应截到 %d，得到 %d", media.MaxPerSay, len(msg.Images))
	}
}
