package engine

import (
	"encoding/json"
	"sort"
	"strings"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/pgstore"
)

type sceneMention struct{ Key, MemberRef, Name, NameOrigin string }
type sceneReply struct {
	MessageID                     string
	SpeakerRef, SpeakerName, Role string `json:",omitempty"`
	Available                     bool
}
type sceneMessage struct {
	MessageID, SpeakerRef, SpeakerName, Role string
	OccurredAt                               time.Time
	Text                                     string
	ParentID, RootID, ThreadID               string `json:",omitempty"`
	Addressed                                bool
	Mentions                                 []sceneMention  `json:",omitempty"`
	ReplyTo                                  *sceneReply     `json:",omitempty"`
	Truncated                                bool            `json:",omitempty"`
	Media                                    json.RawMessage `json:",omitempty"`
	MediaTotal                               int             `json:",omitempty"`
	MediaHasMore                             bool            `json:",omitempty"`
	MediaNextSequence                        *int            `json:",omitempty"`

	sources []domain.SourceRef
}
type scenePresenter struct {
	people        *participantSet
	messages      map[string]domain.Message
	replies       map[string]pgstore.ConversationReply
	selfRelations map[string]bool
}

func (p *scenePresenter) message(m domain.Message) sceneMessage {
	meta := sceneMetadata(m)
	role := "member"
	if meta.SenderType == "bot" || meta.SenderType == "app" {
		role = "bot"
	}
	person := p.people.person(m.Source.AuthorID, role)
	v := sceneMessage{MessageID: m.Source.MessageID, SpeakerRef: person.MemberRef, SpeakerName: person.Name, Role: person.Role, OccurredAt: m.Source.OccurredAt.In(beijing), Text: m.Text, ParentID: m.ParentID, RootID: m.RootID, ThreadID: m.ThreadID, Addressed: meta.Addressed, sources: []domain.SourceRef{m.Source}}
	// A token is local to this message. Replacements are simultaneous so a
	// display name can never be interpreted as another mention placeholder.
	counts := map[string]int{}
	for _, mention := range meta.Mentions {
		counts[mention.Key]++
	}
	var replacements []string
	for _, mention := range meta.Mentions {
		item := sceneMention{Key: mention.Key, Name: "unknown", NameOrigin: "unknown"}
		if displayID(mention.OpenID) {
			person := p.people.person(mention.OpenID, "unknown")
			item.MemberRef, item.Name, item.NameOrigin = person.MemberRef, person.Name, person.NameOrigin
			if person.NameOrigin == "unknown" && displayName(mention.Name) != "" {
				item.Name, item.NameOrigin = displayName(mention.Name), "message_hint"
			}
		} else if hint := displayName(mention.Name); hint != "" {
			item.Name, item.NameOrigin = hint, "message_hint"
		}
		v.Mentions = append(v.Mentions, item)
		if mention.Key != "" && counts[mention.Key] == 1 {
			replacements = append(replacements, mention.Key, "@"+item.Name)
		}
	}
	// Longest tokens first prevent @_user_1 matching inside @_user_10.
	for i := 0; i < len(replacements); i += 2 {
		for j := i + 2; j < len(replacements); j += 2 {
			if len(replacements[j]) > len(replacements[i]) {
				replacements[i], replacements[j] = replacements[j], replacements[i]
				replacements[i+1], replacements[j+1] = replacements[j+1], replacements[i+1]
			}
		}
	}
	if len(replacements) > 0 {
		v.Text = expandSceneMentions(v.Text, replacements)
	}
	p.relation(&v)
	return v
}

func expandSceneMentions(text string, replacements []string) string {
	var out strings.Builder
	for offset := 0; offset < len(text); {
		matched := false
		for i := 0; i < len(replacements); i += 2 {
			key := replacements[i]
			if !strings.HasPrefix(text[offset:], key) {
				continue
			}
			end := offset + len(key)
			if end < len(text) && sceneTokenByte(text[end]) && sceneTokenByte(key[len(key)-1]) {
				continue
			}
			out.WriteString(replacements[i+1])
			offset = end
			matched = true
			break
		}
		if !matched {
			out.WriteByte(text[offset])
			offset++
		}
	}
	return out.String()
}
func sceneTokenByte(b byte) bool {
	return b == '_' || b >= '0' && b <= '9' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z'
}

