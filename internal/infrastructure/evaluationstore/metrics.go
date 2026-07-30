package evaluationstore

import (
	"context"
	"fmt"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/conversationeval"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/evaluationindex"
)

func (r *Repository) EvaluationMetrics(
	ctx context.Context,
	cursor evaluationindex.ProjectionCursor,
) (map[string]any, error) {
	db, err := r.database()
	if err != nil {
		return nil, err
	}
	groupCounts := func(table, column string) (map[string]int64, error) {
		var rows []struct {
			Value string
			Count int64
		}
		query := fmt.Sprintf(
			"SELECT %s AS value, count(*) AS count FROM %s GROUP BY %s",
			column,
			table,
			column,
		)
		if err := db.WithContext(ctx).Raw(query).Scan(&rows).Error; err != nil {
			return nil, err
		}
		values := make(map[string]int64, len(rows))
		for _, row := range rows {
			values[row.Value] = row.Count
		}
		return values, nil
	}
	cohorts, err := groupCounts("evaluation_cohorts", "status")
	if err != nil {
		return nil, err
	}
	episodes, err := groupCounts("evaluation_episodes", "status")
	if err != nil {
		return nil, err
	}

	var laneRows []struct {
		Lane           string
		Count          int64
		ErrorCount     int64
		AverageLatency float64
		AverageTokens  float64
	}
	if err := db.WithContext(ctx).Raw(`
		SELECT lane,
		       count(*) AS count,
		       count(*) FILTER (WHERE error_json <> '{}'::jsonb) AS error_count,
		       COALESCE(avg(latency_ms), 0) AS average_latency,
		       COALESCE(avg(
		           CASE
		               WHEN COALESCE(
		                   token_usage_json->>'total_tokens',
		                   token_usage_json->>'total',
		                   ''
		               ) ~ '^[0-9]+$'
		               THEN COALESCE(
		                   token_usage_json->>'total_tokens',
		                   token_usage_json->>'total'
		               )::bigint
		               ELSE 0
		           END
		       ), 0) AS average_tokens
		FROM evaluation_lane_outputs
		GROUP BY lane
		ORDER BY lane`,
	).Scan(&laneRows).Error; err != nil {
		return nil, err
	}
	lanes := make(map[string]any, len(laneRows))
	for _, row := range laneRows {
		lanes[row.Lane] = map[string]any{
			"total": row.Count, "errors": row.ErrorCount,
			"average_latency_ms": row.AverageLatency,
			"average_tokens":     row.AverageTokens,
		}
	}

	var agreement struct {
		Comparable         int64
		JoinAgreement      int64
		TopicAgreement     int64
		ShadowSafetyBlocks int64
	}
	if err := db.WithContext(ctx).Raw(`
		SELECT
		    count(*) AS comparable,
		    count(*) FILTER (WHERE control.join_decision = candidate.join_decision)
		        AS join_agreement,
		    count(*) FILTER (WHERE control.topic_relation = candidate.topic_relation)
		        AS topic_agreement,
		    count(*) FILTER (
		        WHERE candidate.error_json->>'safety_blocked' = 'true'
		           OR candidate.tool_plan_json->>'safety_blocked' = 'true'
		    ) AS shadow_safety_blocks
		FROM evaluation_lane_outputs AS control
		JOIN evaluation_lane_outputs AS candidate
		  ON candidate.episode_id = control.episode_id
		 AND candidate.lane = ?
		WHERE control.lane = ?`,
		string(conversationeval.LaneCandidate),
		string(conversationeval.LaneControl),
	).Scan(&agreement).Error; err != nil {
		return nil, err
	}

	var judgeBacklog int64
	if err := db.WithContext(ctx).Raw(`
		SELECT count(*)
		FROM evaluation_episodes AS episode
		WHERE episode.status = ?
		  AND episode.post_window_end IS NOT NULL
		  AND episode.post_window_end <= now()
		  AND (
		      SELECT count(DISTINCT lane)
		      FROM evaluation_lane_outputs
		      WHERE episode_id = episode.id AND lane IN (?, ?)
		  ) = 2`,
		string(conversationeval.EpisodeStatusReadyForJudge),
		string(conversationeval.LaneControl),
		string(conversationeval.LaneCandidate),
	).Scan(&judgeBacklog).Error; err != nil {
		return nil, err
	}

	var winnerRows []struct {
		Winner string
		Count  int64
	}
	if err := db.WithContext(ctx).Raw(`
		SELECT winner, count(*) AS count
		FROM (
		    SELECT DISTINCT ON (episode_id)
		           episode_id, winner
		    FROM evaluation_judgments
		    WHERE source = ?
		    ORDER BY episode_id, version DESC
		) AS latest
		GROUP BY winner`,
		string(conversationeval.JudgmentSourceConversationJudge),
	).Scan(&winnerRows).Error; err != nil {
		return nil, err
	}
	winners := make(map[string]int64, len(winnerRows))
	for _, row := range winnerRows {
		winners[row.Winner] = row.Count
	}

	var feedback struct {
		Total int64
		Late  int64
	}
	if err := db.WithContext(ctx).Raw(`
		SELECT count(*) AS total,
		       count(*) FILTER (
		           WHERE episode.post_window_end IS NOT NULL
		             AND feedback.occurred_at > episode.post_window_end
		       ) AS late
		FROM evaluation_feedback AS feedback
		JOIN evaluation_episodes AS episode ON episode.id = feedback.episode_id`,
	).Scan(&feedback).Error; err != nil {
		return nil, err
	}

	projectionQuery := `
		SELECT count(*)
		FROM evaluation_episodes
		WHERE status = ?`
	projectionArgs := []any{string(conversationeval.EpisodeStatusJudged)}
	if !cursor.UpdatedAt.IsZero() {
		projectionQuery += `
		  AND (updated_at > ? OR (updated_at = ? AND id > ?))`
		projectionArgs = append(
			projectionArgs,
			cursor.UpdatedAt,
			cursor.UpdatedAt,
			cursor.EpisodeID,
		)
	}
	var projectionBacklog int64
	if err := db.WithContext(ctx).Raw(
		projectionQuery,
		projectionArgs...,
	).Scan(&projectionBacklog).Error; err != nil {
		return nil, err
	}

	return map[string]any{
		"cohorts":  cohorts,
		"episodes": episodes,
		"lanes":    lanes,
		"agreement": map[string]any{
			"comparable": agreement.Comparable,
			"join":       agreement.JoinAgreement,
			"topic":      agreement.TopicAgreement,
		},
		"shadow_safety_blocks": agreement.ShadowSafetyBlocks,
		"judge_backlog":        judgeBacklog,
		"latest_winners":       winners,
		"feedback": map[string]int64{
			"total": feedback.Total,
			"late":  feedback.Late,
		},
		"projection_backlog": projectionBacklog,
		"captured_at":        time.Now().UTC(),
	}, nil
}
