package webui

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/testsupport/pgtest"
)

func TestEvaluationWorkbenchRejectsUnboundedOrUnscopedQueries(t *testing.T) {
	store := newEvaluationWorkbenchStore(nil)
	now := time.Now().UTC()
	tests := []EvaluationListQuery{
		{BotOpenID: "bot-1", From: now.Add(-time.Hour), To: now, Limit: 10},
		{AppID: "app-1", From: now.Add(-time.Hour), To: now, Limit: 10},
		{AppID: "app-1", BotOpenID: "bot-1", Limit: 10},
		{
			AppID: "app-1", BotOpenID: "bot-1",
			From: now.Add(-time.Hour), To: now, Limit: 101,
		},
	}
	for _, query := range tests {
		if _, err := store.ListEpisodes(
			context.Background(),
			query,
		); !errors.Is(err, ErrInvalidEvaluationQuery) {
			t.Fatalf("ListEpisodes(%#v) error = %v", query, err)
		}
	}
}

func TestEvaluationWorkbenchCursorRoundTrip(t *testing.T) {
	anchor := time.Date(2026, 7, 30, 12, 0, 0, 123000000, time.UTC)
	cursor := EvaluationCursor{AnchorAt: anchor, EpisodeID: "episode-1"}
	encoded := cursor.Encode()
	decoded, err := DecodeEvaluationCursor(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if !decoded.AnchorAt.Equal(anchor) || decoded.EpisodeID != cursor.EpisodeID {
		t.Fatalf("cursor = %#v", decoded)
	}
	if _, err := DecodeEvaluationCursor("not-a-cursor"); err == nil {
		t.Fatal("invalid cursor was accepted")
	}
}

func TestEvaluationWorkbenchPostgresBundleAndHumanVersioning(t *testing.T) {
	db := pgtest.OpenTempSchema(t)
	for _, table := range []string{
		"evaluation_cohorts",
		"evaluation_episodes",
		"evaluation_episode_messages",
		"evaluation_lane_outputs",
		"evaluation_feedback",
		"evaluation_judgments",
	} {
		if err := db.Exec(
			"CREATE TABLE " + table +
				" (LIKE betago." + table + " INCLUDING ALL)",
		).Error; err != nil {
			t.Fatalf("create %s: %v", table, err)
		}
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	if err := db.Exec(`
		INSERT INTO evaluation_cohorts (
			id, app_id, bot_open_id, chat_ids, start_at, end_at, status,
			serving_lane, control_version, candidate_version,
			judge_config_json, sampling_policy_json, result_version
		) VALUES
		('cohort-1','app-1','bot-1','["chat-1"]'::jsonb,?,?, 'collecting',
		 'control','control-v1','candidate-v1','{}'::jsonb,'{}'::jsonb,0),
		('cohort-foreign','app-2','bot-2','["chat-1"]'::jsonb,?,?, 'collecting',
		 'control','control-v1','candidate-v1','{}'::jsonb,'{}'::jsonb,0)`,
		now.Add(-time.Hour), now.Add(time.Hour),
		now.Add(-time.Hour), now.Add(time.Hour),
	).Error; err != nil {
		t.Fatal(err)
	}
	insertEpisode := func(id, cohort string, anchor time.Time) {
		t.Helper()
		if err := db.Exec(`
			INSERT INTO evaluation_episodes (
				id, cohort_id, chat_id, run_id, anchor_event_id,
				anchor_message_id, topic_id, serving_lane, status,
				pre_window_start, anchor_at, post_window_end,
				late_feedback_until, post_window_reason
			) VALUES (?,?,'chat-1','run-1',?,?,'topic-1','control',
				'judged',?,?,?,?,'topic_boundary')`,
			id, cohort, "event-"+id, "message-"+id,
			anchor.Add(-time.Minute), anchor, anchor.Add(time.Minute),
			anchor.Add(24*time.Hour),
		).Error; err != nil {
			t.Fatal(err)
		}
	}
	insertEpisode("episode-1", "cohort-1", now.Add(-time.Minute))
	insertEpisode("episode-2", "cohort-1", now)
	insertEpisode("episode-foreign", "cohort-foreign", now)
	if err := db.Exec(`
		INSERT INTO evaluation_episode_messages (
			id, episode_id, position, event_id, message_id, sequence,
			occurred_at, payload_json
		) VALUES
		('window-1','episode-1','pre','event-pre','message-pre',0,?,
		 '{"content":"before"}'::jsonb),
		('window-2','episode-1','post','event-post','message-post',1,?,
		 '{"content":"after"}'::jsonb)`,
		now.Add(-2*time.Minute), now,
	).Error; err != nil {
		t.Fatal(err)
	}
	for _, lane := range []string{"control", "candidate"} {
		if err := db.Exec(`
			INSERT INTO evaluation_lane_outputs (
				id, episode_id, lane, output_mode, activation_json,
				relevance_json, join_decision, topic_relation,
				context_snapshot_json, excluded_context_json, tool_plan_json,
				reply_text, latency_ms, token_usage_json, error_json
			) VALUES (?, 'episode-1', ?, ?, '{"active":true}'::jsonb,
				'{"score":0.9}'::jsonb, ?, 'related',
				'{"messages":[{"content":"context"}]}'::jsonb, '[]'::jsonb,
				'{"tools":[]}'::jsonb, ?, 100, '{"total":10}'::jsonb,
				'{}'::jsonb)`,
			"output-"+lane, lane,
			map[bool]string{true: "actual", false: "shadow"}[lane == "control"],
			map[bool]string{true: "join", false: "skip"}[lane == "control"],
			lane+" reply",
		).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Exec(`
		INSERT INTO evaluation_feedback (
			id, episode_id, target_lane, target_message_id,
			feedback_event_id, feedback_type, explicitness, content_json,
			attribution_confidence, occurred_at
		) VALUES ('feedback-1','episode-1','control','delivered-1',
			'feedback-event','direct_reply','explicit',
			'{"text":"good"}'::jsonb,95,?)`, now,
	).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`
		INSERT INTO evaluation_judgments (
			id, episode_id, version, source, evaluator_id, winner,
			scores_json, problem_tags_json, rationale, confidence, needs_review,
			supersedes_id
		) VALUES ('judge-1','episode-1',1,'conversation_evaluation_judge',
			'judge-model','candidate','{"candidate":9}'::jsonb,'[]'::jsonb,
			'candidate is better',80,true,'')`,
	).Error; err != nil {
		t.Fatal(err)
	}

	store := newEvaluationWorkbenchStore(db)
	page, err := store.ListEpisodes(context.Background(), EvaluationListQuery{
		AppID: "app-1", BotOpenID: "bot-1",
		From: now.Add(-time.Hour), To: now.Add(time.Hour), Limit: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].ID != "episode-2" ||
		page.NextCursor == "" {
		t.Fatalf("page = %#v", page)
	}
	cursor, err := DecodeEvaluationCursor(page.NextCursor)
	if err != nil {
		t.Fatal(err)
	}
	secondPage, err := store.ListEpisodes(
		context.Background(),
		EvaluationListQuery{
			AppID: "app-1", BotOpenID: "bot-1",
			From: now.Add(-time.Hour), To: now.Add(time.Hour), Limit: 10,
			CursorAnchorAt: cursor.AnchorAt, CursorID: cursor.EpisodeID,
		},
	)
	if err != nil || len(secondPage.Items) != 1 ||
		secondPage.Items[0].ID != "episode-1" {
		t.Fatalf("second page = %#v, %v", secondPage, err)
	}
	detail, err := store.GetEpisode(
		context.Background(),
		"app-1",
		"bot-1",
		"episode-1",
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.Messages) != 2 || len(detail.Outputs) != 2 ||
		len(detail.Feedback) != 1 || len(detail.Judgments) != 1 ||
		string(detail.Messages[0].PayloadJSON) == "" {
		t.Fatalf("detail = %#v", detail)
	}
	if _, err := store.GetEpisode(
		context.Background(),
		"app-1",
		"bot-1",
		"episode-foreign",
	); !errors.Is(err, ErrEvaluationNotFound) {
		t.Fatalf("foreign detail error = %v", err)
	}
	request := HumanJudgmentRequest{
		EvaluatorID: "reviewer-1", Winner: "candidate",
		ScoresJSON:  json.RawMessage(`{"candidate":10}`),
		ProblemTags: []string{"control_missed_context"},
		Rationale:   "Candidate included the constraint.", Confidence: 95,
	}
	first, err := store.AppendHumanJudgment(
		context.Background(), "app-1", "bot-1", "episode-1", request,
	)
	if err != nil {
		t.Fatal(err)
	}
	request.Winner = "tie"
	second, err := store.AppendHumanJudgment(
		context.Background(), "app-1", "bot-1", "episode-1", request,
	)
	if err != nil {
		t.Fatal(err)
	}
	if first.Version != 1 || second.Version != 2 ||
		second.SupersedesID != first.ID {
		t.Fatalf("human versions = %#v %#v", first, second)
	}

	var wg sync.WaitGroup
	results := make(chan *EvaluationJudgmentView, 2)
	errs := make(chan error, 2)
	for index := range 2 {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			concurrent := request
			concurrent.EvaluatorID = "concurrent-reviewer-" + string(rune('a'+index))
			result, appendErr := store.AppendHumanJudgment(
				context.Background(),
				"app-1",
				"bot-1",
				"episode-2",
				concurrent,
			)
			results <- result
			errs <- appendErr
		}(index)
	}
	wg.Wait()
	close(results)
	close(errs)
	for appendErr := range errs {
		if appendErr != nil {
			t.Fatalf("concurrent append: %v", appendErr)
		}
	}
	versions := make([]int64, 0, 2)
	for result := range results {
		versions = append(versions, result.Version)
	}
	sort.Slice(versions, func(i, j int) bool { return versions[i] < versions[j] })
	if len(versions) != 2 || versions[0] != 1 || versions[1] != 2 {
		t.Fatalf("concurrent versions = %#v", versions)
	}
}
