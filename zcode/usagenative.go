package zcode

// usagenative.go — the usage ledger's four read passes, ported verbatim
// from the retired python3+sqlite3 scripts: same SQL, same JSON
// shapes, same per-pass tolerance (any db error folds that pass to its
// empty answer, exactly as each script's blanket except did). The
// local-calendar bucketing (月长不一/周一锚) walks BACKWARDS from the
// current bucket like the script did — no fixed-step guessing.

import (
	"encoding/json"
	"sort"
	"time"
)

// jsonBytes marshals compact (json.dumps default) — the panels decode
// it, shape is the contract.
func jsonBytes(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}

// studioScopeSQL is the index side's studio-born scope, shared by the
// overview and windows passes (the purge arms minus the id arm).
const studioScopeSQL = "select task_id, title from tasks where deleted=0" +
	" and (json_extract(meta_json, '$.dhEmployee') = 1" +
	"  or title glob '【*】*-Lv.[0-9]*'" +
	"  or title glob '【*】小助手-*')"

// sessionGaugesJSON prints {session_id: gauge} for the given ids —
// turns/total over all rows; the context sample is the newest
// NON-ZERO-input turn (input_tokens sums a turn's every model request,
// so last_requests divides into an estimate; zero rows never mask the
// sample, a session of only zero rows still reports its newest time).
func sessionGaugesJSON(usageDB string, ids []string) []byte {
	type gauge struct {
		Turns        int64 `json:"turns"`
		LastInput    int64 `json:"last_input"`
		LastCache    int64 `json:"last_cache"`
		LastRequests int64 `json:"last_requests"`
		Total        int64 `json:"total"`
		LastAt       int64 `json:"last_at"`
	}
	g := map[string]*gauge{}
	db, err := openStoreDB(usageDB)
	if err == nil {
		defer db.Close()
		for _, part := range chunkIDs(ids, 400) {
			rows, qerr := db.Query(
				"select session_id, started_at, input_tokens,"+
					" cache_read_input_tokens, model_request_count,"+
					" computed_total_tokens"+
					" from turn_usage where session_id in ("+inMarks(len(part))+")",
				toAny(part)...)
			if qerr != nil {
				return jsonBytes(map[string]gauge{})
			}
			for rows.Next() {
				var sid string
				var started, inp, cache, reqs, total any
				if err := rows.Scan(&sid, &started, &inp, &cache, &reqs, &total); err != nil {
					rows.Close()
					return jsonBytes(map[string]gauge{})
				}
				e := g[sid]
				if e == nil {
					e = &gauge{}
					g[sid] = e
				}
				e.Turns++
				e.Total += i64(total)
				if v := i64(inp); v > 0 {
					if s := i64(started); s >= e.LastAt {
						e.LastAt = s
						e.LastInput = v
						e.LastCache = i64(cache)
						e.LastRequests = i64(reqs)
					}
				} else if e.LastAt == 0 {
					if s := i64(started); s >= e.LastAt {
						e.LastAt = s
					}
				}
			}
			rows.Close()
		}
	}
	out := make(map[string]gauge, len(g))
	for sid, e := range g {
		out[sid] = *e
	}
	return jsonBytes(out)
}

// i64 pulls an int64 out of a nullable sql value.
func i64(v any) int64 {
	switch x := v.(type) {
	case int64:
		return x
	case nil:
		return 0
	default:
		return 0
	}
}

