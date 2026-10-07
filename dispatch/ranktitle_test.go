package dispatch

// 等级变更的侧栏标题刷新（rank-change tap）的调度器半边：
// RefreshIndexTitle 重写行时 —— ① 标题的 Lv.N 槽读注册表现值；
// ② 座位的出生装配模型原样保留（不得抹回进程默认）；③ 揭示深链
// 走强制通道（不吃出生爆发的 10s 节流窗——由 zcode 包的 force 语义
// 保证，这里只断言行重写本身）。registerIndex 是测试缝：真身要拉
// python3 写桌面自己的 sqlite，单测不该碰机器。

import (
	"testing"

	"github.com/WWestC/Niuma_Studio/agents"
	"github.com/WWestC/Niuma_Studio/capability"
	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/zcode"
)

// rankTapDispatcher builds a lobby dispatcher with 张三 attached, a
// live rank registry (a plain map behind RankOf), and the registerIndex
// seam capturing every upsert. The registry map is handed back for the
// test to mutate — the tap must read it live, not snapshot it.
func rankTapDispatcher(t *testing.T) (*Dispatcher, *[]zcode.TaskIndexEntry, map[string]int) {
	t.Helper()
	hub := chat.NewHub()
	store, err := agents.Open("")
	if err != nil {
		t.Fatal(err)
	}
	ranks := map[string]int{"张三": 2}
	d := Start(hub, store, &readBridge{}, Config{
		Workspace:   t.TempDir(),
		InboxDir:    t.TempDir(),
		ProjectName: "Niuma_Studio",
		PatrolEvery: -1,
		DailyAt:     "off",
		RankOf:      func(name string) int { return ranks[name] },
	})
	t.Cleanup(d.Stop)
	if err := d.attach("张三", "工程师", "s-rank-1"); err != nil {
		t.Fatal(err)
	}
	// 座位级装配模型：出生时写进 m.fab.fabric 的有效模型。
	m := d.lookup("张三")
	d.mu.Lock()
	m.fab.fabric = &capability.EffectiveFabric{Model: &capability.ModelTier{ID: "seat-model-x"}}
	d.mu.Unlock()

	var got []zcode.TaskIndexEntry
	d.registerIndex = func(e zcode.TaskIndexEntry) error {
		got = append(got, e)
		return nil
	}
	return d, &got, ranks
}

// TestRefreshIndexTitleCarriesNewRank pins the tap's core promise: a
// rank write lands on the sidebar row at once — the Lv.N slot reads
// the registry live, not a boot-time snapshot.
func TestRefreshIndexTitleCarriesNewRank(t *testing.T) {
	d, got, ranks := rankTapDispatcher(t)
	d.RefreshIndexTitle("张三")
	if len(*got) != 1 {
		t.Fatalf("刷新应重写一次行，得到 %d 次", len(*got))
	}
	if e := (*got)[0]; e.Title != "【Niuma_Studio】张三-工程师-Lv.2" {
		t.Fatalf("初始标题不对: %q", e.Title)
	}

	ranks["张三"] = 5 // rank_set 已落注册表
	d.RefreshIndexTitle("张三")
	if len(*got) != 2 {
		t.Fatalf("第二次刷新应再写一行，得到 %d 次", len(*got))
	}
	if e := (*got)[1]; e.Title != "【Niuma_Studio】张三-工程师-Lv.5" {
		t.Fatalf("等级变更后标题未跟上: %q", e.Title)
	}
}

// TestRefreshIndexTitleKeepsSeatModel pins the regression half: the
// refresh used to pass a nil fabric, silently resetting the row's
// model column to the process default on every rank change.
func TestRefreshIndexTitleKeepsSeatModel(t *testing.T) {
	d, got, _ := rankTapDispatcher(t)
	d.RefreshIndexTitle("张三")
	e := (*got)[0]
	if e.Model != "seat-model-x" {
		t.Fatalf("等级刷新不得抹掉座位模型: %q", e.Model)
	}
	if e.Provider != zcode.DesktopListProvider {
		t.Fatalf("provider 应保持桌面列表常量: %q", e.Provider)
	}
}

// TestRefreshIndexTitleIgnoresUnmanaged pins the no-op half: a name
// the dispatcher doesn't manage (offboarded, other rooms) rewrites
// nothing — the fleet's routing already picked this dispatcher as the
// name's home, an unknown name inside it is simply not ours to touch.
func TestRefreshIndexTitleIgnoresUnmanaged(t *testing.T) {
	d, got, _ := rankTapDispatcher(t)
	d.RefreshIndexTitle("路人甲")
	if len(*got) != 0 {
		t.Fatalf("未管理层名应静默跳过，却写了 %d 行", len(*got))
	}
}
