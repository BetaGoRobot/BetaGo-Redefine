package domain

import (
	"encoding/json"
	"strings"
	"time"
	"unicode/utf8"
)

const JobSchedule JobKind = "schedule"

// ScheduleInput freezes one text or configured read-query plan. Intervals are elapsed
// seconds anchored at StartAt, not calendar-day/DST cron promises.
type ScheduleInput struct {
	Text            string         `json:"text,omitempty"`
	Query           *ScheduleQuery `json:"query,omitempty"`
	StartAt         time.Time      `json:"start_at"`
	IntervalSeconds int            `json:"interval_seconds"`
	Runs            int            `json:"runs"`
}
type ScheduleQuery struct {
	Name      string          `json:"tool_name"`
	Arguments json.RawMessage `json:"arguments"`
}

func ScheduleQueryNames() []string {
	return []string{"gold_price_get", "stock_zh_a_get", "finance_market_data_get", "finance_news_get", "economy_indicator_get", "stock_hot_search_get", "oneword_get", "music_search", "music_song_detail", "music_album_detail", "music_playlist_detail"}
}
func ScheduleQueryAllowed(name string) bool {
	for _, n := range ScheduleQueryNames() {
		if n == name {
			return true
		}
	}
	return false
}

type ScheduleUpdateInput struct {
	ID              string         `json:"id"`
	Action          string         `json:"action"`
	Text            *string        `json:"text,omitempty"`
	Query           *ScheduleQuery `json:"query,omitempty"`
	StartAt         *time.Time     `json:"start_at,omitempty"`
	IntervalSeconds *int           `json:"interval_seconds,omitempty"`
	Runs            *int           `json:"runs,omitempty"`
}
type Schedule struct {
	ID, CreatedBy, Status    string
	Scope                    Scope
	Origin                   SourceRef
	Sources                  []SourceRef
	Plan                     ScheduleInput
	Revision                 int64
	NextIndex                int
	PolicyName, PolicyDigest string
	QueryPolicyDigest        string
	CreatedAt, UpdatedAt     time.Time
}
type ScheduleRun struct {
	ScheduleID                                string
	Revision                                  int64
	Index                                     int
	Scope                                     Scope
	Origin                                    SourceRef
	Sources                                   []SourceRef
	Text, UUID, Status, MessageID             string
	Query                                     *ScheduleQuery
	MessageType, Content                      string
	FailureReason                             string
	QueryPolicyDigest                         string
	DueAt, NotifyUntil, SendStartedAt, SentAt time.Time
}

func ValidateScheduleInput(in ScheduleInput, now time.Time) error {
	if !utf8.ValidString(in.Text) || len(in.Text) > 4096 || !in.StartAt.After(now) || in.IntervalSeconds < 60 || in.IntervalSeconds > 366*86400 || in.Runs < 1 || in.Runs > 366 {
		return ErrInvalid
	}
	if in.Query == nil {
		if strings.TrimSpace(in.Text) == "" {
			return ErrInvalid
		}
	} else {
		if in.Text != "" || !ScheduleQueryAllowed(in.Query.Name) || len(in.Query.Arguments) > 16384 {
			return ErrInvalid
		}
		if _, err := CanonicalArguments(string(in.Query.Arguments)); err != nil {
			return ErrInvalid
		}
	}
	seconds := int64(in.Runs-1) * int64(in.IntervalSeconds)
	if seconds > 366*86400 {
		return ErrInvalid
	}
	end := in.StartAt.Add(time.Duration(seconds) * time.Second)
	if end.After(now.Add(366 * 24 * time.Hour)) {
		return ErrInvalid
	}
	return nil
}
func NextScheduleIndex(in ScheduleInput, now time.Time, minimum int) int {
	next := minimum
	if !now.Before(in.StartAt) {
		elapsed := int(now.Sub(in.StartAt)/(time.Duration(in.IntervalSeconds)*time.Second)) + 1
		if elapsed > next {
			next = elapsed
		}
	}
	if next > in.Runs {
		next = in.Runs
	}
	return next
}
func (s Schedule) NextAt() time.Time {
	if s.Status != "active" || s.NextIndex >= s.Plan.Runs {
		return time.Time{}
	}
	return s.Plan.StartAt.Add(time.Duration(s.NextIndex) * time.Duration(s.Plan.IntervalSeconds) * time.Second)
}
