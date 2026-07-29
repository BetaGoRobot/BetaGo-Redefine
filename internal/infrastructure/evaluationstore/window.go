package evaluationstore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/conversationeval"
	"gorm.io/gorm"
)

var _ conversationeval.EvaluationRepository = (*Repository)(nil)

func (r *Repository) SaveWindowMessages(
	ctx context.Context,
	episodeID string,
	messages []conversationeval.WindowMessage,
) error {
	if err := validateQueryID("episode_id", episodeID); err != nil {
		return err
	}
	if len(messages) == 0 {
		return fmt.Errorf("%w: window messages must not be empty", conversationeval.ErrInvalidContract)
	}
	db, err := r.database()
	if err != nil {
		return err
	}
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		episode, err := loadEpisode(tx, episodeID, true)
		if err != nil {
			return err
		}
		for _, message := range messages {
			if message.Position != conversationeval.WindowPositionPre &&
				message.Position != conversationeval.WindowPositionAnchor {
				return fmt.Errorf(
					"%w: initial window message position must be pre or anchor",
					conversationeval.ErrInvalidContract,
				)
			}
			if err := validateWindowMessageForEpisode(message, episode); err != nil {
				return err
			}
			if message.Position == conversationeval.WindowPositionPre &&
				!message.OccurredAt.Before(episode.AnchorAt) {
				return fmt.Errorf(
					"%w: pre-window message must precede anchor",
					conversationeval.ErrInvalidContract,
				)
			}
			if message.Position == conversationeval.WindowPositionAnchor &&
				(message.EventID != episode.AnchorEventID ||
					message.MessageID != episode.AnchorMessageID ||
					message.OccurredAt.UnixMicro() != episode.AnchorAt.UnixMicro()) {
				return fmt.Errorf(
					"%w: anchor window message does not match episode",
					conversationeval.ErrInvalidContract,
				)
			}
			if err := insertWindowMessage(tx, episodeID, message); err != nil {
				return err
			}
		}
		return nil
	})
}

func (r *Repository) OpenEpisodesForMessage(
	ctx context.Context,
	chatID string,
	at time.Time,
) ([]conversationeval.Episode, error) {
	if err := validateQueryID("chat_id", chatID); err != nil {
		return nil, err
	}
	if at.IsZero() {
		return nil, fmt.Errorf("%w: message timestamp must not be zero", conversationeval.ErrInvalidContract)
	}
	db, err := r.database()
	if err != nil {
		return nil, err
	}
	var rows []episodeRow
	if err := db.WithContext(ctx).Raw(`
		SELECT id, cohort_id, chat_id, run_id, anchor_event_id, anchor_message_id,
		       topic_id, serving_lane, status, pre_window_start, anchor_at,
		       post_window_end, late_feedback_until, created_at, updated_at
		FROM evaluation_episodes
		WHERE chat_id = ?
		  AND status = ?
		  AND post_window_end IS NULL
		  AND anchor_at < ?
		  AND late_feedback_until >= ?
		ORDER BY anchor_at, id`,
		chatID, string(conversationeval.EpisodeStatusCollecting), at, at,
	).Scan(&rows).Error; err != nil {
		return nil, err
	}
	episodes := make([]conversationeval.Episode, 0, len(rows))
	for _, row := range rows {
		episode, domainErr := row.domain()
		if domainErr != nil {
			return nil, domainErr
		}
		episodes = append(episodes, episode)
	}
	return episodes, nil
}

func (r *Repository) ApplyPostWindowObservation(
	ctx context.Context,
	episodeID string,
	message conversationeval.WindowMessage,
	topicBoundary bool,
) (conversationeval.PostWindowMutation, error) {
	if err := validateQueryID("episode_id", episodeID); err != nil {
		return conversationeval.PostWindowMutation{}, err
	}
	db, err := r.database()
	if err != nil {
		return conversationeval.PostWindowMutation{}, err
	}
	var mutation conversationeval.PostWindowMutation
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		episode, err := loadEpisodeForUpdate(tx, episodeID)
		if err != nil {
			return err
		}
		if episode.PostWindowEnd != nil {
			return conversationeval.ErrInvalidTransition
		}
		window, err := loadPostWindow(tx, episode)
		if err != nil {
			return err
		}
		added, err := window.Append(message, topicBoundary)
		if err != nil {
			return err
		}
		mutation.Added = added
		if added {
			stored := window.Messages[len(window.Messages)-1]
			if err := validateWindowMessageForEpisode(stored, episode); err != nil {
				return err
			}
			if err := insertWindowMessage(tx, episode.ID, stored); err != nil {
				return err
			}
		}
		if window.ClosedAt == nil {
			return nil
		}
		result := tx.Exec(`
			UPDATE evaluation_episodes
			SET post_window_end = ?, post_window_reason = ?, updated_at = ?
			WHERE id = ? AND post_window_end IS NULL`,
			*window.ClosedAt, string(window.CloseReason), *window.ClosedAt, episode.ID,
		)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return conversationeval.ErrInvalidTransition
		}
		ready, err := markReadyIfComplete(tx, episode.ID, *window.ClosedAt)
		if err != nil {
			return err
		}
		mutation.Closed = true
		closedAt := *window.ClosedAt
		mutation.ClosedAt = &closedAt
		mutation.CloseReason = window.CloseReason
		mutation.Ready = ready
		return nil
	})
	return mutation, err
}

