package domain

import "time"

const JobReminder JobKind = "reminder"

// Work items are authoritative group-visible state, not model memory. Creator
// identity and origin always come from a running server-owned tool execution.
type Todo struct {
	ID, CreatedBy, Title, Description, Status string
	Priority, AssigneeID                      string
	DueAt                                     *time.Time
	Tags                                      []string
	Scope                                     Scope
	Origin                                    SourceRef
	CreatedAt, UpdatedAt                      time.Time
}

type TodoInput struct {
	Title       string     `json:"title"`
	Description string     `json:"description,omitempty"`
	Priority    string     `json:"priority,omitempty"`
	DueAt       *time.Time `json:"due_at,omitempty"`
	Tags        []string   `json:"tags,omitempty"`
	AssigneeID  string     `json:"assignee_id,omitempty"`
}
type TodoChange struct {
	Title       *string    `json:"title,omitempty"`
	Description *string    `json:"description,omitempty"`
	Status      string     `json:"status,omitempty"`
	Priority    *string    `json:"priority,omitempty"`
	DueAt       *time.Time `json:"due_at,omitempty"`
	ClearDueAt  bool       `json:"clear_due_at,omitempty"`
	Tags        *[]string  `json:"tags,omitempty"`
	AddTags     []string   `json:"add_tags,omitempty"`
	RemoveTags  []string   `json:"remove_tags,omitempty"`
	AssigneeID  *string    `json:"assignee_id,omitempty"`
}

type TodoQuery struct {
	Status     string     `json:"status,omitempty"`
	Priority   string     `json:"priority,omitempty"`
	CreatorID  string     `json:"creator_id,omitempty"`
	AssigneeID string     `json:"assignee_id,omitempty"`
	Unassigned bool       `json:"unassigned,omitempty"`
	Tags       []string   `json:"tags,omitempty"`
	DueBefore  *time.Time `json:"due_before,omitempty"`
	DueAfter   *time.Time `json:"due_after,omitempty"`
	Overdue    bool       `json:"overdue,omitempty"`
	Limit      int        `json:"limit,omitempty"`
	Cursor     string     `json:"cursor,omitempty"`
}
type TodoPage struct {
	Items      []Todo `json:"items"`
	NextCursor string `json:"next_cursor,omitempty"`
}
type TodoDeleteInput struct {
	ID string `json:"id"`
}

type Reminder struct {
	ID, CreatedBy, Text, Status, UUID, MessageID string
	Scope                                        Scope
	Origin                                       SourceRef
	Sources                                      []SourceRef
	DueAt, NotifyUntil, CreatedAt, UpdatedAt     time.Time
	SendStartedAt, SentAt                        time.Time
}

type ReminderInput struct {
	Text  string    `json:"text"`
	DueAt time.Time `json:"due_at"`
}

type TodoUpdateInput struct {
	ID string `json:"id"`
	TodoChange
}
type ReminderCancelInput struct {
	ID string `json:"id"`
}
