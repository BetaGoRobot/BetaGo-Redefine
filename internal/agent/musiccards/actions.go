// Package musiccards owns the durable music presentation lifecycle. Its queue
// work uses the agent's existing leases; callbacks perform no remote effects.
package musiccards

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"strings"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
)

type Request struct {
	Kind  string `json:"kind,omitempty"`
	ID    int64  `json:"id,omitempty"`
	Scene string `json:"scene,omitempty"`
	Query string `json:"query,omitempty"`
	Page  int    `json:"page,omitempty"`
}

type Receipt struct {
	MessageID string `json:"message_id,omitempty"`
	SessionID string `json:"session_id"`
	State     string `json:"state"`
}
type ActionInvocation struct {
	Scope                              domain.Scope
	ActorID, EventID, MessageID, Token string
}
type actionClaims struct {
	Session string       `json:"session"`
	Scope   domain.Scope `json:"scope"`
	Version int64        `json:"version"`
	Action  string       `json:"action"`
	ID      int64        `json:"id,omitempty"`
	Page    int          `json:"page,omitempty"`
	Expires int64        `json:"expires"`
}

func normalizeRequest(r Request) (Request, error) {
	validKind := func(k string) bool { return k == "song" || k == "album" || k == "playlist" }
	if r.Page == 0 {
		r.Page = 1
	}
	if r.Page < 1 || r.Page > 2001 {
		return r, domain.ErrInvalid
	}
	if r.Kind != "" {
		if !validKind(r.Kind) || r.ID <= 0 || r.Query != "" || r.Scene != "" {
			return r, domain.ErrInvalid
		}
	} else if !validKind(r.Scene) || r.ID != 0 || strings.TrimSpace(r.Query) == "" || len(r.Query) > 1024 {
		return r, domain.ErrInvalid
	}
	return r, nil
}
func actionMAC(key, body []byte) []byte {
	purpose := hmac.New(sha256.New, key)
	purpose.Write([]byte("betago/music-card-action/v1"))
	mac := hmac.New(sha256.New, purpose.Sum(nil))
	mac.Write(body)
	return mac.Sum(nil)
}
func signAction(key []byte, c actionClaims) string {
	body, _ := json.Marshal(c)
	return base64.RawURLEncoding.EncodeToString(body) + "." + base64.RawURLEncoding.EncodeToString(actionMAC(key, body))
}
func verifyAction(key []byte, token string, now time.Time) (actionClaims, error) {
	var c actionClaims
	if len(token) > 8192 {
		return c, domain.ErrUnauthorized
	}
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return c, domain.ErrUnauthorized
	}
	body, e := base64.RawURLEncoding.DecodeString(parts[0])
	sig, e2 := base64.RawURLEncoding.DecodeString(parts[1])
	if e != nil || e2 != nil || !hmac.Equal(actionMAC(key, body), sig) || json.Unmarshal(body, &c) != nil || c.Scope.Validate() != nil || c.Session == "" || c.Version < 1 || c.Expires <= now.Unix() || c.Expires > now.Add(7*24*time.Hour+time.Minute).Unix() {
		return c, domain.ErrUnauthorized
	}
	switch c.Action {
	case "page", "play", "album", "lyrics", "refresh", "withdraw":
	default:
		return c, domain.ErrUnauthorized
	}
	return c, nil
}
