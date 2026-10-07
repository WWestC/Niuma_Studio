package dispatch

// implements_test.go — Bridge 接口的「实现登记处」：生产实现是
// *zcode.Client（结构化满足——zcode 不知道 dispatch 的存在，依赖
// 倒置），测试基桩是 bridgestub_test.go 的 baseBridge（已有断言）。
// 断言把「谁真正实现 Bridge」钉回编译期：zcode.Client 的方法签名
// 漂移，dispatch 的测试二进制当场编译失败；找实现从 grep 变成一次
// 跳转。测试文件豁免分层，生产依赖方向不变（dispatch 引 zcode 本就
// 合法：桥客户端在分层图的档案行）。

import "github.com/WWestC/Niuma_Studio/zcode"

var _ Bridge = (*zcode.Client)(nil)
