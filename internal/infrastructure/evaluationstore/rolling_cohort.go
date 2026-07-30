package evaluationstore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/conversationeval"
)

var _ conversationeval.RollingCohortStore = (*Repository)(nil)

func (r *Repository) EnsureRollingCohort(
	ctx context.Context,
	input conversationeval.MessageInput,
	duration time.Duration,
) (conversationeval.Cohort, error) {
	if err := input.Validate(); err != nil {
		return conversationeval.Cohort{}, err
	}
	if duration <= 0 || duration > 7*24*time.Hour {
		return conversationeval.Cohort{}, fmt.Errorf(
			"%w: rolling cohort duration must be within (0, 168h]",
			conversationeval.ErrInvalidContract,
		)
	}
	if input.AppID != r.tenant.AppID || input.BotOpenID != r.tenant.BotOpenID {
		return conversationeval.Cohort{}, fmt.Errorf(
			"%w: rolling cohort belongs to another tenant",
			conversationeval.ErrInvalidContract,
		)
	}
	db, err := r.database()
	if err != nil {
		return conversationeval.Cohort{}, err
	}
	startAt := input.OccurredAt.UTC().Truncate(duration)
	endAt := startAt.Add(duration)
	cohortID := rollingCohortID(r.tenant.ID, input.ChatID, startAt, duration)
	chatIDs, _ := json.Marshal([]string{input.ChatID})
	result := db.WithContext(ctx).Exec(`
		INSERT INTO evaluation_cohorts (
			id, tenant_id, app_id, bot_open_id, chat_ids, start_at, end_at,
			status, serving_lane, control_version, candidate_version,
			judge_config_json, sampling_policy_json, result_version
		) VALUES (?, ?, ?, ?, ?::jsonb, ?, ?, ?, ?, ?, ?, '{}'::jsonb,
		          '{"sample_rate":1,"source":"runtime_config"}'::jsonb, 0)
		ON CONFLICT (id) DO NOTHING`,
		cohortID, r.tenant.ID, r.tenant.AppID, r.tenant.BotOpenID,
		string(chatIDs), startAt, endAt,
		string(conversationeval.CohortStatusCollecting),
		string(conversationeval.LaneControl),
		"control-current", "candidate-current",
	)
	if result.Error != nil {
		return conversationeval.Cohort{}, result.Error
	}
	cohort, err := loadCohort(db.WithContext(ctx), r.tenant.ID, cohortID, false)
	if err != nil {
		return conversationeval.Cohort{}, err
	}
	if len(cohort.ChatIDs) != 1 ||
		cohort.ChatIDs[0] != input.ChatID ||
		!cohort.StartAt.Equal(startAt) ||
		!cohort.EndAt.Equal(endAt) {
		return conversationeval.Cohort{}, fmt.Errorf(
			"%w: rolling cohort identity collision",
			conversationeval.ErrInvalidContract,
		)
	}
	return cohort, nil
}

func rollingCohortID(
	tenantID string,
	chatID string,
	startAt time.Time,
	duration time.Duration,
) string {
	sum := sha256.Sum256([]byte(
		tenantID + "\x00" + chatID + "\x00" +
			startAt.Format(time.RFC3339Nano) + "\x00" + duration.String(),
	))
	return "eval_cohort_" + hex.EncodeToString(sum[:16])
}
