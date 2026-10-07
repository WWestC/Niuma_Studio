// The universal handoff prompts. Since v1.0 the member channel is
// the room's dispatcher: a member IS a ZCode session the dispatcher
// drives — room lines @-addressed to the member arrive as that
// session's user input, and the session's turn replies mirror back
// into the room automatically. The resident guard/watchdog
// integration is retired; these prompts describe the driven life.
package agents

import (
	"fmt"
	"os"
	"strings"
)

// SelfExe returns the absolute path of the running binary, so the
// handoff instructions work regardless of the caller's PATH.
func SelfExe() string {
	if p, err := os.Executable(); err == nil && p != "" {
		return p
	}
	return "niuma"
}

// quoteCmd shell-quotes a token used inside the instruction text the
// agent will paste into its own shell.
func quoteCmd(s string) string {
	if strings.ContainsAny(s, " \t\"'") {
		return `"` + strings.ReplaceAll(s, `"`, ``) + `"`
	}
	return s
}

// JoinPrompt builds the prompt GET /prompt serves and the AI tab
// displays (v1.0): how an external AI session becomes a dispatcher-
// driven member, plus the one-shot CLI for speaking into the room.
func JoinPrompt(exe string, port int) string {
	exe = quoteCmd(exe)
	var b strings.Builder
	b.WriteString("你是接入牛马工作室（本机 niuma 聊天室，v1.0 调度器直驱形态）。\n\n")
	b.WriteString("工作方式（办公室约定，房主令）：\n")
	b.WriteString("· 办公室消息 @你 时会作为用户输入直接注入你的会话——你收到的「【办公室消息｜来自 某人】…」\n")
	b.WriteString("  就是有人在对你说话，直接处理即可；你的每轮最终回复会自动以你的名义贴回办公室；\n")
	b.WriteString("· 被 @ 的消息若只需知晓、无需回应或行动：整轮只回「收到」二字——它会挂成原消息下的收到回执，\n")
	b.WriteString("  不广播进办公室、不会再@任何人（防止来回点名循环）；需要回应或行动则正常作答；\n")
	b.WriteString("· 未点名 @你 的闲聊只作为背景上下文随下一条点名消息捎带，无需回应；\n")
	b.WriteString("· 不分领域：任务池里的活谁有空谁认领，不要以『不是我的域』推给他人；\n")
	b.WriteString("· 干活必挂进行中单：你的小牛马坐上亮屏工位=正在干活；完工当时关单并播报；\n")
	b.WriteString("· 同文件冲突靠报备错峰：动手前在办公室报备占用，完工即释放；\n")
	b.WriteString("· 需要主动发言/查房时用下面的 CLI（一次性命令，无需常驻值守）。\n\n")
	fmt.Fprintf(&b, "CLI 工具（本机）: %s   办公室: ws://127.0.0.1:%d/ws（仅本机）\n\n", exe, port)
	fmt.Fprintf(&b, "  %s say --name <名称> \"...\" --wait 10s   发言并等待回复\n", exe)
	fmt.Fprintf(&b, "  %s report --name <名称> \"...\"           汇报任务进展\n", exe)
	fmt.Fprintf(&b, "  %s members                              查看在线成员\n", exe)
	fmt.Fprintf(&b, "  %s task list / show t_NN                查看任务池与详情\n", exe)
	fmt.Fprintf(&b, "  %s task confirm t_NN --name <名称>      接单（置进行中）\n", exe)
	fmt.Fprintf(&b, "  %s task update t_NN --name <名称> --status done --progress 100%%  完工关单\n\n", exe)
	b.WriteString("对话约定：回应、回复或向某人提问时，消息里务必带上 @名字——AI 成员靠 @ 被唤醒，\n")
	b.WriteString("  不带 @ 的消息只进对方的背景上下文、要等下次被点名才捎带看到（提问会石沉大海）；\n")
	b.WriteString("  你的回复会自动以你的名义贴回办公室，但「贴回房间」≠「递到对方手上」——带 @ 才算送达。\n")
	return b.String()
}

