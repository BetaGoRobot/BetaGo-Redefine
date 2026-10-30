package musiccards

import (
	"errors"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
	"strings"
	"testing"
	"time"
)

func TestActionTokenBindsFullScopeViewAndPurpose(t *testing.T) {
	key := []byte(strings.Repeat("k", 32))
	c := actionClaims{Session: "session", Scope: domain.Scope{TenantID: "tenant", AppID: "app", ChatID: "group", Visibility: "group"}, Version: 3, Action: "play", ID: 42, Page: 1, Expires: time.Now().Add(time.Hour).Unix()}
	token := signAction(key, c)
	got, err := verifyAction(key, token, time.Now())
	if err != nil || got != c {
		t.Fatalf("roundtrip %#v %v", got, err)
	}
	for _, bad := range []string{token + "x", strings.Replace(token, ".", "x.", 1), "", strings.Repeat("x", 12000)} {
		if _, err = verifyAction(key, bad, time.Now()); !errors.Is(err, domain.ErrUnauthorized) {
			t.Fatalf("accepted altered token %v", err)
		}
	}
	if _, err = verifyAction([]byte(strings.Repeat("q", 32)), token, time.Now()); !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatal("wrong key accepted")
	}
	if _, err = verifyAction(key, token, time.Now().Add(2*time.Hour)); !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatal("expired token accepted")
	}
	if strings.Contains(token, "actor") {
		t.Fatal("token carries actor identity")
	}
}

func TestRequestRejectsMixedSearchAndSelectionAndClampsPage(t *testing.T) {
	for _, r := range []Request{{Kind: "song", ID: 1, Query: "x", Scene: "song"}, {Scene: "song", Query: "x", Page: -1}, {Scene: "song", Query: "x", Page: 2002}, {Kind: "artist", ID: 1}, {Kind: "song", ID: -1}, {Scene: "song", Query: strings.Repeat("x", 1025)}} {
		if _, err := normalizeRequest(r); !errors.Is(err, domain.ErrInvalid) {
			t.Fatalf("accepted %#v %v", r, err)
		}
	}
	r, err := normalizeRequest(Request{Scene: "song", Query: "x"})
	if err != nil || r.Page != 1 {
		t.Fatalf("default page %#v %v", r, err)
	}
}
