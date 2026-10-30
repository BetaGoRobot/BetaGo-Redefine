package domain

import (
	"encoding/json"
	"io"
	"strings"
	"unicode"
	"unicode/utf8"
)

// These are transport/resource ceilings, not a conversational style target.
const MaxConversationMessages = 16
const MaxConversationBytes = 28 << 10

type ConversationMessage struct {
	Text             string `json:"text"`
	ReplyToMessageID string `json:"reply_to_message_id"`
}
type ConversationPlan struct {
	// Optional model-reported attribution. It grants no authority and is never
	// rendered or copied into a frozen delivery.
	MemeUseIDs          []string              `json:"meme_use_ids,omitempty"`
	MemeUseIDsMalformed bool                  `json:"-"`
	Choice              string                `json:"choice"`
	Summary             string                `json:"summary,omitempty"`
	Messages            []ConversationMessage `json:"messages"`
}

func (p ConversationPlan) Validate() error {
	switch p.Choice {
	case "observe":
		if p.Messages == nil || len(p.Messages) != 0 || strings.TrimSpace(p.Summary) != "" {
			return ErrInvalid
		}
	case "react", "reply", "expand":
		if len(p.Messages) == 0 || len(p.Messages) > MaxConversationMessages {
			return ErrInvalid
		}
	default:
		return ErrInvalid
	}
	if !utf8.ValidString(p.Summary) {
		return ErrInvalid
	}
	size := len(p.Summary)
	if size > MaxConversationBytes {
		return ErrInvalid
	}
	for i, m := range p.Messages {
		// Every section belongs to one physical message or card reply.
		if i > 0 && m.ReplyToMessageID != p.Messages[0].ReplyToMessageID {
			return ErrInvalid
		}
		if strings.TrimSpace(m.Text) == "" || !utf8.ValidString(m.Text) || !validID(m.ReplyToMessageID) {
			return ErrInvalid
		}
		content, err := json.Marshal(map[string]string{"text": m.Text})
		if err != nil {
			return err
		}
		size += len(content)
		if size > MaxConversationBytes {
			return ErrInvalid
		}
	}
	return nil
}
func ParseConversationPlan(raw string) (ConversationPlan, error) {
	var p ConversationPlan
	_, err := CanonicalArguments(raw)
	if err != nil {
		return p, err
	}
	// Attribution errors must not discard an otherwise valid ordinary answer.
	var fields map[string]json.RawMessage
	if json.Unmarshal([]byte(raw), &fields) == nil {
		if attribution, ok := fields["meme_use_ids"]; ok {
			if strings.TrimSpace(string(attribution)) == "null" || json.Unmarshal(attribution, &p.MemeUseIDs) != nil {
				p.MemeUseIDs, p.MemeUseIDsMalformed = nil, true
			}
			delete(fields, "meme_use_ids")
			clean, marshalErr := json.Marshal(fields)
			if marshalErr != nil {
				return p, ErrInvalid
			}
			raw = string(clean)
		}
	}
	d := json.NewDecoder(strings.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&p) != nil {
		return p, ErrInvalid
	}
	var trailing any
	if d.Decode(&trailing) != io.EOF {
		return p, ErrInvalid
	}
	return p, p.Validate()
}

// Invocation origin is only a social invitation hint; it confers no authority.
const InvocationDirect = "direct"
const InvocationAmbient = "ambient"

func IsAgentCommand(text string) bool {
	text = strings.TrimSpace(text)
	if text == "/bb" {
		return true
	}
	if !strings.HasPrefix(text, "/bb") {
		return false
	}
	for _, r := range strings.TrimPrefix(text, "/bb") {
		return unicode.IsSpace(r)
	}
	return false
}
func ConversationInvocation(m Message) string {
	var meta IngressMetadata
	_ = json.Unmarshal(m.Raw, &meta)
	if meta.Addressed || IsAgentCommand(m.Text) {
		return InvocationDirect
	}
	return InvocationAmbient
}
