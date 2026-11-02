// Package lark implements the new Agent's transport and atomic SDK operations.
package lark

import (
	"encoding/json"
	"errors"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
	event "github.com/larksuite/oapi-sdk-go/v3/event"
	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher/callback"
	im "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

var ErrIgnored = errors.New("event outside configured group ingress")
var errMalformed = errors.New("invalid Lark event identity or content")

type NormalizeConfig struct {
	AppID, BotOpenID, TenantID string
	AllowedChatIDs             []string
}
type Normalizer struct {
	cfg   NormalizeConfig
	chats map[string]bool
}

func NewNormalizer(c NormalizeConfig) (*Normalizer, error) {
	if c.AppID == "" || c.BotOpenID == "" {
		return nil, errMalformed
	}
	n := &Normalizer{cfg: c, chats: map[string]bool{}}
	for _, id := range c.AllowedChatIDs {
		if id == "" {
			return nil, errMalformed
		}
		n.chats[id] = true
	}
	return n, nil
}
func value(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
func millis(s string) (time.Time, int64, error) {
	return providerTimestamp(s, time.UnixMilli)
}
func providerTimestamp(s string, convert func(int64) time.Time) (time.Time, int64, error) {
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil || v <= 0 || v > (math.MaxInt64-1)/2 {
		return time.Time{}, 0, errMalformed
	}
	at := convert(v).UTC()
	if at.Year() < 1 || at.Year() > 9999 {
		return time.Time{}, 0, errMalformed
	}
	return at, v, nil
}
func (n *Normalizer) scope(base *event.EventV2Base, chat string) (domain.Scope, error) {
	if base == nil || base.Header == nil || base.Header.AppID != n.cfg.AppID || base.Header.TenantKey == "" || base.Header.EventID == "" {
		return domain.Scope{}, errMalformed
	}
	if n.cfg.TenantID != "" && base.Header.TenantKey != n.cfg.TenantID {
		return domain.Scope{}, ErrIgnored
	}
	if len(n.chats) > 0 && !n.chats[chat] {
		return domain.Scope{}, ErrIgnored
	}
	s := domain.Scope{TenantID: base.Header.TenantKey, AppID: n.cfg.AppID, ChatID: chat, Visibility: "group"}
	if s.Validate() != nil {
		return domain.Scope{}, errMalformed
	}
	return s, nil
}
func (n *Normalizer) Message(e *im.P2MessageReceiveV1) (domain.Event, error) {
	if e == nil || e.Event == nil || e.Event.Message == nil || e.Event.Sender == nil || e.Event.Sender.SenderId == nil {
		return domain.Event{}, errMalformed
	}
	m, sender := e.Event.Message, e.Event.Sender
	if value(m.ChatType) != "group" {
		return domain.Event{}, ErrIgnored
	}
	s, err := n.scope(e.EventV2Base, value(m.ChatId))
	if err != nil {
		return domain.Event{}, err
	}
	if value(sender.TenantKey) != "" && value(sender.TenantKey) != s.TenantID {
		return domain.Event{}, errMalformed
	}
	author := value(sender.SenderId.OpenId)
	created, _, err := millis(value(m.CreateTime))
	if err != nil {
		return domain.Event{}, err
	}
	updated, seq, err := millis(value(m.UpdateTime))
	if value(m.UpdateTime) == "" {
		updated, seq, err = millis(value(m.CreateTime))
	}
	if err != nil || updated.Before(created) {
		return domain.Event{}, errMalformed
	}
	at, _, err := millis(e.EventV2Base.Header.CreateTime)
	if err != nil {
		return domain.Event{}, err
	}
	text, media, err := parseContent(value(m.MessageType), value(m.Content))
	if err != nil {
		return domain.Event{}, err
	}
	meta := domain.IngressMetadata{Kind: "message", MessageType: value(m.MessageType), SenderType: value(sender.SenderType)}
	for _, mention := range m.Mentions {
		if mention == nil || mention.Id == nil {
			continue
		}
		id := value(mention.Id.OpenId)
		meta.Mentions = append(meta.Mentions, domain.IngressMention{Key: value(mention.Key), OpenID: id, Name: value(mention.Name)})
		if id == n.cfg.BotOpenID && meta.SenderType == "user" && author != n.cfg.BotOpenID {
			meta.Addressed = true
		}
	}
	meta.Provider, _ = json.Marshal(e.Event)
	raw, _ := json.Marshal(meta)
	out := domain.Event{ID: e.EventV2Base.Header.EventID, Type: domain.EventMessage, OccurredAt: at, Message: domain.Message{Source: domain.SourceRef{Scope: s, MessageID: value(m.MessageId), Version: seq * 2, AuthorID: author, OccurredAt: created}, EventID: e.EventV2Base.Header.EventID, ParentID: value(m.ParentId), RootID: value(m.RootId), ThreadID: value(m.ThreadId), Text: text, Media: media, Raw: raw, UpdatedAt: updated}}
	if out.Validate() != nil {
		return domain.Event{}, errMalformed
	}
	return out, nil
}
func (n *Normalizer) Recalled(e *im.P2MessageRecalledV1) (domain.Event, error) {
	if e == nil || e.Event == nil {
		return domain.Event{}, errMalformed
	}
	s, err := n.scope(e.EventV2Base, value(e.Event.ChatId))
	if err != nil {
		return domain.Event{}, err
	}
	updated, seq, err := millis(value(e.Event.RecallTime))
	if err != nil {
		return domain.Event{}, err
	}
	at, _, err := millis(e.EventV2Base.Header.CreateTime)
	if err != nil {
		return domain.Event{}, err
	}
	raw, _ := json.Marshal(domain.IngressMetadata{Kind: "recall"})
	out := domain.Event{ID: e.EventV2Base.Header.EventID, Type: domain.EventDelete, OccurredAt: at, Message: domain.Message{Source: domain.SourceRef{Scope: s, MessageID: value(e.Event.MessageId), Version: seq*2 + 1}, EventID: e.EventV2Base.Header.EventID, Deleted: true, UpdatedAt: updated, Raw: raw}}
	if out.Validate() != nil {
		return domain.Event{}, errMalformed
	}
	return out, nil
}
func (n *Normalizer) Card(e *callback.CardActionTriggerEvent) (domain.Event, error) {
	if e == nil || e.Event == nil || e.Event.Operator == nil || e.Event.Context == nil || e.Event.Action == nil {
		return domain.Event{}, errMalformed
	}
	s, err := n.scope(e.EventV2Base, e.Event.Context.OpenChatID)
	if err != nil {
		return domain.Event{}, err
	}
	if value(e.Event.Operator.TenantKey) != "" && value(e.Event.Operator.TenantKey) != s.TenantID {
		return domain.Event{}, errMalformed
	}
	// Card callbacks document microseconds; IM message/recalled timestamps
	// separately document milliseconds. Preserve each protocol's actual unit.
	at, seq, err := providerTimestamp(e.EventV2Base.Header.CreateTime, time.UnixMicro)
	if err != nil {
		return domain.Event{}, err
	}
	get := func(key string) string { v, _ := e.Event.Action.Value[key].(string); return v }
	control := domain.ControlAction{Action: get("action"), TaskID: get("task_id"), ApprovalID: get("approval_id")}
	control.Revision, err = strconv.ParseInt(get("revision"), 10, 64)
	if err != nil || control.Revision <= 0 || control.TaskID == "" || len(control.TaskID) > 256 || len(control.ApprovalID) > 256 {
		return domain.Event{}, errMalformed
	}
	switch control.Action {
	case "approve", "cancel", "modify", "delegate":
	default:
		return domain.Event{}, errMalformed
	}
	if control.Action == "approve" && control.ApprovalID == "" {
		return domain.Event{}, errMalformed
	}
	raw, _ := json.Marshal(domain.IngressMetadata{Kind: "control", SenderType: "user", Control: &control})
	id := e.EventV2Base.Header.EventID
	out := domain.Event{ID: id, Type: domain.EventMessage, OccurredAt: at, Message: domain.Message{Source: domain.SourceRef{Scope: s, MessageID: "card:" + id, AuthorID: e.Event.Operator.OpenID, Version: seq * 2, OccurredAt: at}, EventID: id, ParentID: e.Event.Context.OpenMessageID, UpdatedAt: at, Raw: raw}}
	if out.Validate() != nil {
		return domain.Event{}, errMalformed
	}
	return out, nil
}

type postNode struct{ Tag, Text, ImageKey, FileKey, Href, UserID, UserName string }

func (p *postNode) UnmarshalJSON(b []byte) error {
	var v struct {
		Tag      string `json:"tag"`
		Text     string `json:"text"`
		ImageKey string `json:"image_key"`
		FileKey  string `json:"file_key"`
		Href     string `json:"href"`
		UserID   string `json:"user_id"`
		UserName string `json:"user_name"`
	}
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	*p = postNode{v.Tag, v.Text, v.ImageKey, v.FileKey, v.Href, v.UserID, v.UserName}
	return nil
}
func parseContent(kind, raw string) (string, []domain.Media, error) {
	if len(raw) > 1<<20 || !json.Valid([]byte(raw)) {
		return "", nil, errMalformed
	}
	var v struct {
		Text     string `json:"text"`
		ImageKey string `json:"image_key"`
		FileKey  string `json:"file_key"`
		FileName string `json:"file_name"`
	}
	if json.Unmarshal([]byte(raw), &v) != nil {
		return "", nil, errMalformed
	}
	var media []domain.Media
	add := func(typ domain.MediaType, key, file string) {
		if key != "" {
			media = append(media, domain.Media{Type: typ, Source: domain.MediaSourceLark, ResourceID: key, FileName: file, Sequence: len(media)})
		}
	}
	switch kind {
	case "text":
		return v.Text, nil, nil
	case "image":
		add(domain.MediaImage, v.ImageKey, "")
	case "audio":
		add(domain.MediaAudio, v.FileKey, v.FileName)
	case "media":
		add(domain.MediaVideo, v.FileKey, v.FileName)
	case "file":
		add(domain.MediaFile, v.FileKey, v.FileName)
	case "post":
		type post struct {
			Title   string       `json:"title"`
			Content [][]postNode `json:"content"`
		}
		var p post
		if json.Unmarshal([]byte(raw), &p) != nil {
			return "", nil, errMalformed
		}
		if p.Content == nil {
			var locales map[string]json.RawMessage
			json.Unmarshal([]byte(raw), &locales)
			selected := locales["zh_cn"]
			if selected == nil {
				selected = locales["en_us"]
			}
			if selected == nil && len(locales) == 1 {
				for _, b := range locales {
					selected = b
				}
			}
			if selected == nil || json.Unmarshal(selected, &p) != nil {
				return "", nil, errMalformed
			}
		}
		var text strings.Builder
		text.WriteString(p.Title)
		for _, line := range p.Content {
			text.WriteByte('\n')
			for _, node := range line {
				switch node.Tag {
				case "text", "code_block", "md":
					text.WriteString(node.Text)
				case "a":
					text.WriteString(node.Text + " (" + node.Href + ")")
				case "at":
					text.WriteString("@" + node.UserName)
				case "img":
					add(domain.MediaImage, node.ImageKey, "")
				case "media":
					add(domain.MediaVideo, node.FileKey, "")
				}
			}
		}
		return strings.TrimSpace(text.String()), media, nil
	default:
		// Unsupported message types remain auditable raw facts. They do not
		// masquerade as successfully extracted media.
		return "", nil, nil
	}
	if len(media) == 0 {
		return "", nil, errMalformed
	}
	return v.FileName, media, nil
}