// usageTurnsJSON prints one title's turns newest-first, capped at
// limit (1–500), each with its per-model split folded from
// model_usage (only for the turns shown).
func usageTurnsJSON(usageDB, indexDB, title string, limit int) []byte {
	if limit < 1 {
		limit = 1
	}
	if limit > 500 {
		limit = 500
	}
	type modelSplit struct {
		Model    string `json:"model"`
		Requests int64  `json:"requests"`
		Tokens   int64  `json:"tokens"`
	}
	sids := []string{}
	if db, err := openStoreDB(indexDB); err == nil {
		rows, qerr := db.Query(
			"select distinct task_id from tasks where deleted=0 and title=?", title)
		if qerr == nil {
			for rows.Next() {
				var sid string
				if rows.Scan(&sid) == nil && sid != "" {
					sids = append(sids, sid)
				}
			}
			rows.Close()
		}
		db.Close()
	}
	type turnRow struct {
		sid, tid, status                                            string
		started, dur, ttft, reqs, tool, in, out, reas, cache, total any
	}
	var turns []turnRow
	splits := map[string][]modelSplit{}
	if len(sids) > 0 {
		if db, err := openStoreDB(usageDB); err == nil {
			defer db.Close()
			for _, part := range chunkIDs(sids, 400) {
				rows, qerr := db.Query(
					"select session_id, turn_id, status, started_at,"+
						" duration_ms, time_to_first_token_ms,"+
						" model_request_count, tool_call_count, input_tokens,"+
						" output_tokens, reasoning_tokens,"+
						" cache_read_input_tokens, computed_total_tokens"+
						" from turn_usage where session_id in ("+inMarks(len(part))+")",
					toAny(part)...)
				if qerr != nil {
					return usageTurnsEmpty(title, len(sids))
				}
				for rows.Next() {
					var r turnRow
					if err := rows.Scan(&r.sid, &r.tid, &r.status, &r.started, &r.dur,
						&r.ttft, &r.reqs, &r.tool, &r.in, &r.out, &r.reas,
						&r.cache, &r.total); err != nil {
						rows.Close()
						return usageTurnsEmpty(title, len(sids))
					}
					turns = append(turns, r)
				}
				rows.Close()
			}
			sort.SliceStable(turns, func(i, j int) bool {
				return i64(turns[i].started) > i64(turns[j].started)
			})
			if len(turns) > limit {
				turns = turns[:limit]
			}
			var tids []string
			for _, t := range turns {
				tids = append(tids, t.tid)
			}
			for _, spart := range chunkIDs(sids, 400) {
				for _, tpart := range chunkIDs(tids, 400) {
					args := append(toAny(spart), toAny(tpart)...)
					rows, qerr := db.Query(
						"select turn_id, coalesce(model_id,'?'), count(*),"+
							" coalesce(sum(computed_total_tokens),0)"+
							" from model_usage where session_id in ("+inMarks(len(spart))+")"+
							" and turn_id in ("+inMarks(len(tpart))+") group by turn_id, model_id",
						args...)
					if qerr != nil {
						continue
					}
					for rows.Next() {
						var tid, model string
						var n, tok int64
						if rows.Scan(&tid, &model, &n, &tok) == nil {
							splits[tid] = append(splits[tid],
								modelSplit{Model: model, Requests: n, Tokens: tok})
						}
					}
					rows.Close()
				}
			}
		}
	}
	type turnJSON struct {
		SessionID     string       `json:"session_id"`
		TurnID        string       `json:"turn_id"`
		Status        string       `json:"status"`
		StartedAt     int64        `json:"started_at"`
		DurationMS    int64        `json:"duration_ms"`
		TTFTMS        int64        `json:"ttft_ms"`
		ModelRequests int64        `json:"model_requests"`
		ToolCalls     int64        `json:"tool_calls"`
		Input         int64        `json:"input"`
		Output        int64        `json:"output"`
		Reasoning     int64        `json:"reasoning"`
		CacheRead     int64        `json:"cache_read"`
		Total         int64        `json:"total"`
		Models        []modelSplit `json:"models"`
	}
	out := make([]turnJSON, 0, len(turns))
	for _, r := range turns {
		ms := splits[r.tid]
		if ms == nil {
			ms = []modelSplit{} // python 空列表 []，不是 null
		}
		sort.SliceStable(ms, func(i, j int) bool { return ms[i].Tokens > ms[j].Tokens })
		out = append(out, turnJSON{
			SessionID: r.sid, TurnID: r.tid, Status: strOr(r.status, ""),
			StartedAt: i64(r.started), DurationMS: i64(r.dur), TTFTMS: i64(r.ttft),
			ModelRequests: i64(r.reqs), ToolCalls: i64(r.tool),
			Input: i64(r.in), Output: i64(r.out), Reasoning: i64(r.reas),
			CacheRead: i64(r.cache), Total: i64(r.total), Models: ms,
		})
	}
	return jsonBytes(struct {
		Title    string     `json:"title"`
		Sessions int        `json:"sessions"`
		Turns    []turnJSON `json:"turns"`
	}{Title: title, Sessions: len(sids), Turns: out})
}

