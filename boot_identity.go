package main

// boot_identity.go — the multi-user switch's boot half (决策已定的
// 身份层转正）：NIUMA_MULTIUSER=1 是显式开启的形态——单机单用户仍
// 是默认，零摩擦不变。开启后本文件负责三件事：
//
//   - 启动门：多用户要求 NIUMA_STORE=sqlite（JSON 文件架没有命名
//     空间的诚实对应物，拒绝降级——fail-close，不是回退）；
//   - 一次性迁移：无账户的旧数据目录 → 建 admin 账户（房主升格）、
//     JSON 单向桥直导 admin 命名空间、遗留 ns='' 行全部收拢归位
//     （幂等：WHERE ns='' 自排干，二次启动零行移动）；
//   - 本机服务凭证：铸一枚回环操作者令牌写 ~/.niuma_service_token
//     （0600）——多用户形态下本机的 CLI/webview/镜像通道凭它自动
//     以 admin 入座（机器信任模型的显式化；远端连接不可用）。
//
// admin 首启凭据：随机铸密码写 ~/.niuma_admin_credentials（0600）
// 并打进日志——房主改密后该文件即可删。

import (
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/WWestC/Niuma_Studio/identity"
	"github.com/WWestC/Niuma_Studio/sqlstore"
	"github.com/WWestC/Niuma_Studio/util"
)

// identitySet is the identity gate's product: what multi-user mode
// resolved to (zero value = the single-user historical shape).
type identitySet struct {
	// auth is the login/session service (nil = single-user).
	auth *identity.Service
	// subject is the studio's storage namespace (the admin account
	// name under multi-user; "" single-user).
	subject string
	// admin is the admin account's username ("" single-user).
	admin string
	// serviceToken is the loopback operator credential (multi-user
	// only; presented by local CLI/webview/mirror dials).
	serviceToken string
}

// multiuserEnabled reads the explicit opt-in (NIUMA_MULTIUSER=1).
func multiuserEnabled() bool {
	return util.Env("MULTIUSER") == "1"
}

// parseSessionTTL reads NIUMA_SESSION_TTL: a Go duration ("90m",
// "24h") or a bare number of hours; empty/invalid falls back to
// identity.DefaultSessionTTL.
func parseSessionTTL() time.Duration {
	v := strings.TrimSpace(util.Env("SESSION_TTL"))
	if v == "" {
		return identity.DefaultSessionTTL
	}
	if d, err := time.ParseDuration(v); err == nil && d > 0 {
		return d
	}
	if h, err := strconv.Atoi(v); err == nil && h > 0 {
		return time.Duration(h) * time.Hour
	}
	return identity.DefaultSessionTTL
}

// adminCredentialPath / serviceTokenPath are the conventional
// credential files' locations.
func adminCredentialPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".niuma_admin_credentials"), nil
}

func serviceTokenPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".niuma_service_token"), nil
}

// adminUsername resolves the promoted owner's account name:
// NIUMA_OWNER overrides, else the OS user (defaultName). Refuses
// (fatal) on a shape identity rejects — the admin username becomes a
// storage namespace, it cannot be improvised.
func adminUsername() string {
	if v := strings.TrimSpace(util.Env("OWNER")); v != "" {
		if !identity.ValidUsername(v) {
			log.Fatalf("NIUMA_OWNER %q 不是合法账户名（1-32 字符、无空格/控制字符）——多用户模式拒绝启动", v)
		}
		return v
	}
	name := defaultName()
	if !identity.ValidUsername(name) {
		log.Fatalf("本机用户名 %q 不能直接做管理员账户名——请设置 NIUMA_OWNER 为合法账户名（1-32 字符、无空格/控制字符）后重试", name)
	}
	return name
}

