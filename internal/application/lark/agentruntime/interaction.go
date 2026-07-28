package agentruntime

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"maps"
	"reflect"
	"strings"
)

type StartScheduleEditRequest struct {
	TaskID          string
	ActorOpenID     string
	ChatID          string
	SourceMessageID string
	NewValues       map[string]any
}

func (r StartScheduleEditRequest) Clone() StartScheduleEditRequest {
	r.NewValues = maps.Clone(r.NewValues)
	return r
}

type InteractionStarter interface {
	StartScheduleEdit(context.Context, StartScheduleEditRequest) (*RuntimeEnvelope, error)
}

type interactionStarterContextKey struct{}

type interactionStarterContextValue struct {
	starter InteractionStarter
}

func WithInteractionStarter(ctx context.Context, starter InteractionStarter) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, interactionStarterContextKey{}, interactionStarterContextValue{
		starter: starter,
	})
}

func InteractionStarterFromContext(ctx context.Context) (InteractionStarter, bool) {
	if ctx == nil {
		return nil, false
	}
	value, ok := ctx.Value(interactionStarterContextKey{}).(interactionStarterContextValue)
	if !ok || isNilInteractionStarter(value.starter) {
		return nil, false
	}
	return value.starter, true
}

func isNilInteractionStarter(starter InteractionStarter) bool {
	if starter == nil {
		return true
	}
	value := reflect.ValueOf(starter)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Ptr, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}

func HashInteractionToken(token string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(token)))
	return hex.EncodeToString(sum[:])
}

func MatchInteractionToken(token, hash string) bool {
	if strings.TrimSpace(token) == "" {
		return false
	}
	actual, err := hex.DecodeString(HashInteractionToken(token))
	if err != nil {
		return false
	}
	expected, err := hex.DecodeString(strings.TrimSpace(hash))
	if err != nil || len(expected) != sha256.Size {
		return false
	}
	return subtle.ConstantTimeCompare(actual, expected) == 1
}
