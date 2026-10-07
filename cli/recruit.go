package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/WWestC/Niuma_Studio/agents"
	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/recruit"

	"github.com/coder/websocket/wsjson"
)

// errNoDispatcher marks the birth attempt that could not even reach a
// dispatcher (face absent / 调度器未启用): the one case where the GUI
// injection fallback is still the right next move.
var errNoDispatcher = errors.New("dispatcher face absent")

// recruitPort is the parsed --port flag (0 = auto-discover), kept at
// package level so the step functions can read the ladder result.
var recruitPort int

// runRecruit implements the D3 hiring pipeline as one command (t_62):
// five steps — room discovered → agent saved → session opened →
// member verified → report. Step ③ is the v1.0 standard path: POST
// /dispatch/birth lets the running room's dispatcher create the real
// persistent session (the stored config's manual + task compose the
// onboarding prompt; the capability fabric rides the create). When no
// dispatcher answers (pre-v1 room, --no-dispatch, ZCode 桥未就绪) it
// degrades to the drive engine (recruit.RunDrive — GUI injection),
// productized behavior, not a stub waiting for anything.
//
// --project (v2.9) targets the hire at one project's office: the
// birth lands on that project's dispatcher (session born in ITS
// workspace), the transient hiring desk seats in ITS room and the
// onboarding ledger line lands on ITS room-log shelf. The default
// drinks from pickScope — the workspace's bound project — so a hire
// run from inside a project's workspace cannot land in the lobby by
// accident (the t_223 lesson: a lobby-defaulted recruit put 小笔 in
// the wrong office), and an unbound cwd keeps the legacy lobby shape.
//
//	recruit --name 小新 --role "测试工程师" [--manual 一句话手册] [--project book]
//	        [--timeout 120s]
func runRecruit(args []string) int {
	fs := flag.NewFlagSet("recruit", flag.ContinueOnError)
	fs.IntVar(&recruitPort, "port", 0, "server port (default: auto-discover)")
	name := fs.String("name", "", "the new member's exact name (required)")
	role := fs.String("role", "", "one-line identity (required)")
	manual := fs.String("manual", "", "room-memory manual key, e.g. roles/hr")
	task := fs.String("task", "", "one-off task text for the new member")
	taskID := fs.String("task-id", "", "existing ledger task to hand over (t_NN)")
	project := fs.String("project", "", "the project's office the hire joins (default: the workspace's bound project; empty = the lobby)")
	timeout := fs.Duration("timeout", 120*time.Second, "overall verification budget")
	dryRun := fs.Bool("dry-run", false, "print every step's commands and the onboarding prompt, execute nothing")
	asJSON := fs.Bool("json", false, "machine-readable step log")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *name == "" || *role == "" {
		fmt.Fprintln(os.Stderr, "recruit: --name and --role are required")
		return 2
	}

	type step struct {
		name string
		err  error
	}
	var steps []step
	log := func(n string, err error) bool {
		steps = append(steps, step{n, err})
		if *asJSON {
			b, _ := json.Marshal(struct {
				Step string `json:"step"`
				OK   bool   `json:"ok"`
				Err  string `json:"err,omitempty"`
			}{n, err == nil, fmt.Sprintf("%v", err)})
			fmt.Println(string(b))
		} else if err != nil {
			fmt.Printf("✗ %s: %v\n", n, err)
		} else {
			fmt.Printf("✓ %s\n", n)
		}
		return err == nil
	}

	// ① discover the room (flag override, else the discovery file /
	// default port — same ladder as every other subcommand)
	c := &common{port: recruitPort}
	url, err := c.resolveURL()
	if !log("发现办公室", err) {
		fmt.Fprintln(os.Stderr, recruitTrouble("room", c.port))
		return 1
	}

	// ② upsert the agent record (exact-name, never -2) — carrying the
	// manual key and the RAW task line: the dispatcher's birth composes
	// the onboarding prompt from the stored config (第一步读手册 +
	// 第三步任务), so the capability fabric rides the create instead of
	// being displaced by a fully-specified prompt.
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	proj := pickScope(*project)
	taskLine := ""
	switch {
	case *task != "":
		taskLine = *task
	case *taskID != "":
		taskLine = "运行 niuma task show " + *taskID + " 查看指派给你的任务并开始"
	}
	cfg := chat.AgentConfig{Name: *name, Role: *role, Manual: *manual, Prompt: taskLine}
	handoff := agents.HandoffPrompt(agents.Config{Name: *name, Role: *role, Manual: *manual, Prompt: taskLine})
	if *dryRun {
		fmt.Println("== dry-run: 各步命令与提示词（不执行）==")
		fmt.Printf("① 发现办公室: %s（目标项目: %s）\n", url, recruitOfficeLabel(proj))
		fmt.Println("② 建档 agent_save:")
		fmt.Printf("   name=%s role=%s manual=%s\n", cfg.Name, cfg.Role, cfg.Manual)
		fmt.Printf("③ 出生: POST /dispatch/birth {name, role, project:%s}（调度器直驱；无调度器时降级 drive --brief）\n", proj)
		fmt.Println("   入职提示词（由调度器按档案组合，即下方全文）:")
		fmt.Println("---")
		fmt.Println(handoff)
		fmt.Println("---")
		fmt.Printf("④ 轮询验证 %s 出现 %s（上限 %s）\n", recruitVerifyFace(proj), cfg.Name, *timeout)
		fmt.Println("⑤ 完成提示（把接单指引发给新人会话）")
		fmt.Printf("⑥ 台账: kb_append %s「%s 已上岗（岗位 %s，recruit，<日期>）」\n", roomLogKey(proj), cfg.Name, cfg.Role)
		return 0
	}
	if err := recruitAgentSave(ctx, url, cfg, proj); !log("建档 agent_save", err) {
		fmt.Fprintln(os.Stderr, recruitTrouble("save", c.port))
		return 1
	}

	// ③ open the session: dispatcher birth first (v1.0 standard), the
	// drive engine as the degraded channel (ARB-2 contract: drive
	// --brief PATH [--dry-run] [--attempts N]; exit 0/1/2/3).
	switch sid, berr := recruitBirth(ctx, url, cfg.Name, cfg.Role, proj); {
	case berr == nil:
		log("出生 dispatch birth", nil)
		fmt.Printf("  会话 %s（调度器持有，回执见「dispatch status」）\n", sid)
	case errors.Is(berr, errNoDispatcher):
		// The brief rides a temp file; GUI injection is best-effort —
		// failure degrades to the paste instructions, not a dead
		// pipeline. 名字先消毒（安全核查修复：成员名可含 '/' 或
		// '..'，不折叠能写出 TempDir）；drive 成功即删，降级手动路
		// 径还要粘贴该文件、留盘。
		briefPath := filepath.Join(os.TempDir(), recruit.SafeTempLeaf("dh_recruit_", cfg.Name, ".txt"))
		if err := os.WriteFile(briefPath, []byte(handoff), 0o600); err != nil {
			log("写入职提示词文件", err)
		}
		drvErr := recruit.RunDrive([]string{"--brief", briefPath, "--attempts", "3"})
		if drvErr == 0 {
			_ = os.Remove(briefPath)
			log("开窗 SessionDriver（降级通道）", nil)
		} else {
			if *asJSON {
				fmt.Printf("{\"step\":\"开窗\",\"ok\":false,\"mode\":\"degraded\"}\n")
			} else {
				fmt.Println("△ 开窗: SessionDriver 未成功（rc=", drvErr, "），请手动:")
				fmt.Print(recruitInject(briefPath))
			}
		}
	default:
		// a real refusal (name already dispatched, store down…): surface
		// it and still verify — a same-name reinstatement returns the
		// existing seat, so verification decides success, not the error
		fmt.Printf("△ 出生: dispatch birth 被拒（%v）——继续验证座位（同名复职会直接通过）\n", berr)
		if *asJSON {
			fmt.Printf("{\"step\":\"出生\",\"ok\":false,\"err\":%q}\n", fmt.Sprintf("%v", berr))
		}
	}

	// ④ verify: the seat appears and the prompt endpoint serves
	if err := recruitVerify(ctx, url, cfg.Name, proj); !log("轮询验证", err) {
		fmt.Fprintln(os.Stderr, recruitTrouble("verify", c.port))
		return 1
	}

	// ⑤ report + onboarding pointer
	fmt.Printf("入职完成: %s（%s，%s）。把下面一行发给新人会话即可接单:\n", cfg.Name, cfg.Role, recruitOfficeLabel(proj))
	fmt.Printf("  niuma task list   # 池中领活; confirm t_NN --name %s 置进行中\n", cfg.Name)

	// ⑥ org-memory ledger (v0.8 M3): the onboarding line lands in the
	// hiring office's room-log automatically — HR's manual write-back
	// drops to a supplement. Runs after the completion banner; failure
	// degrades to a manual-append hint (①–④ already succeeded), never
	// a dead pipeline.
	if err := recruitLogStep(ctx, url, cfg, proj); !log("台账 room-log 已上岗", err) {
		fmt.Printf("△ 台账未落，请手动补: niuma kb append %s \"%s 已上岗（岗位 %s，recruit，%s）\"\n",
			roomLogKey(proj), cfg.Name, cfg.Role, time.Now().Format("2006-01-02"))
	}
	return 0
}

