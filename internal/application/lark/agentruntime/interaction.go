package agentruntime

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"strings"
)

func HashInteractionToken(token string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(token)))
	return hex.EncodeToString(sum[:])
}

func MatchInteractionToken(token, hash string) bool {
	if strings.TrimSpace(token) == "" {
		return false
	}
	actual, err := hex.DecodeString(HashInteractionToken(token))
	if err != nil {
		return false
	}
	expected, err := hex.DecodeString(strings.TrimSpace(hash))
	if err != nil || len(expected) != sha256.Size {
		return false
	}
	return subtle.ConstantTimeCompare(actual, expected) == 1
}
