package kb

// implements_test.go — 消费者接口的「实现登记处」：kb 的读面接口
// （Roster/ConfigShelf/TaskLedger 于 kb.go，ReqTitler 于 history.go）由
// 生产者结构化满足——生产者不 import kb（依赖倒置的代价是编译器看不见
// 这层关系，找实现曾只能 grep）。这些断言把关系钉回编译期：生产者的
// 方法签名漂移，kb 的测试二进制当场编译失败；找实现从 grep 变成一次
// 跳转。测试文件豁免分层（fixture 可引域），生产依赖方向不变。
//
// 登记处与接口一一对应；换实现＝改这里的断言行，和 layering 白名单
// 加行一样是一次可评审的显式决定。ReqTitler 例外：它的实现不是结构
// 化满足而是 server/http.go 的 reqTitler 适配器（nil 守卫＋丢 subject
// 参数），断言钉在 server 侧（var _ kb.ReqTitler = reqTitler{}）——
// 那里才看得见适配器类型。

import (
	"github.com/WWestC/Niuma_Studio/agents"
	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/tasks"
)

var (
	_ Roster      = (*chat.Hub)(nil)     // Members/GraceNames/Departed — chat/hub.go 与 chat/presence.go
	_ ConfigShelf = (*agents.Store)(nil) // WireList 一族 — agents/agents.go
	_ TaskLedger  = (*tasks.Engine)(nil) // Rank/TaskStats/WireTasksOf — tasks/
)
