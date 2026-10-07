package main

// size_arch_test.go — 规模护栏（组合根持有）：layering 钉 import 方向，
// 这一份钉另一个维度——包内规模。铁律管的是「谁可以引谁」，管不住
// 「一个包/一个文件长到多大」；server 三万五千行的平铺就是这么长出来的。
// 两条规则：
//   1. 文件上限：非测试、非生成的 .go 文件 ≤ 1200 行。历史超标件记在
//      oversizedAllowlist 里钉住现值——只能缩、不能涨；缩回线下后请删行。
//   2. 包预算：每个包的非测试行数 ≤ packageBudgets 里的显式预算
//     （立表时实测 +5% 余量）。超了＝「拆子系统」或「在这里显式上调」，
//      二者都是走评审的决定，不是无声漂移。新包出生必须同时在这里
//      加行——测试会拦住忘记加的那次提交。
// 生成物（Code generated ... DO NOT EDIT 头）豁免两条规则：它镜像手写
// 的真源，长度不由自己决定。tools/（开发工具链）与 web/（前端，自有
// 测试面）不参与统计。

import (
	"bufio"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

const fileSizeCeiling = 1200

// oversizedAllowlist: files above the ceiling, pinned at their size when
// this guardrail was introduced. A row means "known giant, on the books" —
// the number may only go down (split or shrink it); growing past the
// pinned size fails even though the row exists.
var oversizedAllowlist = map[string]int{
	"tasks/tasks.go":     1963, // Engine 域逻辑主体；拆缝候选：仲裁/提案/结构校验（sqlite 转正后重钉）
	"pixart/room.go":     1831,
	"pixart/wardrobe.go": 1323,
}

// packageBudgets: non-test Go lines per package directory (measured at
// introduction + ~5% headroom). Over budget = split the subsystem or
// consciously raise the number HERE (both are reviewed decisions).
// A budget row whose directory has no non-test .go files is stale and
// also fails — the table stays honest through splits and moves.
var packageBudgets = map[string]int{
	".":                2800, // 组合根（main + boot_*）
	"server":           21100,
	"server/verbs":     1100,
	"server/httputil":  200, // 家族共享写面（writeJSON 一族＋withinDir＋图片扩展表）
	"server/fsview":    500, // 文件浏览器域面（fsview/fsbrowse 外迁）
	"server/fswatch":   220, // 工作区变更推送面（轮询签名 watcher——文件板自动刷新的服务端半边，黑名单与 fsview 共享）
	"server/skills":    1500,
	"server/gitops":    2600,
	"server/roomops":   3700,
	"server/plans":     900,
	"server/autopilot": 2000,
	"server/auth":      900, // 身份/信任域（auth/ownertoken/visitor＋verbRole 矩阵） // 全智能模式域（引擎＋预算政策核＋autopilot_done 动词） // 计划/需求/会议评审域（plan/reqs/meetings＋评审门动词） // 房间生命周期域（establishment/offboard/purge/kick/rank/watch/pause/resume/rebuild/retention/archive/project_ops/reset＋管理动词） // git 域（gitops/gitflow/gitboot/handlers＋合并评审门动词） // 能力装配域（skills/plugins/market＋域动词）
	"server/achv":      500, // 成就域引擎（判定核心＋奖章表＋持久化/广播引擎） // 动词契约层（拆族首站：信封/注册表/StoreSet/FleetAPI/Seat＋六族动词）
	"dispatch":         12100,
	"cli":              7300,
	"chat":             6000,
	"pixart":           4800,
	"zcode":            5080, // 4480→5080：python3+sqlite3 接缝全体退役（13 段内嵌脚本→modernc 原生，desktopdb/usagenative 实体长于平文本脚本） // 发现层九 tier 长成于三日内（装机契约重写/Electron 配对/注册表双查/快捷方式/进程/全盘搜文件），职责实长如实上调：4250→4450（全盘 zcode.cjs 搜索 tier）→4480（python3 接缝跨平台选名 util.PythonBin 带进 3 行 import——Windows 的 python3.exe 执行别名起不来）
	"tasks":            3300,
	"kb":               3000,
	"sqlstore":         2100,
	"vcs":              1600,
	"i18n":             1450,
	"staffing":         1320,
	"assistant":        1230,
	"plugins":          1160,
	"wire":             1120,
	"capability":       1030,
	"storetest":        1400, // sqlite 转正重基（四域两实现套件随引擎化扩面）
	"util":             960,
	"projects":         920,
	"requirements":     1015, // sqlite 转正重基
	"meeting":          890,  // sqlite 转正重基
	"agents":           820,
	"identity":         650,
	"plan":             670, // sqlite 转正重基
	"recruit":          530,
	"shell":            520,
	"shell/extlink":    80,  // 外链跳系统浏览器面（open_external 自 shell 外迁——541>520，零逻辑搬家）
	"merge":            435, // sqlite 转正重基
	"webview":          360,
	"notice":           290, // sqlite 转正重基
	"media":            180,
	"persist":          100, // 80→100：Save 按路径串行（Windows 并发 rename 撞句柄拒 Access Denied）带进锁表与注释
}

// skipDirs: frontend (own test face), docs, dev tooling, VCS metadata.
var skipDirs = map[string]bool{
	".git": true, "web": true, "docs": true, "tools": true, "node_modules": true,
}

// isGenerated reports whether the file carries the standard generated
// marker (convention: "// Code generated ... DO NOT EDIT.").
func isGenerated(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for i := 0; i < 10 && sc.Scan(); i++ {
		line := sc.Text()
		if strings.HasPrefix(line, "// Code generated") && strings.HasSuffix(line, "DO NOT EDIT.") {
			return true
		}
	}
	return false
}

// countGo walks the repo and returns two views: per-file non-test non-
// generated .go line counts, keyed by repo-relative path, and the set of
// directories holding those files (a directory with Go files is a package
// — it owes a budget row).
func countGo() (files map[string]int, dirs map[string]bool, err error) {
	files, dirs = map[string]int{}, map[string]bool{}
	err = filepath.WalkDir(".", func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := d.Name()
		if d.IsDir() {
			if path != "." && (skipDirs[name] || strings.HasPrefix(name, ".")) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			return nil
		}
		if isGenerated(path) {
			return nil
		}
		n := 0
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			n++
		}
		f.Close()
		if err := sc.Err(); err != nil {
			return err
		}
		files[filepath.ToSlash(path)] = n // 预算表/上限表以 "/" 记键——Windows 检出时 WalkDir 吐 "\"，归一后三平台同一张表
		dirs[filepath.ToSlash(filepath.Dir(path))] = true
		return nil
	})
	return files, dirs, err
}

