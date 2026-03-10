package cardaction

import (
	"context"
	"errors"
	"fmt"
	"sync"

	cardactionproto "github.com/BetaGoRobot/BetaGo-Redefine/pkg/cardaction"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xhandler"
	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher/callback"
)

var ErrUnhandledAction = errors.New("unhandled card action")

type Mode string

const (
	ModeSync  Mode = "sync"
	ModeAsync Mode = "async"
)

type SyncHandler func(context.Context, *Context) (*callback.CardActionTriggerResponse, error)
type AsyncTask func(context.Context)
type AsyncHandler func(context.Context, *Context) (AsyncTask, error)

type Context struct {
	Event    *callback.CardActionTriggerEvent
	MetaData *xhandler.BaseMetaData
	Action   *cardactionproto.Parsed
}

type entry struct {
	mode  Mode
	sync  SyncHandler
	async AsyncHandler
}

type registry struct {
	mu       sync.RWMutex
	handlers map[string]entry
}

var defaultRegistry = &registry{
	handlers: make(map[string]entry),
}

func RegisterSync(action string, handler SyncHandler) {
	defaultRegistry.register(action, entry{
		mode: ModeSync,
		sync: handler,
	})
}

func RegisterAsync(action string, handler AsyncHandler) {
	defaultRegistry.register(action, entry{
		mode:  ModeAsync,
		async: handler,
	})
}

func Dispatch(ctx context.Context, event *callback.CardActionTriggerEvent, metaData *xhandler.BaseMetaData) (*callback.CardActionTriggerResponse, error) {
	action, err := cardactionproto.Parse(event)
	if err != nil {
		return nil, err
	}

	handler, ok := defaultRegistry.handler(action.Name)
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrUnhandledAction, action.Name)
	}

	actionCtx := &Context{
		Event:    event,
		MetaData: metaData,
		Action:   action,
	}

	switch handler.mode {
	case ModeAsync:
		task, err := handler.async(ctx, actionCtx)
		if err != nil {
			return nil, err
		}
		if task != nil {
			go task(ctx)
		}
		return nil, nil
	case ModeSync:
		return handler.sync(ctx, actionCtx)
	default:
		return nil, fmt.Errorf("unsupported card action mode: %s", handler.mode)
	}
}

func (c *Context) MessageID() string {
	if c == nil || c.Event == nil || c.Event.Event == nil || c.Event.Event.Context == nil {
		return ""
	}
	return c.Event.Event.Context.OpenMessageID
}

func (c *Context) ChatID() string {
	if c == nil || c.Event == nil || c.Event.Event == nil || c.Event.Event.Context == nil {
		return ""
	}
	return c.Event.Event.Context.OpenChatID
}

func (c *Context) UserID() string {
	if c == nil || c.Event == nil || c.Event.Event == nil || c.Event.Event.Operator == nil {
		return ""
	}
	return c.Event.Event.Operator.OpenID
}

func (r *registry) register(action string, handler entry) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.handlers[action]; exists {
		panic("duplicate card action handler: " + action)
	}
	r.handlers[action] = handler
}

func (r *registry) handler(action string) (entry, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	handler, ok := r.handlers[action]
	return handler, ok
}

func InfoToast(message string) *callback.CardActionTriggerResponse {
	return toast("info", message)
}

func ErrorToast(message string) *callback.CardActionTriggerResponse {
	return toast("error", message)
}

func InfoToastWithCard(message string, cardData interface{}) *callback.CardActionTriggerResponse {
	return toastWithCard("info", message, cardData)
}

func ErrorToastWithCard(message string, cardData interface{}) *callback.CardActionTriggerResponse {
	return toastWithCard("error", message, cardData)
}

func InfoToastWithRawCardPayload(message string, cardData interface{}) *callback.CardActionTriggerResponse {
	// Callback responses only support raw/template card types. The payload itself
	// can still be Card JSON v2 content, so we return it as a raw card body.
	return toastWithCardType("info", message, "raw", cardData)
}

func ErrorToastWithRawCardPayload(message string, cardData interface{}) *callback.CardActionTriggerResponse {
	// Callback responses only support raw/template card types. The payload itself
	// can still be Card JSON v2 content, so we return it as a raw card body.
	return toastWithCardType("error", message, "raw", cardData)
}

func CardOnly(cardData interface{}) *callback.CardActionTriggerResponse {
	return &callback.CardActionTriggerResponse{
		Card: &callback.Card{
			Type: "raw",
			Data: cardData,
		},
	}
}

func RawCardPayloadOnly(cardData interface{}) *callback.CardActionTriggerResponse {
	return &callback.CardActionTriggerResponse{
		Card: &callback.Card{
			// Callback responses only support raw/template card types.
			Type: "raw",
			Data: cardData,
		},
	}
}

func toast(kind, message string) *callback.CardActionTriggerResponse {
	return &callback.CardActionTriggerResponse{
		Toast: &callback.Toast{
			Type:    kind,
			Content: message,
		},
	}
}

func toastWithCard(kind, message string, cardData interface{}) *callback.CardActionTriggerResponse {
	return toastWithCardType(kind, message, "raw", cardData)
}

func toastWithCardType(kind, message, cardType string, cardData interface{}) *callback.CardActionTriggerResponse {
	resp := toast(kind, message)
	resp.Card = &callback.Card{
		Type: cardType,
		Data: cardData,
	}
	return resp
}
