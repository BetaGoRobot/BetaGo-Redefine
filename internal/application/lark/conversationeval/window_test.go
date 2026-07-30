package conversationeval

import (
	"fmt"
	"testing"
	"time"
)

func TestSelectPreWindowKeepsExactlyLastTwenty(t *testing.T) {
	anchor := time.Date(2026, 7, 29, 10, 0, 0, 0, time.UTC)
	messages := make([]WindowMessage, 25)
	for index := range messages {
		messages[index] = windowTestMessage(index, anchor.Add(time.Duration(index-25)*time.Minute))
	}
	// Exercise stable sorting rather than relying on input order.
	messages[0], messages[24] = messages[24], messages[0]

	got, err := SelectPreWindow(messages, anchor)
	if err != nil {
		t.Fatalf("SelectPreWindow() error = %v", err)
	}
	if len(got) != PreWindowMessageLimit {
		t.Fatalf("len(pre-window) = %d, want %d", len(got), PreWindowMessageLimit)
	}
	if got[0].MessageID != "message-05" || got[19].MessageID != "message-24" {
		t.Fatalf("pre-window range = %s..%s, want message-05..message-24",
			got[0].MessageID, got[19].MessageID)
	}
	for index, message := range got {
		if message.Position != WindowPositionPre || message.Sequence != index {
			t.Fatalf("message[%d] position/sequence = %q/%d", index, message.Position, message.Sequence)
		}
	}
}

func TestPostWindowCloseBoundaries(t *testing.T) {
	anchor := time.Date(2026, 7, 29, 10, 0, 0, 0, time.UTC)
	tests := []struct {
		name       string
		run        func(t *testing.T, window *PostWindow)
		wantCount  int
		wantReason PostWindowCloseReason
		wantEnd    time.Time
	}{
		{
			name: "topic boundary excludes boundary message",
			run: func(t *testing.T, window *PostWindow) {
				appendWindowTestMessage(t, window, windowTestMessage(1, anchor.Add(time.Minute)), false)
				added, err := window.Append(windowTestMessage(2, anchor.Add(2*time.Minute)), true)
				if err != nil || added {
					t.Fatalf("Append(boundary) = %v, %v; want false, nil", added, err)
				}
			},
			wantCount: 1, wantReason: PostWindowCloseTopicBoundary,
			wantEnd: anchor.Add(2 * time.Minute),
		},
		{
			name: "fifteen minute deadline excludes late message",
			run: func(t *testing.T, window *PostWindow) {
				appendWindowTestMessage(t, window, windowTestMessage(1, anchor.Add(14*time.Minute)), false)
				added, err := window.Append(windowTestMessage(2, anchor.Add(PostWindowMaxAge)), false)
				if err != nil || added {
					t.Fatalf("Append(deadline) = %v, %v; want false, nil", added, err)
				}
			},
			wantCount: 1, wantReason: PostWindowCloseTimeLimit,
			wantEnd: anchor.Add(PostWindowMaxAge),
		},
		{
			name: "fifty message limit includes final message",
			run: func(t *testing.T, window *PostWindow) {
				for index := 0; index < PostWindowMessageLimit; index++ {
					appendWindowTestMessage(t, window,
						windowTestMessage(index, anchor.Add(time.Duration(index+1)*time.Second)), false)
				}
			},
			wantCount: PostWindowMessageLimit, wantReason: PostWindowCloseMessageLimit,
			wantEnd: anchor.Add(PostWindowMessageLimit * time.Second),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			window, err := NewPostWindow(anchor, "topic-1")
			if err != nil {
				t.Fatalf("NewPostWindow() error = %v", err)
			}
			test.run(t, window)
			if len(window.Messages) != test.wantCount || window.CloseReason != test.wantReason ||
				window.ClosedAt == nil || !window.ClosedAt.Equal(test.wantEnd) {
				t.Fatalf("window = count:%d reason:%q end:%v, want count:%d reason:%q end:%v",
					len(window.Messages), window.CloseReason, window.ClosedAt,
					test.wantCount, test.wantReason, test.wantEnd)
			}
		})
	}
}

func TestPostWindowAdvanceClosesAtDeadline(t *testing.T) {
	anchor := time.Date(2026, 7, 29, 10, 0, 0, 0, time.UTC)
	window, _ := NewPostWindow(anchor, "")
	closed, err := window.Advance(anchor.Add(PostWindowMaxAge))
	if err != nil || !closed || window.ClosedAt == nil ||
		!window.ClosedAt.Equal(anchor.Add(PostWindowMaxAge)) {
		t.Fatalf("Advance() = %v, %v, end %v", closed, err, window.ClosedAt)
	}
}

func TestFeedbackWindowAndResultVersion(t *testing.T) {
	anchor := time.Date(2026, 7, 29, 10, 0, 0, 0, time.UTC)
	postEnd := anchor.Add(10 * time.Minute)
	episode := Episode{
		AnchorAt: anchor, PostWindowEnd: &postEnd,
		LateFeedbackUntil: anchor.Add(LateFeedbackGracePeriod),
	}
	tests := []struct {
		name      string
		kind      FeedbackExplicitness
		at        time.Time
		finalized bool
		want      FeedbackWindowDecision
		version   int64
	}{
		{
			name: "explicit feedback at 24 hour boundary",
			kind: FeedbackExplicit, at: anchor.Add(LateFeedbackGracePeriod),
			want:    FeedbackWindowDecision{Attach: true, Reason: "explicit_feedback"},
			version: 7,
		},
		{
			name: "ordinary feedback after post window rejected",
			kind: FeedbackInferred, at: anchor.Add(time.Hour),
			want:    FeedbackWindowDecision{Reason: "outside_post_window"},
			version: 7,
		},
		{
			name: "feedback after 24 hours rejected",
			kind: FeedbackExplicit, at: anchor.Add(LateFeedbackGracePeriod + time.Nanosecond),
			want:    FeedbackWindowDecision{Reason: "late_feedback_expired"},
			version: 7,
		},
		{
			name: "accepted late feedback versions finalized aggregate",
			kind: FeedbackExplicit, at: anchor.Add(23 * time.Hour), finalized: true,
			want: FeedbackWindowDecision{
				Attach: true, IncrementResultVersion: true, Reason: "explicit_feedback",
			},
			version: 8,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := DecideFeedbackWindow(episode, test.kind, test.at, test.finalized)
			if got != test.want {
				t.Fatalf("DecideFeedbackWindow() = %#v, want %#v", got, test.want)
			}
			if version := NextAggregateResultVersion(7, got); version != test.version {
				t.Fatalf("NextAggregateResultVersion() = %d, want %d", version, test.version)
			}
		})
	}
}

func windowTestMessage(index int, occurredAt time.Time) WindowMessage {
	return WindowMessage{
		EventID: fmt.Sprintf("event-%02d", index), MessageID: fmt.Sprintf("message-%02d", index),
		ChatID: "chat-1", Content: fmt.Sprintf("content-%02d", index), OccurredAt: occurredAt,
	}
}

func appendWindowTestMessage(t *testing.T, window *PostWindow, message WindowMessage, boundary bool) {
	t.Helper()
	added, err := window.Append(message, boundary)
	if err != nil || !added {
		t.Fatalf("Append() = %v, %v; want true, nil", added, err)
	}
}
