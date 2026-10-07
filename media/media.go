package media

// media.go — 输入图片的落盘仓库（聊天发图）：say 行携带的图片不是
// base64 内嵌在线程里（历史 jsonl 会被一张截图撑爆），而是先进仓库
// 换一个稳定 ID，线上只走引用：
//
//	POST /media 上传 → Image{ID,…} → say 帧 images:[…] 引用
//	GET  /media/{id} 出字节（前端 <img> 直出）
//	调度器注入时 media.Store.Path(id) 还原绝对路径，走
//	session/send 的 attachments（kind:image + localPath）让成员
//	的会话真看到图，而不是只读到一句「有图」。
//
// 仓库是工作室级的（~/.niuma/media/），不按项目分房：图片的归属
// 关系在聊天历史里（哪条 say 引用了哪个 id），仓库只管「ID ↔ 字节」。
// 信任模型与整面一致：回环即房主，上传不设房主校验；但 MIME 白名单
// 与尺寸帽照收（trace/file 端点的同款纪律——只收图片、8MB 帽）。

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/WWestC/Niuma_Studio/i18n"
	"github.com/WWestC/Niuma_Studio/persist"
)

// MaxBytes is the one image's size cap (8MB — a screenshot-grade PNG
// fits; a mislabeled monster does not; trace/file's serving cap).
const MaxBytes = 8 << 20

// MaxPerSay caps the images one say line may carry (Feishu sends them
// in a grid of nine; a fatter batch is a paste accident).
const MaxPerSay = 9

// mimeExts folds the accepted MIME types to file extensions — also the
// probing order Path walks (the extension on disk IS the type record).
var mimeExts = map[string]string{
	"image/png": ".png", "image/jpeg": ".jpg", "image/gif": ".gif",
	"image/webp": ".webp", "image/bmp": ".bmp",
}

// Image is one stored image's wire shape — the say frame's reference
// (chat.Message.Images), the upload receipt and the history record all
// carry exactly this. W/H are the client-declared pixel dimensions
// (display-only hints; the server never decodes the bitmap).
type Image struct {
	ID    string `json:"id"`
	Name  string `json:"name,omitempty"` // 原文件名（展示用，无路径语义）
	Mime  string `json:"mime,omitempty"`
	W     int    `json:"w,omitempty"`
	H     int    `json:"h,omitempty"`
	Bytes int64  `json:"bytes,omitempty"`
}

// Store is the on-disk image warehouse over one root directory.
// The zero value is not usable — NewStore builds it.
type Store struct {
	root string
}

// NewStore builds the store, creating root best-effort (a failed mkdir
// only degrades saves — errors surface per call, never at boot).
func NewStore(root string) *Store {
	_ = os.MkdirAll(root, 0o755)
	return &Store{root: root}
}

// Root is the warehouse directory ("" = the zero store: every Save
// fails, every Path misses — images degrade to textual notes).
func (s *Store) Root() string {
	if s == nil {
		return ""
	}
	return s.root
}

// Save stores one image and hands back its reference. Empty or
// over-cap data, and a MIME neither on the whitelist nor deducible
// from the filename's extension, are refused — the caller surfaces the
// reason to the uploader.
func (s *Store) Save(data []byte, name, mime string) (Image, error) {
	if s == nil || s.root == "" {
		return Image{}, errors.New(i18n.S("media: 仓库不可用（未配置目录）"))
	}
	if len(data) == 0 {
		return Image{}, errors.New(i18n.S("media: 空文件"))
	}
	if len(data) > MaxBytes {
		return Image{}, errors.New(i18n.Sf("media: 超过 %dMB 尺寸帽", MaxBytes>>20))
	}
	mime = strings.ToLower(strings.TrimSpace(strings.Split(mime, ";")[0]))
	if _, ok := mimeExts[mime]; !ok {
		// 文件名后缀兜底（Content-Type 缺失或 application/
		// octet-stream 的粘贴上传）：认得的图片后缀换回规范 MIME。
		var ok bool
		if mime, ok = mimeOfName(name); !ok {
			return Image{}, errors.New(i18n.S("media: 只收图片（png/jpg/gif/webp/bmp）"))
		}
	}
	var buf [6]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return Image{}, err
	}
	id := "img_" + hex.EncodeToString(buf[:])
	path := filepath.Join(s.root, id+mimeExts[mime])
	if err := persist.Save(path, data, 0o644); err != nil {
		return Image{}, err
	}
	return Image{ID: id, Name: name, Mime: mime, Bytes: int64(len(data))}, nil
}

// mimeOfName reads a recognizable image extension back into the canon
// MIME (the Save fallback's other arm).
func mimeOfName(name string) (string, bool) {
	switch strings.ToLower(filepath.Ext(strings.TrimSpace(name))) {
	case ".png":
		return "image/png", true
	case ".jpg", ".jpeg":
		return "image/jpeg", true
	case ".gif":
		return "image/gif", true
	case ".webp":
		return "image/webp", true
	case ".bmp":
		return "image/bmp", true
	}
	return "", false
}

// Path resolves id back to its absolute file path — the id itself
// carries no extension (a stable reference survives re-encodes), so
// the whitelist is probed on disk. "" when the id is malformed or the
// file is gone: the caller degrades to a note, never a failed
// delivery.
func (s *Store) Path(id string) string {
	path, _, _, ok := s.Lookup(id)
	if !ok {
		return ""
	}
	return path
}

// Lookup is the serving/injection resolve: id → (absolute path, canon
// MIME, byte size, found). The same disk probe as Path with the type
// and size the GET face and the attachment param want.
func (s *Store) Lookup(id string) (path, mime string, size int64, ok bool) {
	if s == nil || s.root == "" || !ValidID(id) {
		return "", "", 0, false
	}
	for m, ext := range mimeExts {
		p := filepath.Join(s.root, id+ext)
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			return p, m, fi.Size(), true
		}
	}
	return "", "", 0, false
}

// ValidID reports whether id is a well-formed warehouse reference
// (img_ + 12 hex) — the GET /media/{id} guard's parse half.
func ValidID(id string) bool {
	if len(id) != 16 || !strings.HasPrefix(id, "img_") {
		return false
	}
	_, err := hex.DecodeString(id[4:])
	return err == nil
}
