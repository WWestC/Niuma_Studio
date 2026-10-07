package dispatch

// 输入图片在调度器半边的契约：点名线把 say 的 images 还原成
// session/send 附件（media.Store.Path→localPath）；忙时排队的车道保图，
// 重启读档（saveLanes/loadLanes）也保图，旧格式文件（无 img_queue）补
// nil 走纯文本；仓库解析不出的图降级为文字注记——图片永远不拖垮正文。

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/WWestC/Niuma_Studio/agents"
	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/media"
	"github.com/WWestC/Niuma_Studio/wire"
	"github.com/WWestC/Niuma_Studio/zcode"
)

// imgBridge 记录注入与附件：attMu 护并发（上传/注入都在 goroutine 里）。
type imgBridge struct {
	baseBridge
	mu    sync.Mutex
	sends []string
	atts  [][]zcode.Attachment
	busy  bool // true: 首次 Send 回 -32010（busy），排队后放行
	// stickyBusy: 永远回 -32010——车道持久化类测试要车道停在原地，
	// 不许任何 kick（Start 收养与 attach 各有一次）把线投走。
	stickyBusy bool
}

func (f *imgBridge) Send(ctx context.Context, sessionID, content, inputID string, deny []string) (*zcode.SendAck, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.stickyBusy {
		return nil, &zcode.Error{Code: zcode.ErrPromptRunning, Message: "A prompt is already running for this session"}
	}
	if f.busy {
		f.busy = false // 只忙一次：排队后 drain 应能送达
		return nil, &zcode.Error{Code: zcode.ErrPromptRunning, Message: "A prompt is already running for this session"}
	}
	f.sends = append(f.sends, content)
	f.atts = append(f.atts, nil)
	return nil, nil
}
func (f *imgBridge) SendAttached(ctx context.Context, sessionID, content, inputID string, deny []string, atts []zcode.Attachment) (*zcode.SendAck, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.stickyBusy {
		return nil, &zcode.Error{Code: zcode.ErrPromptRunning, Message: "A prompt is already running for this session"}
	}
	if f.busy {
		f.busy = false
		return nil, &zcode.Error{Code: zcode.ErrPromptRunning, Message: "A prompt is already running for this session"}
	}
	f.sends = append(f.sends, content)
	f.atts = append(f.atts, atts)
	return nil, nil
}

func (f *imgBridge) snapshot() ([]string, [][]zcode.Attachment) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := append([]string(nil), f.sends...)
	atts := append([][]zcode.Attachment(nil), f.atts...)
	return out, atts
}

func startImgDispatcher(t *testing.T, b Bridge, store *media.Store, inbox string) *Dispatcher {
	t.Helper()
	hub := chat.NewHub()
	as, err := agents.Open("")
	if err != nil {
		t.Fatal(err)
	}
	if inbox == "" {
		inbox = t.TempDir()
	}
	d := Start(hub, as, b, Config{
		Workspace:   t.TempDir(),
		InboxDir:    inbox,
		PatrolEvery: -1,
		DailyAt:     "off",
		MediaStore:  store,
	})
	t.Cleanup(d.Stop)
	return d
}

func saveImg(t *testing.T, store *media.Store, name string) wire.Image {
	t.Helper()
	img, err := store.Save([]byte("\x89PNG\r\n\x1a\nfake-"+name), name, "image/png")
	if err != nil {
		t.Fatalf("存图 %s: %v", name, err)
	}
	return wire.Image{ID: img.ID, Name: img.Name, Mime: img.Mime, W: img.W, H: img.H, Bytes: img.Bytes}
}

func TestMentionSayInjectsImagesAsAttachments(t *testing.T) {
	store := media.NewStore(t.TempDir())
	im1 := saveImg(t, store, "设计稿.png")
	im2 := saveImg(t, store, "现场照.png")
	ib := &imgBridge{}
	d := startImgDispatcher(t, ib, store, "")
	if err := d.attach("张三", "美术设计师", "s-img-1"); err != nil {
		t.Fatal(err)
	}
	speaker, _ := d.hub.Join("李四", "", false)
	if _, ok := d.hub.SayImages(speaker, "@张三 按这张稿子改", []wire.Image{im1, im2}); !ok {
		t.Fatal("say 未入史")
	}
	waitFor(t, func() bool {
		sends, _ := ib.snapshot()
		return len(sends) > 0
	}, "点名注入应发生")
	sends, atts := ib.snapshot()
	if !strings.Contains(sends[0], "按这张稿子改") || !strings.Contains(sends[0], "设计稿.png") {
		t.Fatalf("注入正文应带原文与图片名: %q", sends[0])
	}
	if len(atts[0]) != 2 {
		t.Fatalf("应带 2 枚附件，得到 %d", len(atts[0]))
	}
	if atts[0][0].Kind != "image" || atts[0][0].Filename != "设计稿.png" || atts[0][0].LocalPath == "" {
		t.Fatalf("附件形状不对: %+v", atts[0][0])
	}
	if !strings.HasPrefix(atts[0][0].LocalPath, store.Root()) {
		t.Fatalf("附件应指仓库绝对路径: %q", atts[0][0].LocalPath)
	}
}

