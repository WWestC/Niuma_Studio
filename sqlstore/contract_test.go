// contract_test.go — the single-database backing wired into the
// portable contract suites: engines over the sqlite document seams.
// THE promotion's gate: these green alongside the memory backings'
// runs (each domain package's contract_local_test) is what allows the
// engine to carry production data on either side.
package sqlstore_test

import (
	"testing"

	"github.com/WWestC/Niuma_Studio/storetest"
)

func TestSQLiteReqContract(t *testing.T) {
	storetest.TestReqStore(t, storetest.ReqHarness{
		MultiSubject: true, Name: "sqlite",
		OpenBacking: storetest.ReqSQLiteBacking,
	})
}

func TestSQLitePlanContract(t *testing.T) {
	storetest.TestPlanStore(t, storetest.PlanHarness{
		MultiSubject: true, Name: "sqlite",
		OpenBacking: storetest.PlanSQLiteBacking,
	})
}

func TestSQLiteMergeContract(t *testing.T) {
	storetest.TestMergeStore(t, storetest.MergeHarness{
		MultiSubject: true, Name: "sqlite",
		OpenBacking: storetest.MergeSQLiteBacking,
	})
}

func TestSQLiteTaskContract(t *testing.T) {
	storetest.TestTaskStore(t, storetest.TaskHarness{
		MultiSubject: true, Name: "sqlite",
		OpenBacking: storetest.TaskSQLiteBacking,
	})
}

func TestSQLiteMeetingContract(t *testing.T) {
	storetest.TestMeetingStore(t, storetest.MeetingHarness{
		Name:        "sqlite",
		OpenBacking: storetest.MeetingSQLiteBacking,
	})
}

func TestSQLiteNoticeContract(t *testing.T) {
	storetest.TestNoticeStore(t, storetest.NoticeHarness{
		Name:        "sqlite",
		OpenBacking: storetest.NoticeSQLiteBacking,
	})
}
