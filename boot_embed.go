package main

import (
	"embed"
	"io/fs"
	"log"
	"os"
	"path/filepath"

	"github.com/WWestC/Niuma_Studio/server"
	"github.com/WWestC/Niuma_Studio/util"
)

// boot_embed.go — the single-binary embed face: the workbench
// frontend, the built-in plugin market, and the version stamp (git
// tag is the only truth; build.sh stamps it in). Split out of main.go
// so the composition function reads as composition.

var version = "dev"

// webFS embeds the Web workbench statics (v2 P1): index.html plus
// assets, served at GET /app — same single-binary discipline as
// kb/manual.md.
//
//go:embed web
var webFS embed.FS

// builtinPlugins embeds the factory-seeded official plugins (M2): the
// market board itself ships with the binary — a fresh machine gets the
// 插件市场 on first boot without any network round-trip, and a user who
// removed it (tombstone) keeps it gone.
//
//go:embed examples/plugins/niuma.market
var builtinPlugins embed.FS

// marketSeedFS returns the embedded market plugin's subtree (nil only
// on an embed failure that cannot happen with the directive). Kept out
// of main: the local `fs` FlagSet shadows io/fs in there.
func marketSeedFS() fs.FS {
	sub, err := fs.Sub(builtinPlugins, "examples/plugins/niuma.market")
	if err != nil {
		return nil
	}
	return sub
}

// webRoot returns the embedded web/ subtree (nil disables /app — only
// on an embed failure, which cannot happen with the directive above).
func webRoot() fs.FS {
	sub, err := fs.Sub(webFS, "web")
	if err != nil {
		log.Printf("web embed: %v — /app disabled", err)
		return nil
	}
	return sub
}

// devWebRoot resolves the NIUMA_DEV=1 asset source: the repo's web/
// read straight off disk (edit → the window reloads itself over
// /app/__reload — no rebuild, no restart). Falls back to the embed
// with a loud log when the disk tree isn't under the cwd (the room
// must never refuse to boot over a dev convenience). Returns the FS,
// the dev flag and the watched directory ("" when not dev).
func devWebRoot() (fs.FS, bool, string) {
	if util.Env("DEV") != "1" { // util.Env adds the NIUMA_ prefix itself: NIUMA_DEV=1
		return webRoot(), false, ""
	}
	wd, _ := os.Getwd()
	dir := filepath.Join(wd, "web")
	if disk := server.WebDevRoot(dir); disk != nil {
		return disk, true, dir
	}
	server.DevAbsentWarn(dir)
	return webRoot(), false, ""
}