func (r *Repository) CloseExpiredPostWindows(
	ctx context.Context,
	chatID string,
	now time.Time,
) (int, error) {
	if err := validateQueryID("chat_id", chatID); err != nil {
		return 0, err
	}
	if now.IsZero() {
		return 0, fmt.Errorf("%w: window sweep timestamp must not be zero", conversationeval.ErrInvalidContract)
	}
	db, err := r.database()
	if err != nil {
		return 0, err
	}
	var candidates []struct {
		ID       string
		AnchorAt time.Time
	}
	if err := db.WithContext(ctx).Raw(`
		SELECT id, anchor_at
		FROM evaluation_episodes
		WHERE chat_id = ?
		  AND status = ?
		  AND post_window_end IS NULL
		  AND anchor_at + (? * interval '1 second') <= ?
		ORDER BY anchor_at, id`,
		chatID, string(conversationeval.EpisodeStatusCollecting),
		int64(conversationeval.PostWindowMaxAge/time.Second), now,
	).Scan(&candidates).Error; err != nil {
		return 0, err
	}
	closed := 0
	for _, candidate := range candidates {
		deadline := candidate.AnchorAt.Add(conversationeval.PostWindowMaxAge)
		_, applyErr := r.ApplyPostWindowObservation(
			ctx,
			candidate.ID,
			conversationeval.WindowMessage{OccurredAt: deadline},
			false,
		)
		if applyErr != nil {
			if applyErr == conversationeval.ErrInvalidTransition {
				continue
			}
			return closed, applyErr
		}
		closed++
	}
	return closed, nil
}

func (r *Repository) MarkReadyIfComplete(
	ctx context.Context,
	episodeID string,
	at time.Time,
) (bool, error) {
	if err := validateQueryID("episode_id", episodeID); err != nil {
		return false, err
	}
	if at.IsZero() {
		return false, fmt.Errorf("%w: readiness timestamp must not be zero", conversationeval.ErrInvalidContract)
	}
	db, err := r.database()
	if err != nil {
		return false, err
	}
	var ready bool
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var err error
		ready, err = markReadyIfComplete(tx, episodeID, at)
		return err
	})
	return ready, err
}

func markReadyIfComplete(tx *gorm.DB, episodeID string, at time.Time) (bool, error) {
	var state struct {
		Status        string
		PostWindowEnd *time.Time
		LaneCount     int64
	}
	result := tx.Raw(`
		SELECT e.status, e.post_window_end,
		       (
		           SELECT count(DISTINCT lane)
		           FROM evaluation_lane_outputs
		           WHERE episode_id = e.id AND lane IN (?, ?)
		       ) AS lane_count
		FROM evaluation_episodes AS e
		WHERE e.id = ?
		FOR UPDATE`,
		string(conversationeval.LaneControl), string(conversationeval.LaneCandidate), episodeID,
	).Scan(&state)
	if result.Error != nil {
		return false, result.Error
	}
	if result.RowsAffected != 1 {
		return false, gorm.ErrRecordNotFound
	}
	if state.Status == string(conversationeval.EpisodeStatusReadyForJudge) ||
		state.Status == string(conversationeval.EpisodeStatusJudged) {
		return true, nil
	}
	if state.PostWindowEnd == nil || state.LaneCount != 2 {
		return false, nil
	}
	update := tx.Exec(`
		UPDATE evaluation_episodes
		SET status = ?, updated_at = ?
		WHERE id = ? AND status = ?`,
		string(conversationeval.EpisodeStatusReadyForJudge), at, episodeID,
		string(conversationeval.EpisodeStatusCollecting),
	)
	if update.Error != nil {
		return false, update.Error
	}
	return update.RowsAffected == 1, nil
}

