package evaluationstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/conversationeval"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/config"
	uuid "github.com/satori/go.uuid"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

type repositoryFixture struct {
	db     *gorm.DB
	repo   *Repository
	suffix string
	now    time.Time
}

func newRepositoryFixture(t *testing.T) *repositoryFixture {
	t.Helper()
	configPath := os.Getenv("BETAGO_CONFIG_PATH")
	if configPath == "" {
		t.Skip("BETAGO_CONFIG_PATH is not set; skipping PostgreSQL integration test")
	}
	cfg, err := config.LoadFileE(configPath)
	if err != nil || cfg == nil || cfg.DBConfig == nil {
		t.Skip("PostgreSQL test configuration is unavailable")
	}
	rootDB, err := gorm.Open(postgres.Open(cfg.DBConfig.DSN()), &gorm.Config{})
	if err != nil {
		t.Skip("PostgreSQL is unavailable")
	}
	rootSQLDB, err := rootDB.DB()
	if err != nil || rootSQLDB.PingContext(context.Background()) != nil {
		t.Skip("PostgreSQL is unavailable")
	}
	var exists bool
	if err := rootDB.Raw(`SELECT to_regclass('betago.evaluation_cohorts') IS NOT NULL`).Scan(&exists).Error; err != nil || !exists {
		_ = rootSQLDB.Close()
		t.Skip("conversation evaluation migration is not installed")
	}

	suffix := strings.ReplaceAll(uuid.NewV4().String(), "-", "")
	schema := "evaluationstore_test_" + suffix
	if err := rootDB.Exec(fmt.Sprintf(`CREATE SCHEMA %q`, schema)).Error; err != nil {
		_ = rootSQLDB.Close()
		t.Fatalf("create isolated schema: %v", err)
	}
	ddl := []string{
		fmt.Sprintf(`CREATE TABLE %q.evaluation_cohorts (LIKE betago.evaluation_cohorts INCLUDING ALL)`, schema),
		fmt.Sprintf(`CREATE TABLE %q.evaluation_episodes (LIKE betago.evaluation_episodes INCLUDING ALL)`, schema),
		fmt.Sprintf(`ALTER TABLE %q.evaluation_episodes ADD FOREIGN KEY (cohort_id) REFERENCES %q.evaluation_cohorts(id) ON DELETE CASCADE`, schema, schema),
		fmt.Sprintf(`CREATE TABLE %q.evaluation_lane_outputs (LIKE betago.evaluation_lane_outputs INCLUDING ALL)`, schema),
		fmt.Sprintf(`ALTER TABLE %q.evaluation_lane_outputs ADD FOREIGN KEY (episode_id) REFERENCES %q.evaluation_episodes(id) ON DELETE CASCADE`, schema, schema),
		fmt.Sprintf(`CREATE TABLE %q.evaluation_feedback (LIKE betago.evaluation_feedback INCLUDING ALL)`, schema),
		fmt.Sprintf(`ALTER TABLE %q.evaluation_feedback ADD FOREIGN KEY (episode_id) REFERENCES %q.evaluation_episodes(id) ON DELETE CASCADE`, schema, schema),
		fmt.Sprintf(`CREATE TABLE %q.evaluation_judgments (LIKE betago.evaluation_judgments INCLUDING ALL)`, schema),
		fmt.Sprintf(`ALTER TABLE %q.evaluation_judgments ADD FOREIGN KEY (episode_id) REFERENCES %q.evaluation_episodes(id) ON DELETE CASCADE`, schema, schema),
	}
	for _, statement := range ddl {
		if err := rootDB.Exec(statement).Error; err != nil {
			_ = rootDB.Exec(fmt.Sprintf(`DROP SCHEMA %q CASCADE`, schema)).Error
			_ = rootSQLDB.Close()
			t.Fatalf("initialize isolated schema: %v", err)
		}
	}

	testConfig := *cfg.DBConfig
	testConfig.SearchPath = schema
	testDB, err := gorm.Open(postgres.Open(testConfig.DSN()), &gorm.Config{})
	if err != nil {
		_ = rootDB.Exec(fmt.Sprintf(`DROP SCHEMA %q CASCADE`, schema)).Error
		_ = rootSQLDB.Close()
		t.Fatalf("open isolated schema: %v", err)
	}
	testSQLDB, err := testDB.DB()
	if err != nil {
		t.Fatalf("get isolated database handle: %v", err)
	}
	t.Cleanup(func() {
		_ = testSQLDB.Close()
		if err := rootDB.Exec(fmt.Sprintf(`DROP SCHEMA %q CASCADE`, schema)).Error; err != nil {
			t.Errorf("drop isolated schema: %v", err)
		}
		_ = rootSQLDB.Close()
	})
	return &repositoryFixture{
		db: testDB, repo: NewRepository(testDB), suffix: suffix,
		now: time.Now().UTC().Truncate(time.Microsecond),
	}
}

