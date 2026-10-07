package sqlstore

// db_corrupt_test.go — 对照表 §6 开库失败词表的三面钉：垃圾文件＝
// ErrCorrupt（①类，boot 侧隔离重开）；库版本超前＝ErrNewerSchema 且
// 版本钉绝不降级（旧程序开新库的那枚前向兼容坑）；迁移步进失败＝
// ErrMigrate（②类，库文件停在原版本、重开可用——migrate 的事务性）。

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestOpenGarbageFileIsErrCorrupt(t *testing.T) {
	p := filepath.Join(t.TempDir(), "studio.db")
	if err := os.WriteFile(p, []byte("这绝对不是一个 sqlite 数据库\n不是也不是\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(p); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("垃圾文件应以 ErrCorrupt 拒开，得到：%v", err)
	}
}

func TestOpenNewerSchemaRefusesAndKeepsStamp(t *testing.T) {
	p := filepath.Join(t.TempDir(), "studio.db")
	d, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	// 手植未来版本戳：模拟「新程序建好的库被旧程序打开」
	if _, err := d.db.Exec(`UPDATE store_meta SET v = 999 WHERE ns='' AND k='schema'`); err != nil {
		t.Fatal(err)
	}
	d.Close()

	if _, err := Open(p); !errors.Is(err, ErrNewerSchema) {
		t.Fatalf("版本超前应以 ErrNewerSchema 拒开，得到：%v", err)
	}
	// 版本钉不降级的证明：若第一次拒开时把戳写回了 len(schema)，
	// 第二次 Open 就会「成功」——仍然拒开＝戳原样未动。
	if _, err := Open(p); !errors.Is(err, ErrNewerSchema) {
		t.Fatalf("版本钉疑似被降级（第二次不再拒开）： %v", err)
	}
	// 拒开不碰库：把戳修回合法版本后照常开库服务。
	raw, rerr := sql.Open("sqlite", "file:"+p)
	if rerr != nil {
		t.Fatal(rerr)
	}
	if _, rerr = raw.Exec(`UPDATE store_meta SET v = ? WHERE ns='' AND k='schema'`, len(schema)); rerr != nil {
		t.Fatal(rerr)
	}
	raw.Close()
	d2, err := Open(p)
	if err != nil {
		t.Fatalf("修回版本后应可开库：%v", err)
	}
	d2.Close()
}

func TestOpenMigrationFailureIsErrMigrate(t *testing.T) {
	p := filepath.Join(t.TempDir(), "studio.db")
	d, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	d.Close()

	// 包内注入一步坏迁移（语法错误）：下一开必然步进失败。注意恢复
	// 必须内联在断言之前完成——任何断言退出都不该带着被污染的包变
	// 量离开（defer＋内联双重截断曾把真 v2 步砍掉殃及全包）。
	schema = append(schema, `CREATE TABLE broken(`)
	_, openErr := Open(p)
	schema = schema[:len(schema)-1]

	if !errors.Is(openErr, ErrMigrate) {
		t.Fatalf("步进失败应以 ErrMigrate 拒开，得到：%v", openErr)
	}
	// migrate 是事务：失败回滚，库停在原版本、文件未坏——还原
	// schema 后重开即恢复服务。
	d2, err := Open(p)
	if err != nil {
		t.Fatalf("还原后应可开库（迁移失败的回滚不留伤）：%v", err)
	}
	d2.Close()
}