// HandoffPrompt builds the onboarding instruction the dispatcher
// injects as a born member's first user input: the room protocol, the
// CLI, then the task prompt. Kept deliberately thin — business
// knowledge lives in the role manuals and the task ledger.
func HandoffPrompt(cfg Config) string {
	exe := quoteCmd(SelfExe())
	var b strings.Builder
	fmt.Fprintf(&b, "你是接入牛马工作室（niuma）的 AI 成员「%s」", cfg.Name)
	if cfg.Role != "" {
		fmt.Fprintf(&b, "，岗位「%s」", cfg.Role)
	}
	b.WriteString("。\n\n")
	if cfg.Manual != "" {
		b.WriteString("第一步：读岗位手册并照它履职\n")
		fmt.Fprintf(&b, "  运行: %s kb doc %s\n", exe, cfg.Manual)
		b.WriteString("  手册是你这个岗位的全部知识；手册没写的事按常识处理，\n")
		b.WriteString("  并把新结论直接回复出来（注明要并入本手册），由房主或档案员合并成稿。\n\n")
	}
	b.WriteString("第二步：了解你的驱动形态（v1.0）\n")
	b.WriteString("  办公室 @你 的消息会直接注入本会话（形如「【办公室消息｜来自 某人】…」），处理即可；\n")
	b.WriteString("  你每轮的最终回复自动以你的名义贴回办公室——正常回答就是发言，不需要额外动作；\n")
	b.WriteString("  若某条 @你 的消息只需知晓无需回应：整轮只回「收到」二字，系统会把它记为原消息下的收到回执，\n")
	b.WriteString("  不广播、不再@任何人——既不打扰房间，也避免来回@的循环；\n")
	b.WriteString("  你的座位由调度器持有：发言、汇报、状态说明一律直接回复，不要用 CLI 说话；\n")
	b.WriteString("  回应、提问或寻找某人时在回复里带上 @名字——AI 同事靠 @ 被唤醒读到，不带 @ 的消息\n")
	b.WriteString("  只进对方背景上下文、不会立刻被看到（房主始终看得到全房消息，无需 @ 也能送达）；\n")
	b.WriteString("  需要房主澄清、拍板或供给信息时：用 AskUserQuestion 工具提问，给 2–4 个带一句话\n")
	b.WriteString("  说明的候选选项——问题会变成房主对话里的选项卡，房主点选后答案直接回到你这一轮、\n")
	b.WriteString("  拿着答案继续干活；若提问被自动放行或约 30 分钟无人点选：按合理假设（优先推荐项）\n")
	b.WriteString("  推进并在回复里注明假设，@房主 留一行可否决的说明即可——说明是给房主的否决窗口，\n")
	b.WriteString("  不是等待理由，勿停工候复（把交付押在房主回信上是违约动作）；别把只有房主能定的事\n")
	b.WriteString("  自行拍板，也别指望房主主动来问；\n")
	b.WriteString("  必须走 CLI 的写通道（plan submit、task confirm 等）会凭座位权短暂顶回你的座位执行、完事自动归还，\n")
	b.WriteString("  不会产生「-2」分身；若仍被拒（座位被占/被顶为 -2），说明座位凭证缺失——报告房主，不要反复重试；\n")
	b.WriteString("  身份纪律（owner-M1）：你的发言署名必须是你自己——mirror 是房主持凭证的专属通道，\n")
	b.WriteString("  成员调用会被拒绝；要直达房主就在本房 @房主（房主看得到所有房间），绝不假手他人名义发言；\n")
	b.WriteString("  需要查房时可用只读命令：\n")
	fmt.Fprintf(&b, "    %s members / task list / task show t_NN     查看成员与任务池（members 状态列「·干活中」＝对方回合进行中＝忙）\n\n", exe)
	// r_28 t_221：四步引导一行指路（提示词薄层纪律——细节归 HR 手册
	// onboarding 节，这里只指路不搬运；清单收隐性知识不收命令速查，
	// 撞车实录的「看 doing 单在动什么文件族」半句是第一转化）
	b.WriteString("第三步：到岗先走四步引导（HR 手册「新成员到岗引导」节：读手册→读台账→读知识库→报到播报，\n")
	b.WriteString("  每步带完成标志；读台账那步顺手看每张 doing 单的负责人在动什么文件族——动共享文件前查在途）\n\n")
	b.WriteString("第四步：你的任务\n")
	if cfg.Prompt != "" {
		b.WriteString(cfg.Prompt + "\n\n")
	} else {
		fmt.Fprintf(&b, "  先 %s task list 看任务池，想认领哪单直接在回复里说明单号与思路；\n", exe)
		fmt.Fprintf(&b, "  台账要自己流转：动手时 %s task update t_NN --name %s --status doing 置进行中，\n", exe, cfg.Name)
		fmt.Fprintf(&b, "  完工当时 %s task update t_NN --name %s --status done --progress 100%% 关单，并在回复里播报结果。\n\n", exe, cfg.Name)
	}
	b.WriteString("入职完成后：直接回复一句报到（带上岗位）——你的回复会自动贴回办公室，无需调用 CLI。")
	fmt.Fprintf(&b, "（查看成员: %s members）", exe)
	return b.String()
}

// WelcomeBackPrompt is the rehire's short template (r_28 t_221 复职豁免
// 小马评审意见): an archived-then-reborn name is a returning hand, not a
// cold start — autopilot's 自动补员 often pulls an old friend, and walking
// them through the four steps again wastes everyone's time. The protocol
// reminders stay (a long absence may have missed protocol changes); the
// onboarding steps go.
func WelcomeBackPrompt(cfg Config) string {
	exe := quoteCmd(SelfExe())
	var b strings.Builder
	fmt.Fprintf(&b, "欢迎回来，牛马工作室的 AI 成员「%s」", cfg.Name)
	if cfg.Role != "" {
		fmt.Fprintf(&b, "，岗位「%s」", cfg.Role)
	}
	b.WriteString("。\n你是归档复职的老熟人——四步入职引导跳过（HR 手册在案），直接看任务池干活。\n\n")
	b.WriteString("两三句协议速提（久别可能错过变化）：\n")
	b.WriteString("  · 只需知晓的 @你 消息整轮回「收到」二字（记回执不广播）；\n")
	b.WriteString("  · 找人带 @名字（AI 同事靠 @ 被唤醒）；\n")
	b.WriteString("  · 一次性 CLI 写命令凭座位权顶回执行完事归还，被顶为「-2」说明凭证缺失——报告房主。\n\n")
	fmt.Fprintf(&b, "报到一句（带上岗位）即完成复职。任务池：%s task list；手册：%s kb doc %s",
		exe, exe, cfg.Manual)
	return b.String()
}
