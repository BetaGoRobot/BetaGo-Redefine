package evaluationstore

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/conversationeval"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/evaluationindex"
	"gorm.io/gorm"
)

var _ evaluationindex.ProjectionSource = (*Repository)(nil)

func (r *Repository) EvaluationSnapshotsAfter(
	ctx context.Context,
	cursor evaluationindex.ProjectionCursor,
	limit int,
) ([]evaluationindex.EvaluationSnapshot, error) {
	if limit <= 0 || limit > 1000 {
		return nil, fmt.Errorf("evaluation projection limit must be between 1 and 1000")
	}
	db, err := r.database()
	if err != nil {
		return nil, err
	}
	var rows []struct {
		ID string
	}
	query := `
		SELECT id
		FROM evaluation_episodes
		WHERE status = ? AND tenant_id = ?`
	args := []any{string(conversationeval.EpisodeStatusJudged), r.tenant.ID}
	if !cursor.UpdatedAt.IsZero() {
		query += `
		  AND (updated_at > ? OR (updated_at = ? AND id > ?))`
		args = append(args, cursor.UpdatedAt, cursor.UpdatedAt, cursor.EpisodeID)
	}
	query += `
		ORDER BY updated_at, id
		LIMIT ?`
	args = append(args, limit)
	if err := db.WithContext(ctx).Raw(query, args...).Scan(&rows).Error; err != nil {
		return nil, err
	}
	snapshots := make([]evaluationindex.EvaluationSnapshot, 0, len(rows))
	for _, row := range rows {
		snapshot, loadErr := loadEvaluationSnapshot(
			db.WithContext(ctx), r.tenant.ID, row.ID,
		)
		if loadErr != nil {
			return nil, loadErr
		}
		snapshots = append(snapshots, snapshot)
	}
	return snapshots, nil
}

func loadEvaluationSnapshot(
	db *gorm.DB,
	tenantID string,
	episodeID string,
) (evaluationindex.EvaluationSnapshot, error) {
	input, err := loadJudgeInput(db, tenantID, episodeID)
	if err != nil {
		return evaluationindex.EvaluationSnapshot{}, err
	}
	var judgmentRows []projectionJudgmentRow
	if err := db.Raw(`
		SELECT DISTINCT ON (source)
		       id, tenant_id, episode_id, version, source, evaluator_id, winner,
		       scores_json::text AS scores_json,
		       problem_tags_json::text AS problem_tags_json,
		       rationale, confidence, needs_review, supersedes_id, created_at
		FROM evaluation_judgments
		WHERE episode_id = ? AND tenant_id = ?
		ORDER BY source, version DESC`,
		episodeID, tenantID,
	).Scan(&judgmentRows).Error; err != nil {
		return evaluationindex.EvaluationSnapshot{}, err
	}
	judgments := make([]conversationeval.Judgment, 0, len(judgmentRows))
	latest := make([]evaluationindex.JudgmentSnapshot, 0, len(judgmentRows))
	needsReview := false
	for _, row := range judgmentRows {
		judgment, decodeErr := row.domain()
		if decodeErr != nil {
			return evaluationindex.EvaluationSnapshot{}, decodeErr
		}
		judgments = append(judgments, judgment)
		latest = append(latest, evaluationindex.JudgmentSnapshot{
			Source: string(judgment.Source), Version: judgment.Version,
			Winner: string(judgment.Winner), Rationale: judgment.Rationale,
			Confidence: judgment.Confidence, NeedsReview: judgment.NeedsReview,
			ProblemTags: append([]string(nil), judgment.ProblemTags...),
			CreatedAt:   judgment.CreatedAt,
		})
		needsReview = needsReview || judgment.NeedsReview
	}
	sort.Slice(latest, func(i, j int) bool {
		return latest[i].Source < latest[j].Source
	})

	pre := make([]evaluationindex.MessageSnapshot, 0)
	post := make([]evaluationindex.MessageSnapshot, 0)
	var anchor evaluationindex.MessageSnapshot
	for _, message := range input.Messages {
		snapshot := evaluationindex.MessageSnapshot{
			MessageID: message.MessageID, SenderOpenID: message.SenderOpenID,
			ReplyToMessageID: message.ReplyToMessageID, Content: message.Content,
			OccurredAt: message.OccurredAt,
		}
		switch message.Position {
		case conversationeval.WindowPositionPre:
			pre = append(pre, snapshot)
		case conversationeval.WindowPositionAnchor:
			anchor = snapshot
		case conversationeval.WindowPositionPost:
			post = append(post, snapshot)
		}
	}
	feedbackTypes := uniqueFeedbackTypes(input.Feedback)
	fullSnapshot, err := json.Marshal(struct {
		Episode   conversationeval.Episode         `json:"episode"`
		Messages  []conversationeval.WindowMessage `json:"messages"`
		Control   conversationeval.LaneOutput      `json:"control"`
		Candidate conversationeval.LaneOutput      `json:"candidate"`
		Feedback  []conversationeval.Feedback      `json:"feedback"`
		Judgments []conversationeval.Judgment      `json:"latest_judgments"`
	}{
		Episode: input.Episode, Messages: input.Messages,
		Control: input.ControlOutput, Candidate: input.CandidateOutput,
		Feedback: input.Feedback, Judgments: judgments,
	})
	if err != nil {
		return evaluationindex.EvaluationSnapshot{}, fmt.Errorf(
			"marshal evaluation snapshot %q: %w",
			episodeID,
			err,
		)
	}
	snapshot := evaluationindex.EvaluationSnapshot{
		EpisodeID: input.Episode.ID, CohortID: input.Episode.CohortID,
		ChatID: input.Episode.ChatID, RunID: input.Episode.RunID,
		AnchorEventID:   input.Episode.AnchorEventID,
		AnchorMessageID: input.Episode.AnchorMessageID, TopicID: input.Episode.TopicID,
		Status: string(input.Episode.Status), ServingLane: string(input.Episode.ServingLane),
		AnchorAt: input.Episode.AnchorAt, PostWindowEnd: input.Episode.PostWindowEnd,
		LateFeedbackUntil: input.Episode.LateFeedbackUntil,
		Disagreements:     laneDisagreements(input.ControlOutput, input.CandidateOutput),
		FeedbackTypes:     feedbackTypes, NeedsReview: needsReview,
		PreMessages: pre, AnchorMessage: anchor, PostMessages: post,
		Control:         laneSnapshot(input.ControlOutput),
		Candidate:       laneSnapshot(input.CandidateOutput),
		LatestJudgments: latest, FullSnapshot: fullSnapshot,
		UpdatedAt: input.Episode.UpdatedAt,
	}
	return snapshot, nil
}