func TestBusyQueueKeepsImagesThroughDrain(t *testing.T) {
	store := media.NewStore(t.TempDir())
	im := saveImg(t, store, "截图.png")
	ib := &imgBridge{busy: true}
	d := startImgDispatcher(t, ib, store, "")
	if err := d.attach("张三", "工程师", "s-img-2"); err != nil {
		t.Fatal(err)
	}
	speaker, _ := d.hub.Join("李四", "", false)
	seq := int64(0)
	if msg, ok := d.hub.SayImages(speaker, "@张三 看图办事", []wire.Image{im}); ok {
		seq = msg.Seq
	}
	// busy：排队（车道保图），随后回合终点 drain 再送
	waitFor(t, func() bool {
		for _, row := range d.hub.QueueRows() {
			if row.Seq == seq {
				return true
			}
		}
		return false
	}, "忙时应亮排队中")
	d.onEvent(turnCompletedEvent("s-img-2", "上一轮忙完了"))
	waitFor(t, func() bool {
		sends, _ := ib.snapshot()
		return len(sends) > 0
	}, "出队后应送达")
	sends, atts := ib.snapshot()
	if !strings.Contains(sends[0], "看图办事") {
		t.Fatalf("drain 的正文不对: %q", sends[0])
	}
	if len(atts[0]) != 1 || atts[0][0].Filename != "截图.png" {
		t.Fatalf("drain 应带原附件: %+v", atts[0])
	}
}

func TestLanePersistenceKeepsImages(t *testing.T) {
	store := media.NewStore(t.TempDir())
	im := saveImg(t, store, "草图.png")
	ib := &imgBridge{}
	inbox := t.TempDir()
	d := startImgDispatcher(t, ib, store, inbox)
	if err := d.attach("张三", "工程师", "s-img-3"); err != nil {
		t.Fatal(err)
	}
	m := d.lookup("张三")
	// enqueueAt 自持锁（callers hold no lock 契约）
	d.enqueueAt(m, "带图排队行", []int64{1}, nil, 0, []wire.Image{im})
	// 重启读档：同一个 inbox 目录上重建成员（新 dispatcher、同一仓库）。
	// 置 stickyBusy：Start 收养与 attach 各有一次 laneKick，哪次抢在断言
	// 前把线投走都是测试赌输调度——持续 busy 让线留在车道，断言成确定性。
	ib.mu.Lock()
	ib.stickyBusy = true
	ib.mu.Unlock()
	d2 := startImgDispatcher(t, ib, store, inbox)
	if err := d2.attach("张三", "工程师", "s-img-3b"); err != nil {
		t.Fatal(err)
	}
	m2 := d2.lookup("张三")
	// 收敛等待：kick 的弹出→busy 回停之间有一个瞬时空窗（消费即落盘
	// 先写空、回停再写回），瞬时断言赌的是调度；stickyBusy 下系统必然
	// 收敛回 queue=1，等它即可。
	waitFor(t, func() bool {
		d2.mu.Lock()
		defer d2.mu.Unlock()
		return len(m2.lanes.queue) > 0 && len(m2.lanes.queue[0].imgs) > 0 &&
			m2.lanes.queue[0].imgs[0].ID == im.ID
	}, "重启后车道应带着排队行与随行图回生")
}

func TestOldLaneFilePadsNilImages(t *testing.T) {
	store := media.NewStore(t.TempDir())
	ib := &imgBridge{}
	d := startImgDispatcher(t, ib, store, "")
	// 旧格式车道文件（无 img_queue 字段）：手写一份
	dir := d.cfg.InboxDir
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	old := `{"default": {"queue": ["旧排队行"], "read_queue": [[7]], "queue_at": [0]}}`
	if err := os.WriteFile(filepath.Join(dir, "张三.json"), []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := d.attach("张三", "工程师", "s-img-4"); err != nil {
		t.Fatal(err)
	}
	// 旧格式补 nil 的可观察效果＝该线文字照送、不报缺图（nil 图道不
	// 是「丢了图」）——在投递结果上断言。不碰车道内部：归位即发车的
	// kick 在自己的 goroutine 上弹线，无锁读车道是数据竞态。
	waitFor(t, func() bool {
		sends, _ := ib.snapshot()
		return len(sends) > 0
	}, "恢复的旧线应归位即发车")
	sends, _ := ib.snapshot()
	got := sends[0]
	if !strings.Contains(got, "旧排队行") {
		t.Fatalf("旧车道应上路：%s", got)
	}
	if strings.Contains(got, "已不可用") {
		t.Fatalf("nil 图道不该报缺图：%s", got)
	}
}

func TestUnresolvableImageDegradesToNote(t *testing.T) {
	store := media.NewStore(t.TempDir())
	ghost := wire.Image{ID: "img_0123456789ab", Name: "丢了的图.png", Mime: "image/png"}
	ib := &imgBridge{}
	d := startImgDispatcher(t, ib, store, "")
	if err := d.attach("张三", "工程师", "s-img-5"); err != nil {
		t.Fatal(err)
	}
	speaker, _ := d.hub.Join("李四", "", false)
	if _, ok := d.hub.SayImages(speaker, "@张三 文字照送", []wire.Image{ghost}); !ok {
		t.Fatal("say 未入史")
	}
	waitFor(t, func() bool {
		sends, _ := ib.snapshot()
		return len(sends) > 0
	}, "注入应发生")
	sends, atts := ib.snapshot()
	if !strings.Contains(sends[0], "文字照送") {
		t.Fatalf("正文必须照送: %q", sends[0])
	}
	if !strings.Contains(sends[0], "丢了的图.png") {
		t.Fatalf("丢图应有文字注记: %q", sends[0])
	}
	if len(atts[0]) != 0 {
		t.Fatalf("解析不出不该有空附件: %+v", atts[0])
	}
}
