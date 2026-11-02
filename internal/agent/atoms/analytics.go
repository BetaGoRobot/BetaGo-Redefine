package atoms

import (
	"context"
	"encoding/json"
	"time"
	"unicode/utf8"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
)

func trimUTF8(text string, max int) string {
	if len(text) <= max {
		return text
	}
	text = text[:max]
	for !utf8.ValidString(text) {
		text = text[:len(text)-1]
	}
	return text
}

func analyticsDefinitions(o Options) []Definition {
	if o.Tasks == nil {
		return nil
	}
	read := func(name, desc string, parameters json.RawMessage, run func(context.Context, Invocation, string) (any, []domain.SourceRef, error)) Definition {
		return Definition{Name: name, Description: desc, Parameters: parameters, sourceRefs: communitySources, Run: func(ctx context.Context, in Invocation, args string) (string, error) {
			data, refs, err := run(ctx, in, args)
			if err != nil {
				return communityFailure(ctx, err)
			}
			raw, err := communityRead(ctx, o, in.Task.Scope, data, refs)
			if err != nil {
				return "", err
			}
			return guardCommunityRead(ctx, o, in, raw, communitySources)
		}}
	}
	statistics := read("group_message_statistics", "Count the entire locally captured current-message window [start,end), max 31 days. Exact total and author counts; explicitly bounded word frequencies use lowercase Unicode words and Han bigrams, never imply complete Feishu history or linguistic segmentation. Returned word sample completeness is separate from exact counts.", json.RawMessage(`{"type":"object","properties":{"start":{"type":"string","format":"date-time"},"end":{"type":"string","format":"date-time"},"author_id":{"type":"string","maxLength":1024},"limit":{"type":"integer","minimum":1,"maximum":50}},"required":["start","end"],"additionalProperties":false}`), func(ctx context.Context, in Invocation, args string) (any, []domain.SourceRef, error) {
		var a struct {
			Start    time.Time `json:"start"`
			End      time.Time `json:"end"`
			AuthorID string    `json:"author_id"`
			Limit    int       `json:"limit"`
		}
		if json.Unmarshal([]byte(args), &a) != nil {
			return nil, nil, domain.ErrInvalid
		}
		if a.Limit == 0 {
			a.Limit = 20
		}
		out, err := o.Tasks.MessageStatistics(ctx, domain.AnalyticsQuery{Scope: in.Task.Scope, Start: a.Start, End: a.End, Anchor: time.Now().UTC(), AuthorID: a.AuthorID, Limit: a.Limit})
		return out, out.Sources, err
	})
	list := read("discussion_list", "List source-verified discussions from the new local context index with stable IDs, versions and bounded keyset pagination. These are derived discussions, not complete original history; stale/deleted sources are excluded.", json.RawMessage(`{"type":"object","properties":{"after_id":{"type":"string","maxLength":1024},"limit":{"type":"integer","minimum":1,"maximum":20}},"additionalProperties":false}`), func(ctx context.Context, in Invocation, args string) (any, []domain.SourceRef, error) {
		var a struct {
			After string `json:"after_id"`
			Limit int    `json:"limit"`
		}
		if json.Unmarshal([]byte(args), &a) != nil {
			return nil, nil, domain.ErrInvalid
		}
		if a.Limit == 0 {
			a.Limit = 10
		}
		rows, scanned, more, err := o.Tasks.ListDiscussions(ctx, in.Task.Scope, time.Now().UTC(), a.After, a.Limit)
		if err != nil {
			return nil, nil, err
		}
		refs := []domain.SourceRef{}
		items := []any{}
		next := a.After
		complete := true
		budget, err := communitySourcesBudget(ctx, o, in)
		if err != nil {
			return nil, nil, err
		}
		for _, c := range rows {
			text := c.Text
			truncated := len(text) > 4096
			if truncated {
				text = trimUTF8(text, 4096)
			}
			merged := dedupCommunityRefs(append(append([]domain.SourceRef(nil), refs...), c.Sources...))
			_, sourceFits := budget.with(merged)
			preview := map[string]any{"id": c.ID, "version": c.Version, "text": text, "text_truncated": truncated, "updated_at": c.UpdatedAt}
			proposal := append(append([]any(nil), items...), preview)
			fits := sourceFits && communityFits(map[string]any{"items": proposal, "has_more": more, "next_after_id": c.ID}, merged)
			if !fits && len(items) > 0 {
				more = true
				complete = false
				break
			}
			if !fits {
				// A single large proof must never be truncated into false evidence.
				// Advance the stable cursor with metadata and an explicit omitted preview.
				preview = map[string]any{"id": c.ID, "version": c.Version, "preview_omitted": true, "reason": "evidence_budget_exceeded", "source_count": len(c.Sources)}
				items = append(items, preview)
			} else {
				items = proposal
				refs = merged
			}
			next = c.ID
		}
		if complete {
			next = scanned
		}
		return map[string]any{"items": items, "has_more": more, "next_after_id": next}, refs, nil
	})
	detail := read("discussion_get", "Read a current discussion by its stable ID. Revalidates every original source and returns its full dependency set; stale derivations are rejected.", json.RawMessage(`{"type":"object","properties":{"id":{"type":"string","minLength":1,"maxLength":1024}},"required":["id"],"additionalProperties":false}`), func(ctx context.Context, in Invocation, args string) (any, []domain.SourceRef, error) {
		var a struct {
			ID string `json:"id"`
		}
		if json.Unmarshal([]byte(args), &a) != nil {
			return nil, nil, domain.ErrInvalid
		}
		c, err := o.Tasks.Context(ctx, in.Task.Scope, a.ID)
		if err != nil {
			return nil, nil, err
		}
		if c.Scope != in.Task.Scope || c.Invalidated {
			return nil, nil, domain.ErrStaleSource
		}
		valid, err := o.Tasks.ValidSources(ctx, domain.Query{Scope: in.Task.Scope, Anchor: time.Now().UTC(), Mode: domain.QueryDiscussion, Limit: 1}, c.Sources)
		if err != nil {
			return nil, nil, err
		}
		if !valid {
			return nil, nil, domain.ErrStaleSource
		}
		if len(c.Sources) > 128 {
			return map[string]any{"id": c.ID, "preview_omitted": true, "reason": "evidence_budget_exceeded", "source_count": len(c.Sources)}, nil, nil
		}
		text := trimUTF8(c.Text, 24<<10)
		view := map[string]any{"id": c.ID, "version": c.Version, "text": text, "text_truncated": len(text) < len(c.Text), "updated_at": c.UpdatedAt}
		for !communityFits(view, c.Sources) && len(text) > 0 {
			text = trimUTF8(text, len(text)/2)
			view["text"] = text
			view["text_truncated"] = true
		}
		if !communityFits(view, c.Sources) {
			return map[string]any{"id": c.ID, "preview_omitted": true, "reason": "evidence_budget_exceeded", "source_count": len(c.Sources)}, nil, nil
		}
		return view, c.Sources, nil
	})
	return []Definition{statistics, list, detail}
}
