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
	// TransitionCohorts returns the number of state transitions. A collecting
	// cohort recovered after its late-feedback deadline counts twice when one
	// sweep advances it through waiting_late_feedback to finalized.
	TransitionCohorts(context.Context, time.Time) (int64, error)
}
