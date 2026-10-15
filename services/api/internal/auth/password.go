package auth

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// DefaultPBKDF2Iterations follows the OWASP guidance for PBKDF2-HMAC-SHA256.
// Uses the Go standard library (crypto/pbkdf2, Go 1.24+). Moving to argon2id needs
// golang.org/x/crypto and a decision record; NeedsRehash enables a transparent upgrade.
const DefaultPBKDF2Iterations = 600_000

const (
	hashScheme = "pbkdf2-sha256"
	saltLen    = 16
	keyLen     = 32
)

type Hasher struct{ Iterations int }

func NewHasher() Hasher { return Hasher{Iterations: DefaultPBKDF2Iterations} }

func (h Hasher) Hash(password string) (string, error) {
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key, err := pbkdf2.Key(sha256.New, password, salt, h.Iterations, keyLen)
	if err != nil {
		return "", err
	}
	enc := base64.RawStdEncoding
	return fmt.Sprintf("%s$%d$%s$%s", hashScheme, h.Iterations, enc.EncodeToString(salt), enc.EncodeToString(key)), nil
}

var errBadHash = errors.New("malformed password hash")

func parseHash(encoded string) (iter int, salt, key []byte, err error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 4 || parts[0] != hashScheme {
		return 0, nil, nil, errBadHash
	}
	iter, err = strconv.Atoi(parts[1])
	if err != nil || iter < 1 {
		return 0, nil, nil, errBadHash
	}
	enc := base64.RawStdEncoding
	if salt, err = enc.DecodeString(parts[2]); err != nil {
		return 0, nil, nil, errBadHash
	}
	if key, err = enc.DecodeString(parts[3]); err != nil || len(key) == 0 {
		return 0, nil, nil, errBadHash
	}
	return iter, salt, key, nil
}

// Verify compares in constant time. A malformed stored hash verifies as false.
func (h Hasher) Verify(password, encoded string) bool {
	iter, salt, key, err := parseHash(encoded)
	if err != nil {
		return false
	}
	got, err := pbkdf2.Key(sha256.New, password, salt, iter, len(key))
	if err != nil {
		return false
	}
	return subtle.ConstantTimeCompare(got, key) == 1
}

// NeedsRehash reports whether a stored hash uses weaker parameters than the current ones.
func (h Hasher) NeedsRehash(encoded string) bool {
	iter, _, _, err := parseHash(encoded)
	return err != nil || iter < h.Iterations
}

// ValidatePassword enforces the password policy. Returns a user-facing message or "".
func ValidatePassword(password, email string) string {
	switch {
	case len(password) < 12:
		return "Password must be at least 12 characters."
	case len(password) > 128:
		return "Password must be at most 128 characters."
	case strings.EqualFold(password, email):
		return "Password must not be the same as your email."
	}
	return ""
}