func TestCohortQueriesAndLifecycleAreTimeBoundAndIrreversible(t *testing.T) {
	fixture := newRepositoryFixture(t)
	ctx := context.Background()

	active := fixture.cohort("active", fixture.now.Add(-time.Hour), fixture.now.Add(time.Hour))
	if err := fixture.repo.CreateCohort(ctx, active); err != nil {
		t.Fatalf("CreateCohort(active) error = %v", err)
	}
	invalidInitialState := fixture.cohort(
		"invalid_initial_state",
		fixture.now.Add(-time.Hour),
		fixture.now.Add(time.Hour),
	)
	invalidInitialState.Status = conversationeval.CohortStatusFinalized
	if err := fixture.repo.CreateCohort(ctx, invalidInitialState); err == nil {
		t.Fatal("CreateCohort(finalized) error = nil, want strict lifecycle rejection")
	}
	expired := fixture.cohort(
		"expired",
		fixture.now.Add(-48*time.Hour),
		fixture.now.Add(-25*time.Hour),
	)
	if err := fixture.repo.CreateCohort(ctx, expired); err != nil {
		t.Fatalf("CreateCohort(expired) error = %v", err)
	}

	cohorts, err := fixture.repo.ActiveCohorts(ctx, active.ChatIDs[0], fixture.now)
	if err != nil {
		t.Fatalf("ActiveCohorts() error = %v", err)
	}
	if len(cohorts) != 1 || cohorts[0].ID != active.ID {
		t.Fatalf("ActiveCohorts() = %#v, want only %q", cohorts, active.ID)
	}

	transitioned, err := fixture.repo.TransitionCohorts(ctx, fixture.now)
	if err != nil {
		t.Fatalf("first TransitionCohorts() error = %v", err)
	}
	if transitioned != 2 {
		t.Fatalf("first TransitionCohorts() = %d, want two legal state transitions", transitioned)
	}
	assertCohortStatus(t, fixture.db, expired.ID, conversationeval.CohortStatusFinalized)

	transitioned, err = fixture.repo.TransitionCohorts(ctx, fixture.now)
	if err != nil {
		t.Fatalf("restart TransitionCohorts() error = %v", err)
	}
	if transitioned != 0 {
		t.Fatalf("restart TransitionCohorts() = %d, want idempotent no-op", transitioned)
	}
	assertCohortStatus(t, fixture.db, expired.ID, conversationeval.CohortStatusFinalized)
}

