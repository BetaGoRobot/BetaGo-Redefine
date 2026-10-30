package engine

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/pgstore"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/retrieval"
)

const toolMaterialLimit = 64

type evidenceView struct {
	ID, Channel      string
	DerivedText      string `json:",omitempty"`
	SourceMessageIDs []string
	Truncated        bool
}
type toolReadingView struct {
	Participants    []participant
	MemberDirectory memberDirectory
	SourceMessages  []sceneMessage
	OwnReplies      []sceneMessage             `json:",omitempty"`
	Available       *bool                      `json:"available,omitempty"`
	Hint            string                     `json:"hint,omitempty"`
	Data            map[string]json.RawMessage `json:"data,omitempty"`
	Evidence        []evidenceView
	Degraded        bool
	Failures        []retrieval.Failure `json:",omitempty"`
	RecentWindow    int                 `json:",omitempty"`
	Truncated       bool
}

// Raw results have already been source-bound and durably receipted before
// this function is called. The view grants no new authority and never changes
// the ledger. Canonical originals are re-read only with exact pinned versions.
func (e *Engine) presentTool(ctx context.Context, scope domain.Scope, name, raw string) (string, error) {
	switch name {
	case "search_literal", "search_discussion", "search_memory":
	case "feishu_read_message", "feishu_message_history", "feishu_message_context":
		return e.presentCommunityMessages(ctx, scope, name, raw)
	default:
		return raw, nil
	}
	var result retrieval.Result
	if json.Unmarshal([]byte(raw), &result) != nil {
		return "", domain.ErrInvalid
	}
	snapshot, err := e.memberSnapshot(ctx, scope)
	if err != nil {
		return "", err
	}
	presenter := &scenePresenter{people: newParticipantSet(scope, e.c.BotOpenID, snapshot), messages: map[string]domain.Message{}, replies: map[string]pgstore.ConversationReply{}}
	view := toolReadingView{MemberDirectory: snapshot.status, SourceMessages: []sceneMessage{}, Evidence: []evidenceView{}, Degraded: result.Degraded, Failures: result.Failures, RecentWindow: result.RecentWindow}
	var originals []domain.Message
	seen := map[domain.SourceRef]bool{}
	for _, evidence := range result.Evidence {
		item := evidenceView{ID: evidence.ID, Channel: evidence.Channel, DerivedText: evidence.DerivedText, SourceMessageIDs: []string{}}
		ids := map[string]bool{}
		for _, ref := range evidence.Sources {
			if ref.Validate() != nil || ref.Scope != scope {
				return "", domain.ErrStaleSource
			}
			if !ids[ref.MessageID] {
				item.SourceMessageIDs = append(item.SourceMessageIDs, ref.MessageID)
				ids[ref.MessageID] = true
			}
			if seen[ref] {
				continue
			}
			seen[ref] = true
			if len(originals) == toolMaterialLimit {
				view.Truncated = true
				continue
			}
			m, err := e.c.Store.Message(ctx, scope, ref.MessageID)
			if errors.Is(err, domain.ErrNotFound) {
				return "", domain.ErrStaleSource
			}
			if err != nil {
				return "", err
			}
			if m.Source != ref || m.Deleted {
				return "", domain.ErrStaleSource
			}
			originals = append(originals, m)
			presenter.messages[ref.MessageID] = m
		}
		if len(view.Evidence) < toolMaterialLimit {
			view.Evidence = append(view.Evidence, item)
		} else {
			view.Truncated = true
		}
	}
	for _, m := range originals {
		item := presenter.message(m)
		if len(item.Text) > 8<<10 {
			item.Text = truncateUTF8(item.Text, 8<<10)
			item.Truncated = true
			view.Truncated = true
		}
		view.SourceMessages = append(view.SourceMessages, item)
	}
	for i := range view.Evidence {
		if len(view.Evidence[i].DerivedText) > 8<<10 {
			view.Evidence[i].DerivedText = truncateUTF8(view.Evidence[i].DerivedText, 8<<10)
			view.Evidence[i].Truncated = true
			view.Truncated = true
		}
	}
	return e.fitToolReadingView(ctx, presenter, view)
}

