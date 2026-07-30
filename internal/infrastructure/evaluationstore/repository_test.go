package evaluationstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/conversationeval"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/config"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/evaluationindex"
	uuid "github.com/satori/go.uuid"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

type repositoryFixture struct {
	db              *gorm.DB
	repo            *Repository
	suffix          string
	applicationName string
	now             time.Time
}

func TestConversationEvaluationRuntimeMigrationContract(t *testing.T) {
	configPath := os.Getenv("BETAGO_CONFIG_PATH")
	if configPath == "" {
		t.Skip("BETAGO_CONFIG_PATH is not set; skipping PostgreSQL migration contract test")
	}
	cfg, err := config.LoadFileE(configPath)
	if err != nil || cfg == nil || cfg.DBConfig == nil {
		t.Skip("PostgreSQL test configuration is unavailable")
	}
	rootDB, err := gorm.Open(postgres.Open(cfg.DBConfig.DSN()), &gorm.Config{})
	if err != nil {
		t.Skip("PostgreSQL is unavailable")
	}
	sqlDB, err := rootDB.DB()
	if err != nil {
		t.Fatalf("get PostgreSQL handle: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := sqlDB.PingContext(context.Background()); err != nil {
		t.Skip("PostgreSQL is unavailable")
	}

	var installed struct {
		EpisodeMessages  bool
		CandidateTasks   bool
		PostWindowReason bool
		TimelineIndex    bool
		ChatIDsIndex     bool
		ClaimIndex       bool
		ReclaimIndex     bool
	}
	if err := rootDB.Raw(`
		SELECT
			to_regclass('betago.evaluation_episode_messages') IS NOT NULL
				AS episode_messages,
			to_regclass('betago.evaluation_candidate_tasks') IS NOT NULL
				AS candidate_tasks,
			EXISTS (
				SELECT 1
				FROM information_schema.columns
				WHERE table_schema = 'betago'
				  AND table_name = 'evaluation_episodes'
				  AND column_name = 'post_window_reason'
			) AS post_window_reason,
			to_regclass('betago.idx_eval_episode_message_timeline') IS NOT NULL
				AS timeline_index,
			to_regclass('betago.idx_eval_cohort_chat_ids') IS NOT NULL
				AS chat_ids_index,
			to_regclass('betago.idx_eval_candidate_task_claim') IS NOT NULL
				AS claim_index,
			to_regclass('betago.idx_eval_candidate_task_reclaim') IS NOT NULL
				AS reclaim_index`,
	).Scan(&installed).Error; err != nil {
		t.Fatalf("inspect conversation evaluation runtime migration: %v", err)
	}
	if !installed.EpisodeMessages || !installed.CandidateTasks ||
		!installed.PostWindowReason || !installed.TimelineIndex ||
		!installed.ChatIDsIndex || !installed.ClaimIndex || !installed.ReclaimIndex {
		t.Fatalf("conversation evaluation runtime migration is incomplete: %+v", installed)
	}
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
		fmt.Sprintf(`ALTER TABLE %q.evaluation_episodes ADD COLUMN IF NOT EXISTS post_window_reason text NOT NULL DEFAULT ''`, schema),
		fmt.Sprintf(`ALTER TABLE %q.evaluation_episodes ADD FOREIGN KEY (cohort_id) REFERENCES %q.evaluation_cohorts(id) ON DELETE CASCADE`, schema, schema),
		fmt.Sprintf(`CREATE TABLE %q.evaluation_episode_messages (
			id text PRIMARY KEY,
			episode_id text NOT NULL REFERENCES %q.evaluation_episodes(id) ON DELETE CASCADE,
			position text NOT NULL,
			event_id text NOT NULL,
			message_id text NOT NULL,
			sequence integer NOT NULL,
			occurred_at timestamptz NOT NULL,
			payload_json jsonb NOT NULL DEFAULT '{}'::jsonb,
			created_at timestamptz NOT NULL DEFAULT now(),
			UNIQUE (episode_id, position, event_id)
		)`, schema, schema),
		fmt.Sprintf(`CREATE TABLE %q.evaluation_candidate_tasks (
			id text PRIMARY KEY,
			episode_id text NOT NULL REFERENCES %q.evaluation_episodes(id) ON DELETE CASCADE,
			status text NOT NULL,
			payload_json jsonb NOT NULL DEFAULT '{}'::jsonb,
			attempt_count integer NOT NULL DEFAULT 0,
			next_attempt_at timestamptz NOT NULL,
			worker_id text NOT NULL DEFAULT '',
			lease_expires_at timestamptz NULL,
			last_error text NOT NULL DEFAULT '',
			created_at timestamptz NOT NULL DEFAULT now(),
			updated_at timestamptz NOT NULL DEFAULT now(),
			UNIQUE (episode_id)
		)`, schema, schema),
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
	testConfig.ApplicationName = schema
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
		db: testDB, repo: NewRepository(testDB), suffix: suffix, applicationName: schema,
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

func TestGetOrCreateEpisodeFrozenCohortAllowsReplayButRejectsNewNaturalKey(t *testing.T) {
	fixture := newRepositoryFixture(t)
	ctx := context.Background()
	cohort := fixture.cohort("frozen_replay", fixture.now.Add(-time.Hour), fixture.now.Add(time.Hour))
	if err := fixture.repo.CreateCohort(ctx, cohort); err != nil {
		t.Fatalf("CreateCohort() error = %v", err)
	}
	original := fixture.episode(
		cohort.ID,
		"episode_frozen_original_"+fixture.suffix,
		"anchor_frozen_original",
	)
	canonical, err := fixture.repo.GetOrCreateEpisode(ctx, original)
	if err != nil {
		t.Fatalf("GetOrCreateEpisode(original) error = %v", err)
	}

	for _, sweep := range []struct {
		at         time.Time
		wantStatus conversationeval.CohortStatus
	}{
		{cohort.EndAt.Add(time.Hour), conversationeval.CohortStatusWaitingLateFeedback},
		{
			cohort.EndAt.Add(conversationeval.LateFeedbackGracePeriod + time.Hour),
			conversationeval.CohortStatusFinalized,
		},
	} {
		count, err := fixture.repo.TransitionCohorts(ctx, sweep.at)
		if err != nil {
			t.Fatalf("TransitionCohorts(%s) error = %v", sweep.at, err)
		}
		if count != 1 {
			t.Fatalf("TransitionCohorts(%s) count = %d, want 1", sweep.at, count)
		}
		assertCohortStatus(t, fixture.db, cohort.ID, sweep.wantStatus)
		replay := original
		replay.ID = "episode_frozen_replay_" + uuid.NewV4().String()
		stored, err := fixture.repo.GetOrCreateEpisode(ctx, replay)
		if err != nil {
			t.Fatalf("GetOrCreateEpisode(frozen replay) error = %v", err)
		}
		if stored.ID != canonical.ID {
			t.Fatalf("frozen replay ID = %q, want canonical %q", stored.ID, canonical.ID)
		}
		for name, mutate := range map[string]func(*conversationeval.Episode){
			"chat": func(episode *conversationeval.Episode) {
				episode.ChatID = "forged_chat"
			},
			"serving lane": func(episode *conversationeval.Episode) {
				episode.ServingLane = conversationeval.LaneCandidate
			},
			"anchor time": func(episode *conversationeval.Episode) {
				episode.AnchorAt = cohort.EndAt
				episode.PreWindowStart = episode.AnchorAt.Add(-time.Minute)
				episode.LateFeedbackUntil = episode.AnchorAt.Add(time.Hour)
			},
		} {
			forged := replay
			forged.ID = "episode_frozen_forged_" + uuid.NewV4().String()
			mutate(&forged)
			if _, err := fixture.repo.GetOrCreateEpisode(
				ctx,
				forged,
			); !errors.Is(err, conversationeval.ErrInvalidContract) {
				t.Fatalf(
					"GetOrCreateEpisode(frozen replay with forged %s) error = %v, want ErrInvalidContract",
					name, err,
				)
			}
		}

		newEpisode := fixture.episode(
			cohort.ID,
			"episode_frozen_new_"+uuid.NewV4().String(),
			"anchor_frozen_new_"+uuid.NewV4().String(),
		)
		if _, err := fixture.repo.GetOrCreateEpisode(
			ctx,
			newEpisode,
		); !errors.Is(err, conversationeval.ErrInvalidTransition) {
			t.Fatalf("GetOrCreateEpisode(new frozen key) error = %v, want ErrInvalidTransition", err)
		}
	}
	var count int64
	if err := fixture.db.Table("evaluation_episodes").
		Where("cohort_id = ?", cohort.ID).
		Count(&count).Error; err != nil {
		t.Fatalf("count frozen cohort episodes: %v", err)
	}
	if count != 1 {
		t.Fatalf("frozen cohort episode count = %d, want canonical only", count)
	}
}

func TestGetOrCreateEpisodeRejectsImmutableReplayConflicts(t *testing.T) {
	fixture := newRepositoryFixture(t)
	ctx := context.Background()
	cohort := fixture.cohort("replay_immutability", fixture.now.Add(-time.Hour), fixture.now.Add(time.Hour))
	cohort.ChatIDs = append(cohort.ChatIDs, "chat_also_allowed_"+fixture.suffix)
	if err := fixture.repo.CreateCohort(ctx, cohort); err != nil {
		t.Fatalf("CreateCohort() error = %v", err)
	}
	original := fixture.episode(
		cohort.ID,
		"episode_replay_original_"+fixture.suffix,
		"anchor_replay_immutable",
	)
	canonical, err := fixture.repo.GetOrCreateEpisode(ctx, original)
	if err != nil {
		t.Fatalf("GetOrCreateEpisode(original) error = %v", err)
	}

	validReplay := original
	validReplay.ID = "episode_replay_other_id_" + fixture.suffix
	validReplay.AnchorAt = validReplay.AnchorAt.Add(500 * time.Nanosecond)
	validReplay.PreWindowStart = validReplay.PreWindowStart.Add(500 * time.Nanosecond)
	validReplay.LateFeedbackUntil = validReplay.LateFeedbackUntil.Add(500 * time.Nanosecond)
	replayed, err := fixture.repo.GetOrCreateEpisode(ctx, validReplay)
	if err != nil {
		t.Fatalf("GetOrCreateEpisode(valid replay) error = %v", err)
	}
	if replayed.ID != canonical.ID {
		t.Fatalf("valid replay ID = %q, want canonical %q", replayed.ID, canonical.ID)
	}

	tests := []struct {
		name   string
		mutate func(*conversationeval.Episode)
	}{
		{"chat_id", func(episode *conversationeval.Episode) {
			episode.ChatID = cohort.ChatIDs[1]
		}},
		{"anchor_message_id", func(episode *conversationeval.Episode) {
			episode.AnchorMessageID = "different_anchor_message"
		}},
		{"run_id", func(episode *conversationeval.Episode) {
			episode.RunID = "different_run"
		}},
		{"topic_id", func(episode *conversationeval.Episode) {
			episode.TopicID = "different_topic"
		}},
		{"pre_window_start", func(episode *conversationeval.Episode) {
			episode.PreWindowStart = episode.PreWindowStart.Add(time.Microsecond)
		}},
		{"anchor_at", func(episode *conversationeval.Episode) {
			episode.AnchorAt = episode.AnchorAt.Add(time.Microsecond)
		}},
		{"late_feedback_until", func(episode *conversationeval.Episode) {
			episode.LateFeedbackUntil = episode.LateFeedbackUntil.Add(time.Microsecond)
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			replay := original
			replay.ID = "episode_replay_conflict_" + tt.name + "_" + fixture.suffix
			tt.mutate(&replay)
			if _, err := fixture.repo.GetOrCreateEpisode(
				ctx,
				replay,
			); !errors.Is(err, conversationeval.ErrInvalidContract) {
				t.Fatalf("GetOrCreateEpisode() error = %v, want ErrInvalidContract", err)
			}
		})
	}
}

func TestUpsertLaneOutputAcceptsPostgresMicrosecondAnchorRoundTrip(t *testing.T) {
	fixture := newRepositoryFixture(t)
	ctx := context.Background()
	cohort := fixture.cohort("anchor_precision", fixture.now.Add(-time.Hour), fixture.now.Add(time.Hour))
	if err := fixture.repo.CreateCohort(ctx, cohort); err != nil {
		t.Fatalf("CreateCohort() error = %v", err)
	}
	original := fixture.episode(
		cohort.ID,
		"episode_anchor_precision_"+fixture.suffix,
		"anchor_precision",
	)
	original.AnchorAt = fixture.now.Add(789 * time.Nanosecond)
	original.PreWindowStart = original.AnchorAt.Add(-time.Minute)
	original.LateFeedbackUntil = original.AnchorAt.Add(conversationeval.LateFeedbackGracePeriod)
	if _, err := fixture.repo.GetOrCreateEpisode(ctx, original); err != nil {
		t.Fatalf("GetOrCreateEpisode(nanosecond anchor) error = %v", err)
	}
	output := fixture.laneOutput(original, conversationeval.LaneControl, "anchor_precision_output")
	if err := fixture.repo.UpsertLaneOutput(ctx, output); err != nil {
		t.Fatalf("UpsertLaneOutput(nanosecond anchor) error = %v", err)
	}
}

func TestGetOrCreateEpisodeCommitsBeforeWaitingCohortTransition(t *testing.T) {
	fixture := newRepositoryFixture(t)
	ctx := context.Background()
	cohort := fixture.cohort(
		"create_before_transition",
		fixture.now.Add(-2*time.Hour),
		fixture.now.Add(-30*time.Minute),
	)
	if err := fixture.repo.CreateCohort(ctx, cohort); err != nil {
		t.Fatalf("CreateCohort() error = %v", err)
	}
	advisoryKey := fixtureAdvisoryKey(fixture.suffix, "episode-insert")
	installAdvisoryTrigger(t, fixture.db, "evaluation_episodes", "INSERT", "block_episode_insert", advisoryKey)
	lock := holdAdvisoryLock(t, fixture.db, advisoryKey)

	episode := fixture.episode(
		cohort.ID,
		"episode_create_before_transition_"+fixture.suffix,
		"anchor_create_before_transition",
	)
	episode.AnchorAt = fixture.now.Add(-time.Hour)
	episode.PreWindowStart = episode.AnchorAt.Add(-time.Minute)
	episode.LateFeedbackUntil = episode.AnchorAt.Add(conversationeval.LateFeedbackGracePeriod)
	createDone := make(chan error, 1)
	go func() {
		_, err := fixture.repo.GetOrCreateEpisode(ctx, episode)
		createDone <- err
	}()
	waitForAdvisoryWaiter(t, fixture.db, advisoryKey)

	transitionDone := make(chan transitionResult, 1)
	go func() {
		count, err := fixture.repo.TransitionCohorts(ctx, fixture.now)
		transitionDone <- transitionResult{count: count, err: err}
	}()
	waitForTransactionWaiter(t, fixture.db, fixture.applicationName)
	select {
	case result := <-transitionDone:
		lock.release()
		t.Fatalf("TransitionCohorts() returned before creator released cohort lock: %#v", result)
	default:
	}
	lock.release()
	if err := receiveError(t, createDone); err != nil {
		t.Fatalf("GetOrCreateEpisode() error = %v", err)
	}
	transition := receiveTransition(t, transitionDone)
	if transition.err != nil || transition.count != 1 {
		t.Fatalf("TransitionCohorts() = %#v, want one transition after create commit", transition)
	}
	assertCohortStatus(t, fixture.db, cohort.ID, conversationeval.CohortStatusWaitingLateFeedback)
}

func TestGetOrCreateEpisodeRejectsNewKeyWhenTransitionCommitsFirst(t *testing.T) {
	fixture := newRepositoryFixture(t)
	ctx := context.Background()
	cohort := fixture.cohort(
		"transition_before_create",
		fixture.now.Add(-2*time.Hour),
		fixture.now.Add(-30*time.Minute),
	)
	if err := fixture.repo.CreateCohort(ctx, cohort); err != nil {
		t.Fatalf("CreateCohort() error = %v", err)
	}
	advisoryKey := fixtureAdvisoryKey(fixture.suffix, "cohort-update")
	installAdvisoryTrigger(t, fixture.db, "evaluation_cohorts", "UPDATE", "block_cohort_update", advisoryKey)
	lock := holdAdvisoryLock(t, fixture.db, advisoryKey)

	transitionDone := make(chan transitionResult, 1)
	go func() {
		count, err := fixture.repo.TransitionCohorts(ctx, fixture.now)
		transitionDone <- transitionResult{count: count, err: err}
	}()
	waitForAdvisoryWaiter(t, fixture.db, advisoryKey)

	episode := fixture.episode(
		cohort.ID,
		"episode_transition_before_create_"+fixture.suffix,
		"anchor_transition_before_create",
	)
	episode.AnchorAt = fixture.now.Add(-time.Hour)
	episode.PreWindowStart = episode.AnchorAt.Add(-time.Minute)
	episode.LateFeedbackUntil = episode.AnchorAt.Add(conversationeval.LateFeedbackGracePeriod)
	createDone := make(chan error, 1)
	go func() {
		_, err := fixture.repo.GetOrCreateEpisode(ctx, episode)
		createDone <- err
	}()
	waitForTransactionWaiter(t, fixture.db, fixture.applicationName)
	select {
	case err := <-createDone:
		lock.release()
		t.Fatalf("GetOrCreateEpisode() returned before transition released cohort lock: %v", err)
	default:
	}
	lock.release()
	transition := receiveTransition(t, transitionDone)
	if transition.err != nil || transition.count != 1 {
		t.Fatalf("TransitionCohorts() = %#v, want one committed transition", transition)
	}
	if err := receiveError(t, createDone); !errors.Is(err, conversationeval.ErrInvalidTransition) {
		t.Fatalf("GetOrCreateEpisode() error = %v, want ErrInvalidTransition", err)
	}
	var count int64
	if err := fixture.db.Table("evaluation_episodes").
		Where("cohort_id = ?", cohort.ID).
		Count(&count).Error; err != nil {
		t.Fatalf("count rejected episodes: %v", err)
	}
	if count != 0 {
		t.Fatalf("episode count = %d, want no insert after transition commit", count)
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
	actual.ToolPlanJSON = json.RawMessage(`{"delivery_message_id":"candidate_message"}`)
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
	actual := fixture.laneOutput(*stored, conversationeval.LaneControl, "feedback_control_actual")
	actual.ToolPlanJSON = json.RawMessage(`{"delivery_message_id":"message_reply"}`)
	if err := fixture.repo.UpsertLaneOutput(ctx, actual); err != nil {
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

func TestFeedbackCandidatesExposeOnlyActualDeliveryIdentity(t *testing.T) {
	fixture := newRepositoryFixture(t)
	ctx := context.Background()
	cohort := fixture.cohort(
		"feedback_candidates",
		fixture.now.Add(-time.Hour),
		fixture.now.Add(time.Hour),
	)
	if err := fixture.repo.CreateCohort(ctx, cohort); err != nil {
		t.Fatalf("CreateCohort() error = %v", err)
	}
	episode := fixture.episode(
		cohort.ID,
		"episode_feedback_candidates_"+fixture.suffix,
		"anchor_feedback_candidates",
	)
	stored, err := fixture.repo.GetOrCreateEpisode(ctx, episode)
	if err != nil {
		t.Fatalf("GetOrCreateEpisode() error = %v", err)
	}
	actual := fixture.laneOutput(*stored, conversationeval.LaneControl, "actual_delivery")
	actual.ToolPlanJSON = json.RawMessage(`{"delivery_message_id":"actual-message"}`)
	if err := fixture.repo.UpsertLaneOutput(ctx, actual); err != nil {
		t.Fatalf("UpsertLaneOutput(actual) error = %v", err)
	}
	shadow := fixture.laneOutput(*stored, conversationeval.LaneCandidate, "shadow_delivery")
	shadow.ToolPlanJSON = json.RawMessage(`{"delivery_message_id":"shadow-message"}`)
	if err := fixture.repo.UpsertLaneOutput(ctx, shadow); err != nil {
		t.Fatalf("UpsertLaneOutput(shadow) error = %v", err)
	}

	candidates, err := fixture.repo.FeedbackCandidates(
		ctx,
		stored.ChatID,
		stored.AnchorAt.Add(time.Minute),
	)
	if err != nil {
		t.Fatalf("FeedbackCandidates() error = %v", err)
	}
	if len(candidates) != 1 ||
		candidates[0].Episode.ID != stored.ID ||
		candidates[0].DeliveryMessageID != "actual-message" {
		t.Fatalf("FeedbackCandidates() = %#v", candidates)
	}
}

func TestAppendFeedbackEnforcesWindowAndVersionsFinalizedCohortOnce(t *testing.T) {
	fixture := newRepositoryFixture(t)
	ctx := context.Background()
	cohort := fixture.cohort(
		"feedback_window",
		fixture.now.Add(-time.Hour),
		fixture.now.Add(time.Hour),
	)
	if err := fixture.repo.CreateCohort(ctx, cohort); err != nil {
		t.Fatalf("CreateCohort() error = %v", err)
	}
	episode := fixture.episode(
		cohort.ID,
		"episode_feedback_window_"+fixture.suffix,
		"anchor_feedback_window",
	)
	stored, err := fixture.repo.GetOrCreateEpisode(ctx, episode)
	if err != nil {
		t.Fatalf("GetOrCreateEpisode() error = %v", err)
	}
	postEnd := stored.AnchorAt.Add(10 * time.Minute)
	if err := fixture.db.Table("evaluation_episodes").
		Where("id = ?", stored.ID).
		Update("post_window_end", postEnd).Error; err != nil {
		t.Fatalf("set post window end: %v", err)
	}

	expired := conversationeval.Feedback{
		ID: "feedback_expired_" + fixture.suffix, EpisodeID: stored.ID,
		FeedbackEventID:       "feedback_expired_event",
		FeedbackType:          conversationeval.FeedbackTypeCorrection,
		Explicitness:          conversationeval.FeedbackExplicit,
		ContentJSON:           json.RawMessage(`{"text":"late"}`),
		AttributionConfidence: 90,
		OccurredAt:            stored.LateFeedbackUntil.Add(time.Second),
	}
	if err := fixture.repo.AppendFeedback(ctx, expired); !errors.Is(err, conversationeval.ErrInvalidContract) {
		t.Fatalf("AppendFeedback(expired) error = %v, want ErrInvalidContract", err)
	}
	inferred := expired
	inferred.ID = "feedback_inferred_" + fixture.suffix
	inferred.FeedbackEventID = "feedback_inferred_event"
	inferred.FeedbackType = conversationeval.FeedbackTypeSemanticInference
	inferred.Explicitness = conversationeval.FeedbackInferred
	inferred.OccurredAt = postEnd.Add(time.Second)
	if err := fixture.repo.AppendFeedback(ctx, inferred); !errors.Is(err, conversationeval.ErrInvalidContract) {
		t.Fatalf("AppendFeedback(inferred after post window) error = %v, want ErrInvalidContract", err)
	}

	if err := fixture.db.Table("evaluation_cohorts").
		Where("id = ?", cohort.ID).
		Updates(map[string]any{
			"status":         string(conversationeval.CohortStatusFinalized),
			"result_version": 7,
		}).Error; err != nil {
		t.Fatalf("finalize cohort fixture: %v", err)
	}
	lateExplicit := expired
	lateExplicit.ID = "feedback_late_explicit_" + fixture.suffix
	lateExplicit.FeedbackEventID = "feedback_late_explicit_event"
	lateExplicit.OccurredAt = stored.AnchorAt.Add(23 * time.Hour)
	if err := fixture.repo.AppendFeedback(ctx, lateExplicit); err != nil {
		t.Fatalf("AppendFeedback(late explicit) error = %v", err)
	}
	if err := fixture.repo.AppendFeedback(ctx, lateExplicit); err != nil {
		t.Fatalf("AppendFeedback(duplicate late explicit) error = %v", err)
	}
	var resultVersion int64
	if err := fixture.db.Table("evaluation_cohorts").
		Select("result_version").
		Where("id = ?", cohort.ID).
		Scan(&resultVersion).Error; err != nil {
		t.Fatalf("read result version: %v", err)
	}
	if resultVersion != 8 {
		t.Fatalf("result_version = %d, want 8", resultVersion)
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

func TestNextJudgeInputLoadsBundleAndAppendMarksJudged(t *testing.T) {
	fixture := newRepositoryFixture(t)
	ctx := context.Background()
	cohort := fixture.cohort(
		"judge_bundle",
		fixture.now.Add(-time.Hour),
		fixture.now.Add(time.Hour),
	)
	if err := fixture.repo.CreateCohort(ctx, cohort); err != nil {
		t.Fatalf("CreateCohort() error = %v", err)
	}
	ready := fixture.episode(
		cohort.ID,
		"episode_judge_bundle_"+fixture.suffix,
		"anchor_judge_bundle",
	)
	ready.Status = conversationeval.EpisodeStatusReadyForJudge
	postEnd := fixture.now
	ready.PostWindowEnd = &postEnd
	stored, err := fixture.repo.GetOrCreateEpisode(ctx, ready)
	if err != nil {
		t.Fatalf("GetOrCreateEpisode() error = %v", err)
	}
	anchor := conversationeval.WindowMessage{
		EventID: stored.AnchorEventID, MessageID: stored.AnchorMessageID,
		ChatID: stored.ChatID, TopicID: stored.TopicID, SenderOpenID: "sender",
		Content: "anchor", OccurredAt: stored.AnchorAt,
		Position: conversationeval.WindowPositionAnchor,
	}
	if err := fixture.repo.SaveWindowMessages(ctx, stored.ID, []conversationeval.WindowMessage{anchor}); err != nil {
		t.Fatalf("SaveWindowMessages() error = %v", err)
	}
	for _, lane := range []conversationeval.Lane{
		conversationeval.LaneControl,
		conversationeval.LaneCandidate,
	} {
		if err := fixture.repo.UpsertLaneOutput(
			ctx,
			fixture.laneOutput(*stored, lane, "judge_"+string(lane)),
		); err != nil {
			t.Fatalf("UpsertLaneOutput(%s) error = %v", lane, err)
		}
	}

	input, err := fixture.repo.NextJudgeInput(ctx, fixture.now.Add(time.Second))
	if err != nil {
		t.Fatalf("NextJudgeInput() error = %v", err)
	}
	if input.Episode.ID != stored.ID || input.Version != 1 ||
		input.PreviousJudgmentID != "" || len(input.Messages) != 1 ||
		input.ControlOutput.Lane != conversationeval.LaneControl ||
		input.CandidateOutput.Lane != conversationeval.LaneCandidate {
		t.Fatalf("judge input = %#v", input)
	}
	judgment := conversationeval.Judgment{
		ID: "judge_bundle_v1_" + fixture.suffix, EpisodeID: stored.ID, Version: 1,
		Source: conversationeval.JudgmentSourceConversationJudge, EvaluatorID: "judge-model",
		Winner: conversationeval.JudgmentWinnerTie, ScoresJSON: json.RawMessage(`{"ok":true}`),
		Rationale: "tie", Confidence: 80, CreatedAt: fixture.now.Add(time.Second),
	}
	if err := fixture.repo.AppendJudgment(ctx, judgment); err != nil {
		t.Fatalf("AppendJudgment() error = %v", err)
	}
	var status string
	if err := fixture.db.Table("evaluation_episodes").
		Select("status").
		Where("id = ?", stored.ID).
		Scan(&status).Error; err != nil {
		t.Fatalf("read episode status: %v", err)
	}
	if status != string(conversationeval.EpisodeStatusJudged) {
		t.Fatalf("episode status = %q, want judged", status)
	}
	snapshots, err := fixture.repo.EvaluationSnapshotsAfter(
		ctx,
		evaluationindex.ProjectionCursor{},
		10,
	)
	if err != nil {
		t.Fatalf("EvaluationSnapshotsAfter() error = %v", err)
	}
	if len(snapshots) != 1 || snapshots[0].EpisodeID != stored.ID ||
		snapshots[0].AnchorMessage.MessageID != stored.AnchorMessageID ||
		len(snapshots[0].LatestJudgments) != 1 ||
		snapshots[0].LatestJudgments[0].Version != 1 {
		t.Fatalf("evaluation snapshots = %#v", snapshots)
	}
	metrics, err := fixture.repo.EvaluationMetrics(
		ctx,
		evaluationindex.ProjectionCursor{},
	)
	if err != nil {
		t.Fatalf("EvaluationMetrics() error = %v", err)
	}
	episodeMetrics, ok := metrics["episodes"].(map[string]int64)
	if !ok || episodeMetrics[string(conversationeval.EpisodeStatusJudged)] != 1 ||
		metrics["projection_backlog"] != int64(1) {
		t.Fatalf("evaluation metrics = %#v", metrics)
	}
	after := evaluationindex.ProjectionCursor{
		UpdatedAt: snapshots[0].UpdatedAt,
		EpisodeID: snapshots[0].EpisodeID,
	}
	if snapshots, err := fixture.repo.EvaluationSnapshotsAfter(ctx, after, 10); err != nil {
		t.Fatalf("EvaluationSnapshotsAfter(cursor) error = %v", err)
	} else if len(snapshots) != 0 {
		t.Fatalf("EvaluationSnapshotsAfter(cursor) = %#v, want empty", snapshots)
	}
	if _, err := fixture.repo.NextJudgeInput(
		ctx,
		fixture.now.Add(2*time.Second),
	); !errors.Is(err, conversationeval.ErrJudgeInputNotFound) {
		t.Fatalf("NextJudgeInput(after judge) error = %v, want ErrJudgeInputNotFound", err)
	}
}

func TestNextJudgeInputRejudgesAfterNewFeedback(t *testing.T) {
	fixture := newRepositoryFixture(t)
	ctx := context.Background()
	cohort := fixture.cohort(
		"judge_feedback",
		fixture.now.Add(-time.Hour),
		fixture.now.Add(time.Hour),
	)
	if err := fixture.repo.CreateCohort(ctx, cohort); err != nil {
		t.Fatalf("CreateCohort() error = %v", err)
	}
	ready := fixture.episode(
		cohort.ID,
		"episode_judge_feedback_"+fixture.suffix,
		"anchor_judge_feedback",
	)
	ready.Status = conversationeval.EpisodeStatusReadyForJudge
	postEnd := fixture.now
	ready.PostWindowEnd = &postEnd
	stored, err := fixture.repo.GetOrCreateEpisode(ctx, ready)
	if err != nil {
		t.Fatalf("GetOrCreateEpisode() error = %v", err)
	}
	anchor := conversationeval.WindowMessage{
		EventID: stored.AnchorEventID, MessageID: stored.AnchorMessageID,
		ChatID: stored.ChatID, Content: "anchor", OccurredAt: stored.AnchorAt,
		Position: conversationeval.WindowPositionAnchor,
	}
	if err := fixture.repo.SaveWindowMessages(ctx, stored.ID, []conversationeval.WindowMessage{anchor}); err != nil {
		t.Fatalf("SaveWindowMessages() error = %v", err)
	}
	for _, lane := range []conversationeval.Lane{
		conversationeval.LaneControl,
		conversationeval.LaneCandidate,
	} {
		if err := fixture.repo.UpsertLaneOutput(
			ctx,
			fixture.laneOutput(*stored, lane, "feedback_"+string(lane)),
		); err != nil {
			t.Fatalf("UpsertLaneOutput(%s) error = %v", lane, err)
		}
	}
	first := conversationeval.Judgment{
		ID: "judge_feedback_v1_" + fixture.suffix, EpisodeID: stored.ID, Version: 1,
		Source: conversationeval.JudgmentSourceConversationJudge, EvaluatorID: "judge-model",
		Winner:     conversationeval.JudgmentWinnerControl,
		ScoresJSON: json.RawMessage(`{"ok":true}`), Rationale: "control",
		Confidence: 80,
	}
	if err := fixture.repo.AppendJudgment(ctx, first); err != nil {
		t.Fatalf("AppendJudgment(v1) error = %v", err)
	}
	feedback := conversationeval.Feedback{
		ID: "judge_feedback_event_" + fixture.suffix, EpisodeID: stored.ID,
		FeedbackEventID: "judge_feedback_event", FeedbackType: conversationeval.FeedbackTypeCorrection,
		Explicitness:          conversationeval.FeedbackExplicit,
		ContentJSON:           json.RawMessage(`{"text":"不对"}`),
		AttributionConfidence: 95, OccurredAt: stored.AnchorAt.Add(time.Minute),
	}
	if err := fixture.repo.AppendFeedback(ctx, feedback); err != nil {
		t.Fatalf("AppendFeedback() error = %v", err)
	}
	input, err := fixture.repo.NextJudgeInput(ctx, fixture.now.Add(2*time.Minute))
	if err != nil {
		t.Fatalf("NextJudgeInput(rejudge) error = %v", err)
	}
	if input.Version != 2 || input.PreviousJudgmentID != first.ID ||
		len(input.Feedback) != 1 || input.Episode.Status != conversationeval.EpisodeStatusJudged {
		t.Fatalf("rejudge input = %#v", input)
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

type heldAdvisoryLock struct {
	t        *testing.T
	conn     *sql.Conn
	key      int64
	released bool
}

func holdAdvisoryLock(t *testing.T, db *gorm.DB, key int64) *heldAdvisoryLock {
	t.Helper()
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get SQL database: %v", err)
	}
	conn, err := sqlDB.Conn(context.Background())
	if err != nil {
		t.Fatalf("reserve advisory lock connection: %v", err)
	}
	if _, err := conn.ExecContext(context.Background(), `SELECT pg_advisory_lock($1)`, key); err != nil {
		_ = conn.Close()
		t.Fatalf("hold advisory lock: %v", err)
	}
	lock := &heldAdvisoryLock{t: t, conn: conn, key: key}
	t.Cleanup(lock.release)
	return lock
}

func (l *heldAdvisoryLock) release() {
	l.t.Helper()
	if l.released {
		return
	}
	l.released = true
	if _, err := l.conn.ExecContext(
		context.Background(),
		`SELECT pg_advisory_unlock($1)`,
		l.key,
	); err != nil {
		l.t.Errorf("release advisory lock: %v", err)
	}
	if err := l.conn.Close(); err != nil {
		l.t.Errorf("close advisory lock connection: %v", err)
	}
}

func installAdvisoryTrigger(
	t *testing.T,
	db *gorm.DB,
	table string,
	event string,
	name string,
	key int64,
) {
	t.Helper()
	functionName := name + "_fn"
	if err := db.Exec(fmt.Sprintf(`
		CREATE FUNCTION %q() RETURNS trigger
		LANGUAGE plpgsql
		AS $trigger$
		BEGIN
			PERFORM pg_advisory_xact_lock(%d);
			RETURN NEW;
		END
		$trigger$`, functionName, key)).Error; err != nil {
		t.Fatalf("create advisory trigger function: %v", err)
	}
	if err := db.Exec(fmt.Sprintf(`
		CREATE TRIGGER %q
		BEFORE %s ON %q
		FOR EACH ROW EXECUTE FUNCTION %q()`,
		name, event, table, functionName,
	)).Error; err != nil {
		t.Fatalf("create advisory trigger: %v", err)
	}
}

func waitForAdvisoryWaiter(t *testing.T, db *gorm.DB, key int64) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var count int64
		if err := db.Raw(`
			SELECT count(*)
			FROM pg_locks
			WHERE locktype = 'advisory'
			  AND classid = 0
			  AND objid = ?
			  AND NOT granted`,
			key,
		).Scan(&count).Error; err != nil {
			t.Fatalf("inspect advisory lock waiter: %v", err)
		}
		if count > 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for advisory lock waiter %d", key)
}

func waitForTransactionWaiter(t *testing.T, db *gorm.DB, applicationName string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var count int64
		if err := db.Raw(`
			SELECT count(*)
			FROM pg_stat_activity
			WHERE datname = current_database()
			  AND application_name = ?
			  AND wait_event_type = 'Lock'
			  AND wait_event = 'transactionid'`,
			applicationName,
		).Scan(&count).Error; err != nil {
			t.Fatalf("inspect transaction lock waiter: %v", err)
		}
		if count > 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("timed out waiting for transaction lock waiter")
}

func fixtureAdvisoryKey(suffix, purpose string) int64 {
	hash := fnv.New32a()
	_, _ = hash.Write([]byte(suffix))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write([]byte(purpose))
	return int64(hash.Sum32()&0x7fffffff) + 1
}

type transitionResult struct {
	count int64
	err   error
}

func receiveTransition(t *testing.T, results <-chan transitionResult) transitionResult {
	t.Helper()
	select {
	case result := <-results:
		return result
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for cohort transition")
		return transitionResult{}
	}
}

func receiveError(t *testing.T, results <-chan error) error {
	t.Helper()
	select {
	case err := <-results:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for repository operation")
		return nil
	}
}