func TestEpisodeAndLaneOutputUseTargetedConcurrentUpserts(t *testing.T) {
	fixture := newRepositoryFixture(t)
	ctx := context.Background()
	cohort := fixture.cohort("episodes", fixture.now.Add(-time.Hour), fixture.now.Add(time.Hour))
	if err := fixture.repo.CreateCohort(ctx, cohort); err != nil {
		t.Fatalf("CreateCohort() error = %v", err)
	}

	const workers = 8
	results := make(chan *conversationeval.Episode, workers)
	errs := make(chan error, workers)
	var group sync.WaitGroup
	for index := range workers {
		group.Add(1)
		go func() {
			defer group.Done()
			episode := fixture.episode(cohort.ID, fmt.Sprintf("episode_%d_%s", index, fixture.suffix), "anchor_shared")
			stored, err := fixture.repo.GetOrCreateEpisode(ctx, episode)
			if err != nil {
				errs <- err
				return
			}
			results <- stored
		}()
	}
	group.Wait()
	close(results)
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent GetOrCreateEpisode() error = %v", err)
	}
	var episodeID string
	var storedEpisode conversationeval.Episode
	for stored := range results {
		if episodeID == "" {
			episodeID = stored.ID
			storedEpisode = *stored
		}
		if stored.ID != episodeID {
			t.Fatalf("concurrent stored episode ID = %q, want %q", stored.ID, episodeID)
		}
	}
	var episodeCount int64
	if err := fixture.db.Table("evaluation_episodes").
		Where("cohort_id = ? AND anchor_event_id = ?", cohort.ID, "anchor_shared").
		Count(&episodeCount).Error; err != nil {
		t.Fatalf("count episodes: %v", err)
	}
	if episodeCount != 1 {
		t.Fatalf("episode count = %d, want 1", episodeCount)
	}

	pkCollision := fixture.episode(cohort.ID, episodeID, "different_anchor")
	if _, err := fixture.repo.GetOrCreateEpisode(ctx, pkCollision); err == nil {
		t.Fatal("GetOrCreateEpisode(primary-key collision) error = nil, want non-target conflict")
	}

	first := fixture.laneOutput(storedEpisode, conversationeval.LaneControl, "lane_control_first")
	if err := fixture.repo.UpsertLaneOutput(ctx, first); err != nil {
		t.Fatalf("first UpsertLaneOutput() error = %v", err)
	}
	var firstCreatedAt time.Time
	if err := fixture.db.Table("evaluation_lane_outputs").
		Select("created_at").
		Where("episode_id = ? AND lane = ?", episodeID, conversationeval.LaneControl).
		Scan(&firstCreatedAt).Error; err != nil {
		t.Fatalf("read first lane created_at: %v", err)
	}
	second := fixture.laneOutput(storedEpisode, conversationeval.LaneControl, "lane_control_second")
	second.ReplyText = "updated reply"
	if err := fixture.repo.UpsertLaneOutput(ctx, second); err != nil {
		t.Fatalf("second UpsertLaneOutput() error = %v", err)
	}
	var laneCount int64
	if err := fixture.db.Table("evaluation_lane_outputs").
		Where("episode_id = ? AND lane = ?", episodeID, conversationeval.LaneControl).
		Count(&laneCount).Error; err != nil {
		t.Fatalf("count lane outputs: %v", err)
	}
	var storedLane struct {
		ID        string
		ReplyText string
		CreatedAt time.Time
	}
	if err := fixture.db.Table("evaluation_lane_outputs").
		Select("id, reply_text, created_at").
		Where("episode_id = ? AND lane = ?", episodeID, conversationeval.LaneControl).
		Scan(&storedLane).Error; err != nil {
		t.Fatalf("read lane output: %v", err)
	}
	if laneCount != 1 || storedLane.ID != first.ID || storedLane.ReplyText != second.ReplyText ||
		!storedLane.CreatedAt.Equal(firstCreatedAt) {
		t.Fatalf(
			"stored lane = count:%d row:%#v, want first ID/created_at and updated reply %q/%s/%q",
			laneCount, storedLane, first.ID, firstCreatedAt, second.ReplyText,
		)
	}

	nonTargetCollision := fixture.laneOutput(storedEpisode, conversationeval.LaneCandidate, first.ID)
	nonTargetCollision.ID = first.ID
	if err := fixture.repo.UpsertLaneOutput(ctx, nonTargetCollision); err == nil {
		t.Fatal("UpsertLaneOutput(primary-key collision) error = nil, want non-target conflict")
	}
}