func (e *Engine) fitToolReadingView(ctx context.Context, presenter *scenePresenter, view toolReadingView) (string, error) {
	limit := min(64<<10, e.c.MaxContextBytes/4)
	for {
		available := map[string]bool{}
		for _, m := range view.SourceMessages {
			available[m.MessageID] = true
		}
		for i := range view.Evidence {
			for _, id := range view.Evidence[i].SourceMessageIDs {
				if !available[id] {
					view.Evidence[i].Truncated = true
					view.Truncated = true
				}
			}
		}
		messages := append(append([]sceneMessage{}, view.SourceMessages...), view.OwnReplies...)
		view.Participants = presenter.people.selected(messages)
		b, err := json.Marshal(view)
		if err != nil {
			return "", err
		}
		if len(b) <= limit {
			if err := ctx.Err(); err != nil {
				return "", err
			}
			return string(b), nil
		}
		view.Truncated = true
		// Preserve each retained message's speaker, mentions and reply relation
		// while shortening text; if metadata alone is too large, omit a whole
		// background material rather than inventing an incomplete identity.
		largest, sourceIndex, derivedIndex, ownIndex := 256, -1, -1, -1
		for i, m := range view.SourceMessages {
			if len(m.Text) > largest {
				largest, sourceIndex, derivedIndex, ownIndex = len(m.Text), i, -1, -1
			}
		}
		for i, item := range view.Evidence {
			if len(item.DerivedText) > largest {
				largest, sourceIndex, derivedIndex, ownIndex = len(item.DerivedText), -1, i, -1
			}
		}
		for i, m := range view.OwnReplies {
			if len(m.Text) > largest {
				largest, sourceIndex, derivedIndex, ownIndex = len(m.Text), -1, -1, i
			}
		}
		if ownIndex >= 0 {
			item := &view.OwnReplies[ownIndex]
			item.Text = truncateUTF8(item.Text, largest/2)
			item.Truncated = true
			continue
		}
		if sourceIndex >= 0 {
			item := &view.SourceMessages[sourceIndex]
			item.Text = truncateUTF8(item.Text, largest/2)
			item.Truncated = true
			continue
		}
		if derivedIndex >= 0 {
			item := &view.Evidence[derivedIndex]
			item.DerivedText = truncateUTF8(item.DerivedText, largest/2)
			item.Truncated = true
			continue
		}
		if len(view.OwnReplies) > 0 {
			view.OwnReplies = view.OwnReplies[:len(view.OwnReplies)-1]
			continue
		}
		if len(view.SourceMessages) > 0 {
			view.SourceMessages = view.SourceMessages[:len(view.SourceMessages)-1]
			continue
		}
		if len(view.Evidence) > 0 {
			view.Evidence = view.Evidence[:len(view.Evidence)-1]
			continue
		}
		return "", domain.ErrInvalid
	}
}

