package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"github.com/WWestC/Niuma_Studio/kb"
	"github.com/WWestC/Niuma_Studio/server"
	"net/http"
	"os"
	"time"
)

// The members roster reader (HTTP face).

func runMembers(args []string) int {
	fs := flag.NewFlagSet("members", flag.ContinueOnError)
	c := parseCommon(fs)
	project := fs.String("project", "", "project key to scope to (default: the workspace's bound project; empty = studio-wide)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	port := c.port
	if port == 0 {
		p, err := server.DiscoverPort()
		if err != nil {
			fmt.Fprintln(os.Stderr, "members:", err)
			return 1
		}
		port = p
	}
	// The status column rides the blackboard's people data — the same
	// /kb/people source `kb people` renders — so a presence-grace seat
	// shows as 在线(宽限) here too (v0.9 ME1, t_47②). The /members
	// endpoint and its []chat.Member JSON stay untouched for machine
	// consumers; offline people rows are filtered back out so members
	// remains "who is online". Scoped runs see one office's roster
	// only (v2.9 isolation — same ?project= contract as kb people).
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/kb/people%s", port, withQuery("", "project", pickScope(*project))))
	if err != nil {
		fmt.Fprintln(os.Stderr, "members:", err)
		return 1
	}
	defer resp.Body.Close()
	var people []kb.PersonSummary
	if err := json.NewDecoder(resp.Body).Decode(&people); err != nil {
		fmt.Fprintln(os.Stderr, "members:", err)
		return 1
	}
	online := make([]kb.PersonSummary, 0, len(people))
	for _, pr := range people {
		if pr.Online {
			online = append(online, pr)
		}
	}
	if c.json {
		_ = json.NewEncoder(os.Stdout).Encode(online)
		return 0
	}
	if len(online) == 0 {
		fmt.Println("(nobody online)")
		return 0
	}
	for _, pr := range online {
		line := pr.Name
		if pr.Role != "" {
			line += "\t" + pr.Role
		}
		state := "在线"
		if pr.Grace { // presence-grace seat: no live connection behind it
			state = "在线(宽限)"
		}
		// 干活位（v2.10 同步收口）：调度器回合进行中 tell，与 kb people、
		// 侧栏 chip 同一位真相——成员互查忙闲不再各说各话。
		if pr.Working {
			state += "·干活中"
		}
		if pr.Local {
			state += "·本机"
		}
		if pr.Room != "" && pr.Room != "default" {
			state += "@" + pr.Room // seated in that project's office
		}
		line += "\t" + state
		if pr.LastReportTS > 0 {
			line += fmt.Sprintf("\t汇报于 %s", time.Unix(pr.LastReportTS, 0).Format("15:04"))
		}
		fmt.Println(line)
	}
	return 0
}

// runKB reads the room's blackboard knowledge base: the manual, the
// operator's notice, the personnel list or one person's detail.