// roomLogKey is the onboarding ledger's shelf (v2.9): the lobby keeps
// the seeded v1 ops/room-log; a project's office keeps p/<key>/room-log
// — the same one-address-per-office rule as the offboard ceremony
// (server/offboard.go) and the chronicle/establishment keys.
func roomLogKey(project string) string {
	if project == "" || project == chat.LobbyKey {
		return "ops/room-log"
	}
	return "p/" + project + "/room-log"
}

// recruitOfficeLabel names the hire's destination for the operator's
// reading: the bound project key, or Niuma_Studio for the legacy empty scope.
func recruitOfficeLabel(project string) string {
	if project == "" || project == chat.LobbyKey {
		return "Niuma_Studio"
	}
	return "项目 " + project
}

// recruitVerifyFace is the read face step ④ polls for a project hire
// (/members only serves the lobby roster).
func recruitVerifyFace(project string) string {
	if project == "" || project == chat.LobbyKey {
		return "/members"
	}
	return "/p/" + project + "/staffing"
}

// recruitLogStep is step ⑥: append the onboarding line to the hiring
// office's room-log over one WS connection, same dial-and-await shape
// as recruitAgentSave. A project whose ledger doc does not exist yet
// (fresh offices only birth it on first offboard) gets it created by
// the same guest seat — the write gate admits a seat in the room.
func recruitLogStep(ctx context.Context, url string, cfg chat.AgentConfig, proj string) error {
	conn, _, err := dialTokenRoom(ctx, url, "recruit", "hiring", false, "", proj)
	if err != nil {
		return err
	}
	defer conn.CloseNow()
	key := roomLogKey(proj)
	line := fmt.Sprintf("%s 已上岗（岗位 %s，recruit，%s）", cfg.Name, cfg.Role, time.Now().Format("2006-01-02"))
	if err := wsjson.Write(ctx, conn, chat.Message{Type: chat.MsgKbAppend, Key: key, Text: line}); err != nil {
		return err
	}
	var m chat.Message
	for {
		if err := wsjson.Read(ctx, conn, &m); err != nil {
			return fmt.Errorf("no answer: %w", err)
		}
		if m.Type == chat.MsgKb {
			if m.Event == "denied" {
				// a fresh project office has no room-log doc yet —
				// create it with this line (kb_write) instead of
				// failing the step
				if strings.Contains(m.Text, "不存在") {
					if werr := wsjson.Write(ctx, conn, chat.Message{Type: chat.MsgKbWrite,
						KbDoc: &chat.KbDoc{Key: key, Title: "办公室日志", Body: line}}); werr != nil {
						return fmt.Errorf("kb_write 建档未成: %w", werr)
					}
					continue
				}
				taskBye(ctx, conn)
				return fmt.Errorf("kb_append 被拒: %s", m.Text)
			}
			if m.Event == "appended" || m.Event == "written" {
				taskBye(ctx, conn)
				return nil
			}
		}
	}
}

