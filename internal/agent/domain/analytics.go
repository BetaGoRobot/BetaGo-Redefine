package domain

import "time"

type AnalyticsQuery struct {
	Scope              Scope
	Start, End, Anchor time.Time
	AuthorID           string
	Limit              int
}
type CountEntry struct {
	Value string `json:"value"`
	Count int64  `json:"count"`
}
type MessageStatistics struct {
	Start            time.Time    `json:"start"`
	End              time.Time    `json:"end"`
	AsOf             time.Time    `json:"as_of"`
	Coverage         string       `json:"coverage"`
	Total            int64        `json:"total"`
	Authors          []CountEntry `json:"authors"`
	AuthorsTruncated bool         `json:"authors_truncated"`
	Words            []CountEntry `json:"words"`
	Tokenization     string       `json:"tokenization"`
	WordMessages     int          `json:"word_messages"`
	WordsComplete    bool         `json:"words_complete"`
	Sources          []SourceRef  `json:"-"`
}
