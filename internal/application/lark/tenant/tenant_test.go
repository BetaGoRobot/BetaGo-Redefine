package tenant

import (
	"regexp"
	"strings"
	"testing"
)

func TestNewTenantIsStableAndSeparatesBots(t *testing.T) {
	t.Parallel()

	first, err := New(" cli_app ", " ou_bot_a ")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	repeated, err := New("cli_app", "ou_bot_a")
	if err != nil {
		t.Fatalf("New() repeated error = %v", err)
	}
	second, err := New("cli_app", "ou_bot_b")
	if err != nil {
		t.Fatalf("New() second bot error = %v", err)
	}

	if first != repeated {
		t.Fatalf("tenant is not stable: first=%#v repeated=%#v", first, repeated)
	}
	if first.ID == second.ID {
		t.Fatalf("different bots share tenant ID %q", first.ID)
	}
	if first.AppID != "cli_app" || first.BotOpenID != "ou_bot_a" {
		t.Fatalf("canonical tenant = %#v", first)
	}
	if !regexp.MustCompile(`^bot_[0-9a-f]{24}$`).MatchString(first.ID) {
		t.Fatalf("tenant ID %q does not match canonical format", first.ID)
	}
	if err := first.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}

	tampered := first
	tampered.ID = second.ID
	if err := tampered.Validate(); err == nil {
		t.Fatal("Validate() accepted a tenant ID from another bot")
	}
}

func TestNewTenantRejectsIncompleteIdentity(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name      string
		appID     string
		botOpenID string
	}{
		{name: "missing app", botOpenID: "ou_bot"},
		{name: "missing bot", appID: "cli_app"},
		{name: "both whitespace", appID: " ", botOpenID: "\t"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, err := New(test.appID, test.botOpenID); err == nil {
				t.Fatal("New() accepted incomplete bot identity")
			}
		})
	}
}

func TestTenantDocumentIDNamespacesDomainIDs(t *testing.T) {
	t.Parallel()

	first, err := New("cli_app", "ou_bot_a")
	if err != nil {
		t.Fatal(err)
	}
	second, err := New("cli_app", "ou_bot_b")
	if err != nil {
		t.Fatal(err)
	}

	firstID, err := first.DocumentID("episode-1")
	if err != nil {
		t.Fatalf("DocumentID() error = %v", err)
	}
	secondID, err := second.DocumentID("episode-1")
	if err != nil {
		t.Fatalf("DocumentID() second error = %v", err)
	}
	if firstID != first.ID+":episode-1" {
		t.Fatalf("DocumentID() = %q", firstID)
	}
	if firstID == secondID {
		t.Fatalf("cross-tenant document IDs collide: %q", firstID)
	}
	for _, invalid := range []string{"", " ", " episode-1", "episode-1 "} {
		if _, err := first.DocumentID(invalid); err == nil {
			t.Fatalf("DocumentID(%q) unexpectedly succeeded", invalid)
		}
	}
}

func TestTenantIndexAliasSanitizesAndSeparatesBots(t *testing.T) {
	t.Parallel()

	first, err := New("cli_app", "ou_bot_a")
	if err != nil {
		t.Fatal(err)
	}
	second, err := New("cli_app", "ou_bot_b")
	if err != nil {
		t.Fatal(err)
	}

	firstAlias, err := first.IndexAlias(" Agent Conversation/Evaluations ")
	if err != nil {
		t.Fatalf("IndexAlias() error = %v", err)
	}
	secondAlias, err := second.IndexAlias(" Agent Conversation/Evaluations ")
	if err != nil {
		t.Fatalf("IndexAlias() second error = %v", err)
	}
	want := "agent-conversation-evaluations-" + first.ID
	if firstAlias != want {
		t.Fatalf("IndexAlias() = %q, want %q", firstAlias, want)
	}
	if firstAlias == secondAlias {
		t.Fatalf("cross-tenant aliases collide: %q", firstAlias)
	}
	if len(firstAlias) > 255 || firstAlias != strings.ToLower(firstAlias) {
		t.Fatalf("invalid OpenSearch alias %q", firstAlias)
	}
	for _, invalid := range []string{"", " ", ".", "..", firstAlias} {
		if _, err := first.IndexAlias(invalid); err == nil {
			t.Fatalf("IndexAlias(%q) unexpectedly succeeded", invalid)
		}
	}
}
