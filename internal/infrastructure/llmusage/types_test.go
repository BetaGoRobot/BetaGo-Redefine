package llmusage

import (
	"testing"
	"time"
)

func TestNormalizeScopeDefaultsAndTrimsFields(t *testing.T) {
	scope := NormalizeScope(Scope{
		ChatID:     " oc_chat ",
		ChatName:   " Test Chat ",
		OpenID:     " ou_actor ",
		UserName:   " Alice ",
		SourceType: "",
		Source:     "",
	})

	if scope.ChatID != "oc_chat" {
		t.Fatalf("ChatID = %q, want %q", scope.ChatID, "oc_chat")
	}
	if scope.ChatName != "Test Chat" {
		t.Fatalf("ChatName = %q, want %q", scope.ChatName, "Test Chat")
	}
	if scope.OpenID != "ou_actor" {
		t.Fatalf("OpenID = %q, want %q", scope.OpenID, "ou_actor")
	}
	if scope.UserName != "Alice" {
		t.Fatalf("UserName = %q, want %q", scope.UserName, "Alice")
	}
	if scope.SourceType != SourceTypeSystem {
		t.Fatalf("SourceType = %q, want %q", scope.SourceType, SourceTypeSystem)
	}
	if scope.Source != "unknown" {
		t.Fatalf("Source = %q, want %q", scope.Source, "unknown")
	}
}

func TestNormalizeScopeAllowsBackgroundWithoutUser(t *testing.T) {
	scope := NormalizeScope(Scope{
		ChatID:     "oc_background",
		SourceType: SourceTypeBackground,
		Source:     "chunking",
	})

	if scope.OpenID != "" {
		t.Fatalf("OpenID = %q, want empty", scope.OpenID)
	}
	if scope.UserName != "" {
		t.Fatalf("UserName = %q, want empty", scope.UserName)
	}
	if scope.SourceType != SourceTypeBackground {
		t.Fatalf("SourceType = %q, want %q", scope.SourceType, SourceTypeBackground)
	}
}

func TestBucketTimes(t *testing.T) {
	createdAt := time.Date(2026, 5, 28, 9, 10, 42, 123456789, time.FixedZone("CST", 8*60*60))
	buckets := BucketTimes(createdAt)

	if want := time.Date(2026, 5, 28, 9, 10, 0, 0, createdAt.Location()); !buckets.Minute.Equal(want) {
		t.Fatalf("minute = %s, want %s", buckets.Minute, want)
	}
	if want := time.Date(2026, 5, 28, 9, 0, 0, 0, createdAt.Location()); !buckets.Hour.Equal(want) {
		t.Fatalf("hour = %s, want %s", buckets.Hour, want)
	}
	if want := time.Date(2026, 5, 28, 0, 0, 0, 0, createdAt.Location()); !buckets.Day.Equal(want) {
		t.Fatalf("day = %s, want %s", buckets.Day, want)
	}
}
