// Package larkeventstore preserves event provenance independently of message
// visibility, embeddings and search indexing. It never stores message content.
package larkeventstore

import (
	"context"
	"time"
)

type Scope struct{ AppID, BotOpenID string }

func (s Scope) Valid() bool { return s.AppID != "" && s.BotOpenID != "" }

type Event struct {
	AppID            string `gorm:"primaryKey"`
	BotOpenID        string `gorm:"primaryKey"`
	EventID          string `gorm:"primaryKey"`
	EventType        string
	MessageID        string
	OperatorType     string
	ActorOpenID      string
	ActorUserID      string
	ActorAppID       string
	ReactionType     string
	ActionTime       string
	OccurredAt       *time.Time
	ReceivedAt       time.Time
	TraceID          string
	ChatID           string
	Status           string
	ResolutionSource string
	ErrorCode        int
	Error            string
	Payload          string `gorm:"type:jsonb"`
}

func (Event) TableName() string { return "lark_event_audits" }
func (e Event) Scope() Scope    { return Scope{e.AppID, e.BotOpenID} }

type Location struct {
	AppID      string `gorm:"primaryKey"`
	BotOpenID  string `gorm:"primaryKey"`
	MessageID  string `gorm:"primaryKey"`
	ChatID     string
	Recalled   bool
	ObservedAt time.Time
	LastSeenAt time.Time
}

func (Location) TableName() string { return "lark_message_locations" }

type Resolution struct {
	ChatID    string
	Source    string
	Status    string
	ErrorCode int
	Error     string
}

type Store interface {
	SaveEvent(context.Context, Event) error
	UpdateResolution(context.Context, Scope, string, Resolution) error
	PutLocation(context.Context, Location) error
	FindLocation(context.Context, Scope, string) (Location, bool, error)
}