func TestGetOrCreateEpisodeValidatesCohortOwnership(t *testing.T) {
	fixture := newRepositoryFixture(t)
	ctx := context.Background()
	cohort := fixture.cohort("ownership", fixture.now.Add(-time.Hour), fixture.now.Add(time.Hour))
	if err := fixture.repo.CreateCohort(ctx, cohort); err != nil {
		t.Fatalf("CreateCohort() error = %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*conversationeval.Episode)
	}{
		{"chat outside cohort", func(episode *conversationeval.Episode) { episode.ChatID = "other_chat" }},
		{"anchor before cohort", func(episode *conversationeval.Episode) { episode.AnchorAt = cohort.StartAt.Add(-time.Microsecond) }},
		{"anchor at exclusive end", func(episode *conversationeval.Episode) { episode.AnchorAt = cohort.EndAt }},
		{"serving lane mismatch", func(episode *conversationeval.Episode) { episode.ServingLane = conversationeval.LaneCandidate }},
	}
	for index, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			episode := fixture.episode(
				cohort.ID,
				fmt.Sprintf("episode_invalid_%d_%s", index, fixture.suffix),
				fmt.Sprintf("anchor_invalid_%d", index),
			)
			tt.mutate(&episode)
			if episode.PreWindowStart.After(episode.AnchorAt) {
				episode.PreWindowStart = episode.AnchorAt.Add(-time.Minute)
			}
			if episode.LateFeedbackUntil.Before(episode.AnchorAt) {
				episode.LateFeedbackUntil = episode.AnchorAt.Add(time.Hour)
			}
			if _, err := fixture.repo.GetOrCreateEpisode(ctx, episode); !errors.Is(err, conversationeval.ErrInvalidContract) {
				t.Fatalf("GetOrCreateEpisode() error = %v, want ErrInvalidContract", err)
			}
		})
	}

	candidateCohort := fixture.cohort(
		"candidate_serving",
		fixture.now.Add(-time.Hour),
		fixture.now.Add(time.Hour),
	)
	candidateCohort.ServingLane = conversationeval.LaneCandidate
	if err := fixture.repo.CreateCohort(ctx, candidateCohort); err != nil {
		t.Fatalf("CreateCohort(candidate-serving) error = %v", err)
	}
	candidateEpisode := fixture.episode(
		candidateCohort.ID,
		"episode_candidate_"+fixture.suffix,
		"anchor_candidate",
	)
	candidateEpisode.ServingLane = conversationeval.LaneCandidate
	if _, err := fixture.repo.GetOrCreateEpisode(ctx, candidateEpisode); err != nil {
		t.Fatalf("GetOrCreateEpisode(candidate-serving) error = %v", err)
	}

	corruptible := fixture.episode(
		cohort.ID,
		"episode_corruptible_"+fixture.suffix,
		"anchor_corruptible",
	)
	storedCorruptible, err := fixture.repo.GetOrCreateEpisode(ctx, corruptible)
	if err != nil {
		t.Fatalf("GetOrCreateEpisode(corruptible) error = %v", err)
	}
	if err := fixture.db.Table("evaluation_episodes").
		Where("id = ?", storedCorruptible.ID).
		Update("status", "invalid_status").Error; err != nil {
		t.Fatalf("corrupt episode fixture: %v", err)
	}
	replay := corruptible
	replay.ID = "episode_corruptible_replay_" + fixture.suffix
	if _, err := fixture.repo.GetOrCreateEpisode(ctx, replay); !errors.Is(err, conversationeval.ErrInvalidContract) {
		t.Fatalf("GetOrCreateEpisode(corrupt stored row) error = %v, want ErrInvalidContract", err)
	}

	crossEntity := fixture.episode(
		cohort.ID,
		"episode_cross_entity_"+fixture.suffix,
		"anchor_cross_entity",
	)
	storedCrossEntity, err := fixture.repo.GetOrCreateEpisode(ctx, crossEntity)
	if err != nil {
		t.Fatalf("GetOrCreateEpisode(cross-entity) error = %v", err)
	}
	if err := fixture.db.Table("evaluation_episodes").
		Where("id = ?", storedCrossEntity.ID).
		Update("chat_id", "chat_outside_cohort").Error; err != nil {
		t.Fatalf("corrupt episode cohort ownership: %v", err)
	}
	crossEntityReplay := crossEntity
	crossEntityReplay.ID = "episode_cross_entity_replay_" + fixture.suffix
	if _, err := fixture.repo.GetOrCreateEpisode(
		ctx,
		crossEntityReplay,
	); !errors.Is(err, conversationeval.ErrInvalidContract) {
		t.Fatalf("GetOrCreateEpisode(cross-entity stored row) error = %v, want ErrInvalidContract", err)
	}
}

