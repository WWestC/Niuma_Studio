package kb

import "testing"

// Doc-key scoping (v2.9): the three tiers' grammar (公共 / 大厅 ops/
// 私档 / 项目 p/<key>/) and the index's Project filter.

func TestKeyProject(t *testing.T) {
	cases := map[string]string{
		"readme":               "",
		"roles/hr":             "",
		"ops/chronicle":        "",
		"p/book/spec":          "book",
		"p/default/establishm": "default",
		"p/a/b/c":              "a",
		"p/book":               "book", // prefix without leaf: still book's shelf
	}
	for key, want := range cases {
		if got := KeyProject(key); got != want {
			t.Errorf("KeyProject(%q) = %q, want %q", key, got, want)
		}
	}
}

func TestKeyRoom(t *testing.T) {
	cases := map[string]string{
		"readme":                  "",        // 公共
		"roles/hr":                "",        // 公共
		"design/r31-parking":      "",        // 公共
		"ops/chronicle":           "default", // 大厅私档
		"ops/establishment":       "default",
		"ops/meetings/r_99-1":     "default",
		"ops/room-log":            "default",
		"p/book/spec":             "book",
		"p/book/meetings/r_25-1":  "book",
		"p/default/establishment": "default",
	}
	for key, want := range cases {
		if got := KeyRoom(key); got != want {
			t.Errorf("KeyRoom(%q) = %q, want %q", key, got, want)
		}
	}
}

func TestKeyInScope(t *testing.T) {
	if !KeyInScope("readme", "book") || !KeyInScope("roles/hr", "book") {
		t.Fatal("公共键在任何范围可见")
	}
	if !KeyInScope("p/book/spec", "book") {
		t.Fatal("本房键在本房可见")
	}
	if KeyInScope("p/novel/spec", "book") {
		t.Fatal("别家键不可见")
	}
	// ops/ 是大厅的房档：项目读不到，大厅自己读得到
	if KeyInScope("ops/chronicle", "book") || KeyInScope("ops/establishment", "book") {
		t.Fatal("Niuma_Studio 私档对项目不可见")
	}
	if !KeyInScope("ops/chronicle", "default") || !KeyInScope("p/default/x", "default") {
		t.Fatal("Niuma_Studio 范围＝公共＋ops/＋p/default/")
	}
	if KeyInScope("p/book/x", "default") {
		t.Fatal("Niuma_Studio 不见项目架——Niuma_Studio 也是独立一间房")
	}
	if !KeyInScope("p/anything/x", "") || !KeyInScope("ops/x", "") {
		t.Fatal("空范围（全室面）一切可见")
	}
}

func TestProjectDocKey(t *testing.T) {
	if got := ProjectDocKey("book", "spec"); got != "p/book/spec" {
		t.Fatalf("ProjectDocKey = %q", got)
	}
}

func TestListOptProjectFilter(t *testing.T) {
	store, err := OpenDocs(t.TempDir())
	if err != nil {
		t.Fatalf("OpenDocs: %v", err)
	}
	for _, key := range []string{"readme", "p/book/spec", "p/novel/spec", "ops/chronicle"} {
		if _, err := store.Write(key, key, "body", "房主"); err != nil {
			t.Fatalf("seed %s: %v", key, err)
		}
	}
	got := store.ListOpt(ListOptions{Project: "book"})
	keys := map[string]bool{}
	for _, m := range got {
		keys[m.Key] = true
	}
	if !keys["readme"] || !keys["p/book/spec"] {
		t.Fatalf("book 架应含公共 readme＋本房 p/book/spec：%v", keys)
	}
	if keys["p/novel/spec"] {
		t.Fatalf("别家架不进：%v", keys)
	}
	if keys["ops/chronicle"] {
		t.Fatalf("Niuma_Studio 私档不进项目架：%v", keys)
	}
	if hall := store.ListOpt(ListOptions{Project: "default"}); len(hall) != 2 {
		t.Fatalf("Niuma_Studio 架＝公共 readme＋ops/chronicle，got %d 篇", len(hall))
	}
	if all := store.ListOpt(ListOptions{}); len(all) != 4 {
		t.Fatalf("无范围＝全量，got %d", len(all))
	}
}

func TestValidateKeyFourthLevelUnderProjectPrefix(t *testing.T) {
	for _, ok := range []string{"roles/hr", "p/book/meetings/r_25-1", "p/a/b/c/d"} { // p/ 前缀可到四级
		if err := ValidateKey(ok); err != nil {
			t.Errorf("ValidateKey(%q) 不该拒：%v", ok, err)
		}
	}
	for _, bad := range []string{"a/b/c/d", "roles/a/b/c"} { // 公共键仍限三级
		if err := ValidateKey(bad); err == nil {
			t.Errorf("ValidateKey(%q) 应拒（公共键最多三级）", bad)
		}
	}
}
