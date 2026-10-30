package larkeventstore

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
	"time"

	larkevent "github.com/larksuite/oapi-sdk-go/v3/event"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
)

func value(s *string) string {
	if s == nil {
		return ""
	}
	return strings.TrimSpace(*s)
}

func reactionEvent(ctx context.Context, scope Scope, event *larkim.P2MessageReactionCreatedV1) Event {
	if event == nil || event.Event == nil {
		return Event{}
	}
	d := event.Event
	e := Event{AppID: scope.AppID, BotOpenID: scope.BotOpenID, EventType: "im.message.reaction.created_v1", MessageID: value(d.MessageId), OperatorType: value(d.OperatorType), ActorAppID: value(d.AppId), ActionTime: value(d.ActionTime), Status: "received"}
	if d.UserId != nil {
		e.ActorOpenID = value(d.UserId.OpenId)
		e.ActorUserID = value(d.UserId.UserId)
	}
	if d.ReactionType != nil {
		e.ReactionType = value(d.ReactionType.EmojiType)
	}
	var header *larkevent.EventHeader
	if event.EventV2Base != nil {
		header = event.EventV2Base.Header
	}
	e.Payload, e.ChatID = eventPayload(header, d, event.EventReq)
	return finishEvent(ctx, e, header)
}

func finishEvent(ctx context.Context, e Event, header *larkevent.EventHeader) Event {
	if header != nil {
		e.EventID = strings.TrimSpace(header.EventID)
	}
	if e.EventID == "" && e.MessageID != "" {
		sum := sha256.Sum256([]byte(strings.Join([]string{e.EventType, e.MessageID, e.OperatorType, e.ActorOpenID, e.ActorUserID, e.ActorAppID, e.ReactionType, e.ActionTime}, "\x00")))
		e.EventID = "derived_" + hex.EncodeToString(sum[:])
	}
	if n, err := strconv.ParseInt(e.ActionTime, 10, 64); err == nil && n > 0 && n <= 253402300799999 {
		t := time.UnixMilli(n).UTC()
		e.OccurredAt = &t
	}
	e.ReceivedAt = time.Now().UTC()
	sc := trace.SpanContextFromContext(ctx)
	if sc.IsValid() {
		e.TraceID = sc.TraceID().String()
	}
	return e
}

// Prefer the raw event JSON when available: new Feishu fields must not vanish
// merely because the SDK does not yet declare them. Never retain HTTP headers.
func eventPayload(header *larkevent.EventHeader, event any, req *larkevent.EventReq) (string, string) {
	var root map[string]any
	if req != nil && len(req.Body) > 0 {
		decoder := json.NewDecoder(bytes.NewReader(req.Body))
		decoder.UseNumber()
		if decoder.Decode(&root) != nil {
			root = nil
		}
		if _, ok := root["event"].(map[string]any); !ok {
			root = nil
		}
	}
	if root == nil {
		body, _ := json.Marshal(struct {
			Schema string                 `json:"schema"`
			Header *larkevent.EventHeader `json:"header,omitempty"`
			Event  any                    `json:"event"`
		}{"2.0", header, event})
		_ = json.Unmarshal(body, &root)
	}
	chatID := ""
	if eventMap, ok := root["event"].(map[string]any); ok {
		chatID, _ = eventMap["chat_id"].(string)
	}
	redact(root)
	body, _ := json.Marshal(root)
	return string(body), strings.TrimSpace(chatID)
}

func redact(v any) {
	switch x := v.(type) {
	case map[string]any:
		for k, v := range x {
			n := strings.ToLower(strings.NewReplacer("_", "", "-", "", ".", "").Replace(k))
			secret := false
			for _, needle := range []string{"token", "password", "passwd", "secret", "authorization", "cookie", "apikey", "encryptionkey", "encryptkey", "accesskey"} {
				if strings.Contains(n, needle) {
					secret = true
					break
				}
			}
			if secret {
				x[k] = "[REDACTED]"
			} else {
				x[k] = redactValue(v)
			}
		}
	case []any:
		for i, v := range x {
			x[i] = redactValue(v)
		}
	}
}

