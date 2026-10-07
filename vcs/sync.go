package vcs

// sync.go — 版本管理（v2.14）的远端同步与授权半场：push/pull 写原语、
// 远端授权探测、git 缺失与凭据失败的分类锚点。此前远端只有 fetch（只
// 读拉进来），推回去与快进全靠房主进终端；凭据失败也只有一句拼在文
// 案尾部的提示，面板无从分辨「网络不通」与「远端不认我」。纪律与
// git.go/flow.go 同源：一切经 runGit（超时兜墙钟、拒绝文案原文透传、
// 机器可读解析绝不猜）；走网络的超时同 fetch 档，动工作区文件的同
// switch 档。

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/WWestC/Niuma_Studio/i18n"
)

// ErrNoGit 是「本机找不到 git 命令」的分类锚点：errors.Is(err,
// ErrNoGit) 判定用——它与 ErrNotRepo 是两回事（前者整页 git 都跑不
// 了，后者只差一个 init），服务端摘要面据此分成两种空态卡。真实报错
// 是 NoGitError，文案在渲染时刻组（i18n 热切后报错也是新语种）。
var ErrNoGit = errors.New("git not found")

// NoGitError is the "git binary absent" receipt: message composed at
// render time (i18n hot-swap works), identity via Is for errors.Is.
type NoGitError struct{ Err error } // Err 是 LookPath 的底层错误，可空

func (e *NoGitError) Error() string {
	return i18n.S("未找到 git——PATH 里没有 git，常见安装位也未命中；请安装 git（https://git-scm.com/ 或 brew install git）后重试")
}

func (e *NoGitError) Is(target error) bool { return target == ErrNoGit }

func (e *NoGitError) Unwrap() error { return e.Err }

// authRejectClassifiers 是凭据/权限类拒绝的指纹（小写匹配）。LC_ALL=C
// 钉死了输出语种，这套指纹才可靠：GIT_TERMINAL_PROMPT=0 的立刻失败、
// HTTP 401/403、ssh 公钥被拒——四类都指向同一件事「远端不认这台机
// 器」。指纹同时喂两处：GitError.Auth 分类（服务端分流 401）与
// AuthProbe 的行判定。
var authRejectClassifiers = []string{
	"terminal prompts disabled",             // GIT_TERMINAL_PROMPT=0 下的立刻失败
	"could not read username",               // HTTP 401 且无凭据可试
	"authentication failed",                 // 用户名/密码或 token 被拒
	"permission denied (publickey)",         // ssh 公钥不在远端白名单
	"could not read from remote repository", // ssh 权限 companion 行
	"returned error: 403",                   // HTTP 403（token 权限不足等）
	"403 forbidden",
}

// authReject reports whether git's stderr is a credential/permission
// refusal (as opposed to a dirty tree, a bad ref, a merge conflict…).
func authReject(detail string) bool {
	l := strings.ToLower(detail)
	for _, fp := range authRejectClassifiers {
		if strings.Contains(l, fp) {
			return true
		}
	}
	return false
}

// AuthHint is the credential guidance appended to auth-class refusals
// （面板 toast 与探测行共用同一句——出路只有一条：终端里配好凭据）。
func AuthHint() string {
	return i18n.S("（远端需要凭据——在终端里对这棵树手动 git fetch 一次，配好凭据或 ssh 后回面板重试）")
}

// probeTimeout is one remote's reachability probe（网络抖动不该拖垮整
// 张探测表；fetch 档的三分钟是给大仓库首拉的，探测只问 refs）。
const probeTimeout = 30 * time.Second

// resolveRemote names the push target: 显式名须在册；缺省 origin；没有
// origin 且恰有一个远端时取它；一个远端都没有时如实报——推是发布，
// 推错地方比不推更糟。
func (r *Repo) resolveRemote(ctx context.Context, name string) (string, error) {
	remotes, err := r.Remotes(ctx)
	if err != nil {
		return "", err
	}
	if len(remotes) == 0 {
		return "", errors.New(i18n.S("仓库没有配置任何远端——先在终端 git remote add origin <url> 再推"))
	}
	names := make([]string, 0, len(remotes))
	for _, rm := range remotes {
		names = append(names, rm.Name)
	}
	if name == "" {
		for _, rm := range remotes {
			if rm.Name == "origin" {
				return "origin", nil
			}
		}
		if len(remotes) == 1 {
			return remotes[0].Name, nil
		}
		return "", errors.New(i18n.Sf("仓库有 %d 个远端（%s）——请指名要推哪个", len(remotes), strings.Join(names, "、")))
	}
	for _, rm := range remotes {
		if rm.Name == name {
			return name, nil
		}
	}
	return "", errors.New(i18n.Sf("远端 %q 不在该仓库的配置里（现有：%s）", name, strings.Join(names, "、")))
}