func TestUpsertLaneOutputValidatesEpisodeAnchorAndServingMode(t *testing.T) {
	fixture := newRepositoryFixture(t)
	ctx := context.Background()
	cohort := fixture.cohort("lane_contract", fixture.now.Add(-time.Hour), fixture.now.Add(time.Hour))
	if err := fixture.repo.CreateCohort(ctx, cohort); err != nil {
		t.Fatalf("CreateCohort() error = %v", err)
	}
	episode, err := fixture.repo.GetOrCreateEpisode(
		ctx,
		fixture.episode(cohort.ID, "episode_lane_contract_"+fixture.suffix, "anchor_lane_contract"),
	)
	if err != nil {
		t.Fatalf("GetOrCreateEpisode() error = %v", err)
	}
	tests := []struct {
		name   string
		mutate func(*conversationeval.LaneOutput)
	}{
		{"anchor event mismatch", func(output *conversationeval.LaneOutput) {
			output.ContextSnapshot.AnchorEventID = "other_anchor"
		}},
		{"anchor time mismatch", func(output *conversationeval.LaneOutput) {
			output.ContextSnapshot.AnchorAt = output.ContextSnapshot.AnchorAt.Add(time.Microsecond)
		}},
		{"serving lane marked shadow", func(output *conversationeval.LaneOutput) {
			output.OutputMode = conversationeval.OutputModeShadow
		}},
	}
	for index, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			output := fixture.laneOutput(
				*episode,
				conversationeval.LaneControl,
				fmt.Sprintf("invalid_control_%d", index),
			)
			tt.mutate(&output)
			if err := fixture.repo.UpsertLaneOutput(ctx, output); !errors.Is(err, conversationeval.ErrInvalidContract) {
				t.Fatalf("UpsertLaneOutput() error = %v, want ErrInvalidContract", err)
			}
		})
	}
	candidateActual := fixture.laneOutput(*episode, conversationeval.LaneCandidate, "candidate_actual")
	candidateActual.OutputMode = conversationeval.OutputModeActual
	if err := fixture.repo.UpsertLaneOutput(ctx, candidateActual); !errors.Is(err, conversationeval.ErrInvalidContract) {
		t.Fatalf("UpsertLaneOutput(non-serving actual) error = %v, want ErrInvalidContract", err)
	}

	candidateCohort := fixture.cohort(
		"candidate_lane_contract",
		fixture.now.Add(-time.Hour),
		fixture.now.Add(time.Hour),
	)
	candidateCohort.ServingLane = conversationeval.LaneCandidate
	if err := fixture.repo.CreateCohort(ctx, candidateCohort); err != nil {
		t.Fatalf("CreateCohort(candidate-serving) error = %v", err)
	}
	candidateEpisode := fixture.episode(
		candidateCohort.ID,
		"episode_candidate_lane_"+fixture.suffix,
		"anchor_candidate_lane",
	)
	candidateEpisode.ServingLane = conversationeval.LaneCandidate
	storedCandidate, err := fixture.repo.GetOrCreateEpisode(ctx, candidateEpisode)
	if err != nil {
		t.Fatalf("GetOrCreateEpisode(candidate-serving) error = %v", err)
	}
	if err := fixture.repo.UpsertLaneOutput(
		ctx,
		fixture.laneOutput(*storedCandidate, conversationeval.LaneControl, "candidate_control_shadow"),
	); err != nil {
		t.Fatalf("UpsertLaneOutput(control shadow) error = %v", err)
	}
	actual := fixture.laneOutput(*storedCandidate, conversationeval.LaneCandidate, "candidate_serving_actual")
	actual.OutputMode = conversationeval.OutputModeActual
	if err := fixture.repo.UpsertLaneOutput(ctx, actual); err != nil {
		t.Fatalf("UpsertLaneOutput(candidate actual) error = %v", err)
	}
	candidateFeedback := conversationeval.Feedback{
		ID: "feedback_candidate_" + fixture.suffix, EpisodeID: storedCandidate.ID,
		TargetLane: conversationeval.LaneCandidate, TargetMessageID: "candidate_message",
		FeedbackEventID: "candidate_feedback_event", FeedbackType: conversationeval.FeedbackTypeReaction,
		Explicitness: conversationeval.FeedbackExplicit, ContentJSON: json.RawMessage(`{"reaction":"THUMBSUP"}`),
		AttributionConfidence: 100, OccurredAt: fixture.now,
	}
	if err := fixture.repo.AppendFeedback(ctx, candidateFeedback); err != nil {
		t.Fatalf("AppendFeedback(candidate-serving actual) error = %v", err)
	}
}