// applyMultiUser runs the one-time migration over the freshly opened
// database and returns the studio's identity wiring. root is the v2
// data root (JSON-era forensic copies live there); taskDir/rankPath
// are the tasks bridge's sources (legacy split runs first — the
// bridge reads the split shelves).
func applyMultiUser(db *sqlstore.DB, ownerName, root, taskDir, rankPath string) identitySet {
	accounts := db.Identity().List()
	fresh := len(accounts) == 0
	subject := ""
	if fresh {
		subject = adminUsername()
		password := mintAdminPassword()
		salt, hash, err := identity.NewCredential(password)
		if err != nil {
			log.Fatalf("多用户迁移：管理员凭据铸造失败: %v", err)
		}
		if err := db.Identity().Create(identity.Account{
			Username: subject, Role: identity.RoleAdmin,
			DisplayName: ownerName, Salt: salt, Hash: hash,
			CreatedTS: time.Now().Unix(),
		}); err != nil {
			log.Fatalf("多用户迁移：建管理员账户失败: %v", err)
		}
		if path, perr := adminCredentialPath(); perr == nil {
			if serr := os.WriteFile(path, []byte(subject+"\n"+password+"\n"), 0o600); serr != nil {
				log.Printf("管理员首启凭据落盘失败（%v）——请立即用 niuma 身份面重置密码", serr)
			} else {
				log.Printf("身份层：房主 %s 已升格为管理员账户 %q（唯一命名空间 %s）——首启密码已写 %s（0600，改密后可删）",
					ownerName, subject, subject, path)
			}
		}
	} else {
		// 已有账户：主体取与房主同名的管理员，否则第一名管理员。
		pick := ""
		for _, a := range accounts {
			if a.Role != identity.RoleAdmin {
				continue
			}
			if pick == "" || a.Username == ownerName {
				pick = a.Username
			}
		}
		if pick == "" {
			log.Fatalf("多用户迁移：账户表里没有管理员——请检查 %s 的 accounts 表", db.Path())
		}
		subject = pick
	}

	hasLegacy, hlerr := db.HasDefaultNSRows()
	if hlerr != nil {
		log.Fatalf("多用户迁移：探测单用户存量失败: %v", hlerr)
	}
	if hasLegacy {
		// 单用户 sqlite 时代的存量（ns=''）已在库内——'' 行就是当年
		// 桥进来的 JSON，重导会在每个主键上相撞；这一轮只做收拢。
		log.Printf("身份层迁移：检测到单用户 sqlite 存量——本轮跳过 JSON 桥，直接收拢进 %s 命名空间", subject)
	}
	if fresh && root != "" && !hasLegacy && !ledgerImportStampDone(root) {
		// JSON 单向桥直导 admin 命名空间（无 '' 中间态；表空门按目标
		// 主体判——取证副本不会被二次导入）。一次性导入戳把门（与单
		// 用户桥同一纪律：损坏重开/手动删库绝不回导旧快照）。Legacy
		// 拆架先跑（桥读的正是分项目架）。
		runLegacySplits(root, taskDir)
		if rep, err := db.ImportLocalInto(root, subject); err != nil {
			log.Printf("身份层迁移：JSON 台账并入中断（已提交部分照用）: %v", err)
		} else if rep.ReqShelves+rep.Reqs+rep.Plans+rep.Merges+rep.Meetings+rep.Notices > 0 {
			log.Printf("身份层迁移：JSON 台账已直导 %s 命名空间（需求 %d 架 %d 条、提案槽 %d、合并槽 %d、会议架 %d、公告 %d 行；JSON 文件保留取证）",
				subject, rep.ReqShelves, rep.Reqs, rep.Plans, rep.Merges, rep.Meetings, rep.Notices)
		}
		if trep, err := db.ImportTasksLedgerInto(taskDir, rankPath, subject); err != nil {
			log.Printf("身份层迁移：JSON 任务台账并入中断（已提交部分照用）: %v", err)
		} else if trep.Shelves+trep.Tasks+trep.Ranks > 0 {
			log.Printf("身份层迁移：任务台账已直导 %s 命名空间（任务架 %d 架 %d 张、等级 %d 行；JSON 文件保留取证）",
				subject, trep.Shelves, trep.Tasks, trep.Ranks)
		}
		writeLedgerImportStamp(root)
	}

	// 收拢遗留 ns='' 行（单用户 sqlite 时期的存量）：幂等，二次启动
	// 零行移动。失败＝事务回滚、库未动——fail-close 好过半迁状态。
	moved, err := db.RehomeNamespace(subject)
	if err != nil {
		log.Fatalf("多用户迁移失败: %v", err)
	}
	if moved > 0 {
		log.Printf("身份层迁移：%d 行存量已归位 %s 命名空间（一次性，幂等）", moved, subject)
	}

	// 本机服务凭证：回环操作者令牌（远端不可用）。
	svc := identity.NewService(db.Identity(), parseSessionTTL(), identity.ThrottleConfig{})
	tok, err := identity.MintToken()
	if err != nil {
		log.Fatalf("多用户迁移：服务令牌铸造失败: %v", err)
	}
	if path, perr := serviceTokenPath(); perr == nil {
		if serr := os.WriteFile(path, []byte(tok+"\n"), 0o600); serr != nil {
			log.Printf("服务令牌落盘失败（%v）——本机 CLI/webview 将需手动登录", serr)
		}
	}

	return identitySet{auth: svc, subject: subject, admin: subject, serviceToken: tok}
}

// mintAdminPassword mints the first-boot admin password (shown once
// via the credential file). Random, fail-closed, no fallback.
func mintAdminPassword() string {
	pw, err := identity.MintToken()
	if err != nil {
		log.Fatalf("多用户迁移：管理员密码铸造失败: %v", err)
	}
	return pw
}