// Only the three conversation-read shapes are adapted here. Pagination and
// coverage remain the atom's facts; private SourceRefs are retained in its raw
// receipt, while message text is shared with the same speaker/mention view.
func (e *Engine) presentCommunityMessages(ctx context.Context, scope domain.Scope, name, raw string) (string, error) {
	var envelope struct {
		Available *bool                      `json:"available"`
		Truncated bool                       `json:"truncated"`
		Hint      string                     `json:"hint"`
		Data      map[string]json.RawMessage `json:"data"`
		Sources   []domain.SourceRef         `json:"source_refs"`
	}
	if json.Unmarshal([]byte(raw), &envelope) != nil {
		return "", domain.ErrInvalid
	}
	if envelope.Available != nil && !*envelope.Available {
		return raw, nil
	}
	type originalView struct {
		Source            domain.SourceRef `json:"source"`
		Media             json.RawMessage  `json:"media"`
		MediaTotal        int              `json:"media_total"`
		MediaHasMore      bool             `json:"media_has_more"`
		MediaNextSequence *int             `json:"media_next_sequence"`
		Truncated         bool             `json:"truncated"`
	}
	var originals []originalView
	var replies []struct {
		pgstore.ConversationReply
		Truncated bool `json:"truncated"`
	}
	if name == "feishu_read_message" {
		var original originalView
		if json.Unmarshal([]byte(raw), &original) != nil {
			return "", domain.ErrInvalid
		}
		originals = append(originals, original)
		if json.Unmarshal([]byte(raw), &envelope.Data) != nil {
			return "", domain.ErrInvalid
		}
		for _, key := range []string{"source", "text", "parent_id", "root_id", "thread_id", "updated_at", "truncated", "media", "media_total", "media_has_more", "media_next_sequence"} {
			delete(envelope.Data, key)
		}
		envelope.Truncated = original.Truncated
		envelope.Sources = []domain.SourceRef{original.Source}
	} else {
		if item, exists := envelope.Data["messages"]; exists {
			if json.Unmarshal(item, &originals) != nil {
				return "", domain.ErrInvalid
			}
			delete(envelope.Data, "messages")
		}
		if item, exists := envelope.Data["own_replies"]; exists {
			if json.Unmarshal(item, &replies) != nil {
				return "", domain.ErrInvalid
			}
			delete(envelope.Data, "own_replies")
		}
	}
	snapshot, err := e.memberSnapshot(ctx, scope)
	if err != nil {
		return "", err
	}
	presenter := &scenePresenter{people: newParticipantSet(scope, e.c.BotOpenID, snapshot), messages: map[string]domain.Message{}, replies: map[string]pgstore.ConversationReply{}}
	view := toolReadingView{MemberDirectory: snapshot.status, SourceMessages: []sceneMessage{}, Evidence: []evidenceView{}, Available: envelope.Available, Hint: envelope.Hint, Data: envelope.Data, Truncated: envelope.Truncated}
	pinned := map[domain.SourceRef]bool{}
	for _, ref := range envelope.Sources {
		if ref.Validate() != nil || ref.Scope != scope {
			return "", domain.ErrStaleSource
		}
		pinned[ref] = true
	}
	selected := []domain.Message{}
	seen := map[domain.SourceRef]bool{}
	for _, original := range originals {
		ref := original.Source
		if !pinned[ref] {
			return "", domain.ErrStaleSource
		}
		if seen[ref] {
			continue
		}
		seen[ref] = true
		if len(selected) == toolMaterialLimit {
			view.Truncated = true
			continue
		}
		m, err := e.c.Store.Message(ctx, scope, ref.MessageID)
		if errors.Is(err, domain.ErrNotFound) {
			return "", domain.ErrStaleSource
		}
		if err != nil {
			return "", err
		}
		if m.Source != ref || m.Deleted {
			return "", domain.ErrStaleSource
		}
		selected = append(selected, m)
		presenter.messages[ref.MessageID] = m
		view.Truncated = view.Truncated || original.Truncated
	}
	for _, reply := range replies {
		for _, ref := range reply.Sources {
			if !pinned[ref] {
				return "", domain.ErrStaleSource
			}
		}
		presenter.replies[reply.MessageID] = reply.ConversationReply
	}
	for _, m := range selected {
		item := presenter.message(m)
		for _, original := range originals {
			if original.Source == m.Source {
				item.Media, item.MediaTotal, item.MediaHasMore, item.MediaNextSequence = original.Media, original.MediaTotal, original.MediaHasMore, original.MediaNextSequence
				break
			}
		}

		if len(item.Text) > 8<<10 {
			item.Text = truncateUTF8(item.Text, 8<<10)
			item.Truncated = true
			view.Truncated = true
		}
		view.SourceMessages = append(view.SourceMessages, item)
	}
	for _, reply := range replies {
		if len(view.SourceMessages)+len(view.OwnReplies) == toolMaterialLimit {
			view.Truncated = true
			break
		}
		item := presenter.reply(reply.ConversationReply)
		item.Truncated = reply.Truncated
		if len(item.Text) > 8<<10 {
			item.Text = truncateUTF8(item.Text, 8<<10)
			item.Truncated = true
		}
		view.Truncated = view.Truncated || item.Truncated
		view.OwnReplies = append(view.OwnReplies, item)
	}
	return e.fitToolReadingView(ctx, presenter, view)
}
