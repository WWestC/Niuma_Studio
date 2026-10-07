package requirements

// helpers.go — the ledger's shared validation and refusal copy. Every
// Store implementation speaks these (the file-backed LocalStore and
// the single-database sqlstore alike): the input clamps' trim order,
// the state-machine refusals and the not-found shapes are CONTRACT —
// server/dispatch tests assert their substrings, and sqlstore's
// error-parity test pins both backends to identical bytes. A wording
// change here is a wording change everywhere; a wording change in one
// backend is a bug the parity test catches.

import (
	"errors"
	"fmt"
	"strings"

	"github.com/WWestC/Niuma_Studio/i18n"
	"github.com/WWestC/Niuma_Studio/projects"
)

// SanitizeCreate applies the ledger's input clamps: prose fields are
// trimmed then rune-capped (MaxTitle/MaxBody), the provenance name is
// house-styled (single line, trimmed, ≤24 runes). The trim happens
// BEFORE the emptiness gate — a whitespace-only title must read as
// empty, not as a title of spaces.
func SanitizeCreate(title, body, createdBy string) (string, string, string) {
	title = clampRunes(strings.TrimSpace(title), MaxTitle)
	body = clampRunes(strings.TrimSpace(body), MaxBody)
	return title, body, sanitizeBy(createdBy)
}

// ValidateCreate is the create gate: a legal project key and a
// non-empty (post-trim) title.
func ValidateCreate(projectKey, title string) error {
	if !projects.ValidKey(projectKey) {
		return errors.New(i18n.Sf("非法项目 key %q", projectKey))
	}
	if title == "" {
		return errors.New(i18n.S("需求标题不能为空"))
	}
	return nil
}

// ErrNoShelf / ErrMissing — the not-found shapes: an unknown project
// names the missing shelf, a known shelf with a wrong id does not.
func ErrNoShelf(id, projectKey string) error {
	return errors.New(i18n.Sf("需求不存在: %s（项目 %s 无需求架）", id, projectKey))
}

func ErrMissing(id, projectKey string) error {
	return errors.New(i18n.Sf("需求不存在: %s（项目 %s）", id, projectKey))
}

// ErrWrongState wraps a state-machine refusal with the id and the
// status that refused it ("仅 open 需求可标记拆解（r_01 当前 split…）").
func ErrWrongState(refusal, id, status string) error {
	return errors.New(i18n.Sf("%s（%s 当前 %s）", refusal, id, status))
}

// ErrParkOnlyOpen — parking's own refusal shape (it points at unpark).
func ErrParkOnlyOpen(id, status string) error {
	return errors.New(i18n.Sf("仅 open 需求可暂缓（%s 当前 %s：先 unpark 再操作）", id, status))
}

func ErrAlreadyClosed(id string) error {
	return errors.New(i18n.Sf("需求 %s 已关闭", id))
}

func ErrCloseWhileParking(id string) error {
	return errors.New(i18n.Sf("需求 %s 暂缓中，先 unpark 再关闭（暂缓的等待条件未核对）", id))
}

func ErrDeleteOnly(id, status string) error {
	return errors.New(i18n.Sf("仅 open/parking 需求可删除（%s 当前 %s：已拆解的任务树与已关闭的留档不随删）", id, status))
}

// ErrNoShelfRenumber — RenumberApply's no-shelf refusal (fmt, not
// i18n: an operator-facing migration error, never a room face).
func ErrNoShelfRenumber(projectKey string) error {
	return fmt.Errorf("项目 %s 无需求架", projectKey)
}

// ErrSingleSubject is the single-user engine's honest refusal: the
// in-memory shape (and any backend bound to "") has no counterpart
// for a non-empty storage subject — serving it would silently share
// one subject's data with another. Multi-user mode requires the
// single database (boot enforces it); this refusal is the backstop
// for embedders and tests.
func ErrSingleSubject(subject string) error {
	return errors.New(i18n.Sf("需求台账是单用户形态（收到主体 %q）——多用户命名空间需 sqlite 单库", subject))
}

// ErrSubjectMismatch is the bound engine's refusal for a foreign
// subject: the engine opened under one namespace and will not serve
// another (a wiring bug, never a runtime shape).
func ErrSubjectMismatch(bound, got string) error {
	return errors.New(i18n.Sf("需求台账引擎绑定主体 %q（收到 %q）——主体不匹配", bound, got))
}
