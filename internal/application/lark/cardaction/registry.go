package cardaction

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"

	cardactionproto "github.com/BetaGoRobot/BetaGo-Redefine/pkg/cardaction"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xhandler"
	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher/callback"
)

var (
	ErrUnhandledAction = errors.New("unhandled card action")
	// ErrContinuationDispatcherPanic identifies a recovered continuation panic
	// without retaining or exposing the panic payload.
	ErrContinuationDispatcherPanic = errors.New("card continuation dispatcher panic")
)

const continuationDispatchErrorMessage = "card continuation dispatch failed"

// ContinuationDispatchError marks failures originating from a continuation
// dispatcher while exposing only a fixed safe message.
type ContinuationDispatchError struct {
	cause error
}

func (e *ContinuationDispatchError) Error() string {
	return continuationDispatchErrorMessage
}

func (e *ContinuationDispatchError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

// IsContinuationDispatchError reports whether err originated from continuation
// dispatch, including safely recovered dispatcher panics.
func IsContinuationDispatchError(err error) bool {
	var continuationErr *ContinuationDispatchError
	return errors.As(err, &continuationErr)
}

type Mode string

const (
	ModeSync  Mode = "sync"
	ModeAsync Mode = "async"
)

type (
	SyncHandler  func(context.Context, *Context) (*callback.CardActionTriggerResponse, error)
	AsyncTask    func(context.Context)
	AsyncHandler func(context.Context, *Context) (AsyncTask, error)
)

type Context struct {
	Event    *callback.CardActionTriggerEvent
	MetaData *xhandler.BaseMetaData
	Action   *cardactionproto.Parsed
}

// ContinuationRequest borrows its pointer fields for the duration of Dispatch.
// Dispatchers must treat them as read-only and must not retain, mutate, or use
// them asynchronously after Dispatch returns.
type ContinuationRequest struct {
	Event  *callback.CardActionTriggerEvent
	Meta   *xhandler.BaseMetaData
	Action *cardactionproto.Parsed
}

type ContinuationDispatcher interface {
	CanHandle(*cardactionproto.Parsed) bool
	Dispatch(context.Context, ContinuationRequest) (*callback.CardActionTriggerResponse, error)
}

type DispatchOptions struct {
	Continuation ContinuationDispatcher
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

func RegisterSyncIfAbsent(action string, handler SyncHandler) {
	defaultRegistry.registerIfAbsent(action, entry{
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

func RegisterAsyncIfAbsent(action string, handler AsyncHandler) {
	defaultRegistry.registerIfAbsent(action, entry{
		mode:  ModeAsync,
		async: handler,
	})
}

func Dispatch(ctx context.Context, event *callback.CardActionTriggerEvent, metaData *xhandler.BaseMetaData) (*callback.CardActionTriggerResponse, error) {
	return DispatchWithOptions(ctx, event, metaData, DispatchOptions{})
}

func DispatchWithOptions(
	ctx context.Context,
	event *callback.CardActionTriggerEvent,
	metaData *xhandler.BaseMetaData,
	options DispatchOptions,
) (*callback.CardActionTriggerResponse, error) {
	action, err := cardactionproto.Parse(event)
	if err != nil {
		return nil, err
	}

	if !isNilContinuationDispatcher(options.Continuation) {
		handled, response, continuationErr := tryContinuation(ctx, options.Continuation, ContinuationRequest{
			Event:  event,
			Meta:   metaData,
			Action: action,
		})
		if handled {
			return response, continuationErr
		}
	}

	actionCtx := &Context{
		Event:    event,
		MetaData: metaData,
		Action:   action,
	}

	handler, ok := defaultRegistry.handler(action.Name)
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrUnhandledAction, action.Name)
	}

	switch handler.mode {
	case ModeAsync:
		task, err := handler.async(ctx, actionCtx)
		if err != nil {
			return nil, err
		}
		if task != nil {
			go task(context.WithoutCancel(ctx))
		}
		return nil, nil
	case ModeSync:
		return handler.sync(ctx, actionCtx)
	default:
		return nil, fmt.Errorf("unsupported card action mode: %s", handler.mode)
	}
}

func isNilContinuationDispatcher(dispatcher ContinuationDispatcher) bool {
	if dispatcher == nil {
		return true
	}
	value := reflect.ValueOf(dispatcher)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Ptr, reflect.Slice, reflect.UnsafePointer:
		return value.IsNil()
	default:
		return false
	}
}

func tryContinuation(
	ctx context.Context,
	dispatcher ContinuationDispatcher,
	request ContinuationRequest,
) (handled bool, response *callback.CardActionTriggerResponse, err error) {
	handled = true
	defer func() {
		if recover() != nil {
			handled = true
			response = nil
			err = &ContinuationDispatchError{cause: ErrContinuationDispatcherPanic}
		}
	}()

	if !dispatcher.CanHandle(request.Action) {
		return false, nil, nil
	}

	response, err = dispatcher.Dispatch(ctx, request)
	if err != nil {
		err = &ContinuationDispatchError{cause: err}
	}
	return handled, response, err
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

func (c *Context) OpenID() string {
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

func (r *registry) registerIfAbsent(action string, handler entry) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.handlers[action]; exists {
		return
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

func InfoToastWithCard(message string, cardData any) *callback.CardActionTriggerResponse {
	return toastWithCard("info", message, cardData)
}

func ErrorToastWithCard(message string, cardData any) *callback.CardActionTriggerResponse {
	return toastWithCard("error", message, cardData)
}

func InfoToastWithRawCardPayload(message string, cardData any) *callback.CardActionTriggerResponse {
	// Callback responses only support raw/template card types. The payload itself
	// can still be Card JSON v2 content, so we return it as a raw card body.
	return toastWithCardType("info", message, "raw", cardData)
}

func ErrorToastWithRawCardPayload(message string, cardData any) *callback.CardActionTriggerResponse {
	// Callback responses only support raw/template card types. The payload itself
	// can still be Card JSON v2 content, so we return it as a raw card body.
	return toastWithCardType("error", message, "raw", cardData)
}

func CardOnly(cardData any) *callback.CardActionTriggerResponse {
	return &callback.CardActionTriggerResponse{
		Card: &callback.Card{
			Type: "raw",
			Data: cardData,
		},
	}
}

func RawCardPayloadOnly(cardData any) *callback.CardActionTriggerResponse {
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

func toastWithCard(kind, message string, cardData any) *callback.CardActionTriggerResponse {
	return toastWithCardType(kind, message, "raw", cardData)
}

func toastWithCardType(kind, message, cardType string, cardData any) *callback.CardActionTriggerResponse {
	resp := toast(kind, message)
	resp.Card = &callback.Card{
		Type: cardType,
		Data: cardData,
	}
	return resp
}
