package evaluationstore

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/conversationeval"
	"gorm.io/gorm"
)

var _ conversationeval.CandidateTaskQueue = (*Repository)(nil)

func (r *Repository) SubmitCandidate(
	ctx context.Context,
	task conversationeval.CandidateTask,
) error {
	if err := task.Validate(); err != nil {
		return err
	}
	payload, err := json.Marshal(task)
	if err != nil {
		return fmt.Errorf("marshal candidate task: %w", err)
	}
	db, err := r.database()
	if err != nil {
		return err
	}
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		episode, err := loadEpisode(tx, task.Episode.ID, true)
		if err != nil {
			return err
		}
		if episode.CohortID != task.Cohort.ID ||
			episode.AnchorEventID != task.Message.EventID {
			return fmt.Errorf(
				"%w: candidate task does not own stored episode",
				conversationeval.ErrInvalidContract,
			)
		}
		result := tx.Exec(`
			INSERT INTO evaluation_candidate_tasks (
				id, episode_id, status, payload_json, next_attempt_at
			) VALUES (?, ?, ?, ?::jsonb, ?)
			ON CONFLICT (episode_id) DO NOTHING`,
			task.ID, episode.ID, string(conversationeval.CandidateTaskQueued),
			string(payload), task.CreatedAt,
		)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 1 {
			return nil
		}
		var stored struct {
			ID          string
			PayloadJSON string
		}
		load := tx.Raw(`
			SELECT id, payload_json::text AS payload_json
			FROM evaluation_candidate_tasks
			WHERE episode_id = ?
			LIMIT 1`,
			episode.ID,
		).Scan(&stored)
		if load.Error != nil {
			return load.Error
		}
		if load.RowsAffected != 1 {
			return gorm.ErrRecordNotFound
		}
		if stored.ID != task.ID || !semanticJSONEqual([]byte(stored.PayloadJSON), payload) {
			return fmt.Errorf(
				"%w: candidate task replay conflicts with episode %q",
				conversationeval.ErrInvalidContract,
				episode.ID,
			)
		}
		return nil
	})
}

func (r *Repository) ClaimCandidate(
	ctx context.Context,
	claim conversationeval.CandidateTaskClaim,
) (*conversationeval.CandidateTaskLease, error) {
	if err := claim.Validate(); err != nil {
		return nil, err
	}
	db, err := r.database()
	if err != nil {
		return nil, err
	}
	var row candidateTaskRow
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Raw(`
			SELECT id, episode_id, status, payload_json::text AS payload_json,
			       attempt_count, next_attempt_at, worker_id, lease_expires_at,
			       last_error, created_at, updated_at
			FROM evaluation_candidate_tasks
			WHERE (
				status = ? AND next_attempt_at <= ?
			) OR (
				status = ? AND lease_expires_at <= ?
			)
			ORDER BY next_attempt_at, id
			FOR UPDATE SKIP LOCKED
			LIMIT 1`,
			string(conversationeval.CandidateTaskQueued), claim.Now,
			string(conversationeval.CandidateTaskRunning), claim.Now,
		).Scan(&row)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return conversationeval.ErrCandidateTaskNotFound
		}
		leaseExpiresAt := claim.Now.Add(claim.LeaseTTL)
		update := tx.Exec(`
			UPDATE evaluation_candidate_tasks
			SET status = ?, attempt_count = attempt_count + 1, worker_id = ?,
			    lease_expires_at = ?, updated_at = ?
			WHERE id = ? AND status = ?`,
			string(conversationeval.CandidateTaskRunning), claim.WorkerID,
			leaseExpiresAt, claim.Now, row.ID, row.Status,
		)
		if update.Error != nil {
			return update.Error
		}
		if update.RowsAffected != 1 {
			return conversationeval.ErrCandidateTaskLeaseLost
		}
		row.Status = string(conversationeval.CandidateTaskRunning)
		row.AttemptCount++
		row.WorkerID = claim.WorkerID
		row.LeaseExpiresAt = &leaseExpiresAt
		return nil
	})
	if err != nil {
		return nil, err
	}
	return row.domain()
}