func TestFeedbackDedupesAndJudgmentsRemainAppendOnlyAndVersioned(t *testing.T) {
	fixture := newRepositoryFixture(t)
	ctx := context.Background()
	cohort := fixture.cohort("append", fixture.now.Add(-time.Hour), fixture.now.Add(time.Hour))
	if err := fixture.repo.CreateCohort(ctx, cohort); err != nil {
		t.Fatalf("CreateCohort() error = %v", err)
	}
	episode := fixture.episode(cohort.ID, "episode_append_"+fixture.suffix, "anchor_append")
	stored, err := fixture.repo.GetOrCreateEpisode(ctx, episode)
	if err != nil {
		t.Fatalf("GetOrCreateEpisode() error = %v", err)
	}

	feedback := conversationeval.Feedback{
		ID: "feedback_1_" + fixture.suffix, EpisodeID: stored.ID,
		TargetLane: conversationeval.LaneControl, TargetMessageID: "message_reply",
		FeedbackEventID: "event_feedback", FeedbackType: conversationeval.FeedbackTypeCorrection,
		Explicitness: conversationeval.FeedbackExplicit, ContentJSON: json.RawMessage(`{"text":"不对"}`),
		AttributionConfidence: 100, OccurredAt: fixture.now,
	}
	if err := fixture.repo.AppendFeedback(ctx, feedback); !errors.Is(err, conversationeval.ErrInvalidContract) {
		t.Fatalf("AppendFeedback(without actual output) error = %v, want ErrInvalidContract", err)
	}
	episodeOnly := feedback
	episodeOnly.ID = "feedback_episode_only_" + fixture.suffix
	episodeOnly.FeedbackEventID = "event_episode_only"
	episodeOnly.TargetLane = ""
	episodeOnly.TargetMessageID = ""
	if err := fixture.repo.AppendFeedback(ctx, episodeOnly); err != nil {
		t.Fatalf("AppendFeedback(episode-only) error = %v", err)
	}
	if err := fixture.repo.UpsertLaneOutput(
		ctx,
		fixture.laneOutput(*stored, conversationeval.LaneControl, "feedback_control_actual"),
	); err != nil {
		t.Fatalf("UpsertLaneOutput(actual) error = %v", err)
	}
	if err := fixture.repo.AppendFeedback(ctx, feedback); err != nil {
		t.Fatalf("first AppendFeedback() error = %v", err)
	}
	duplicate := feedback
	duplicate.ID = "feedback_2_" + fixture.suffix
	duplicate.ContentJSON = json.RawMessage(`{"text":"overwritten"}`)
	if err := fixture.repo.AppendFeedback(ctx, duplicate); err != nil {
		t.Fatalf("duplicate AppendFeedback() error = %v", err)
	}
	var feedbackCount int64
	if err := fixture.db.Table("evaluation_feedback").
		Where("episode_id = ? AND feedback_event_id = ?", stored.ID, feedback.FeedbackEventID).
		Count(&feedbackCount).Error; err != nil {
		t.Fatalf("count feedback: %v", err)
	}
	if feedbackCount != 1 {
		t.Fatalf("feedback count = %d, want 1", feedbackCount)
	}
	var storedFeedback struct {
		ID          string
		ContentText string
	}
	if err := fixture.db.Table("evaluation_feedback").
		Select("id, content_json->>'text' AS content_text").
		Where("episode_id = ? AND feedback_event_id = ?", stored.ID, feedback.FeedbackEventID).
		Scan(&storedFeedback).Error; err != nil {
		t.Fatalf("read first feedback: %v", err)
	}
	if storedFeedback.ID != feedback.ID || storedFeedback.ContentText != "不对" {
		t.Fatalf("stored feedback = %#v, want first ID/content %q/%q", storedFeedback, feedback.ID, "不对")
	}
	nonServing := feedback
	nonServing.ID = "feedback_non_serving_" + fixture.suffix
	nonServing.FeedbackEventID = "event_non_serving"
	nonServing.TargetLane = conversationeval.LaneCandidate
	if err := fixture.repo.AppendFeedback(ctx, nonServing); !errors.Is(err, conversationeval.ErrInvalidContract) {
		t.Fatalf("AppendFeedback(non-serving lane) error = %v, want ErrInvalidContract", err)
	}
	nonTargetFeedback := feedback
	nonTargetFeedback.FeedbackEventID = "event_different"
	if err := fixture.repo.AppendFeedback(ctx, nonTargetFeedback); err == nil {
		t.Fatal("AppendFeedback(primary-key collision) error = nil, want non-target conflict")
	}

	first := conversationeval.Judgment{
		ID: "judgment_1_" + fixture.suffix, EpisodeID: stored.ID, Version: 1,
		Source: conversationeval.JudgmentSourceConversationJudge, EvaluatorID: "judge_v1",
		Winner: conversationeval.JudgmentWinnerControl, ScoresJSON: json.RawMessage(`{"relevance":8}`),
		ProblemTags: []string{"candidate_context"}, Rationale: "control is better", Confidence: 90,
	}
	if err := fixture.repo.AppendJudgment(ctx, first); err != nil {
		t.Fatalf("first AppendJudgment() error = %v", err)
	}
	second := first
	second.ID = "judgment_2_" + fixture.suffix
	second.Version = 2
	second.SupersedesID = first.ID
	second.Winner = conversationeval.JudgmentWinnerCandidate
	if err := fixture.repo.AppendJudgment(ctx, second); err != nil {
		t.Fatalf("second AppendJudgment() error = %v", err)
	}
	orphaned := second
	orphaned.ID = "judgment_orphaned_" + fixture.suffix
	orphaned.Version = 3
	orphaned.SupersedesID = "judgment_from_another_stream"
	if err := fixture.repo.AppendJudgment(ctx, orphaned); err == nil {
		t.Fatal("AppendJudgment(orphaned supersedes_id) error = nil, want version-chain rejection")
	}
	gapped := second
	gapped.ID = "judgment_gapped_" + fixture.suffix
	gapped.Version = 4
	if err := fixture.repo.AppendJudgment(ctx, gapped); err == nil {
		t.Fatal("AppendJudgment(gapped version) error = nil, want version-chain rejection")
	}
	duplicateVersion := first
	duplicateVersion.ID = "judgment_duplicate_" + fixture.suffix
	if err := fixture.repo.AppendJudgment(ctx, duplicateVersion); err == nil {
		t.Fatal("AppendJudgment(duplicate version) error = nil, want append-only conflict")
	}
	var judgmentCount int64
	if err := fixture.db.Table("evaluation_judgments").
		Where("episode_id = ? AND source = ?", stored.ID, first.Source).
		Count(&judgmentCount).Error; err != nil {
		t.Fatalf("count judgments: %v", err)
	}
	if judgmentCount != 2 {
		t.Fatalf("judgment count = %d, want 2 immutable versions", judgmentCount)
	}
}