func sceneMetadata(m domain.Message) domain.IngressMetadata {
	var meta domain.IngressMetadata
	if json.Unmarshal(m.Raw, &meta) != nil {
		return meta
	}
	// Older refresh envelopes kept the SDK Message but omitted its mentions
	// from the normalized projection. Only this message's explicitly typed
	// open IDs can restore local display hints; they never grant authority.
	if meta.Kind == "refresh" && len(meta.Mentions) == 0 {
		var provider struct {
			Mentions []struct {
				Key, ID, Name string
				IDType        string `json:"id_type"`
			} `json:"mentions"`
		}
		if json.Unmarshal(meta.Provider, &provider) == nil {
			for _, mention := range provider.Mentions {
				if mention.IDType == "open_id" && mention.Key != "" && displayID(mention.ID) {
					meta.Mentions = append(meta.Mentions, domain.IngressMention{Key: mention.Key, OpenID: mention.ID, Name: mention.Name})
				}
			}
		}
	}
	return meta
}
func (p *scenePresenter) reply(reply pgstore.ConversationReply) sceneMessage {
	person := p.people.person("", "assistant")
	v := sceneMessage{MessageID: reply.MessageID, SpeakerRef: person.MemberRef, SpeakerName: person.Name, Role: "assistant", OccurredAt: reply.OccurredAt.In(beijing), Text: reply.Text, ParentID: reply.ParentID, sources: append([]domain.SourceRef(nil), reply.Sources...)}
	p.relation(&v)
	return v
}
func (p *scenePresenter) relation(v *sceneMessage) {
	id := v.ParentID
	if id == "" {
		id = v.RootID
	}
	if id == "" || id == v.MessageID {
		return
	}
	v.ReplyTo = &sceneReply{MessageID: id}
	if m, ok := p.messages[id]; ok {
		role := "member"
		var meta domain.IngressMetadata
		_ = json.Unmarshal(m.Raw, &meta)
		if meta.SenderType == "bot" || meta.SenderType == "app" {
			role = "bot"
		}
		person := p.people.person(m.Source.AuthorID, role)
		v.ReplyTo.SpeakerRef, v.ReplyTo.SpeakerName, v.ReplyTo.Role, v.ReplyTo.Available = person.MemberRef, person.Name, person.Role, true
		v.sources = append(v.sources, m.Source)
	} else if _, ok := p.replies[id]; ok {
		person := p.people.person("", "assistant")
		v.ReplyTo.SpeakerRef, v.ReplyTo.SpeakerName, v.ReplyTo.Role, v.ReplyTo.Available = person.MemberRef, person.Name, person.Role, true
	} else if p.selfRelations[id] {
		// A verified physical receipt proves whom this replies to, independently
		// of whether old content/evidence is still valid. No old text is revived.
		person := p.people.person("", "assistant")
		v.ReplyTo.SpeakerRef, v.ReplyTo.SpeakerName, v.ReplyTo.Role = person.MemberRef, person.Name, person.Role
	}
}

func sceneReferences(base []domain.SourceRef, messages []sceneMessage, inFlight []pgstore.ConversationInFlight) []domain.SourceRef {
	var out []domain.SourceRef
	seen := map[domain.SourceRef]bool{}
	add := func(ref domain.SourceRef) {
		if !seen[ref] {
			seen[ref] = true
			out = append(out, ref)
		}
	}
	for _, ref := range base {
		add(ref)
	}
	for _, m := range messages {
		for _, ref := range m.sources {
			add(ref)
		}
	}
	for _, work := range inFlight {
		for _, ref := range work.Sources {
			add(ref)
		}
	}
	return out
}
func sortScene(messages []sceneMessage) {
	sort.Slice(messages, func(i, j int) bool {
		if messages[i].OccurredAt.Equal(messages[j].OccurredAt) {
			return messages[i].MessageID < messages[j].MessageID
		}
		return messages[i].OccurredAt.Before(messages[j].OccurredAt)
	})
}

// Attention's source proofs stay private just like message proofs.
type attentionView struct {
	AsOf, WindowStart                                                           time.Time
	OwnNotifications, OwnReplyTurns, OwnReactions, HumanMessages, HumanSpeakers int
	NotificationsComplete                                                       bool
	CurrentRepliesToSelf, CurrentRepliesToMember                                bool
	HumanMessagesAfterCurrent                                                   int
	InFlight                                                                    []inFlightView
	InFlightTruncated                                                           bool
}
type inFlightView struct {
	sceneMessage
	Phase string
}

func (p *scenePresenter) attention(a attentionState) attentionView {
	v := attentionView{AsOf: a.AsOf.In(beijing), WindowStart: a.WindowStart.In(beijing), OwnNotifications: a.OwnNotifications, OwnReplyTurns: a.OwnReplyTurns, OwnReactions: a.OwnReactions, HumanMessages: a.HumanMessages, HumanSpeakers: a.HumanSpeakers, NotificationsComplete: a.NotificationsComplete, CurrentRepliesToSelf: a.CurrentRepliesToSelf, CurrentRepliesToMember: a.CurrentRepliesToMember, HumanMessagesAfterCurrent: a.HumanMessagesAfterCurrent, InFlightTruncated: a.InFlightTruncated, InFlight: []inFlightView{}}
	for _, work := range a.InFlight {
		m, ok := p.messages[work.Root.MessageID]
		if !ok {
			m = domain.Message{Source: work.Root, Text: work.Request}
		}
		item := p.message(m)
		v.InFlight = append(v.InFlight, inFlightView{sceneMessage: item, Phase: work.Phase})
	}
	return v
}