func loadEpisodeForUpdate(db *gorm.DB, episodeID string) (conversationeval.Episode, error) {
	var row episodeRow
	result := db.Raw(`
		SELECT id, cohort_id, chat_id, run_id, anchor_event_id, anchor_message_id,
		       topic_id, serving_lane, status, pre_window_start, anchor_at,
		       post_window_end, late_feedback_until, created_at, updated_at
		FROM evaluation_episodes
		WHERE id = ?
		LIMIT 1
		FOR UPDATE`,
		episodeID,
	).Scan(&row)
	if result.Error != nil {
		return conversationeval.Episode{}, result.Error
	}
	if result.RowsAffected != 1 {
		return conversationeval.Episode{}, gorm.ErrRecordNotFound
	}
	return row.domain()
}

func loadPostWindow(
	db *gorm.DB,
	episode conversationeval.Episode,
) (*conversationeval.PostWindow, error) {
	window, err := conversationeval.NewPostWindow(episode.AnchorAt, episode.TopicID)
	if err != nil {
		return nil, err
	}
	var rows []struct {
		PayloadJSON string
	}
	if err := db.Raw(`
		SELECT payload_json::text AS payload_json
		FROM evaluation_episode_messages
		WHERE episode_id = ? AND position = ?
		ORDER BY occurred_at, event_id`,
		episode.ID, string(conversationeval.WindowPositionPost),
	).Scan(&rows).Error; err != nil {
		return nil, err
	}
	window.Messages = make([]conversationeval.WindowMessage, 0, len(rows))
	for index, row := range rows {
		var message conversationeval.WindowMessage
		if err := json.Unmarshal([]byte(row.PayloadJSON), &message); err != nil {
			return nil, fmt.Errorf("decode post-window message: %w", err)
		}
		if err := validateWindowMessageForEpisode(message, episode); err != nil {
			return nil, err
		}
		message.Position = conversationeval.WindowPositionPost
		message.Sequence = index
		window.Messages = append(window.Messages, message)
	}
	return window, nil
}

func insertWindowMessage(
	tx *gorm.DB,
	episodeID string,
	message conversationeval.WindowMessage,
) error {
	payload, err := json.Marshal(message)
	if err != nil {
		return fmt.Errorf("marshal evaluation window message: %w", err)
	}
	id := windowMessageID(episodeID, message.Position, message.EventID)
	result := tx.Exec(`
		INSERT INTO evaluation_episode_messages (
			id, episode_id, position, event_id, message_id, sequence, occurred_at, payload_json
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?::jsonb)
		ON CONFLICT (episode_id, position, event_id) DO NOTHING`,
		id, episodeID, string(message.Position), message.EventID, message.MessageID,
		message.Sequence, message.OccurredAt, string(payload),
	)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 1 {
		return nil
	}
	var stored struct {
		ID          string
		MessageID   string
		Sequence    int
		OccurredAt  time.Time
		PayloadJSON string
	}
	load := tx.Raw(`
		SELECT id, message_id, sequence, occurred_at, payload_json::text AS payload_json
		FROM evaluation_episode_messages
		WHERE episode_id = ? AND position = ? AND event_id = ?
		LIMIT 1`,
		episodeID, string(message.Position), message.EventID,
	).Scan(&stored)
	if load.Error != nil {
		return load.Error
	}
	if load.RowsAffected != 1 {
		return gorm.ErrRecordNotFound
	}
	if stored.ID != id || stored.MessageID != message.MessageID ||
		stored.Sequence != message.Sequence ||
		stored.OccurredAt.UnixMicro() != message.OccurredAt.UnixMicro() ||
		!semanticJSONEqual([]byte(stored.PayloadJSON), payload) {
		return fmt.Errorf(
			"%w: window message replay conflicts with canonical event %q",
			conversationeval.ErrInvalidContract,
			message.EventID,
		)
	}
	return nil
}

func validateWindowMessageForEpisode(
	message conversationeval.WindowMessage,
	episode conversationeval.Episode,
) error {
	if err := message.Validate(); err != nil {
		return err
	}
	if message.ChatID != episode.ChatID {
		return fmt.Errorf(
			"%w: window message chat does not match episode",
			conversationeval.ErrInvalidContract,
		)
	}
	return nil
}

func windowMessageID(
	episodeID string,
	position conversationeval.WindowPosition,
	eventID string,
) string {
	sum := sha256.Sum256([]byte(episodeID + "\x00" + string(position) + "\x00" + eventID))
	return "eval_msg_" + hex.EncodeToString(sum[:16])
}