func TestEpisodesReadyForJudgeRequiresClosedWindowAndBothLanes(t *testing.T) {
	fixture := newRepositoryFixture(t)
	ctx := context.Background()
	cohort := fixture.cohort("ready", fixture.now.Add(-time.Hour), fixture.now.Add(time.Hour))
	if err := fixture.repo.CreateCohort(ctx, cohort); err != nil {
		t.Fatalf("CreateCohort() error = %v", err)
	}
	ready := fixture.episode(cohort.ID, "episode_ready_"+fixture.suffix, "anchor_ready")
	ready.Status = conversationeval.EpisodeStatusReadyForJudge
	postEnd := fixture.now
	ready.PostWindowEnd = &postEnd
	stored, err := fixture.repo.GetOrCreateEpisode(ctx, ready)
	if err != nil {
		t.Fatalf("GetOrCreateEpisode() error = %v", err)
	}
	if err := fixture.repo.UpsertLaneOutput(ctx, fixture.laneOutput(*stored, conversationeval.LaneControl, "ready_control")); err != nil {
		t.Fatalf("UpsertLaneOutput(control) error = %v", err)
	}
	episodes, err := fixture.repo.EpisodesReadyForJudge(ctx, fixture.now, 10)
	if err != nil {
		t.Fatalf("EpisodesReadyForJudge(one lane) error = %v", err)
	}
	if len(episodes) != 0 {
		t.Fatalf("EpisodesReadyForJudge(one lane) = %#v, want empty", episodes)
	}
	if err := fixture.repo.UpsertLaneOutput(ctx, fixture.laneOutput(*stored, conversationeval.LaneCandidate, "ready_candidate")); err != nil {
		t.Fatalf("UpsertLaneOutput(candidate) error = %v", err)
	}
	episodes, err = fixture.repo.EpisodesReadyForJudge(ctx, fixture.now, 10)
	if err != nil {
		t.Fatalf("EpisodesReadyForJudge(two lanes) error = %v", err)
	}
	if len(episodes) != 1 || episodes[0].ID != stored.ID || episodes[0].PostWindowEnd == nil {
		t.Fatalf("EpisodesReadyForJudge(two lanes) = %#v, want %q", episodes, stored.ID)
	}
	if _, err := fixture.repo.EpisodesReadyForJudge(ctx, fixture.now, 0); err == nil {
		t.Fatal("EpisodesReadyForJudge(limit=0) error = nil, want validation error")
	}
	if err := fixture.db.Table("evaluation_episodes").
		Where("id = ?", stored.ID).
		Update("serving_lane", "invalid_lane").Error; err != nil {
		t.Fatalf("corrupt stored episode fixture: %v", err)
	}
	if _, err := fixture.repo.EpisodesReadyForJudge(ctx, fixture.now, 10); !errors.Is(err, conversationeval.ErrInvalidContract) {
		t.Fatalf("EpisodesReadyForJudge(corrupt episode) error = %v, want ErrInvalidContract", err)
	}
}