// usageTurnsEmpty is the pass's failure fold: the title and the
// session count survive, the turns list empties.
func usageTurnsEmpty(title string, sessions int) []byte {
	return jsonBytes(struct {
		Title    string `json:"title"`
		Sessions int    `json:"sessions"`
		Turns    []any  `json:"turns"`
	}{Title: title, Sessions: sessions, Turns: []any{}})
}

// strOr coalesces a nullable sql string.
func strOr(v any, fallback string) string {
	if s, ok := v.(string); ok {
		return s
	}
	return fallback
}

// ---- the overview pass -------------------------------------------------
//
// The drawer's whole diet in one pass: {"employees": […], "series":
// {hour, day, week, month, models}}. Grids are built by walking
// BACKWARDS from the current bucket — true local-calendar anchors
// (月长不一/周一锚/DST 不靠固定步长硬猜); rows landing outside a narrow
// grid's window simply skip. A dead usage db folds the aggregates to
// empty while the grids stay (the script's blanket except, kept).

type usageBucketRow struct {
	T    int64                `json:"t"`
	Turn int64                `json:"turns"`
	In   int64                `json:"input"`
	Out  int64                `json:"output"`
	Cach int64                `json:"cache"`
	Tot  int64                `json:"total"`
	Mod  map[string]*[2]int64 // model → {requests, tokens}（出列前折算）
}

func localMidnight(t time.Time) time.Time {
	t = t.In(time.Local)
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.Local)
}

// bucketStart is the script's bucket_of: the local-calendar start of
// the bucket ms falls in, at the given level.
func bucketStart(ms int64, level string) int64 {
	t := time.UnixMilli(ms).In(time.Local)
	switch level {
	case "hour":
		return time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), 0, 0, 0, time.Local).UnixMilli()
	case "month":
		return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.Local).UnixMilli()
	}
	day0 := localMidnight(t)
	if level == "day" {
		return day0.UnixMilli()
	}
	// week：周一锚——day0 回退 wday 天（python tm_wday 周一为 0）
	wday := (int(t.Weekday()) + 6) % 7
	return localMidnight(day0.AddDate(0, 0, -wday)).UnixMilli()
}

var usageLevels = []struct {
	name  string
	count int
}{
	{"hour", 48}, {"day", 30}, {"week", 12}, {"month", 12},
}

