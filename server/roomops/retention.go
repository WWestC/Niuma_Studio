package roomops

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/i18n"
	"github.com/WWestC/Niuma_Studio/server/httputil"
)

const (
	// RetentionEvery is the sweep beat: the policy is day-granular, a
	// slow clock keeps the disk honest without rescanning hot files.
	RetentionEvery = 6 * time.Hour
	// RetentionMaxDays bounds a custom retention (10 years — beyond
	// that the honest answer is 永久, not a bigger number).
	RetentionMaxDays = 3650
)

// retentionPath is the policy's slot under the v2 root.
func (rc *Face) retentionPath() string {
	return filepath.Join(rc.Registry.Root(), "retention.json")
}

func (rc *Face) HandleRetention(w http.ResponseWriter, r *http.Request) {
	if rc.Registry == nil || rc.Registry.Root() == "" {
		http.NotFound(w, r)
		return
	}
	switch r.Method {
	case http.MethodGet:
		cfg := chat.LoadRetention(rc.retentionPath())
		httputil.WriteJSON(w, map[string]any{"days": cfg.Days, "default_days": chat.DefaultRetentionDays})
	case http.MethodPost:
		var body struct {
			Days int `json:"days"`
		}
		data, err := io.ReadAll(io.LimitReader(r.Body, 1<<20)) // 安全核查修复：有界读取
		if err == nil && len(data) > 0 {
			err = json.Unmarshal(data, &body)
		} else if err == nil {
			err = errors.New(i18n.S("载荷缺失"))
		}
		if err != nil {
			httputil.WriteJSONErr(w, http.StatusBadRequest, i18n.Sf("载荷解析失败: %s", err.Error()))
			return
		}
		if body.Days != 0 && (body.Days < 1 || body.Days > RetentionMaxDays) {
			httputil.WriteJSONErr(w, http.StatusBadRequest,
				i18n.S("days 需为 0（永久）或 1–3650 天——更久的历史请选永久"))
			return
		}
		if err := chat.SaveRetention(rc.retentionPath(), body.Days); err != nil {
			httputil.WriteJSONErr(w, http.StatusInternalServerError, i18n.Sf("保留期写入失败: %s", err.Error()))
			return
		}
		pruned := rc.SweepRetention(time.Now()) // the write is the sweep trigger too
		httputil.WriteJSON(w, map[string]any{"days": body.Days, "pruned": pruned})
	default:
		http.Error(w, "GET/POST only", http.StatusMethodNotAllowed)
	}
}

// sweepRetention prunes every room's history per the saved policy
// (nothing on 永久) and returns the total dropped count. Live rooms go
// through their Hub (locked against appends); hub-less files go
// direct. Errors log and continue — one unreadable file must never
// stop the sweep.
func (rc *Face) SweepRetention(now time.Time) int {
	reg := rc.Registry
	if reg == nil || reg.Root() == "" {
		return 0
	}
	cfg := chat.LoadRetention(rc.retentionPath())
	if cfg.Days <= 0 {
		return 0 // 永久
	}
	keepSince := now.Unix() - int64(cfg.Days)*86400
	dropped := 0
	live := map[string]bool{}
	for key, h := range reg.Rooms() {
		live[key] = true
		dropped += h.PruneHistory(keepSince)
		dropped += h.PruneTerm(keepSince) // r_17：终端转录与聊天记录同窗
	}
	// 终端转录的 hub-less 房间照扫（terminal/<key>/*.jsonl，整册走
	// PruneTermFile 的逐行保新写回）。
	if rooms, err := os.ReadDir(filepath.Join(reg.Root(), "terminal")); err == nil {
		for _, room := range rooms {
			if !room.IsDir() || live[room.Name()] {
				continue // live 房已在锁内清过
			}
			files, err := os.ReadDir(filepath.Join(reg.Root(), "terminal", room.Name()))
			if err != nil {
				continue
			}
			for _, f := range files {
				if !f.IsDir() && strings.HasSuffix(f.Name(), ".jsonl") {
					dropped += chat.PruneTermFile(
						filepath.Join(reg.Root(), "terminal", room.Name(), f.Name()), keepSince)
				}
			}
		}
	}
	entries, err := os.ReadDir(filepath.Join(reg.Root(), "history"))
	if err != nil {
		return dropped // no history dir: nothing hub-less to prune
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".jsonl") {
			continue // the reads/acks/reacts ledgers ride other suffixes
		}
		key := strings.TrimSuffix(name, ".jsonl")
		if live[key] {
			continue // its Hub already pruned under the lock
		}
		dropped += chat.PruneHistoryFile(filepath.Join(reg.Root(), "history", name), keepSince)
	}
	// 一日回放帧（r_32）：day 粒度的整文件删（文件名日期 < 保留界即删，
	// 与聊天同一旋钮；live 与否无关——append 走 replayStore 自己的锁）。
	// nil-safe（裸 Server 直扫，跳过该腿）。
	if rc.Replays != nil {
		dropped += rc.Replays.Prune(reg.Root(), time.Unix(keepSince, 0).Format("20060102"))
	}
	if dropped > 0 {
		log.Printf("retention: 按保留 %d 天清理了 %d 条历史消息", cfg.Days, dropped)
	}
	return dropped
}

// retentionLoop sweeps at boot then once per beat until Close stops it.
func (rc *Face) retentionLoop(every time.Duration) {
	rc.SweepRetention(time.Now())
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-rc.retentionStop:
			return
		case <-t.C:
			rc.SweepRetention(time.Now())
		}
	}
}
