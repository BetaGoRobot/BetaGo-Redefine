package lark

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/telemetry"
	sdk "github.com/larksuite/oapi-sdk-go/v3"
	core "github.com/larksuite/oapi-sdk-go/v3/core"
	im "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

type ClientConfig struct {
	AppID, AppSecret, BotOpenID, TenantID string
	AllowedChatIDs                        []string
	BaseURL                               string
	HTTPClient                            *http.Client
	MaxDownloadBytes                      int64
	Timeout                               time.Duration
	// LocalMessages holds scoped originals, including deletion tombstones.
	LocalMessages interface {
		Message(context.Context, domain.Scope, string) (domain.Message, error)
	}
	// ValidateTarget checks a scoped original or a confirmed own delivery.
	ValidateTarget func(context.Context, domain.Scope, string) error
	// ValidateMusicCard verifies a pinned own-card receipt in the local music
	// session store before CardKit updates; it never grants arbitrary targets.
	ValidateMusicCard func(context.Context, domain.Scope, string) error
}
type Client struct {
	sdk        *sdk.Client
	cfg        ClientConfig
	chats      map[string]bool
	cursorOnce sync.Once
	cursorKey  [32]byte
	cursorErr  error
	musicMu    sync.Mutex
	musicCards map[string]string
}

var errAPI = errors.New("Lark atomic API call failed")
var errResponseLimit = errors.New("Lark response exceeds byte budget")

type quietLogger struct{}

func (quietLogger) Debug(context.Context, ...interface{}) {}
func (quietLogger) Info(context.Context, ...interface{})  {}
func (quietLogger) Warn(context.Context, ...interface{})  {}
func (quietLogger) Error(context.Context, ...interface{}) {}

// boundedHTTP applies limits before the SDK's internal io.ReadAll. The SDK's
// generated file API buffers bytes, but cannot consume an unbounded resource.
type boundedHTTP struct {
	client        *http.Client
	resourceLimit int64
}

func (b boundedHTTP) Do(req *http.Request) (*http.Response, error) {
	resp, err := b.client.Do(req)
	if err != nil {
		return nil, errAPI
	}
	limit := int64(2 << 20)
	if strings.Contains(req.URL.Path, "/resources/") {
		limit = b.resourceLimit
		if bound, ok := req.Context().Value(downloadByteLimitKey{}).(int64); ok && bound > 0 && bound < limit {
			limit = bound
		}
	}
	if resp.ContentLength > limit {
		resp.Body.Close()
		return nil, errResponseLimit
	}
	resp.Body = &boundedBody{ReadCloser: resp.Body, remaining: limit}
	return resp, nil
}

type boundedBody struct {
	io.ReadCloser
	remaining int64
}

