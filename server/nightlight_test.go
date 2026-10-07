package server

// nightlight_test.go — r_18 night-10 深夜亮灯接线（t_189 遗留动作）：
// nightLightTap 的窗口（0–6 点本时区）与每夜一粒门闩；成就事件流自动
// 过门；say 喂线挂拍（wireNightTap 幂等）；C3 night-owl 第十个深夜点亮。
// 测试时刻全部以 time.Local 构造——窗口判定跟着宿主时区走，断言不依赖
// 具体时区；util.SetNow 时钟缝（dispatch 测试同款手法）。
import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/util"
)

// sysTap collects the system lines the broadcast face speaks.
type sysTap struct {
	mu    sync.Mutex
	lines []string
}

func (t *sysTap) System(text string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.lines = append(t.lines, text)
}

func (t *sysTap) joined() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return strings.Join(t.lines, "\n")
}

func nightAt(day, hour, min int) time.Time {
	return time.Date(2026, 10, day, hour, min, 0, 0, time.Local)
}

func nightLightsOf(t *testing.T, s *Server) int {
	t.Helper()
	return s.achieve.NightLights()
}

// tapAt 把时钟缝拨到拍点再拍：observeAchievements 的自动挂拍读的是
// util.Now（真实时刻），测试的构造拍点若与真实时刻异日、且测试恰好跑
// 在本地 0–6 点，门闩就在两日期间乒乓、一拍落两粒——让缝跟着拍点走，
// 递归读到的永远是同一夜（凌晨套件/白天套件同绿）。
func tapAt(s *Server, hub *sysTap, at time.Time) {
	util.SetNow(func() int64 { return at.Unix() })
	s.nightLightTap(hub, at)
}

// ① 窗口与每夜一粒：0–6 点内的活动只在当夜首拍落一粒；白天不落；
// 次夜重新可落；6 点整已是窗外。
func TestNightLightTapOncePerNight(t *testing.T) {
	realNow := util.NowFn()
	defer func() { util.SetNow(realNow) }()
	s := &Server{achieve: NewAchievementEngine(nil)}
	tap := &sysTap{}
	tapAt(s, tap, nightAt(2, 1, 0))  // 深夜首拍 → 1
	tapAt(s, tap, nightAt(2, 1, 30)) // 同夜再拍 → 门闩
	tapAt(s, tap, nightAt(2, 5, 59)) // 同夜尾拍 → 门闩
	tapAt(s, tap, nightAt(2, 23, 0)) // 白天 → 不落
	tapAt(s, tap, nightAt(3, 0, 0))  // 次夜零点 → 2
	tapAt(s, tap, nightAt(3, 6, 0))  // 6 点整（窗外）→ 不落
	if got := nightLightsOf(t, s); got != 2 {
		t.Fatalf("两夜各一粒夜灯（got %d）", got)
	}
}

// ② 成就事件流自动过门：夜间的一拍 task-done 同时点亮当夜夜灯
// （observeAchievements 首行的自动挂拍面）。
func TestNightLightRidesAchieveTap(t *testing.T) {
	realNow := util.NowFn()
	defer func() { util.SetNow(realNow) }()
	util.SetNow(func() int64 { return nightAt(5, 3, 0).Unix() })
	s := &Server{achieve: NewAchievementEngine(nil)}
	s.observeAchievements(&sysTap{}, Event{Kind: "task-done", Member: "小猿", At: nightAt(5, 3, 0).Unix()})
	if got := nightLightsOf(t, s); got != 1 {
		t.Fatalf("夜间事件流应顺带点亮夜灯（got %d）", got)
	}
}

// ③ say 喂线挂拍：深夜房内发言也算亮灯（sayLike → sayFeed → 观察者）；
// wireNightTap 幂等——重复挂线不重复计数。喂线是异步泵，短轮询等账。
func TestNightLightSayFeed(t *testing.T) {
	realNow := util.NowFn()
	defer func() { util.SetNow(realNow) }()
	util.SetNow(func() int64 { return nightAt(7, 2, 0).Unix() })
	s := &Server{achieve: NewAchievementEngine(nil)}
	hub := chat.NewHub()
	s.wireNightTap(hub)
	s.wireNightTap(hub) // 幂等：第二次 no-op
	s.wireNightTap(hub) // 三次也不过
	c, _ := hub.Join("房主本人", "房主", false)
	hub.SetOwnerSeat(c)
	if _, ok := hub.SayAsOwner("深夜赶工"); !ok {
		t.Fatal("房主发言失败")
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if nightLightsOf(t, s) == 1 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("深夜发言应点亮夜灯且只一粒（got %d）", nightLightsOf(t, s))
}

// ④ C3 night-owl：第十个深夜点亮「亮灯的深夜」，第九夜不亮；点亮有
// 系统行广播。
func TestNightOwlFiresOnTenthNight(t *testing.T) {
	realNow := util.NowFn()
	defer func() { util.SetNow(realNow) }()
	s := &Server{achieve: NewAchievementEngine(nil)}
	tap := &sysTap{}
	for day := 1; day <= 9; day++ {
		tapAt(s, tap, nightAt(day, 1, 0))
	}
	ninth := s.achieve.Unlocked("night-owl")
	if ninth {
		t.Fatal("第九夜不该点亮 night-owl")
	}
	tapAt(s, tap, nightAt(10, 1, 0))
	tenth := s.achieve.Unlocked("night-owl")
	if !tenth {
		t.Fatal("第十个深夜应点亮 night-owl")
	}
	if !strings.Contains(tap.joined(), "亮灯的深夜") {
		t.Fatalf("点亮应有系统行广播: %q", tap.joined())
	}
}