// Push pushes branch（""=当前分支）to remote（""=origin，唯一远端时取
// 它），-u 顺手钉上游（幂等）。不动工作树一个文件——不设脏树护栏；
// 分叉被远端拒绝、凭据不认，文案原文透传（授权类由 GitError.Auth 分
// 流）。返回解析后的远端名（房间留痕要写清楚推去了哪）。
func (r *Repo) Push(ctx context.Context, remote, branch string) (string, error) {
	if branch == "" {
		st, err := r.Status(ctx)
		if err != nil {
			return "", err
		}
		if st.Branch == "" {
			return "", errors.New(i18n.S("工作区处于游离 HEAD——无从推送，先切回分支再推"))
		}
		branch = st.Branch
	}
	if !ValidRefName(branch) {
		return "", RefNameError(i18n.S("分支名"), branch)
	}
	remote, err := r.resolveRemote(ctx, remote)
	if err != nil {
		return "", err
	}
	if err := runErr(runGit(ctx, r.Workspace, fetchTimeout, "push", "-u", remote, branch)); err != nil {
		return "", err
	}
	return remote, nil
}

// PullFFOnly fast-forwards the workspace's current branch to its
// upstream（--ff-only：分叉时 git 拒绝，绝不制造合并提交——分叉的收
// 拾是人的判断）。上游缺失先行一步用人话报。返回分支名与实际进来的
// 提交数（pull 前后 HEAD 之间的 rev-list 计数——status 的 behind 是
// 上次 fetch 的快照，拿来当「进了几笔」会把刚 fetch 过的世界说错）。
func (r *Repo) PullFFOnly(ctx context.Context) (string, int, error) {
	st, err := r.Status(ctx)
	if err != nil {
		return "", 0, err
	}
	if st.Branch == "" {
		return "", 0, errors.New(i18n.S("工作区处于游离 HEAD——无从拉取更新，先切回分支"))
	}
	if st.Upstream == "" {
		return "", 0, errors.New(i18n.Sf("分支 %s 没有上游——先推送一次（push -u）钉上，或先在面板 fetch", st.Branch))
	}
	pre, err := r.HeadSHA(ctx)
	if err != nil {
		return st.Branch, 0, err
	}
	if err := runErr(runGit(ctx, r.Workspace, fetchTimeout, "pull", "--ff-only")); err != nil {
		return st.Branch, 0, errors.New(i18n.Sf("快进失败（工作区保持原状）：%v", err))
	}
	post, err := r.HeadSHA(ctx)
	if err != nil {
		return st.Branch, 0, err
	}
	incoming := 0
	if post != pre {
		if n, cerr := r.AheadCount(ctx, pre, post); cerr == nil {
			incoming = n
		}
	}
	return st.Branch, incoming, nil
}

// AuthState is one remote's reachability verdict（远端授权探测的一行）。
type AuthState struct {
	Remote string `json:"remote"`
	URL    string `json:"url"`
	OK     bool   `json:"ok"`
	Auth   bool   `json:"auth,omitempty"`   // 凭据/权限类拒绝
	Reason string `json:"reason,omitempty"` // git 原文（截尾 200 字，人话指引在 hint）
	Hint   string `json:"hint,omitempty"`   // auth 类附带的一句出路
}

// AuthProbe asks the remote for its heads（git ls-remote，与 fetch 走
// 同一条凭据链）——不落任何本地状态，是「这根远端认不认我」的体检。
// 探测永不返回 error：结果就是这一行（网络不通、凭据被拒都是如实
// 的 Reason），调用方逐行渲染即可。
func (r *Repo) AuthProbe(ctx context.Context, remote string) AuthState {
	st := AuthState{Remote: remote}
	for _, rm := range func() []Remote {
		rs, _ := r.Remotes(ctx)
		return rs
	}() {
		if rm.Name == remote {
			st.URL = rm.URL
			break
		}
	}
	if st.URL == "" {
		st.Reason = i18n.S("远端不存在——git remote 里没有这个名字")
		return st
	}
	_, err := runGit(ctx, r.Workspace, probeTimeout, "ls-remote", "--heads", remote)
	if err == nil {
		st.OK = true
		return st
	}
	var ge *GitError
	if errors.As(err, &ge) {
		st.Auth = ge.Auth
		if ge.Timeout {
			st.Reason = i18n.Sf("探测超时（%s）——网络太慢或远端挂了", ge.Span)
		} else {
			st.Reason = clipReason(ge.Detail, 200)
		}
	} else {
		st.Reason = clipReason(err.Error(), 200)
	}
	if st.Auth {
		st.Hint = AuthHint()
	}
	return st
}

// clipReason clips git's stderr to n runes for probe rows/panel tooltips.
func clipReason(s string, n int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) > n {
		return string(r[:n]) + i18n.S("……（后文截断）")
	}
	return string(r)
}