func (r *Repository) CompleteCandidateTask(
	ctx context.Context,
	request conversationeval.CompleteCandidateTaskRequest,
) error {
	if err := request.Validate(); err != nil {
		return err
	}
	db, err := r.database()
	if err != nil {
		return err
	}
	result := db.WithContext(ctx).Exec(`
		UPDATE evaluation_candidate_tasks
		SET status = ?, worker_id = '', lease_expires_at = NULL,
		    last_error = '', updated_at = ?
		WHERE id = ? AND status = ? AND worker_id = ? AND attempt_count = ?
		  AND lease_expires_at > ?`,
		string(conversationeval.CandidateTaskCompleted), request.FinishedAt,
		request.TaskID, string(conversationeval.CandidateTaskRunning),
		request.WorkerID, request.AttemptCount, request.FinishedAt,
	)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return conversationeval.ErrCandidateTaskLeaseLost
	}
	return nil
}

func (r *Repository) RetryCandidateTask(
	ctx context.Context,
	request conversationeval.RetryCandidateTaskRequest,
) error {
	if err := request.Validate(); err != nil {
		return err
	}
	db, err := r.database()
	if err != nil {
		return err
	}
	result := db.WithContext(ctx).Exec(`
		UPDATE evaluation_candidate_tasks
		SET status = ?, worker_id = '', lease_expires_at = NULL,
		    last_error = ?, next_attempt_at = ?, updated_at = ?
		WHERE id = ? AND status = ? AND worker_id = ? AND attempt_count = ?
		  AND lease_expires_at > ?`,
		string(conversationeval.CandidateTaskQueued), request.ErrorText,
		request.RetryAt, request.FailedAt, request.TaskID,
		string(conversationeval.CandidateTaskRunning), request.WorkerID,
		request.AttemptCount, request.FailedAt,
	)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return conversationeval.ErrCandidateTaskLeaseLost
	}
	return nil
}

type candidateTaskRow struct {
	ID             string
	EpisodeID      string
	Status         string
	PayloadJSON    string
	AttemptCount   int32
	NextAttemptAt  time.Time
	WorkerID       string
	LeaseExpiresAt *time.Time
	LastError      string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

func (r candidateTaskRow) domain() (*conversationeval.CandidateTaskLease, error) {
	var task conversationeval.CandidateTask
	if err := json.Unmarshal([]byte(r.PayloadJSON), &task); err != nil {
		return nil, fmt.Errorf("decode candidate task %q: %w", r.ID, err)
	}
	if err := task.Validate(); err != nil {
		return nil, fmt.Errorf("stored candidate task %q: %w", r.ID, err)
	}
	if task.ID != r.ID || task.Episode.ID != r.EpisodeID {
		return nil, fmt.Errorf(
			"%w: stored candidate task identity does not match payload",
			conversationeval.ErrInvalidContract,
		)
	}
	status := conversationeval.CandidateTaskStatus(r.Status)
	if status != conversationeval.CandidateTaskQueued &&
		status != conversationeval.CandidateTaskRunning &&
		status != conversationeval.CandidateTaskCompleted {
		return nil, fmt.Errorf(
			"%w: stored candidate task has invalid status %q",
			conversationeval.ErrInvalidContract,
			status,
		)
	}
	lease := &conversationeval.CandidateTaskLease{
		Task: task, Status: status, AttemptCount: r.AttemptCount,
		WorkerID: r.WorkerID,
	}
	if r.LeaseExpiresAt != nil {
		lease.LeaseExpiresAt = *r.LeaseExpiresAt
	}
	return lease, nil
}

func semanticJSONEqual(left, right []byte) bool {
	var leftValue any
	var rightValue any
	leftDecoder := json.NewDecoder(bytes.NewReader(left))
	leftDecoder.UseNumber()
	rightDecoder := json.NewDecoder(bytes.NewReader(right))
	rightDecoder.UseNumber()
	if leftDecoder.Decode(&leftValue) != nil || rightDecoder.Decode(&rightValue) != nil {
		return false
	}
	return reflect.DeepEqual(leftValue, rightValue)
}