func usageOverviewJSON(usageDB, indexDB string) []byte {
	// 索引面：titles→sids（员工折叠）与 sids（趋势过滤）一趟读
	titles := map[string]map[string]bool{}
	sids := map[string]bool{}
	if db, err := openStoreDB(indexDB); err == nil {
		rows, qerr := db.Query(studioScopeSQL)
		if qerr == nil {
			for rows.Next() {
				var sid, title any
				if rows.Scan(&sid, &title) == nil {
					s, _ := sid.(string)
					t, _ := title.(string)
					if s != "" && t != "" {
						if titles[t] == nil {
							titles[t] = map[string]bool{}
						}
						titles[t][s] = true
						sids[s] = true
					}
				}
			}
			rows.Close()
		}
		db.Close()
	}
	now := time.Now().UnixMilli()
	grids := map[string][]int64{}
	where := map[string]map[int64]int{}
	for _, lv := range usageLevels {
		cur := bucketStart(now, lv.name)
		g := []int64{cur}
		for i := 1; i < lv.count; i++ {
			// 向后走一格：上一桶起点减 1ms 落到前一桶，再取其桶起点
			cur = bucketStart(cur-1, lv.name)
			g = append(g, cur)
		}
		// 反转为升序
		for i, j := 0, len(g)-1; i < j; i, j = i+1, j-1 {
			g[i], g[j] = g[j], g[i]
		}
		grids[lv.name] = g
		m := map[int64]int{}
		for i, t := range g {
			m[t] = i
		}
		where[lv.name] = m
	}
	minStart := int64(1<<62 - 1)
	for _, lv := range usageLevels {
		if g := grids[lv.name]; len(g) > 0 && g[0] < minStart {
			minStart = g[0]
		}
	}
	zeroAcc := func() map[string][]*usageBucketRow {
		acc := map[string][]*usageBucketRow{}
		for _, lv := range usageLevels {
			acc[lv.name] = make([]*usageBucketRow, len(grids[lv.name]))
			for i := range acc[lv.name] {
				acc[lv.name][i] = &usageBucketRow{T: grids[lv.name][i], Mod: map[string]*[2]int64{}}
			}
		}
		return acc
	}
	acc := zeroAcc()
	rowOf := func(ms int64, level string) *usageBucketRow {
		if i, ok := where[level][bucketStart(ms, level)]; ok {
			return acc[level][i]
		}
		return nil
	}
	type perAgg struct{ turns, in, out, reas, cache, total, last int64 }
	per := map[string]*perAgg{}
	grand := map[string]*[2]int64{}
	if db, err := openStoreDB(usageDB); err == nil {
		rows, qerr := db.Query(
			"select session_id, started_at, input_tokens, output_tokens," +
				" reasoning_tokens, cache_read_input_tokens," +
				" computed_total_tokens from turn_usage")
		if qerr != nil {
			per, grand = map[string]*perAgg{}, map[string]*[2]int64{}
			acc = zeroAcc()
		} else {
			for rows.Next() {
				var sid string
				var st, inp, outp, reas, cache, tot any
				if rows.Scan(&sid, &st, &inp, &outp, &reas, &cache, &tot) != nil {
					continue
				}
				r := per[sid]
				if r == nil {
					r = &perAgg{}
					per[sid] = r
				}
				r.turns++
				r.in += i64(inp)
				r.out += i64(outp)
				r.reas += i64(reas)
				r.cache += i64(cache)
				r.total += i64(tot)
				stMS := i64(st)
				if stMS > r.last {
					r.last = stMS
				}
				if sids[sid] && stMS >= minStart {
					for _, lv := range usageLevels {
						if row := rowOf(stMS, lv.name); row != nil {
							row.Turn++
							row.In += i64(inp)
							row.Out += i64(outp)
							row.Cach += i64(cache)
							row.Tot += i64(tot)
						}
					}
				}
			}
			rows.Close()
			mrows, qerr := db.Query(
				"select session_id, coalesce(model_id,'?'), started_at," +
					" computed_total_tokens from model_usage")
			if qerr == nil {
				for mrows.Next() {
					var sid, model string
					var st, tot any
					if mrows.Scan(&sid, &model, &st, &tot) != nil {
						continue
					}
					if !sids[sid] {
						continue
					}
					e := grand[model]
					if e == nil {
						e = &[2]int64{}
						grand[model] = e
					}
					e[0]++
					e[1] += i64(tot)
					if s := i64(st); s >= minStart {
						for _, lv := range usageLevels {
							if row := rowOf(s, lv.name); row != nil {
								b := row.Mod[model]
								if b == nil {
									b = &[2]int64{}
									row.Mod[model] = b
								}
								b[0]++
								b[1] += i64(tot)
							}
						}
					}
				}
				mrows.Close()
			}
		}
		db.Close()
	}
	type empJSON struct {
		Title      string `json:"title"`
		Sessions   int    `json:"sessions"`
		Turns      int64  `json:"turns"`
		Input      int64  `json:"input"`
		Output     int64  `json:"output"`
		Reasoning  int64  `json:"reasoning"`
		CacheRead  int64  `json:"cache_read"`
		Total      int64  `json:"total"`
		LastActive int64  `json:"last_active"`
	}
	emps := []empJSON{}
	for title, ss := range titles {
		a := empJSON{Title: title, Sessions: len(ss)}
		for sid := range ss {
			if r := per[sid]; r != nil {
				a.Turns += r.turns
				a.Input += r.in
				a.Output += r.out
				a.Reasoning += r.reas
				a.CacheRead += r.cache
				a.Total += r.total
				if r.last > a.LastActive {
					a.LastActive = r.last
				}
			}
		}
		emps = append(emps, a)
	}
	sort.SliceStable(emps, func(i, j int) bool { return emps[i].Total > emps[j].Total })
	type modelJSON struct {
		Model    string `json:"model"`
		Requests int64  `json:"requests"`
		Tokens   int64  `json:"tokens"`
	}
	series := map[string]any{}
	for _, lv := range usageLevels {
		type pointJSON struct {
			T     int64       `json:"t"`
			Turn  int64       `json:"turns"`
			In    int64       `json:"input"`
			Out   int64       `json:"output"`
			Cach  int64       `json:"cache"`
			Tot   int64       `json:"total"`
			Model []modelJSON `json:"models"`
		}
		pts := make([]pointJSON, 0, len(acc[lv.name]))
		for _, row := range acc[lv.name] {
			ms := make([]modelJSON, 0, len(row.Mod))
			for k, v := range row.Mod {
				ms = append(ms, modelJSON{Model: k, Requests: v[0], Tokens: v[1]})
			}
			sort.SliceStable(ms, func(i, j int) bool { return ms[i].Tokens > ms[j].Tokens })
			pts = append(pts, pointJSON{T: row.T, Turn: row.Turn, In: row.In,
				Out: row.Out, Cach: row.Cach, Tot: row.Tot, Model: ms})
		}
		series[lv.name] = map[string]any{"bucket": lv.name, "points": pts}
	}
	rank := make([]modelJSON, 0, len(grand))
	for k, v := range grand {
		rank = append(rank, modelJSON{Model: k, Requests: v[0], Tokens: v[1]})
	}
	sort.SliceStable(rank, func(i, j int) bool { return rank[i].Tokens > rank[j].Tokens })
	if len(rank) > 24 {
		rank = rank[:24]
	}
	series["models"] = rank
	return jsonBytes(struct {
		Employees []empJSON      `json:"employees"`
		Series    map[string]any `json:"series"`
	}{Employees: emps, Series: series})
}

