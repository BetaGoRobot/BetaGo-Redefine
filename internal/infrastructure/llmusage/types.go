package llmusage

import (
	"strings"
	"time"
	"unicode/utf8"
)

const labelMaxLen = 64

type SourceType string

const (
	SourceTypeUser       SourceType = "user"
	SourceTypeBackground SourceType = "background"
	SourceTypeSystem     SourceType = "system"
	SourceTypeDebug      SourceType = "debug"
)

type Status string

const (
	StatusSuccess      Status = "success"
	StatusError        Status = "error"
	StatusUsageMissing Status = "usage_missing"
)

type Kind string

const (
	KindResponses       Kind = "responses"
	KindResponsesStream Kind = "responses_stream"
	KindEmbedding       Kind = "embedding"
)

type Scope struct {
	ChatID     string
	ChatName   string
	OpenID     string
	UserName   string
	SourceType SourceType
	Source     string
}

type Record struct {
	Scope            Scope
	Provider         string
	Model            string
	Kind             Kind
	Status           Status
	PromptTokens     int64
	CompletionTokens int64
	TotalTokens      int64
	ResponseID       string
	TraceID          string
	Error            string
	CreatedAt        time.Time
}

type Buckets struct {
	Minute time.Time
	Hour   time.Time
	Day    time.Time
}

func NormalizeScope(scope Scope) Scope {
	scope.ChatID = strings.TrimSpace(scope.ChatID)
	scope.ChatName = strings.TrimSpace(scope.ChatName)
	scope.OpenID = strings.TrimSpace(scope.OpenID)
	scope.UserName = strings.TrimSpace(scope.UserName)
	scope.Source = strings.TrimSpace(scope.Source)
	scope.SourceType = SourceType(strings.TrimSpace(string(scope.SourceType)))
	if scope.Source == "" {
		scope.Source = "unknown"
	}
	switch scope.SourceType {
	case SourceTypeUser, SourceTypeBackground, SourceTypeSystem, SourceTypeDebug:
	default:
		scope.SourceType = SourceTypeSystem
	}
	if scope.ChatName == "" {
		scope.ChatName = fallbackChatName(scope)
	}
	return scope
}

func fallbackChatName(scope Scope) string {
	if scope.ChatID != "" {
		return scope.ChatID
	}
	if scope.Source != "" && scope.SourceType != "" {
		return string(scope.SourceType) + ":" + scope.Source
	}
	if scope.Source != "" {
		return scope.Source
	}
	return "unknown"
}

func BucketTimes(createdAt time.Time) Buckets {
	if createdAt.IsZero() {
		createdAt = time.Now()
	}
	return Buckets{
		Minute: createdAt.Truncate(time.Minute),
		Hour:   createdAt.Truncate(time.Hour),
		Day:    time.Date(createdAt.Year(), createdAt.Month(), createdAt.Day(), 0, 0, 0, 0, createdAt.Location()),
	}
}

func sanitizeLabel(value string) string {
	value = strings.ToValidUTF8(strings.TrimSpace(value), "�")
	if utf8.RuneCountInString(value) <= labelMaxLen {
		return value
	}
	runes := []rune(value)
	return string(runes[:labelMaxLen]) + "..."
}
