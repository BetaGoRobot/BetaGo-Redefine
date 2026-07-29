package conversationeval

import (
	"context"
	"time"
)

type Store interface {
	CreateCohort(context.Context, Cohort) error
	ActiveCohorts(context.Context, string, time.Time) ([]Cohort, error)
	GetOrCreateEpisode(context.Context, Episode) (*Episode, error)
	UpsertLaneOutput(context.Context, LaneOutput) error
	AppendFeedback(context.Context, Feedback) error
	AppendJudgment(context.Context, Judgment) error
	EpisodesReadyForJudge(context.Context, time.Time, int) ([]Episode, error)
	TransitionCohorts(context.Context, time.Time) (int64, error)
}