// recruitAgentSave upserts the agent record over one WS connection
// seated in the hiring office (a project hire's guest desk belongs in
// that project's room).
func recruitAgentSave(ctx context.Context, url string, cfg chat.AgentConfig, proj string) error {
	conn, _, err := dialTokenRoom(ctx, url, "recruit", "hiring", false, "", proj)
	if err != nil {
		return err
	}
	defer conn.CloseNow()
	if err := wsjson.Write(ctx, conn, chat.Message{Type: chat.MsgAgentSave, Agent: &cfg}); err != nil {
		return err
	}
	var m chat.Message
	for {
		if err := wsjson.Read(ctx, conn, &m); err != nil {
			return fmt.Errorf("no answer: %w", err)
		}
		if m.Type == chat.MsgAgent && m.Agent != nil && m.Agent.Name == cfg.Name {
			// hand the transient "recruit" seat to the grace period —
			// exiting without the bye left a 10-minute ghost per run
			taskBye(ctx, conn)
			return nil // saved broadcast confirms the upsert
		}
	}
}

// httpBase converts a room ws URL into its HTTP origin — the shared
// shape behind the birth POST and the seat verification GET.
func httpBase(url string) string {
	base := strings.Replace(url, "ws://", "http://", 1)
	return strings.TrimSuffix(base, "/ws")
}

