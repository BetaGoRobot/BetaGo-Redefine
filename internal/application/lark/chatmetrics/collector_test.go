package chatmetrics

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestCollectorReportsMembersAndRecentMessagesForEachChat(t *testing.T) {
	now := time.Date(2026, 5, 29, 12, 0, 0, 0, time.UTC)
	var recorded []Snapshot

	collector := Collector{
		ListChats: func(context.Context) ([]Chat, error) {
			return []Chat{
				{ID: "oc_1", Name: "Alpha", Status: "active"},
				{ID: "oc_2", Name: "Beta", Status: "active"},
			}, nil
		},
		CountMembers: func(_ context.Context, chatID string) (int, error) {
			switch chatID {
			case "oc_1":
				return 3, nil
			case "oc_2":
				return 5, nil
			default:
				t.Fatalf("unexpected chatID for CountMembers: %s", chatID)
				return 0, nil
			}
		},
		CountRecentMessages: func(_ context.Context, chatID string, since time.Time) (int, error) {
			if !since.Equal(now.Add(-24 * time.Hour)) {
				t.Fatalf("since = %s, want %s", since, now.Add(-24*time.Hour))
			}
			switch chatID {
			case "oc_1":
				return 11, nil
			case "oc_2":
				return 0, nil
			default:
				t.Fatalf("unexpected chatID for CountRecentMessages: %s", chatID)
				return 0, nil
			}
		},
		Record: func(snapshot Snapshot) {
			recorded = append(recorded, snapshot)
		},
		Now:          func() time.Time { return now },
		RecentWindow: 24 * time.Hour,
	}

	if err := collector.Collect(context.Background()); err != nil {
		t.Fatalf("Collect() error = %v", err)
	}

	if len(recorded) != 2 {
		t.Fatalf("recorded %d snapshots, want 2: %+v", len(recorded), recorded)
	}
	if recorded[0].Chat.ID != "oc_1" || recorded[0].MemberCount != 3 || recorded[0].RecentMessageCount != 11 {
		t.Fatalf("recorded[0] = %+v", recorded[0])
	}
	if recorded[1].Chat.ID != "oc_2" || recorded[1].MemberCount != 5 || recorded[1].RecentMessageCount != 0 {
		t.Fatalf("recorded[1] = %+v", recorded[1])
	}
}

func TestCollectorContinuesWhenOneChatFails(t *testing.T) {
	var recorded []Snapshot
	memberErr := errors.New("member count failed")

	collector := Collector{
		ListChats: func(context.Context) ([]Chat, error) {
			return []Chat{
				{ID: "oc_bad", Name: "Bad"},
				{ID: "oc_ok", Name: "OK"},
			}, nil
		},
		CountMembers: func(_ context.Context, chatID string) (int, error) {
			if chatID == "oc_bad" {
				return 0, memberErr
			}
			return 8, nil
		},
		CountRecentMessages: func(context.Context, string, time.Time) (int, error) {
			return 4, nil
		},
		Record: func(snapshot Snapshot) {
			recorded = append(recorded, snapshot)
		},
		Now:          func() time.Time { return time.Unix(0, 0).UTC() },
		RecentWindow: time.Hour,
	}

	err := collector.Collect(context.Background())
	if !errors.Is(err, memberErr) {
		t.Fatalf("Collect() error = %v, want memberErr", err)
	}
	if len(recorded) != 1 || recorded[0].Chat.ID != "oc_ok" {
		t.Fatalf("recorded = %+v, want only oc_ok", recorded)
	}
}