func (b *boundedBody) Read(p []byte) (int, error) {
	if int64(len(p)) > b.remaining+1 {
		p = p[:b.remaining+1]
	}
	n, err := b.ReadCloser.Read(p)
	b.remaining -= int64(n)
	if b.remaining < 0 {
		b.ReadCloser.Close()
		return 0, errResponseLimit
	}
	return n, err
}
func NewClient(c ClientConfig) (*Client, error) {
	if c.AppID == "" || c.AppSecret == "" || c.BotOpenID == "" {
		return nil, errMalformed
	}
	if c.MaxDownloadBytes == 0 {
		c.MaxDownloadBytes = 32 << 20
	}
	if c.Timeout == 0 {
		c.Timeout = 45 * time.Second
	}
	if c.MaxDownloadBytes < 1 || c.MaxDownloadBytes > 256<<20 || c.Timeout <= 0 || c.Timeout > 5*time.Minute {
		return nil, errMalformed
	}
	if c.BaseURL == "" {
		c.BaseURL = "https://open.feishu.cn"
	}
	u, err := url.Parse(c.BaseURL)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, errMalformed
	}
	hc := http.Client{}
	if c.HTTPClient != nil {
		hc = *c.HTTPClient
	}
	hc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	hc.Timeout = c.Timeout
	hc.Transport = telemetry.Transport("lark", hc.Transport)
	leaf := sdk.NewClient(c.AppID, c.AppSecret, sdk.WithOpenBaseUrl(strings.TrimRight(c.BaseURL, "/")), sdk.WithHttpClient(boundedHTTP{&hc, c.MaxDownloadBytes}), sdk.WithReqTimeout(c.Timeout), sdk.WithLogger(quietLogger{}), sdk.WithLogReqAtDebug(false))
	client := &Client{sdk: leaf, cfg: c, chats: map[string]bool{}}
	for _, id := range c.AllowedChatIDs {
		client.chats[id] = true
	}
	return client, nil
}
func (c *Client) authorize(s domain.Scope) error {
	if s.Validate() != nil || s.AppID != c.cfg.AppID || s.Visibility != "group" || (c.cfg.TenantID != "" && s.TenantID != c.cfg.TenantID) || (len(c.chats) > 0 && !c.chats[s.ChatID]) {
		return errMalformed
	}
	return nil
}
func (c *Client) Download(ctx context.Context, m domain.Message, resource domain.Media) (io.ReadCloser, int64, error) {
	if c.authorize(m.Source.Scope) != nil || m.Source.Validate() != nil {
		return nil, 0, errMalformed
	}
	found := false
	for _, original := range m.Media {
		if original == resource && original.Source == domain.MediaSourceLark {
			found = true
			break
		}
	}
	if !found {
		return nil, 0, errMalformed
	}
	typ := "file"
	if resource.Type == domain.MediaImage {
		typ = "image"
	}
	resp, err := c.sdk.Im.MessageResource.Get(ctx, im.NewGetMessageResourceReqBuilder().MessageId(m.Source.MessageID).FileKey(resource.ResourceID).Type(typ).Build(), core.WithTenantKey(m.Source.Scope.TenantID))
	if err != nil || resp == nil {
		return nil, 0, communityAPIError(ctx, err, 0, 0)
	}
	if resp.ApiResp == nil {
		return nil, 0, errAPI
	}
	if resp.StatusCode != http.StatusOK || !resp.Success() {
		return nil, 0, communityAPIError(ctx, nil, resp.StatusCode, resp.Code, resp.CodeError)
	}
	if resp.File == nil {
		return nil, 0, errAPI
	}
	return io.NopCloser(resp.File), int64(len(resp.RawBody)), nil
}
func (c *Client) get(ctx context.Context, s domain.Scope, id string) (*im.Message, error) {
	if c.authorize(s) != nil {
		return nil, domain.ErrUnauthorized
	}
	if id == "" || len(id) > 1024 {
		return nil, domain.ErrInvalid
	}
	r, err := c.sdk.Im.Message.Get(ctx, im.NewGetMessageReqBuilder().MessageId(id).UserIdType("open_id").Build(), core.WithTenantKey(s.TenantID))
	if err != nil || r == nil {
		return nil, communityAPIError(ctx, err, 0, 0)
	}
	if !r.Success() {
		return nil, communityAPIError(ctx, nil, r.StatusCode, r.Code, r.CodeError)
	}
	if r.Data != nil && len(r.Data.Items) == 0 {
		return nil, domain.ErrNotFound
	}
	if r.Data == nil || len(r.Data.Items) != 1 || r.Data.Items[0] == nil {
		return nil, errAPI
	}
	m := r.Data.Items[0]
	if value(m.ChatId) != s.ChatID || value(m.MessageId) != id {
		return nil, domain.ErrUnauthorized
	}
	if m.Sender != nil && value(m.Sender.TenantKey) != "" && value(m.Sender.TenantKey) != s.TenantID {
		return nil, domain.ErrUnauthorized
	}
	return m, nil
}

// localMessage returns trusted scoped originals without consulting the provider.
// A tombstone is a found row, even when its original author is unavailable.
func (c *Client) localMessage(ctx context.Context, s domain.Scope, id string) (domain.Message, error) {
	if err := ctx.Err(); err != nil {
		return domain.Message{}, err
	}
	if c.authorize(s) != nil {
		return domain.Message{}, domain.ErrUnauthorized
	}
	if !validCommunityID(id) {
		return domain.Message{}, domain.ErrInvalid
	}
	if c.cfg.LocalMessages == nil {
		return domain.Message{}, domain.ErrNotFound
	}
	m, err := c.cfg.LocalMessages.Message(ctx, s, id)
	if err != nil {
		return domain.Message{}, err
	}
	if err = ctx.Err(); err != nil {
		return domain.Message{}, err
	}
	if m.Source.Scope != s || m.Source.MessageID != id {
		return domain.Message{}, domain.ErrUnauthorized
	}
	if m.Source.Version <= 0 || (!m.Deleted && m.Source.Validate() != nil) {
		return domain.Message{}, domain.ErrInvalid
	}
	return m, nil
}

