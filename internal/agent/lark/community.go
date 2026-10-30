package lark

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/telemetry"
	core "github.com/larksuite/oapi-sdk-go/v3/core"
	im "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

const maxImageBytes int64 = 10 << 20

type downloadByteLimitKey struct{}

func communityAPIError(ctx context.Context, err error, status, code int, details ...core.CodeError) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	if err != nil || code != 0 || status >= 400 {
		reason := "provider_rejected"
		if code == 0 && status < 400 {
			reason = "transport_failed"
		}
		telemetry.Error(ctx, "lark.api.failed", telemetry.Wrap("lark.api", reason, errAPI), "http_status", status, "provider_code", code)
	}
	for _, detail := range details {
		if detail.Err != nil && len(detail.Err.PermissionViolations) > 0 {
			return domain.ErrUnauthorized
		}
	}
	if status == http.StatusUnauthorized || status == http.StatusForbidden || code == 99991672 || code == 99991679 || code == 99991671 || code == 99991664 || code == 99991663 {
		return domain.ErrUnauthorized
	}
	if status == http.StatusNotFound {
		return domain.ErrNotFound
	}
	if errors.Is(err, domain.ErrUnauthorized) || errors.Is(err, domain.ErrStaleSource) || errors.Is(err, domain.ErrNotFound) || errors.Is(err, domain.ErrInvalid) {
		return err
	}
	if errors.Is(err, errMalformed) {
		return domain.ErrUnauthorized
	}
	return errAPI
}
func (c *Client) communityScope(ctx context.Context, s domain.Scope) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	if c.authorize(s) != nil {
		return domain.ErrUnauthorized
	}
	return nil
}
func (c *Client) currentTarget(ctx context.Context, s domain.Scope, id string) (domain.Message, error) {
	if err := c.communityScope(ctx, s); err != nil {
		return domain.Message{}, err
	}
	m, err := c.ReadMessage(ctx, s, id)
	if err != nil {
		return m, communityAPIError(ctx, err, 0, 0)
	}
	if m.Deleted {
		return m, domain.ErrStaleSource
	}
	return m, nil
}
func (c *Client) GroupInfo(ctx context.Context, s domain.Scope) (domain.GroupInfo, error) {
	var out domain.GroupInfo
	if err := c.communityScope(ctx, s); err != nil {
		return out, err
	}
	r, err := c.sdk.Im.Chat.Get(ctx, im.NewGetChatReqBuilder().ChatId(s.ChatID).UserIdType("open_id").Build(), core.WithTenantKey(s.TenantID))
	if err != nil || r == nil {
		return out, communityAPIError(ctx, err, 0, 0)
	}
	if !r.Success() || r.Data == nil {
		return out, communityAPIError(ctx, nil, r.StatusCode, r.Code, r.CodeError)
	}
	if value(r.Data.TenantKey) != "" && value(r.Data.TenantKey) != s.TenantID {
		return out, domain.ErrUnauthorized
	}
	d := r.Data
	return domain.GroupInfo{Name: value(d.Name), Description: value(d.Description), OwnerID: value(d.OwnerId), Mode: value(d.ChatMode), UserCount: value(d.UserCount), BotCount: value(d.BotCount)}, nil
}

type providerCursor struct{ Scope, Kind, Token string }

func (c *Client) cursorSecret() error {
	c.cursorOnce.Do(func() { _, c.cursorErr = rand.Read(c.cursorKey[:]) })
	return c.cursorErr
}
func (c *Client) sealCursor(s domain.Scope, kind, token string) (string, error) {
	if token == "" || len(token) > 4096 {
		return "", errAPI
	}
	if err := c.cursorSecret(); err != nil {
		return "", errAPI
	}
	b, _ := json.Marshal(providerCursor{s.Key(), kind, token})
	mac := hmac.New(sha256.New, c.cursorKey[:])
	mac.Write(b)
	return base64.RawURLEncoding.EncodeToString(b) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}
