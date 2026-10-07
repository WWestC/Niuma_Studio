package identity

// credential.go — the password discipline: per-account 16-byte random
// salt (crypto/rand, failure is fatal — no predictable fallback, the
// same law the seat/gate tokens mint under) and PBKDF2-HMAC-SHA256
// over the password. The hash lives only in the accounts table and
// never leaves the process; comparisons are constant-time.

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
)

const (
	// SaltLen is the per-account salt size in bytes.
	SaltLen = 16
	// HashLen is the derived key size in bytes.
	HashLen = 32
	// pbkdf2Iterations is the work factor (OWASP 2023 guidance for
	// PBKDF2-HMAC-SHA256 passwords: 600k for interactive, 210k as the
	// accepted floor — a local-first studio on someone's laptop takes
	// the floor; raising it later is one constant away and old hashes
	// keep verifying since the iteration count is not stored per
	// account... which is exactly why it must NOT change silently:
	// bumping it invalidates every stored hash. It is a migration
	// decision, documented here).
	pbkdf2Iterations = 210_000
)

// dummySalt/dummyHash feed the unknown-username decoy verification in
// Login: fixed bytes derived once so the timing shape of a miss
// matches a real check (the VALUES are public and useless — they only
// ever compare against a password that already failed on existence).
var (
	dummySalt = pbkdf2Key([]byte("niuma-decoy"), []byte("niuma-decoy-salt"), pbkdf2Iterations, SaltLen)
	dummyHash = pbkdf2Key([]byte("niuma-decoy"), dummySalt, pbkdf2Iterations, HashLen)
)

// NewCredential mints a salt and derives the password's hash. A
// crypto/rand failure is returned, never papered over — credentials
// are the one place a predictable fallback would be load-bearing.
func NewCredential(password string) (salt, hash []byte, err error) {
	salt = make([]byte, SaltLen)
	if _, err := rand.Read(salt); err != nil {
		return nil, nil, err
	}
	return salt, pbkdf2Key([]byte(password), salt, pbkdf2Iterations, HashLen), nil
}

// VerifyPassword answers whether password matches the stored
// salt+hash, in constant time over the digest.
func VerifyPassword(salt, hash []byte, password string) bool {
	if len(salt) == 0 || len(hash) == 0 {
		return false
	}
	want := pbkdf2Key([]byte(password), salt, pbkdf2Iterations, len(hash))
	return subtle.ConstantTimeCompare(want, hash) == 1
}

// pbkdf2Key is PBKDF2 (RFC 8018) with HMAC-SHA256 — hand-rolled from
// the standard library because the repo's dependency budget is three
// packages and x/crypto is not one of them. The pinning vectors live
// in credential_test.go.
func pbkdf2Key(password, salt []byte, iter, keyLen int) []byte {
	prf := hmac.New(sha256.New, password)
	hLen := prf.Size()
	numBlocks := (keyLen + hLen - 1) / hLen
	var buf [4]byte
	dk := make([]byte, 0, numBlocks*hLen)
	u := make([]byte, hLen)
	for block := 1; block <= numBlocks; block++ {
		prf.Reset()
		prf.Write(salt)
		buf[0] = byte(block >> 24)
		buf[1] = byte(block >> 16)
		buf[2] = byte(block >> 8)
		buf[3] = byte(block)
		prf.Write(buf[:4])
		dk = prf.Sum(dk)
		t := dk[len(dk)-hLen:]
		copy(u, t)
		for n := 2; n <= iter; n++ {
			prf.Reset()
			prf.Write(u)
			u = prf.Sum(u[:0])
			for i := range t {
				t[i] ^= u[i]
			}
		}
	}
	return dk[:keyLen]
}