func (f *repositoryFixture) cohort(name string, startAt, endAt time.Time) conversationeval.Cohort {
	return conversationeval.Cohort{
		ID: "cohort_" + name + "_" + f.suffix, AppID: "app_" + f.suffix,
		BotOpenID: "bot_" + f.suffix, ChatIDs: []string{"chat_" + f.suffix},
		StartAt: startAt, EndAt: endAt, Status: conversationeval.CohortStatusCollecting,
		ServingLane: conversationeval.LaneControl, ControlVersion: "control-v1",
		CandidateVersion: "candidate-v1", JudgeConfigJSON: json.RawMessage(`{}`),
		SamplingPolicyJSON: json.RawMessage(`{"sample_rate":1}`),
	}
}

func (f *repositoryFixture) episode(cohortID, id, anchorEventID string) conversationeval.Episode {
	return conversationeval.Episode{
		ID: id, CohortID: cohortID, ChatID: "chat_" + f.suffix, RunID: "run_" + f.suffix,
		AnchorEventID: anchorEventID, AnchorMessageID: "message_" + anchorEventID,
		TopicID: "topic_" + f.suffix, ServingLane: conversationeval.LaneControl,
		Status:         conversationeval.EpisodeStatusCollecting,
		PreWindowStart: f.now.Add(-20 * time.Minute), AnchorAt: f.now,
		LateFeedbackUntil: f.now.Add(conversationeval.LateFeedbackGracePeriod),
	}
}

func (f *repositoryFixture) laneOutput(
	episode conversationeval.Episode,
	lane conversationeval.Lane,
	id string,
) conversationeval.LaneOutput {
	mode := conversationeval.OutputModeShadow
	if lane == episode.ServingLane {
		mode = conversationeval.OutputModeActual
	}
	return conversationeval.LaneOutput{
		ID: id + "_" + f.suffix, EpisodeID: episode.ID, Lane: lane, OutputMode: mode,
		ActivationJSON: json.RawMessage(`{"active":true}`),
		RelevanceJSON:  json.RawMessage(`{"score":0.9}`),
		JoinDecision:   conversationeval.JoinDecisionJoin,
		TopicRelation:  conversationeval.TopicRelationRelated,
		ContextSnapshot: conversationeval.ContextSnapshot{
			SchemaVersion: conversationeval.SchemaVersion, AnchorEventID: episode.AnchorEventID,
			AnchorAt: episode.AnchorAt, SystemPrompt: "system", UserPrompt: "user",
			TokenEstimate: 10, TokenBudget: 100,
			Messages: []conversationeval.ContextItem{{
				ID: "message_context", Source: "lark_message", SourceID: "om_context",
				Kind: "message", Content: "context", ContentHash: "sha256:context",
				Rank: 0, TokenCount: 10, Selected: true, OccurredAt: episode.AnchorAt.Add(-time.Minute),
			}},
		},
		ToolPlanJSON: json.RawMessage(`{}`), ReplyText: "reply",
		Latency: 100 * time.Millisecond, TokenUsageJSON: json.RawMessage(`{"total":10}`),
		ErrorJSON: json.RawMessage(`{}`),
	}
}

func assertCohortStatus(t *testing.T, db *gorm.DB, cohortID string, want conversationeval.CohortStatus) {
	t.Helper()
	var got string
	if err := db.Table("evaluation_cohorts").Select("status").Where("id = ?", cohortID).Scan(&got).Error; err != nil {
		t.Fatalf("read cohort status: %v", err)
	}
	if got != string(want) {
		t.Fatalf("cohort status = %q, want %q", got, want)
	}
}
