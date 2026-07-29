package evaluationstore

import (
	"context"
	"encoding/json"
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
	if transitioned != 1 {
		t.Fatalf("first TransitionCohorts() = %d, want 1", transitioned)
	}
	assertCohortStatus(t, fixture.db, expired.ID, conversationeval.CohortStatusWaitingLateFeedback)

	transitioned, err = fixture.repo.TransitionCohorts(ctx, fixture.now)
	if err != nil {
		t.Fatalf("second TransitionCohorts() error = %v", err)
	}
	if transitioned != 1 {
		t.Fatalf("second TransitionCohorts() = %d, want 1", transitioned)
	}
	assertCohortStatus(t, fixture.db, expired.ID, conversationeval.CohortStatusFinalized)

	transitioned, err = fixture.repo.TransitionCohorts(ctx, fixture.now.Add(-72*time.Hour))
	if err != nil {
		t.Fatalf("past TransitionCohorts() error = %v", err)
	}
	if transitioned != 0 {
		t.Fatalf("past TransitionCohorts() = %d, want irreversible no-op", transitioned)
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
	for stored := range results {
		if episodeID == "" {
			episodeID = stored.ID
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

	first := fixture.laneOutput(episodeID, conversationeval.LaneControl, "lane_control_first")
	if err := fixture.repo.UpsertLaneOutput(ctx, first); err != nil {
		t.Fatalf("first UpsertLaneOutput() error = %v", err)
	}
	second := fixture.laneOutput(episodeID, conversationeval.LaneControl, "lane_control_second")
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
	var storedReply string
	if err := fixture.db.Table("evaluation_lane_outputs").
		Select("reply_text").
		Where("episode_id = ? AND lane = ?", episodeID, conversationeval.LaneControl).
		Scan(&storedReply).Error; err != nil {
		t.Fatalf("read lane output: %v", err)
	}
	if laneCount != 1 || storedReply != second.ReplyText {
		t.Fatalf("lane output count/reply = %d/%q, want 1/%q", laneCount, storedReply, second.ReplyText)
	}

	nonTargetCollision := fixture.laneOutput(episodeID, conversationeval.LaneCandidate, first.ID)
	nonTargetCollision.ID = first.ID
	if err := fixture.repo.UpsertLaneOutput(ctx, nonTargetCollision); err == nil {
		t.Fatal("UpsertLaneOutput(primary-key collision) error = nil, want non-target conflict")
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
	if err := fixture.repo.AppendFeedback(ctx, feedback); err != nil {
		t.Fatalf("first AppendFeedback() error = %v", err)
	}
	duplicate := feedback
	duplicate.ID = "feedback_2_" + fixture.suffix
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
	if err := fixture.repo.UpsertLaneOutput(ctx, fixture.laneOutput(stored.ID, conversationeval.LaneControl, "ready_control")); err != nil {
		t.Fatalf("UpsertLaneOutput(control) error = %v", err)
	}
	episodes, err := fixture.repo.EpisodesReadyForJudge(ctx, fixture.now, 10)
	if err != nil {
		t.Fatalf("EpisodesReadyForJudge(one lane) error = %v", err)
	}
	if len(episodes) != 0 {
		t.Fatalf("EpisodesReadyForJudge(one lane) = %#v, want empty", episodes)
	}
	if err := fixture.repo.UpsertLaneOutput(ctx, fixture.laneOutput(stored.ID, conversationeval.LaneCandidate, "ready_candidate")); err != nil {
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

func (f *repositoryFixture) laneOutput(episodeID string, lane conversationeval.Lane, id string) conversationeval.LaneOutput {
	mode := conversationeval.OutputModeShadow
	if lane == conversationeval.LaneControl {
		mode = conversationeval.OutputModeActual
	}
	return conversationeval.LaneOutput{
		ID: id + "_" + f.suffix, EpisodeID: episodeID, Lane: lane, OutputMode: mode,
		ActivationJSON: json.RawMessage(`{"active":true}`),
		RelevanceJSON:  json.RawMessage(`{"score":0.9}`),
		JoinDecision:   conversationeval.JoinDecisionJoin,
		TopicRelation:  conversationeval.TopicRelationRelated,
		ContextSnapshot: conversationeval.ContextSnapshot{
			SchemaVersion: conversationeval.SchemaVersion, AnchorEventID: "anchor_shared",
			AnchorAt: f.now, SystemPrompt: "system", UserPrompt: "user",
			TokenEstimate: 10, TokenBudget: 100,
			Messages: []conversationeval.ContextItem{{
				ID: "message_context", Source: "lark_message", SourceID: "om_context",
				Kind: "message", Content: "context", ContentHash: "sha256:context",
				Rank: 0, TokenCount: 10, Selected: true, OccurredAt: f.now.Add(-time.Minute),
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
