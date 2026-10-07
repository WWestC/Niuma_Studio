package main

// boot_multiuser_test.go — 身份层迁移的验收钉：构造旧版（JSON 时代）
// 数据目录 → 多用户启动 → 断言全部存量直导 admin 命名空间、admin 账
// 户在册可登录、房间文件（历史/编制/收件箱的载体）原封不动；二次启
// 动幂等（零行移动、无重复导入）。

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/identity"
	"github.com/WWestC/Niuma_Studio/requirements"
	"github.com/WWestC/Niuma_Studio/sqlstore"
	"github.com/WWestC/Niuma_Studio/tasks"
	"github.com/WWestC/Niuma_Studio/util"
)

// seedJSONEra writes the pre-multiuser data shape under root: the
// four ledger families' JSON shelves, the flat ranks file, and one
// history file (the room-file witness migration must not touch).
func seedJSONEra(t *testing.T, home string) {
	t.Helper()
	root := filepath.Join(home, ".niuma")
	mkdir := func(dir string) {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write := func(path, body string) {
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mkdir(filepath.Join(root, "requirements"))
	write(filepath.Join(root, "requirements", "demo.json"),
		`{"next":2,"reqs":[{"id":"r_01","project_key":"demo","title":"种子需求","status":"open","created_by":"testowner","created_ts":100}]}`)
	mkdir(filepath.Join(root, "plan"))
	write(filepath.Join(root, "plan", "demo.json"), `{"next":1}`)
	mkdir(filepath.Join(root, "merge"))
	write(filepath.Join(root, "merge", "demo.json"), `{"next":1}`)
	mkdir(filepath.Join(root, "tasks"))
	write(filepath.Join(root, "tasks", "demo.json"),
		`{"next":2,"tasks":[{"id":"t_01","title":"种子任务","project_key":"demo","status":"todo","created_by":"testowner"}]}`)
	write(filepath.Join(home, ".niuma_ranks.json"), `{"小明":5}`)
	// 房间文件见证：历史一行——迁移不得动房间的任何文件。
	mkdir(filepath.Join(root, "history"))
	write(filepath.Join(root, "history", "demo.jsonl"),
		`{"type":"say","from":"testowner","text":"旧纪元的一行","ts":100,"seq":1}`+"\n")
}

func TestBootMultiUserMigration(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // Windows 的 os.UserHomeDir 读 USERPROFILE——两侧同设，家目录才落进测试沙箱
	t.Setenv("NIUMA_MULTIUSER", "1")
	t.Setenv("USERNAME", "testowner")
	t.Setenv("USER", "testowner")
	util.MigrateLegacyPaths()
	seedJSONEra(t, home)

	projStore := openProjectStore()
	lobbyWS := stableWorkspace()
	if _, err := projStore.EnsureLobby(lobbyWS); err != nil {
		t.Fatalf("lobby: %v", err)
	}
	registry := chat.NewRegistry(projStore, v2Root(), defaultName(), "人类玩家")
	if _, err := registry.AdoptActive(); err != nil {
		t.Fatalf("adopt: %v", err)
	}
	hub, err := registry.Lobby()
	if err != nil {
		t.Fatalf("lobby hub: %v", err)
	}

	st := openStoresT(t, projStore, registry, hub, "人类玩家")
	subject := st.ident.subject

	// 1. 房主升格 admin、主体即账户名、服务已接（可登录）。
	if subject != "testowner" {
		t.Fatalf("工作室主体应为 admin 账户名: %q", subject)
	}
	accounts := st.ledgerDB.Identity().List()
	if len(accounts) != 1 || accounts[0].Username != "testowner" || accounts[0].Role != identity.RoleAdmin {
		t.Fatalf("admin 账户应已建: %+v", accounts)
	}

	// 2. 存量全部归位 admin ns（需求/任务架/等级）；'' 侧为空。
	reqs := st.reqs.List(subject)
	if len(reqs) != 1 || reqs[0].ID != "r_01" || reqs[0].Title != "种子需求" {
		t.Fatalf("需求应归位 admin ns: %+v", reqs)
	}
	if shelves2, _ := st.ledgerDB.ReqShelves().OpenShelves(""); len(shelves2) != 0 {
		t.Fatalf("'' 侧不应有余留: %+v", shelves2)
	}
	shelves, ranks, err := st.ledgerDB.Tasks().OpenShelves(subject)
	if err != nil || len(shelves) != 1 || len(shelves["demo"].Tasks) != 1 || shelves["demo"].Tasks[0].ID != "t_01" {
		t.Fatalf("任务架应归位 admin ns: %+v %v", shelves, err)
	}
	if ranks["小明"] != 5 {
		t.Fatalf("等级表应归位 admin ns: %v", ranks)
	}
	if _, ranks2, _ := st.ledgerDB.Tasks().OpenShelves(""); ranks2["小明"] != 0 {
		t.Fatalf("'' 侧等级不应有余留: %v", ranks2)
	}

	// 3. 引擎绑定工作室主体且看得到种子任务。
	if st.engine.Subject() != subject {
		t.Fatalf("引擎应绑定工作室主体: %q", st.engine.Subject())
	}
	if got := st.engine.Count(); got != 1 {
		t.Fatalf("引擎应见种子任务: %d", got)
	}
	if _, ok := st.engine.GetIn("demo", "t_01"); !ok {
		t.Fatal("项目内应解析到 t_01")
	}

	// 4. 首启凭据文件（0600、两行）可真实登录。
	credPath := filepath.Join(home, ".niuma_admin_credentials")
	b, err := os.ReadFile(credPath)
	if err != nil {
		t.Fatalf("首启凭据应落盘: %v", err)
	}
	// Windows 的权限位由目录 ACL 承担（Chmod 只认只读位、常规文件显示 0666）
	if fi, _ := os.Stat(credPath); fi.Mode().Perm() != 0o600 && runtime.GOOS != "windows" {
		t.Fatalf("凭据文件应 0600: %v", fi.Mode())
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(lines) != 2 || lines[0] != "testowner" {
		t.Fatalf("凭据文件形状: %q", string(b))
	}
	sess, err := st.ident.auth.Login("test", "testowner", lines[1])
	if err != nil || sess.Role != identity.RoleAdmin {
		t.Fatalf("首启凭据应能登录 admin: %v %+v", err, sess)
	}
	// 错密码拒绝（不可枚举）。
	if _, err := st.ident.auth.Login("test", "testowner", "wrong"); err == nil {
		t.Fatal("错密码应拒绝")
	}

	// 5. 服务令牌文件（0600）在位。
	tokPath := filepath.Join(home, ".niuma_service_token")
	tb, err := os.ReadFile(tokPath)
	if err != nil || len(strings.TrimSpace(string(tb))) != 32 {
		t.Fatalf("服务令牌文件形状: %v %q", err, string(tb))
	}
	// Windows 的权限位由目录 ACL 承担（同上：Chmod 只认只读位、显示恒 0666）
	if fi, _ := os.Stat(tokPath); fi.Mode().Perm() != 0o600 && runtime.GOOS != "windows" {
		t.Fatalf("服务令牌应 0600: %v", fi.Mode())
	}

	// 6. 房间文件原封不动（历史一行还在——房间可进、纪元不丢）。
	hb, err := os.ReadFile(filepath.Join(v2Root(), "history", "demo.jsonl"))
	if err != nil || !strings.Contains(string(hb), "旧纪元的一行") {
		t.Fatalf("房间历史文件应原封不动: %v", err)
	}

	// —— 二次启动幂等：账户在册 → 不再导入、不再移动行、无重复 ——
	st2 := openStoresT(t, projStore, registry, hub, "人类玩家")
	if st2.ident.subject != subject {
		t.Fatalf("二次启动主体应稳定: %q", st2.ident.subject)
	}
	if got := st2.ledgerDB.Identity().List(); len(got) != 1 {
		t.Fatalf("二次启动不应重复建账户: %+v", got)
	}
	if got := st2.reqs.List(subject); len(got) != 1 {
		t.Fatalf("二次启动不应重复导入需求: %+v", got)
	}
	if got := st2.engine.Count(); got != 1 {
		t.Fatalf("二次启动不应重复导入任务: %d", got)
	}
	if _, ok := st2.engine.GetIn("demo", "t_01"); !ok {
		t.Fatal("二次启动后任务仍可解析")
	}
}

// TestBootMultiUserSQLiteEraRehome — 单用户 sqlite 时期（ns=” 存量）
// 直接开多用户：ns=” 行收拢进 admin 命名空间（不动 JSON——桥的表空
// 门按目标主体判，JSON 取证副本不重导）。
func TestBootMultiUserSQLiteEraRehome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // Windows 的 os.UserHomeDir 读 USERPROFILE——两侧同设，家目录才落进测试沙箱
	t.Setenv("NIUMA_MULTIUSER", "1")
	t.Setenv("USERNAME", "testowner")
	t.Setenv("USER", "testowner")
	util.MigrateLegacyPaths()

	// 单用户 sqlite 先跑一轮（JSON 时代种子 → ns='' 落库）。
	seedJSONEra(t, home)
	{
		t.Setenv("NIUMA_MULTIUSER", "0")
		projStore := openProjectStore()
		lobbyWS := stableWorkspace()
		if _, err := projStore.EnsureLobby(lobbyWS); err != nil {
			t.Fatal(err)
		}
		registry := chat.NewRegistry(projStore, v2Root(), defaultName(), "人类玩家")
		_, _ = registry.AdoptActive()
		hub, err := registry.Lobby()
		if err != nil {
			t.Fatal(err)
		}
		st := openStoresT(t, projStore, registry, hub, "人类玩家")
		if st.ident.subject != "" {
			t.Fatalf("单用户应无主体: %q", st.ident.subject)
		}
		if got := st.reqs.List(""); len(got) != 1 {
			t.Fatalf("单用户 sqlite 存量应落 ns='': %+v", got)
		}
	}

	// 多用户启动：ns='' 收拢进 admin。
	t.Setenv("NIUMA_MULTIUSER", "1")
	projStore := openProjectStore()
	registry := chat.NewRegistry(projStore, v2Root(), defaultName(), "人类玩家")
	_, _ = registry.AdoptActive()
	hub, err := registry.Lobby()
	if err != nil {
		t.Fatal(err)
	}
	st := openStoresT(t, projStore, registry, hub, "人类玩家")
	if st.ident.subject != "testowner" {
		t.Fatalf("多用户主体: %q", st.ident.subject)
	}
	if got := st.reqs.List("testowner"); len(got) != 1 || got[0].ID != "r_01" {
		t.Fatalf("ns='' 存量应收拢进 admin ns: %+v", got)
	}
	if shelves2, _ := st.ledgerDB.ReqShelves().OpenShelves(""); len(shelves2) != 0 {
		t.Fatalf("'' 侧应收干: %+v", shelves2)
	}
	if got := st.engine.Count(); got != 1 {
		t.Fatalf("任务应收拢可见: %d", got)
	}
	_ = requirements.StatusOpen
	_ = sqlstore.Open
	_ = tasks.StatusTodo
}
