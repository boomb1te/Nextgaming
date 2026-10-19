package core

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
)

// Member passwords. A member (authorized client) gets a short, system-generated
// password that is shown once to the operator and stored only as a salted,
// iterated hash. Verification happens wherever a login lives (today nowhere; the
// mobile app later). We use PBKDF2-HMAC-SHA256 from the standard library so the
// build stays dependency-free; the stored string names its own algorithm and
// parameters, so a future move to bcrypt/argon2 can re-hash on next login.
const (
	pwIterations = 100_000
	pwSaltBytes  = 16
	pwKeyBytes   = 32
	// memberPwLen / memberPwAlphabet: 8 characters from a 31-symbol set with no
	// look-alikes (0/O, 1/l/I removed) so an operator can read it aloud or write
	// it on a card without confusion. ~40 bits of entropy.
	memberPwLen      = 8
	memberPwAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
)

// GenerateMemberPassword returns a fresh human-readable one-time password.
func GenerateMemberPassword() (string, error) {
	n := len(memberPwAlphabet)
	limit := 256 - (256 % n) // reject bytes at/above this to avoid modulo bias
	out := make([]byte, 0, memberPwLen)
	buf := make([]byte, 1)
	for len(out) < memberPwLen {
		if _, err := rand.Read(buf); err != nil {
			return "", err
		}
		if int(buf[0]) >= limit {
			continue
		}
		out = append(out, memberPwAlphabet[int(buf[0])%n])
	}
	return string(out), nil
}

// HashPassword returns an encoded hash of the form
// "pbkdf2_sha256$<iter>$<saltB64>$<hashB64>" (RawStd base64).
func HashPassword(plain string) (string, error) {
	salt := make([]byte, pwSaltBytes)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	dk := pbkdf2SHA256([]byte(plain), salt, pwIterations, pwKeyBytes)
	enc := base64.RawStdEncoding.EncodeToString
	return fmt.Sprintf("pbkdf2_sha256$%d$%s$%s", pwIterations, enc(salt), enc(dk)), nil
}

// VerifyPassword reports whether plain matches a previously encoded hash. It is
// constant-time and returns false on any malformed input.
func VerifyPassword(encoded, plain string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 4 || parts[0] != "pbkdf2_sha256" {
		return false
	}
	iter, err := strconv.Atoi(parts[1])
	if err != nil || iter < 1 {
		return false
	}
	salt, err1 := base64.RawStdEncoding.DecodeString(parts[2])
	want, err2 := base64.RawStdEncoding.DecodeString(parts[3])
	if err1 != nil || err2 != nil || len(want) == 0 {
		return false
	}
	got := pbkdf2SHA256([]byte(plain), salt, iter, len(want))
	return subtle.ConstantTimeCompare(got, want) == 1
}

// pbkdf2SHA256 is the PBKDF2 key-derivation function (RFC 2898) over HMAC-SHA256.
func pbkdf2SHA256(password, salt []byte, iter, keyLen int) []byte {
	const hLen = sha256.Size
	blocks := (keyLen + hLen - 1) / hLen
	dk := make([]byte, 0, blocks*hLen)
	U := make([]byte, hLen)
	for b := 1; b <= blocks; b++ {
		mac := hmac.New(sha256.New, password)
		mac.Write(salt)
		mac.Write([]byte{byte(b >> 24), byte(b >> 16), byte(b >> 8), byte(b)})
		T := mac.Sum(nil)
		copy(U, T)
		for i := 2; i <= iter; i++ {
			mac.Reset()
			mac.Write(U)
			U = mac.Sum(U[:0])
			for j := range T {
				T[j] ^= U[j]
			}
		}
		dk = append(dk, T...)
	}
	return dk[:keyLen]
}
