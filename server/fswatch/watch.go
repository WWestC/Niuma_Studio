package fswatch

// watch.go — 工作区变更的推送面（文件板自动刷新的服务端半边）。轮询
// 签名法（web.go 的 dev watcher 同款纪律：不引 fsnotify 依赖——一个
// 纯 Go 进程多一双 OS 事件的眼睛，换来的却是每平台一条 cgo 链），每
// 拍把每个项目工作区折成一份「相对路径＋mtime＋大小」签名，动了的项
// 目落一次 Notify。帧只说「脏了」不带增量（查询式真源是 /fs/tree），
// 广播谁收、收了怎么合并，是壳与前端的事——本文件只回答「哪个工作区
// 动了」。

import (
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/WWestC/Niuma_Studio/projects"
	"github.com/WWestC/Niuma_Studio/util"
)

// SkipDirs is the workspace-walk blacklist shared with fsview's search
// face（检索面不进的目录，变更监听也不进——.git 一拍几百次落盘，全
// 是自家 git 操作的回声；node_modules 是永无变更新闻的依赖海）。
var SkipDirs = map[string]bool{
	".git": true, "node_modules": true, ".hg": true, ".svn": true,
}

const (
	// watchEvery 是拍距。文件树的新鲜度以秒计足够（VSCode 的资源管理
	// 器同样有百毫秒级迟滞），换 2 秒一拍把常在后台的步行开销钉在噪
	// 声线上。
	watchEvery = 2 * time.Second
	// walkCap 是单项目单拍的目录数步行帽：病态大树及时止损。触帽的
	// 拍该项目的签名退化为常量——不再广播（诚实降级：巨树没有自动刷
	// 新，好过每拍全量步行烧 CPU）。
	walkCap = 20000
)

// Watcher polls every registered project's workspace for on-disk change
// and calls Notify once per changed project per beat. Notify carries the
// project key; nil Notify (embeds, tests) makes the loop a no-op meter.
// Loop runs the beat; Stop ends it. The zero value is not usable — build
// with a literal (Projects required).
type Watcher struct {
	Projects *projects.Store
	Notify   func(project string)
	Every    time.Duration // 拍距；0 = fsWatchEvery（测试注短拍）
	WalkCap  int           // 单项目单拍目录数帽；0 = fsWatchWalk（测试注小帽验降级）

	stop     chan struct{}
	stopOnce sync.Once

	mu     sync.Mutex
	stamps map[string]string // project key → 上一拍签名
}

// NewWatcher builds a watcher over the store. The stamps ledger starts
// empty: the first beat records every workspace WITHOUT notifying (进程
// 刚起，工作区「存在」不是新闻——从空 ledger 到首拍签名必然全量漂移，
// 放行就是一次全员假警报).
func NewWatcher(store *projects.Store, notify func(project string)) *Watcher {
	return &Watcher{Projects: store, Notify: notify, stop: make(chan struct{}), stamps: map[string]string{}}
}

// Loop runs the beat every w.Every until Stop. One goroutine for the
// watcher's whole life (Start 里 go 一把)；每拍骑 panic 栏——死了的
// watcher 是一块静默停摆的文件板（web.go watchLoop 同款教训）。
func (w *Watcher) Loop() {
	every := w.Every
	if every <= 0 {
		every = watchEvery
	}
	tick := time.NewTicker(every)
	defer tick.Stop()
	for {
		select {
		case <-w.stop:
			return
		case <-tick.C:
			util.Guard("fswatch: workspace watcher", w.beat)
		}
	}
}

// Stop ends the loop at the next beat boundary. Idempotent.
func (w *Watcher) Stop() {
	w.stopOnce.Do(func() { close(w.stop) })
}

// beat is one poll pass: re-sign every workspace, notify the movers,
// prune the ledger of keys the store no longer lists (项目除名后台账
// 不缩，一次除名漏一滴——紧着登记表收口).
func (w *Watcher) beat() {
	if w.Projects == nil {
		return
	}
	list := w.Projects.List()
	live := make(map[string]bool, len(list))
	for _, p := range list {
		if p.Workspace == "" {
			continue // 未立项工作区（草稿没填路径）：没树可看
		}
		live[p.Key] = true
		sig := w.signature(p.Workspace)
		w.mu.Lock()
		old, had := w.stamps[p.Key]
		w.stamps[p.Key] = sig
		w.mu.Unlock()
		if had && sig != old && w.Notify != nil {
			w.Notify(p.Key)
		}
	}
	w.mu.Lock()
	for k := range w.stamps {
		if !live[k] {
			delete(w.stamps, k)
		}
	}
	w.mu.Unlock()
}

// signature folds one workspace into a comparable string: every file's
// slash-relative path + mtime + size. A vanished tree contributes an
// empty signature (the change itself still broadcasts — 换库/清空也是
// 新闻). Symlinked directories are not descended (WalkDir 的既有口径，
// 与 web.go 签名一致)：链接目标树丢了监听，属已知降级。
func (w *Watcher) signature(root string) string {
	cap := w.WalkCap
	if cap <= 0 {
		cap = walkCap
	}
	var b strings.Builder
	dirs := 0
	capped := false
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // 断链/无权子树跳过——签名是尽力而为
		}
		if d.IsDir() {
			if dirs++; dirs > cap {
				capped = true
				return filepath.SkipAll
			}
			if p != root && SkipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		b.WriteString(filepath.ToSlash(strings.TrimPrefix(strings.TrimPrefix(p, root), string(filepath.Separator))))
		b.WriteByte('\x00')
		b.WriteString(info.ModTime().String())
		b.WriteByte('\x00')
		b.WriteString(strconv.FormatInt(info.Size(), 10))
		b.WriteByte('\x00')
		return nil
	})
	if capped {
		return "\x00capped"
	}
	return b.String()
}
