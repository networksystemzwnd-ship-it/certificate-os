package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
)

// NewSessionToken returns a high-entropy opaque token (sent to the client once) and its
// SHA-256 hash (the only form stored). A database leak therefore does not yield usable sessions.
func NewSessionToken() (token string, hash []byte, err error) {
	b := make([]byte, 32)
	if _, err = rand.Read(b); err != nil {
		return "", nil, err
	}
	token = base64.RawURLEncoding.EncodeToString(b)
	return token, HashToken(token), nil
}

func HashToken(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}
