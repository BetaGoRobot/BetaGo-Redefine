package lark

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/telemetry"
	"github.com/gorilla/websocket"
	core "github.com/larksuite/oapi-sdk-go/v3/core"
	event "github.com/larksuite/oapi-sdk-go/v3/event"
	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher"
	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher/callback"
	im "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	"github.com/larksuite/oapi-sdk-go/v3/ws"
)

type IngressConfig struct {
	MusicCardAction                             func(context.Context, MusicCardActionSubmission) error
	Normalize                                   NormalizeConfig
	AppSecret, VerificationToken, EncryptionKey string
	Persist                                     func(context.Context, domain.Event) error
	// LuckinBinding receives a verified private form, never a normal chat fact.
	LuckinBinding func(context.Context, CredentialBindingSubmission) error
	HTTPClient    *http.Client
}
type Ingress struct {
	cfg        IngressConfig
	n          *Normalizer
	dispatcher *dispatcher.EventDispatcher
	ready      atomic.Bool
	requests   chan struct{}
}

func NewIngress(c IngressConfig) (*Ingress, error) {
	if c.Persist == nil || c.AppSecret == "" {
		return nil, errMalformed
	}
	n, err := NewNormalizer(c.Normalize)
	if err != nil {
		return nil, err
	}
	i := &Ingress{cfg: c, n: n, requests: make(chan struct{}, 16)}
	d := dispatcher.NewEventDispatcher(c.VerificationToken, c.EncryptionKey)
	d.Config.Logger = quietLogger{}
	d.OnP2MessageReceiveV1(func(ctx context.Context, e *im.P2MessageReceiveV1) error {
		if e != nil && e.Event != nil && e.Event.Message != nil {
			ctx = telemetry.Context(ctx, "message_id", value(e.Event.Message.MessageId), "chat_id", value(e.Event.Message.ChatId))
		}
		m, err := i.n.Message(e)
		return i.persist(ctx, m, err)
	})
	d.OnP2MessageRecalledV1(func(ctx context.Context, e *im.P2MessageRecalledV1) error {
		m, err := i.n.Recalled(e)
		return i.persist(ctx, m, err)
	})
	d.OnP2CardActionTrigger(func(ctx context.Context, e *callback.CardActionTriggerEvent) (*callback.CardActionTriggerResponse, error) {
		if isMusicCardAction(e) {
			return i.handleMusicCardAction(ctx, e)
		}
		if isLuckinBindingCallback(e) {
			return i.handleLuckinBinding(ctx, e)
		}
		m, parseErr := i.n.Card(e)
		if err := i.persist(ctx, m, parseErr); err != nil {
			return nil, err
		}
		return &callback.CardActionTriggerResponse{Toast: &callback.Toast{Type: "info", Content: "已收到，正在核验任务权限"}}, nil
	})
	i.dispatcher = d
	return i, nil
}
func (i *Ingress) Dispatcher() *dispatcher.EventDispatcher { return i.dispatcher }
func (i *Ingress) persist(ctx context.Context, e domain.Event, err error) (result error) {
	if e.ID != "" {
		ctx = telemetry.Context(ctx, "event_id", e.ID, "message_id", e.Message.Source.MessageID, "scope_hash", e.Message.Source.Scope.Key(), "chat_id", e.Message.Source.Scope.ChatID, "author_id", e.Message.Source.AuthorID)
	}
	ctx, span := telemetry.Start(ctx, "agent.ingress")
	defer func() { telemetry.End(ctx, span, result) }()
	if errors.Is(err, ErrIgnored) {
		telemetry.Info(ctx, "agent.ingress.ignored", "reason", "outside_configured_group")
		return nil
	}
	if err != nil {
		return telemetry.Wrap("ingress.normalize", "invalid_event_identity_or_content", errMalformed)
	}
	var meta domain.IngressMetadata
	_ = json.Unmarshal(e.Message.Raw, &meta)
	telemetry.Info(ctx, "agent.message.received", "event_kind", string(e.Type), "message_type", meta.MessageType, "addressed", meta.Addressed, "sender_type", meta.SenderType, "text_bytes", len(e.Message.Text), "media_count", len(e.Message.Media))
	telemetry.Payload(ctx, "agent.message.content", "message_text", e.Message.Text)
	ctx, cancel := context.WithTimeout(ctx, 2500*time.Millisecond)
	defer cancel()
	if err := i.cfg.Persist(ctx, e); err != nil {
		return telemetry.Wrap("ingress.persist", "persistence_failed", err)
	}
	return nil
}
func (i *Ingress) Ready() bool { return i.ready.Load() }

// Start runs only the long connection. Its SDK lifecycle waits for dispatch
// workers on cancellation, so they cannot outlive the owning process context.
func (i *Ingress) Start(ctx context.Context) error {
	hc := http.Client{Timeout: 15 * time.Second}
	if i.cfg.HTTPClient != nil {
		hc = *i.cfg.HTTPClient
		hc.Timeout = 15 * time.Second
	}
	hc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	telemetry.Info(ctx, "lark.connection.starting")
	client := ws.NewClient(i.cfg.Normalize.AppID, i.cfg.AppSecret, ws.WithEventHandler(i.dispatcher), ws.WithLogger(quietLogger{}), ws.WithLogLevel(core.LogLevelError), ws.WithHttpClient(boundedHTTP{&hc, 2 << 20}), ws.WithWebSocketDialer(&websocket.Dialer{Proxy: http.ProxyFromEnvironment, HandshakeTimeout: 15 * time.Second}), ws.WithOnReady(func() { i.ready.Store(true); telemetry.Info(ctx, "lark.connection.ready") }), ws.WithOnReconnected(func() { i.ready.Store(true); telemetry.Info(ctx, "lark.connection.reconnected") }), ws.WithOnReconnecting(func() { i.ready.Store(false); telemetry.Warn(ctx, "lark.connection.reconnecting") }), ws.WithOnDisconnected(func() { i.ready.Store(false); telemetry.Warn(ctx, "lark.connection.disconnected") }))
	err := client.Start(ctx)
	i.ready.Store(false)
	closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client.CloseAndWait(closeCtx)
	if ctx.Err() != nil {
		return nil
	}
	if err != nil {
		return telemetry.Wrap("lark.connection", "connection_stopped", err)
	}
	return nil
}

// Handler retains SDK signature/token/encryption verification. The caller only
// mounts it when callback transport is explicitly selected in configuration.
func (i *Ingress) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", 405)
			return
		}
		select {
		case i.requests <- struct{}{}:
			defer func() { <-i.requests }()
		default:
			http.Error(w, "busy", 429)
			return
		}
		if r.ContentLength > 1<<20 {
			http.Error(w, "event too large", 413)
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, (1<<20)+1))
		if err != nil || len(body) > 1<<20 {
			http.Error(w, "invalid event body", 413)
			return
		}
		resp := i.dispatcher.Handle(r.Context(), &event.EventReq{Header: r.Header.Clone(), Body: body, RequestURI: r.URL.Path})
		if resp == nil || resp.StatusCode < 200 || resp.StatusCode >= 300 {
			http.Error(w, "event verification or persistence failed", 400)
			return
		}
		for key, values := range resp.Header {
			for _, v := range values {
				w.Header().Add(key, v)
			}
		}
		w.WriteHeader(resp.StatusCode)
		w.Write(resp.Body)
	})
}