// ---- the windows pass --------------------------------------------------

// usageWindowsJSON prints {"day": D, "week": W} — the studio's
// computed_total_tokens summed over turns started at/after each
// anchor, scoped to studio-born sessions.
func usageWindowsJSON(usageDB, indexDB string, dayMS, weekMS int64) []byte {
	sids := map[string]bool{}
	if db, err := openStoreDB(indexDB); err == nil {
		rows, qerr := db.Query("select task_id from tasks where deleted=0" +
			" and (json_extract(meta_json, '$.dhEmployee') = 1" +
			"  or title glob '【*】*-Lv.[0-9]*'" +
			"  or title glob '【*】小助手-*')")
		if qerr == nil {
			for rows.Next() {
				var sid any
				if rows.Scan(&sid) == nil {
					if s, ok := sid.(string); ok && s != "" {
						sids[s] = true
					}
				}
			}
			rows.Close()
		}
		db.Close()
	}
	out := map[string]int64{"day": 0, "week": 0}
	if len(sids) > 0 {
		if db, err := openStoreDB(usageDB); err == nil {
			for _, a := range []struct {
				anchor int64
				key    string
			}{{weekMS, "week"}, {dayMS, "day"}} {
				rows, qerr := db.Query(
					"select session_id, sum(computed_total_tokens)"+
						" from turn_usage where started_at >= ?"+
						" group by session_id", a.anchor)
				if qerr != nil {
					continue
				}
				for rows.Next() {
					var sid string
					var sum any
					if rows.Scan(&sid, &sum) == nil && sids[sid] {
						out[a.key] += i64(sum)
					}
				}
				rows.Close()
			}
			db.Close()
		}
	}
	return jsonBytes(out)
}
