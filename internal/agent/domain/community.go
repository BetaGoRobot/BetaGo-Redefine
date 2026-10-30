package domain

import "time"

// Community DTOs contain provider facts, never caller-supplied authority.
type GroupInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	OwnerID     string `json:"owner_id,omitempty"`
	Mode        string `json:"mode,omitempty"`
	UserCount   string `json:"user_count,omitempty"`
	BotCount    string `json:"bot_count,omitempty"`
}
type GroupMember struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	IDType string `json:"id_type"`
}
type GroupMembers struct {
	Members         []GroupMember `json:"members"`
	HasMore         bool          `json:"has_more"`
	PageToken       string        `json:"page_token,omitempty"`
	Total           int           `json:"total"`
	SecurityLimited bool          `json:"security_limited"`
}
type Reaction struct {
	ID           string `json:"reaction_id"`
	Emoji        string `json:"emoji"`
	OperatorID   string `json:"operator_id"`
	OperatorType string `json:"operator_type"`
	Own          bool   `json:"own"`
	ActionTime   string `json:"action_time,omitempty"`
}
type ReactionPage struct {
	Reactions []Reaction `json:"reactions"`
	HasMore   bool       `json:"has_more"`
	PageToken string     `json:"page_token,omitempty"`
}

// HistoryCursor is a keyset position. Time is exact to the nanosecond and IDs
// break ties. Query scope and filters always remain independent SQL predicates.
type HistoryCursor struct {
	Time      time.Time `json:"time"`
	MessageID string    `json:"message_id"`
}
type HistoryQuery struct {
	Scope                 Scope
	Anchor, After, Before time.Time
	AuthorID              string
	Limit                 int
	Cursor                *HistoryCursor
}
type HistoryPage struct {
	Messages []Message
	HasMore  bool
	Next     *HistoryCursor
}