// recruitBirth births the member through the dispatcher's HTTP face
// (v1.0 standard): {name, role[, project]} — the stored config
// (manual + task) composes the onboarding prompt and the capability
// fabric rides the create. project (v2.9) routes to that project's
// dispatcher; empty keeps the fleet's legacy lobby default.
// errNoDispatcher marks the absent-face cases (no dispatch route,
// bridge not started) where the drive fallback is still the
// legitimate channel; any other error is a real refusal handed back
// verbatim (the caller verifies the seat before declaring failure).
func recruitBirth(ctx context.Context, url, name, role, project string) (string, error) {
	payload := map[string]string{"name": name, "role": role}
	if project != "" {
		payload["project"] = project
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		httpBase(url)+"/dispatch/birth", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
	text := strings.TrimSpace(string(out))
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return "", errNoDispatcher // no dispatch face (pre-v1 room / 无调度器)
	case resp.StatusCode != http.StatusOK:
		if strings.Contains(text, "调度器未启用") {
			return "", errNoDispatcher // dispatcher present, ZCode bridge nil
		}
		return "", errors.New(text)
	}
	return text, nil
}

// recruitInject is step ③'s degraded-path guidance: the paste-the-brief
// instructions printed when even the drive engine cannot inject the
// GUI session (no window, no accessibility permission). The brief file
// holds the composed onboarding prompt — the same text the birth path
// would have delivered as the session's first input.
func recruitInject(briefPath string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "  1) 在 ZCode 新建会话，把入职提示词文件 %s 的内容整段粘贴发送\n", briefPath)
	fmt.Fprintf(&b, "  2) 新人 say 报到后回到本命令的验证步骤\n")
	return b.String()
}

// recruitVerify polls the room until the new member holds a seat and
// the prompt endpoint serves — the C1 spike's verified sequence. A
// project hire polls that project's staffing face (/members serves
// the lobby roster only).
func recruitVerify(ctx context.Context, url, name, proj string) error {
	deadline := time.Now().Add(90 * time.Second)
	if dl, ok := ctx.Deadline(); ok && dl.Before(deadline) {
		deadline = dl
	}
	for {
		if err := recruitSeatPresent(url, name, proj); err == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%w（%s 未在窗口内出现）", os.ErrDeadlineExceeded, name)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(3 * time.Second):
		}
	}
}

// recruitSeatPresent checks the hire's office for the exact name: the
// lobby's /members roster, or the project's staffing rows.
func recruitSeatPresent(url, name, proj string) error {
	client := &http.Client{Timeout: 3 * time.Second}
	if proj == "" || proj == chat.LobbyKey {
		resp, err := client.Get(httpBase(url) + "/members")
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("members: HTTP %d", resp.StatusCode)
		}
		var members []chat.Member
		if err := json.NewDecoder(resp.Body).Decode(&members); err != nil {
			return err
		}
		for _, m := range members {
			if m.Name == name {
				return nil
			}
		}
		return fmt.Errorf("seat not present yet: %s", name)
	}
	resp, err := client.Get(httpBase(url) + "/p/" + proj + "/staffing")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("p/%s/staffing: HTTP %d", proj, resp.StatusCode)
	}
	var out struct {
		Rows []struct {
			Person string `json:"person"`
			State  string `json:"state"`
			Seated bool   `json:"seated"`
		} `json:"rows"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return err
	}
	for _, r := range out.Rows {
		if r.Person == name && (r.Seated || r.State == "active") {
			return nil
		}
	}
	return fmt.Errorf("seat not present yet: %s@%s", name, proj)
}

// recruitTrouble prints the per-step troubleshooting table (step ⑤'s
// failure arm): each failure mode gets one concrete next action.
func recruitTrouble(step string, port int) string {
	switch step {
	case "room":
		return fmt.Sprintf("排障: 办公室未响应（端口 %d）。先启动 ./niuma 再重跑 recruit。", port)
	case "save":
		return "排障: agent_save 被拒。确认办公室为最新版本（agent_save 帧需 v0.5 M2 二进制）。"
	case "verify":
		return "排障: 新人未上线。按顺序查: 窗口开了吗→提示词贴了吗→名字与 --name 完全一致吗；五步序列详见 kb manual「招聘开位」一节。"
	}
	return ""
}
