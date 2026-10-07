package assistant

// TestMain walls the assistant test binary off the operator's LIVE
// ZCode desktop state: the two sidebar seams (row register / row
// fold) go no-op for every test — the per-cfg injections stay working
// and take precedence, but forgetting one can no longer pin a phantom
// 小助手 row into the real desktop sidebar (the phantom-member 实录
// the dispatch package fixed the same way). Production is untouched.

import (
	"os"
	"testing"

	"github.com/WWestC/Niuma_Studio/zcode"
)

func TestMain(m *testing.M) {
	assistantIndexRegister = func(zcode.TaskIndexEntry) error { return nil }
	assistantIndexFold = func(string) error { return nil }
	os.Exit(m.Run())
}