// TestFileSizesStayUnderCeiling: no non-test source file may exceed the
// ceiling; allowlisted giants may not grow past their pinned size; rows
// whose file has shrunk back under the ceiling must be removed.
func TestFileSizesStayUnderCeiling(t *testing.T) {
	files, _, err := countGo()
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		n := files[p]
		if n <= fileSizeCeiling {
			if pinned, listed := oversizedAllowlist[p]; listed && pinned > fileSizeCeiling {
				t.Errorf("%s 已缩回 %d 行（上限 %d）——请从 oversizedAllowlist 删除该行，棘轮只收不涨", p, n, fileSizeCeiling)
			}
			continue
		}
		pinned, listed := oversizedAllowlist[p]
		switch {
		case !listed:
			t.Errorf("%s 达 %d 行，超上限 %d——按「拆自」纪律拆文件（零逻辑编辑），或 oversizedAllowlist 加行钉住现值（走评审）", p, n, fileSizeCeiling)
		case n > pinned:
			t.Errorf("%s 从钉住的 %d 行涨到 %d 行——超标件只许缩不许涨：先拆再改", p, pinned, n)
		}
	}
	for p := range oversizedAllowlist {
		if _, still := files[p]; !still {
			t.Errorf("oversizedAllowlist 的 %s 已不存在（改名或删除后请同步删行）", p)
		}
	}
}

// TestPackageBudgetsHold: every package directory owes an explicit budget
// row, and no package may exceed its budget.
func TestPackageBudgetsHold(t *testing.T) {
	files, dirs, err := countGo()
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	totals := map[string]int{}
	for p, n := range files {
		totals[filepath.Dir(p)] += n
	}
	for dir := range dirs {
		budget, listed := packageBudgets[dir]
		if !listed {
			t.Errorf("包 %s 没有预算行（实测 %d 行）——新包出生即在 packageBudgets 加行：这是一次可评审的规模决策", dir, totals[dir])
			continue
		}
		if totals[dir] > budget {
			t.Errorf("包 %s 实测 %d 行超预算 %d——拆子系统，或在此显式上调预算（走评审）；巨石是这么长出来的，别让它无声发生", dir, totals[dir], budget)
		}
	}
	for dir := range packageBudgets {
		if _, live := dirs[dir]; !live {
			t.Errorf("packageBudgets 的 %s 已无 Go 文件（拆分/搬迁后请同步删行）", dir)
		}
	}
}