// ReadMessage uses the scoped local original or tombstone when present. Only an
// absent local row permits an explicit provider read; storage errors stay errors.
func (c *Client) ReadMessage(ctx context.Context, s domain.Scope, id string) (domain.Message, error) {
	if m, err := c.localMessage(ctx, s, id); !errors.Is(err, domain.ErrNotFound) {
		return m, err
	}
	p, err := c.get(ctx, s, id)
	if err != nil {
		return domain.Message{}, err
	}
	updated, seq, err := millis(value(p.UpdateTime))
	if value(p.UpdateTime) == "" {
		updated, seq, err = millis(value(p.CreateTime))
	}
	if err != nil {
		return domain.Message{}, err
	}
	created, _, createdErr := millis(value(p.CreateTime))
	m := domain.Message{Source: domain.SourceRef{Scope: s, MessageID: id, Version: seq * 2, OccurredAt: created}, ParentID: value(p.ParentId), RootID: value(p.RootId), ThreadID: value(p.ThreadId), UpdatedAt: updated}
	m.Deleted = p.Deleted != nil && *p.Deleted
	meta := domain.IngressMetadata{Kind: "refresh", MessageType: value(p.MsgType)}
	if m.Deleted {
		m.Source.Version++
	} else {
		if createdErr != nil || p.Sender == nil || p.Body == nil {
			return domain.Message{}, errMalformed
		}
		meta.SenderType = value(p.Sender.SenderType)
		switch value(p.Sender.IdType) {
		case "open_id":
			m.Source.AuthorID = value(p.Sender.Id)
		case "app_id":
			m.Source.AuthorID = value(p.Sender.OpenBotId)
			if value(p.Sender.Id) == c.cfg.AppID {
				m.Source.AuthorID = c.cfg.BotOpenID
			}
		default:
			return domain.Message{}, errMalformed
		}
		m.Text, m.Media, err = parseContent(value(p.MsgType), value(p.Body.Content))
		if err != nil || m.Source.Validate() != nil {
			return domain.Message{}, errMalformed
		}
		// get requests open_id identities. Preserve the current message's
		// bindings for presentation, without turning a refresh into a fresh
		// invitation. Other identity namespaces cannot be joined as OpenIDs.
		for _, mention := range p.Mentions {
			if mention == nil || value(mention.Key) == "" || value(mention.Id) == "" || (value(mention.IdType) != "" && value(mention.IdType) != "open_id") {
				continue
			}
			meta.Mentions = append(meta.Mentions, domain.IngressMention{Key: value(mention.Key), OpenID: value(mention.Id), Name: value(mention.Name)})
		}
	}
	meta.Provider, _ = json.Marshal(p)
	m.Raw, _ = json.Marshal(meta)
	identity, _ := json.Marshal([]any{s.Key(), id, m.Source.Version, m.Deleted})
	digest := sha256.Sum256(identity)
	m.EventID = "refresh:" + hex.EncodeToString(digest[:])
	return m, nil
}

// Send accepts a service-frozen idempotency UUID and a server-owned scope. A
// reply's parent is checked against server-owned facts before any write.
func (c *Client) Send(ctx context.Context, s domain.Scope, parent, typ, content, uuid string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if c.authorize(s) != nil || len(content) > 28<<10 || !json.Valid([]byte(content)) || uuid == "" || len(uuid) > 50 {
		return "", errMalformed
	}
	switch typ {
	case "text", "post", "interactive", "image", "file", "audio", "media":
	default:
		return "", errMalformed
	}
	if parent != "" {
		if !validCommunityID(parent) {
			return "", domain.ErrInvalid
		}
		if c.cfg.ValidateTarget != nil {
			if err := c.cfg.ValidateTarget(ctx, s, parent); err != nil {
				return "", err
			}
		} else {
			m, err := c.get(ctx, s, parent)
			if err != nil {
				return "", err
			}
			if m.Deleted != nil && *m.Deleted {
				return "", errMalformed
			}
		}
		r, err := c.sdk.Im.Message.Reply(ctx, im.NewReplyMessageReqBuilder().MessageId(parent).Body(im.NewReplyMessageReqBodyBuilder().Content(content).MsgType(typ).Uuid(uuid).Build()).Build(), core.WithTenantKey(s.TenantID))
		if err != nil || r == nil {
			return "", communityAPIError(ctx, err, 0, 0)
		}
		if !r.Success() {
			return "", communityAPIError(ctx, nil, r.StatusCode, r.Code, r.CodeError)
		}
		if r.Data == nil || value(r.Data.MessageId) == "" {
			return "", errAPI
		}
		return value(r.Data.MessageId), nil
	}
	r, err := c.sdk.Im.Message.Create(ctx, im.NewCreateMessageReqBuilder().ReceiveIdType("chat_id").Body(im.NewCreateMessageReqBodyBuilder().ReceiveId(s.ChatID).MsgType(typ).Content(content).Uuid(uuid).Build()).Build(), core.WithTenantKey(s.TenantID))
	if err != nil || r == nil {
		return "", communityAPIError(ctx, err, 0, 0)
	}
	if !r.Success() {
		return "", communityAPIError(ctx, nil, r.StatusCode, r.Code, r.CodeError)
	}
	if r.Data == nil || value(r.Data.MessageId) == "" {
		return "", errAPI
	}
	return value(r.Data.MessageId), nil
}
