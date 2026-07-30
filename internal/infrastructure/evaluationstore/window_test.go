package evaluationstore

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/conversationeval"
)

func TestWindowPersistenceClosesAtFiftyAndMarksReady(t *testing.T) {
	fixture := newRepositoryFixture(t)
	ctx := context.Background()
	cohort := fixture.cohort("window", fixture.now.Add(-time.Hour), fixture.now.Add(time.Hour))
	if err := fixture.repo.CreateCohort(ctx, cohort); err != nil {
		t.Fatalf("CreateCohort() error = %v", err)
	}
	episode, err := fixture.repo.GetOrCreateEpisode(
		ctx,
		fixture.episode(cohort.ID, "episode_window_"+fixture.suffix, "anchor_window"),
	)
	if err != nil {
		t.Fatalf("GetOrCreateEpisode() error = %v", err)
	}
	initial := make([]conversationeval.WindowMessage, 0, conversationeval.PreWindowMessageLimit+1)
	for index := 0; index < conversationeval.PreWindowMessageLimit; index++ {
		initial = append(initial, fixture.windowMessage(
			fmt.Sprintf("pre-%02d", index),
			episode.AnchorAt.Add(time.Duration(index-conversationeval.PreWindowMessageLimit)*time.Minute),
			conversationeval.WindowPositionPre,
			index,
		))
	}
	initial = append(initial, conversationeval.WindowMessage{
		EventID: episode.AnchorEventID, MessageID: episode.AnchorMessageID,
		ChatID: episode.ChatID, TopicID: episode.TopicID, Content: "anchor",
		OccurredAt: episode.AnchorAt, Position: conversationeval.WindowPositionAnchor,
	})
	if err := fixture.repo.SaveWindowMessages(ctx, episode.ID, initial); err != nil {
		t.Fatalf("SaveWindowMessages() error = %v", err)
	}
	if err := fixture.repo.SaveWindowMessages(ctx, episode.ID, initial); err != nil {
		t.Fatalf("SaveWindowMessages(replay) error = %v", err)
	}
	if err := fixture.repo.UpsertLaneOutput(
		ctx,
		fixture.laneOutput(*episode, conversationeval.LaneControl, "window_control"),
	); err != nil {
		t.Fatalf("UpsertLaneOutput(control) error = %v", err)
	}
	if err := fixture.repo.UpsertLaneOutput(
		ctx,
		fixture.laneOutput(*episode, conversationeval.LaneCandidate, "window_candidate"),
	); err != nil {
		t.Fatalf("UpsertLaneOutput(candidate) error = %v", err)
	}

	for index := 0; index < conversationeval.PostWindowMessageLimit; index++ {
		mutation, applyErr := fixture.repo.ApplyPostWindowObservation(
			ctx,
			episode.ID,
			fixture.windowMessage(
				fmt.Sprintf("post-%02d", index),
				episode.AnchorAt.Add(time.Duration(index+1)*time.Second),
				conversationeval.WindowPositionPost,
				index,
			),
			false,
		)
		if applyErr != nil {
			t.Fatalf("ApplyPostWindowObservation(%d) error = %v", index, applyErr)
		}
		wantClosed := index == conversationeval.PostWindowMessageLimit-1
		if mutation.Closed != wantClosed {
			t.Fatalf("mutation[%d].Closed = %v, want %v", index, mutation.Closed, wantClosed)
		}
		if wantClosed && (!mutation.Ready ||
			mutation.CloseReason != conversationeval.PostWindowCloseMessageLimit) {
			t.Fatalf("final mutation = %#v", mutation)
		}
	}
	var stored struct {
		Status           string
		PostWindowReason string
		PostWindowEnd    time.Time
	}
	if err := fixture.db.Table("evaluation_episodes").
		Select("status, post_window_reason, post_window_end").
		Where("id = ?", episode.ID).
		Scan(&stored).Error; err != nil {
		t.Fatalf("load stored episode: %v", err)
	}
	if stored.Status != string(conversationeval.EpisodeStatusReadyForJudge) ||
		stored.PostWindowReason != string(conversationeval.PostWindowCloseMessageLimit) {
		t.Fatalf("stored episode state = %#v", stored)
	}
	var count int64
	if err := fixture.db.Table("evaluation_episode_messages").
		Where("episode_id = ?", episode.ID).
		Count(&count).Error; err != nil {
		t.Fatalf("count window messages: %v", err)
	}
	if count != conversationeval.PreWindowMessageLimit+1+conversationeval.PostWindowMessageLimit {
		t.Fatalf("window message count = %d", count)
	}
}

func TestTopicBoundaryAndDeadlineCloseWithoutAppendingBoundary(t *testing.T) {
	for _, test := range []struct {
		name       string
		boundary   bool
		occurredAt func(time.Time) time.Time
		reason     conversationeval.PostWindowCloseReason
	}{
		{
			name: "topic boundary", boundary: true,
			occurredAt: func(anchor time.Time) time.Time { return anchor.Add(time.Minute) },
			reason:     conversationeval.PostWindowCloseTopicBoundary,
		},
		{
			name: "deadline", boundary: false,
			occurredAt: func(anchor time.Time) time.Time {
				return anchor.Add(conversationeval.PostWindowMaxAge)
			},
			reason: conversationeval.PostWindowCloseTimeLimit,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newRepositoryFixture(t)
			ctx := context.Background()
			cohort := fixture.cohort("boundary", fixture.now.Add(-time.Hour), fixture.now.Add(time.Hour))
			if err := fixture.repo.CreateCohort(ctx, cohort); err != nil {
				t.Fatalf("CreateCohort() error = %v", err)
			}
			episode, err := fixture.repo.GetOrCreateEpisode(
				ctx,
				fixture.episode(cohort.ID, "episode_boundary_"+fixture.suffix, "anchor_boundary"),
			)
			if err != nil {
				t.Fatalf("GetOrCreateEpisode() error = %v", err)
			}
			mutation, err := fixture.repo.ApplyPostWindowObservation(
				ctx,
				episode.ID,
				fixture.windowMessage(
					"boundary",
					test.occurredAt(episode.AnchorAt),
					conversationeval.WindowPositionPost,
					0,
				),
				test.boundary,
			)
			if err != nil {
				t.Fatalf("ApplyPostWindowObservation() error = %v", err)
			}
			if mutation.Added || !mutation.Closed || mutation.CloseReason != test.reason {
				t.Fatalf("mutation = %#v", mutation)
			}
			var count int64
			if err := fixture.db.Table("evaluation_episode_messages").
				Where("episode_id = ? AND position = ?", episode.ID, conversationeval.WindowPositionPost).
				Count(&count).Error; err != nil {
				t.Fatalf("count post messages: %v", err)
			}
			if count != 0 {
				t.Fatalf("post message count = %d, want 0", count)
			}
		})
	}
}

func (f *repositoryFixture) windowMessage(
	id string,
	occurredAt time.Time,
	position conversationeval.WindowPosition,
	sequence int,
) conversationeval.WindowMessage {
	return conversationeval.WindowMessage{
		EventID:   "event_" + id + "_" + f.suffix,
		MessageID: "message_" + id + "_" + f.suffix,
		ChatID:    "chat_" + f.suffix, TopicID: "topic_" + f.suffix,
		Content: id, OccurredAt: occurredAt, Position: position, Sequence: sequence,
	}
}