func laneSnapshot(output conversationeval.LaneOutput) evaluationindex.LaneSnapshot {
	selected := make([]string, 0)
	for _, items := range [][]conversationeval.ContextItem{
		output.ContextSnapshot.Messages,
		output.ContextSnapshot.Retrieved,
		output.ContextSnapshot.Events,
	} {
		for _, item := range items {
			if item.Selected {
				selected = append(selected, item.Content)
			}
		}
	}
	excluded := make([]string, 0, len(output.ExcludedContext))
	for _, item := range output.ExcludedContext {
		excluded = append(excluded, item.Content)
	}
	var errorObject map[string]any
	_ = json.Unmarshal(output.ErrorJSON, &errorObject)
	return evaluationindex.LaneSnapshot{
		JoinDecision:  string(output.JoinDecision),
		TopicRelation: string(output.TopicRelation),
		ReplyText:     output.ReplyText, ContextText: selected, ExcludedText: excluded,
		HasError: len(errorObject) > 0,
	}
}

func laneDisagreements(
	control conversationeval.LaneOutput,
	candidate conversationeval.LaneOutput,
) []string {
	values := make([]string, 0, 4)
	if control.JoinDecision != candidate.JoinDecision {
		values = append(values, "join_decision")
	}
	if control.TopicRelation != candidate.TopicRelation {
		values = append(values, "topic_relation")
	}
	if control.ReplyText != candidate.ReplyText {
		values = append(values, "reply")
	}
	if !sameContextIdentity(control, candidate) {
		values = append(values, "context")
	}
	return values
}

func sameContextIdentity(a, b conversationeval.LaneOutput) bool {
	identities := func(output conversationeval.LaneOutput) []string {
		values := make([]string, 0)
		for _, items := range [][]conversationeval.ContextItem{
			output.ContextSnapshot.Messages,
			output.ContextSnapshot.Retrieved,
			output.ContextSnapshot.Events,
		} {
			for _, item := range items {
				if item.Selected {
					values = append(values, item.Source+"\x00"+item.SourceID)
				}
			}
		}
		sort.Strings(values)
		return values
	}
	aValues := identities(a)
	bValues := identities(b)
	if len(aValues) != len(bValues) {
		return false
	}
	for index := range aValues {
		if aValues[index] != bValues[index] {
			return false
		}
	}
	return true
}

func uniqueFeedbackTypes(feedback []conversationeval.Feedback) []string {
	seen := make(map[string]struct{}, len(feedback))
	values := make([]string, 0, len(feedback))
	for _, item := range feedback {
		value := string(item.FeedbackType)
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		values = append(values, value)
	}
	sort.Strings(values)
	return values
}

type projectionJudgmentRow struct {
	ID              string
	TenantID        string
	EpisodeID       string
	Version         int64
	Source          string
	EvaluatorID     string
	Winner          string
	ScoresJSON      string
	ProblemTagsJSON string
	Rationale       string
	Confidence      int
	NeedsReview     bool
	SupersedesID    string
	CreatedAt       time.Time
}

func (r projectionJudgmentRow) domain() (conversationeval.Judgment, error) {
	var problemTags []string
	if err := json.Unmarshal([]byte(r.ProblemTagsJSON), &problemTags); err != nil {
		return conversationeval.Judgment{}, fmt.Errorf(
			"decode judgment %q problem tags: %w",
			r.ID,
			err,
		)
	}
	judgment := conversationeval.Judgment{
		ID: r.ID, TenantID: r.TenantID,
		EpisodeID: r.EpisodeID, Version: r.Version,
		Source: conversationeval.JudgmentSource(r.Source), EvaluatorID: r.EvaluatorID,
		Winner:     conversationeval.JudgmentWinner(r.Winner),
		ScoresJSON: json.RawMessage(r.ScoresJSON), ProblemTags: problemTags,
		Rationale: r.Rationale, Confidence: r.Confidence, NeedsReview: r.NeedsReview,
		SupersedesID: r.SupersedesID, CreatedAt: r.CreatedAt,
	}
	if err := judgment.Validate(); err != nil {
		return conversationeval.Judgment{}, fmt.Errorf(
			"stored judgment %q: %w",
			r.ID,
			err,
		)
	}
	return judgment, nil
}