func redactValue(v any) any {
	if text, ok := v.(string); ok {
		var nested any
		decoder := json.NewDecoder(strings.NewReader(text))
		decoder.UseNumber()
		if json.Valid([]byte(text)) && decoder.Decode(&nested) == nil {
			switch nested.(type) {
			case map[string]any, []any:
				redact(nested)
				body, _ := json.Marshal(nested)
				return string(body)
			}
		}
		return redactText(text)
	}
	redact(v)
	return v
}

var errorCodePattern = regexp.MustCompile(`(?i)["']?code["']?\s*[:=]\s*(-?\d+)`)
var credentialPattern = regexp.MustCompile(`(?i)(["']?(?:api[_-]?key|[a-z_]*token|password|passwd|authorization|cookie|set-cookie|[a-z_]*secret)["']?\s*[:=]\s*)(?:"(?:\\.|[^"\\])*"|'(?:\\.|[^'\\])*'|[^\s,&;]+)`)
var headerCredentialPattern = regexp.MustCompile(`(?i)(\b(?:authorization|cookie|set-cookie)\s*[:=]\s*)[^\r\n]+`)
var bearerPattern = regexp.MustCompile(`(?i)\bBearer\s+[A-Za-z0-9._~+/=-]+`)
var userInfoPattern = regexp.MustCompile(`(https?://)[^/\s@:]+:[^/\s@]+@`)

func redactText(s string) string {
	s = credentialPattern.ReplaceAllString(s, "${1}[REDACTED]")
	s = headerCredentialPattern.ReplaceAllString(s, "${1}[REDACTED]")
	s = bearerPattern.ReplaceAllString(s, "Bearer [REDACTED]")
	return userInfoPattern.ReplaceAllString(s, "${1}[REDACTED]@")
}

func safeError(err error) (string, int) {
	if err == nil {
		return "", 0
	}
	code := 0
	if m := errorCodePattern.FindStringSubmatch(err.Error()); len(m) > 1 {
		code, _ = strconv.Atoi(m[1])
	}
	s := redactText(err.Error())
	if runes := []rune(s); len(runes) > 2048 {
		s = string(runes[:2048])
	}
	return s, code
}

func (e Event) Attributes() []attribute.KeyValue {
	a := []attribute.KeyValue{attribute.String("event.id", e.EventID), attribute.String("event.type", e.EventType), attribute.String("message.id", e.MessageID), attribute.String("operator.type", e.OperatorType), attribute.String("actor.open_id", e.ActorOpenID), attribute.String("actor.user_id", e.ActorUserID), attribute.String("actor.app_id", e.ActorAppID), attribute.String("reaction.type", e.ReactionType), attribute.String("event.action_time", e.ActionTime), attribute.String("bot.app_id", e.AppID), attribute.String("bot.open_id", e.BotOpenID)}
	if e.OccurredAt != nil {
		a = append(a, attribute.String("event.action_at", e.OccurredAt.Format(time.RFC3339Nano)))
	}
	if e.ChatID != "" {
		a = append(a, attribute.String("chat.id", e.ChatID))
	}
	return a
}

func (e Event) Fields() []zap.Field {
	f := []zap.Field{zap.String("event_id", e.EventID), zap.String("event_type", e.EventType), zap.String("message_id", e.MessageID), zap.String("operator_type", e.OperatorType), zap.String("actor_open_id", e.ActorOpenID), zap.String("actor_user_id", e.ActorUserID), zap.String("actor_app_id", e.ActorAppID), zap.String("reaction_type", e.ReactionType), zap.String("action_time", e.ActionTime), zap.String("app_id", e.AppID), zap.String("bot_open_id", e.BotOpenID)}
	if e.ChatID != "" {
		f = append(f, zap.String("chat_id", e.ChatID))
	}
	return f
}
