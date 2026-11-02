package larkeventstore

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/config"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	larkevent "github.com/larksuite/oapi-sdk-go/v3/event"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
)

const ioBudget = time.Second

type Recorder struct {
	store     Store
	AllowChat func(context.Context, string) bool
}

func NewRecorder(store Store) *Recorder { return &Recorder{store: store} }

var defaultMu sync.RWMutex
var defaultRecorder = NewRecorder(nil)

func SetDefault(r *Recorder) {
	defaultMu.Lock()
	defer defaultMu.Unlock()
	if r == nil {
		r = NewRecorder(nil)
	}
	defaultRecorder = r
}
func Default() *Recorder { defaultMu.RLock(); defer defaultMu.RUnlock(); return defaultRecorder }
func CurrentScope() Scope {
	c := config.Get()
	if c == nil || c.LarkConfig == nil {
		return Scope{}
	}
	return Scope{strings.TrimSpace(c.LarkConfig.AppID), strings.TrimSpace(c.LarkConfig.BotOpenID)}
}

func AnnotateReaction(ctx context.Context, event *larkim.P2MessageReactionCreatedV1) {
	e := reactionEvent(ctx, CurrentScope(), event)
	trace.SpanFromContext(ctx).SetAttributes(e.Attributes()...)
	logs.L().Ctx(ctx).Info("reaction event context", e.Fields()...)
}

func (r *Recorder) CaptureReaction(ctx context.Context, scope Scope, event *larkim.P2MessageReactionCreatedV1) Event {
	e := reactionEvent(ctx, scope, event)
	if e.EventID == "" {
		return e
	}
	r.capture(ctx, e)
	if e.ChatID != "" {
		r.ObserveMessage(ctx, scope, e.MessageID, e.ChatID, false)
	}
	return e
}

func (r *Recorder) CaptureRecall(ctx context.Context, scope Scope, event *larkim.P2MessageRecalledV1) Event {
	if event == nil || event.Event == nil {
		return Event{}
	}
	d := event.Event
	e := Event{AppID: scope.AppID, BotOpenID: scope.BotOpenID, EventType: "im.message.recalled_v1", MessageID: value(d.MessageId), ChatID: value(d.ChatId), ActionTime: value(d.RecallTime), Status: "recalled"}
	var h *larkevent.EventHeader
	if event.EventV2Base != nil {
		h = event.EventV2Base.Header
	}
	e.Payload, _ = eventPayload(h, d, event.EventReq)
	e = finishEvent(ctx, e, h)
	if e.EventID == "" {
		return e
	}
	r.capture(ctx, e)
	r.ObserveMessage(ctx, scope, e.MessageID, e.ChatID, true)
	return e
}

func (r *Recorder) capture(ctx context.Context, e Event) {
	span := trace.SpanFromContext(ctx)
	span.SetAttributes(e.Attributes()...)
	span.SetAttributes(attribute.String("event.received_at", e.ReceivedAt.Format(time.RFC3339Nano)), attribute.String("event.payload", e.Payload))
	fields := append(e.Fields(), zap.Time("received_at", e.ReceivedAt), zap.String("event_payload", e.Payload))
	logs.L().Ctx(ctx).Info("lark event received", fields...)
	if r == nil || r.store == nil || !e.Scope().Valid() {
		return
	}
	child, cancel := context.WithTimeout(ctx, ioBudget)
	defer cancel()
	if err := r.store.SaveEvent(child, e); err != nil {
		msg, code := safeError(err)
		span.SetAttributes(attribute.Bool("event.persisted", false))
		logs.L().Ctx(ctx).Warn("lark event journal write failed", append(e.Fields(), zap.String("error", msg), zap.Int("error_code", code))...)
	} else {
		span.SetAttributes(attribute.Bool("event.persisted", true))
	}
}

func (r *Recorder) ObserveMessage(ctx context.Context, scope Scope, mid, cid string, recalled bool) {
	if r == nil || r.store == nil || !scope.Valid() || strings.TrimSpace(mid) == "" || strings.TrimSpace(cid) == "" {
		return
	}
	child, cancel := context.WithTimeout(ctx, ioBudget)
	defer cancel()
	if r.AllowChat != nil && !r.AllowChat(child, cid) {
		return
	}
	now := time.Now().UTC()
	if err := r.store.PutLocation(child, Location{AppID: scope.AppID, BotOpenID: scope.BotOpenID, MessageID: mid, ChatID: cid, Recalled: recalled, ObservedAt: now, LastSeenAt: now}); err != nil {
		msg, _ := safeError(err)
		logs.L().Ctx(ctx).Warn("message location write failed", zap.String("message_id", mid), zap.String("chat_id", cid), zap.String("error", msg))
	}
}

func (r *Recorder) Resolve(ctx context.Context, scope Scope, mid string, remote func(context.Context, string) (string, error)) Resolution {
	if r != nil && r.store != nil && scope.Valid() {
		child, cancel := context.WithTimeout(ctx, ioBudget)
		loc, ok, err := r.store.FindLocation(child, scope, mid)
		cancel()
		if err == nil && ok && loc.ChatID != "" {
			return Resolution{ChatID: loc.ChatID, Source: "locator", Status: "resolved"}
		}
		if err != nil {
			msg, _ := safeError(err)
			logs.L().Ctx(ctx).Warn("message location lookup failed", zap.String("message_id", mid), zap.String("error", msg))
		}
	}
	if remote == nil {
		return Resolution{Source: "none", Status: "unresolved", Error: "message lookup unavailable"}
	}
	child, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	cid, err := remote(child, mid)
	if err == nil && cid == "" {
		err = errors.New("message lookup returned no chat id")
	}
	if err != nil {
		msg, code := safeError(err)
		return Resolution{Source: "feishu", Status: "unresolved", Error: msg, ErrorCode: code}
	}
	r.ObserveMessage(ctx, scope, mid, cid, false)
	return Resolution{ChatID: cid, Source: "feishu", Status: "resolved"}
}

func (r *Recorder) RecordResolution(ctx context.Context, scope Scope, eventID string, result Resolution) {
	if result.Error != "" {
		message, code := safeError(errors.New(result.Error))
		result.Error = message
		if result.ErrorCode == 0 {
			result.ErrorCode = code
		}
	}
	span := trace.SpanFromContext(ctx)
	span.SetAttributes(attribute.String("chat.id", result.ChatID), attribute.String("chat.resolution_source", result.Source), attribute.String("event.resolution_status", result.Status), attribute.Int("lark.error_code", result.ErrorCode))
	fields := []zap.Field{zap.String("event_id", eventID), zap.String("chat_id", result.ChatID), zap.String("resolution_source", result.Source), zap.String("resolution_status", result.Status), zap.Int("error_code", result.ErrorCode), zap.String("error", result.Error)}
	if result.Status != "resolved" {
		logs.L().Ctx(ctx).Warn("reaction processing incomplete", fields...)
	} else {
		logs.L().Ctx(ctx).Info("reaction chat resolved", fields...)
	}
	if r == nil || r.store == nil || !scope.Valid() || eventID == "" {
		return
	}
	child, cancel := context.WithTimeout(ctx, ioBudget)
	defer cancel()
	if err := r.store.UpdateResolution(child, scope, eventID, result); err != nil {
		msg, _ := safeError(err)
		logs.L().Ctx(ctx).Warn("reaction journal outcome write failed", zap.String("event_id", eventID), zap.String("error", msg))
	}
}

func ReactionID(ctx context.Context, scope Scope, event *larkim.P2MessageReactionCreatedV1) string {
	return reactionEvent(ctx, scope, event).EventID
}