func (c *Client) openCursor(s domain.Scope, kind, token string) (string, error) {
	if token == "" {
		return "", nil
	}
	if len(token) > 8192 {
		return "", domain.ErrInvalid
	}
	if err := c.cursorSecret(); err != nil {
		return "", errAPI
	}
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return "", domain.ErrInvalid
	}
	b, e := base64.RawURLEncoding.DecodeString(parts[0])
	sig, se := base64.RawURLEncoding.DecodeString(parts[1])
	if e != nil || se != nil {
		return "", domain.ErrInvalid
	}
	mac := hmac.New(sha256.New, c.cursorKey[:])
	mac.Write(b)
	if !hmac.Equal(sig, mac.Sum(nil)) {
		return "", domain.ErrInvalid
	}
	var v providerCursor
	if json.Unmarshal(b, &v) != nil || v.Scope != s.Key() || v.Kind != kind || v.Token == "" || len(v.Token) > 4096 {
		return "", domain.ErrInvalid
	}
	return v.Token, nil
}
func (c *Client) GroupMembers(ctx context.Context, s domain.Scope, n int, token string) (domain.GroupMembers, error) {
	out := domain.GroupMembers{Members: []domain.GroupMember{}}
	if err := c.communityScope(ctx, s); err != nil {
		return out, err
	}
	if n < 1 || n > 100 {
		return out, domain.ErrInvalid
	}
	cursor, err := c.openCursor(s, "members", token)
	if err != nil {
		return out, err
	}
	r, err := c.sdk.Im.ChatMembers.Get(ctx, im.NewGetChatMembersReqBuilder().ChatId(s.ChatID).MemberIdType("open_id").PageSize(n).PageToken(cursor).CheckSecurityConf(true).Build(), core.WithTenantKey(s.TenantID))
	if err != nil || r == nil {
		return out, communityAPIError(ctx, err, 0, 0)
	}
	if !r.Success() || r.Data == nil {
		return out, communityAPIError(ctx, nil, r.StatusCode, r.Code, r.CodeError)
	}
	// Feishu deliberately returns an entire same-join batch even if it exceeds n.
	// The transport's byte ceiling bounds this full page without losing members.
	for _, m := range r.Data.Items {
		if m == nil || value(m.MemberId) == "" {
			return out, errAPI
		}
		out.Members = append(out.Members, domain.GroupMember{ID: value(m.MemberId), Name: value(m.Name), IDType: value(m.MemberIdType)})
	}
	out.HasMore = r.Data.HasMore != nil && *r.Data.HasMore
	out.SecurityLimited = r.Data.TriggerSecurityConfLimit != nil && *r.Data.TriggerSecurityConfLimit
	if r.Data.MemberTotal != nil {
		out.Total = *r.Data.MemberTotal
	}
	if out.HasMore {
		out.PageToken, err = c.sealCursor(s, "members", value(r.Data.PageToken))
		if err != nil {
			return domain.GroupMembers{}, err
		}
	}
	return out, nil
}
func (c *Client) ownOperator(op *im.Operator) bool {
	if op == nil {
		return false
	}
	id, typ := value(op.OperatorId), value(op.OperatorType)
	return (typ == "app" && id == c.cfg.AppID) || ((typ == "user" || typ == "app") && id == c.cfg.BotOpenID)
}
func (c *Client) reaction(id *string, op *im.Operator, emoji *im.Emoji, at *string) domain.Reaction {
	out := domain.Reaction{ID: value(id), ActionTime: value(at), Own: c.ownOperator(op)}
	if op != nil {
		out.OperatorID = value(op.OperatorId)
		out.OperatorType = value(op.OperatorType)
	}
	if emoji != nil {
		out.Emoji = value(emoji.EmojiType)
	}
	return out
}
func (c *Client) ListReactions(ctx context.Context, s domain.Scope, id string, n int, token string) (domain.ReactionPage, error) {
	out := domain.ReactionPage{Reactions: []domain.Reaction{}}
	if n < 1 || n > 100 {
		return out, domain.ErrInvalid
	}
	if err := c.communityScope(ctx, s); err != nil {
		return out, err
	}
	cursor, err := c.openCursor(s, "reactions:"+id, token)
	if err != nil {
		return out, err
	}
	if _, err = c.currentTarget(ctx, s, id); err != nil {
		return out, err
	}
	r, err := c.sdk.Im.MessageReaction.List(ctx, im.NewListMessageReactionReqBuilder().MessageId(id).UserIdType("open_id").PageSize(n).PageToken(cursor).Build(), core.WithTenantKey(s.TenantID))
	if err != nil || r == nil {
		return out, communityAPIError(ctx, err, 0, 0)
	}
	if !r.Success() || r.Data == nil {
		return out, communityAPIError(ctx, nil, r.StatusCode, r.Code, r.CodeError)
	}
	for _, v := range r.Data.Items {
		if v == nil || value(v.ReactionId) == "" {
			return out, errAPI
		}
		out.Reactions = append(out.Reactions, c.reaction(v.ReactionId, v.Operator, v.ReactionType, v.ActionTime))
	}
	out.HasMore = r.Data.HasMore != nil && *r.Data.HasMore
	if out.HasMore {
		out.PageToken, err = c.sealCursor(s, "reactions:"+id, value(r.Data.PageToken))
		if err != nil {
			return domain.ReactionPage{}, err
		}
	}
	return out, nil
}
func validCommunityID(s string) bool {
	return len(s) > 0 && len(s) <= 1024 && strings.TrimSpace(s) == s && !strings.ContainsAny(s, "\r\n\x00")
}
func (c *Client) AddReaction(ctx context.Context, s domain.Scope, id, emoji, uuid string) (domain.Reaction, error) {
	var out domain.Reaction
	if !validCommunityID(emoji) || len(emoji) > 128 || uuid == "" || len(uuid) > 50 {
		return out, domain.ErrInvalid
	}
	if _, err := c.currentTarget(ctx, s, id); err != nil {
		return out, err
	}
	// Reaction.Create has no provider UUID field. The durable tool ledger owns
	// replay prevention; Feishu also permits only one same-emoji reaction per actor.
	r, err := c.sdk.Im.MessageReaction.Create(ctx, im.NewCreateMessageReactionReqBuilder().MessageId(id).Body(im.NewCreateMessageReactionReqBodyBuilder().ReactionType(im.NewEmojiBuilder().EmojiType(emoji).Build()).Build()).Build(), core.WithTenantKey(s.TenantID))
	if err != nil || r == nil {
		return out, communityAPIError(ctx, err, 0, 0)
	}
	if !r.Success() || r.Data == nil || value(r.Data.ReactionId) == "" {
		return out, communityAPIError(ctx, nil, r.StatusCode, r.Code, r.CodeError)
	}
	d := r.Data
	return c.reaction(d.ReactionId, d.Operator, d.ReactionType, d.ActionTime), nil
}
func (c *Client) RemoveReaction(ctx context.Context, s domain.Scope, id, reactionID, uuid string) error {
	if !validCommunityID(reactionID) || uuid == "" || len(uuid) > 50 {
		return domain.ErrInvalid
	}
	if _, err := c.currentTarget(ctx, s, id); err != nil {
		return err
	}
	cursor := ""
	found := false
	for page := 0; page < 10; page++ {
		p, err := c.ListReactions(ctx, s, id, 100, cursor)
		if err != nil {
			return err
		}
		for _, v := range p.Reactions {
			if v.ID == reactionID {
				if !v.Own {
					return domain.ErrUnauthorized
				}
				found = true
				break
			}
		}
		if found || !p.HasMore {
			break
		}
		if p.PageToken == cursor {
			return errAPI
		}
		cursor = p.PageToken
	}
	if !found {
		return domain.ErrNotFound
	}
	if _, err := c.currentTarget(ctx, s, id); err != nil {
		return err
	}
	r, err := c.sdk.Im.MessageReaction.Delete(ctx, im.NewDeleteMessageReactionReqBuilder().MessageId(id).ReactionId(reactionID).Build(), core.WithTenantKey(s.TenantID))
	if err != nil || r == nil {
		return communityAPIError(ctx, err, 0, 0)
	}
	if !r.Success() {
		return communityAPIError(ctx, nil, r.StatusCode, r.Code, r.CodeError)
	}
	return nil
}
func (c *Client) checkImage(ctx context.Context, s domain.Scope, source domain.Message, resource domain.Media) error {
	if source.Source.Scope != s || source.Deleted || resource.Type != domain.MediaImage || resource.Source != domain.MediaSourceLark {
		return domain.ErrUnauthorized
	}
	m, err := c.currentTarget(ctx, s, source.Source.MessageID)
	if err != nil {
		return err
	}
	if m.Source != source.Source {
		return domain.ErrStaleSource
	}
	for _, v := range m.Media {
		if v == resource {
			return nil
		}
	}
	return domain.ErrStaleSource
}
func (c *Client) SendImage(ctx context.Context, s domain.Scope, source domain.Message, resource domain.Media, parent, uuid string) (string, error) {
	if uuid == "" || len(uuid) > 50 {
		return "", domain.ErrInvalid
	}
	if err := c.checkImage(ctx, s, source, resource); err != nil {
		return "", err
	}
	if parent != "" {
		if _, err := c.currentTarget(ctx, s, parent); err != nil {
			return "", err
		}
	}
	downloadCtx := context.WithValue(ctx, downloadByteLimitKey{}, maxImageBytes)
	reader, _, err := c.Download(downloadCtx, source, resource)
	if err != nil {
		return "", communityAPIError(ctx, err, 0, 0)
	}
	b, err := io.ReadAll(io.LimitReader(reader, maxImageBytes+1))
	reader.Close()
	if err != nil || len(b) == 0 || int64(len(b)) > maxImageBytes {
		return "", domain.ErrInvalid
	}
	if err = c.checkImage(ctx, s, source, resource); err != nil {
		return "", err
	}
	r, err := c.sdk.Im.Image.Create(ctx, im.NewCreateImageReqBuilder().Body(im.NewCreateImageReqBodyBuilder().ImageType("message").Image(bytes.NewReader(b)).Build()).Build(), core.WithTenantKey(s.TenantID))
	if err != nil || r == nil {
		return "", communityAPIError(ctx, err, 0, 0)
	}
	if !r.Success() || r.Data == nil || value(r.Data.ImageKey) == "" {
		return "", communityAPIError(ctx, nil, r.StatusCode, r.Code, r.CodeError)
	}
	if err = c.checkImage(ctx, s, source, resource); err != nil {
		return "", err
	}
	content, _ := json.Marshal(map[string]string{"image_key": value(r.Data.ImageKey)})
	id, err := c.Send(ctx, s, parent, "image", string(content), uuid)
	if err != nil {
		return "", communityAPIError(ctx, err, 0, 0)
	}
	return id, nil
}
