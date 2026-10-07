package util

import (
	"time"

	"github.com/WWestC/Niuma_Studio/i18n"
)

func ShortDur(d time.Duration) string {
	mins := int(d.Minutes())
	if mins < 1 {
		return i18n.S("刚发生")
	}
	return i18n.Sf("%d 分钟", mins)
}
